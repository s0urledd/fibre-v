// Package ingest tails the append-only JSONL files written by sentinel-scan
// and sentinel-probe into the observer store. Each file has a byte-offset
// cursor in the store; records are idempotent on their primary keys, so a
// cursor that is behind (or a file that is re-ingested from zero) only costs
// re-reading, never duplicate rows.
package ingest

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/plsgiveup/fibre/fibre-sentinel/internal/probe"
	"github.com/plsgiveup/fibre/fibre-sentinel/internal/scan"
	"github.com/plsgiveup/fibre/fibre-sentinel/observer/store"
)

// maxLine bounds one JSONL record. A publication with full row lists for 100
// validators is a few hundred KB; 64 MiB leaves a wide margin.
const maxLine = 64 << 20

// Result summarises one ingest pass over a file.
type Result struct {
	Read     int64 // lines read this pass
	Inserted int64 // rows actually inserted (rest were already present)
	Offset   int64 // byte offset after the pass
	Line     int64 // line number after the pass
}

// handler consumes one raw JSONL line and reports whether it inserted a row.
type handler func(raw []byte) (bool, error)

// tail reads file from the stored cursor to EOF, feeding complete lines to fn
// and persisting the cursor after every line. A trailing partial line (a
// write in progress) is left for the next pass.
func tail(st *store.Store, path string, fn handler, now time.Time) (Result, error) {
	offset, line, err := st.Cursor(path)
	if err != nil {
		return Result{}, err
	}
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return Result{Offset: offset, Line: line}, nil
	}
	if err != nil {
		return Result{}, err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return Result{}, err
	}
	if info.Size() < offset {
		// The file was truncated or replaced: start over. Idempotent keys
		// make this safe; it just re-reads.
		offset, line = 0, 0
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return Result{}, err
	}

	r := bufio.NewReaderSize(f, 1<<20)
	res := Result{Offset: offset, Line: line}
	for {
		raw, err := r.ReadBytes('\n')
		if err != nil {
			if errors.Is(err, io.EOF) {
				break // partial trailing line stays unread until it is complete
			}
			return res, err
		}
		if len(raw) > maxLine {
			return res, fmt.Errorf("%s line %d: record longer than %d bytes", path, res.Line+1, maxLine)
		}
		trimmed := bytes.TrimSpace(raw)
		res.Read++
		res.Line++
		res.Offset += int64(len(raw))
		if len(trimmed) == 0 {
			continue
		}
		ins, err := fn(trimmed)
		if err != nil {
			return res, fmt.Errorf("%s line %d: %w", path, res.Line, err)
		}
		if ins {
			res.Inserted++
		}
		if err := st.SetCursor(path, res.Offset, res.Line, now); err != nil {
			return res, err
		}
	}
	return res, nil
}

// Publications ingests publications.jsonl.
func Publications(st *store.Store, path string, now time.Time) (Result, error) {
	return tail(st, path, func(raw []byte) (bool, error) {
		var p scan.Publication
		if err := json.Unmarshal(raw, &p); err != nil {
			return false, fmt.Errorf("decode publication: %w", err)
		}
		if p.PromiseHash == "" {
			return false, errors.New("publication without promise_hash")
		}
		return st.UpsertPublication(p, raw)
	}, now)
}

// Measurements ingests measurements.jsonl.
func Measurements(st *store.Store, path string, now time.Time) (Result, error) {
	return tail(st, path, func(raw []byte) (bool, error) {
		var m probe.Measurement
		if err := json.Unmarshal(raw, &m); err != nil {
			return false, fmt.Errorf("decode measurement: %w", err)
		}
		if m.PromiseHash == "" || m.ValidatorAddress == "" {
			return false, errors.New("measurement without promise_hash or validator_address")
		}
		return st.InsertProbe(m, raw)
	}, now)
}

// State copies the scanner's state.json (param history, cursor, chain id)
// into the store. It is small and fully re-read every pass.
func State(st *store.Store, path string, now time.Time) error {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var ps scan.PersistState
	if err := json.Unmarshal(b, &ps); err != nil {
		return fmt.Errorf("decode %s: %w", path, err)
	}
	if err := st.UpsertParams(ps.ParamHistory); err != nil {
		return err
	}
	for k, v := range map[string]string{
		"chain_id":                    ps.ChainID,
		"scan_start_height":           fmt.Sprint(ps.StartHeight),
		"last_scanned_height":         fmt.Sprint(ps.LastScannedHeight),
		"protocol_params_fingerprint": ps.ParamFingerprint,
	} {
		if err := st.SetMeta(k, v, now); err != nil {
			return err
		}
	}
	return nil
}
