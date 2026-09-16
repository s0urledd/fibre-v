package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/plsgiveup/fibre/fibre-sentinel/internal/probe"
	"github.com/plsgiveup/fibre/fibre-sentinel/internal/scan"
	"github.com/plsgiveup/fibre/fibre-sentinel/observer/api"
	"github.com/plsgiveup/fibre/fibre-sentinel/observer/ingest"
	"github.com/plsgiveup/fibre/fibre-sentinel/observer/store"
)

const sampleDir = "../testdata"

func serverWithSample(t *testing.T) *httptest.Server {
	t.Helper()
	ts, _ := serverAndStore(t)
	return ts
}

func serverAndStore(t *testing.T) (*httptest.Server, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "observer.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	now := time.Now()
	if _, err := ingest.Publications(st, filepath.Join(sampleDir, "publications.jsonl"), now); err != nil {
		t.Fatal(err)
	}
	if _, err := ingest.Measurements(st, filepath.Join(sampleDir, "measurements.jsonl"), now); err != nil {
		t.Fatal(err)
	}
	if err := ingest.State(st, filepath.Join(sampleDir, "state.json"), now); err != nil {
		t.Fatal(err)
	}
	if _, err := st.StartRun("collector", "test", "t", now); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(api.New(st, "test"))
	t.Cleanup(ts.Close)
	return ts, st
}

func get(t *testing.T, ts *httptest.Server, path string, into any) int {
	t.Helper()
	resp, err := http.Get(ts.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if into != nil && resp.StatusCode == 200 {
		if err := json.NewDecoder(resp.Body).Decode(into); err != nil {
			t.Fatalf("%s: decode: %v", path, err)
		}
	}
	return resp.StatusCode
}

func TestMetaAndNetwork(t *testing.T) {
	ts := serverWithSample(t)
	var meta struct {
		ChainID   string                 `json:"chain_id"`
		Counts    struct{ Probes int64 } `json:"counts"`
		Collector *struct{ Alive bool }  `json:"collector"`
		OneLoc    bool                   `json:"observed_from_one_location"`
	}
	if code := get(t, ts, "/v1/meta", &meta); code != 200 {
		t.Fatalf("meta: %d", code)
	}
	if meta.ChainID != "fibre-devnet" || meta.Counts.Probes != 60 || meta.Collector == nil || !meta.Collector.Alive || !meta.OneLoc {
		t.Fatalf("meta: %+v", meta)
	}
	var net struct {
		ServeRate       struct{ Num, Den int64 } `json:"serve_rate"`
		Classes         map[string]int64         `json:"classes"`
		ProbeCount      int64                    `json:"probe_count"`
		Coverage        struct{ Num, Den int64 } `json:"serve_rate_coverage"`
		HeldOut         map[string]int64         `json:"serve_rate_held_out"`
		Reconstructable struct {
			Rate                 struct{ Num, Den int64 } `json:"rate"`
			Recoverable          struct{ Num, Den int64 } `json:"recoverable"`
			Yes, Degraded, No    int64
			PublicationsInWindow int64 `json:"publications_in_window"`
			Examined             int64 `json:"publications_examined"`
			SampleLimit          int   `json:"sample_limit"`
		} `json:"reconstructable"`
	}
	if code := get(t, ts, "/v1/network?window=all", &net); code != 200 {
		t.Fatalf("network: %d", code)
	}
	// fixture run: in-window assigned probes are 27 HEALTHY and 9 FAULT. The
	// 12 TOLERATED are grace probes, which are outside the rate's population:
	// a grace probe can only ever add HEALTHY, so counting it would reward
	// over-retention rather than measure retention.
	if net.ServeRate.Num != 27 || net.ServeRate.Den != 36 {
		t.Fatalf("serve rate = %+v (classes %v)", net.ServeRate, net.Classes)
	}
	if net.Coverage.Num != 36 || net.Coverage.Den != 36 {
		t.Fatalf("coverage = %+v, want every in-window probe to have produced a verdict", net.Coverage)
	}
	if net.ProbeCount != 60 {
		t.Fatalf("probe count = %d", net.ProbeCount)
	}
	// the sample bound is published, and with a small fixture nothing is cut
	if net.Reconstructable.SampleLimit == 0 || net.Reconstructable.Examined != net.Reconstructable.PublicationsInWindow {
		t.Fatalf("reconstructable coverage not disclosed: %+v", net.Reconstructable)
	}
	if net.Reconstructable.Rate.Den == 0 {
		t.Fatalf("reconstructable has no denominator: %+v", net.Reconstructable)
	}
	// degraded is reported on its own, never folded into the numerator
	if net.Reconstructable.Rate.Num+net.Reconstructable.Degraded+net.Reconstructable.No != net.Reconstructable.Rate.Den {
		t.Fatalf("reconstructable counts do not add up: %+v", net.Reconstructable)
	}
	if net.Reconstructable.Recoverable.Num < net.Reconstructable.Rate.Num {
		t.Fatalf("recoverable must include the fully-served ones: %+v", net.Reconstructable)
	}
	if code := get(t, ts, "/v1/network?window=bogus", nil); code != 400 {
		t.Fatalf("bad window: %d", code)
	}
}

func TestValidatorsAndBlobs(t *testing.T) {
	ts := serverWithSample(t)
	var vals struct {
		Validators []struct {
			Address        string                   `json:"address"`
			ServeRate      struct{ Num, Den int64 } `json:"serve_rate"`
			ProbeCount     int64                    `json:"probe_count"`
			IdentityStatus string                   `json:"identity_status"`
			Reachable      *bool                    `json:"reachable"`
		} `json:"validators"`
	}
	if code := get(t, ts, "/v1/validators?window=all", &vals); code != 200 {
		t.Fatalf("validators: %d", code)
	}
	if len(vals.Validators) != 4 {
		t.Fatalf("want 4 validators, got %d", len(vals.Validators))
	}
	var faulted, verified int
	for _, v := range vals.Validators {
		if v.ServeRate.Den == 0 {
			t.Fatalf("validator %s has no denominator", v.Address)
		}
		if v.ServeRate.Num < v.ServeRate.Den {
			faulted++
		}
		if v.IdentityStatus == "verified" {
			verified++
		}
	}
	if faulted != 1 {
		t.Fatalf("want exactly the killed validator below 100%%, got %d", faulted)
	}
	if verified < 3 {
		t.Fatalf("want at least 3 verified identities, got %d", verified)
	}
	var one struct {
		Validator struct{ Address string } `json:"validator"`
		Windows   []struct {
			Count int64 `json:"probe_count"`
		} `json:"windows"`
		Recent []any `json:"recent_probes"`
	}
	if code := get(t, ts, "/v1/validators/"+vals.Validators[0].Address, &one); code != 200 {
		t.Fatalf("validator detail: %d", code)
	}
	if len(one.Windows) != 3 || len(one.Recent) == 0 {
		t.Fatalf("detail: %+v", one)
	}
	if code := get(t, ts, "/v1/validators/zzz", nil); code != 400 {
		t.Fatalf("bad address: %d", code)
	}

	var blobs struct {
		Blobs []struct {
			PromiseHash     string `json:"promise_hash"`
			ProbeCount      int64  `json:"probe_count"`
			Reconstructable struct {
				Status     string
				ServedRows int `json:"served_distinct_rows"`
				NeededRows int `json:"needed_rows"`
			} `json:"reconstructable"`
		} `json:"blobs"`
	}
	if code := get(t, ts, "/v1/blobs", &blobs); code != 200 {
		t.Fatalf("blobs: %d", code)
	}
	if len(blobs.Blobs) != 3 {
		t.Fatalf("want 3 blobs, got %d", len(blobs.Blobs))
	}
	probed := 0
	for _, b := range blobs.Blobs {
		if b.ProbeCount > 0 {
			probed++
			if b.Reconstructable.NeededRows != 4096 {
				t.Fatalf("needed rows = %d", b.Reconstructable.NeededRows)
			}
			// three of four validators served at the last complete in-window point; 3 × ~3000
			// distinct rows > 4096 needed, but not everyone answered.
			if b.Reconstructable.Status != "degraded" || b.Reconstructable.ServedRows < 4096 {
				t.Fatalf("blob %s reconstructable = %+v", b.PromiseHash, b.Reconstructable)
			}
		}
	}
	if probed != 3 {
		t.Fatalf("want 3 probed blobs, got %d", probed)
	}
	var detail struct {
		Assignments []any `json:"assignments"`
		Probes      []any `json:"probes"`
	}
	if code := get(t, ts, "/v1/blobs/"+blobs.Blobs[0].PromiseHash, &detail); code != 200 {
		t.Fatalf("blob detail: %d", code)
	}
	if len(detail.Assignments) != 4 {
		t.Fatalf("assignments: %d", len(detail.Assignments))
	}
	if code := get(t, ts, "/v1/blobs/deadbeef", nil); code != 404 {
		t.Fatalf("missing blob: %d", code)
	}
	var probes struct{ Probes []any }
	if code := get(t, ts, "/v1/probes?class=fault&limit=5", &probes); code != 200 || len(probes.Probes) != 5 {
		t.Fatalf("probes: %d, %d rows", code, len(probes.Probes))
	}
}

type reconResp struct {
	Status           string `json:"status"`
	Point            string `json:"point"`
	ServedBy         int    `json:"served_by_validators"`
	Assigned         int    `json:"assigned_validators"`
	ProbedValidators int    `json:"probed_validators"`
}

type blobResp struct {
	PromiseHash     string     `json:"promise_hash"`
	Reconstructable *reconResp `json:"reconstructable"`
}

func sampleMeasurements(t *testing.T) []probe.Measurement {
	t.Helper()
	ms, err := probe.LoadMeasurements(filepath.Join(sampleDir, "measurements.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	return ms
}

func insert(t *testing.T, st *store.Store, m probe.Measurement) {
	t.Helper()
	raw, _ := json.Marshal(m)
	if _, err := st.InsertProbe(m, raw); err != nil {
		t.Fatal(err)
	}
}

// A newer in-window point with a row for only one validator must not flip
// the blob's verdict: the verdict stays at the last complete point, and a
// point where every validator was skipped by the policy is not "no".
func TestReconstructableIgnoresIncompletePoint(t *testing.T) {
	ts, st := serverAndStore(t)
	var before struct{ Blobs []blobResp }
	if code := get(t, ts, "/v1/blobs", &before); code != 200 || len(before.Blobs) == 0 {
		t.Fatalf("blobs: %d / %d", code, len(before.Blobs))
	}
	var target blobResp
	for _, b := range before.Blobs {
		if b.Reconstructable != nil && (b.Reconstructable.Status == "yes" || b.Reconstructable.Status == "degraded") {
			target = b
			break
		}
	}
	if target.PromiseHash == "" {
		t.Fatalf("fixture has no reconstructable blob: %+v", before.Blobs)
	}
	var seed probe.Measurement
	for _, m := range sampleMeasurements(t) {
		if m.PromiseHash == target.PromiseHash && m.Phase == probe.PhaseInWindow && m.Outcome == probe.OutcomeServedOK {
			seed = m
			break
		}
	}
	if seed.PromiseHash == "" {
		t.Fatal("no HEALTHY in-window row for the target blob")
	}
	// one validator's row at a newer in-window point (sweep in progress)
	late := seed
	late.ScheduledAt = seed.ScheduledAt.Add(time.Hour)
	late.StartedAt = late.ScheduledAt
	late.ScheduleLabel = "w9"
	insert(t, st, late)

	var after struct{ Blobs []blobResp }
	get(t, ts, "/v1/blobs", &after)
	for _, b := range after.Blobs {
		if b.PromiseHash != target.PromiseHash {
			continue
		}
		r := b.Reconstructable
		if r.Status != target.Reconstructable.Status || r.Point != target.Reconstructable.Point {
			t.Fatalf("incomplete point changed the verdict: before %+v after %+v", target.Reconstructable, r)
		}
	}

	// a point where every validator was backoff-skipped: NOT_PROBED rows only
	for _, m := range sampleMeasurements(t) {
		if m.PromiseHash != target.PromiseHash || m.Phase != probe.PhaseInWindow || !m.ScheduledAt.Equal(seed.ScheduledAt) {
			continue
		}
		sk := m
		sk.ScheduledAt = seed.ScheduledAt.Add(2 * time.Hour)
		sk.StartedAt = sk.ScheduledAt
		sk.ScheduleLabel = "w10"
		sk.Outcome = probe.OutcomeReachable
		sk.Classification = probe.ClassNotProbed
		sk.Download = probe.DownloadResult{}
		insert(t, st, sk)
	}
	get(t, ts, "/v1/blobs", &after)
	for _, b := range after.Blobs {
		if b.PromiseHash == target.PromiseHash && b.Reconstructable.Status == "no" {
			t.Fatalf("all-skipped point read as not reconstructable: %+v", b.Reconstructable)
		}
	}

	// a blob whose only in-window point is half done is "pending", not "no"
	var netAll struct {
		Reconstructable struct {
			Rate    struct{ Num, Den int64 } `json:"rate"`
			Pending int64                    `json:"pending"`
		} `json:"reconstructable"`
	}
	get(t, ts, "/v1/network?window=all", &netAll)
	if netAll.Reconstructable.Rate.Den == 0 {
		t.Fatal("network reconstructable lost its denominator")
	}
}

// Rows from a second vantage never make served_by exceed assigned.
func TestTwoVantagesDoNotDoubleCountReconstructability(t *testing.T) {
	ts, st := serverAndStore(t)
	for _, m := range sampleMeasurements(t) {
		m.Vantage = "b"
		insert(t, st, m)
	}
	var blobs struct{ Blobs []blobResp }
	get(t, ts, "/v1/blobs", &blobs)
	for _, b := range blobs.Blobs {
		if r := b.Reconstructable; r != nil && r.ServedBy > r.Assigned {
			t.Fatalf("served_by %d > assigned %d for %s", r.ServedBy, r.Assigned, b.PromiseHash)
		}
	}
	var meta struct {
		VantageCount int  `json:"vantage_count"`
		OneLoc       bool `json:"observed_from_one_location"`
	}
	get(t, ts, "/v1/meta", &meta)
	if meta.VantageCount != 2 || meta.OneLoc {
		t.Fatalf("meta vantages: %+v", meta)
	}
}

func TestLimitsAndMethods(t *testing.T) {
	ts := serverWithSample(t)
	for _, q := range []string{"limit=0", "limit=-1", "limit=abc", "limit=5000"} {
		if code := get(t, ts, "/v1/blobs?"+q, nil); code != 400 {
			t.Errorf("/v1/blobs?%s -> %d, want 400", q, code)
		}
		if code := get(t, ts, "/v1/probes?"+q, nil); code != 400 {
			t.Errorf("/v1/probes?%s -> %d, want 400", q, code)
		}
	}
	if code := get(t, ts, "/v1/blobs?limit=2", nil); code != 200 {
		t.Errorf("valid limit -> %d", code)
	}
	resp, err := http.Post(ts.URL+"/v1/meta", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 405 {
		t.Errorf("POST /v1/meta -> %d, want 405", resp.StatusCode)
	}
	var meta struct {
		LastProbeAt *string `json:"last_probe_at"`
	}
	get(t, ts, "/v1/meta", &meta)
	if meta.LastProbeAt == nil || *meta.LastProbeAt == "" {
		t.Error("last_probe_at missing")
	}
}

// Errors never carry the internal detail, and only successes are cacheable.
func TestErrorsAreOpaqueAndUncached(t *testing.T) {
	ts := serverWithSample(t)
	resp, err := http.Get(ts.URL + "/v1/blobs?limit=0")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if cc := resp.Header.Get("Cache-Control"); cc != "no-store" {
		t.Errorf("400 Cache-Control = %q, want no-store", cc)
	}
	ok, err := http.Get(ts.URL + "/v1/meta")
	if err != nil {
		t.Fatal(err)
	}
	ok.Body.Close()
	if cc := ok.Header.Get("Cache-Control"); cc == "no-store" || cc == "" {
		t.Errorf("200 Cache-Control = %q, want a cacheable value", cc)
	}
	// unknown path answers JSON, not text/plain
	nf, err := http.Get(ts.URL + "/v1/nope")
	if err != nil {
		t.Fatal(err)
	}
	defer nf.Body.Close()
	if nf.StatusCode != 404 {
		t.Fatalf("unknown path -> %d", nf.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(nf.Body).Decode(&body); err != nil {
		t.Fatalf("404 body is not JSON: %v", err)
	}
	if body["error"] == "" {
		t.Errorf("404 body = %v", body)
	}
	if ct := nf.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("404 Content-Type = %q", ct)
	}
	// a handler's own 404 keeps its specific message
	hr, err := http.Get(ts.URL + "/v1/validators/" + strings.Repeat("ab", 20))
	if err != nil {
		t.Fatal(err)
	}
	defer hr.Body.Close()
	if hr.StatusCode != 404 {
		t.Fatalf("unknown validator -> %d", hr.StatusCode)
	}
	var hb map[string]any
	if err := json.NewDecoder(hr.Body).Decode(&hb); err != nil {
		t.Fatalf("handler 404 body: %v", err)
	}
	if msg, _ := hb["error"].(string); !strings.Contains(msg, "validator") {
		t.Errorf("handler 404 lost its message: %v", hb)
	}
}

// An operator or account address must not be accepted as a consensus address.
func TestValidatorAddressRequiresConsensusPrefix(t *testing.T) {
	ts := serverWithSample(t)
	for _, addr := range []string{
		"celestiavaloper1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqfk0xdj",
		"celestia1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq",
	} {
		if code := get(t, ts, "/v1/validators/"+addr, nil); code != 400 {
			t.Errorf("%s -> %d, want 400", addr, code)
		}
	}
	if code := get(t, ts, "/v1/probes?validator=celestiavaloper1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqfk0xdj", nil); code != 400 {
		t.Errorf("probes with an operator address -> %d, want 400", code)
	}
}

// Pagination must not drop the rest of a block when the page boundary falls
// inside a height that carries several publications.
func TestBlobsPaginationKeepsSameHeightRows(t *testing.T) {
	ts, st := serverAndStore(t)
	var pubs struct {
		Blobs []struct {
			PromiseHash      string `json:"promise_hash"`
			SettlementHeight int64  `json:"settlement_height"`
		} `json:"blobs"`
	}
	get(t, ts, "/v1/blobs", &pubs)
	if len(pubs.Blobs) == 0 {
		t.Fatal("fixture has no publications")
	}
	// add a second publication at the same height as the newest one
	raw, err := os.ReadFile(filepath.Join(sampleDir, "publications.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var first scan.Publication
	if err := json.Unmarshal([]byte(strings.SplitN(string(raw), "\n", 2)[0]), &first); err != nil {
		t.Fatal(err)
	}
	twin := first
	twin.PromiseHash = "dead" + first.PromiseHash[4:]
	twin.SettlementTxHash = "beef" + first.SettlementTxHash[4:]
	twin.SettlementTxIndex = first.SettlementTxIndex + 1
	tw, _ := json.Marshal(twin)
	if _, err := st.UpsertPublication(twin, tw); err != nil {
		t.Fatal(err)
	}

	var page1 struct {
		Blobs []struct {
			PromiseHash       string `json:"promise_hash"`
			SettlementHeight  int64  `json:"settlement_height"`
			SettlementTxIndex int    `json:"settlement_tx_index"`
		} `json:"blobs"`
	}
	get(t, ts, "/v1/blobs?limit=1", &page1)
	if len(page1.Blobs) != 1 {
		t.Fatalf("page 1: %d rows", len(page1.Blobs))
	}
	cur := page1.Blobs[0]
	var page2 struct {
		Blobs []struct {
			PromiseHash string `json:"promise_hash"`
		} `json:"blobs"`
	}
	get(t, ts, fmt.Sprintf("/v1/blobs?limit=5&before_height=%d&before_tx_index=%d", cur.SettlementHeight, cur.SettlementTxIndex), &page2)
	for _, b := range page2.Blobs {
		if b.PromiseHash == cur.PromiseHash {
			t.Fatal("the cursor row appeared again on page 2")
		}
	}
	if len(page2.Blobs) == 0 {
		t.Fatal("page 2 is empty; the rest of the block was dropped")
	}
	// bad cursor values are rejected, not ignored
	if code := get(t, ts, "/v1/blobs?before_height=abc", nil); code != 400 {
		t.Error("before_height=abc should be 400")
	}
	if code := get(t, ts, "/v1/blobs?before_height=10&before_tx_index=-1", nil); code != 400 {
		t.Error("negative before_tx_index should be 400")
	}
}
