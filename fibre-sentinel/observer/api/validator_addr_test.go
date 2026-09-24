package api_test

import (
	"encoding/hex"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cosmos/cosmos-sdk/types/bech32"

	"github.com/plsgiveup/fibre/fibre-sentinel/internal/scan"
	"github.com/plsgiveup/fibre/fibre-sentinel/observer/api"
	"github.com/plsgiveup/fibre/fibre-sentinel/observer/store"
)

// An operator pastes the address they know: the celestiavaloper1… every
// explorer shows, or the celestia1… account they sign with. Every spelling of
// one validator must land on the same validator, resolved through the staking
// set rather than by reading an operator key's bytes as a consensus address
// (validator_addr.go). The two keys are unrelated, so the fixture gives them
// different bytes: a resolution that merely re-encoded the bytes would find
// nothing.
func TestValidatorResolvesOperatorAndAccountAddresses(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "observer.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	cons, opBytes := make([]byte, 20), make([]byte, 20)
	for i := range cons {
		cons[i], opBytes[i] = byte(0x10+i), byte(0xc0+i)
	}
	consHex := hex.EncodeToString(cons)
	must := func(s string, err error) string {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	valcons := must(bech32.ConvertAndEncode("celestiavalcons", cons))
	valoper := must(bech32.ConvertAndEncode("celestiavaloper", opBytes))
	account := must(bech32.ConvertAndEncode("celestia", opBytes))
	// A second validator, so a lookup that ignored its argument and returned
	// "the" validator would be caught.
	other := make([]byte, 20)
	other[0] = 0xee
	ids := []scan.ValidatorIdentity{
		{ConsAddressHex: consHex, OperatorAddress: valoper, Moniker: "alpha", Tokens: "1000000", Status: "BOND_STATUS_BONDED"},
		{ConsAddressHex: hex.EncodeToString(other), OperatorAddress: must(bech32.ConvertAndEncode("celestiavaloper", other)), Moniker: "beta", Tokens: "900000", Status: "BOND_STATUS_BONDED"},
	}
	if _, err := st.UpsertValidatorIdentities(ids, time.Now()); err != nil {
		t.Fatal(err)
	}
	srv := api.NewWithVantage(st, api.VantageInfo{Name: "test"}, nil, api.WithDataDir(dir))
	ts := httptest.NewServer(srv)
	defer func() { ts.Close(); srv.Close() }()

	for _, addr := range []string{consHex, strings.ToUpper(consHex), valcons, valoper, account} {
		var one struct {
			Validator struct {
				Address     string `json:"address"`
				ConsAddress string `json:"cons_address"`
				Operator    string `json:"operator_address"`
				Moniker     string `json:"moniker"`
			} `json:"validator"`
		}
		if code := get(t, ts, "/v1/validators/"+addr, &one); code != 200 {
			t.Fatalf("%s: %d, want 200", addr, code)
		}
		v := one.Validator
		if v.Address != consHex || v.ConsAddress != valcons || v.Operator != valoper || v.Moniker != "alpha" {
			t.Fatalf("%s resolved to %+v, want alpha at %s", addr, v, consHex)
		}
		// the raw rows and the feed take the same spellings
		if code := get(t, ts, "/v1/probes?validator="+addr, nil); code != 200 {
			t.Fatalf("probes?validator=%s: %d, want 200", addr, code)
		}
		if code := get(t, ts, "/v1/validators/"+addr+"/feed.atom", nil); code != 200 {
			t.Fatalf("feed for %s: %d, want 200", addr, code)
		}
	}

	// A well-formed operator address the staking set never named is a 404,
	// not a 400: the address is real, the validator is not on record.
	unknown := must(bech32.ConvertAndEncode("celestiavaloper", make([]byte, 20)))
	if code := get(t, ts, "/v1/validators/"+unknown, nil); code != 404 {
		t.Fatalf("unknown operator: %d, want 404", code)
	}
	// The consensus public key's prefix is not an address of any kind.
	if code := get(t, ts, "/v1/validators/"+must(bech32.ConvertAndEncode("celestiavalconspub", cons)), nil); code != 400 {
		t.Fatalf("valconspub: want 400, got %d", code)
	}
}
