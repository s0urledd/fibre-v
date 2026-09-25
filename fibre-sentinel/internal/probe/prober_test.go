package probe

import (
	"context"
	"crypto/ed25519"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
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
	so, err := OpenSampledOutStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { so.Close() })
	cfg := Config{Vantage: "v1", DataDir: dir, MaxLateness: 90 * time.Second, BackfillMissed: time.Hour}.withDefaults()
	return &Prober{
		cfg: cfg, log: scan.NewLogger(50), store: st, sampled: so, chainID: "chain-1",
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

// With no backfill horizon (the default) every elapsed slot gets a
// NOT_PROBED row, however old, so an obligation the prober never reached is
// counted as unobserved instead of vanishing from the obligation total.
func TestPlan_NoHorizonBackfillsEverySlot(t *testing.T) {
	p := testProber(t)
	p.cfg.BackfillMissed = 0
	now := time.Now().UTC()
	old := pub(now.Add(-30*time.Hour), now.Add(-26*time.Hour))
	old.Promise.ChainID = "chain-1"
	due, _, missed, _, finished := p.plan([]scan.Publication{old}, now)
	if len(finished) != 0 {
		t.Fatalf("a publication with unrecorded slots must not be finished: %v", finished)
	}
	if len(due) != 0 || len(missed) != len(ScheduleFor(old, p.cfg.Schedule)) {
		t.Fatalf("every elapsed slot should be missed: due=%d missed=%d", len(due), len(missed))
	}
}

// The lateness allowance follows the blob's own window: ninety seconds on
// a ten-minute devnet window, a share of the window on a four-hour one, so
// a point of a hundred validators is not filed NOT_PROBED at its tail
// because a few dead endpoints held the worker pool.
func TestLatenessFor_FollowsTheWindow(t *testing.T) {
	p := testProber(t)
	p.cfg.MaxLateness, p.cfg.MaxLatenessFraction = 90*time.Second, 0.05
	now := time.Now().UTC()
	short := pub(now, now.Add(10*time.Minute))
	if got := p.latenessFor(short); got != 90*time.Second {
		t.Fatalf("10 min window: %s, want 90s", got)
	}
	long := pub(now, now.Add(4*time.Hour))
	if got := p.latenessFor(long); got != 12*time.Minute {
		t.Fatalf("4 h window: %s, want 12m", got)
	}
	p.cfg.MaxLatenessFraction = 0
	if got := p.latenessFor(long); got != 90*time.Second {
		t.Fatalf("fraction off: %s, want 90s", got)
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
	probed, _ := p.runOne(context.Background(), work{job: job{pubA, pt}, target: tg, key: pointKey("v1", pubA.PromiseHash, pt.At)})
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

// Concurrency is a count of probes, which says nothing about memory:
// DownloadShard is unary, so one in-flight probe holds the whole shard twice,
// and a validator assigned every row of a large blob holds hundreds of MiB.
// The byte budget is what keeps eight of those from being resident at once,
// and it must never deadlock on an item bigger than itself.
func TestByteSemBoundsInFlightBytes(t *testing.T) {
	const limit = 100
	b := newByteSem(limit)

	// Under the limit, several at once.
	b.acquire(40)
	b.acquire(40)
	third := make(chan struct{})
	go func() { b.acquire(40); close(third) }()
	select {
	case <-third:
		t.Fatal("a third item was admitted past the budget")
	case <-time.After(50 * time.Millisecond):
	}
	b.release(40)
	select {
	case <-third:
	case <-time.After(2 * time.Second):
		t.Fatal("the waiter was not woken when room was freed")
	}
	b.release(40)
	b.release(40)

	// An item heavier than the whole budget runs alone rather than waiting
	// for room that can never exist.
	done := make(chan struct{})
	go func() { b.acquire(limit * 10); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("an item larger than the budget deadlocked")
	}
	b.release(limit * 10)

	// And it did hold the budget while it ran: the next item waits.
	b.acquire(limit * 10)
	after := make(chan struct{})
	go func() { b.acquire(1); close(after) }()
	select {
	case <-after:
		t.Fatal("an item was admitted beside one that had taken the whole budget")
	case <-time.After(50 * time.Millisecond):
	}
	b.release(limit * 10)
	select {
	case <-after:
	case <-time.After(2 * time.Second):
		t.Fatal("the waiter was not woken")
	}
}

// A sweep starts the probe that must start soonest first: the last
// in-window point, whose allowance ends at the deadline, ahead of early
// points with minutes to spare, whatever the rotation. Within one deadline
// the rotation still decides, so the loss a short sweep takes is spread.
func TestOrderItems_EarliestDeadlineFirst(t *testing.T) {
	now := time.Now().UTC()
	mk := func(addr string, dl time.Duration) work {
		return work{target: Target{AddressHex: addr}, deadline: now.Add(dl)}
	}
	for sweep := uint64(0); sweep < 6; sweep++ {
		items := []work{mk("early1", 12*time.Minute), mk("early2", 12*time.Minute), mk("w4-a", 2*time.Minute),
			mk("w4-b", 2*time.Minute), mk("early3", 12*time.Minute), mk("grace", 5*time.Minute)}
		orderItems(items, sweep)
		got := []string{}
		for _, it := range items {
			got = append(got, it.target.AddressHex)
		}
		if !strings.HasPrefix(got[0], "w4-") || !strings.HasPrefix(got[1], "w4-") || got[2] != "grace" {
			t.Fatalf("sweep %d: %v, want the two w4 probes, then grace, then the rest", sweep, got)
		}
	}
	// the rotation still moves which validator of a point goes first
	a := []work{mk("x", time.Minute), mk("y", time.Minute)}
	b := []work{mk("x", time.Minute), mk("y", time.Minute)}
	orderItems(a, 0)
	orderItems(b, 1)
	if a[0].target.AddressHex == b[0].target.AddressHex {
		t.Fatalf("the rotation no longer varies the order within a deadline")
	}
}

// A probe that waited for this validator's previous one past its allowance
// is not started: it would be judged in whatever phase it landed in.
func TestRunOne_LatenessIsCheckedAgainUnderTheValidatorLock(t *testing.T) {
	p := testProber(t)
	now := time.Now().UTC()
	pubA := pub(now.Add(-10*time.Minute), now.Add(10*time.Minute))
	// inside the allowance by 300 ms when runOne is called
	pt := SchedulePoint{At: now.Add(-p.cfg.MaxLateness + 300*time.Millisecond), Label: "w2", Phase: PhaseInWindow}
	tg := Target{AddressHex: "aa", Host: "192.0.2.1:7980", Assigned: true, RowCount: 148}
	p.feed = newPubFeed(filepath.Join(t.TempDir(), "publications.jsonl"))
	lock := p.validatorLock("aa")
	lock.Lock() // the validator's previous probe
	res := make(chan bool, 1)
	go func() {
		probed, retry := p.runOne(context.Background(), work{job: job{pubA, pt}, target: tg, key: pointKey("v1", pubA.PromiseHash, pt.At)})
		res <- probed || retry != nil
	}()
	time.Sleep(600 * time.Millisecond)
	lock.Unlock()
	if <-res {
		t.Fatal("a slot that went past its allowance waiting for the lock was probed")
	}
	if err := p.store.Sync(); err != nil {
		t.Fatal(err)
	}
	ms, err := LoadMeasurements(p.store.Path())
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) != 1 || ms[0].Classification != ClassNotProbed || !strings.Contains(ms[0].ClassificationReason, "previous probe") {
		t.Fatalf("got %+v", ms)
	}
}

// A queued retry whose time came after the phase moved on records the first
// attempt as it stood: a second attempt in another phase would change the
// verdict, not add evidence.
func TestRunRetry_AcrossAPhaseBoundaryRecordsTheFirstAttempt(t *testing.T) {
	p := testProber(t)
	now := time.Now().UTC()
	pubA := pub(now.Add(-4*time.Hour), now.Add(-time.Second)) // the window just ended
	pt := SchedulePoint{At: now.Add(-time.Minute), Label: "w4", Phase: PhaseInWindow}
	tg := Target{AddressHex: "aa", Host: "192.0.2.1:7980", Assigned: true, RowCount: 148}
	first := rowFor(pubA, "v1", "aa", pt)
	first.Outcome, first.Classification, first.FinishedAt = OutcomeTCPTimeout, ClassUnreachable, now.Add(-20*time.Second)
	it := work{job: job{pubA, pt}, target: tg, key: pointKey("v1", pubA.PromiseHash, pt.At)}
	p.runRetry(context.Background(), retryReq{it: it, first: first, at: now})
	if err := p.store.Sync(); err != nil {
		t.Fatal(err)
	}
	ms, err := LoadMeasurements(p.store.Path())
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) != 1 || ms[0].Retry != nil || ms[0].Outcome != OutcomeTCPTimeout || ms[0].Phase != PhaseInWindow {
		t.Fatalf("got %+v", ms)
	}
}

// recPolicy is a Policy whose BeforeProbe answer the test sets, and which
// records every AfterProbe it is told about.
type recPolicy struct {
	mu            sync.Mutex
	allow, skipDL bool
	reason        string
	after         []Measurement
	asked         []Target
	released      int
}

func (r *recPolicy) Admit(scan.Publication, bool) (bool, string) { return true, "" }
func (r *recPolicy) BeforeProbe(_ scan.Publication, t Target, _ time.Time) (bool, bool, string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.asked = append(r.asked, t)
	return r.allow, r.skipDL, r.reason
}
func (r *recPolicy) AfterProbe(_ scan.Publication, m Measurement) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.after = append(r.after, m)
}
func (r *recPolicy) Release(scan.Publication, Target) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.released++
}
func (r *recPolicy) SamplingFor(scan.Publication) (float64, string, string) { return 1, "", "" }
func (r *recPolicy) Forget(string)                                          {}

// A queued retry asks the policy again when its time comes. Between the
// first attempt and the retry, other probes of the validator can push it
// into backoff or use up a budget; the retry then does not run, the first
// attempt is recorded with the reason, and the policy is not told about the
// first attempt a second time (it was told when the retry was queued).
func TestRunRetry_AsksThePolicyAgainAndRecordsTheFirstAttemptWhenItSaysNo(t *testing.T) {
	for _, c := range []struct {
		name          string
		allow, skipDL bool
		reason        string
	}{
		{"backed off meanwhile", true, true, "backoff:transport:k=3"},
		{"budget used up meanwhile", false, false, "budget:validator_requests_per_minute=6"},
	} {
		t.Run(c.name, func(t *testing.T) {
			p := testProber(t)
			pol := &recPolicy{allow: c.allow, skipDL: c.skipDL, reason: c.reason}
			p.cfg.Policy = pol
			now := time.Now().UTC()
			pubA := pub(now.Add(-2*time.Hour), now.Add(time.Hour)) // still in window
			pt := SchedulePoint{At: now.Add(-time.Minute), Label: "w2", Phase: PhaseInWindow}
			tg := Target{AddressHex: "aa", Host: "192.0.2.1:7980", Assigned: true, RowCount: 148}
			first := rowFor(pubA, "v1", "aa", pt)
			first.Outcome, first.Classification, first.FinishedAt = OutcomeTCPTimeout, ClassUnreachable, now.Add(-20*time.Second)
			first.ClassificationReason = "tcp connect timed out"
			it := work{job: job{pubA, pt}, target: tg, key: pointKey("v1", pubA.PromiseHash, pt.At)}

			p.runRetry(context.Background(), retryReq{it: it, first: first, at: now})

			if err := p.store.Sync(); err != nil {
				t.Fatal(err)
			}
			ms, err := LoadMeasurements(p.store.Path())
			if err != nil {
				t.Fatal(err)
			}
			if len(ms) != 1 || ms[0].Retry != nil || ms[0].Outcome != OutcomeTCPTimeout ||
				!strings.Contains(ms[0].ClassificationReason, "retry not run: "+c.reason) {
				t.Fatalf("got %+v", ms)
			}
			if n := len(pol.after); n != 0 {
				t.Fatalf("the policy was told about the first attempt again (%d calls)", n)
			}
		})
	}
}

// The first attempt of a retried probe reaches the policy the moment the
// retry is queued: it is a request the endpoint received and a failure the
// backoff counts, and while the retry waits, every other probe of the
// validator and the retry's own check decide on that state. The retry, when
// it runs, is told about separately: two requests, two AfterProbe calls.
func TestRunOne_QueuingARetryTellsThePolicyAboutTheFirstAttemptAtOnce(t *testing.T) {
	// accepts the connection and never answers the TLS handshake
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	var held []net.Conn
	var hmu sync.Mutex
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			hmu.Lock()
			held = append(held, c)
			hmu.Unlock()
		}
	}()
	defer func() {
		hmu.Lock()
		for _, c := range held {
			c.Close()
		}
		hmu.Unlock()
	}()

	p := testProber(t)
	p.feed = newPubFeed(filepath.Join(t.TempDir(), "publications.jsonl"))
	pol := &recPolicy{allow: true}
	p.cfg.Policy = pol
	p.cfg.RetryTransportTimeout = true
	p.cfg.RetryDelay = 10 * time.Millisecond
	p.cfg.AllowUnroutableHosts = true
	p.cfg.Timeouts = StepTimeouts{DNS: time.Second, TCP: time.Second, TLS: 200 * time.Millisecond, Identity: time.Second, Download: 3 * time.Second}
	coder, err := NewCoder(4, 8)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	pubA := pub(now.Add(-2*time.Hour), now.Add(time.Hour))
	pubA.Assignment.ProtocolParams = scan.ProtocolParamsSnapshot{OriginalRows: 4, TotalRows: 8}
	pt := SchedulePoint{At: now, Label: "w2", Phase: PhaseInWindow}
	key, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	tg := Target{AddressHex: "aa", Host: ln.Addr().String(), Assigned: true, RowCount: 1, PubKey: key}
	it := work{job: job{pubA, pt}, target: tg, coder: coder, key: pointKey("v1", pubA.PromiseHash, pt.At)}

	probed, retry := p.runOne(context.Background(), it)
	if !probed || retry == nil {
		_ = p.store.Sync()
		ms, _ := LoadMeasurements(p.store.Path())
		t.Fatalf("a handshake timeout should queue the retry: probed=%v retry=%v rows=%+v", probed, retry, ms)
	}
	if len(pol.after) != 1 || pol.after[0].Outcome != retry.first.Outcome {
		t.Fatalf("the policy was told %d times at queue time, want once with the first attempt: %+v", len(pol.after), pol.after)
	}

	p.runRetry(context.Background(), *retry)
	if len(pol.after) != 2 {
		t.Fatalf("after the retry the policy has %d attempts, want 2", len(pol.after))
	}
	if err := p.store.Sync(); err != nil {
		t.Fatal(err)
	}
	ms, err := LoadMeasurements(p.store.Path())
	if err != nil || len(ms) != 1 || ms[0].Retry == nil {
		t.Fatalf("recorded: %+v %v", ms, err)
	}
}

// When a queued retry does not run, no request of any kind goes out for it:
// not the retry, and not the evidence probe of the host the validator was
// registered at when the promise settled. That probe is a request too, and
// the answer that stopped the retry (the policy's, or the phase that moved
// on) stops it as well. The control case, a retry that does run, shows the
// settlement host is reachable and probed, so a zero count is not vacuous.
func TestRunRetry_NotRunMeansNoProbeOfTheSettlementHost(t *testing.T) {
	// accepts the connection and never answers the TLS handshake, counting
	listen := func(t *testing.T) (net.Listener, *atomic.Int32) {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		var n atomic.Int32
		var mu sync.Mutex
		var held []net.Conn
		go func() {
			for {
				c, err := ln.Accept()
				if err != nil {
					return
				}
				n.Add(1)
				mu.Lock()
				held = append(held, c)
				mu.Unlock()
			}
		}()
		t.Cleanup(func() {
			ln.Close()
			mu.Lock()
			for _, c := range held {
				c.Close()
			}
			mu.Unlock()
		})
		return ln, &n
	}
	for _, c := range []struct {
		name          string
		allow, skipDL bool
		phaseMoved    bool
		probed        bool
	}{
		{name: "budget used up meanwhile", allow: false},
		{name: "backed off meanwhile", allow: true, skipDL: true},
		{name: "phase moved on", allow: true, phaseMoved: true},
		{name: "retry runs", allow: true, probed: true},
	} {
		t.Run(c.name, func(t *testing.T) {
			current, _ := listen(t)
			old, oldConns := listen(t)
			p := testProber(t)
			p.feed = newPubFeed(filepath.Join(t.TempDir(), "publications.jsonl"))
			pol := &recPolicy{allow: true}
			p.cfg.Policy = pol
			p.cfg.RetryTransportTimeout = true
			p.cfg.RetryDelay = 10 * time.Millisecond
			p.cfg.AllowUnroutableHosts = true
			p.cfg.Timeouts = StepTimeouts{DNS: time.Second, TCP: time.Second, TLS: 200 * time.Millisecond, Identity: time.Second, Download: 3 * time.Second}
			coder, err := NewCoder(4, 8)
			if err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC()
			pubA := pub(now.Add(-2*time.Hour), now.Add(time.Hour))
			pubA.Assignment.ProtocolParams = scan.ProtocolParamsSnapshot{OriginalRows: 4, TotalRows: 8}
			pt := SchedulePoint{At: now, Label: "w2", Phase: PhaseInWindow}
			key, _, err := ed25519.GenerateKey(nil)
			if err != nil {
				t.Fatal(err)
			}
			tg := Target{AddressHex: "aa", Host: current.Addr().String(), HostAtSettlement: old.Addr().String(),
				Assigned: true, RowCount: 1, PubKey: key}
			it := work{job: job{pubA, pt}, target: tg, coder: coder, key: pointKey("v1", pubA.PromiseHash, pt.At)}

			_, retry := p.runOne(context.Background(), it)
			if retry == nil {
				t.Fatal("a handshake timeout should queue the retry")
			}
			if n := oldConns.Load(); n != 0 {
				t.Fatalf("the settlement host was probed before the retry: %d connection(s)", n)
			}
			pol.mu.Lock()
			pol.allow, pol.skipDL, pol.reason = c.allow, c.skipDL, "budget or backoff"
			pol.mu.Unlock()
			if c.phaseMoved {
				retry.first.Phase = PhasePost
			}
			p.runRetry(context.Background(), *retry)

			if err := p.store.Sync(); err != nil {
				t.Fatal(err)
			}
			ms, err := LoadMeasurements(p.store.Path())
			if err != nil || len(ms) != 1 {
				t.Fatalf("recorded: %+v %v", ms, err)
			}
			n := oldConns.Load()
			switch {
			case c.probed && (n == 0 || ms[0].SettlementHost == nil):
				t.Fatalf("the retry ran and failed, so the settlement host should be probed: %d connection(s), %+v", n, ms[0].SettlementHost)
			case !c.probed && (n != 0 || ms[0].SettlementHost != nil):
				t.Fatalf("the retry did not run, yet the settlement host got %d connection(s): %+v", n, ms[0].SettlementHost)
			case ms[0].HostAtSettlement != old.Addr().String():
				t.Fatalf("the row lost the host registered at settlement: %q", ms[0].HostAtSettlement)
			}
			// Every admission is settled exactly once. A retry admitted and
			// then not run (backed off, phase moved on) gives its slot back;
			// one that runs is accounted, and so is the settlement-host
			// probe, which asked the policy for its own slot first.
			pol.mu.Lock()
			defer pol.mu.Unlock()
			wantAfter, wantReleased, wantAsked := 1, 0, 2
			switch {
			case c.probed:
				wantAfter, wantAsked = 3, 3
			case c.allow:
				wantReleased = 1
			}
			if len(pol.after) != wantAfter || pol.released != wantReleased || len(pol.asked) != wantAsked {
				t.Fatalf("policy saw %d asks, %d accounted, %d released; want %d, %d, %d",
					len(pol.asked), len(pol.after), pol.released, wantAsked, wantAfter, wantReleased)
			}
			if c.probed && pol.asked[2].Host != old.Addr().String() {
				t.Fatalf("the settlement-host probe was not put to the policy: asked about %+v", pol.asked[2])
			}
		})
	}
}

// overlapPolicy admits everything and counts every time it is asked about a
// validator while an earlier admission of that validator is still unsettled
// (neither accounted nor released).
type overlapPolicy struct {
	recPolicy
	inflight map[string]int
	overlaps int
}

func (o *overlapPolicy) BeforeProbe(_ scan.Publication, t Target, _ time.Time) (bool, bool, string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.inflight[t.AddressHex] > 0 {
		o.overlaps++
	}
	o.inflight[t.AddressHex]++
	return true, false, ""
}
func (o *overlapPolicy) AfterProbe(_ scan.Publication, m Measurement) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.inflight[m.ValidatorAddress]--
	o.after = append(o.after, m)
}
func (o *overlapPolicy) Release(_ scan.Publication, t Target) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.inflight[t.AddressHex]--
	o.released++
}

// Several work items for one validator run by concurrent workers — the first
// burst after a start, with eight workers — are put to the policy one at a
// time: each is asked about only once the one before it has been accounted.
// The policy used to be asked before the validator's lock was taken and told
// after it was released, so every item of the burst was admitted on a state
// that included none of the others, and then ran back to back with no
// spacing, past the per-validator caps.
func TestRunOne_ConcurrentItemsOfOneValidatorAreAdmittedOneAtATime(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close() // connection refused from here on: a fast, real request

	p := testProber(t)
	p.feed = newPubFeed(filepath.Join(t.TempDir(), "publications.jsonl"))
	pol := &overlapPolicy{inflight: map[string]int{}}
	p.cfg.Policy = pol
	p.cfg.AllowUnroutableHosts = true
	coder, err := NewCoder(4, 8)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	pubA := pub(now.Add(-2*time.Hour), now.Add(time.Hour))
	pubA.Assignment.ProtocolParams = scan.ProtocolParamsSnapshot{OriginalRows: 4, TotalRows: 8}
	tg := Target{AddressHex: "aa", Host: addr, Assigned: true, RowCount: 1}

	const workers = 8
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		pt := SchedulePoint{At: now, Label: "w" + string(rune('0'+i)), Phase: PhaseInWindow}
		it := work{job: job{pubA, pt}, target: tg, coder: coder, key: pointKey("v1", pubA.PromiseHash, pt.At)}
		wg.Add(1)
		go func() {
			defer wg.Done()
			p.runOne(context.Background(), it)
		}()
	}
	wg.Wait()

	pol.mu.Lock()
	defer pol.mu.Unlock()
	if pol.overlaps != 0 {
		t.Fatalf("%d admissions of the validator were asked for while an earlier one was still unsettled", pol.overlaps)
	}
	if len(pol.after)+pol.released != workers || pol.inflight["aa"] != 0 {
		t.Fatalf("%d accounted + %d released for %d admissions, %d left unsettled", len(pol.after), pol.released, workers, pol.inflight["aa"])
	}
}
