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
	"google.golang.org/grpc/credentials"
)

// StepTimeouts bounds every layer of a probe. Nothing in a probe blocks longer
// than the relevant field.
type StepTimeouts struct {
	DNS      time.Duration
	TCP      time.Duration
	TLS      time.Duration
	Identity time.Duration
	Download time.Duration
}

// DefaultStepTimeouts are conservative for a WAN vantage.
func DefaultStepTimeouts() StepTimeouts {
	return StepTimeouts{
		DNS:      5 * time.Second,
		TCP:      5 * time.Second,
		TLS:      10 * time.Second,
		Identity: 2 * time.Second,
		Download: 25 * time.Second,
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
	return t
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
		LatenessMS:         now.Sub(in.SchedulePoint.At).Milliseconds(),
	}
	defer func() {
		m.FinishedAt = time.Now().UTC()
		m.TotalDurationMS = m.FinishedAt.Sub(m.StartedAt).Milliseconds()
		m.Classification, m.ClassificationReason = Classify(m.Assigned, m.Phase, m.Outcome)
	}()

	if in.Target.Host == "" {
		m.Outcome = OutcomeProbeError
		m.RawError = "validator has no registered fibre host"
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
	var ip string
	if pip := net.ParseIP(host); pip != nil {
		m.DNS = StepResult{Attempted: false, OK: true, Detail: "literal IP " + host}
		ip = host
	} else {
		t0 := time.Now()
		dctx, cancel := context.WithTimeout(ctx, to.DNS)
		addrs, derr := net.DefaultResolver.LookupHost(dctx, host)
		cancel()
		m.DNS = StepResult{Attempted: true, OK: derr == nil, DurationMS: sinceMS(t0)}
		if derr != nil {
			m.DNS.Error = derr.Error()
			m.Outcome = OutcomeDNSFail
			m.RawError = derr.Error()
			return m
		}
		m.DNS.Detail = strings.Join(addrs, ",")
		ip = addrs[0]
	}

	// ---- L2: TCP ----
	t0 := time.Now()
	d := net.Dialer{Timeout: to.TCP}
	rawConn, terr := d.DialContext(ctx, "tcp", net.JoinHostPort(ip, port))
	m.TCP = StepResult{Attempted: true, OK: terr == nil, DurationMS: sinceMS(t0)}
	if terr != nil {
		m.TCP.Error = terr.Error()
		m.Outcome = classifyDialError(terr)
		m.RawError = terr.Error()
		return m
	}
	m.TCP.Detail = "-> " + rawConn.RemoteAddr().String()

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
	verr := tlsverify.VerifyCertificateAt(peerCert, in.Target.PubKey, in.ChainID, time.Now())
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

	// ---- L4: retrievability (fresh dial with the verifying TLS config) ----
	dl := downloadAndVerify(ctx, in, coder, to.Download)
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

func downloadAndVerify(ctx context.Context, in Input, coder *Coder, timeout time.Duration) dlResult {
	r := dlResult{DownloadResult: DownloadResult{Attempted: true, RowsExpected: in.Target.RowCount}}
	t0 := time.Now()

	tlsCfg := tlsverify.ClientTLSConfig(in.Target.PubKey, in.ChainID)
	conn, err := grpc.NewClient(in.Target.Host, grpc.WithTransportCredentials(credentials.NewTLS(tlsCfg)))
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

func classifyDownloadError(err error) Outcome {
	if _, ok := tlsverify.ReasonOf(err); ok {
		return OutcomeIdentityFail
	}
	s := err.Error()
	ls := strings.ToLower(s)
	switch {
	case strings.Contains(s, "NotFound"), strings.Contains(ls, "no blob shard"), strings.Contains(ls, "not found"):
		return OutcomeNotFound
	case strings.Contains(s, "Unavailable"), strings.Contains(ls, "connection refused"), strings.Contains(ls, "actively refused"), strings.Contains(ls, "transport is closing"):
		return OutcomeRPCUnavailable
	case strings.Contains(ls, "authentication handshake"), strings.Contains(ls, "tls"):
		return OutcomeTLSFail
	default:
		return OutcomeRPCError
	}
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
