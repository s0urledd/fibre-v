package api_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/plsgiveup/fibre/fibre-sentinel/internal/probe"
	"github.com/plsgiveup/fibre/fibre-sentinel/internal/scan"
	"github.com/plsgiveup/fibre/fibre-sentinel/observer/api"
	"github.com/plsgiveup/fibre/fibre-sentinel/observer/store"
	"github.com/plsgiveup/fibre/fibre-sentinel/observer/verdict"
)

// heldJSON is the part of /v1/network these tests read.
type heldJSON struct {
	Faults      int64            `json:"faults"`
	Classes     map[string]int64 `json:"classes"`
	Obligations struct {
		Total                 int64 `json:"total"`
		Served                int64 `json:"served"`
		Broken                int64 `json:"broken"`
		EndUnobserved         int64 `json:"end_unobserved"`
		HeldParamUnverified   int64 `json:"held_param_unverified"`
		Unobserved            int64 `json:"unobserved"`
		UnobservedReachable   int64 `json:"unobserved_reachable"`
		UnobservedUnreachable int64 `json:"unobserved_unreachable"`
		UnobservedNotProbed   int64 `json:"unobserved_not_probed"`
		Pending               int64 `json:"pending"`
	} `json:"obligations"`
	ServeRate struct {
		Value *float64 `json:"value"`
		Num   int64    `json:"num"`
		Den   int64    `json:"den"`
	} `json:"serve_rate"`
	Coverage struct {
		Num int64 `json:"num"`
		Den int64 `json:"den"`
	} `json:"serve_rate_coverage"`
	HeldOut     map[string]int64 `json:"serve_rate_held_out"`
	Attestation struct {
		AttestedProbes   int64 `json:"attested_probes"`
		UnattestedProbes int64 `json:"unattested_probes"`
		UnknownProbes    int64 `json:"unknown_probes"`
	} `json:"attestation"`
	RetentionUncertainty *struct {
		OpenRanges       int64  `json:"open_ranges"`
		PublicationsHeld int64  `json:"publications_held"`
		ProbesHeld       int64  `json:"probes_held"`
		Note             string `json:"note"`
	} `json:"retention_uncertainty"`
	VantageHealth struct {
		Suspect []struct {
			At     string `json:"at"`
			Reason string `json:"reason"`
		} `json:"suspect"`
	} `json:"vantage_health"`
}

// heldFixture builds one publication whose promise sits at height 150,
// inside the range 121-180 the tests open, with `outcomes` per validator
// over four in-window schedule points.
func heldFixture(t *testing.T, outcomes map[string][]probe.Outcome) (*store.Store, time.Time, time.Time) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "observer.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	now := time.Now().UTC().Truncate(time.Second)
	created, msu := now.Add(-2*time.Hour), now.Add(-30*time.Minute)

	var addrs []string
	for a := range outcomes {
		addrs = append(addrs, a)
	}
	sortStrings(addrs)
	var vals []scan.ValidatorAssignment
	for i, a := range addrs {
		vals = append(vals, scan.ValidatorAssignment{Address: a, VotingPower: 10, RowCount: 2, Rows: []int{2 * i, 2*i + 1}, Attested: true})
	}
	pub := scan.Publication{
		SchemaVersion: scan.AttestationSchemaVersion, PromiseHash: "held1",
		SettlementHeight: 150, SettlementTime: created, MustServeUntil: msu, RecordedAt: now,
		SettlementTxHash: "txheld", Signer: "celestia1pub",
		Promise:                 scan.PromiseFields{ChainID: "t", Height: 150, Commitment: "ccheld", CreationTimestamp: created, BlobSize: 4096},
		ValidatorSignatureCount: len(vals),
		Assignment: scan.AssignmentTable{
			ProtocolParams:     scan.ProtocolParamsSnapshot{OriginalRows: 4, TotalRows: 16},
			ValidatorSetHeight: 149, TotalVotingPower: int64(10 * len(vals)), Sigma: 2 * len(vals), Distinct: 2 * len(vals),
			ValidatorsWithRows: len(vals), AttestedWithRows: len(vals), SignatureEntries: len(vals), SignaturesVerified: len(vals),
			AttestedVotingPower: int64(10 * len(vals)), Validators: vals,
		},
	}
	raw, err := json.Marshal(pub)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpsertPublication(pub, raw); err != nil {
		t.Fatal(err)
	}
	points := []string{"w1", "w2", "w3", "w4"}
	for addr, os := range outcomes {
		for i, o := range os {
			at := inWindowPoint(created, msu, i)
			class, reason := probe.Classify(probe.Evidence{Assigned: true, Attested: true, Phase: probe.PhaseInWindow, Outcome: o})
			m := probe.Measurement{
				SchemaVersion: probe.AttestationSchemaVersion, Vantage: "test",
				PromiseHash: "held1", Commitment: "ccheld", MustServeUntil: msu, ValidatorSetHeight: 149,
				ValidatorAddress: addr, ValidatorHost: addr + ":443",
				Assigned: true, Attested: true, AssignedRowCount: 2,
				ScheduleLabel: points[i], ScheduledAt: at, StartedAt: at, FinishedAt: at,
				Phase: probe.PhaseInWindow, Outcome: o,
				Classification: class, ClassificationReason: reason, TotalDurationMS: 10,
			}
			m.TCP.OK, m.TLS.OK, m.Identity.OK = true, true, true
			if o == probe.OutcomeServedOK {
				m.Download.OK, m.Download.RowsReturned, m.Download.RowsExpected = true, 2, 2
				m.Download.CommitmentVerified, m.Download.AssignmentVerified = true, true
			}
			raw, err := json.Marshal(m)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := st.InsertProbe(m, raw); err != nil {
				t.Fatal(err)
			}
		}
	}
	return st, created, msu
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

// openRange records a silent shortening over heights 121-180 that could not
// be closed, so it holds. The publication's promise height is 150.
func openRange(t *testing.T, st *store.Store) scan.ParamUncertainty {
	t.Helper()
	u := scan.ParamUncertainty{
		SchemaVersion: scan.ParamUncertaintySchemaVersion,
		ID:            "t:silent_change:121-180", ChainID: "t", Kind: scan.UncertaintySilentChange,
		FromHeight: 121, ToHeight: 180, EffectiveFromHeight: 181, IntervalStartKnown: true,
		Direction: "shorter", WindowBeforeS: 14400, WindowAfterS: 3600,
		PublicationsAffected: 1, DetectedAt: time.Now().UTC(),
		Resolution: scan.ResolutionUnresolvable, ResolveError: "the node cannot answer for those heights",
	}
	raw, err := json.Marshal(u)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpsertParamUncertainty(u, raw); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SyncParamHolds(context.Background()); err != nil {
		t.Fatal(err)
	}
	return u
}

func networkHeld(t *testing.T, st *store.Store) heldJSON {
	t.Helper()
	ts := httptest.NewServer(api.New(st, "test"))
	t.Cleanup(ts.Close)
	var out heldJSON
	get(t, ts, "/v1/network?window=24h", &out)
	return out
}

// The open P1, closed. Two validators prune on the retention the server
// really used and answer NOT_FOUND inside the window this observer still
// thinks is running. Two is under the correlated-failure guard's floor of
// three, so the guard does not fire and never would: the guard is a
// mitigation for a correlated outage, not for the observer being wrong
// about the deadline.
//
// The first half asserts the defect as a fact, so that anyone who "fixes"
// this by weakening the guard instead breaks a test that says why that is
// not the fix.
func TestTwoValidatorsPruningOnAShortenedRetentionAreNotPublishedAsFaults(t *testing.T) {
	ok := []probe.Outcome{probe.OutcomeServedOK, probe.OutcomeServedOK, probe.OutcomeServedOK, probe.OutcomeServedOK}
	gone := []probe.Outcome{probe.OutcomeServedOK, probe.OutcomeServedOK, probe.OutcomeNotFound, probe.OutcomeNotFound}
	outcomes := map[string][]probe.Outcome{
		"v1": gone, "v2": gone,
		"v3": ok, "v4": ok, "v5": ok, "v6": ok, "v7": ok, "v8": ok,
	}

	st, _, _ := heldFixture(t, outcomes)

	// Without the range on record: the fault is published, and the guard
	// says nothing about it.
	before := networkHeld(t, st)
	if before.Faults != 4 {
		t.Fatalf("without the range, faults = %d, want 4 (two validators x two points)", before.Faults)
	}
	if before.Obligations.Broken != 2 {
		t.Fatalf("without the range, broken = %d, want 2", before.Obligations.Broken)
	}
	if len(before.VantageHealth.Suspect) != 0 {
		t.Fatalf("two faulting validators is under MinValidators=%d; the guard must not fire: %+v",
			verdict.MinValidators, before.VantageHealth.Suspect)
	}
	if before.RetentionUncertainty != nil {
		t.Fatal("nothing is held yet")
	}

	// With it: neither the fault nor the credit is published.
	openRange(t, st)
	after := networkHeld(t, st)
	if after.Faults != 0 {
		t.Fatalf("faults = %d, want 0: this observer cannot say when the obligation ended", after.Faults)
	}
	if after.Obligations.Broken != 0 {
		t.Fatalf("broken = %d, want 0", after.Obligations.Broken)
	}
	if after.Obligations.HeldParamUnverified != 8 {
		t.Fatalf("held_param_unverified = %d, want 8: every obligation of the covered publication", after.Obligations.HeldParamUnverified)
	}
	if after.Obligations.Served != 0 {
		t.Fatalf("served = %d, want 0: withholding only the accusations would raise the rate", after.Obligations.Served)
	}
	if after.Obligations.Unobserved != 0 {
		t.Fatalf("unobserved = %d, want 0: the observer looked, it just cannot speak for what it saw", after.Obligations.Unobserved)
	}
	if n := after.HeldOut["RETENTION_UNVERIFIED"]; n != 32 {
		t.Fatalf("serve_rate_held_out[RETENTION_UNVERIFIED] = %d, want 32 (8 validators x 4 points)", n)
	}
	if after.ServeRate.Den != 0 || after.ServeRate.Value != nil {
		t.Fatalf("serve_rate = %+v, want no rate at all", after.ServeRate)
	}
	if after.RetentionUncertainty == nil || after.RetentionUncertainty.PublicationsHeld != 1 || after.RetentionUncertainty.OpenRanges != 1 {
		t.Fatalf("retention_uncertainty = %+v, want one range holding one publication", after.RetentionUncertainty)
	}
	// The removal is visible, not silent: the rows never left the
	// population, they changed name.
	if after.Coverage.Den != before.Coverage.Den {
		t.Fatalf("coverage.den moved from %d to %d; the hold is an override, not an exclusion", before.Coverage.Den, after.Coverage.Den)
	}
	assertReconciles(t, after)
}

// A share under the guard's threshold is the other half of the same hole:
// four of ten validators faulting is 40%, under FaultThreshold, so the
// guard does not fire. The hold does not care about the share at all —
// it is keyed on the publication, not on the schedule point.
func TestAShareUnderTheGuardThresholdIsStillHeld(t *testing.T) {
	ok := []probe.Outcome{probe.OutcomeServedOK, probe.OutcomeServedOK, probe.OutcomeServedOK, probe.OutcomeServedOK}
	gone := []probe.Outcome{probe.OutcomeServedOK, probe.OutcomeServedOK, probe.OutcomeNotFound, probe.OutcomeNotFound}
	outcomes := map[string][]probe.Outcome{}
	for _, a := range []string{"f1", "f2", "f3", "f4"} {
		outcomes[a] = gone
	}
	for _, a := range []string{"g1", "g2", "g3", "g4", "g5", "g6"} {
		outcomes[a] = ok
	}
	if share := 4.0 / 10.0; share >= verdict.FaultThreshold {
		t.Fatalf("this test no longer sits under the guard's threshold: %v >= %v", share, verdict.FaultThreshold)
	}

	st, _, _ := heldFixture(t, outcomes)
	before := networkHeld(t, st)
	if before.Faults != 8 || before.Obligations.Broken != 4 {
		t.Fatalf("without the range: faults=%d broken=%d, want 8/4", before.Faults, before.Obligations.Broken)
	}
	if len(before.VantageHealth.Suspect) != 0 {
		t.Fatalf("40%% faulting is under the threshold; the guard must not fire: %+v", before.VantageHealth.Suspect)
	}

	openRange(t, st)
	after := networkHeld(t, st)
	if after.Faults != 0 || after.Obligations.Broken != 0 {
		t.Fatalf("faults=%d broken=%d, want 0/0", after.Faults, after.Obligations.Broken)
	}
	// The symmetry that matters: the six clean validators are withheld too.
	// If only the four accusations were withheld, the published rate would
	// read 6/6 = 100% off the back of a deadline this observer cannot
	// vouch for — the exact inflation the codebase refuses for UNATTESTED
	// and for grace probes.
	if after.Obligations.Served != 0 || after.ServeRate.Den != 0 {
		t.Fatalf("served=%d serve_rate.den=%d: withholding only the failures inflates the rate",
			after.Obligations.Served, after.ServeRate.Den)
	}
	if after.Obligations.HeldParamUnverified != 10 {
		t.Fatalf("held_param_unverified = %d, want 10", after.Obligations.HeldParamUnverified)
	}
	assertReconciles(t, after)
}

// A held row is still a row, so the response must still reconcile against
// itself exactly as docs/verdicts.md promises. This is what the class
// override buys over a WHERE exclusion, and it is asserted rather than
// argued.
func assertReconciles(t *testing.T, r heldJSON) {
	t.Helper()
	if sum := r.Attestation.AttestedProbes + r.Attestation.UnattestedProbes + r.Attestation.UnknownProbes; sum != r.Coverage.Den {
		t.Errorf("serve_rate_coverage.den = %d but attested+unattested+unknown = %d", r.Coverage.Den, sum)
	}
	if r.HeldOut["UNATTESTED"] != r.Attestation.UnattestedProbes {
		t.Errorf("serve_rate_held_out[UNATTESTED] = %d but attestation.unattested_probes = %d",
			r.HeldOut["UNATTESTED"], r.Attestation.UnattestedProbes)
	}
	var out int64
	for _, n := range r.HeldOut {
		out += n
	}
	if r.ServeRate.Den+out != r.Coverage.Den {
		t.Errorf("serve_rate.den (%d) + held out (%d) = %d, want coverage.den %d", r.ServeRate.Den, out, r.ServeRate.Den+out, r.Coverage.Den)
	}
	o := r.Obligations
	if sum := o.Broken + o.Served + o.EndUnobserved + o.HeldParamUnverified +
		o.UnobservedReachable + o.UnobservedUnreachable + o.UnobservedNotProbed + o.Pending; sum != o.Total {
		t.Errorf("the obligation buckets sum to %d, not total %d", sum, o.Total)
	}
	if o.Unobserved != o.UnobservedReachable+o.UnobservedUnreachable+o.UnobservedNotProbed {
		t.Errorf("unobserved (%d) absorbed something it should not have", o.Unobserved)
	}
}

// Verifying the range lifts the hold and the withheld verdicts come back,
// recomputed against the deadline the proven params support. This is the
// half of the mechanism the two tests above do not reach: withholding that
// never ends is not a measurement.
func TestAVerifiedRangeGivesTheVerdictsBackUnderTheCorrectedDeadline(t *testing.T) {
	ok := []probe.Outcome{probe.OutcomeServedOK, probe.OutcomeServedOK, probe.OutcomeServedOK, probe.OutcomeServedOK}
	st, _, _ := heldFixture(t, map[string][]probe.Outcome{"v1": ok, "v2": ok, "v3": ok})
	openRange(t, st)
	if got := networkHeld(t, st); got.Obligations.HeldParamUnverified != 3 {
		t.Fatalf("held = %d, want 3", got.Obligations.HeldParamUnverified)
	}

	// The same range, now closed by reading every height, with no change to
	// the window: the deadline does not move and every verdict returns.
	u := scan.ParamUncertainty{
		SchemaVersion: scan.ParamUncertaintySchemaVersion,
		ID:            "t:silent_change:121-180", ChainID: "t", Kind: scan.UncertaintySilentChange,
		FromHeight: 121, ToHeight: 180, EffectiveFromHeight: 181, IntervalStartKnown: true,
		Direction: "shorter", PublicationsAffected: 1, DetectedAt: time.Now().UTC(),
		Resolution: scan.ResolutionVerified, HeightsRead: 61, ResolveMethod: "exhaustive_read",
	}
	raw, err := json.Marshal(u)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpsertParamUncertainty(u, raw); err != nil {
		t.Fatal(err)
	}
	if err := st.MarkRangeCorrected(context.Background(), u.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SyncParamHolds(context.Background()); err != nil {
		t.Fatal(err)
	}

	after := networkHeld(t, st)
	if after.Obligations.HeldParamUnverified != 0 {
		t.Fatalf("held = %d after the range was verified, want 0", after.Obligations.HeldParamUnverified)
	}
	if after.Obligations.Served != 3 {
		t.Fatalf("served = %d, want 3: the withheld credit comes back", after.Obligations.Served)
	}
	if after.RetentionUncertainty != nil {
		t.Fatalf("retention_uncertainty is still set: %+v", after.RetentionUncertainty)
	}
	assertReconciles(t, after)
}
