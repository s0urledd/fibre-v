package probe

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/plsgiveup/fibre/fibre-sentinel/internal/scan"
)

func testProber(t *testing.T) *Prober {
	t.Helper()
	dir := t.TempDir()
	st, err := OpenMeasurementStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	cfg := Config{Vantage: "v1", DataDir: dir, MaxLateness: 90 * time.Second, BackfillMissed: time.Hour}.withDefaults()
	return &Prober{
		cfg: cfg, log: scan.NewLogger(50), store: st, chainID: "chain-1",
		coders: map[[2]int]*Coder{}, complete: map[string]bool{}, skippedPubs: map[string]bool{},
		valLocks: map[string]*sync.Mutex{},
	}
}

func rowFor(pub scan.Publication, vantage, addr string, pt SchedulePoint) Measurement {
	return Measurement{
		SchemaVersion: MeasurementSchemaVersion, Vantage: vantage, PromiseHash: pub.PromiseHash,
		ValidatorAddress: addr, ScheduledAt: pt.At, ScheduleLabel: pt.Label, StartedAt: pt.At,
		Phase: PhaseInWindow, Outcome: OutcomeServedOK, Classification: ClassHealthy,
	}
}

// A point with a row for only one validator is still pending: the other
// validators must be probed (or marked) on the next cycle.
func TestPlan_PartialPointStaysPending(t *testing.T) {
	p := testProber(t)
	now := time.Now().UTC()
	settle := now.Add(-5 * time.Minute)
	pubA := pub(settle, now.Add(5*time.Minute))
	pubA.Promise.ChainID = "chain-1"
	pts := ScheduleFor(pubA, p.cfg.Schedule)
	var w1 SchedulePoint
	for _, pt := range pts {
		if pt.Label == "w1" {
			w1 = pt
		}
	}
	if w1.At.IsZero() || !w1.At.Before(now) {
		t.Fatalf("expected w1 in the past: %+v (now %s)", w1, now)
	}
	if err := p.store.Append(rowFor(pubA, "v1", "aa", w1)); err != nil {
		t.Fatal(err)
	}
	due, _, missed, _, finished := p.plan([]scan.Publication{pubA}, now)
	found := false
	for _, j := range append(due, missed...) {
		if j.point.At.Equal(w1.At) {
			found = true
		}
	}
	if !found {
		t.Fatalf("w1 with one recorded validator must still be planned; due=%d missed=%d", len(due), len(missed))
	}
	if len(finished) != 0 {
		t.Fatalf("publication must not be finished: %v", finished)
	}
	// once marked complete it is not planned again
	p.complete[pointKey("v1", pubA.PromiseHash, w1.At)] = true
	due, _, missed, _, _ = p.plan([]scan.Publication{pubA}, now)
	for _, j := range append(due, missed...) {
		if j.point.At.Equal(w1.At) {
			t.Fatal("complete point was planned again")
		}
	}
}

// Slots older than the backfill horizon get no row and the publication is
// forgotten once its whole schedule is behind the horizon.
func TestPlan_BackfillHorizon(t *testing.T) {
	p := testProber(t)
	now := time.Now().UTC()
	old := pub(now.Add(-3*time.Hour), now.Add(-2*time.Hour))
	old.Promise.ChainID = "chain-1"
	due, future, missed, dropped, finished := p.plan([]scan.Publication{old}, now)
	if len(due)+len(future)+len(missed)+len(dropped) != 0 {
		t.Fatalf("old publication planned: due=%d future=%d missed=%d dropped=%d", len(due), len(future), len(missed), len(dropped))
	}
	if len(finished) != 1 || finished[0] != old.PromiseHash {
		t.Fatalf("old publication should be finished: %v", finished)
	}

	// a publication with points on both sides of the horizon: old ones silent, recent ones missed
	mid := pub(now.Add(-90*time.Minute), now.Add(-10*time.Minute))
	mid.Promise.ChainID = "chain-1"
	_, _, missed, _, finished = p.plan([]scan.Publication{mid}, now)
	if len(finished) != 0 {
		t.Fatalf("mid publication must not be finished yet")
	}
	for _, j := range missed {
		if j.point.At.Before(now.Add(-time.Hour)) {
			t.Fatalf("slot behind the horizon recorded as missed: %s", j.point.At)
		}
	}
	if len(missed) == 0 {
		t.Fatal("recent elapsed slots should be missed")
	}
}

// Publications from another chain or with a failed settlement tx are never probed.
func TestPlan_SkipsForeignAndFailed(t *testing.T) {
	p := testProber(t)
	now := time.Now().UTC()
	foreign := pub(now.Add(-time.Minute), now.Add(10*time.Minute))
	foreign.Promise.ChainID = "other-chain"
	failed := pub(now.Add(-time.Minute), now.Add(10*time.Minute))
	failed.PromiseHash = "ff00"
	failed.Promise.ChainID = "chain-1"
	failed.SettlementTxCode = 5
	due, future, _, _, _ := p.plan([]scan.Publication{foreign, failed}, now)
	if len(due)+len(future) != 0 {
		t.Fatalf("foreign/failed publications planned: due=%d future=%d", len(due), len(future))
	}
}

// A slot that aged past MaxLateness while the cycle ran is recorded
// NOT_PROBED, never probed in a later phase.
func TestRunOne_LateSlotBecomesNotProbed(t *testing.T) {
	p := testProber(t)
	now := time.Now().UTC()
	pubA := pub(now.Add(-10*time.Minute), now.Add(10*time.Minute))
	pt := SchedulePoint{At: now.Add(-3 * time.Minute), Label: "w2", Phase: PhaseInWindow}
	tg := Target{AddressHex: "aa", Host: "192.0.2.1:7980", Assigned: true, RowCount: 148}
	probed := p.runOne(context.Background(), work{job: job{pubA, pt}, target: tg, key: pointKey("v1", pubA.PromiseHash, pt.At)})
	if probed {
		t.Fatal("late slot was probed")
	}
	if err := p.store.Sync(); err != nil {
		t.Fatal(err)
	}
	ms, err := LoadMeasurements(p.store.Path())
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) != 1 || ms[0].Classification != ClassNotProbed || !strings.Contains(ms[0].ClassificationReason, "elapsed while the cycle ran") {
		t.Fatalf("got %+v", ms)
	}
}

func TestMeasurementStore_TornTailIsRepaired(t *testing.T) {
	dir := t.TempDir()
	st, err := OpenMeasurementStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	pubA := pub(time.Now(), time.Now().Add(time.Hour))
	pt := SchedulePoint{At: time.Now().UTC(), Label: "w1"}
	if err := st.Append(rowFor(pubA, "v1", "aa", pt)); err != nil {
		t.Fatal(err)
	}
	st.Close()
	f, _ := os.OpenFile(filepath.Join(dir, "measurements.jsonl"), os.O_WRONLY|os.O_APPEND, 0)
	f.WriteString(`{"vantage":"v1","promise_hash":"abc`)
	f.Close()

	st2, err := OpenMeasurementStore(dir)
	if err != nil {
		t.Fatalf("torn tail must be repaired, got %v", err)
	}
	defer st2.Close()
	if !st2.Has("v1", pubA.PromiseHash, "aa", pt.At) {
		t.Fatal("intact row lost")
	}
	ms, err := LoadMeasurements(st2.Path())
	if err != nil || len(ms) != 1 {
		t.Fatalf("after repair: %d rows, err %v", len(ms), err)
	}
	// a second append lands on a clean line
	if err := st2.Append(rowFor(pubA, "v1", "bb", pt)); err != nil {
		t.Fatal(err)
	}
	ms, err = LoadMeasurements(st2.Path())
	if err != nil || len(ms) != 2 {
		t.Fatalf("after append: %d rows, err %v", len(ms), err)
	}
	st2.Forget(pubA.PromiseHash)
	if st2.Has("v1", pubA.PromiseHash, "aa", pt.At) {
		t.Fatal("Forget did not drop the keys")
	}
}

func TestPubFeed_IncrementalAndTornLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "publications.jsonl")
	feed := newPubFeed(path)
	if _, err := feed.refresh(); err == nil {
		t.Fatal("missing file should error")
	}
	write := func(s string) {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			t.Fatal(err)
		}
		f.WriteString(s)
		f.Close()
	}
	write(`{"promise_hash":"aa","settlement_height":1}` + "\n")
	write(`{"promise_hash":"bb","settlement_hei`) // torn / in progress
	n, err := feed.refresh()
	if err != nil || n != 1 {
		t.Fatalf("first refresh: n=%d err=%v", n, err)
	}
	write(`ght":2}` + "\n")
	n, err = feed.refresh()
	if err != nil || n != 1 {
		t.Fatalf("second refresh: n=%d err=%v", n, err)
	}
	if all := feed.all(); len(all) != 2 || all[1].PromiseHash != "bb" || all[1].SettlementHeight != 2 {
		t.Fatalf("all = %+v", all)
	}
	feed.forget("aa")
	if all := feed.all(); len(all) != 1 || all[0].PromiseHash != "bb" {
		t.Fatalf("after forget = %+v", all)
	}
	// a malformed complete line is a hard error
	write("{not json}\n")
	if _, err := feed.refresh(); err == nil {
		t.Fatal("malformed complete line must error")
	}
	// rewrite (shrink) -> reload from zero
	os.WriteFile(path, []byte(`{"promise_hash":"cc"}`+"\n"), 0o644)
	if _, err := feed.refresh(); err != nil {
		t.Fatal(err)
	}
	if all := feed.all(); len(all) != 1 || all[0].PromiseHash != "cc" {
		t.Fatalf("after rewrite = %+v", all)
	}
}

func TestInWindowFractions(t *testing.T) {
	if got := InWindowFractions(4); len(got) != 4 || got[3] != 0.92 || got[0] != 0.12 {
		t.Fatalf("n=4: %v", got)
	}
	for _, n := range []int{2, 3, 5, 8} {
		got := InWindowFractions(n)
		if len(got) != n || got[n-1] < 0.9 || got[n-1] >= 1 || got[0] <= 0 {
			t.Fatalf("n=%d: %v", n, got)
		}
		for i := 1; i < n; i++ {
			if got[i] <= got[i-1] {
				t.Fatalf("n=%d not increasing: %v", n, got)
			}
		}
	}
	if c := (ScheduleConfig{}).withDefaults(); c.MinSpacing != 20*time.Second {
		t.Fatalf("zero MinSpacing should default, got %s", c.MinSpacing)
	}
}
