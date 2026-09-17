package api_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/plsgiveup/fibre/fibre-sentinel/internal/scan"
	"github.com/plsgiveup/fibre/fibre-sentinel/observer/api"
	"github.com/plsgiveup/fibre/fibre-sentinel/observer/ingest"
	"github.com/plsgiveup/fibre/fibre-sentinel/observer/store"
	"net/http/httptest"
)

// The sample store's publications: the first one is signed by this account
// and settled in this tx. The payments written here reuse them so the blob
// page can join charge to publication.
const (
	samplePublisher = "celestia1d3mmg652pxj776dyqwlsrc93y64088g6ux8deq"
	otherPublisher  = "celestia1zyg3zyg3zyg3zyg3zyg3zyg3zyg3zyg3shxjgz"
	timeoutOperator = "celestia1yg3zyg3zyg3zyg3zyg3zyg3zyg3zyg3zl2r5q4" // same bytes as timeoutValoper
	timeoutValoper  = "celestiavaloper1yg3zyg3zyg3zyg3zyg3zyg3zyg3zyg3z64pdkn"
	// The first validator of the sample assignment, given an identity whose
	// operator address has timeoutOperator's bytes.
	sampleValidator = "7730f065f885965a04e6c6f41ef9a29d547f1c99"
)

func writePayments(t *testing.T, dir string, ps []scan.Payment) string {
	t.Helper()
	path := filepath.Join(dir, "payments.jsonl")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for _, p := range ps {
		if err := enc.Encode(p); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func marketServer(t *testing.T, labels map[string]api.PublisherLabel) (*httptest.Server, *store.Store) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "observer.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	now := time.Now()
	if _, err := ingest.Publications(st, filepath.Join(sampleDir, "publications.jsonl"), now); err != nil {
		t.Fatal(err)
	}
	var pubs []scan.Publication
	pubs, err = scan.LoadPublications(filepath.Join(sampleDir, "publications.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	first := pubs[0]
	settle := now.Add(-2 * time.Hour)
	fee := uint64(650_000 + 45_000*1) // 256 KiB → one chunk
	ps := []scan.Payment{
		{SchemaVersion: 1, DedupeKey: "d1", Kind: "deposit", Height: 10, Time: settle.Add(-time.Hour), TxHash: "aa", Publisher: samplePublisher, Denom: "utia", AmountUtia: 6_000_000_000},
		{SchemaVersion: 1, DedupeKey: first.SettlementTxHash + ":0", Kind: "settlement", Height: first.SettlementHeight, Time: settle, TxHash: first.SettlementTxHash,
			Publisher: samplePublisher, Processor: samplePublisher, PromiseHash: first.PromiseHash, BlobSize: 262144, GasUnits: fee, Denom: "utia", AmountUtia: fee},
		{SchemaVersion: 1, DedupeKey: "t1", Kind: "timeout", Height: 12, Time: settle.Add(time.Minute), TxHash: "bb",
			Publisher: otherPublisher, Processor: timeoutOperator, PromiseHash: "deadbeef", BlobSize: 1 << 20, GasUnits: 830_000, Denom: "utia", AmountUtia: 830_000},
		{SchemaVersion: 1, DedupeKey: "s2", Kind: "settlement", Height: 13, Time: settle.Add(2 * time.Minute), TxHash: "cc",
			Publisher: otherPublisher, Processor: otherPublisher, PromiseHash: "cafe", BlobSize: 1 << 20, GasUnits: 830_000, Denom: "utia", AmountUtia: 830_000},
		{SchemaVersion: 1, DedupeKey: "w1", Kind: "withdrawal_request", Height: 14, Time: settle.Add(3 * time.Minute), TxHash: "dd", Publisher: samplePublisher, Denom: "utia", AmountUtia: 1000},
		{SchemaVersion: 1, DedupeKey: "h15:executed:0", Kind: "withdrawal_executed", Height: 15, Time: settle.Add(4 * time.Minute), TxIndex: -1, Publisher: samplePublisher, Denom: "utia", AmountUtia: 1000},
		// A payment ten days old: in 30d and all, not in 24h or 7d.
		{SchemaVersion: 1, DedupeKey: "old", Kind: "settlement", Height: 5, Time: now.Add(-10 * 24 * time.Hour), TxHash: "ee",
			Publisher: otherPublisher, Processor: otherPublisher, PromiseHash: "0ld", BlobSize: 65536, GasUnits: 695_000, Denom: "utia", AmountUtia: 695_000},
	}
	if r, err := ingest.Payments(st, writePayments(t, dir, ps), now); err != nil || r.Inserted != int64(len(ps)) {
		t.Fatalf("ingest payments: inserted=%d err=%v", r.Inserted, err)
	}
	if err := st.UpsertEscrowAccount(scan.Escrow{Signer: samplePublisher, Denom: "utia", BalanceUtia: 5_999_304_000, AvailableUtia: 5_999_304_000, Height: 20, Found: true}, now); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertEscrowAccount(scan.Escrow{Signer: otherPublisher, Found: false}, now); err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpsertValidatorIdentities([]scan.ValidatorIdentity{{
		ConsAddressHex: sampleValidator, Moniker: "enforcer",
		OperatorAddress: timeoutValoper,
	}}, now); err != nil {
		t.Fatal(err)
	}
	var opts []api.Option
	if labels != nil {
		opts = append(opts, api.WithPublisherLabels(labels))
	}
	ts := httptest.NewServer(api.NewWithVantage(st, api.VantageInfo{Name: "test"}, nil, opts...))
	t.Cleanup(ts.Close)
	return ts, st
}

func TestMarketSummary(t *testing.T) {
	ts, _ := marketServer(t, map[string]api.PublisherLabel{samplePublisher: {Address: samplePublisher, Label: "Sentinel test publisher", Source: "this observer"}})
	var m struct {
		Settlements     int64                       `json:"settlements"`
		Fees            int64                       `json:"fees_settled_utia"`
		Bytes           int64                       `json:"bytes"`
		Publishers      int64                       `json:"publishers_active"`
		PerMiB          *float64                    `json:"paid_per_mib_utia"`
		Timeouts        int64                       `json:"timeouts"`
		TimedOut        int64                       `json:"timed_out_utia"`
		Rate            struct{ Num, Den int64 }    `json:"settlement_rate"`
		Deposits        struct{ Count, Utia int64 } `json:"deposits"`
		WithdrawalsExec struct{ Count, Utia int64 } `json:"withdrawals_executed"`
		EscrowHeld      int64                       `json:"escrow_held_utia"`
		EscrowAccounts  int64                       `json:"escrow_accounts"`
		Daily           []map[string]any            `json:"daily"`
		Top             []struct {
			Publisher string   `json:"publisher"`
			Label     string   `json:"label"`
			FeesShare *float64 `json:"fees_share"`
		} `json:"top_publishers"`
		Other   *map[string]any             `json:"other_publishers"`
		Largest *struct{ Publisher string } `json:"largest_poster"`
		Formula struct {
			BaseGas     uint64 `json:"base_gas"`
			GasPerChunk uint64 `json:"gas_per_chunk"`
			ChunkBytes  uint64 `json:"chunk_bytes"`
		} `json:"price_formula"`
		Notes      []string `json:"notes"`
		Source     string   `json:"source"`
		ComputedAt string   `json:"computed_at"`
	}
	if code := get(t, ts, "/v1/market?window=24h", &m); code != 200 {
		t.Fatalf("market: %d", code)
	}
	if m.Settlements != 2 || m.Fees != 695_000+830_000 || m.Bytes != 262144+1<<20 || m.Publishers != 2 {
		t.Fatalf("settlements: %+v", m)
	}
	if m.PerMiB == nil || *m.PerMiB < 1_000_000 || *m.PerMiB > 1_300_000 {
		t.Fatalf("paid per MiB: %v", m.PerMiB)
	}
	if m.Timeouts != 1 || m.TimedOut != 830_000 || m.Rate.Num != 2 || m.Rate.Den != 3 {
		t.Fatalf("timeouts: %+v", m)
	}
	if m.Deposits.Count != 1 || m.Deposits.Utia != 6_000_000_000 || m.WithdrawalsExec.Utia != 1000 {
		t.Fatalf("deposits/withdrawals: %+v", m)
	}
	if m.EscrowHeld != 5_999_304_000 || m.EscrowAccounts != 1 {
		t.Fatalf("escrow: held=%d accounts=%d (a not-found account must not count)", m.EscrowHeld, m.EscrowAccounts)
	}
	if len(m.Daily) == 0 {
		t.Fatal("no daily buckets")
	}
	if len(m.Top) != 2 || m.Top[0].Publisher != otherPublisher || m.Top[1].Label != "Sentinel test publisher" {
		t.Fatalf("top: %+v", m.Top)
	}
	if m.Other != nil {
		t.Fatalf("two publishers must not produce an 'other' bucket: %v", m.Other)
	}
	if m.Largest == nil || m.Largest.Publisher != otherPublisher {
		t.Fatalf("largest poster: %+v", m.Largest)
	}
	if m.Formula.BaseGas != 650_000 || m.Formula.GasPerChunk != 45_000 || m.Formula.ChunkBytes != 262144 {
		t.Fatalf("formula: %+v", m.Formula)
	}
	if len(m.Notes) < 4 || m.Source == "" || m.ComputedAt == "" {
		t.Fatalf("honesty fields missing: notes=%d source=%q at=%q", len(m.Notes), m.Source, m.ComputedAt)
	}
	// The old settlement is in 30d, not in 24h.
	var m30 struct{ Settlements int64 }
	get(t, ts, "/v1/market?window=30d", &m30)
	if m30.Settlements != 3 {
		t.Fatalf("30d settlements: %d", m30.Settlements)
	}
	if code := get(t, ts, "/v1/market?window=1y", nil); code != 400 {
		t.Fatalf("bad window: %d", code)
	}
}

func TestPublishersListAndDetail(t *testing.T) {
	ts, _ := marketServer(t, nil)
	var list struct {
		Publishers []struct {
			Publisher   string   `json:"publisher"`
			Settlements int64    `json:"settlements"`
			FeesShare   *float64 `json:"fees_share"`
			Timeouts    int64    `json:"timeouts"`
			FirstSeen   string   `json:"first_seen_at"`
			Escrow      *struct {
				Found   bool  `json:"found"`
				Balance int64 `json:"balance_utia"`
			} `json:"escrow"`
		} `json:"publishers"`
		Count int `json:"count"`
	}
	if code := get(t, ts, "/v1/publishers?window=24h", &list); code != 200 {
		t.Fatalf("publishers: %d", code)
	}
	if list.Count != 2 {
		t.Fatalf("want 2 publishers, got %+v", list)
	}
	if list.Publishers[0].Publisher != otherPublisher || list.Publishers[0].Timeouts != 1 {
		t.Fatalf("ordering by fees, timeouts: %+v", list.Publishers[0])
	}
	if list.Publishers[0].Escrow == nil || list.Publishers[0].Escrow.Found {
		t.Fatalf("a polled but absent escrow must be reported as not found: %+v", list.Publishers[0].Escrow)
	}
	if list.Publishers[1].Escrow == nil || list.Publishers[1].Escrow.Balance != 5_999_304_000 {
		t.Fatalf("escrow: %+v", list.Publishers[1].Escrow)
	}
	if list.Publishers[1].FirstSeen == "" {
		t.Fatal("first_seen_at must be filled from the whole history")
	}

	var one struct {
		Publisher struct {
			Publisher string `json:"publisher"`
			Fees      int64  `json:"fees_utia"`
		} `json:"publisher"`
		Windows []struct {
			Window      struct{ Name string } `json:"window"`
			Settlements int64                 `json:"settlements"`
		} `json:"windows"`
		Payments []struct{ Kind string } `json:"recent_payments"`
		Blobs    []struct {
			PromiseHash string `json:"promise_hash"`
			Charge      *struct {
				Fee     int64 `json:"fee_utia"`
				Settled bool  `json:"settled"`
			} `json:"charge"`
		} `json:"recent_blobs"`
	}
	if code := get(t, ts, "/v1/publishers/"+samplePublisher+"?window=24h", &one); code != 200 {
		t.Fatalf("publisher detail: %d", code)
	}
	if one.Publisher.Fees != 695_000 || len(one.Windows) != 4 || len(one.Payments) != 4 {
		t.Fatalf("detail: %+v", one)
	}
	for _, w := range one.Windows {
		if w.Window.Name == "24h" && w.Settlements != 1 {
			t.Fatalf("24h span: %+v", w)
		}
	}
	if len(one.Blobs) == 0 || one.Blobs[len(one.Blobs)-1].Charge == nil || !one.Blobs[len(one.Blobs)-1].Charge.Settled {
		t.Fatalf("the settled sample blob must carry its charge: %+v", one.Blobs)
	}
	// A publisher with history but nothing in the window still resolves.
	var quiet struct {
		Publisher struct{ Settlements int64 } `json:"publisher"`
	}
	if code := get(t, ts, "/v1/publishers/"+otherPublisher+"?window=24h", &quiet); code != 200 {
		t.Fatalf("quiet publisher: %d", code)
	}
	// A publisher with only a deposit has no settlement in any span: every
	// SUM over an empty CASE is NULL in SQLite and must scan as zero.
	dir := t.TempDir()
	st2, err := store.Open(filepath.Join(dir, "o.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st2.Close()
	if _, err := ingest.Payments(st2, writePayments(t, dir, []scan.Payment{{SchemaVersion: 1, DedupeKey: "d", Kind: "deposit", Height: 1, Time: time.Now(), Publisher: samplePublisher, Denom: "utia", AmountUtia: 5}}), time.Now()); err != nil {
		t.Fatal(err)
	}
	ts2 := httptest.NewServer(api.New(st2, "t"))
	defer ts2.Close()
	if code := get(t, ts2, "/v1/publishers/"+samplePublisher, &quiet); code != 200 {
		t.Fatalf("deposit-only publisher: %d", code)
	}
	if code := get(t, ts, "/v1/publishers/celestia1nobody?window=24h", nil); code != 400 {
		t.Fatalf("malformed address: %d", code)
	}
	if code := get(t, ts, "/v1/publishers/"+timeoutOperator+"?window=24h", nil); code != 404 {
		t.Fatalf("unknown publisher: %d", code)
	}
	if code := get(t, ts, "/v1/publishers/"+timeoutValoper, nil); code != 400 {
		t.Fatalf("valoper prefix: %d", code)
	}
}

func TestBlobChargeAndValidatorTimeouts(t *testing.T) {
	ts, _ := marketServer(t, nil)
	pubs, err := scan.LoadPublications(filepath.Join(sampleDir, "publications.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var blob struct {
		Blob struct {
			Charge *struct {
				Fee      int64  `json:"fee_utia"`
				Gas      int64  `json:"gas_units"`
				Settled  bool   `json:"settled"`
				TimedOut bool   `json:"timed_out"`
				Pub      string `json:"publisher"`
			} `json:"charge"`
		} `json:"blob"`
	}
	if code := get(t, ts, "/v1/blobs/"+pubs[0].PromiseHash, &blob); code != 200 {
		t.Fatalf("blob: %d", code)
	}
	if c := blob.Blob.Charge; c == nil || c.Fee != 695_000 || !c.Settled || c.TimedOut || c.Pub != samplePublisher {
		t.Fatalf("charge: %+v", blob.Blob.Charge)
	}
	if code := get(t, ts, "/v1/blobs/"+pubs[1].PromiseHash, &blob); code != 200 {
		t.Fatalf("blob: %d", code)
	}
	if blob.Blob.Charge != nil {
		t.Fatalf("a publication without a recorded payment must have a null charge, got %+v", blob.Blob.Charge)
	}

	var vals struct {
		Validators []struct {
			Address  string `json:"address"`
			Timeouts int64  `json:"timeouts_enforced"`
		} `json:"validators"`
	}
	if code := get(t, ts, "/v1/validators?window=24h", &vals); code != 200 {
		t.Fatalf("validators: %d", code)
	}
	found := false
	for _, v := range vals.Validators {
		if v.Address == sampleValidator {
			found = true
			if v.Timeouts != 1 {
				t.Fatalf("the operator whose account submitted the timeout must show it: %+v", v)
			}
		} else if v.Timeouts != 0 {
			t.Fatalf("validator %s did not submit a timeout: %+v", v.Address, v)
		}
	}
	if !found {
		t.Fatal("sample validator missing from the table")
	}
}

func TestLoadPublisherLabels(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "publishers.yaml")
	if err := os.WriteFile(path, []byte("publishers:\n  - address: "+samplePublisher+"\n    label: Sentinel\n    source: this observer\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := api.LoadPublisherLabels(path)
	if err != nil || len(m) != 1 || m[samplePublisher].Label != "Sentinel" {
		t.Fatalf("labels: %v %v", m, err)
	}
	if m, err := api.LoadPublisherLabels(filepath.Join(dir, "missing.yaml")); err != nil || len(m) != 0 {
		t.Fatalf("missing file must be an empty registry: %v %v", m, err)
	}
	if err := os.WriteFile(path, []byte("publishers:\n  - address: nope\n    label: x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := api.LoadPublisherLabels(path); err == nil {
		t.Fatal("a non-bech32 address must be rejected")
	}
	if err := os.WriteFile(path, []byte("publishers:\n  - address: "+samplePublisher+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := api.LoadPublisherLabels(path); err == nil {
		t.Fatal("a label-less entry must be rejected")
	}
}
