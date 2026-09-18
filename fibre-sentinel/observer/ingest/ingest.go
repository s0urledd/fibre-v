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
	"github.com/plsgiveup/fibre/fibre-sentinel/internal/status"
	"github.com/plsgiveup/fibre/fibre-sentinel/observer/store"
)

// maxLine bounds one JSONL record. A publication with full row lists for 100
// validators is a few hundred KB; 64 MiB leaves a wide margin.
const maxLine = 64 << 20

// Result summarises one ingest pass over a file.
type Result struct {
	Read     int64 // lines read this pass
	Inserted int64 // rows actually inserted (rest were already present)
	Skipped  int64 // undecodable lines stepped over this pass (see ErrBadRecord)
	Offset   int64 // byte offset after the pass
	Line     int64 // line number after the pass
	// LastSkipped is the error of the most recent skipped line, for the log.
	LastSkipped string
	// Deferred names the line the pass stopped before because its row is
	// not in the store yet (ErrRetryLater); empty when the pass read to EOF.
	Deferred string
}

// ErrBadRecord marks a line that cannot be decoded. A torn write (a crash
// mid-record followed by the next record appended after it) leaves exactly
// one such line; stopping at it would stall the file forever, so the tailer
// logs it, counts it and advances past it. Store errors are never wrapped in
// it and still stop the pass.
var ErrBadRecord = errors.New("bad record")

// ErrRetryLater marks a line whose row is not in the store yet (an
// amendment for a probe row that has not been ingested). The pass stops
// before it without advancing the cursor, so the next pass tries again
// once the measurements have caught up; after retryPasses passes the line
// is stepped over like a bad record, so a row that never arrives cannot
// stall the file.
var ErrRetryLater = errors.New("retry later")

const retryPasses = 3

// retries counts the passes on which a line has asked to be retried, by
// file and line number.
var retries = map[string]int{}

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
		trimmed := bytes.TrimSpace(raw)
		res.Read++
		res.Line++
		res.Offset += int64(len(raw))
		if len(raw) > maxLine {
			// Too long to be one of our records: step over it like any other
			// undecodable line rather than stalling the file forever.
			res.Skipped++
			res.LastSkipped = fmt.Sprintf("%s line %d: record longer than %d bytes", path, res.Line, maxLine)
			if err := st.SetCursor(path, res.Offset, res.Line, now); err != nil {
				return res, err
			}
			continue
		}
		if len(trimmed) == 0 {
			continue
		}
		ins, err := fn(trimmed)
		if err != nil {
			if errors.Is(err, ErrRetryLater) {
				k := fmt.Sprintf("%s#%d", path, res.Line)
				retries[k]++
				if retries[k] < retryPasses {
					// leave the cursor before this line; the next pass retries
					res.Read--
					res.Line--
					res.Offset -= int64(len(raw))
					res.Deferred = fmt.Sprintf("%s line %d: %v (pass %d of %d)", path, res.Line+1, err, retries[k], retryPasses)
					return res, nil
				}
				delete(retries, k)
				err = fmt.Errorf("%w: %v after %d passes", ErrBadRecord, err, retryPasses)
			}
			if !errors.Is(err, ErrBadRecord) {
				return res, fmt.Errorf("%s line %d: %w", path, res.Line, err)
			}
			res.Skipped++
			res.LastSkipped = fmt.Sprintf("%s line %d: %v", path, res.Line, err)
		} else if ins {
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
			return false, fmt.Errorf("%w: decode publication: %v", ErrBadRecord, err)
		}
		if p.PromiseHash == "" {
			return false, fmt.Errorf("%w: publication without promise_hash", ErrBadRecord)
		}
		return st.UpsertPublication(p, raw)
	}, now)
}

// Measurements ingests measurements.jsonl.
func Measurements(st *store.Store, path string, now time.Time) (Result, error) {
	return tail(st, path, func(raw []byte) (bool, error) {
		var m probe.Measurement
		if err := json.Unmarshal(raw, &m); err != nil {
			return false, fmt.Errorf("%w: decode measurement: %v", ErrBadRecord, err)
		}
		if m.PromiseHash == "" || m.ValidatorAddress == "" {
			return false, fmt.Errorf("%w: measurement without promise_hash or validator_address", ErrBadRecord)
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
	gaps := "[]"
	if len(ps.Gaps) > 0 {
		if b, err := json.Marshal(ps.Gaps); err == nil {
			gaps = string(b)
		}
	}
	for k, v := range map[string]string{
		"chain_id":                    ps.ChainID,
		"scan_start_height":           fmt.Sprint(ps.StartHeight),
		"last_scanned_height":         fmt.Sprint(ps.LastScannedHeight),
		"protocol_params_fingerprint": ps.ParamFingerprint,
		"scan_gaps":                   gaps,
		"last_scanned_time":           scannedTime(ps),
	} {
		if err := st.SetMeta(k, v, now); err != nil {
			return err
		}
	}
	return nil
}

// Reachability ingests reachability.jsonl written by observer-heartbeat.
func Reachability(st *store.Store, path string, now time.Time) (Result, error) {
	return tail(st, path, func(raw []byte) (bool, error) {
		var m probe.Measurement
		if err := json.Unmarshal(raw, &m); err != nil {
			return false, fmt.Errorf("%w: decode reachability: %v", ErrBadRecord, err)
		}
		if m.ValidatorAddress == "" {
			return false, fmt.Errorf("%w: reachability without validator_address", ErrBadRecord)
		}
		return st.InsertReachability(m, raw)
	}, now)
}

// Payments ingests payments.jsonl written by sentinel-scan.
func Payments(st *store.Store, path string, now time.Time) (Result, error) {
	return tail(st, path, func(raw []byte) (bool, error) {
		var p scan.Payment
		if err := json.Unmarshal(raw, &p); err != nil {
			return false, fmt.Errorf("%w: decode payment: %v", ErrBadRecord, err)
		}
		if p.DedupeKey == "" || p.Publisher == "" {
			return false, fmt.Errorf("%w: payment without dedupe_key or publisher", ErrBadRecord)
		}
		return st.UpsertPayment(p, raw)
	}, now)
}

// Registry replays registry.jsonl, the collector's own log of endpoint
// openings and closings, so a database rebuilt from the JSONL files keeps
// the endpoint history the live polls produced.
func Registry(st *store.Store, path string, now time.Time) (Result, error) {
	return tail(st, path, func(raw []byte) (bool, error) {
		var e store.EndpointEvent
		if err := json.Unmarshal(raw, &e); err != nil {
			return false, fmt.Errorf("%w: decode endpoint event: %v", ErrBadRecord, err)
		}
		if e.ConsAddress == "" || e.Host == "" || e.At.IsZero() {
			return false, fmt.Errorf("%w: endpoint event without address, host or time", ErrBadRecord)
		}
		return st.ReplayEndpointEvent(e)
	}, now)
}

// Runs replays runs.jsonl, every component's own record of its starts and
// stops with the configuration it ran under (status.RunEvent).
func Runs(st *store.Store, path string, now time.Time) (Result, error) {
	return tail(st, path, func(raw []byte) (bool, error) {
		var e status.RunEvent
		if err := json.Unmarshal(raw, &e); err != nil {
			return false, fmt.Errorf("%w: decode run event: %v", ErrBadRecord, err)
		}
		if e.Component == "" || e.At.IsZero() {
			return false, fmt.Errorf("%w: run event without component or time", ErrBadRecord)
		}
		return st.ReplayRunEvent(e)
	}, now)
}

// SamplingSecrets replays sampling-secrets.jsonl, the prober's reveals of
// past days' sampling secrets.
func SamplingSecrets(st *store.Store, path string, now time.Time) (Result, error) {
	return tail(st, path, func(raw []byte) (bool, error) {
		var e store.SamplingSecret
		if err := json.Unmarshal(raw, &e); err != nil {
			return false, fmt.Errorf("%w: decode sampling secret: %v", ErrBadRecord, err)
		}
		if e.Day == "" || e.Commitment == "" || e.Secret == "" || e.RevealedAt.IsZero() {
			return false, fmt.Errorf("%w: sampling secret without day, commitment, secret or time", ErrBadRecord)
		}
		return st.UpsertSamplingSecret(e)
	}, now)
}

// scannedTime is the scanner's frontier on the chain's clock, or "" when
// the state predates the field.
func scannedTime(ps scan.PersistState) string {
	if ps.LastScannedTime.IsZero() {
		return ""
	}
	return store.TS(ps.LastScannedTime)
}

// HostEvents replays host_history.jsonl, the scanner's record of every
// Fibre host registration read from the chain's events and its seed.
func HostEvents(st *store.Store, path string, now time.Time) (Result, error) {
	return tail(st, path, func(raw []byte) (bool, error) {
		var e scan.HostEvent
		if err := json.Unmarshal(raw, &e); err != nil {
			return false, fmt.Errorf("%w: decode host event: %v", ErrBadRecord, err)
		}
		if e.ConsAddress == "" || e.Source == "" {
			return false, fmt.Errorf("%w: host event without address or source", ErrBadRecord)
		}
		return st.ReplayHostEvent(e)
	}, now)
}

// Amendments replays amendments.jsonl, the collector's own log of late
// shadow verdicts, so a rebuilt database carries them without re-judging.
func Amendments(st *store.Store, path string, now time.Time) (Result, error) {
	return tail(st, path, func(raw []byte) (bool, error) {
		var a store.Amendment
		if err := json.Unmarshal(raw, &a); err != nil {
			return false, fmt.Errorf("%w: decode amendment: %v", ErrBadRecord, err)
		}
		if a.DedupeKey == "" || a.To == "" || a.JudgedAt.IsZero() {
			return false, fmt.Errorf("%w: amendment without key, verdict or time", ErrBadRecord)
		}
		ok, err := st.ApplyAmendment(a)
		if errors.Is(err, store.ErrNoSuchRow) {
			return false, fmt.Errorf("%w: %v", ErrRetryLater, err)
		}
		return ok, err
	}, now)
}
