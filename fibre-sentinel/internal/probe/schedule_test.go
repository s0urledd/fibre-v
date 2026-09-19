package probe

import (
	"testing"
	"time"

	"github.com/plsgiveup/fibre/fibre-sentinel/internal/scan"
)

// The in-window points are fractions of the publication's own window. That
// was written against a devnet whose window was ten minutes, where 0.92 put
// the last reading 48 seconds before the deadline. Mocha's shard_retention is
// four hours, and the same fraction puts it nineteen minutes out — with the
// grace probe, where a missing shard is TOLERATED by construction, the only
// thing after it. A validator that pruned anywhere in those nineteen minutes
// served every probe it was given and bucketed as `served`. That is the one
// failure this observer exists to catch.
func TestScheduleFor_LastReadingStaysNearTheDeadlineAtMochaScale(t *testing.T) {
	creation := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	cfg := DefaultScheduleConfig()
	mocha := func(retention, timeout time.Duration, settleAfter time.Duration) scan.Publication {
		msu := creation.Add(max2(retention, timeout))
		p := scan.Publication{SettlementTime: creation.Add(settleAfter), MustServeUntil: msu}
		p.ParamsAtPublication.ShardRetentionSeconds = int64(retention / time.Second)
		p.ParamsAtPublication.PaymentPromiseTimeoutSeconds = int64(timeout / time.Second)
		return p
	}
	lastInWindow := func(pts []SchedulePoint) SchedulePoint {
		var last SchedulePoint
		for _, p := range pts {
			if p.Phase == PhaseInWindow && p.At.After(last.At) {
				last = p
			}
		}
		return last
	}

	for _, c := range []struct {
		name                       string
		retention, timeout, settle time.Duration
	}{
		{"mocha 4h retention, fast settlement", 4 * time.Hour, time.Hour, 30 * time.Second},
		{"mocha 4h retention, settled at the timeout edge", 4 * time.Hour, time.Hour, time.Hour},
		{"a day of retention", 24 * time.Hour, time.Hour, time.Minute},
	} {
		pub := mocha(c.retention, c.timeout, c.settle)
		last := lastInWindow(ScheduleFor(pub, cfg))
		gap := pub.MustServeUntil.Sub(last.At)
		if gap > cfg.LastPointMargin {
			t.Errorf("%s: last in-window reading is %s before must_serve_until, want no more than %s: a prune inside that gap is invisible",
				c.name, gap, cfg.LastPointMargin)
		}
		if gap <= 0 {
			t.Errorf("%s: last in-window reading is not before must_serve_until (%s)", c.name, gap)
		}
	}

	// A short window keeps the tighter reading its fraction already gives it:
	// the margin pulls a point forward, it never pushes one back.
	short := mocha(10*time.Minute, 5*time.Minute, 10*time.Second)
	last := lastInWindow(ScheduleFor(short, cfg))
	if gap := short.MustServeUntil.Sub(last.At); gap > time.Minute {
		t.Errorf("a ten-minute window's last reading moved back to %s before the deadline; the fraction already put it closer", gap)
	}

	// Ordering still holds: every in-window point is before the grace point,
	// and the grace point is after must_serve_until.
	pts := ScheduleFor(mocha(4*time.Hour, time.Hour, 30*time.Second), cfg)
	for i := 1; i < len(pts); i++ {
		if pts[i].At.Before(pts[i-1].At) {
			t.Fatalf("points out of order at %d: %v", i, pts)
		}
	}
	for _, p := range pts {
		switch p.Phase {
		case PhaseInWindow:
			if !p.At.Before(mochaMSU(creation)) {
				t.Errorf("%s is in_window but not before must_serve_until", p.Label)
			}
		case PhaseGrace, PhasePost:
			if p.At.Before(mochaMSU(creation)) {
				t.Errorf("%s is %s but before must_serve_until", p.Label, p.Phase)
			}
		}
	}
}

func mochaMSU(creation time.Time) time.Time { return creation.Add(4 * time.Hour) }

func max2(a, b time.Duration) time.Duration {
	if a > b {
		return a
	}
	return b
}

// The margin is on the scheduled time. The prober may run a slot late — up to
// a twentieth of the publication's own window, twelve minutes on mocha's four
// hours — and the phase is taken from the actual start, so the reading that
// exists to catch an early prune could have run nine minutes after the
// obligation ended, where NOT_FOUND is TOLERATED by construction. The
// schedule change would then have bought nothing whenever the prober was
// busy. An in-window slot is never run past its deadline: it is recorded
// unobserved, which is a figure this observer publishes, rather than judged
// in a phase that cannot fault.
func TestLatenessNeverCarriesAnInWindowSlotPastTheDeadline(t *testing.T) {
	creation := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	msu := creation.Add(4 * time.Hour)
	pub := scan.Publication{SettlementTime: creation.Add(30 * time.Second), MustServeUntil: msu}
	pub.ParamsAtPublication.ShardRetentionSeconds = 4 * 3600
	p := &Prober{cfg: Config{MaxLateness: 90 * time.Second, MaxLatenessFraction: 0.05}}

	// Unbounded, the allowance is 5% of four hours.
	if got := p.latenessFor(pub); got < 11*time.Minute {
		t.Fatalf("the window-scaled allowance is %s; this test cannot show the bug", got)
	}

	var last SchedulePoint
	for _, pt := range ScheduleFor(pub, DefaultScheduleConfig()) {
		if pt.Phase == PhaseInWindow && pt.At.After(last.At) {
			last = pt
		}
	}
	room := msu.Sub(last.At)
	got := p.latenessAt(pub, last)
	if got > room {
		t.Errorf("the last in-window slot may run %s late with only %s before the deadline", got, room)
	}
	if got <= 0 {
		t.Errorf("the allowance for the last in-window slot is %s: it could never run", got)
	}

	// An early point keeps the full allowance: there is no deadline near it.
	first := ScheduleFor(pub, DefaultScheduleConfig())[0]
	if p.latenessAt(pub, first) != p.latenessFor(pub) {
		t.Errorf("the first point's allowance was shortened to %s", p.latenessAt(pub, first))
	}
	// A grace point is not bounded by the deadline it sits after.
	grace := SchedulePoint{Phase: PhaseGrace, At: msu.Add(30 * time.Second)}
	if p.latenessAt(pub, grace) != p.latenessFor(pub) {
		t.Errorf("the grace point's allowance was shortened to %s", p.latenessAt(pub, grace))
	}
}
