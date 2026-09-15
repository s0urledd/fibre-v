package ingest_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/plsgiveup/fibre/fibre-sentinel/observer/ingest"
	"github.com/plsgiveup/fibre/fibre-sentinel/observer/store"
)

const good = `{"vantage":"t","promise_hash":"aa","validator_address":"bb","scheduled_at":"2026-09-02T23:14:02Z","started_at":"2026-09-02T23:14:02Z","phase":"in_window","outcome":"NOT_FOUND","classification":"FAULT"}`

func openStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "o.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestUndecodableLineIsSkipped(t *testing.T) {
	st := openStore(t)
	path := filepath.Join(t.TempDir(), "measurements.jsonl")
	os.WriteFile(path, []byte(`{"vantage":"t","promise_hash":"x`+"\n"+good+"\n"), 0o644)
	r, err := ingest.Measurements(st, path, time.Now())
	if err != nil {
		t.Fatalf("bad line must not stop the pass: %v", err)
	}
	if r.Skipped != 1 || r.Inserted != 1 || r.Line != 2 || r.LastSkipped == "" {
		t.Fatalf("result = %+v", r)
	}
	// the cursor moved past both lines
	r, err = ingest.Measurements(st, path, time.Now())
	if err != nil || r.Read != 0 {
		t.Fatalf("second pass: %+v err=%v", r, err)
	}
}

func TestPartialLineAndTruncation(t *testing.T) {
	st := openStore(t)
	path := filepath.Join(t.TempDir(), "measurements.jsonl")
	os.WriteFile(path, []byte(good), 0o644) // no newline yet
	r, err := ingest.Measurements(st, path, time.Now())
	if err != nil || r.Read != 0 || r.Inserted != 0 {
		t.Fatalf("partial line ingested: %+v err=%v", r, err)
	}
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString("\n")
	f.Close()
	r, err = ingest.Measurements(st, path, time.Now())
	if err != nil || r.Inserted != 1 {
		t.Fatalf("completed line: %+v err=%v", r, err)
	}
	// a rewrite that shrinks the file below the cursor restarts from zero;
	// idempotent keys mean the old row is not duplicated and the new one lands.
	short := strings.Replace(good, `"validator_address":"bb"`, `"validator_address":"c"`, 1)
	os.WriteFile(path, []byte(short+"\n"), 0o644)
	r, err = ingest.Measurements(st, path, time.Now())
	if err != nil || r.Inserted != 1 || r.Read != 1 || r.Line != 1 {
		t.Fatalf("after rewrite: %+v err=%v", r, err)
	}
	r, _ = ingest.Measurements(st, path, time.Now())
	if r.Read != 0 {
		t.Fatalf("cursor not at EOF after rewrite: %+v", r)
	}
}
