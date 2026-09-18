package api_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/plsgiveup/fibre/fibre-sentinel/internal/probe"
	"github.com/plsgiveup/fibre/fibre-sentinel/internal/scan"
	"github.com/plsgiveup/fibre/fibre-sentinel/observer/rollup"
	"github.com/plsgiveup/fibre/fibre-sentinel/observer/store"
)

// insertPub writes one publication over commitment with the given per
// validator rows, settled at `settled`, timeout one hour, retention 30 min.
func insertPub(t *testing.T, st *store.Store, hash, commitment string, settled time.Time, rows map[string][]int) scan.Publication {
	return insertPubRetained(t, st, hash, commitment, settled, 30*time.Minute, rows)
}

func insertPubRetained(t *testing.T, st *store.Store, hash, commitment string, settled time.Time, retention time.Duration, rows map[string][]int) scan.Publication {
	t.Helper()
	var vals []scan.ValidatorAssignment
	for addr, r := range rows {
		vals = append(vals, scan.ValidatorAssignment{Address: addr, VotingPower: 10, RowCount: len(r), Rows: r, Attested: true})
	}
	pub := scan.Publication{
		SchemaVersion: scan.AttestationSchemaVersion, PromiseHash: hash,
		SettlementHeight: 300, SettlementTime: settled, MustServeUntil: settled.Add(retention), RecordedAt: settled,
		SettlementTxHash: "tx" + hash, Signer: "celestia1pub",
		Promise:                 scan.PromiseFields{ChainID: "t", Height: 299, Commitment: commitment, CreationTimestamp: settled.Add(-time.Minute), BlobSize: 4096},
		ValidatorSignatureCount: len(vals),
		Assignment: scan.AssignmentTable{
			ProtocolParams:     scan.ProtocolParamsSnapshot{OriginalRows: 4, TotalRows: 16},
			ValidatorSetHeight: 299, TotalVotingPower: int64(10 * len(vals)), Sigma: 4, Distinct: 4,
			ValidatorsWithRows: len(vals), AttestedWithRows: len(vals), SignatureEntries: len(vals), SignaturesVerified: len(vals),
			AttestedVotingPower: int64(10 * len(vals)), Validators: vals,
		},
	}
	pub.ParamsAtPublication.PaymentPromiseTimeoutSeconds = 3600
	pub.ParamsAtPublication.ShardRetentionSeconds = 1800
	raw, err := json.Marshal(pub)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpsertPublication(pub, raw); err != nil {
		t.Fatal(err)
	}
	return pub
}

// insertDeferred writes one in-window probe row of pub for addr that
// returned genuine rows `got`, deferred at the probe with reason gap.
func insertDeferred(t *testing.T, st *store.Store, pub scan.Publication, addr string, at time.Time, got []uint32, gap string) probe.Measurement {
	t.Helper()
	m := probe.Measurement{
		SchemaVersion: probe.AttestationSchemaVersion, Vantage: "test",
		PromiseHash: pub.PromiseHash, Commitment: pub.Promise.Commitment, MustServeUntil: pub.MustServeUntil, ValidatorSetHeight: 299,
		ValidatorAddress: addr, ValidatorHost: addr + ":443", Assigned: true, Attested: true, AssignedRowCount: 2,
		ScheduleLabel: "w2", ScheduledAt: at, StartedAt: at, FinishedAt: at.Add(time.Second),
		Phase: probe.PhaseInWindow, Outcome: probe.OutcomeWrongRows, TotalDurationMS: 1000,
	}
	m.TCP.OK, m.TLS.OK, m.Identity.OK = true, true, true
	m.Download.Attempted, m.Download.RowsReturned, m.Download.RowsExpected = true, len(got), 2
	m.Download.CommitmentVerified, m.Download.RowIndices, m.Download.ShadowGap = true, got, gap
	m.Classification, m.ClassificationReason = probe.Classify(probe.Evidence{Assigned: true, Attested: true, Phase: probe.PhaseInWindow,
		Outcome: probe.OutcomeWrongRows, CommitmentVerified: true, ShadowUncertain: true})
	if m.Classification != probe.ClassProbeError {
		t.Fatalf("deferred row classified %s at the probe", m.Classification)
	}
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.InsertProbe(m, raw); err != nil {
		t.Fatal(err)
	}
	return m
}

// The Fibre store serves the first shard of a commitment by promise-hash
// order, so a promise settled after the probe can own the rows it
// returned. The prober defers; the collector judges once the scanner has
// read past probe time + payment_promise_timeout: a match is SHADOWED_SHARD,
// no match is UNMATCHED_GENUINE, a scan gap stays PROBE_ERROR, and the row
// keeps the verdict it was stamped with.
func TestLateShadowVerdicts(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "observer.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	ctx := context.Background()
	t0 := time.Date(2026, 9, 18, 10, 0, 0, 0, time.UTC)
	// A settles first; B, over the same blob, settles 20 min after A's probe
	// and sorts before it by hash, so B's shard answers downloads of A
	a := insertPub(t, st, "aaaa", "cc1", t0, map[string][]int{"v1": {0, 1}, "v2": {2, 3}})
	probeAt := t0.Add(10 * time.Minute)
	insertPub(t, st, "0bbb", "cc1", probeAt.Add(20*time.Minute), map[string][]int{"v1": {4, 5}, "v2": {6, 7}})
	// an unrelated promise over another blob must never match
	insertPub(t, st, "zzzz", "cc2", t0, map[string][]int{"v1": {8, 9}})
	shadowed := insertDeferred(t, st, a, "v1", probeAt, []uint32{5, 4}, "shadow_pending: a promise uploaded before this probe may settle until "+probeAt.Add(time.Hour).Format(time.RFC3339))
	faulty := insertDeferred(t, st, a, "v2", probeAt, []uint32{2}, "shadow_pending: …")
	gapped := insertDeferred(t, st, a, "v3", probeAt, []uint32{9}, probe.ShadowGapScanPrefix+" #100-#110 (2026-09-18T09:30:00Z to 2026-09-18T09:31:00Z) overlaps the shard lifetime")

	// the frontier has not reached probe time + timeout: only the scan gap
	// is judged, permanently
	ams, err := st.LateShadowVerdicts(ctx, probeAt.Add(30*time.Minute), t0.Add(2*time.Hour), 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(ams) != 1 || ams[0].DedupeKey != gapped.DedupeKey() || ams[0].To != "PROBE_ERROR" {
		t.Fatalf("before the frontier: %+v", ams)
	}
	// past it: every candidate is on record
	frontier := probeAt.Add(time.Hour)
	ams, err = st.LateShadowVerdicts(ctx, frontier, t0.Add(3*time.Hour), 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	byKey := map[string]store.Amendment{}
	for _, a := range ams {
		byKey[a.DedupeKey] = a
	}
	if a := byKey[shadowed.DedupeKey()]; a.To != "SHADOWED_SHARD" || a.ShadowedBy != "0bbb" || a.From != "PROBE_ERROR" {
		t.Errorf("shadowed row: %+v", a)
	}
	// no settled promise assigns these rows: held out, not a fault, since
	// an upload that never settled can answer under hash-order serving
	if a := byKey[faulty.DedupeKey()]; a.To != "UNMATCHED_GENUINE" || a.ShadowedBy != "" {
		t.Errorf("unmatched row: %+v", a)
	}
	if a := byKey[gapped.DedupeKey()]; a.To != "PROBE_ERROR" {
		t.Errorf("gapped row: %+v", a)
	}
	for _, a := range ams {
		if a.PruneToleranceS != 300 {
			t.Errorf("amendment without the tolerance it was drawn with: %+v", a)
		}
	}
	applied := 0
	for _, a := range ams {
		ok, err := st.ApplyAmendment(a)
		if err != nil {
			t.Fatal(err)
		}
		if ok {
			applied++
		}
	}
	if applied != 3 {
		t.Errorf("applied %d, want 3", applied)
	}
	// idempotent: nothing left to judge, nothing re-applied
	if ams, _ := st.LateShadowVerdicts(ctx, frontier, t0.Add(3*time.Hour), 5*time.Minute); len(ams) != 0 {
		t.Errorf("re-judged %d rows", len(ams))
	}
	if ok, _ := st.ApplyAmendment(byKey[faulty.DedupeKey()]); ok {
		t.Error("an amendment applied twice")
	}
	var cls, atProbe, sb string
	var amended *string
	if err := st.DB().QueryRow(`SELECT classification, classification_at_probe, COALESCE(shadowed_by, ''), amended_at FROM probes WHERE dedupe_key = ?`,
		shadowed.DedupeKey()).Scan(&cls, &atProbe, &sb, &amended); err != nil {
		t.Fatal(err)
	}
	if cls != "SHADOWED_SHARD" || atProbe != "PROBE_ERROR" || sb != "0bbb" || amended == nil {
		t.Errorf("row after amendment: %s %s %s %v", cls, atProbe, sb, amended)
	}
	// the API shows both verdicts
	ts := httptestServer(t, st)
	var out struct {
		Probes []struct {
			Validator             string `json:"validator_address"`
			Classification        string `json:"classification"`
			ClassificationAtProbe string `json:"classification_at_probe"`
			AmendedAt             string `json:"amended_at"`
			ShadowGap             string `json:"shadow_gap"`
		} `json:"probes"`
	}
	if code := get(t, ts, "/v1/probes?limit=10", &out); code != 200 {
		t.Fatalf("probes: %d", code)
	}
	seen := 0
	for _, p := range out.Probes {
		if p.Validator == "v2" && p.Classification == "UNMATCHED_GENUINE" && p.ClassificationAtProbe == "PROBE_ERROR" && p.AmendedAt != "" && p.ShadowGap != "" {
			seen++
		}
	}
	if seen != 1 {
		t.Errorf("amended UNMATCHED_GENUINE row not shown with its probe-time verdict: %+v", out.Probes)
	}

	// the rollup waits for the deferred verdict and for the windows to close
	late := t0.Add(-40 * 24 * time.Hour)
	old := insertPub(t, st, "old1", "cc9", late, map[string][]int{"v1": {0, 1}})
	insertDeferred(t, st, old, "v1", late.Add(5*time.Minute), []uint32{1}, "shadow_pending: …")
	now := t0.Add(time.Hour)
	rep, err := rollup.Run(ctx, st, now, rollup.Config{RollupAfter: 14 * 24 * time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.RolledDays) != 0 || rep.Waiting != late.Format("2006-01-02") || rep.WaitingWhy == "" {
		t.Fatalf("rolled %v, waiting %q (%s); want to wait on the deferred row", rep.RolledDays, rep.Waiting, rep.WaitingWhy)
	}
	ams, _ = st.LateShadowVerdicts(ctx, frontier, now, 5*time.Minute)
	for _, a := range ams {
		if _, err := st.ApplyAmendment(a); err != nil {
			t.Fatal(err)
		}
	}
	rep, err = rollup.Run(ctx, st, now, rollup.Config{RollupAfter: 14 * 24 * time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.RolledDays) == 0 || rep.RolledDays[0] != late.Format("2006-01-02") {
		t.Fatalf("after the verdict: rolled %v, waiting %q (%s)", rep.RolledDays, rep.Waiting, rep.WaitingWhy)
	}
	// a day whose promise is still under obligation is not final either,
	// whatever RollupAfter says: a three-day retention settled on t0's day
	// holds that day (and every later one) while it runs
	insertPubRetained(t, st, "long", "cc7", t0.Add(time.Hour), 3*24*time.Hour, map[string][]int{"v1": {0, 1}})
	rep, err = rollup.Run(ctx, st, t0.Add(26*time.Hour), rollup.Config{RollupAfter: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Waiting != t0.Format("2006-01-02") || rep.WaitingWhy == "" {
		t.Errorf("waiting %q (%s), want t0's day held by the open window", rep.Waiting, rep.WaitingWhy)
	}
	for _, d := range rep.RolledDays {
		if d >= t0.Format("2006-01-02") {
			t.Errorf("day %s rolled while a promise settled on it was under obligation", d)
		}
	}
}

// A publication with no payment_promise_timeout on record names no bound
// by which every candidate has settled, so no frontier ever closes the
// question; the row is a gap for good rather than a deferral that holds
// its day's rollup and prune forever.
func TestLateShadow_NoTimeoutOnRecordIsAPermanentGap(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "observer.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	ctx := context.Background()
	t0 := time.Date(2026, 9, 18, 10, 0, 0, 0, time.UTC)
	a := insertPub(t, st, "aaaa", "cc1", t0, map[string][]int{"v1": {0, 1}})
	if _, err := st.DB().Exec(`UPDATE publications SET payment_promise_timeout_s = 0 WHERE promise_hash = ?`, a.PromiseHash); err != nil {
		t.Fatal(err)
	}
	probeAt := t0.Add(10 * time.Minute)
	m := insertDeferred(t, st, a, "v1", probeAt, []uint32{7}, probe.ShadowGapPendingPrefix+": a promise uploaded before this probe may settle after it (payment promise timeout not on record)")
	ams, err := st.LateShadowVerdicts(ctx, probeAt.Add(time.Minute), t0.Add(time.Hour), 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(ams) != 1 || ams[0].DedupeKey != m.DedupeKey() || ams[0].To != "PROBE_ERROR" || !strings.Contains(ams[0].Reason, "payment_promise_timeout") {
		t.Fatalf("no timeout on record: %+v", ams)
	}
}
