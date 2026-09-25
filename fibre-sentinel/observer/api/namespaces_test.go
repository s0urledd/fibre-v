package api_test

import (
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/plsgiveup/fibre/fibre-sentinel/observer/api"
	"github.com/plsgiveup/fibre/fibre-sentinel/observer/store"
)

func insertPublication(t *testing.T, st *store.Store, idx int, h int64, at time.Time, ns, signer string, size int64) {
	t.Helper()
	hash := fmt.Sprintf("%064x", 0x5a0000+idx)
	if _, err := st.DB().Exec(`INSERT INTO publications (
		promise_hash, commitment, blob_version, blob_size, namespace, chain_id,
		promise_height, creation_timestamp, signer, signer_public_key,
		validator_signature_count, settlement_height, settlement_time,
		settlement_tx_hash, settlement_tx_index, settlement_tx_code,
		must_serve_until, must_serve_until_basis, shard_retention_s,
		payment_promise_timeout_s, assignment_error, validator_set_height,
		total_voting_power, sigma_rows, distinct_rows, wrap_overlaps,
		validators_with_rows, recorded_at, raw_json
	) VALUES (?,?,0,?,?,'test',?,?,?,'pk',0,?,?,'tx',0,0,?,'shard_retention',14400,3600,
		'',?,0,0,0,0,0,?,'{}')`,
		hash, hash, size, ns, h-1, store.TS(at), signer, h, store.TS(at), store.TS(at.Add(4*time.Hour)), h, store.TS(at)); err != nil {
		t.Fatal(err)
	}
}

type namespacesBody struct {
	Namespaces []struct {
		Namespace string `json:"namespace"`
		Blobs     int64  `json:"blobs"`
		Bytes     int64  `json:"bytes"`
		Blobs24h  int64  `json:"blobs_24h"`
		Bytes24h  int64  `json:"bytes_24h"`
		Accounts  int64  `json:"accounts"`
		FirstSeen string `json:"first_seen"`
		LastBlob  string `json:"last_blob"`
	} `json:"namespaces"`
	Truncated bool `json:"truncated"`
}

// Namespaces are summed per namespace, newest publication first, with the
// last 24 hours counted apart and paying accounts counted once each.
func TestNamespacesSummary(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "observer.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Now().UTC()
	old := now.Add(-48 * time.Hour)
	insertPublication(t, st, 1, 100, old, "aa", "alice", 1000)
	insertPublication(t, st, 2, 200, now.Add(-time.Hour), "aa", "bob", 3000)
	insertPublication(t, st, 3, 210, now.Add(-30*time.Minute), "aa", "alice", 2000)
	insertPublication(t, st, 4, 150, old.Add(time.Hour), "bb", "carol", 500)

	ts := httptest.NewServer(api.NewWithVantage(st, api.VantageInfo{Name: "test"}, nil))
	defer ts.Close()
	var b namespacesBody
	if code := getAny(t, ts, "/v1/namespaces", &b); code != 200 {
		t.Fatalf("code %d", code)
	}
	if len(b.Namespaces) != 2 {
		t.Fatalf("namespaces = %d, want 2: %+v", len(b.Namespaces), b.Namespaces)
	}
	a, o := b.Namespaces[0], b.Namespaces[1]
	if a.Namespace != "aa" || o.Namespace != "bb" {
		t.Fatalf("order = %s, %s; want aa (newest) then bb", a.Namespace, o.Namespace)
	}
	if a.Blobs != 3 || a.Bytes != 6000 || a.Blobs24h != 2 || a.Bytes24h != 5000 || a.Accounts != 2 {
		t.Errorf("aa = %+v, want 3 blobs, 6000 B, 2 / 5000 B in 24h, 2 accounts", a)
	}
	if a.FirstSeen != store.TS(old) || a.LastBlob != store.TS(now.Add(-30*time.Minute)) {
		t.Errorf("aa first/last = %s / %s", a.FirstSeen, a.LastBlob)
	}
	if o.Blobs != 1 || o.Blobs24h != 0 || o.Bytes24h != 0 || o.Accounts != 1 {
		t.Errorf("bb = %+v, want 1 blob, none in 24h, 1 account", o)
	}

	var one namespacesBody
	getAny(t, ts, "/v1/namespaces?limit=1", &one)
	if len(one.Namespaces) != 1 || !one.Truncated {
		t.Errorf("limit=1: %d rows, truncated=%v", len(one.Namespaces), one.Truncated)
	}
}
