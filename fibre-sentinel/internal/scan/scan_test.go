package scan

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	fibretypes "github.com/celestiaorg/celestia-app/v10/x/fibre/types"
	abci "github.com/cometbft/cometbft/abci/types"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	"github.com/cosmos/cosmos-sdk/crypto/keys/ed25519"
	gogojsonpb "github.com/cosmos/gogoproto/jsonpb"
	assign "github.com/plsgiveup/fibre/fibre-assign"
)

func params(timeout, retention, withdrawal time.Duration) fibretypes.Params {
	return fibretypes.Params{
		WithdrawalDelay:            withdrawal,
		PaymentPromiseTimeout:      timeout,
		PaymentPromiseHeightWindow: 1000,
		ShardRetention:             retention,
		FullStakeStorageBudget:     2 << 40,
	}
}

func TestMustServeUntil_MaxFormula(t *testing.T) {
	creation := time.Date(2026, 9, 2, 23, 14, 2, 0, time.UTC)

	// retention dominates
	h := NewParamHistory(10, params(10*time.Minute, 30*time.Minute, 13*time.Hour))
	got, _, basis, ok := h.MustServeUntil(creation, 20, 0)
	if !ok {
		t.Fatal("no entry")
	}
	if want := creation.Add(30 * time.Minute); !got.Equal(want) {
		t.Fatalf("retention-dominated: got %s want %s", got, want)
	}
	if basis == "" {
		t.Fatal("empty basis")
	}

	// timeout dominates
	h = NewParamHistory(10, params(45*time.Minute, 15*time.Minute, 13*time.Hour))
	got, _, _, _ = h.MustServeUntil(creation, 20, 0)
	if want := creation.Add(45 * time.Minute); !got.Equal(want) {
		t.Fatalf("timeout-dominated: got %s want %s", got, want)
	}
}

func TestParamHistory_Ordering(t *testing.T) {
	creation := time.Unix(1_700_000_000, 0).UTC()
	h := NewParamHistory(100, params(10*time.Minute, 10*time.Minute, 13*time.Hour))

	// tx event at h=200 tx=3 raises retention to 1h. Effective for tx index > 3.
	if !h.AddTxEvent(200, 3, params(10*time.Minute, time.Hour, 13*time.Hour)) {
		t.Fatal("AddTxEvent should have changed effective value")
	}
	// finalize event at h=300 raises retention to 2h, effective h>=301.
	if !h.AddFinalizeEvent(300, params(10*time.Minute, 2*time.Hour, 13*time.Hour)) {
		t.Fatal("AddFinalizeEvent should have changed value")
	}

	cases := []struct {
		h    int64
		txi  int
		want time.Duration
	}{
		{150, 0, 10 * time.Minute}, // before any change
		{200, 2, 10 * time.Minute}, // same block, before the update tx
		{200, 3, 10 * time.Minute}, // the update tx itself -> old still
		{200, 4, time.Hour},        // same block, after the update tx
		{250, 0, time.Hour},        // between changes
		{300, 9, time.Hour},        // finalize not yet effective in its own block
		{301, 0, 2 * time.Hour},    // next block
		{5000, 0, 2 * time.Hour},   // long after
	}
	for _, c := range cases {
		got, _, _, ok := h.MustServeUntil(creation, c.h, c.txi)
		if !ok {
			t.Fatalf("h=%d txi=%d: no entry", c.h, c.txi)
		}
		want := creation.Add(c.want)
		if !got.Equal(want) {
			t.Errorf("h=%d txi=%d: must_serve_until %s, want %s (window %s)", c.h, c.txi, got, want, c.want)
		}
	}
}

func TestParamHistory_NoOpRepeatDropped(t *testing.T) {
	h := NewParamHistory(1, params(10*time.Minute, 10*time.Minute, 13*time.Hour))
	if h.AddTxEvent(50, 0, params(10*time.Minute, 10*time.Minute, 13*time.Hour)) {
		t.Fatal("identical params should not create a new entry")
	}
	if n := len(h.Entries()); n != 1 {
		t.Fatalf("entries = %d, want 1", n)
	}
}

func TestParamHistory_ReloadRoundTrip(t *testing.T) {
	creation := time.Unix(1_700_000_000, 0).UTC()
	h := NewParamHistory(100, params(10*time.Minute, 10*time.Minute, 13*time.Hour))
	h.AddTxEvent(200, 3, params(20*time.Minute, time.Hour, 13*time.Hour))
	h.AddFinalizeEvent(300, params(20*time.Minute, 3*time.Hour, 14*time.Hour))

	reloaded := LoadParamHistory(h.Entries())
	for _, c := range []struct {
		h   int64
		txi int
	}{{150, 0}, {200, 4}, {301, 0}} {
		a, _, _, _ := h.MustServeUntil(creation, c.h, c.txi)
		b, _, _, _ := reloaded.MustServeUntil(creation, c.h, c.txi)
		if !a.Equal(b) {
			t.Errorf("h=%d txi=%d: original %s, reloaded %s", c.h, c.txi, a, b)
		}
	}
}

func TestParseUpdateFibreParams(t *testing.T) {
	p := params(600*time.Second, 600*time.Second, 43800*time.Second)
	m := gogojsonpb.Marshaler{}
	js, err := m.MarshalToString(&p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	ev := abci.Event{
		Type: eventUpdateFibreParamsType,
		Attributes: []abci.EventAttribute{
			{Key: "signer", Value: `"celestia1xyz"`},
			{Key: "params", Value: js},
		},
	}
	got, isUpdate, err := parseUpdateFibreParams(ev)
	if err != nil || !isUpdate {
		t.Fatalf("parse: isUpdate=%v err=%v", isUpdate, err)
	}
	if got.PaymentPromiseTimeout != 600*time.Second || got.ShardRetention != 600*time.Second || got.WithdrawalDelay != 43800*time.Second {
		t.Fatalf("round-trip mismatch: %+v", got)
	}

	// a non-matching event type is ignored.
	if _, isUpdate, _ := parseUpdateFibreParams(abci.Event{Type: "celestia.fibre.v1.EventPayForFibre"}); isUpdate {
		t.Fatal("wrong event type reported as update")
	}
}

func TestBuildAssignmentTable(t *testing.T) {
	vals := make([]assign.Validator, 4)
	for i := range vals {
		var a assign.Address
		a[0] = byte(i + 1)
		vals[i] = assign.Validator{Address: a, VotingPower: 1_000_000}
	}
	var commit [32]byte
	commit[0] = 0xAB

	// mark the first two validators attested, so the table's attested counts
	// are exercised alongside the assignment itself
	att := Attestation{Attested: map[string]bool{
		vals[0].Address.String(): true,
		vals[1].Address.String(): true,
	}, Entries: 2, Verified: 2, AttestedPower: 2_000_000, TotalPower: 4_000_000}

	tbl := buildAssignmentTable(commit, 0, 51, vals, true, att)
	if tbl.Error != "" {
		t.Fatalf("unexpected error: %s", tbl.Error)
	}
	if len(tbl.Validators) != 4 {
		t.Fatalf("validators = %d", len(tbl.Validators))
	}
	sum := 0
	for _, v := range tbl.Validators {
		if v.RowCount != len(v.Rows) {
			t.Errorf("row count %d != len(rows) %d", v.RowCount, len(v.Rows))
		}
		sum += v.RowCount
	}
	if sum != tbl.Sigma {
		t.Errorf("sigma %d != sum %d", tbl.Sigma, sum)
	}
	attested := 0
	for _, v := range tbl.Validators {
		if v.Attested {
			attested++
		}
	}
	if attested != 2 || tbl.AttestedWithRows != 2 {
		t.Errorf("attested = %d, AttestedWithRows = %d, want 2 and 2", attested, tbl.AttestedWithRows)
	}
	if tbl.AttestedVotingPower != 2_000_000 || tbl.SignaturesVerified != 2 {
		t.Errorf("attested power %d, verified %d", tbl.AttestedVotingPower, tbl.SignaturesVerified)
	}
	if tbl.ProtocolParams.Fingerprint != assign.ParamsV10BlobV0.Fingerprint() {
		t.Errorf("fingerprint mismatch")
	}

	// unknown blob version -> explicit error, no assignment.
	bad := buildAssignmentTable(commit, 7, 51, vals, true, Attestation{})
	if bad.Error == "" {
		t.Fatal("expected error for blob version 7")
	}
}

func TestStore_DedupeAndResume(t *testing.T) {
	dir := t.TempDir()
	st, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	pub := Publication{PromiseHash: "aa", SettlementTxHash: "deadbeef", SettlementHeight: 42}
	if err := st.AppendPublication(pub); err != nil {
		t.Fatal(err)
	}
	if err := st.AppendPublication(pub); err != nil { // dupe in-run
		t.Fatal(err)
	}
	if err := st.Sync(); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveState(PersistState{ChainID: "c1", LastScannedHeight: 42, ParamHistory: []ParamEntry{{FromHeight: 1, FromTxIndex: -1}}}); err != nil {
		t.Fatal(err)
	}
	st.Close()

	st2, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st2.Close()
	if !st2.Seen("deadbeef") {
		t.Fatal("seen key not reloaded")
	}
	loaded, err := st2.LoadState()
	if err != nil || loaded == nil {
		t.Fatalf("load state: %v", err)
	}
	if loaded.LastScannedHeight != 42 || loaded.ChainID != "c1" {
		t.Fatalf("state mismatch: %+v", loaded)
	}
	// only one line in the jsonl despite two appends.
	if got := countLines(t, filepath.Join(dir, "publications.jsonl")); got != 1 {
		t.Fatalf("jsonl lines = %d, want 1", got)
	}
}

func countLines(t *testing.T, path string) int {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return len(strings.Split(strings.TrimRight(string(b), "\n"), "\n"))
}

func TestDecodePayForFibre_NotFibre(t *testing.T) {
	msg, err := decodePayForFibre([]byte("not a tx at all"))
	if err != nil || msg != nil {
		t.Fatalf("expected (nil,nil), got (%v,%v)", msg, err)
	}
}

func TestIsModuleInactive(t *testing.T) {
	if !IsModuleInactive(errors.New("abci query params h=595770: code=6 log=unknown query path: unknown request")) {
		t.Fatal("pre-v10 error should read as inactive module")
	}
	if IsModuleInactive(errors.New("post failed: connection refused")) || IsModuleInactive(nil) {
		t.Fatal("transport errors and nil are not an inactive module")
	}
}

func TestMustServeUntilForPromise_UploadTimeAmbiguity(t *testing.T) {
	creation := time.Unix(1_700_000_000, 0).UTC()
	h := NewParamHistory(100, params(10*time.Minute, time.Hour, 13*time.Hour))
	// gov change at end of block 300: retention 1h -> 30m, effective from 301.
	if !h.AddFinalizeEvent(300, params(10*time.Minute, 30*time.Minute, 13*time.Hour)) {
		t.Fatal("finalize event should change the value")
	}

	// promise and settlement on the same side of the change: exact, not ambiguous
	msu, _, _, amb, ok := h.MustServeUntilForPromise(creation, 250, 280, 0)
	if !ok || amb || !msu.Equal(creation.Add(time.Hour)) {
		t.Fatalf("same-side: msu=%s amb=%v ok=%v", msu, amb, ok)
	}
	msu, _, _, amb, _ = h.MustServeUntilForPromise(creation, 310, 320, 0)
	if amb || !msu.Equal(creation.Add(30*time.Minute)) {
		t.Fatalf("same-side after change: msu=%s amb=%v", msu, amb)
	}

	// change lands between promise height and settlement: earlier bound, flagged
	msu, snap, basis, amb, _ := h.MustServeUntilForPromise(creation, 290, 320, 0)
	if !amb {
		t.Fatal("expected ambiguous")
	}
	if !msu.Equal(creation.Add(30 * time.Minute)) {
		t.Fatalf("ambiguous: msu=%s want creation+30m", msu)
	}
	if snap.ShardRetentionSeconds != 1800 {
		t.Fatalf("snapshot should be the params that produced the bound, got retention %ds", snap.ShardRetentionSeconds)
	}
	if !strings.Contains(basis, "AMBIGUOUS") {
		t.Fatalf("basis should say AMBIGUOUS: %q", basis)
	}

	// the earlier bound can also come from the PRE-change params
	h2 := NewParamHistory(100, params(10*time.Minute, 30*time.Minute, 13*time.Hour))
	h2.AddFinalizeEvent(300, params(10*time.Minute, time.Hour, 13*time.Hour))
	msu, _, _, amb, _ = h2.MustServeUntilForPromise(creation, 290, 320, 0)
	if !amb || !msu.Equal(creation.Add(30*time.Minute)) {
		t.Fatalf("pre-change earlier: msu=%s amb=%v", msu, amb)
	}

	// Promise height before the scan start, over an interval that still
	// contains a known change: ambiguous, and the earliest bound wins. The
	// upload happened somewhere in [50, 320] and the params changed at 300,
	// so which window the server used is genuinely unknown.
	msu, _, _, amb, ok = h.MustServeUntilForPromise(creation, 50, 320, 0)
	if !ok || !amb || !msu.Equal(creation.Add(30*time.Minute)) {
		t.Fatalf("pre-history promise spanning a change: msu=%s amb=%v ok=%v", msu, amb, ok)
	}

	// Promise height before the scan start with no known change in the
	// interval: the seed params are the only thing we have and there is
	// nothing to be ambiguous about.
	h3 := NewParamHistory(100, params(10*time.Minute, time.Hour, 13*time.Hour))
	msu, _, _, amb, ok = h3.MustServeUntilForPromise(creation, 50, 320, 0)
	if !ok || amb || !msu.Equal(creation.Add(time.Hour)) {
		t.Fatalf("pre-history promise, no change: msu=%s amb=%v ok=%v", msu, amb, ok)
	}
}

// A params change that reverts before settlement leaves the two ends of the
// interval agreeing. Comparing only the ends called the window unambiguous
// and recorded the later deadline, so every probe between the two deadlines
// would have been published as a retention failure against a validator whose
// server had pruned exactly when its params said it could.
func TestMustServeUntilForPromise_ChangeThatRevertsInsideTheInterval(t *testing.T) {
	creation := time.Unix(1700000000, 0).UTC()
	h := NewParamHistory(100, params(10*time.Minute, time.Hour, 13*time.Hour))
	h.AddFinalizeEvent(295, params(10*time.Minute, 30*time.Minute, 13*time.Hour)) // shorter
	h.AddFinalizeEvent(299, params(10*time.Minute, time.Hour, 13*time.Hour))      // and back again

	msu, snap, basis, amb, ok := h.MustServeUntilForPromise(creation, 290, 320, 0)
	if !ok {
		t.Fatal("no params for the settlement height")
	}
	if !amb {
		t.Fatal("a change that reverted inside the interval is still a change the server could have uploaded under")
	}
	if !msu.Equal(creation.Add(30 * time.Minute)) {
		t.Fatalf("msu=%s, want creation+30m: the shortest window any params in the interval could produce", msu)
	}
	if snap.ShardRetentionSeconds != 1800 {
		t.Fatalf("snapshot should be the params that produced the bound, got retention %ds", snap.ShardRetentionSeconds)
	}
	if !strings.Contains(basis, "AMBIGUOUS") {
		t.Fatalf("basis should say AMBIGUOUS: %q", basis)
	}
}

// The consensus address the staking module implies must be the same
// identifier every probe row and assignment already uses. If the derivation
// drifts, validator names silently stop joining and every row loses its
// moniker with nothing failing.
func TestConsAddressFromConsensusKey(t *testing.T) {
	// A known ed25519 consensus key and the CometBFT address derived from it:
	// the first 20 bytes of SHA-256 over the raw 32-byte key.
	raw := make([]byte, 32)
	for i := range raw {
		raw[i] = byte(i + 1)
	}
	sum := sha256.Sum256(raw)
	want := strings.ToLower(hex.EncodeToString(sum[:20]))

	pk := &ed25519.PubKey{Key: raw}
	b, err := pk.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	got, err := consAddressFromAny(&codectypes.Any{Value: b})
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	if got != want {
		t.Fatalf("cons address = %s, want %s", got, want)
	}
	if len(got) != 40 {
		t.Fatalf("cons address is %d hex chars, want 40 to match the probe rows", len(got))
	}

	// A missing or malformed key is an error, never a blank address: a blank
	// would collide every such validator into one row in the store.
	if _, err := consAddressFromAny(nil); err == nil {
		t.Fatal("a nil consensus key should be an error")
	}
	if _, err := consAddressFromAny(&codectypes.Any{Value: []byte("not a key")}); err == nil {
		t.Fatal("a malformed consensus key should be an error")
	}
}
