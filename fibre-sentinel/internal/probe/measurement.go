package probe

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// MeasurementSchemaVersion is bumped when the Measurement JSON shape changes.
const MeasurementSchemaVersion = 1

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
	AssignedRowCount int    `json:"assigned_row_count"`

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
	Phase                Phase          `json:"phase"` // from StartedAt
	Outcome              Outcome        `json:"outcome"`
	Classification       Classification `json:"classification"`
	ClassificationReason string         `json:"classification_reason"`
	RawError             string         `json:"raw_error,omitempty"`
	TotalDurationMS      int64          `json:"total_duration_ms"`
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

// IdentityResult is the fibre-tlsverify consensus-key binding check on the peer
// certificate, run as its own step against the handshake's peer cert.
type IdentityResult struct {
	Attempted  bool   `json:"attempted"`
	OK         bool   `json:"ok"`
	DurationMS int64  `json:"duration_ms"`
	Reason     string `json:"reason,omitempty"` // tlsverify.Reason on failure
	// ClaimedNotBefore/After come from Inspect — what the peer's extension
	// says regardless of verdict.
	ClaimedNotBefore string `json:"claimed_not_before,omitempty"`
	ClaimedNotAfter  string `json:"claimed_not_after,omitempty"`
	Error            string `json:"error,omitempty"`
}

// DownloadResult is the L4 retrievability step: DownloadShard + verify rows
// against the commitment and against the assignment.
type DownloadResult struct {
	Attempted          bool   `json:"attempted"`
	OK                 bool   `json:"ok"`
	DurationMS         int64  `json:"duration_ms"`
	RowsReturned       int    `json:"rows_returned"`
	RowsExpected       int    `json:"rows_expected"`
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
// has.
type MeasurementStore struct {
	path       string
	f          *os.File
	w          *bufio.Writer
	seen       map[string]bool // full dedupe keys
	seenPoints map[string]bool // vantage|promise|scheduledAt — "this point was handled"
}

func pointKey(vantage, promiseHash string, scheduledAt time.Time) string {
	return vantage + "|" + promiseHash + "|" + scheduledAt.UTC().Format(time.RFC3339Nano)
}

// OpenMeasurementStore opens or creates <dir>/measurements.jsonl.
func OpenMeasurementStore(dir string) (*MeasurementStore, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir %s: %w", dir, err)
	}
	path := filepath.Join(dir, "measurements.jsonl")
	s := &MeasurementStore{path: path, seen: map[string]bool{}, seenPoints: map[string]bool{}}
	if err := s.loadSeen(); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	s.f = f
	s.w = bufio.NewWriter(f)
	return s, nil
}

func (s *MeasurementStore) loadSeen() error {
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
		s.seen[m.DedupeKey()] = true
		s.seenPoints[pointKey(m.Vantage, m.PromiseHash, m.ScheduledAt)] = true
		n++
	}
	return sc.Err()
}

// Has reports whether a measurement for this exact slot is already recorded.
func (s *MeasurementStore) Has(vantage, promiseHash, validatorAddr string, scheduledAt time.Time) bool {
	return s.seen[dedupeKey(vantage, promiseHash, validatorAddr, scheduledAt)]
}

// HandledPoint reports whether this (vantage, publication, schedule point) has
// been touched at all — used to skip re-planning a point the prober already ran
// (or marked missed) for every target.
func (s *MeasurementStore) HandledPoint(vantage, promiseHash string, scheduledAt time.Time) bool {
	return s.seenPoints[pointKey(vantage, promiseHash, scheduledAt)]
}

// Append writes one measurement (skipping an already-seen slot) and fsyncs.
// Probes are infrequent, so a sync per measurement is cheap and makes every
// record durable immediately.
func (s *MeasurementStore) Append(m Measurement) error {
	if s.seen[m.DedupeKey()] {
		return nil
	}
	b, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("marshal measurement: %w", err)
	}
	if _, err := s.w.Write(append(b, '\n')); err != nil {
		return fmt.Errorf("write measurement: %w", err)
	}
	if err := s.w.Flush(); err != nil {
		return fmt.Errorf("flush measurements: %w", err)
	}
	if err := s.f.Sync(); err != nil {
		return fmt.Errorf("fsync measurements: %w", err)
	}
	s.seen[m.DedupeKey()] = true
	s.seenPoints[pointKey(m.Vantage, m.PromiseHash, m.ScheduledAt)] = true
	return nil
}

// Close flushes and closes the file.
func (s *MeasurementStore) Close() error {
	if s.w != nil {
		_ = s.w.Flush()
	}
	if s.f != nil {
		return s.f.Close()
	}
	return nil
}

// Path is the measurements file path.
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
