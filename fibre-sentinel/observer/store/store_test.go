package store_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/plsgiveup/fibre/fibre-sentinel/internal/scan"
	"github.com/plsgiveup/fibre/fibre-sentinel/observer/ingest"
	"github.com/plsgiveup/fibre/fibre-sentinel/observer/store"
)

const sampleDir = "../../sample"

func open(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "observer.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

// TestIngestSampleIsIdempotent ingests the committed devnet run twice and
// checks the second pass inserts nothing.
func TestIngestSampleIsIdempotent(t *testing.T) {
	st := open(t)
	now := time.Now()

	pubs, err := ingest.Publications(st, filepath.Join(sampleDir, "publications.jsonl"), now)
	if err != nil {
		t.Fatal(err)
	}
	if pubs.Inserted == 0 || pubs.Inserted != pubs.Read {
		t.Fatalf("publications: read=%d inserted=%d", pubs.Read, pubs.Inserted)
	}
	meas, err := ingest.Measurements(st, filepath.Join(sampleDir, "probe", "measurements.jsonl"), now)
	if err != nil {
		t.Fatal(err)
	}
	if meas.Inserted != 60 {
		t.Fatalf("measurements: want 60 inserted (the README run), got read=%d inserted=%d", meas.Read, meas.Inserted)
	}
	if err := ingest.State(st, filepath.Join(sampleDir, "state.json"), now); err != nil {
		t.Fatal(err)
	}

	c, err := st.Count(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if c.Publications != pubs.Inserted || c.Probes != 60 || c.Assignments != pubs.Inserted*4 {
		t.Fatalf("counts after first pass: %+v", c)
	}

	// second pass: cursors are at EOF, nothing read.
	pubs2, err := ingest.Publications(st, filepath.Join(sampleDir, "publications.jsonl"), now)
	if err != nil {
		t.Fatal(err)
	}
	if pubs2.Read != 0 || pubs2.Inserted != 0 {
		t.Fatalf("second pass read=%d inserted=%d", pubs2.Read, pubs2.Inserted)
	}

	// reset the cursor and re-read: rows are re-read but not re-inserted.
	if err := st.SetCursor(filepath.Join(sampleDir, "probe", "measurements.jsonl"), 0, 0, now); err != nil {
		t.Fatal(err)
	}
	meas2, err := ingest.Measurements(st, filepath.Join(sampleDir, "probe", "measurements.jsonl"), now)
	if err != nil {
		t.Fatal(err)
	}
	if meas2.Read != 60 || meas2.Inserted != 0 {
		t.Fatalf("re-read read=%d inserted=%d", meas2.Read, meas2.Inserted)
	}

	// the sample run's classification distribution is documented in the README.
	var n int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM probes WHERE classification = 'FAULT'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 9 {
		t.Fatalf("FAULT rows: want 9, got %d", n)
	}
	if v, _ := st.Meta("chain_id"); v != "fibre-devnet" {
		t.Fatalf("meta chain_id = %q", v)
	}
	var params int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM params_history`).Scan(&params); err != nil {
		t.Fatal(err)
	}
	if params == 0 {
		t.Fatal("no params_history rows")
	}
}

func TestEndpointHistory(t *testing.T) {
	st := open(t)
	ctx := context.Background()
	t0 := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)

	snap := []scan.FibreProvider{
		{ConsAddressBech32: "celestiavalcons1aaa", Host: "1.2.3.4:7980"},
		{ConsAddressBech32: "celestiavalcons1bbb", Host: "5.6.7.8:7980"},
	}
	opened, closed, err := st.ObserveEndpoints(ctx, snap, 100, t0)
	if err != nil || opened != 2 || closed != 0 {
		t.Fatalf("first snapshot: opened=%d closed=%d err=%v", opened, closed, err)
	}
	// same snapshot again: nothing opens or closes, last_seen advances.
	opened, closed, err = st.ObserveEndpoints(ctx, snap, 110, t0.Add(time.Minute))
	if err != nil || opened != 0 || closed != 0 {
		t.Fatalf("repeat snapshot: opened=%d closed=%d err=%v", opened, closed, err)
	}
	// validator bbb changes host, validator aaa disappears.
	snap = []scan.FibreProvider{{ConsAddressBech32: "celestiavalcons1bbb", Host: "9.9.9.9:7980"}}
	opened, closed, err = st.ObserveEndpoints(ctx, snap, 120, t0.Add(2*time.Minute))
	if err != nil || opened != 1 || closed != 2 {
		t.Fatalf("change snapshot: opened=%d closed=%d err=%v", opened, closed, err)
	}
	cur, err := st.CurrentEndpoints(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(cur) != 1 || cur[0].Host != "9.9.9.9:7980" || cur[0].LastSeenHeight != 120 {
		t.Fatalf("current endpoints: %+v", cur)
	}
	var total int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM endpoints`).Scan(&total); err != nil {
		t.Fatal(err)
	}
	if total != 3 {
		t.Fatalf("history rows: want 3, got %d", total)
	}
}

func TestRuns(t *testing.T) {
	st := open(t)
	t0 := time.Now()
	id, err := st.StartRun("collector", "local", "test", t0)
	if err != nil || id == 0 {
		t.Fatal(err)
	}
	if err := st.Heartbeat(id, t0.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := st.StopRun(id, t0.Add(2*time.Second), "test"); err != nil {
		t.Fatal(err)
	}
	var stopped *string
	if err := st.DB().QueryRow(`SELECT stopped_at FROM observer_runs WHERE id = ?`, id).Scan(&stopped); err != nil {
		t.Fatal(err)
	}
	if stopped == nil {
		t.Fatal("stopped_at not set")
	}
}
