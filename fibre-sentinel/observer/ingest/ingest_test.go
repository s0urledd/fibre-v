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

// An amendment whose probe row is not in the store yet is not stepped over:
// the pass stops before it and the next passes retry, so a measurements
// file that is merely behind does not lose its late verdict; a row that
// never arrives is skipped after a bounded number of passes, so it cannot
// stall the file either.
func TestAmendmentForAMissingRowIsRetriedThenSkipped(t *testing.T) {
	st := openStore(t)
	dir := t.TempDir()
	mpath, apath := filepath.Join(dir, "measurements.jsonl"), filepath.Join(dir, "amendments.jsonl")
	amend := `{"dedupe_key":"t|aa|bb|2026-09-02T23:14:02Z","promise_hash":"aa","validator_address":"bb","scheduled_at":"2026-09-02T23:14:02Z","from":"PROBE_ERROR","to":"UNMATCHED_GENUINE","reason":"test","judged_at":"2026-09-03T01:00:00Z","scanner_frontier":"2026-09-03T00:59:00Z"}`
	os.WriteFile(apath, []byte(amend+"\n"), 0o644)
	r, err := ingest.Amendments(st, apath, time.Now())
	if err != nil || r.Deferred == "" || r.Line != 0 {
		t.Fatalf("first pass without the row: %+v err=%v, want deferred with the cursor held", r, err)
	}
	// the row arrives; the retried amendment applies
	os.WriteFile(mpath, []byte(good+"\n"), 0o644)
	if _, err := ingest.Measurements(st, mpath, time.Now()); err != nil {
		t.Fatal(err)
	}
	r, err = ingest.Amendments(st, apath, time.Now())
	if err != nil || r.Inserted != 1 || r.Deferred != "" || r.Line != 1 {
		t.Fatalf("second pass with the row: %+v err=%v", r, err)
	}
	var cls string
	if err := st.DB().QueryRow(`SELECT classification FROM probes WHERE dedupe_key = ?`, "t|aa|bb|2026-09-02T23:14:02Z").Scan(&cls); err != nil || cls != "UNMATCHED_GENUINE" {
		t.Fatalf("classification after the retried amendment = %q err=%v", cls, err)
	}

	// a row that never arrives: retried a bounded number of passes, then skipped
	orphan := strings.Replace(amend, `"t|aa|bb|2026-09-02T23:14:02Z"`, `"t|zz|bb|2026-09-02T23:14:02Z"`, 1)
	f, _ := os.OpenFile(apath, os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString(orphan + "\n")
	f.Close()
	skipped := false
	for i := 0; i < 5; i++ {
		r, err = ingest.Amendments(st, apath, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		if r.Skipped == 1 {
			skipped = true
			break
		}
	}
	if !skipped || r.Line != 2 {
		t.Fatalf("an orphan amendment must be skipped after bounded retries: %+v", r)
	}
}
