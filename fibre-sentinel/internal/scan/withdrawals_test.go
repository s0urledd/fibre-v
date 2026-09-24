package scan

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	fibretypes "github.com/celestiaorg/celestia-app/v10/x/fibre/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	sdkquery "github.com/cosmos/cosmos-sdk/types/query"
)

const wdSigner = "celestia1d3mmg652pxj776dyqwlsrc93y64088g6ux8deq"

func marshalWithdrawals(t *testing.T, resp fibretypes.QueryWithdrawalsResponse) []byte {
	t.Helper()
	b, err := resp.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// The queue decodes to one entry per stored withdrawal, in the order the
// module returned them, with both timestamps kept in UTC and the amount in
// utia.
func TestDecodeWithdrawals(t *testing.T) {
	req := time.Date(2026, 9, 24, 20, 30, 0, 0, time.FixedZone("x", 3600))
	resp := fibretypes.QueryWithdrawalsResponse{Withdrawals: []fibretypes.Withdrawal{
		{Signer: wdSigner, Amount: sdk.NewInt64Coin("utia", 1500), RequestedTimestamp: req, AvailableTimestamp: req.Add(24 * time.Hour)},
		{Signer: wdSigner, Amount: sdk.NewInt64Coin("utia", 7), RequestedTimestamp: req.Add(time.Minute), AvailableTimestamp: req.Add(24*time.Hour + time.Minute)},
	}}
	got, err := decodeWithdrawals(wdSigner, marshalWithdrawals(t, resp))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d withdrawals, want 2", len(got))
	}
	if got[0].AmountUtia != 1500 || got[0].Denom != "utia" || got[1].AmountUtia != 7 {
		t.Fatalf("amounts: %+v", got)
	}
	if got[0].RequestedAt.Location() != time.UTC || !got[0].RequestedAt.Equal(req) || !got[0].AvailableAt.Equal(req.Add(24*time.Hour)) {
		t.Fatalf("timestamps: %+v", got[0])
	}
	if got[0].Signer != wdSigner {
		t.Fatalf("signer: %q", got[0].Signer)
	}
}

// Nothing queued is an empty list, never nil-as-error: the collector closes
// every open row of the account on an empty answer, so the distinction
// between "empty" and "failed" is the whole point.
func TestDecodeWithdrawalsEmpty(t *testing.T) {
	got, err := decodeWithdrawals(wdSigner, marshalWithdrawals(t, fibretypes.QueryWithdrawalsResponse{}))
	if err != nil || got == nil || len(got) != 0 {
		t.Fatalf("empty answer: got=%v err=%v", got, err)
	}
}

// A paginated answer is refused. Upstream never pages (grpc_query.go:46-60);
// keeping page one of one that does would close page two's withdrawals.
func TestDecodeWithdrawalsRefusesPagination(t *testing.T) {
	resp := fibretypes.QueryWithdrawalsResponse{Pagination: &sdkquery.PageResponse{NextKey: []byte{1}}}
	if _, err := decodeWithdrawals(wdSigner, marshalWithdrawals(t, resp)); err == nil || !strings.Contains(err.Error(), "paginated") {
		t.Fatalf("paginated answer accepted: %v", err)
	}
	// A page response without a next key is the last page: fine.
	resp.Pagination = &sdkquery.PageResponse{Total: 0}
	if _, err := decodeWithdrawals(wdSigner, marshalWithdrawals(t, resp)); err != nil {
		t.Fatalf("final page refused: %v", err)
	}
}

// A row for another account is refused rather than filed under this one.
func TestDecodeWithdrawalsRefusesForeignSigner(t *testing.T) {
	resp := fibretypes.QueryWithdrawalsResponse{Withdrawals: []fibretypes.Withdrawal{
		{Signer: "celestia1zyg3zyg3zyg3zyg3zyg3zyg3zyg3zyg3shxjgz", Amount: sdk.NewInt64Coin("utia", 1)},
	}}
	if _, err := decodeWithdrawals(wdSigner, marshalWithdrawals(t, resp)); err == nil {
		t.Fatal("foreign signer accepted")
	}
}

func TestDecodeWithdrawalsGarbage(t *testing.T) {
	if _, err := decodeWithdrawals(wdSigner, []byte{0xff, 0xff, 0xff}); err == nil {
		t.Fatal("garbage decoded")
	}
}

// Opt-in, read-only: the Withdrawals query against a real node. Before the
// node runs x/fibre (app version < 10) the answer must be an error, never an
// empty queue; on a v10 node it must decode.
//
//	FIBRE_LIVE_RPC=http://127.0.0.1:26657 go test ./internal/scan/ -run LiveWithdrawals -v
func TestLiveWithdrawals(t *testing.T) {
	url := os.Getenv("FIBRE_LIVE_RPC")
	if url == "" {
		t.Skip("set FIBRE_LIVE_RPC to run")
	}
	c, err := NewChain(url, 20*time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	av, err := c.AppVersion(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, h, at, err := c.StatusAt(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ht, err := c.HeaderTime(ctx, h)
	t.Logf("app_version=%d tip=%d tip_time=%s header_time=%s err=%v", av, h, at, ht, err)
	if err != nil || !ht.Equal(at.UTC()) {
		t.Fatalf("header time %s != status tip time %s (err %v)", ht, at, err)
	}
	signer := os.Getenv("FIBRE_LIVE_SIGNER")
	if signer == "" {
		signer = wdSigner
	}
	ws, answered, err := c.Withdrawals(ctx, signer, h)
	t.Logf("withdrawals(%s)@%d: n=%d answered_at=%d err=%v", signer, h, len(ws), answered, err)
	if av < FibreAppVersion {
		if err == nil {
			t.Fatalf("app v%d has no x/fibre, yet the query answered %d withdrawals", av, len(ws))
		}
		var ae *ABCIError
		if !errors.As(err, &ae) {
			t.Fatalf("pre-fibre error is not an ABCI error: %v", err)
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	if answered != h {
		t.Fatalf("asked for height %d, node answered at %d", h, answered)
	}
}
