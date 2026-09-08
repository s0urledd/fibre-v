package probe

import (
	"testing"
	"time"

	"github.com/plsgiveup/fibre/fibre-sentinel/internal/scan"
)

func pub(settle, msu time.Time) scan.Publication {
	return scan.Publication{
		PromiseHash:    "abcdef0123456789",
		SettlementTime: settle,
		MustServeUntil: msu,
		Promise:        scan.PromiseFields{Commitment: "00", BlobVersion: 0},
		Assignment:     scan.AssignmentTable{ValidatorSetHeight: 100},
	}
}

func TestScheduleFor_ShapeAndOrder(t *testing.T) {
	settle := time.Date(2026, 9, 3, 0, 0, 0, 0, time.UTC)
	msu := settle.Add(10 * time.Minute)
	cfg := ScheduleConfig{
		InWindowFractions: []float64{0.15, 0.45, 0.72, 0.92},
		GraceOffset:       30 * time.Second,
		PruneTolerance:    150 * time.Second,
		PostMargin:        60 * time.Second,
		MinSpacing:        10 * time.Second,
	}
	pts := ScheduleFor(pub(settle, msu), cfg)

	if len(pts) < 5 {
		t.Fatalf("want >=5 points, got %d", len(pts))
	}
	// ordering
	for i := 1; i < len(pts); i++ {
		if pts[i].At.Before(pts[i-1].At) {
			t.Fatalf("points not sorted at %d", i)
		}
	}
	var nIn, nGrace, nPost int
	for _, pt := range pts {
		switch pt.Phase {
		case PhaseInWindow:
			nIn++
			if !pt.At.Before(msu) {
				t.Errorf("in-window point %s not before must_serve_until %s", pt.At, msu)
			}
		case PhaseGrace:
			nGrace++
			if pt.At.Before(msu) || pt.At.After(msu.Add(cfg.PruneTolerance)) {
				t.Errorf("grace point %s outside (msu, msu+tol]", pt.At)
			}
		case PhasePost:
			nPost++
			if !pt.At.After(msu.Add(cfg.PruneTolerance)) {
				t.Errorf("post point %s not after msu+tol", pt.At)
			}
		}
	}
	if nIn < 3 || nGrace != 1 || nPost != 1 {
		t.Fatalf("phase counts: in=%d grace=%d post=%d", nIn, nGrace, nPost)
	}
	// last in-window point must be the closest to the deadline
	if last := pts[nIn-1]; last.Phase != PhaseInWindow {
		t.Errorf("point %d expected in-window, got %s", nIn-1, last.Phase)
	}
}

func TestScheduleFor_FollowsMustServeUntil(t *testing.T) {
	settle := time.Unix(1_800_000_000, 0).UTC()
	short := ScheduleFor(pub(settle, settle.Add(10*time.Minute)), DefaultScheduleConfig())
	long := ScheduleFor(pub(settle, settle.Add(4*time.Hour)), DefaultScheduleConfig())

	// the schedule is a function of the window, so a 4h window pushes every
	// point later than the 10m window's counterpart.
	if len(short) != len(long) {
		t.Fatalf("point counts differ: %d vs %d", len(short), len(long))
	}
	for i := range short {
		if !long[i].At.After(short[i].At) {
			t.Errorf("point %d (%s): long window %s not later than short %s", i, short[i].Label, long[i].At, short[i].At)
		}
	}
}

func TestScheduleFor_MinSpacing(t *testing.T) {
	settle := time.Now().UTC()
	// a 20s window with 4 in-window fractions would burst; MinSpacing thins it.
	cfg := DefaultScheduleConfig()
	cfg.MinSpacing = 30 * time.Second
	pts := ScheduleFor(pub(settle, settle.Add(20*time.Second)), cfg)
	for i := 1; i < len(pts); i++ {
		if d := pts[i].At.Sub(pts[i-1].At); d < cfg.MinSpacing {
			t.Fatalf("points %d and %d only %s apart (< MinSpacing %s)", i-1, i, d, cfg.MinSpacing)
		}
	}
}

func TestPhaseAt(t *testing.T) {
	settle := time.Now().UTC()
	msu := settle.Add(10 * time.Minute)
	p := pub(settle, msu)
	cfg := DefaultScheduleConfig()
	tol := cfg.PruneTolerance

	cases := []struct {
		t    time.Time
		want Phase
	}{
		{msu.Add(-time.Minute), PhaseInWindow},
		{msu.Add(-time.Millisecond), PhaseInWindow},
		{msu.Add(time.Second), PhaseGrace},
		{msu.Add(tol), PhaseGrace},
		{msu.Add(tol + time.Second), PhasePost},
	}
	for _, c := range cases {
		if got := PhaseAt(c.t, p, cfg); got != c.want {
			t.Errorf("PhaseAt(%s) = %s, want %s", c.t.Sub(msu), got, c.want)
		}
	}
}

func TestClassify_Taxonomy(t *testing.T) {
	type row struct {
		assigned bool
		phase    Phase
		outcome  Outcome
		want     Classification
	}
	rows := []row{
		// assigned, in obligation
		{true, PhaseInWindow, OutcomeServedOK, ClassHealthy},
		{true, PhaseInWindow, OutcomeNotFound, ClassFault},
		{true, PhaseInWindow, OutcomeRPCUnavailable, ClassFault},
		{true, PhaseInWindow, OutcomeTCPRefused, ClassFault},
		{true, PhaseInWindow, OutcomeIdentityFail, ClassFault},
		{true, PhaseInWindow, OutcomeWrongRows, ClassFault},
		{true, PhaseInWindow, OutcomeInvalidRows, ClassFault},
		// assigned, grace
		{true, PhaseGrace, OutcomeNotFound, ClassTolerated},
		{true, PhaseGrace, OutcomeRPCUnavailable, ClassTolerated},
		{true, PhaseGrace, OutcomeServedOK, ClassHealthy},
		{true, PhaseGrace, OutcomeIdentityFail, ClassFault},
		// assigned, post
		{true, PhasePost, OutcomeNotFound, ClassExpectedGone},
		{true, PhasePost, OutcomeServedOK, ClassServedPastWindow},
		{true, PhasePost, OutcomeTCPTimeout, ClassUnreachablePostWindow},
		// unassigned
		{false, PhaseInWindow, OutcomeNotFound, ClassExpectedUnassigned},
		{false, PhaseInWindow, OutcomeServedOK, ClassServingUnassigned},
		{false, PhasePost, OutcomeNotFound, ClassExpectedUnassigned},
		{false, PhaseGrace, OutcomeRPCUnavailable, ClassExpectedUnassigned},
		// probe-side
		{true, PhaseInWindow, OutcomeProbeError, ClassProbeError},
		{true, PhaseInWindow, OutcomeMissed, ClassNotProbed},
	}
	for _, r := range rows {
		got, reason := Classify(r.assigned, r.phase, r.outcome)
		if got != r.want {
			t.Errorf("Classify(assigned=%v, %s, %s) = %s (%q), want %s", r.assigned, r.phase, r.outcome, got, reason, r.want)
		}
		if reason == "" {
			t.Errorf("Classify(%v,%s,%s): empty reason", r.assigned, r.phase, r.outcome)
		}
	}
}

func TestClassifyDialError(t *testing.T) {
	cases := map[string]Outcome{
		"dial tcp 127.0.0.1:7980: connectex: No connection could be made because the target machine actively refused it.": OutcomeTCPRefused,
		"dial tcp 10.0.0.1:7980: i/o timeout":                OutcomeTCPTimeout,
		"dial tcp: lookup nonexistent.invalid: no such host": OutcomeTCPUnreachable,
	}
	for in, want := range cases {
		if got := classifyDialError(errStr(in)); got != want {
			t.Errorf("classifyDialError(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestMeasurementStore_DedupeResume(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenMeasurementStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC().Truncate(time.Second)
	m := Measurement{
		SchemaVersion: MeasurementSchemaVersion, Vantage: "v1",
		PromiseHash: "ph1", ValidatorAddress: "aa", ScheduledAt: at,
		Outcome: OutcomeServedOK, Classification: ClassHealthy,
	}
	if err := s.Append(m); err != nil {
		t.Fatal(err)
	}
	if err := s.Append(m); err != nil { // dupe
		t.Fatal(err)
	}
	s.Close()

	s2, err := OpenMeasurementStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if !s2.Has("v1", "ph1", "aa", at) {
		t.Fatal("dedupe key not reloaded")
	}
	if !s2.HandledPoint("v1", "ph1", at) {
		t.Fatal("point key not reloaded")
	}
	if s2.Has("v1", "ph1", "bb", at) {
		t.Fatal("unexpected dedupe hit for other validator")
	}
	ms, err := LoadMeasurements(s2.Path())
	if err != nil || len(ms) != 1 {
		t.Fatalf("LoadMeasurements: n=%d err=%v", len(ms), err)
	}
}

type errStr string

func (e errStr) Error() string { return string(e) }
