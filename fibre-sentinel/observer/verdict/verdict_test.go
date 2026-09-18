package verdict

import (
	"testing"
	"time"

	"github.com/plsgiveup/fibre/fibre-sentinel/internal/probe"
)

func row(v string, at time.Time, cls probe.Classification) Row {
	return Row{PromiseHash: "p", Validator: v, ScheduleLabel: "w1", ScheduledAt: at, StartedAt: at,
		MustServeUntil: at.Add(time.Hour), Assigned: true, Attested: true, Phase: probe.PhaseInWindow, Classification: cls}
}

// A validator the observer did not ask (a load cap, a slot that elapsed, a
// probe of its own that failed) says nothing about the point: it is not in
// the share's denominator, so a burst of faults among the validators that
// were probed is caught however many the cap turned away. Every row at the
// point is still counted as removed, gaps included.
func TestSuspectPoints_GapRowsDoNotDiluteTheShare(t *testing.T) {
	at := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	w := Window{All: true, End: at.Add(time.Hour)}
	rows := []Row{row("a", at, probe.ClassFault), row("b", at, probe.ClassFault), row("c", at, probe.ClassFault),
		row("d", at, probe.ClassHealthy)}
	for _, v := range []string{"e", "f", "g", "h", "i", "j"} {
		rows = append(rows, row(v, at, probe.ClassNotProbed))
	}
	pts := SuspectPoints(rows, w)
	if len(pts) != 1 || pts[0].Reason != "fault" {
		t.Fatalf("three of four probed faulting must be a fault suspect point: %+v", pts)
	}
	if pts[0].Validators != 4 || pts[0].Faulted != 3 || pts[0].Rows != 10 {
		t.Fatalf("validators=%d faulted=%d rows=%d, want 4/3/10", pts[0].Validators, pts[0].Faulted, pts[0].Rows)
	}
	// a point where only gap rows exist is no point at all
	only := []Row{row("a", at, probe.ClassNotProbed), row("b", at, probe.ClassProbeError), row("c", at, probe.ClassNotProbed)}
	if pts := SuspectPoints(only, w); len(pts) != 0 {
		t.Fatalf("a point of gaps became suspect: %+v", pts)
	}
}

// A candidate in range whose assignment rows were never recorded cannot be
// matched or ruled out, so the late verdict is PROBE_ERROR for good, as the
// store's SQL draws it; recorded candidates are still tried first.
func TestLateShadow_UnrecordedCandidateIsAGap(t *testing.T) {
	probeAt := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	frontier := probeAt.Add(2 * time.Hour)
	got := []uint32{4, 5}
	recorded := Candidate{PromiseHash: "0bbb", Commitment: "c", SettlementTime: probeAt.Add(20 * time.Minute), MustServeUntil: probeAt.Add(time.Hour), Rows: []int{5, 4}}
	blank := Candidate{PromiseHash: "0aaa", Commitment: "c", SettlementTime: probeAt.Add(-time.Minute), MustServeUntil: probeAt.Add(time.Hour)}
	if cls, by, ok := LateShadow(got, probeAt, frontier, time.Hour, 5*time.Minute, []Candidate{blank, recorded}); !ok || cls != probe.ClassShadowedShard || by != "0bbb" {
		t.Fatalf("a recorded match beside an unrecorded candidate: %s %q ok=%v", cls, by, ok)
	}
	if cls, _, ok := LateShadow(got, probeAt, frontier, time.Hour, 5*time.Minute, []Candidate{blank}); !ok || cls != probe.ClassProbeError {
		t.Fatalf("only an unrecorded candidate: %s ok=%v, want PROBE_ERROR", cls, ok)
	}
	// an unrecorded candidate outside the lifetime is no candidate
	stale := Candidate{PromiseHash: "0aaa", Commitment: "c", SettlementTime: probeAt.Add(-3 * time.Hour), MustServeUntil: probeAt.Add(-2 * time.Hour)}
	if cls, _, ok := LateShadow(got, probeAt, frontier, time.Hour, 5*time.Minute, []Candidate{stale}); !ok || cls != probe.ClassUnmatchedGenuine {
		t.Fatalf("unrecorded candidate outside the lifetime: %s ok=%v, want UNMATCHED_GENUINE", cls, ok)
	}
}
