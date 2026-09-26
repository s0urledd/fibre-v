package probe

import (
	"sync"
	"testing"
	"time"

	"github.com/plsgiveup/fibre/fibre-sentinel/internal/record"
	"github.com/plsgiveup/fibre/fibre-sentinel/internal/scan"
)

// proberAt is testProber over an existing data dir, as a restart sees it.
func proberAt(t *testing.T, dir string) *Prober {
	t.Helper()
	st, err := OpenMeasurementStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	so, err := OpenSampledOutStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { so.Close() })
	cfg := Config{Vantage: "v1", DataDir: dir, MaxLateness: 90 * time.Second}.withDefaults() // no backfill horizon
	return &Prober{
		cfg: cfg, log: scan.NewLogger(50), store: st, sampled: so, chainID: "chain-1",
		coders: map[[2]int]*Coder{}, complete: map[string]bool{}, skippedPubs: map[string]bool{},
		valLocks: map[string]*sync.Mutex{},
	}
}

// A prober restarted after its measurements were archived loads only the
// live file, and must not take the archived slots for unrecorded ones:
// with no backfill horizon it would write a NOT_PROBED row for every one
// of them. A publication whose rows may be archived is finished; one whose
// schedule began after the archive's cutoff has every row live and is
// planned exactly as before.
func TestRestartAfterArchiveDoesNotReplanArchivedSlots(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().UTC()
	old := pub(now.Add(-10*24*time.Hour), now.Add(-10*24*time.Hour+4*time.Hour))
	old.PromiseHash = "01d"
	old.Promise.ChainID = "chain-1"
	recent := pub(now.Add(-time.Hour), now.Add(3*time.Hour))
	recent.PromiseHash = "4ec"
	recent.Promise.ChainID = "chain-1"

	p := proberAt(t, dir)
	oldPts := ScheduleFor(old, p.cfg.Schedule)
	for _, pt := range oldPts {
		if err := p.store.Append(rowFor(old, "v1", "aa", pt)); err != nil {
			t.Fatal(err)
		}
	}
	w1 := ScheduleFor(recent, p.cfg.Schedule)[0]
	if err := p.store.Append(rowFor(recent, "v1", "aa", w1)); err != nil {
		t.Fatal(err)
	}
	p.store.Close()
	p.sampled.Close()

	res, err := record.Archive(p.store.Path(), record.Options{
		Cutoff: now.Add(-7 * 24 * time.Hour), TimeField: "scheduled_at", Limit: -1})
	if err != nil || res.Lines != int64(len(oldPts)) {
		t.Fatalf("archive: %+v %v", res, err)
	}

	r := proberAt(t, dir)
	if r.store.Has("v1", old.PromiseHash, "aa", oldPts[0].At) {
		t.Fatal("an archived row is in the restart index; the test proves nothing")
	}
	if !r.store.Has("v1", recent.PromiseHash, "aa", w1.At) {
		t.Fatal("a live row is missing from the restart index")
	}

	// Without the archive's horizon the old publication's every slot looks
	// unrecorded: this is the duplicate the horizon prevents.
	_, _, missed, _, _ := r.plan([]scan.Publication{old}, now)
	if len(missed) != len(oldPts) {
		t.Fatalf("control: %d missed without the horizon, want %d", len(missed), len(oldPts))
	}

	r.liveSince = r.recordLiveSince()
	if r.liveSince.IsZero() {
		t.Fatal("no live-since read from the archive index")
	}
	due, future, missed, dropped, finished := r.plan([]scan.Publication{old, recent}, now)
	for _, j := range append(append(due, future...), missed...) {
		if j.pub.PromiseHash == old.PromiseHash {
			t.Fatalf("archived publication planned again at %s", j.point.At)
		}
	}
	if len(dropped) != 0 || len(finished) != 1 || finished[0] != old.PromiseHash {
		t.Fatalf("finished %v dropped %d", finished, len(dropped))
	}
	if len(future) == 0 {
		t.Fatal("the live publication's future points were not planned")
	}
}

// archivedFrom draws the line at the earlier of the first schedule point
// and the settlement, less the clock skew.
func TestArchivedFrom(t *testing.T) {
	since := time.Date(2026, 9, 18, 0, 0, 0, 0, time.UTC)
	p := pub(since.Add(2*time.Hour), since.Add(6*time.Hour))
	pts := ScheduleFor(p, ScheduleConfig{})
	if archivedFrom(p, pts, time.Time{}) {
		t.Fatal("nothing archived yet, yet archived")
	}
	if archivedFrom(p, pts, since) {
		t.Fatal("settled two hours after the cutoff: every row is live")
	}
	p = pub(since.Add(30*time.Minute), since.Add(4*time.Hour))
	if !archivedFrom(p, ScheduleFor(p, ScheduleConfig{}), since) {
		t.Fatal("within the clock skew of the cutoff: a decision row may be archived")
	}
}
