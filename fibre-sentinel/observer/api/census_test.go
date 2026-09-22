package api_test

import (
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/cosmos/cosmos-sdk/types/bech32"

	"github.com/plsgiveup/fibre/fibre-sentinel/internal/probe"
	"github.com/plsgiveup/fibre/fibre-sentinel/observer/api"
	"github.com/plsgiveup/fibre/fibre-sentinel/observer/store"
)

// The "answering now" census is taken over the validators with an open Fibre
// endpoint. The endpoints table keys them by the bech32 consensus address
// the chain prints (celestiavalcons1…), while every heartbeat and probe row
// carries the 20-byte address in hex. The census compared the two forms as
// strings and matched nothing, so /v1/network reported reachability as 0 of
// 0 while the validator rows, resolved through the same identity, said
// every endpoint answered.
func TestReachabilityCensusMatchesBech32Registry(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "observer.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	now := time.Now().UTC().Truncate(time.Second)

	raw := make([]byte, 20)
	for i := range raw {
		raw[i] = byte(i + 1)
	}
	hexAddr := hex.EncodeToString(raw)
	cons, err := bech32.ConvertAndEncode("celestiavalcons", raw)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB().Exec(`INSERT INTO endpoints
		(validator_cons_address, host, first_seen_at, first_seen_height, last_seen_at, last_seen_height)
		VALUES (?, ?, ?, ?, ?, ?)`, cons, "fibre.example.net:7980", store.TS(now.Add(-2*time.Hour)), 99, store.TS(now), 99); err != nil {
		t.Fatal(err)
	}
	m := probe.Measurement{
		SchemaVersion: probe.AttestationSchemaVersion, Vantage: "test",
		ValidatorAddress: hexAddr, ValidatorHost: "fibre.example.net:7980", ValidatorSetHeight: 99,
		ScheduledAt: now.Add(-time.Minute), StartedAt: now.Add(-time.Minute), FinishedAt: now.Add(-time.Minute),
		Outcome: probe.OutcomeReachable,
	}
	m.DNS.OK, m.TCP.OK, m.TLS.OK, m.Identity.OK = true, true, true, true
	rawM, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.InsertReachability(m, rawM); err != nil {
		t.Fatal(err)
	}
	if _, err := st.StartRun("collector", "test", "t", now); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(api.New(st, "test"))
	t.Cleanup(ts.Close)

	res, err := http.Get(ts.URL + "/v1/network?window=24h")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var out struct {
		Reachability struct{ Num, Den int64 } `json:"reachability"`
		Registered   int64                    `json:"registered_endpoints"`
	}
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.Registered != 1 {
		t.Fatalf("registered_endpoints = %d, want 1", out.Registered)
	}
	if out.Reachability.Num != 1 || out.Reachability.Den != 1 {
		t.Fatalf("reachability census = %d / %d, want 1 / 1: the registry's bech32 key did not match the heartbeat's hex address", out.Reachability.Num, out.Reachability.Den)
	}
}
