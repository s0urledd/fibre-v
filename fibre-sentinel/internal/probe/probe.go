package probe

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"runtime"
	"strings"
	"time"

	celfibre "github.com/celestiaorg/celestia-app/v10/fibre"
	"github.com/celestiaorg/celestia-app/v10/pkg/rsema1d"
	"github.com/celestiaorg/celestia-app/v10/pkg/rsema1d/field"
	"github.com/celestiaorg/celestia-app/v10/pkg/rsema1d/rlc"
	fibretypes "github.com/celestiaorg/celestia-app/v10/x/fibre/types"
	assign "github.com/plsgiveup/fibre/fibre-assign"
	tlsverify "github.com/plsgiveup/fibre/fibre-tlsverify"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"
)

// StepTimeouts bounds every layer of a probe. Nothing in a probe blocks longer
// than the relevant field.
type StepTimeouts struct {
	DNS      time.Duration
	TCP      time.Duration
	TLS      time.Duration
	Identity time.Duration
	// Download is the base download deadline. The effective deadline is
	// Download + ExpectedShardBytes / MinDownloadBytesPerSec, so a 122 MB
	// shard is not judged by the deadline chosen for a 1 MB one.
	Download time.Duration
	// MinDownloadBytesPerSec is the slowest transfer the observer is willing
	// to wait for before recording RPC_DEADLINE (default 1 MiB/s).
	MinDownloadBytesPerSec int64
}

// DefaultStepTimeouts are conservative for a WAN vantage.
func DefaultStepTimeouts() StepTimeouts {
	return StepTimeouts{
		DNS:      5 * time.Second,
		TCP:      5 * time.Second,
		TLS:      10 * time.Second,
		Identity: 2 * time.Second,
		Download: 25 * time.Second,

		MinDownloadBytesPerSec: 1 << 20,
	}
}

func (t StepTimeouts) withDefaults() StepTimeouts {
	d := DefaultStepTimeouts()
	if t.DNS <= 0 {
		t.DNS = d.DNS
	}
	if t.TCP <= 0 {
		t.TCP = d.TCP
	}
	if t.TLS <= 0 {
		t.TLS = d.TLS
	}
	if t.Identity <= 0 {
		t.Identity = d.Identity
	}
	if t.Download <= 0 {
		t.Download = d.Download
	}
	if t.MinDownloadBytesPerSec <= 0 {
		t.MinDownloadBytesPerSec = d.MinDownloadBytesPerSec
	}
	return t
}

// downloadDeadline is the base deadline plus the time a transfer of
// expectedBytes takes at the slowest acceptable rate.
func (t StepTimeouts) downloadDeadline(expectedBytes int64) time.Duration {
	d := t.Download
	if expectedBytes > 0 && t.MinDownloadBytesPerSec > 0 {
		d += time.Duration(expectedBytes/t.MinDownloadBytesPerSec) * time.Second
	}
	return d
}

// Coder wraps the rsema1d coder used to verify returned rows against the
// commitment. Build one per (OriginalRows, TotalRows) and reuse it.
type Coder struct {
	c            *rsema1d.Coder
	originalRows int
}

// NewCoder builds a verifier for a blob-v0 K/N split.
func NewCoder(originalRows, totalRows int) (*Coder, error) {
	c, err := rsema1d.NewCoder(&rsema1d.Config{
		K:           originalRows,
		N:           totalRows - originalRows,
		WorkerCount: runtime.GOMAXPROCS(0),
	})
	if err != nil {
		return nil, err
	}
	return &Coder{c: c, originalRows: originalRows}, nil
}

// Input is everything one probe needs about the publication + target.
type Input struct {
	Vantage            string
	ChainID            string
	PromiseHash        string
	Commitment         [32]byte
	CommitmentHex      string
	BlobVersion        uint32
	MustServeUntil     time.Time
	ValidatorSetHeight int64

	Target Target

	SchedulePoint  SchedulePoint
	PruneTolerance time.Duration // grace/post boundary; phase is computed from the actual start time

	// ExpectedShardBytes is the estimated wire size of this validator's shard
	// (ShardBytes); it scales the download deadline. 0 = base deadline only.
	ExpectedShardBytes int64

	// ClockOffsetMS is the observer's wall clock minus the chain's latest
	// block time, in milliseconds, as measured by the caller. Every phase
	// decision is made against the local clock, so the offset is recorded
	// with the probe: a reader can discount a vantage whose clock drifted.
	ClockOffsetMS int64

	// SkipDownload stops after the identity step (L1-L3 only). Used by the
	// reachability heartbeat and by the probe policy's backoff, where the
	// expensive DownloadShard would only repeat a transport failure.
	SkipDownload bool
}

// Run executes one layered probe and returns a fully-populated Measurement.
// It never returns an error — a probe that cannot run records
// OutcomeProbeError. The named return lets the deferred finaliser stamp
// FinishedAt / TotalDurationMS / Classification after every early return.
func Run(ctx context.Context, in Input, coder *Coder, to StepTimeouts) (m Measurement) {
	to = to.withDefaults()
	now := time.Now().UTC()
	phase := PhaseAtWindow(now, in.MustServeUntil, in.PruneTolerance)
	m = Measurement{
		SchemaVersion:      MeasurementSchemaVersion,
		Vantage:            in.Vantage,
		PromiseHash:        in.PromiseHash,
		Commitment:         in.CommitmentHex,
		BlobVersion:        in.BlobVersion,
		MustServeUntil:     in.MustServeUntil,
		ValidatorSetHeight: in.ValidatorSetHeight,
		ValidatorAddress:   in.Target.AddressHex,
		ValidatorHost:      in.Target.Host,
		Assigned:           in.Target.Assigned,
		AssignedRowCount:   in.Target.RowCount,
		ScheduleLabel:      in.SchedulePoint.Label,
		ScheduledAt:        in.SchedulePoint.At.UTC(),
		StartedAt:          now,
		Phase:              phase,
		ClockOffsetMS:      in.ClockOffsetMS,
		LatenessMS:         now.Sub(in.SchedulePoint.At).Milliseconds(),
	}
	defer func() {
		m.FinishedAt = time.Now().UTC()
		m.TotalDurationMS = m.FinishedAt.Sub(m.StartedAt).Milliseconds()
		m.Classification, m.ClassificationReason = Classify(m.Assigned, m.Phase, m.Outcome)
	}()

	if !in.SkipDownload && coder == nil {
		// A download needs the rsema1d coder for this (K, N); without it the
		// returned rows could not be verified, so there is nothing to judge
		// and no reason to open a connection.
		m.Outcome = OutcomeProbeError
		m.RawError = "no coder for this blob version (originalRows/totalRows); cannot verify rows"
		return m
	}
	if in.Target.Host == "" {
		// A validator with no x/valaddr registration cannot be reached by
		// anyone; that is a fact about the validator, not about the probe.
		m.Outcome = OutcomeNoHost
		m.RawError = "validator has no registered fibre host (x/valaddr)"
		return m
	}
	if len(in.Target.PubKey) != ed25519.PublicKeySize {
		m.Outcome = OutcomeProbeError
		m.RawError = "no consensus public key for validator"
		return m
	}
	host, port, err := net.SplitHostPort(in.Target.Host)
	if err != nil {
		m.Outcome = OutcomeProbeError
		m.RawError = fmt.Sprintf("bad host %q: %v", in.Target.Host, err)
		return m
	}

	// ---- L1: DNS ----
	var addrs []string
	if pip := net.ParseIP(host); pip != nil {
		m.DNS = StepResult{Attempted: false, OK: true, Detail: "literal IP " + host}
		addrs = []string{host}
	} else {
		t0 := time.Now()
		dctx, cancel := context.WithTimeout(ctx, to.DNS)
		got, derr := net.DefaultResolver.LookupHost(dctx, host)
		cancel()
		m.DNS = StepResult{Attempted: true, OK: derr == nil, DurationMS: sinceMS(t0)}
		if derr != nil {
			m.DNS.Error = derr.Error()
			m.Outcome = OutcomeDNSFail
			m.RawError = derr.Error()
			return m
		}
		addrs = orderAddrs(got)
		m.DNS.Detail = strings.Join(addrs, ",")
	}

	// ---- L2: TCP ----
	// Every resolved address is tried in turn (IPv4 first), like a real
	// client's happy-eyeballs would; the first that connects is the endpoint
	// every later layer talks to. A vantage without IPv6 must not turn a
	// dual-stack validator into a FAULT.
	t0 := time.Now()
	var rawConn net.Conn
	var terr error
	var ip string
	var attempts []string
	for _, cand := range addrs {
		d := net.Dialer{Timeout: to.TCP}
		c, err := d.DialContext(ctx, "tcp", net.JoinHostPort(cand, port))
		if err == nil {
			rawConn, ip = c, cand
			break
		}
		attempts = append(attempts, cand+": "+err.Error())
		if terr == nil || !isNoRoute(err) {
			terr = err // keep the most informative failure
		}
		if ctx.Err() != nil {
			break
		}
	}
	m.TCP = StepResult{Attempted: true, OK: rawConn != nil, DurationMS: sinceMS(t0)}
	if rawConn == nil {
		if terr == nil {
			terr = errors.New("no address to dial")
		}
		m.TCP.Error = strings.Join(attempts, "; ")
		m.Outcome = classifyDialError(terr)
		m.RawError = terr.Error()
		return m
	}
	if len(attempts) > 0 {
		m.TCP.Detail = "failed " + strings.Join(attempts, "; ") + "; "
	}
	m.TCP.Detail += "-> " + rawConn.RemoteAddr().String()

	// ---- L3a: TLS handshake (no verification here; identity is its own step) ----
	t0 = time.Now()
	rawCfg := &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS13} //nolint:gosec // identity checked separately below
	tlsConn := tls.Client(rawConn, rawCfg)
	hctx, cancel := context.WithTimeout(ctx, to.TLS)
	herr := tlsConn.HandshakeContext(hctx)
	cancel()
	m.TLS = TLSResult{Attempted: true, OK: herr == nil, DurationMS: sinceMS(t0)}
	if herr != nil {
		m.TLS.Error = herr.Error()
		_ = tlsConn.Close()
		m.Outcome = OutcomeTLSFail
		m.RawError = herr.Error()
		return m
	}
	st := tlsConn.ConnectionState()
	m.TLS.Version = tlsVersionString(st.Version)
	m.TLS.CipherSuite = tls.CipherSuiteName(st.CipherSuite)
	var peerCert *x509.Certificate
	if len(st.PeerCertificates) > 0 {
		peerCert = st.PeerCertificates[0]
	}
	if peerCert != nil {
		sum := sha256.Sum256(peerCert.Raw)
		m.TLS.PeerCertSHA256 = hex.EncodeToString(sum[:])
		m.TLS.PeerCertNotAfter = peerCert.NotAfter.UTC().Format(time.RFC3339)
	}
	_ = tlsConn.Close()

	// ---- L3b: identity (consensus-key binding) ----
	t0 = time.Now()
	m.Identity = IdentityResult{Attempted: true}
	if peerCert == nil {
		m.Identity.Error = "peer presented no certificate"
		m.Identity.DurationMS = sinceMS(t0)
		m.Outcome = OutcomeIdentityFail
		m.RawError = m.Identity.Error
		return m
	}
	if claimed, ierr := tlsverify.Inspect(peerCert); ierr == nil {
		m.Identity.ClaimedNotBefore = claimed.NotBefore.UTC().Format(time.RFC3339)
		m.Identity.ClaimedNotAfter = claimed.NotAfter.UTC().Format(time.RFC3339)
	}
	// The identity check is pure computation on the peer cert; the timeout
	// bounds it anyway so a pathological certificate cannot stall a probe.
	verr := verifyWithin(peerCert, in.Target.PubKey, in.ChainID, to.Identity)
	m.Identity.DurationMS = sinceMS(t0)
	m.Identity.OK = verr == nil
	if verr != nil {
		if reason, ok := tlsverify.ReasonOf(verr); ok {
			m.Identity.Reason = string(reason)
		}
		m.Identity.Error = verr.Error()
		m.Outcome = OutcomeIdentityFail
		m.RawError = verr.Error()
		return m
	}

	if in.SkipDownload {
		m.Outcome = OutcomeReachable
		return m
	}

	// ---- L4: retrievability (fresh dial with the verifying TLS config) ----
	dl := downloadAndVerify(ctx, in, coder, net.JoinHostPort(ip, port), to.downloadDeadline(in.ExpectedShardBytes))
	m.Download = dl.DownloadResult
	m.Outcome = dl.outcome
	if dl.rawErr != "" {
		m.RawError = dl.rawErr
	}
	return m
}

// dlResult carries the raw outcome + error out of downloadAndVerify without
// leaking them into the JSON schema.
type dlResult struct {
	DownloadResult
	outcome Outcome
	rawErr  string
}

// maxRecvMsgSize matches the reference client's receive bound
// (fibre/internal/grpc/fibre_client.go: MaxCallRecvMsgSize(maxMsgSize) with
// maxMsgSize = ProtocolParams.MaxMessageSize()). grpc-go's default is 4 MiB,
// which would turn every shard larger than that into a spurious failure.
var maxRecvMsgSize = celfibre.DefaultProtocolParams.MaxMessageSize()

// downloadAndVerify runs the L4 step against endpoint, the ip:port literal
// that L2/L3 already verified, so all layers judge the same address.
func downloadAndVerify(ctx context.Context, in Input, coder *Coder, endpoint string, timeout time.Duration) dlResult {
	r := dlResult{DownloadResult: DownloadResult{Attempted: true, RowsExpected: in.Target.RowCount}}
	t0 := time.Now()

	tlsCfg := tlsverify.ClientTLSConfig(in.Target.PubKey, in.ChainID)
	conn, err := grpc.NewClient("passthrough:///"+endpoint,
		grpc.WithTransportCredentials(credentials.NewTLS(tlsCfg)),
		grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(maxRecvMsgSize)),
	)
	if err != nil {
		r.DurationMS = sinceMS(t0)
		r.Error = err.Error()
		r.outcome, r.rawErr = OutcomeRPCError, err.Error()
		return r
	}
	defer conn.Close()

	dctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	blobID := celfibre.NewBlobID(uint8(in.BlobVersion), celfibre.Commitment(in.Commitment))
	resp, err := fibretypes.NewFibreClient(conn).DownloadShard(dctx, &fibretypes.DownloadShardRequest{BlobId: blobID})
	r.DurationMS = sinceMS(t0)
	if err != nil {
		r.Error = err.Error()
		r.outcome, r.rawErr = classifyDownloadError(err), err.Error()
		return r
	}

	proofs, rlcv, perr := parseShard(resp.Shard, coder.originalRows)
	if perr != nil {
		r.Error = "parse: " + perr.Error()
		r.outcome, r.rawErr = OutcomeInvalidRows, perr.Error()
		return r
	}
	r.RowsReturned = len(proofs)

	rec, rerr := coder.c.NewReconstructor(rsema1d.Commitment(in.Commitment))
	if rerr != nil {
		r.Error = "reconstructor: " + rerr.Error()
		r.outcome, r.rawErr = OutcomeProbeError, rerr.Error()
		return r
	}
	if _, aerr := rec.Add(proofs, rlcv); aerr != nil {
		r.Error = "commitment verify: " + aerr.Error()
		r.outcome, r.rawErr = OutcomeInvalidRows, aerr.Error()
		return r
	}
	r.CommitmentVerified = true

	idx := make([]uint32, len(proofs))
	for i, p := range proofs {
		idx[i] = uint32(p.Index)
	}
	sm := assign.ShardMap{in.Target.Address: in.Target.AssignedRows}
	if verr := sm.Verify(in.Target.Address, idx); verr != nil {
		r.Error = "assignment verify: " + verr.Error()
		if len(proofs) < in.Target.RowCount {
			r.outcome, r.rawErr = OutcomePartial, verr.Error()
		} else {
			r.outcome, r.rawErr = OutcomeWrongRows, verr.Error()
		}
		return r
	}
	r.AssignmentVerified = true
	r.OK = true
	r.outcome = OutcomeServedOK
	return r
}

// parseShard mirrors fibre.parseShard (unexported).
func parseShard(shard *fibretypes.BlobShard, originalRows int) ([]*rsema1d.RowProof, rlc.Vector, error) {
	if shard == nil {
		return nil, nil, errors.New("nil shard")
	}
	rows := shard.GetRows()
	if len(rows) == 0 {
		return nil, nil, errors.New("no rows")
	}
	proofs := make([]*rsema1d.RowProof, len(rows))
	for i, rw := range rows {
		if rw == nil {
			return nil, nil, fmt.Errorf("nil row %d", i)
		}
		proofs[i] = &rsema1d.RowProof{Index: int(rw.Index), Row: rw.Data, RowProof: rw.Proof}
	}
	want := originalRows * field.GF128Size
	if len(shard.GetRlcs()) != want {
		return nil, nil, fmt.Errorf("rlc bytes %d != %d", len(shard.GetRlcs()), want)
	}
	v, err := rlc.Unmarshal(shard.GetRlcs())
	if err != nil {
		return nil, nil, err
	}
	return proofs, v, nil
}

func classifyDialError(err error) Outcome {
	s := strings.ToLower(err.Error())
	switch {
	case strings.Contains(s, "refused"):
		return OutcomeTCPRefused
	case strings.Contains(s, "timeout") || strings.Contains(s, "deadline exceeded") || strings.Contains(s, "i/o timeout"):
		return OutcomeTCPTimeout
	case strings.Contains(s, "no route to host") || strings.Contains(s, "unreachable") || strings.Contains(s, "no such host"):
		return OutcomeTCPUnreachable
	default:
		return OutcomeTCPUnreachable
	}
}

// classifyDownloadError maps an L4 error to an outcome by its gRPC status
// code first, so that "the observer gave up" (deadline, our own limits) is
// never recorded as a verdict about the validator.
func classifyDownloadError(err error) Outcome {
	if err == nil {
		return OutcomeServedOK
	}
	if _, ok := tlsverify.ReasonOf(err); ok {
		return OutcomeIdentityFail
	}
	s := err.Error()
	ls := strings.ToLower(s)
	// The transport stringifies the VerifyConnection error into an
	// Unavailable status; the tlsverify prefix survives that.
	if strings.Contains(s, "fibre tls identity [") {
		return OutcomeIdentityFail
	}
	if st, ok := status.FromError(err); ok {
		switch st.Code() {
		case codes.NotFound:
			return OutcomeNotFound
		case codes.Unavailable:
			return OutcomeRPCUnavailable
		case codes.DeadlineExceeded:
			return OutcomeRPCDeadline
		case codes.ResourceExhausted, codes.InvalidArgument:
			// our request was refused as too large / malformed, or a server
			// limit answered: not a retention verdict.
			return OutcomeProbeError
		case codes.Canceled:
			return OutcomeProbeError
		default:
			return OutcomeRPCError
		}
	}
	switch {
	case errors.Is(err, context.DeadlineExceeded) || strings.Contains(ls, "deadline exceeded"):
		return OutcomeRPCDeadline
	case strings.Contains(ls, "not found"), strings.Contains(ls, "no blob shard"):
		return OutcomeNotFound
	case strings.Contains(ls, "connection refused"), strings.Contains(ls, "actively refused"), strings.Contains(ls, "transport is closing"):
		return OutcomeRPCUnavailable
	case strings.Contains(ls, "authentication handshake"), strings.Contains(ls, "tls"):
		return OutcomeTLSFail
	default:
		return OutcomeRPCError
	}
}

// verifyWithin runs the identity verification under a timeout. Verification is
// CPU-bound (ASN.1 parse plus one ed25519 verify) and normally takes
// microseconds; the bound exists so that no single probe step is unbounded.
func verifyWithin(cert *x509.Certificate, expected ed25519.PublicKey, chainID string, timeout time.Duration) error {
	type result struct{ err error }
	ch := make(chan result, 1)
	go func() { ch <- result{tlsverify.VerifyCertificateAt(cert, expected, chainID, time.Now())} }()
	t := time.NewTimer(timeout)
	defer t.Stop()
	select {
	case r := <-ch:
		return r.err
	case <-t.C:
		return fmt.Errorf("identity verification did not finish within %s", timeout)
	}
}

// orderAddrs puts IPv4 literals before IPv6 ones, keeping the resolver's
// order within each family.
func orderAddrs(addrs []string) []string {
	var v4, v6 []string
	for _, a := range addrs {
		if ip := net.ParseIP(a); ip != nil && ip.To4() == nil {
			v6 = append(v6, a)
		} else {
			v4 = append(v4, a)
		}
	}
	return append(v4, v6...)
}

// isNoRoute reports the local "this family is not routed here" failures that
// should not outrank a real answer from another address.
func isNoRoute(err error) bool {
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "network is unreachable") || strings.Contains(s, "no route to host")
}

// ShardBytes estimates the wire size of one validator's shard for a blob:
// rows × (row data + 14 proof hashes + framing) plus the row-linear-combination
// vector (originalRows × 16 bytes) and the response envelope. Framing is 36
// bytes per row: 14 proof entries × 2 (tag, length), the row data tag and
// 4-byte length, and the index field. The numbers match the table in
// docs/research/R4-probe-etiquette.md section 1.3.
func ShardBytes(blobSize uint32, originalRows, rows int) int64 {
	if originalRows <= 0 {
		return 0
	}
	rowSize := int64(blobSize) / int64(originalRows)
	const proofBytes = 14 * 32
	const framing = 36
	return int64(rows)*(rowSize+proofBytes+framing) + int64(originalRows)*16 + 4
}

func sinceMS(t time.Time) int64 { return time.Since(t).Milliseconds() }

func tlsVersionString(v uint16) string {
	switch v {
	case tls.VersionTLS13:
		return "1.3"
	case tls.VersionTLS12:
		return "1.2"
	default:
		return fmt.Sprintf("0x%04x", v)
	}
}
