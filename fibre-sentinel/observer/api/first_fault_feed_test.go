package api_test

import (
	"fmt"
	"io"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/plsgiveup/fibre/fibre-sentinel/internal/probe"
	"github.com/plsgiveup/fibre/fibre-sentinel/observer/api"
	"github.com/plsgiveup/fibre/fibre-sentinel/observer/store"
)

// The network feed's first FAULT of a validator is its first genuine one:
// not hidden because an earlier FAULT at a suspect point fell before the
// feed's span, not lost behind twenty suspect ones, not a fault still
// settling, and the same probe on a tie however the rows were written.
func TestNetworkFeedFirstFault(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "observer.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	now := time.Now().UTC().Truncate(time.Second)
	addr := func(i int) string { return fmt.Sprintf("%040x", 0xa0+i) }
	// others fault with v at at, making the point suspect
	incident := func(at time.Time, hash string, v string) {
		insertProbe(t, st, hash, v, at, probe.OutcomeNotFound)
		for i := 10; i < 13; i++ {
			insertProbe(t, st, hash, addr(i), at, probe.OutcomeNotFound)
		}
	}

	// v1: a suspect FAULT 40 days ago, before the span; its first genuine
	// one five days ago.
	v1 := addr(1)
	incident(now.Add(-40*24*time.Hour), "old", v1)
	v1First := now.Add(-5 * 24 * time.Hour)
	insertProbe(t, st, "g1", v1, v1First, probe.OutcomeNotFound)
	// v2: 25 FAULTs at suspect points, then its first genuine one.
	v2 := addr(2)
	for i := 0; i < 25; i++ {
		incident(now.Add(-10*24*time.Hour+time.Duration(i)*time.Hour), fmt.Sprintf("s%02d", i), v2)
	}
	v2First := now.Add(-2 * 24 * time.Hour)
	insertProbe(t, st, "g2", v2, v2First, probe.OutcomeNotFound)
	// v3: only a FAULT still settling.
	v3 := addr(3)
	insertProbe(t, st, "g3", v3, now.Add(-5*time.Minute), probe.OutcomeNotFound)
	// v4: two FAULTs at the same moment, the higher hash written first.
	v4 := addr(4)
	v4First := now.Add(-3 * 24 * time.Hour)
	insertProbe(t, st, "zz", v4, v4First, probe.OutcomeNotFound)
	insertProbe(t, st, "aa", v4, v4First, probe.OutcomeNotFound)

	ts := httptest.NewServer(api.New(st, "test"))
	defer ts.Close()
	resp, d := fetchAtom(t, ts, "/v1/feed.atom", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	got := map[string]string{}
	for _, e := range d.Entries {
		if e.Category.Term != "first-fault" {
			continue
		}
		for _, v := range []string{v1, v2, v3, v4, addr(10)} {
			if strings.Contains(e.ID, v) {
				got[v] = e.Updated
			}
		}
	}
	want := map[string]string{
		v1: v1First.Format(time.RFC3339),
		v2: v2First.Format(time.RFC3339),
		v4: v4First.Format(time.RFC3339),
	}
	for v, at := range want {
		if got[v] != at {
			t.Errorf("%s: first fault at %q, want %q", v, got[v], at)
		}
	}
	if _, ok := got[v3]; ok {
		t.Errorf("a fault still settling was published as the first: %v", got)
	}
	if _, ok := got[addr(10)]; ok {
		t.Errorf("a validator whose every fault is at a suspect point has a first fault: %v", got)
	}

	// The tie goes to the lower hash, in the validator's own feed too (a
	// heartbeat puts the validator on record for it).
	at := store.TS(now.Add(-time.Hour))
	if _, err := st.DB().Exec(`INSERT INTO reachability (dedupe_key, vantage, validator_address, validator_host, height, scheduled_at, started_at,
		dns_ok, tcp_ok, tcp_ms, tls_ok, tls_ms, identity_ok, identity_reason, outcome, raw_error, total_duration_ms, raw_json)
		VALUES ('k', 'test', ?, 'h:7980', 1, ?, ?, 1, 1, 10, 0, 20, 0, '', 'TLS_HANDSHAKE_FAIL', '', 30, '{}')`, v4, at, at); err != nil {
		t.Fatal(err)
	}
	resp, err = ts.Client().Get(ts.URL + "/v1/validators/" + v4 + "/feed.atom")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "hash=aa") || strings.Contains(string(body), "hash=zz") {
		t.Errorf("tie not broken by hash:\n%s", body)
	}
}
