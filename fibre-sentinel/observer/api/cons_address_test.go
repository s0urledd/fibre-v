package api_test

import (
	"encoding/hex"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/cosmos/cosmos-sdk/types/bech32"

	"github.com/plsgiveup/fibre/fibre-sentinel/internal/scan"
	"github.com/plsgiveup/fibre/fibre-sentinel/observer/api"
	"github.com/plsgiveup/fibre/fibre-sentinel/observer/store"
)

// Every validator row carries its consensus address in the form the chain
// prints (celestiavalcons1…), whether or not it registered a Fibre endpoint.
// The bech32 form used to come only from the registry, so before Fibre was
// live — when nobody had registered — every row on the site printed raw hex.
func TestConsAddressIsDerivedWhenTheRegistryDoesNotNameIt(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "observer.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Now()
	raw := make([]byte, 20)
	for i := range raw {
		raw[i] = byte(0xa0 + i)
	}
	hexAddr := hex.EncodeToString(raw)
	want, err := bech32.ConvertAndEncode("celestiavalcons", raw)
	if err != nil {
		t.Fatal(err)
	}
	ids := []scan.ValidatorIdentity{
		{ConsAddressHex: hexAddr, OperatorAddress: "celestiavaloper1alpha", Moniker: "alpha", Tokens: "1000000", Status: "BOND_STATUS_BONDED"},
	}
	if _, err := st.UpsertValidatorIdentities(ids, now); err != nil {
		t.Fatal(err)
	}
	srv := api.NewWithVantage(st, api.VantageInfo{Name: "test"}, nil, api.WithDataDir(dir))
	ts := httptest.NewServer(srv)
	defer func() { ts.Close(); srv.Close() }()

	var vals struct {
		Validators []struct {
			Address     string `json:"address"`
			ConsAddress string `json:"cons_address"`
			Host        string `json:"host"`
		} `json:"validators"`
	}
	if code := get(t, ts, "/v1/validators", &vals); code != 200 {
		t.Fatalf("validators: %d", code)
	}
	if len(vals.Validators) != 1 {
		t.Fatalf("rows: %d, want 1", len(vals.Validators))
	}
	v := vals.Validators[0]
	if v.Host != "" {
		t.Fatalf("host %q: the fixture registers no endpoint", v.Host)
	}
	if v.Address != hexAddr || v.ConsAddress != want {
		t.Fatalf("address %q cons_address %q, want %q and %q", v.Address, v.ConsAddress, hexAddr, want)
	}

	var one struct {
		Validator struct {
			ConsAddress string `json:"cons_address"`
		} `json:"validator"`
	}
	if code := get(t, ts, "/v1/validators/"+hexAddr, &one); code != 200 {
		t.Fatalf("validator: %d", code)
	}
	if one.Validator.ConsAddress != want {
		t.Fatalf("detail cons_address %q, want %q", one.Validator.ConsAddress, want)
	}
}
