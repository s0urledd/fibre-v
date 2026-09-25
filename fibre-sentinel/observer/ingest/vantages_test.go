package ingest_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/plsgiveup/fibre/fibre-sentinel/observer/ingest"
	"github.com/plsgiveup/fibre/fibre-sentinel/observer/store"
)

// beatLine is one heartbeat record as observer-heartbeat writes it.
func beatLine(vantage, addr string, minute int) string {
	at := time.Date(2026, 9, 25, 10, minute, 0, 0, time.UTC).Format(time.RFC3339)
	return fmt.Sprintf(`{"vantage":%q,"validator_address":%q,"validator_host":"h:7980","scheduled_at":%q,"started_at":%q,"tcp":{"ok":true},"tls":{"ok":true},"outcome":"REACHABLE"}`,
		vantage, addr, at, at)
}

func countRows(t *testing.T, st *store.Store, vantage string) int {
	t.Helper()
	var n int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM reachability WHERE vantage = ?`, vantage).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// ingestVantages is the collector's pass over the copied files.
func ingestVantages(t *testing.T, st *store.Store, dir string) (inserted, skipped int64) {
	t.Helper()
	files, err := ingest.VantageFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		r, err := ingest.VantageReachability(st, f, "ut-1", time.Now())
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		inserted += r.Inserted
		skipped += r.Skipped
	}
	return inserted, skipped
}

// Other vantages' heartbeat files are tailed beside this observer's own, each
// on its own cursor: a copy replaced by a longer one reads only what is new, a
// half-copied last line waits, a shrunken copy re-reads without duplicating,
// a vantage directory that appears later is picked up, and the observer's own
// file keeps the cursor it had.
func TestVantageFilesAreTailedBesideTheOwnFile(t *testing.T) {
	st := openStore(t)
	data := t.TempDir()
	own := filepath.Join(data, "reachability.jsonl")
	vdir := filepath.Join(data, ingest.VantagesDir)

	// The observer's own file, ingested before any vantage exists: this is
	// the cursor an upgraded collector already holds.
	os.WriteFile(own, []byte(beatLine("ut-1", "aa", 0)+"\n"+beatLine("ut-1", "aa", 5)+"\n"), 0o644)
	if r, err := ingest.Reachability(st, own, time.Now()); err != nil || r.Inserted != 2 {
		t.Fatalf("own file: %+v %v", r, err)
	}
	ownOff, ownLine, _ := st.Cursor(own)

	// No vantages directory yet: nothing to do, and no error.
	if files, err := ingest.VantageFiles(vdir); err != nil || len(files) != 0 {
		t.Fatalf("missing dir: %v %v", files, err)
	}

	// de-1 arrives, its copy ending half-way through a line.
	de := filepath.Join(vdir, "de-1", "reachability.jsonl")
	os.MkdirAll(filepath.Dir(de), 0o755)
	third := beatLine("de-1", "aa", 10)
	os.WriteFile(de, []byte(beatLine("de-1", "aa", 0)+"\n"+beatLine("de-1", "aa", 5)+"\n"+third[:40]), 0o644)
	if ins, _ := ingestVantages(t, st, vdir); ins != 2 {
		t.Fatalf("first copy inserted %d, want 2 (the partial line waits)", ins)
	}
	if ins, _ := ingestVantages(t, st, vdir); ins != 0 {
		t.Fatalf("unchanged copy inserted %d", ins)
	}

	// rsync replaces the whole file with a longer copy: the line completes
	// and one more follows.
	grown := beatLine("de-1", "aa", 0) + "\n" + beatLine("de-1", "aa", 5) + "\n" + third + "\n" + beatLine("de-1", "aa", 15) + "\n"
	os.WriteFile(de+".tmp", []byte(grown), 0o644)
	os.Rename(de+".tmp", de)
	if ins, _ := ingestVantages(t, st, vdir); ins != 2 {
		t.Fatalf("grown copy inserted %d, want 2", ins)
	}

	// A copy shorter than the cursor (the sender's file was replaced) is
	// read again from the top, and the keys keep it from duplicating.
	os.WriteFile(de, []byte(beatLine("de-1", "aa", 0)+"\n"), 0o644)
	if ins, _ := ingestVantages(t, st, vdir); ins != 0 {
		t.Fatalf("shrunken copy inserted %d", ins)
	}
	os.WriteFile(de, []byte(grown), 0o644)
	if ins, _ := ingestVantages(t, st, vdir); ins != 0 {
		t.Fatalf("re-grown copy inserted %d", ins)
	}

	// A second vantage directory appears between passes. A row in it that
	// claims this observer's own vantage is stepped over, not counted as ours.
	us := filepath.Join(vdir, "us-2", "reachability.jsonl")
	os.MkdirAll(filepath.Dir(us), 0o755)
	os.WriteFile(us, []byte(beatLine("us-2", "aa", 0)+"\n"+beatLine("ut-1", "aa", 20)+"\n"), 0o644)
	ins, skipped := ingestVantages(t, st, vdir)
	if ins != 1 || skipped != 1 {
		t.Fatalf("new vantage: inserted %d skipped %d, want 1 and 1", ins, skipped)
	}

	if n := countRows(t, st, "de-1"); n != 4 {
		t.Errorf("de-1 rows = %d, want 4", n)
	}
	if n := countRows(t, st, "us-2"); n != 1 {
		t.Errorf("us-2 rows = %d, want 1", n)
	}
	if n := countRows(t, st, "ut-1"); n != 2 {
		t.Errorf("own rows = %d, want 2: another file's line was counted as this observer's", n)
	}

	// Each file has its own cursor, and the own file's did not move.
	if off, line, _ := st.Cursor(own); off != ownOff || line != ownLine {
		t.Errorf("own cursor moved: %d/%d, was %d/%d", off, line, ownOff, ownLine)
	}
	if off, _, _ := st.Cursor(de); off != int64(len(grown)) {
		t.Errorf("de-1 cursor = %d, want %d", off, len(grown))
	}
	if r, err := ingest.Reachability(st, own, time.Now()); err != nil || r.Read != 0 {
		t.Errorf("own file re-read after the vantage passes: %+v %v", r, err)
	}
}
