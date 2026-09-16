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

// The stability layers an operator reads, and the unit each is counted in.
//
// These exist because the same fact stated in the wrong unit reads as a
// different, worse fact. An obligation is probed at four schedule points, so
// a probe count of anything held out of the serve rate runs four times the
// number of blobs it actually concerns, and "812 unattested" against a named
// operator is not the same claim as "203 blobs carried no signature from you".

// stabilityFixture builds two publications over two validators. v1 carries a
// verified signature on both; v2 carries none, which is what the two-thirds
// quorum leaves behind. Every validator is probed at all four in-window
// points of both blobs, so probe counts are exactly four times obligation
// counts and a test can tell which unit a figure is in.
//
// v1 serves at w1 and w2 of the second blob and returns nothing at w3 and w4:
// a validator that prunes early, which the pooled rate cannot distinguish
// from one that is uniformly poor.
//
// Reachability heartbeats: ten for each validator, of which v2's last three
// did not complete TLS.
func stabilityFixture(t *testing.T) *httptest.Server {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "observer.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	now := time.Now().UTC().Truncate(time.Second)
	points := []string{"w1", "w2", "w3", "w4"}

	for bi, hash := range []string{"b001", "b002"} {
		created := now.Add(-time.Duration(60-bi*10) * time.Minute)
		msu := now.Add(time.Duration(30+bi*10) * time.Minute)
		pub := scan.Publication{
			SchemaVersion:    scan.AttestationSchemaVersion,
			PromiseHash:      hash,
			SettlementHeight: int64(100 + bi),
			SettlementTime:   created,
			MustServeUntil:   msu,
			RecordedAt:       now,
			SettlementTxHash: "tx" + hash,
			Signer:           "celestia1pub",
			Promise: scan.PromiseFields{ChainID: "t", Height: 99, Commitment: "cc" + hash,
				CreationTimestamp: created, BlobSize: 1024},
			ValidatorSignatureCount: 1,
			Assignment: scan.AssignmentTable{
				ProtocolParams:      scan.ProtocolParamsSnapshot{OriginalRows: 4, TotalRows: 8},
				ValidatorSetHeight:  99,
				TotalVotingPower:    20,
				Sigma:               4,
				Distinct:            4,
				ValidatorsWithRows:  2,
				AttestedWithRows:    1,
				SignatureEntries:    1,
				SignaturesVerified:  1,
				AttestedVotingPower: 10,
				Validators: []scan.ValidatorAssignment{
					{Address: "v1", VotingPower: 10, RowCount: 2, Rows: []int{0, 1}, Attested: true},
					{Address: "v2", VotingPower: 10, RowCount: 2, Rows: []int{2, 3}, Attested: false},
				},
			},
		}
		raw, err := json.Marshal(pub)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := st.UpsertPublication(pub, raw); err != nil {
			t.Fatal(err)
		}

		for pi, label := range points {
			at := created.Add(time.Duration(pi+1) * time.Minute)
			for _, addr := range []string{"v1", "v2"} {
				attested := addr == "v1"
				outcome := probe.OutcomeServedOK
				// v1 prunes the second blob after the first half of its window.
				if addr == "v1" && bi == 1 && pi >= 2 {
					outcome = probe.OutcomeNotFound
				}
				if addr == "v2" {
					outcome = probe.OutcomeNotFound
				}
				class, reason := probe.Classify(probe.Evidence{
					Assigned: true, Attested: attested, Phase: probe.PhaseInWindow, Outcome: outcome,
				})
				m := probe.Measurement{
					SchemaVersion: probe.AttestationSchemaVersion, Vantage: "test",
					PromiseHash: hash, Commitment: "cc" + hash, MustServeUntil: msu, ValidatorSetHeight: 99,
					ValidatorAddress: addr, ValidatorHost: addr + ":443",
					Assigned: true, Attested: attested, AssignedRowCount: 2,
					ScheduleLabel: label, ScheduledAt: at, StartedAt: at, FinishedAt: at,
					Phase: probe.PhaseInWindow, Outcome: outcome,
					Classification: class, ClassificationReason: reason,
				}
				if outcome == probe.OutcomeServedOK {
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
	}

	for i := 0; i < 10; i++ {
		at := now.Add(-time.Duration(10-i) * 10 * time.Minute)
		for _, addr := range []string{"v1", "v2"} {
			up := addr == "v1" || i < 7
			m := probe.Measurement{
				SchemaVersion: probe.AttestationSchemaVersion, Vantage: "test",
				ValidatorAddress: addr, ValidatorHost: addr + ":443", ValidatorSetHeight: 99,
				ScheduledAt: at, StartedAt: at, FinishedAt: at,
			}
			m.DNS.OK = true
			m.TCP.OK, m.TLS.OK = up, up
			// v2's endpoint answers throughout but its endorsement lapsed for
			// the two heartbeats before it went down, so identity validity and
			// reachability are different measurements of different things.
			m.Identity.OK = up && !(addr == "v2" && i >= 5)
			if up {
				m.Outcome = probe.OutcomeReachable
			} else {
				m.Outcome = probe.OutcomeTCPRefused
			}
			raw, err := json.Marshal(m)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := st.InsertReachability(m, raw); err != nil {
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

type stabilityValidator struct {
	Address     string `json:"address"`
	Attestation struct {
		Attested        int64                    `json:"attested_probes"`
		Unattested      int64                    `json:"unattested_probes"`
		Unknown         int64                    `json:"unknown_probes"`
		AttestedBlobs   int64                    `json:"attested_blobs"`
		UnattestedBlobs int64                    `json:"unattested_blobs"`
		UnknownBlobs    int64                    `json:"unknown_blobs"`
		BlobCoverage    struct{ Num, Den int64 } `json:"blob_coverage"`
	} `json:"attestation"`
	HeldOut       map[string]int64 `json:"serve_rate_held_out"`
	Uptime        rateJSON         `json:"reachability_window"`
	IdentityValid rateJSON         `json:"identity_rate_window"`
	LastDown      *string          `json:"last_unreachable_at"`
	ByPoint       []struct {
		Key  string   `json:"key"`
		Rate rateJSON `json:"serve_rate"`
	} `json:"serve_rate_by_point"`
}

type rateJSON struct {
	Num   int64    `json:"num"`
	Den   int64    `json:"den"`
	Value *float64 `json:"value"`
}

func stabilityValidators(t *testing.T, ts *httptest.Server) map[string]stabilityValidator {
	t.Helper()
	var resp struct {
		Validators []stabilityValidator `json:"validators"`
	}
	if code := get(t, ts, "/v1/validators?window=all", &resp); code != 200 {
		t.Fatalf("validators: %d", code)
	}
	out := map[string]stabilityValidator{}
	for _, v := range resp.Validators {
		out[v.Address] = v
	}
	return out
}

// The figure a page shows a named operator must be counted in blobs, and the
// probe count must stay available and stay labelled as probes. Both blobs of
// v2 are unattested and each was probed four times, so the two units differ by
// exactly the size of the schedule.
func TestUnattestedIsCountedPerObligationAndPerProbe(t *testing.T) {
	vals := stabilityValidators(t, stabilityFixture(t))
	v2, ok := vals["v2"]
	if !ok {
		t.Fatal("v2 missing from /v1/validators")
	}
	if v2.Attestation.UnattestedBlobs != 2 {
		t.Errorf("unattested_blobs = %d, want 2: v2 is assigned both blobs and proven to hold neither",
			v2.Attestation.UnattestedBlobs)
	}
	if v2.Attestation.Unattested != 8 {
		t.Errorf("unattested_probes = %d, want 8: two obligations at four schedule points",
			v2.Attestation.Unattested)
	}
	if v2.HeldOut["UNATTESTED"] != 8 {
		t.Errorf("serve_rate_held_out.UNATTESTED = %d, want 8: it is a probe count and stays one",
			v2.HeldOut["UNATTESTED"])
	}
	if v2.Attestation.BlobCoverage.Num != 0 || v2.Attestation.BlobCoverage.Den != 2 {
		t.Errorf("blob_coverage = %d/%d, want 0/2", v2.Attestation.BlobCoverage.Num, v2.Attestation.BlobCoverage.Den)
	}

	v1 := vals["v1"]
	if v1.Attestation.AttestedBlobs != 2 || v1.Attestation.UnattestedBlobs != 0 {
		t.Errorf("v1 attestation by blob = %d attested / %d unattested, want 2/0",
			v1.Attestation.AttestedBlobs, v1.Attestation.UnattestedBlobs)
	}
	if v1.Attestation.Attested != 8 {
		t.Errorf("v1 attested_probes = %d, want 8", v1.Attestation.Attested)
	}
}

// Reachability is sampled for every registered validator on a fixed heartbeat,
// so it says whether the service was up over the window even for a validator
// the publisher never collected a signature from. That is the one stability
// figure whose coverage does not depend on attestation.
func TestReachabilityHistoryIsPublishedPerValidator(t *testing.T) {
	ts := stabilityFixture(t)
	vals := stabilityValidators(t, ts)

	v1 := vals["v1"]
	if v1.Uptime.Num != 10 || v1.Uptime.Den != 10 {
		t.Errorf("v1 reachability_window = %d/%d, want 10/10", v1.Uptime.Num, v1.Uptime.Den)
	}
	if v1.LastDown != nil {
		t.Errorf("v1 last_unreachable_at = %v, want null: it never failed a heartbeat", *v1.LastDown)
	}

	v2 := vals["v2"]
	if v2.Uptime.Num != 7 || v2.Uptime.Den != 10 {
		t.Errorf("v2 reachability_window = %d/%d, want 7/10", v2.Uptime.Num, v2.Uptime.Den)
	}
	if v2.LastDown == nil {
		t.Error("v2 last_unreachable_at is null, but three of its heartbeats did not complete TLS")
	}
	// Identity validity is measured over the heartbeats that saw a
	// certificate. v2 presented one on seven, and two of those were no longer
	// endorsed, so it is 5/7 — not 5/10, which would report one outage twice.
	if v2.IdentityValid.Num != 5 || v2.IdentityValid.Den != 7 {
		t.Errorf("v2 identity_rate_window = %d/%d, want 5/7: an endpoint that was down presented no certificate to judge",
			v2.IdentityValid.Num, v2.IdentityValid.Den)
	}

	// Network-wide, the same two questions have different answers: one
	// endpoint is down right now, and 17 of 20 heartbeats completed over the
	// window. A census of the present cannot say how the week went.
	var net struct {
		Window rateJSON `json:"reachability_window"`
		Now    rateJSON `json:"reachability"`
	}
	if code := get(t, ts, "/v1/network?window=all", &net); code != 200 {
		t.Fatalf("network: %d", code)
	}
	if net.Window.Num != 17 || net.Window.Den != 20 {
		t.Errorf("network reachability_window = %d/%d, want 17/20", net.Window.Num, net.Window.Den)
	}
	if net.Now.Num != 1 || net.Now.Den != 2 {
		t.Errorf("network reachability = %d/%d, want 1/2: v2 is unreachable as of its newest evidence",
			net.Now.Num, net.Now.Den)
	}
}

// A validator that serves early and not late has pruned before it was allowed
// to. The pooled rate cannot tell that apart from one that is uniformly poor,
// and pruning early is the failure this observer exists to catch, so the
// breakdown is published per validator and not only network-wide.
func TestRetentionProfileIsPublishedPerValidator(t *testing.T) {
	vals := stabilityValidators(t, stabilityFixture(t))
	v1 := vals["v1"]
	if len(v1.ByPoint) != 4 {
		t.Fatalf("v1 serve_rate_by_point has %d points, want 4: %+v", len(v1.ByPoint), v1.ByPoint)
	}
	want := map[string][2]int64{
		"w1": {2, 2}, "w2": {2, 2}, "w3": {1, 2}, "w4": {1, 2},
	}
	for _, p := range v1.ByPoint {
		w, ok := want[p.Key]
		if !ok {
			t.Errorf("unexpected point %q", p.Key)
			continue
		}
		if p.Rate.Num != w[0] || p.Rate.Den != w[1] {
			t.Errorf("v1 %s = %d/%d, want %d/%d", p.Key, p.Rate.Num, p.Rate.Den, w[0], w[1])
		}
	}

	// v2 is unattested throughout, so it has no rated probe at any point and
	// its profile must be empty rather than a row of zeroes: a validator never
	// proven to owe anything cannot have failed to retain it.
	for _, p := range vals["v2"].ByPoint {
		if p.Rate.Den != 0 {
			t.Errorf("v2 %s = %d/%d, want an empty rate", p.Key, p.Rate.Num, p.Rate.Den)
		}
	}
}
