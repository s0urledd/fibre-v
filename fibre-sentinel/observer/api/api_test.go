package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/plsgiveup/fibre/fibre-sentinel/observer/api"
	"github.com/plsgiveup/fibre/fibre-sentinel/observer/ingest"
	"github.com/plsgiveup/fibre/fibre-sentinel/observer/store"
)

const sampleDir = "../testdata"

func serverWithSample(t *testing.T) *httptest.Server {
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
	return ts
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
		Reconstructable struct{ Num, Den int64 } `json:"reconstructable"`
	}
	if code := get(t, ts, "/v1/network?window=all", &net); code != 200 {
		t.Fatalf("network: %d", code)
	}
	// fixture run: in-window + grace assigned probes: 27 HEALTHY, 9 FAULT, 12 TOLERATED.
	if net.ServeRate.Num != 27 || net.ServeRate.Den != 36 {
		t.Fatalf("serve rate = %+v (classes %v)", net.ServeRate, net.Classes)
	}
	if net.ProbeCount != 60 {
		t.Fatalf("probe count = %d", net.ProbeCount)
	}
	if net.Reconstructable.Den == 0 {
		t.Fatalf("reconstructable has no denominator: %+v", net.Reconstructable)
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
			// three of four validators served the grace point; 3 × ~3000
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
