package scan

import (
	"testing"
	"time"

	fibretypes "github.com/celestiaorg/celestia-app/v10/x/fibre/types"
	"github.com/celestiaorg/go-square/v4/share"
	abci "github.com/cometbft/cometbft/abci/types"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/bech32"
	cosmostx "github.com/cosmos/cosmos-sdk/types/tx"
)

// rawTx wraps SDK messages in a TxRaw the way a broadcast tx is encoded.
func rawTx(t *testing.T, msgs ...proto) []byte {
	t.Helper()
	body := cosmostx.TxBody{}
	for _, m := range msgs {
		b, err := m.Marshal()
		if err != nil {
			t.Fatal(err)
		}
		body.Messages = append(body.Messages, &codectypes.Any{TypeUrl: m.typeURL(), Value: b})
	}
	bb, err := body.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	raw := cosmostx.TxRaw{BodyBytes: bb, AuthInfoBytes: []byte{}, Signatures: [][]byte{{1}}}
	out, err := raw.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	return out
}

type proto interface {
	Marshal() ([]byte, error)
	typeURL() string
}

type depositMsg struct{ *fibretypes.MsgDepositToEscrow }
type timeoutMsg struct {
	*fibretypes.MsgPaymentPromiseTimeout
}
type withdrawMsg struct {
	*fibretypes.MsgRequestWithdrawal
}
type payMsg struct{ *fibretypes.MsgPayForFibre }

func (depositMsg) typeURL() string  { return msgDepositTypeURL }
func (timeoutMsg) typeURL() string  { return msgTimeoutTypeURL }
func (withdrawMsg) typeURL() string { return msgWithdrawalTypeURL }
func (payMsg) typeURL() string      { return fibretypes.MsgPayForFibreTypeURL }

func testPromise(t *testing.T, key *secp256k1.PrivKey, blobSize uint32) fibretypes.PaymentPromise {
	t.Helper()
	ns := share.MustNewV0Namespace([]byte("economy-t"))
	commit := make([]byte, 32)
	for i := range commit {
		commit[i] = byte(i)
	}
	return fibretypes.PaymentPromise{
		ChainId:           "fibre-devnet",
		Height:            42,
		Namespace:         ns.Bytes(),
		BlobSize:          blobSize,
		BlobVersion:       0,
		Commitment:        commit,
		CreationTimestamp: time.Date(2026, 9, 17, 10, 0, 0, 0, time.UTC),
		SignerPublicKey:   *key.PubKey().(*secp256k1.PubKey),
		Signature:         []byte("sig-bytes-not-verified-here"),
	}
}

func TestPaymentsInTx_EveryKind(t *testing.T) {
	key := secp256k1.GenPrivKey()
	owner, err := bech32.ConvertAndEncode(accountHRP, key.PubKey().Address())
	if err != nil {
		t.Fatal(err)
	}
	other := "celestia1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq"
	pp := testPromise(t, key, 1<<20) // 1 MiB → 4 chunks

	raw := rawTx(t,
		depositMsg{&fibretypes.MsgDepositToEscrow{Signer: owner, Amount: sdk.NewInt64Coin("utia", 6_000_000_000)}},
		payMsg{&fibretypes.MsgPayForFibre{Signer: owner, PaymentPromise: pp}},
		timeoutMsg{&fibretypes.MsgPaymentPromiseTimeout{Signer: other, PaymentPromise: pp}},
		withdrawMsg{&fibretypes.MsgRequestWithdrawal{Signer: owner, Amount: sdk.NewInt64Coin("utia", 7)}},
	)
	blk := &Block{Height: 100, Time: time.Date(2026, 9, 17, 11, 0, 0, 0, time.UTC)}
	now := time.Now()
	ps, err := paymentsInTx(raw, blk, 3, "abcd", now)
	if err != nil {
		t.Fatalf("paymentsInTx: %v", err)
	}
	if len(ps) != 4 {
		t.Fatalf("want 4 payments, got %d: %+v", len(ps), ps)
	}
	kinds := []string{PaymentDeposit, PaymentSettlement, PaymentTimeout, PaymentWithdrawalRequest}
	for i, p := range ps {
		if p.Kind != kinds[i] {
			t.Errorf("payment %d kind=%s want %s", i, p.Kind, kinds[i])
		}
		if p.Height != 100 || p.TxIndex != 3 || p.TxHash != "abcd" || p.MsgIndex != i {
			t.Errorf("payment %d position wrong: %+v", i, p)
		}
		if p.DedupeKey == "" {
			t.Errorf("payment %d without dedupe key", i)
		}
		if p.Publisher != owner {
			t.Errorf("payment %d publisher=%s want %s", i, p.Publisher, owner)
		}
	}
	if ps[0].AmountUtia != 6_000_000_000 || ps[0].Denom != "utia" {
		t.Errorf("deposit amount: %+v", ps[0])
	}
	// The charge is the module's own formula: (650k + 45k×⌈size/256KiB⌉) gas at 1 utia/gas.
	wantGas := uint64(650_000 + 45_000*4)
	for _, i := range []int{1, 2} {
		if ps[i].GasUnits != wantGas || ps[i].AmountUtia != wantGas {
			t.Errorf("payment %d charge gas=%d amount=%d want %d", i, ps[i].GasUnits, ps[i].AmountUtia, wantGas)
		}
		if ps[i].BlobSize != 1<<20 || ps[i].PromiseHash == "" || ps[i].Namespace == "" {
			t.Errorf("payment %d promise fields: %+v", i, ps[i])
		}
	}
	if ps[1].PromiseHash != ps[2].PromiseHash {
		t.Errorf("settlement and timeout of the same promise must share a hash")
	}
	if ps[1].Processor != owner || ps[2].Processor != other {
		t.Errorf("processor: settlement=%s timeout=%s", ps[1].Processor, ps[2].Processor)
	}
	if ps[3].AmountUtia != 7 {
		t.Errorf("withdrawal request amount: %+v", ps[3])
	}
}

func TestPaymentsInTx_NotAnSDKTx(t *testing.T) {
	ps, err := paymentsInTx([]byte("garbage"), &Block{Height: 1}, 0, "x", time.Now())
	if err != nil || len(ps) != 0 {
		t.Fatalf("want nothing, got %v %v", ps, err)
	}
}

func TestWithdrawalsExecuted(t *testing.T) {
	blk := &Block{Height: 7, Time: time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)}
	evs := []abci.Event{
		{Type: "some.other.Event", Attributes: []abci.EventAttribute{{Key: "signer", Value: `"x"`}}},
		{Type: eventWithdrawExecutedType, Attributes: []abci.EventAttribute{
			{Key: "signer", Value: `"celestia1abc"`},
			{Key: "amount", Value: `{"denom":"utia","amount":"123"}`},
		}},
		{Type: eventWithdrawExecutedType, Attributes: []abci.EventAttribute{
			{Key: "amount", Value: `{"denom":"utia","amount":"5"}`},
			{Key: "signer", Value: `"celestia1def"`},
		}},
	}
	ps, err := withdrawalsExecuted(evs, blk, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 2 {
		t.Fatalf("want 2, got %d", len(ps))
	}
	if ps[0].Publisher != "celestia1abc" || ps[0].AmountUtia != 123 || ps[0].Kind != PaymentWithdrawalExecuted {
		t.Errorf("first: %+v", ps[0])
	}
	if ps[1].Publisher != "celestia1def" || ps[1].AmountUtia != 5 {
		t.Errorf("second: %+v", ps[1])
	}
	if ps[0].DedupeKey == ps[1].DedupeKey || ps[0].DedupeKey == "" {
		t.Errorf("dedupe keys: %q %q", ps[0].DedupeKey, ps[1].DedupeKey)
	}
	if ps[0].TxHash != "" || ps[0].TxIndex != -1 {
		t.Errorf("begin-block payout must not claim a tx: %+v", ps[0])
	}

	bad := []abci.Event{{Type: eventWithdrawExecutedType, Attributes: []abci.EventAttribute{
		{Key: "signer", Value: `"celestia1abc"`}, {Key: "amount", Value: `not json`}}}}
	if _, err := withdrawalsExecuted(bad, blk, time.Now()); err == nil {
		t.Error("malformed amount must error")
	}
}

func TestStore_PaymentsDedupeAndResume(t *testing.T) {
	dir := t.TempDir()
	st, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	p := Payment{SchemaVersion: 1, DedupeKey: "k1", Kind: PaymentDeposit, Height: 1, Publisher: "a", AmountUtia: 1}
	for i := 0; i < 3; i++ {
		if err := st.AppendPayment(p); err != nil {
			t.Fatal(err)
		}
	}
	p.DedupeKey = "k2"
	if err := st.AppendPayment(p); err != nil {
		t.Fatal(err)
	}
	if err := st.AppendPayment(Payment{Kind: "x"}); err == nil {
		t.Error("empty dedupe key must be refused")
	}
	if err := st.Sync(); err != nil {
		t.Fatal(err)
	}
	st.Close()

	st2, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st2.Close()
	if !st2.PaymentSeen("k1") || !st2.PaymentSeen("k2") || st2.PaymentSeen("k3") {
		t.Error("seen set not reloaded")
	}
	got, err := LoadPayments(st2.PaymentsPath())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Errorf("want 2 lines, got %d", len(got))
	}
}
