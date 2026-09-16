package probe

import (
	"context"
	"errors"
	"strings"
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
		// commitmentVerified matters only for WRONG_ROWS and PARTIAL, where
		// it is the whole question: rows that verify against the commitment
		// but carry the wrong indices mean another promise answered in this
		// one's place, which no validator can prevent.
		commitmentVerified bool
	}
	// Every existing row describes an ATTESTED validator: one the settled
	// promise proves stored the shard. The unattested rows are a separate
	// table below, because the obligation itself differs.
	rows := []row{
		// assigned, in obligation
		{assigned: true, phase: PhaseInWindow, outcome: OutcomeServedOK, want: ClassHealthy},
		{assigned: true, phase: PhaseInWindow, outcome: OutcomeNotFound, want: ClassFault},
		{assigned: true, phase: PhaseInWindow, outcome: OutcomeInvalidRows, want: ClassFault},
		{assigned: true, phase: PhaseInWindow, outcome: OutcomeServerError, want: ClassFault},
		{assigned: true, phase: PhaseInWindow, outcome: OutcomeIdentityFail, want: ClassFault},
		// unreachable is not a retention verdict: from one vantage it is not
		// distinguishable from a problem on the observer's own path.
		{assigned: true, phase: PhaseInWindow, outcome: OutcomeRPCUnavailable, want: ClassUnreachable},
		{assigned: true, phase: PhaseInWindow, outcome: OutcomeTCPRefused, want: ClassUnreachable},
		{assigned: true, phase: PhaseInWindow, outcome: OutcomeTCPTimeout, want: ClassUnreachable},
		{assigned: true, phase: PhaseInWindow, outcome: OutcomeDNSFail, want: ClassUnreachable},
		{assigned: true, phase: PhaseInWindow, outcome: OutcomeTLSFail, want: ClassUnreachable},
		{assigned: true, phase: PhaseInWindow, outcome: OutcomeRPCError, want: ClassUnreachable},
		// rows that do not verify against the commitment are corrupt data
		{assigned: true, phase: PhaseInWindow, outcome: OutcomeWrongRows, want: ClassFault},
		{assigned: true, phase: PhaseInWindow, outcome: OutcomePartial, want: ClassFault},
		// rows that DO verify are another promise's shard for the same blob
		{assigned: true, phase: PhaseInWindow, outcome: OutcomeWrongRows, want: ClassShadowedShard, commitmentVerified: true},
		{assigned: true, phase: PhaseInWindow, outcome: OutcomePartial, want: ClassShadowedShard, commitmentVerified: true},
		{assigned: true, phase: PhaseGrace, outcome: OutcomeWrongRows, want: ClassShadowedShard, commitmentVerified: true},
		{assigned: true, phase: PhasePost, outcome: OutcomeWrongRows, want: ClassShadowedShard, commitmentVerified: true},
		// no registered host is a registry state, not a refusal to serve
		{assigned: true, phase: PhaseInWindow, outcome: OutcomeNoHost, want: ClassNotRegistered},
		{assigned: true, phase: PhaseGrace, outcome: OutcomeNoHost, want: ClassNotRegistered},
		{assigned: true, phase: PhasePost, outcome: OutcomeNoHost, want: ClassNotRegistered},
		// assigned, grace
		{assigned: true, phase: PhaseGrace, outcome: OutcomeNotFound, want: ClassTolerated},
		{assigned: true, phase: PhaseGrace, outcome: OutcomeRPCUnavailable, want: ClassTolerated},
		{assigned: true, phase: PhaseGrace, outcome: OutcomeServedOK, want: ClassHealthy},
		{assigned: true, phase: PhaseGrace, outcome: OutcomeIdentityFail, want: ClassFault},
		{assigned: true, phase: PhaseGrace, outcome: OutcomePartial, want: ClassFault},
		{assigned: true, phase: PhaseGrace, outcome: OutcomeWrongRows, want: ClassFault},
		{assigned: true, phase: PhaseGrace, outcome: OutcomeServerError, want: ClassTolerated},
		// assigned, post
		{assigned: true, phase: PhasePost, outcome: OutcomeNotFound, want: ClassExpectedGone},
		{assigned: true, phase: PhasePost, outcome: OutcomeServedOK, want: ClassServedPastWindow},
		{assigned: true, phase: PhasePost, outcome: OutcomeTCPTimeout, want: ClassUnreachablePostWindow},
		{assigned: true, phase: PhasePost, outcome: OutcomeInvalidRows, want: ClassFault},
		// DownloadShard enforces no assignment, so serving rows outside it
		// after the obligation ended is not a rule the validator broke.
		{assigned: true, phase: PhasePost, outcome: OutcomeWrongRows, want: ClassServedPastWindow},
		{assigned: true, phase: PhasePost, outcome: OutcomePartial, want: ClassServedPastWindow},
		{assigned: true, phase: PhasePost, outcome: OutcomeServerError, want: ClassUnreachablePostWindow},
		// unassigned
		{assigned: false, phase: PhaseInWindow, outcome: OutcomeNotFound, want: ClassExpectedUnassigned},
		{assigned: false, phase: PhaseInWindow, outcome: OutcomeServedOK, want: ClassServingUnassigned},
		{assigned: false, phase: PhasePost, outcome: OutcomeNotFound, want: ClassExpectedUnassigned},
		{assigned: false, phase: PhaseGrace, outcome: OutcomeRPCUnavailable, want: ClassExpectedUnassigned},
		{assigned: false, phase: PhaseInWindow, outcome: OutcomeWrongRows, want: ClassServingUnassigned},
		{assigned: false, phase: PhaseInWindow, outcome: OutcomeInvalidRows, want: ClassServingUnassigned},
		{assigned: false, phase: PhaseInWindow, outcome: OutcomePartial, want: ClassServingUnassigned},
		{assigned: false, phase: PhaseInWindow, outcome: OutcomeIdentityFail, want: ClassFault},
		{assigned: false, phase: PhaseGrace, outcome: OutcomeNoHost, want: ClassExpectedUnassigned},
		{assigned: false, phase: PhaseInWindow, outcome: OutcomeServerError, want: ClassExpectedUnassigned},
		{assigned: true, phase: PhaseInWindow, outcome: OutcomeReachable, want: ClassNotProbed},
		{assigned: true, phase: PhaseInWindow, outcome: OutcomeRPCDeadline, want: ClassProbeError},
		{assigned: false, phase: PhasePost, outcome: OutcomeRPCDeadline, want: ClassProbeError},
		// probe-side
		{assigned: true, phase: PhaseInWindow, outcome: OutcomeProbeError, want: ClassProbeError},
		{assigned: true, phase: PhaseInWindow, outcome: OutcomeMissed, want: ClassNotProbed},
	}
	for _, r := range rows {
		got, reason := Classify(Evidence{
			Assigned: r.assigned, Attested: true, Phase: r.phase,
			Outcome: r.outcome, CommitmentVerified: r.commitmentVerified,
		})
		if got != r.want {
			t.Errorf("Classify(assigned=%v, attested, %s, %s, commitmentVerified=%v) = %s (%q), want %s",
				r.assigned, r.phase, r.outcome, r.commitmentVerified, got, reason, r.want)
		}
		if reason == "" {
			t.Errorf("Classify(%v,%s,%s): empty reason", r.assigned, r.phase, r.outcome)
		}
	}
}

// Every outcome must be named by the taxonomy. A new outcome that fell
// through to a default arm used to become a FAULT under obligation, which
// made "we have not taught the observer about this yet" indistinguishable
// from "the validator broke its promise".
func TestClassify_NoOutcomeReachesAnUnnamedFault(t *testing.T) {
	for _, o := range AllOutcomes {
		for _, phase := range []Phase{PhaseInWindow, PhaseGrace, PhasePost} {
			got, reason := Classify(Evidence{Assigned: true, Attested: true, Phase: phase, Outcome: o})
			if reason == "" {
				t.Errorf("Classify(%s, %s): empty reason", phase, o)
			}
			if got == ClassFault && strings.Contains(reason, "unrecognised") {
				t.Errorf("Classify(%s, %s) faulted on an outcome it does not recognise", phase, o)
			}
		}
	}
	// and an outcome the taxonomy has never seen must not accuse anyone
	got, _ := Classify(Evidence{Assigned: true, Attested: true, Phase: PhaseInWindow, Outcome: Outcome("INVENTED")})
	if got.CountsAgainst() {
		t.Errorf("an unknown outcome under obligation = %s, want something that does not count against the validator", got)
	}
}

// A validator that cannot be reached has not been shown to break anything.
// Publishing that as a fault is the accusation this product must not make
// from a single vantage.
func TestClassify_UnreachableIsNotAFault(t *testing.T) {
	for _, o := range []Outcome{
		OutcomeDNSFail, OutcomeTCPRefused, OutcomeTCPTimeout, OutcomeTCPUnreachable,
		OutcomeTLSFail, OutcomeRPCUnavailable, OutcomeRPCError,
	} {
		got, _ := Classify(Evidence{Assigned: true, Attested: true, Phase: PhaseInWindow, Outcome: o})
		if got != ClassUnreachable {
			t.Errorf("in-window %s = %s, want %s", o, got, ClassUnreachable)
		}
		if got.CountsAgainst() || got.Rated() {
			t.Errorf("%s reached the serve rate as %s", o, got)
		}
	}
	// but a server that answered and failed to produce the shard is a fault
	for _, o := range []Outcome{OutcomeNotFound, OutcomeInvalidRows, OutcomeServerError} {
		got, _ := Classify(Evidence{Assigned: true, Attested: true, Phase: PhaseInWindow, Outcome: o})
		if !got.CountsAgainst() {
			t.Errorf("in-window %s = %s, want a fault: the validator answered and did not serve", o, got)
		}
	}
}

// A second, never-settled promise over the same blob makes an honest
// validator answer with the other promise's rows, because DownloadShard is
// addressed by commitment alone. That must never be published as a fault.
func TestClassify_ShadowedShardIsNeverAFault(t *testing.T) {
	for _, phase := range []Phase{PhaseInWindow, PhaseGrace, PhasePost} {
		for _, o := range []Outcome{OutcomeWrongRows, OutcomePartial} {
			got, reason := Classify(Evidence{
				Assigned: true, Attested: true, Phase: phase, Outcome: o, CommitmentVerified: true,
			})
			if got != ClassShadowedShard {
				t.Errorf("%s %s with verified rows = %s (%q), want %s", phase, o, got, reason, ClassShadowedShard)
			}
			if got.CountsAgainst() {
				t.Errorf("%s %s with verified rows counts against the validator", phase, o)
			}
		}
	}
	// rows that do not verify against the commitment are still corrupt data
	if got, _ := Classify(Evidence{Assigned: true, Attested: true, Phase: PhaseInWindow, Outcome: OutcomeWrongRows}); !got.CountsAgainst() {
		t.Errorf("unverifiable rows in window = %s, want a fault", got)
	}
}

// A certificate that is signed by the right key but has lapsed is a missed
// renewal. Publishing it in the same column as "someone else is answering on
// this endpoint" would put those two in the same sentence.
func TestClassify_StaleIdentityIsNotImpersonation(t *testing.T) {
	stale, reason := Classify(Evidence{
		Assigned: true, Attested: true, Phase: PhaseInWindow,
		Outcome: OutcomeIdentityFail, IdentityStale: true,
	})
	if stale != ClassIdentityExpired {
		t.Fatalf("lapsed identity = %s (%q), want %s", stale, reason, ClassIdentityExpired)
	}
	if stale.CountsAgainst() || stale.Rated() {
		t.Errorf("lapsed identity reached the serve rate as %s", stale)
	}
	wrong, _ := Classify(Evidence{
		Assigned: true, Attested: true, Phase: PhaseInWindow, Outcome: OutcomeIdentityFail,
	})
	if !wrong.CountsAgainst() {
		t.Errorf("a wrong consensus key = %s, want a fault", wrong)
	}
}

// A validator with no Fibre host in x/valaddr cannot be reached by anyone,
// but jailing and unbonding remove it from the bonded provider list while the
// chain keeps its registration. That is a registry state, not a refusal.
func TestClassify_NoRegisteredHostIsNotAFault(t *testing.T) {
	for _, phase := range []Phase{PhaseInWindow, PhaseGrace, PhasePost} {
		got, _ := Classify(Evidence{Assigned: true, Attested: true, Phase: phase, Outcome: OutcomeNoHost})
		if got != ClassNotRegistered {
			t.Errorf("%s NO_REGISTERED_HOST = %s, want %s", phase, got, ClassNotRegistered)
		}
		if got.CountsAgainst() || got.Rated() {
			t.Errorf("%s NO_REGISTERED_HOST reached the serve rate as %s", phase, got)
		}
	}
}

// An assigned validator whose signature is not in the settled promise is never
// accused: nothing proves it was ever sent the shard. It is never credited
// either, so the serve rate cannot be gamed by publishing blobs nobody signed.
func TestClassify_UnattestedIsNeverAFault(t *testing.T) {
	outcomes := []Outcome{
		OutcomeNotFound, OutcomeTCPRefused, OutcomeTCPTimeout, OutcomeTLSFail,
		OutcomeRPCUnavailable, OutcomeRPCError, OutcomeWrongRows,
		OutcomeInvalidRows, OutcomePartial, OutcomeServedOK,
	}
	for _, phase := range []Phase{PhaseInWindow, PhaseGrace, PhasePost} {
		for _, o := range outcomes {
			got, reason := Classify(Evidence{Assigned: true, Phase: phase, Outcome: o})
			if got != ClassUnattested {
				t.Errorf("Classify(assigned, UNattested, %s, %s) = %s, want %s", phase, o, got, ClassUnattested)
			}
			if reason == "" {
				t.Errorf("Classify(assigned, UNattested, %s, %s): empty reason", phase, o)
			}
		}
	}
	// serving anyway is reported as such: it proves the validator does hold it
	_, reason := Classify(Evidence{Assigned: true, Phase: PhaseInWindow, Outcome: OutcomeServedOK})
	if !strings.Contains(reason, "served") {
		t.Errorf("a served shard from an unattested validator should say so: %q", reason)
	}

	// identity is a property of the endpoint, not of one shard, so it is a
	// fault even with no attestation for this blob
	if got, _ := Classify(Evidence{Assigned: true, Phase: PhaseInWindow, Outcome: OutcomeIdentityFail}); got != ClassFault {
		t.Errorf("unattested IDENTITY_FAIL = %s, want %s", got, ClassFault)
	}
	// observer-side classes are unchanged by attestation
	for _, o := range []Outcome{OutcomeProbeError, OutcomeMissed, OutcomeRPCDeadline, OutcomeReachable} {
		a, _ := Classify(Evidence{Assigned: true, Attested: true, Phase: PhaseInWindow, Outcome: o})
		b, _ := Classify(Evidence{Assigned: true, Phase: PhaseInWindow, Outcome: o})
		if a != b {
			t.Errorf("attestation changed an observer-side class for %s: %s vs %s", o, a, b)
		}
	}
	// an UNassigned validator is judged as before, attested or not
	for _, att := range []bool{true, false} {
		if got, _ := Classify(Evidence{Attested: att, Phase: PhaseInWindow, Outcome: OutcomeNotFound}); got != ClassExpectedUnassigned {
			t.Errorf("unassigned attested=%v NOT_FOUND = %s", att, got)
		}
	}
}

func TestClassifyDialError(t *testing.T) {
	cases := map[string]Outcome{
		"dial tcp 127.0.0.1:7980: connectex: No connection could be made because the target machine actively refused it.": OutcomeTCPRefused,
		"dial tcp 10.0.0.1:7980: i/o timeout": OutcomeTCPTimeout,
		// a name that does not resolve is a DNS failure, not an unreachable network
		"dial tcp: lookup nonexistent.invalid: no such host": OutcomeDNSFail,
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
		// the endpoint was reached, proved its identity and answered; calling
		// that "unreachable" would be false about a server we just talked to
		{status.Error(codes.Internal, "store: i/o error"), OutcomeServerError},
		{status.Error(codes.Unknown, "boom"), OutcomeServerError},
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
	if defaultMaxRecvMsgSize <= 4<<20 {
		t.Errorf("defaultMaxRecvMsgSize = %d, must exceed grpc's 4 MiB default", defaultMaxRecvMsgSize)
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
