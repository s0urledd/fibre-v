package scan

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Store is the scanner's durable state on disk:
//
//	<dir>/state.json          scan cursor + param history + protocol-params pin
//	<dir>/publications.jsonl  one Publication per line, append-only
//
// Restart safety: publications for a block are appended and fsynced BEFORE the
// cursor in state.json advances past that block (atomic temp+rename). A crash in
// between re-scans the block; DedupeKey (settlement tx hash) makes the re-append
// a no-op because seen keys are loaded on startup.
type Store struct {
	dir      string
	pubPath  string
	statePth string

	pubFile *os.File
	pubW    *bufio.Writer
	seen    map[string]bool
}

// PersistState is state.json.
type PersistState struct {
	SchemaVersion     int          `json:"schema_version"`
	ChainID           string       `json:"chain_id"`
	StartHeight       int64        `json:"start_height"`
	LastScannedHeight int64        `json:"last_scanned_height"`
	ParamFingerprint  string       `json:"protocol_params_fingerprint"`
	ParamHistory      []ParamEntry `json:"param_history"`
}

// OpenStore opens or creates the store in dir.
func OpenStore(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir %s: %w", dir, err)
	}
	s := &Store{
		dir:      dir,
		pubPath:  filepath.Join(dir, "publications.jsonl"),
		statePth: filepath.Join(dir, "state.json"),
		seen:     map[string]bool{},
	}
	if err := s.loadSeen(); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(s.pubPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", s.pubPath, err)
	}
	s.pubFile = f
	s.pubW = bufio.NewWriter(f)
	return s, nil
}

func (s *Store) loadSeen() error {
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

// AppendPublication writes one record (skipping an already-seen one) and flushes
// it to the OS. It does NOT fsync per call; call Sync() before advancing the
// cursor.
func (s *Store) AppendPublication(p Publication) error {
	if s.seen[p.SettlementTxHash] {
		return nil
	}
	b, err := json.Marshal(p)
	if err != nil {
		return fmt.Errorf("marshal publication %s: %w", p.PromiseHash, err)
	}
	if _, err := s.pubW.Write(append(b, '\n')); err != nil {
		return fmt.Errorf("write publication: %w", err)
	}
	s.seen[p.SettlementTxHash] = true
	return nil
}

// Sync flushes and fsyncs the publications file.
func (s *Store) Sync() error {
	if err := s.pubW.Flush(); err != nil {
		return fmt.Errorf("flush publications: %w", err)
	}
	if err := s.pubFile.Sync(); err != nil {
		return fmt.Errorf("fsync publications: %w", err)
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
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, s.statePth); err != nil {
		return fmt.Errorf("rename %s: %w", tmp, err)
	}
	return nil
}

// Close flushes and closes the publications file.
func (s *Store) Close() error {
	if s.pubW != nil {
		_ = s.pubW.Flush()
	}
	if s.pubFile != nil {
		return s.pubFile.Close()
	}
	return nil
}

// PublicationsPath is the jsonl path (for tooling / tests).
func (s *Store) PublicationsPath() string { return s.pubPath }
