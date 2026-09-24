package api_test

import (
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/plsgiveup/fibre/fibre-sentinel/observer/api"
	"github.com/plsgiveup/fibre/fibre-sentinel/observer/store"
)

// An operator whose host fails the handshake sees which stage failed and
// the error it failed with, on their own validator page.
func TestValidatorPageCarriesTheLastEndpointCheck(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "observer.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	addr := "00112233445566778899aabbccddeeff00112233"
	ins := `INSERT INTO reachability (dedupe_key, vantage, validator_address, validator_host, height, scheduled_at, started_at,
		dns_ok, tcp_ok, tcp_ms, tls_ok, tls_ms, identity_ok, identity_reason, outcome, raw_error, total_duration_ms, raw_json)
		VALUES (?, 'test', ?, 'fibre.example.org:7980', 1, ?, ?, 1, 1, 12, ?, 30, 0, ?, ?, ?, 50, '{}')`
	older := store.TS(time.Now().Add(-10 * time.Minute))
	newer := store.TS(time.Now().Add(-time.Minute))
	if _, err := st.DB().Exec(ins, "a", addr, older, older, 1, "", "OK", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB().Exec(ins, "b", addr, newer, newer, 0, "handshake", "UNREACHABLE", "remote error: tls: handshake failure"); err != nil {
		t.Fatal(err)
	}
	srv := api.NewWithVantage(st, api.VantageInfo{Name: "test"}, nil, api.WithDataDir(dir))
	ts := httptest.NewServer(srv)
	defer func() { ts.Close(); srv.Close() }()
	var body struct {
		Check *struct {
			Host     string `json:"host"`
			Outcome  string `json:"outcome"`
			TCPOK    bool   `json:"tcp_ok"`
			TLSOK    bool   `json:"tls_ok"`
			RawError string `json:"raw_error"`
		} `json:"last_endpoint_check"`
	}
	if code := getAny(t, ts, "/v1/validators/"+addr, &body); code != 200 {
		t.Fatalf("validator: %d", code)
	}
	c := body.Check
	if c == nil || c.Outcome != "UNREACHABLE" || !c.TCPOK || c.TLSOK || c.RawError != "remote error: tls: handshake failure" || c.Host != "fibre.example.org:7980" {
		t.Fatalf("last check %+v, want the newer, failing one", c)
	}
}
