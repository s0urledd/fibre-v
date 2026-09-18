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

const sampleDir = "../testdata"

func open(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "observer.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

// TestIngestSampleIsIdempotent ingests the committed devnet fixture twice and
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
	meas, err := ingest.Measurements(st, filepath.Join(sampleDir, "measurements.jsonl"), now)
	if err != nil {
		t.Fatal(err)
	}
	if meas.Inserted != 60 {
		t.Fatalf("measurements: want 60 inserted (testdata/README.md), got read=%d inserted=%d", meas.Read, meas.Inserted)
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
	if err := st.SetCursor(filepath.Join(sampleDir, "measurements.jsonl"), 0, 0, now); err != nil {
		t.Fatal(err)
	}
	meas2, err := ingest.Measurements(st, filepath.Join(sampleDir, "measurements.jsonl"), now)
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

func TestTimestampOrdering(t *testing.T) {
	base := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	a := store.TS(base)                             // zero nanoseconds
	b := store.TS(base.Add(500 * time.Millisecond)) // half a second later
	c := store.TS(base.Add(time.Second))
	if !(a < b && b < c) {
		t.Fatalf("not chronological as text: %q %q %q", a, b, c)
	}
	if len(a) != len(b) || len(b) != len(c) {
		t.Fatalf("not fixed width: %q %q %q", a, b, c)
	}
	if _, err := time.Parse(time.RFC3339Nano, b); err != nil {
		t.Fatalf("not RFC 3339: %v", err)
	}
}

func TestOpenReadOnly(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "observer.db")
	if _, err := store.OpenReadOnly(path); err == nil {
		t.Fatal("read-only open of a missing database must fail")
	}
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ingest.Publications(st, filepath.Join(sampleDir, "publications.jsonl"), time.Now()); err != nil {
		t.Fatal(err)
	}
	st.Close()
	ro, err := store.OpenReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	defer ro.Close()
	c, err := ro.Count(context.Background())
	if err != nil || c.Publications == 0 {
		t.Fatalf("counts via read-only handle: %+v err=%v", c, err)
	}
	if _, err := ro.DB().Exec(`INSERT INTO meta (key, value, updated_at) VALUES ('x','y','z')`); err == nil {
		t.Fatal("write through a read-only handle succeeded")
	}
}

// registry.jsonl replay: the events one store produced rebuild the same
// endpoint history in an empty store, and replaying them again changes
// nothing.
func TestEndpointEventsReplay(t *testing.T) {
	a := open(t)
	ctx := context.Background()
	t0 := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	var log []store.EndpointEvent
	step := func(snap []scan.FibreProvider, h int64, at time.Time) {
		evs, err := a.ObserveEndpointEvents(ctx, snap, h, at)
		if err != nil {
			t.Fatal(err)
		}
		log = append(log, evs...)
	}
	step([]scan.FibreProvider{{ConsAddressBech32: "celestiavalcons1aaa", Host: "1.2.3.4:7980"}, {ConsAddressBech32: "celestiavalcons1bbb", Host: "5.6.7.8:7980"}}, 100, t0)
	step([]scan.FibreProvider{{ConsAddressBech32: "celestiavalcons1bbb", Host: "9.9.9.9:7980"}}, 120, t0.Add(2*time.Minute))
	if len(log) != 5 {
		t.Fatalf("want 5 events (2 opens, 2 closes, 1 open), got %d: %+v", len(log), log)
	}

	b := open(t)
	changed := 0
	for _, e := range log {
		ok, err := b.ReplayEndpointEvent(e)
		if err != nil {
			t.Fatal(err)
		}
		if ok {
			changed++
		}
	}
	if changed != 5 {
		t.Fatalf("replay changed %d rows, want 5", changed)
	}
	for _, e := range log {
		if ok, err := b.ReplayEndpointEvent(e); err != nil || ok {
			t.Fatalf("second replay must be a no-op: ok=%v err=%v", ok, err)
		}
	}
	rowsOf := func(s *store.Store) string {
		rows, err := s.DB().Query(`SELECT validator_cons_address, host, first_seen_at, first_seen_height, COALESCE(closed_at,''), COALESCE(closed_height,0)
			FROM endpoints ORDER BY validator_cons_address, first_seen_at`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		out := ""
		for rows.Next() {
			var addr, host, first, closed string
			var fh, ch int64
			if err := rows.Scan(&addr, &host, &first, &fh, &closed, &ch); err != nil {
				t.Fatal(err)
			}
			out += addr + "|" + host + "|" + first + "|" + closed + "\n"
		}
		return out
	}
	if rowsOf(a) != rowsOf(b) {
		t.Fatalf("replayed history differs:\n%s\n--\n%s", rowsOf(a), rowsOf(b))
	}
	cur, err := b.CurrentEndpoints(ctx)
	if err != nil || len(cur) != 1 || cur[0].Host != "9.9.9.9:7980" {
		t.Fatalf("current after replay: %+v %v", cur, err)
	}
}

// A validator's Keybase picture is held per identity, refreshed after the
// max age, and only a picture that is actually held is served.
func TestAvatars(t *testing.T) {
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	now := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	ids := []scan.ValidatorIdentity{
		{ConsAddressHex: "aa", Moniker: "with picture", Identity: "D27EE330254D4F6A", Status: "BOND_STATUS_BONDED"},
		{ConsAddressHex: "bb", Moniker: "same keybase", Identity: "D27EE330254D4F6A", Status: "BOND_STATUS_BONDED"},
		{ConsAddressHex: "cc", Moniker: "no identity", Identity: "", Status: "BOND_STATUS_BONDED"},
		{ConsAddressHex: "dd", Moniker: "a name, not a suffix", Identity: "huginn.tech", Status: "BOND_STATUS_BONDED"},
	}
	if _, err := st.UpsertValidatorIdentities(ids, now); err != nil {
		t.Fatal(err)
	}
	due, err := st.AvatarsDue(ctx, now, 24*time.Hour, 100)
	if err != nil || len(due) != 1 || due[0] != "D27EE330254D4F6A" {
		t.Fatalf("due = %v (%v), want the one well-formed suffix once", due, err)
	}
	if err := st.PutAvatar("D27EE330254D4F6A", "ok", "https://x/pic.jpg", "image/jpeg", []byte("jpeg"), now); err != nil {
		t.Fatal(err)
	}
	if due, _ := st.AvatarsDue(ctx, now.Add(time.Hour), 24*time.Hour, 100); len(due) != 0 {
		t.Fatalf("freshly resolved identity due again: %v", due)
	}
	if due, _ := st.AvatarsDue(ctx, now.Add(25*time.Hour), 24*time.Hour, 100); len(due) != 1 {
		t.Fatalf("stale identity not due: %v", due)
	}
	ct, data, checked, ok, err := st.Avatar(ctx, "D27EE330254D4F6A")
	if err != nil || !ok || ct != "image/jpeg" || string(data) != "jpeg" || !checked.Equal(now) {
		t.Fatalf("avatar = %q %q %s ok=%v err=%v", ct, data, checked, ok, err)
	}
	if err := st.PutAvatar("D27EE330254D4F6A", "none", "", "", nil, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, _, _, ok, _ := st.Avatar(ctx, "D27EE330254D4F6A"); ok {
		t.Fatal("a picture Keybase no longer has is still served")
	}
}
