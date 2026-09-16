package probe

import (
	"context"
	"errors"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

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
		// rows added by the pre-mocha audit (docs/verdicts.md alignment)
		{true, PhaseInWindow, OutcomePartial, ClassFault},
		{true, PhaseInWindow, OutcomeNoHost, ClassFault},
		{true, PhaseGrace, OutcomePartial, ClassFault},
		{true, PhaseGrace, OutcomeWrongRows, ClassFault},
		{true, PhaseGrace, OutcomeNoHost, ClassTolerated},
		{true, PhasePost, OutcomeWrongRows, ClassFault},
		{true, PhasePost, OutcomeInvalidRows, ClassFault},
		{true, PhasePost, OutcomePartial, ClassServedPastWindow},
		{true, PhasePost, OutcomeNoHost, ClassUnreachablePostWindow},
		{false, PhaseInWindow, OutcomeWrongRows, ClassServingUnassigned},
		{false, PhaseInWindow, OutcomeInvalidRows, ClassServingUnassigned},
		{false, PhaseInWindow, OutcomePartial, ClassServingUnassigned},
		{false, PhaseInWindow, OutcomeIdentityFail, ClassFault},
		{false, PhaseGrace, OutcomeNoHost, ClassExpectedUnassigned},
		{true, PhaseInWindow, OutcomeReachable, ClassNotProbed},
		{true, PhaseInWindow, OutcomeRPCDeadline, ClassProbeError},
		{false, PhasePost, OutcomeRPCDeadline, ClassProbeError},
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

func TestClassifyDownloadError_StatusCodes(t *testing.T) {
	cases := []struct {
		err  error
		want Outcome
	}{
		{status.Error(codes.NotFound, "no blob shard found"), OutcomeNotFound},
		{status.Error(codes.Unavailable, "connection error"), OutcomeRPCUnavailable},
		{status.Error(codes.DeadlineExceeded, "context deadline exceeded"), OutcomeRPCDeadline},
		{status.Error(codes.ResourceExhausted, "grpc: received message larger than max (5000000 vs. 4194304)"), OutcomeProbeError},
		{status.Error(codes.InvalidArgument, "bad blob id"), OutcomeProbeError},
		{status.Error(codes.Internal, "store: i/o error"), OutcomeRPCError},
		{status.Error(codes.Unknown, "boom"), OutcomeRPCError},
		{status.Error(codes.Unavailable, "connection error: desc = \"transport: authentication handshake failed: fibre tls identity [signature_invalid]: bad\""), OutcomeIdentityFail},
		{context.DeadlineExceeded, OutcomeRPCDeadline},
		{errors.New("dial tcp: connection refused"), OutcomeRPCUnavailable},
	}
	for _, c := range cases {
		if got := classifyDownloadError(c.err); got != c.want {
			t.Errorf("classifyDownloadError(%v) = %s, want %s", c.err, got, c.want)
		}
	}
}

func TestOrderAddrsAndDownloadDeadline(t *testing.T) {
	got := orderAddrs([]string{"2001:db8::1", "10.0.0.1", "::1", "192.0.2.7"})
	want := []string{"10.0.0.1", "192.0.2.7", "2001:db8::1", "::1"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("orderAddrs = %v, want %v", got, want)
		}
	}
	to := StepTimeouts{}.withDefaults()
	if d := to.downloadDeadline(0); d != 25*time.Second {
		t.Errorf("base deadline = %s", d)
	}
	// 122 MB at 1 MiB/s adds ~116 s
	if d := to.downloadDeadline(122_000_000); d < 25*time.Second+110*time.Second || d > 25*time.Second+120*time.Second {
		t.Errorf("scaled deadline = %s", d)
	}
	if maxRecvMsgSize <= 4<<20 {
		t.Errorf("maxRecvMsgSize = %d, must exceed grpc's 4 MiB default", maxRecvMsgSize)
	}
}

func TestRun_NilCoderIsProbeErrorNotFault(t *testing.T) {
	in := Input{
		Vantage: "t", ChainID: "c", PromiseHash: "aa", MustServeUntil: time.Now().Add(time.Hour),
		Target:        Target{AddressHex: "aa", Host: "192.0.2.1:7980", Assigned: true, RowCount: 148, PubKey: make([]byte, 32)},
		SchedulePoint: SchedulePoint{Label: "w1", At: time.Now()},
	}
	m := Run(context.Background(), in, nil, StepTimeouts{})
	if m.Outcome != OutcomeProbeError {
		t.Fatalf("outcome = %s, want %s", m.Outcome, OutcomeProbeError)
	}
	if m.Classification != ClassProbeError {
		t.Fatalf("classification = %s, want %s", m.Classification, ClassProbeError)
	}
	if m.TCP.Attempted {
		t.Error("a probe with no coder must not open a connection")
	}
	// the same input with the download skipped is a normal reachability probe
	in.SkipDownload = true
	m = Run(context.Background(), in, nil, StepTimeouts{TCP: 10 * time.Millisecond})
	if m.Outcome == OutcomeProbeError {
		t.Errorf("reachability probe must not need a coder: %s (%s)", m.Outcome, m.RawError)
	}
}

func TestRun_StampsClockOffset(t *testing.T) {
	in := Input{
		Vantage: "t", PromiseHash: "aa", MustServeUntil: time.Now().Add(time.Hour),
		Target:        Target{AddressHex: "aa", Host: "", Assigned: true},
		SchedulePoint: SchedulePoint{Label: "w1", At: time.Now()},
		ClockOffsetMS: -4200,
	}
	m := Run(context.Background(), in, nil, StepTimeouts{})
	if m.ClockOffsetMS != -4200 {
		t.Fatalf("ClockOffsetMS = %d, want -4200", m.ClockOffsetMS)
	}
}

func TestScheduleFor_DegenerateWindowUsesRecordParams(t *testing.T) {
	msu := time.Now().UTC()
	p := pub(msu.Add(time.Minute), msu) // settled after must_serve_until
	p.ParamsAtPublication.ShardRetentionSeconds = 4 * 3600
	p.ParamsAtPublication.PaymentPromiseTimeoutSeconds = 3600
	pts := ScheduleFor(p, DefaultScheduleConfig())
	if len(pts) == 0 {
		t.Fatal("no points")
	}
	first := pts[0].At
	if span := msu.Sub(first); span < 3*time.Hour || span > 4*time.Hour {
		t.Fatalf("fallback window spans %s, want close to the 4h retention", span)
	}
	// a record with no params at all still gets a usable window
	p.ParamsAtPublication.ShardRetentionSeconds = 0
	p.ParamsAtPublication.PaymentPromiseTimeoutSeconds = 0
	pts = ScheduleFor(p, DefaultScheduleConfig())
	if len(pts) == 0 {
		t.Fatal("no points without params")
	}
	if span := msu.Sub(pts[0].At); span > 10*time.Minute {
		t.Fatalf("no-params fallback spans %s, want <= 10m", span)
	}
}
