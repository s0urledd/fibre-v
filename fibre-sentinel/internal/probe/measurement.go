package probe

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// MeasurementSchemaVersion is bumped when the Measurement JSON shape changes.
//
// 1: initial shape.
// 2: Attested, and the UNATTESTED classification it can produce.
const MeasurementSchemaVersion = 2

// AttestationSchemaVersion is the first measurement version whose Attested
// field carries evidence. A record below it was written by a prober that did
// not know about attestation: its Attested is false because the field did not
// exist, and its classification can never be UNATTESTED. Consumers must treat
// that as unknown rather than as "did not attest".
const AttestationSchemaVersion = 2

// HasAttestation reports whether Attested carries evidence.
func (m Measurement) HasAttestation() bool {
	return m.SchemaVersion >= AttestationSchemaVersion && !m.AttestationUnknown
}

// Measurement is one probe's raw result: which vantage, when, each network
// layer timed and judged separately, plus the raw error text. No scores — a
// reliability view is derived from these later.
type Measurement struct {
	SchemaVersion int    `json:"schema_version"`
	Vantage       string `json:"vantage"`

	// what was probed
	PromiseHash        string    `json:"promise_hash"`
	Commitment         string    `json:"commitment"`
	BlobVersion        uint32    `json:"blob_version"`
	MustServeUntil     time.Time `json:"must_serve_until"`
	ValidatorSetHeight int64     `json:"validator_set_height"`

	// the target validator
	ValidatorAddress string `json:"validator_address"` // 20-byte consensus addr, hex
	ValidatorHost    string `json:"validator_host"`    // host:port as registered
	Assigned         bool   `json:"assigned"`
	// HostSource is "bonded" when the validator was in
	// AllBondedFibreProviders at the time of the probe, and "last_known" when
	// it was not but this observer had seen it register that host earlier and
	// kept probing it anyway. A validator's retention obligation comes from
	// the promise it signed, not from its bonding status, so leaving the
	// bonded set must not stop the evidence.
	HostSource string `json:"host_source,omitempty"`
	// Attested: the settled promise carries a signature from this validator
	// that the observer verified against its consensus key. That is the only
	// on-chain proof the validator ever stored this shard, because a Fibre
	// server writes the shard before it signs. False means unproven, not
	// absent: the publisher stops collecting signatures at the safety
	// threshold and keeps delivering in the background.
	Attested bool `json:"attested"`
	// AttestationUnknown is set when the publication record the probe was
	// built from predates signature verification, so Attested is not
	// evidence. Omitted (false) on every row that carries evidence, which is
	// also every row written before the field existed.
	AttestationUnknown bool `json:"attestation_unknown,omitempty"`
	AssignedRowCount   int  `json:"assigned_row_count"`

	// scheduling
	ScheduleLabel string    `json:"schedule_label"` // w1..wN, grace, post
	ScheduledAt   time.Time `json:"scheduled_at"`
	StartedAt     time.Time `json:"started_at"`
	FinishedAt    time.Time `json:"finished_at"`
	LatenessMS    int64     `json:"lateness_ms"` // started_at - scheduled_at

	// per-layer results (each timed and judged on its own)
	DNS      StepResult     `json:"dns"`
	TCP      StepResult     `json:"tcp"`
	TLS      TLSResult      `json:"tls"`
	Identity IdentityResult `json:"identity"`
	Download DownloadResult `json:"download"`

	// verdict
	Phase Phase `json:"phase"` // from StartedAt
	// PhaseNote says why Phase differs from the phase at StartedAt, when it
	// does: "not_found_at_deadline" is a NOT_FOUND that arrived within
	// NotFoundGuard of must_serve_until and was graded as grace.
	PhaseNote            string         `json:"phase_note,omitempty"`
	Outcome              Outcome        `json:"outcome"`
	Classification       Classification `json:"classification"`
	ClassificationReason string         `json:"classification_reason"`
	RawError             string         `json:"raw_error,omitempty"`
	TotalDurationMS      int64          `json:"total_duration_ms"`

	// Sampling records the admission decision this publication was probed (or
	// not probed) under: the probability, the cap that bound it, and the
	// commitment to that day's secret. Every row carries it, admitted or
	// denied, so that once the secret for a day is published anyone can
	// recompute which publications should have been in the sample and check
	// this observer against it. Empty when no policy was configured, which
	// means everything was probed.
	Sampling *SamplingDecision `json:"sampling,omitempty"`

	// ClockOffsetMS is the observer's clock minus the chain's latest block
	// time when the probe ran. Phases are decided by the local clock, so a
	// reader can judge how much to trust a vantage. Additive, omitempty.
	ClockOffsetMS int64 `json:"clock_offset_ms,omitempty"`

	// Retry is set when this measurement is the second attempt after a
	// transport timeout (see Config.RetryTransportTimeout). Absent on
	// single-attempt measurements; additive, so the schema version is unchanged.
	Retry *RetryInfo `json:"retry,omitempty"`
}

// RetryInfo records the first attempt of a probe that was retried once after
// a transport timeout. The enclosing Measurement is the second attempt.
type RetryInfo struct {
	Attempts        int       `json:"attempts"` // always 2
	DelayMS         int64     `json:"delay_ms"`
	FirstStartedAt  time.Time `json:"first_started_at"`
	FirstOutcome    Outcome   `json:"first_outcome"`
	FirstError      string    `json:"first_error,omitempty"`
	FirstDurationMS int64     `json:"first_duration_ms"`
}

// StepResult is one timed network step (DNS resolution, TCP connect).
type StepResult struct {
	Attempted  bool   `json:"attempted"`
	OK         bool   `json:"ok"`
	DurationMS int64  `json:"duration_ms"`
	Detail     string `json:"detail,omitempty"` // resolved IPs, remote addr, ...
	Error      string `json:"error,omitempty"`
}

// TLSResult is the raw TLS handshake outcome (no identity judgement — that is
// IdentityResult).
type TLSResult struct {
	Attempted        bool   `json:"attempted"`
	OK               bool   `json:"ok"`
	DurationMS       int64  `json:"duration_ms"`
	Version          string `json:"version,omitempty"` // "1.3"
	CipherSuite      string `json:"cipher_suite,omitempty"`
	PeerCertSHA256   string `json:"peer_cert_sha256,omitempty"`
	PeerCertNotAfter string `json:"peer_cert_not_after,omitempty"`
	Error            string `json:"error,omitempty"`
}

// SamplingDecision is the load-policy decision a row was produced under.
type SamplingDecision struct {
	// P is the admission probability at the moment the publication was first
	// seen. 1 means no cap was binding and nothing was sampled out.
	P float64 `json:"p"`
	// Binding names the cap that produced P ("none" when P is 1).
	Binding string `json:"binding,omitempty"`
	// DayCommitment is SHA256 of the day secret the draw used. It can be
	// published in advance; revealing the secret afterwards lets anyone
	// recompute the draw for every promise hash of that day.
	DayCommitment string `json:"day_commitment,omitempty"`
}

// IdentityResult is the fibre-tlsverify consensus-key binding check on the peer
// certificate, run as its own step against the handshake's peer cert.
type IdentityResult struct {
	Attempted  bool   `json:"attempted"`
	OK         bool   `json:"ok"`
	DurationMS int64  `json:"duration_ms"`
	Reason     string `json:"reason,omitempty"` // tlsverify.Reason on failure
	// Stale: the certificate is endorsed by the right consensus key but its
	// signed validity window has lapsed or has not started. That is endpoint
	// hygiene, not impersonation, and the taxonomy keeps the two apart.
	Stale bool `json:"stale,omitempty"`
	// ClaimedNotBefore/After come from Inspect — what the peer's extension
	// says regardless of verdict.
	ClaimedNotBefore string `json:"claimed_not_before,omitempty"`
	ClaimedNotAfter  string `json:"claimed_not_after,omitempty"`
	Error            string `json:"error,omitempty"`
}

// DownloadResult is the L4 retrievability step: DownloadShard + verify rows
// against the commitment and against the assignment.
type DownloadResult struct {
	Attempted    bool  `json:"attempted"`
	OK           bool  `json:"ok"`
	DurationMS   int64 `json:"duration_ms"`
	RowsReturned int   `json:"rows_returned"`
	RowsExpected int   `json:"rows_expected"`
	// BytesReturned is the row payload the server handed over: the sum of
	// the row data bytes, proofs and the RLC vector excluded. Rows are not a
	// unit of size — a row is as wide as the blob's square — so this is what
	// makes a transfer rate comparable across blobs. Zero on a record written
	// before the field existed, which the store keeps as unknown, not as
	// zero bytes.
	BytesReturned      int64  `json:"bytes_returned,omitempty"`
	CommitmentVerified bool   `json:"commitment_verified"` // rsema1d reconstructor accepted the proofs
	AssignmentVerified bool   `json:"assignment_verified"` // returned indices == assigned set
	Error              string `json:"error,omitempty"`
}

// DedupeKey identifies a measurement slot: one probe per (vantage, promise,
// validator, scheduled point).
func (m Measurement) DedupeKey() string {
	return m.Vantage + "|" + m.PromiseHash + "|" + m.ValidatorAddress + "|" + m.ScheduledAt.UTC().Format(time.RFC3339Nano)
}

func dedupeKey(vantage, promiseHash, validatorAddr string, scheduledAt time.Time) string {
	return vantage + "|" + promiseHash + "|" + validatorAddr + "|" + scheduledAt.UTC().Format(time.RFC3339Nano)
}

// MeasurementStore is an append-only measurements.jsonl plus an in-memory set
// of dedupe keys loaded on open, so a restart never re-probes a slot it already
// has. Keys are grouped by promise hash so a finished publication can be
// forgotten in O(1) (Forget) instead of growing the set forever. Safe for
// concurrent use.
type MeasurementStore struct {
	path string
	f    *os.File

	mu         sync.Mutex
	seen       map[string]map[string]bool // promise hash -> full dedupe keys
	seenPoints map[string]map[string]bool // promise hash -> vantage|promise|scheduledAt
	dirty      bool                       // appended without fsync since the last Sync
}

func pointKey(vantage, promiseHash string, scheduledAt time.Time) string {
	return vantage + "|" + promiseHash + "|" + scheduledAt.UTC().Format(time.RFC3339Nano)
}

// OpenMeasurementStore opens or creates <dir>/measurements.jsonl. A torn
// final line (a write interrupted by a crash) is truncated away before the
// file is opened for append, so one bad byte sequence at the end never bricks
// the prober; a malformed interior line is still a hard error.
func OpenMeasurementStore(dir string) (*MeasurementStore, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir %s: %w", dir, err)
	}
	path := filepath.Join(dir, "measurements.jsonl")
	s := &MeasurementStore{path: path, seen: map[string]map[string]bool{}, seenPoints: map[string]map[string]bool{}}
	if err := s.loadSeen(); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	s.f = f
	return s, nil
}

// TruncateTornTail cuts a trailing partial line (no final newline) off an
// append-only JSONL file and reports how many bytes were removed. Files that
// end in a newline, are empty, or do not exist are left alone.
func TruncateTornTail(path string) (int64, error) {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.Size() == 0 {
		return 0, err
	}
	size := info.Size()
	buf := make([]byte, 1)
	if _, err := f.ReadAt(buf, size-1); err != nil {
		return 0, err
	}
	if buf[0] == '\n' {
		return 0, nil
	}
	// walk back to the previous newline (bounded chunks)
	cut := size
	const chunk = 1 << 16
	for cut > 0 {
		start := cut - chunk
		if start < 0 {
			start = 0
		}
		b := make([]byte, cut-start)
		if _, err := f.ReadAt(b, start); err != nil && err != io.EOF {
			return 0, err
		}
		if i := bytes.LastIndexByte(b, '\n'); i >= 0 {
			cut = start + int64(i) + 1
			break
		}
		cut = start
	}
	if err := f.Truncate(cut); err != nil {
		return 0, err
	}
	return size - cut, f.Sync()
}

func (s *MeasurementStore) loadSeen() error {
	if cut, err := TruncateTornTail(s.path); err != nil {
		return fmt.Errorf("repair %s: %w", s.path, err)
	} else if cut > 0 {
		fmt.Fprintf(os.Stderr, "measurements: truncated %d bytes of a torn final line in %s\n", cut, s.path)
	}
	f, err := os.Open(s.path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("open %s: %w", s.path, err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 1<<26)
	n := 0
	for sc.Scan() {
		if len(sc.Bytes()) == 0 {
			continue
		}
		var m Measurement
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			return fmt.Errorf("%s line %d: %w", s.path, n+1, err)
		}
		s.remember(m)
		n++
	}
	return sc.Err()
}

func (s *MeasurementStore) remember(m Measurement) {
	if s.seen[m.PromiseHash] == nil {
		s.seen[m.PromiseHash] = map[string]bool{}
		s.seenPoints[m.PromiseHash] = map[string]bool{}
	}
	s.seen[m.PromiseHash][m.DedupeKey()] = true
	s.seenPoints[m.PromiseHash][pointKey(m.Vantage, m.PromiseHash, m.ScheduledAt)] = true
}

// Has reports whether a measurement for this exact slot is already recorded.
func (s *MeasurementStore) Has(vantage, promiseHash, validatorAddr string, scheduledAt time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.seen[promiseHash][dedupeKey(vantage, promiseHash, validatorAddr, scheduledAt)]
}

// HandledPoint reports whether this (vantage, publication, schedule point) has
// been touched at all: at least one target has a row. It is a hint that the
// point was started, not that it is complete (see Prober.complete).
func (s *MeasurementStore) HandledPoint(vantage, promiseHash string, scheduledAt time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.seenPoints[promiseHash][pointKey(vantage, promiseHash, scheduledAt)]
}

// Forget drops the in-memory keys of a publication whose schedule is entirely
// in the past; the file keeps every row.
func (s *MeasurementStore) Forget(promiseHash string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.seen, promiseHash)
	delete(s.seenPoints, promiseHash)
}

// Append writes one measurement (skipping an already-seen slot) and fsyncs,
// so every probe result is durable the moment it is recorded.
func (s *MeasurementStore) Append(m Measurement) error {
	return s.append(m, true)
}

// AppendDeferred writes without fsync; call Sync after a batch (used for the
// NOT_PROBED markers a late start fans out, where one fsync per row would
// take hours).
func (s *MeasurementStore) AppendDeferred(m Measurement) error {
	return s.append(m, false)
}

func (s *MeasurementStore) append(m Measurement, sync bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.seen[m.PromiseHash][m.DedupeKey()] {
		return nil
	}
	b, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("marshal measurement: %w", err)
	}
	// one write per record: a reader never sees half a line from a buffer
	// flush, and a crash leaves at most one torn tail (repaired on open).
	if _, err := s.f.Write(append(b, '\n')); err != nil {
		return fmt.Errorf("write measurement: %w", err)
	}
	s.dirty = true
	if sync {
		if err := s.f.Sync(); err != nil {
			return fmt.Errorf("fsync measurements: %w", err)
		}
		s.dirty = false
	}
	s.remember(m)
	return nil
}

// Sync fsyncs pending deferred appends.
func (s *MeasurementStore) Sync() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.dirty {
		return nil
	}
	if err := s.f.Sync(); err != nil {
		return fmt.Errorf("fsync measurements: %w", err)
	}
	s.dirty = false
	return nil
}

// Close syncs and closes the file.
func (s *MeasurementStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.f != nil {
		if s.dirty {
			_ = s.f.Sync()
		}
		return s.f.Close()
	}
	return nil
}

// Path returns the measurements file path.
func (s *MeasurementStore) Path() string { return s.path }

// LoadMeasurements reads a measurements.jsonl (for tooling / tests).
func LoadMeasurements(path string) ([]Measurement, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 1<<26)
	var out []Measurement
	for sc.Scan() {
		if len(sc.Bytes()) == 0 {
			continue
		}
		var m Measurement
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, sc.Err()
}
