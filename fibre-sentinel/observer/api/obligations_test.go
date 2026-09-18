package api_test

import (
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/plsgiveup/fibre/fibre-sentinel/internal/probe"
	"github.com/plsgiveup/fibre/fibre-sentinel/internal/scan"
	"github.com/plsgiveup/fibre/fibre-sentinel/observer/api"
	"github.com/plsgiveup/fibre/fibre-sentinel/observer/store"
)

type obligationsJSON struct {
	Total                 int64                    `json:"total"`
	Served                int64                    `json:"served"`
	Broken                int64                    `json:"broken"`
	EndUnobserved         int64                    `json:"end_unobserved"`
	Unobserved            int64                    `json:"unobserved"`
	UnobservedReachable   int64                    `json:"unobserved_reachable"`
	UnobservedUnreachable int64                    `json:"unobserved_unreachable"`
	UnobservedNotProbed   int64                    `json:"unobserved_not_probed"`
	Rate                  struct{ Num, Den int64 } `json:"rate"`
}

// obligationsFixture: one blob, eight validators, one obligation each, every
// profile the buckets are meant to separate.
func obligationsFixture(t *testing.T) *httptest.Server {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "observer.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	now := time.Now().UTC().Truncate(time.Second)
	created, msu := now.Add(-time.Hour), now.Add(time.Hour)
	const hash = "obl1"
	addrs := []string{"served", "endun", "broken", "unreach", "reach", "backoff", "gaplast", "unatt"}
	var vals []scan.ValidatorAssignment
	for i, a := range addrs {
		vals = append(vals, scan.ValidatorAssignment{Address: a, VotingPower: 10, RowCount: 2, Rows: []int{2 * i, 2*i + 1}, Attested: a != "unatt"})
	}
	pub := scan.Publication{
		SchemaVersion: scan.AttestationSchemaVersion, PromiseHash: hash,
		SettlementHeight: 100, SettlementTime: created, MustServeUntil: msu, RecordedAt: now,
		SettlementTxHash: "tx", Signer: "celestia1pub",
		Promise:                 scan.PromiseFields{ChainID: "t", Height: 99, Commitment: "cc", CreationTimestamp: created, BlobSize: 4096},
		ValidatorSignatureCount: 7,
		Assignment: scan.AssignmentTable{
			ProtocolParams:     scan.ProtocolParamsSnapshot{OriginalRows: 4, TotalRows: 16},
			ValidatorSetHeight: 99, TotalVotingPower: 80, Sigma: 16, Distinct: 16,
			ValidatorsWithRows: 8, AttestedWithRows: 7, SignatureEntries: 7, SignaturesVerified: 7,
			AttestedVotingPower: 70, Validators: vals,
		},
	}
	raw, err := json.Marshal(pub)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpsertPublication(pub, raw); err != nil {
		t.Fatal(err)
	}

	// what each validator's four in-window probes saw, in schedule order
	type wire struct {
		outcome probe.Outcome
		tls     bool
	}
	ok := wire{probe.OutcomeServedOK, true}
	err500 := wire{probe.OutcomeServerError, true}
	gone := wire{probe.OutcomeNotFound, true}
	refused := wire{probe.OutcomeTCPRefused, false}
	skipped := wire{probe.OutcomeReachable, true} // backoff: handshake done, download deliberately skipped
	profile := map[string][]wire{
		"served":  {ok, ok, ok, ok},
		"endun":   {ok, err500, err500, err500}, // served at 12%, answered 500 after: the early-prune profile
		"broken":  {ok, ok, ok, gone},
		"unreach": {refused, refused, refused, refused},
		"reach":   {err500, err500, err500, err500},
		"backoff": {skipped, skipped, skipped, skipped},
		"gaplast": {ok, ok, ok, skipped}, // a gap at the last point must not undo three served probes
		"unatt":   {gone, gone, gone, gone},
	}
	points := []string{"w1", "w2", "w3", "w4"}
	for addr, ws := range profile {
		for i, w := range ws {
			at := created.Add(time.Duration(i+1) * time.Minute)
			attested := addr != "unatt"
			class, reason := probe.Classify(probe.Evidence{
				Assigned: true, Attested: attested, Phase: probe.PhaseInWindow, Outcome: w.outcome,
			})
			m := probe.Measurement{
				SchemaVersion: probe.AttestationSchemaVersion, Vantage: "test",
				PromiseHash: hash, Commitment: "cc", MustServeUntil: msu, ValidatorSetHeight: 99,
				ValidatorAddress: addr, ValidatorHost: addr + ":443",
				Assigned: true, Attested: attested, AssignedRowCount: 2,
				ScheduleLabel: points[i], ScheduledAt: at, StartedAt: at, FinishedAt: at,
				Phase: probe.PhaseInWindow, Outcome: w.outcome,
				Classification: class, ClassificationReason: reason, TotalDurationMS: 10,
			}
			m.TCP.OK, m.TLS.OK, m.Identity.OK = w.tls, w.tls, w.tls
			if w.outcome == probe.OutcomeServedOK {
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
	if _, err := st.StartRun("collector", "test", "t", now); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(api.New(st, "test"))
	t.Cleanup(ts.Close)
	return ts
}

func TestObligationsAreJudgedByTheNewestProbe(t *testing.T) {
	ts := obligationsFixture(t)

	var net struct {
		Obligations obligationsJSON          `json:"obligations"`
		ByObl       struct{ Num, Den int64 } `json:"serve_rate_by_obligation"`
		ServeRate   struct{ Num, Den int64 } `json:"serve_rate"`
	}
	if code := get(t, ts, "/v1/network?window=all", &net); code != 200 {
		t.Fatalf("network: %d", code)
	}
	o := net.Obligations
	// The unattested validator is nothing to keep or break, so seven, not eight.
	if o.Total != 7 {
		t.Errorf("total = %d, want 7 proven obligations", o.Total)
	}
	if o.Served != 2 || o.Broken != 1 || o.EndUnobserved != 1 {
		t.Errorf("served/broken/end_unobserved = %d/%d/%d, want 2/1/1", o.Served, o.Broken, o.EndUnobserved)
	}
	if o.Unobserved != 3 || o.UnobservedReachable != 1 || o.UnobservedUnreachable != 1 || o.UnobservedNotProbed != 1 {
		t.Errorf("unobserved = %d (reachable %d, unreachable %d, not probed %d), want 3 (1, 1, 1)",
			o.Unobserved, o.UnobservedReachable, o.UnobservedUnreachable, o.UnobservedNotProbed)
	}
	if o.Rate.Num != 2 || o.Rate.Den != 3 {
		t.Errorf("obligation rate = %d/%d, want 2/3: only served and broken enter it", o.Rate.Num, o.Rate.Den)
	}
	if net.ByObl != o.Rate {
		t.Errorf("serve_rate_by_obligation %+v must repeat obligations.rate %+v", net.ByObl, o.Rate)
	}
	// The probe-based rate is still published and still says something
	// different: 11 HEALTHY over 12 rated probes.
	if net.ServeRate.Num != 11 || net.ServeRate.Den != 12 {
		t.Errorf("serve_rate = %d/%d, want 11/12", net.ServeRate.Num, net.ServeRate.Den)
	}

	var resp struct {
		Validators []struct {
			Address     string          `json:"address"`
			Obligations obligationsJSON `json:"obligations"`
		} `json:"validators"`
	}
	if code := get(t, ts, "/v1/validators?window=all", &resp); code != 200 {
		t.Fatalf("validators: %d", code)
	}
	by := map[string]obligationsJSON{}
	for _, v := range resp.Validators {
		by[v.Address] = v.Obligations
	}
	want := map[string]obligationsJSON{
		"served":  {Total: 1, Served: 1},
		"endun":   {Total: 1, EndUnobserved: 1},
		"broken":  {Total: 1, Broken: 1},
		"unreach": {Total: 1, Unobserved: 1, UnobservedUnreachable: 1},
		"reach":   {Total: 1, Unobserved: 1, UnobservedReachable: 1},
		"backoff": {Total: 1, Unobserved: 1, UnobservedNotProbed: 1},
		"gaplast": {Total: 1, Served: 1},
		"unatt":   {},
	}
	for addr, w := range want {
		g := by[addr]
		g.Rate = struct{ Num, Den int64 }{}
		if g != w {
			t.Errorf("%s: obligations = %+v, want %+v", addr, g, w)
		}
	}
}
