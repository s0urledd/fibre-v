package scan

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// Store is the scanner's durable state on disk:
//
//	<dir>/state.json          scan cursor + param history + protocol-params pin
//	<dir>/publications.jsonl  one Publication per line, append-only
//	<dir>/payments.jsonl      one Payment (escrow movement) per line, append-only
//
// Restart safety: publications for a block are appended and fsynced BEFORE the
// cursor in state.json advances past that block (atomic temp+rename). A crash in
// between re-scans the block; DedupeKey (settlement tx hash) makes the re-append
// a no-op because seen keys are loaded on startup.
type Store struct {
	dir      string
	pubPath  string
	payPath  string
	statePth string

	pubFile *os.File
	payFile *os.File
	seen    map[string]bool
	paySeen map[string]bool
}

// PersistState is state.json.
type PersistState struct {
	SchemaVersion     int    `json:"schema_version"`
	ChainID           string `json:"chain_id"`
	StartHeight       int64  `json:"start_height"`
	LastScannedHeight int64  `json:"last_scanned_height"`
	// LastScannedTime is the block time of LastScannedHeight: the frontier
	// on the chain's clock, which the deferred shadow verdict is drawn
	// against. Zero when the scanner has not read a block yet.
	LastScannedTime  time.Time    `json:"last_scanned_time,omitempty"`
	ParamFingerprint string       `json:"protocol_params_fingerprint"`
	ParamHistory     []ParamEntry `json:"param_history"`
	// Gaps are height ranges the scanner had to skip because the node could
	// not serve them. Published, never hidden: a publication in one of these
	// blocks is unknown to this observer.
	Gaps []ScanGap `json:"gaps,omitempty"`
}

// ScanGap is a run of heights the scanner could not read.
type ScanGap struct {
	From      int64     `json:"from"`
	To        int64     `json:"to"`
	Reason    string    `json:"reason"`
	LastError string    `json:"last_error,omitempty"`
	At        time.Time `json:"at"`
	// FromTime and ToTime are the block times of From and To when the
	// block header could still be read (a node that discards ABCI responses
	// keeps headers). They let a reader place the gap on the chain's clock
	// rather than the scanner's; absent, At is the only clue.
	FromTime *time.Time `json:"from_time,omitempty"`
	ToTime   *time.Time `json:"to_time,omitempty"`
}

// Spans reports the chain-time interval a gap covers, falling back to the
// scanner's own clock at the moment it hit the gap when no block time was
// read. The fallback is deliberately the conservative one: a gap whose
// blocks are actually old reads as recent, which errs toward "a promise may
// be hiding in there" rather than toward an accusation.
func (g ScanGap) Spans() (from, to time.Time) {
	from, to = g.At, g.At
	if g.FromTime != nil {
		from = *g.FromTime
	}
	if g.ToTime != nil {
		to = *g.ToTime
	}
	return from, to
}

// OpenStore opens or creates the store in dir.
func OpenStore(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir %s: %w", dir, err)
	}
	s := &Store{
		dir:      dir,
		pubPath:  filepath.Join(dir, "publications.jsonl"),
		payPath:  filepath.Join(dir, "payments.jsonl"),
		statePth: filepath.Join(dir, "state.json"),
		seen:     map[string]bool{},
		paySeen:  map[string]bool{},
	}
	if err := s.loadSeen(); err != nil {
		return nil, err
	}
	if err := s.loadPaySeen(); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(s.pubPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", s.pubPath, err)
	}
	s.pubFile = f
	pf, err := os.OpenFile(s.payPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("open %s: %w", s.payPath, err)
	}
	s.payFile = pf
	return s, nil
}

// TruncateTornTail cuts a trailing partial line (no final newline) off an
// append-only JSONL file and reports how many bytes were removed. A crash or
// power loss mid-write leaves exactly such a tail; without this repair the
// tool refuses to start until someone edits the file by hand. Files that end
// in a newline, are empty, or do not exist are left alone.
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
	last := make([]byte, 1)
	if _, err := f.ReadAt(last, size-1); err != nil {
		return 0, err
	}
	if last[0] == '\n' {
		return 0, nil
	}
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

func (s *Store) loadSeen() error {
	if cut, err := TruncateTornTail(s.pubPath); err != nil {
		return fmt.Errorf("repair %s: %w", s.pubPath, err)
	} else if cut > 0 {
		fmt.Fprintf(os.Stderr, "publications: truncated %d bytes of a torn final line in %s\n", cut, s.pubPath)
	}
	f, err := os.Open(s.pubPath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("open %s: %w", s.pubPath, err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 1<<26)
	n := 0
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var p Publication
		if err := json.Unmarshal(line, &p); err != nil {
			return fmt.Errorf("%s line %d: %w", s.pubPath, n+1, err)
		}
		s.seen[p.SettlementTxHash] = true
		n++
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("scan %s: %w", s.pubPath, err)
	}
	return nil
}

func (s *Store) loadPaySeen() error {
	if cut, err := TruncateTornTail(s.payPath); err != nil {
		return fmt.Errorf("repair %s: %w", s.payPath, err)
	} else if cut > 0 {
		fmt.Fprintf(os.Stderr, "payments: truncated %d bytes of a torn final line in %s\n", cut, s.payPath)
	}
	f, err := os.Open(s.payPath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("open %s: %w", s.payPath, err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 1<<26)
	n := 0
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var p Payment
		if err := json.Unmarshal(line, &p); err != nil {
			return fmt.Errorf("%s line %d: %w", s.payPath, n+1, err)
		}
		s.paySeen[p.DedupeKey] = true
		n++
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("scan %s: %w", s.payPath, err)
	}
	return nil
}

// LoadState returns the persisted state, or (nil, nil) if there is none yet.
func (s *Store) LoadState() (*PersistState, error) {
	b, err := os.ReadFile(s.statePth)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", s.statePth, err)
	}
	var st PersistState
	if err := json.Unmarshal(b, &st); err != nil {
		return nil, fmt.Errorf("parse %s: %w", s.statePth, err)
	}
	return &st, nil
}

// Seen reports whether a publication with this settlement tx hash is already
// persisted.
func (s *Store) Seen(settlementTxHash string) bool { return s.seen[settlementTxHash] }

// AppendPublication writes one record (skipping an already-seen one) in a
// single write, so a concurrent reader (the prober tails this file) never
// sees a record split across two buffer flushes. It does NOT fsync per call;
// call Sync() before advancing the cursor.
func (s *Store) AppendPublication(p Publication) error {
	if s.seen[p.SettlementTxHash] {
		return nil
	}
	b, err := json.Marshal(p)
	if err != nil {
		return fmt.Errorf("marshal publication %s: %w", p.PromiseHash, err)
	}
	if _, err := s.pubFile.Write(append(b, '\n')); err != nil {
		return fmt.Errorf("write publication: %w", err)
	}
	s.seen[p.SettlementTxHash] = true
	return nil
}

// AppendPayment writes one escrow movement (skipping an already-seen one)
// in a single write, under the same crash rules as AppendPublication.
func (s *Store) AppendPayment(p Payment) error {
	if p.DedupeKey == "" {
		return fmt.Errorf("payment without a dedupe key (h=%d kind=%s)", p.Height, p.Kind)
	}
	if s.paySeen[p.DedupeKey] {
		return nil
	}
	b, err := json.Marshal(p)
	if err != nil {
		return fmt.Errorf("marshal payment %s: %w", p.DedupeKey, err)
	}
	if _, err := s.payFile.Write(append(b, '\n')); err != nil {
		return fmt.Errorf("write payment: %w", err)
	}
	s.paySeen[p.DedupeKey] = true
	return nil
}

// PaymentSeen reports whether a payment with this dedupe key is persisted.
func (s *Store) PaymentSeen(key string) bool { return s.paySeen[key] }

// Sync fsyncs the publications and payments files.
func (s *Store) Sync() error {
	if err := s.pubFile.Sync(); err != nil {
		return fmt.Errorf("fsync publications: %w", err)
	}
	if err := s.payFile.Sync(); err != nil {
		return fmt.Errorf("fsync payments: %w", err)
	}
	return nil
}

// SaveState atomically replaces state.json. Call after Sync().
func (s *Store) SaveState(st PersistState) error {
	st.SchemaVersion = SchemaVersion
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal state: %w", err)
	}
	tmp := s.statePth + ".tmp"
	if err := writeFileSync(tmp, b); err != nil {
		return fmt.Errorf("write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, s.statePth); err != nil {
		return fmt.Errorf("rename %s: %w", tmp, err)
	}
	// fsync the directory so the rename itself survives a power loss.
	if d, err := os.Open(s.dir); err == nil {
		_ = d.Sync()
		d.Close()
	}
	return nil
}

// writeFileSync writes b to path and fsyncs it before returning, so a rename
// over the live file never exposes an empty or partial state.json.
func writeFileSync(path string, b []byte) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// Close closes the publications and payments files.
func (s *Store) Close() error {
	var first error
	if s.pubFile != nil {
		if err := s.pubFile.Close(); err != nil {
			first = err
		}
	}
	if s.payFile != nil {
		if err := s.payFile.Close(); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// PublicationsPath is the jsonl path (for tooling / tests).
func (s *Store) PublicationsPath() string { return s.pubPath }

// PaymentsPath is the payments jsonl path.
func (s *Store) PaymentsPath() string { return s.payPath }
