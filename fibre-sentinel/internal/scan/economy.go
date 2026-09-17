package scan

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	celfibre "github.com/celestiaorg/celestia-app/v10/fibre"
	fibretypes "github.com/celestiaorg/celestia-app/v10/x/fibre/types"
	abci "github.com/cometbft/cometbft/abci/types"
	cmttypes "github.com/cometbft/cometbft/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/bech32"
	cosmostx "github.com/cosmos/cosmos-sdk/types/tx"
)

// The economy side of x/fibre, recorded beside the publications so the
// observer can say who publishes, what they pay, and which promises were
// abandoned. Every record here is a count of something the chain did; none
// is a measurement of ours.
//
// What the chain does not record cannot appear here: a promise that was
// handed out and never settled leaves no trace until someone submits its
// timeout, so the timeout count is a floor, never a total. The settlement
// amount is not in any event; it is recomputed from the promise's blob_size
// with the module's own formula, which is what the module charges.

// accountHRP is the bech32 prefix of a Celestia account.
const accountHRP = "celestia"

// PaymentSchemaVersion is bumped when a field changes meaning.
const PaymentSchemaVersion = 1

// Payment kinds. "settlement" and "timeout" charge an escrow; "deposit"
// credits one; the two withdrawal kinds lock and pay out.
const (
	PaymentSettlement         = "settlement"
	PaymentTimeout            = "timeout"
	PaymentDeposit            = "deposit"
	PaymentWithdrawalRequest  = "withdrawal_request"
	PaymentWithdrawalExecuted = "withdrawal_executed"
)

const (
	msgTimeoutTypeURL         = "/celestia.fibre.v1.MsgPaymentPromiseTimeout"
	msgDepositTypeURL         = "/celestia.fibre.v1.MsgDepositToEscrow"
	msgWithdrawalTypeURL      = "/celestia.fibre.v1.MsgRequestWithdrawal"
	eventWithdrawExecutedType = "celestia.fibre.v1.EventWithdrawFromEscrowExecuted"
	eventAttrSigner           = "signer"
	eventAttrAmount           = "amount"
)

// Payment is one movement on a publisher's escrow, as the chain recorded it.
type Payment struct {
	SchemaVersion int    `json:"schema_version"`
	DedupeKey     string `json:"dedupe_key"`
	Kind          string `json:"kind"`

	Height   int64     `json:"height"`
	Time     time.Time `json:"time"`
	TxHash   string    `json:"tx_hash,omitempty"` // empty for a begin-block payout
	TxIndex  int       `json:"tx_index"`
	MsgIndex int       `json:"msg_index"`

	// Publisher is the escrow account: for a settlement or timeout it is
	// derived from the promise's signer public key, which is the account the
	// module charges, whoever broadcast the transaction.
	Publisher string `json:"publisher"`
	// Processor is the transaction signer for a settlement or timeout. For a
	// timeout it can be anyone; in practice a validator that held the
	// abandoned promise.
	Processor string `json:"processor,omitempty"`

	PromiseHash string `json:"promise_hash,omitempty"`
	Namespace   string `json:"namespace,omitempty"`
	BlobSize    uint32 `json:"blob_size,omitempty"`
	GasUnits    uint64 `json:"gas_units,omitempty"`

	Denom      string `json:"denom"`
	AmountUtia uint64 `json:"amount_utia"`

	// AvailableAt is when a requested withdrawal becomes payable.
	AvailableAt *time.Time `json:"available_at,omitempty"`

	RecordedAt time.Time `json:"recorded_at"`
}

// accountFromPubKey is the bech32 account address the module charges for a
// promise: the escrow owner, from the secp256k1 key that signed it.
func accountFromPubKey(pp *fibretypes.PaymentPromise) (string, error) {
	addr := pp.SignerPublicKey.Address()
	if len(addr) == 0 {
		return "", fmt.Errorf("promise without a signer public key")
	}
	return bech32.ConvertAndEncode(accountHRP, addr)
}

func coinAmount(c sdk.Coin) (string, uint64) {
	if c.Amount.IsNil() {
		return c.Denom, 0
	}
	if !c.Amount.IsUint64() {
		return c.Denom, math.MaxUint64
	}
	return c.Denom, c.Amount.Uint64()
}

// txMsg is one message of a decoded SDK transaction.
type txMsg struct {
	index   int
	typeURL string
	value   []byte
}

// decodeTxMsgs lists the messages of raw SDK tx bytes. Anything that is not
// an SDK tx yields nothing.
func decodeTxMsgs(txBytes []byte) []txMsg {
	var raw cosmostx.TxRaw
	if err := raw.Unmarshal(txBytes); err != nil {
		return nil
	}
	var body cosmostx.TxBody
	if err := body.Unmarshal(raw.BodyBytes); err != nil {
		return nil
	}
	out := make([]txMsg, 0, len(body.Messages))
	for i, m := range body.Messages {
		if m == nil {
			continue
		}
		out = append(out, txMsg{index: i, typeURL: m.TypeUrl, value: m.Value})
	}
	return out
}

// promiseCharge is what the module charges for a promise: the gas formula at
// one utia per gas, and the promise hash that identifies it on chain.
func promiseCharge(pp *fibretypes.PaymentPromise) (hash string, gas uint64, amount uint64, err error) {
	var internal celfibre.PaymentPromise
	if err := internal.FromProto(pp); err != nil {
		return "", 0, 0, fmt.Errorf("promise FromProto: %w", err)
	}
	h, err := internal.Hash()
	if err != nil {
		return "", 0, 0, fmt.Errorf("promise hash: %w", err)
	}
	gas = fibretypes.EstimateGasForPayForFibre(pp.BlobSize)
	_, amount = coinAmount(fibretypes.PaymentAmount(pp.BlobSize))
	return hexstr(h), gas, amount, nil
}

// paymentsInTx decodes the economy messages of one successful transaction.
// Failed transactions move no escrow and are not called with.
func paymentsInTx(txBytes []byte, blk *Block, txIndex int, txHash string, now time.Time) ([]Payment, error) {
	var out []Payment
	for _, m := range decodeTxMsgs(txBytes) {
		base := Payment{
			SchemaVersion: PaymentSchemaVersion,
			DedupeKey:     fmt.Sprintf("%s:%d", txHash, m.index),
			Height:        blk.Height,
			Time:          blk.Time.UTC(),
			TxHash:        txHash,
			TxIndex:       txIndex,
			MsgIndex:      m.index,
			RecordedAt:    now,
		}
		switch m.typeURL {
		case fibretypes.MsgPayForFibreTypeURL:
			var msg fibretypes.MsgPayForFibre
			if err := msg.Unmarshal(m.value); err != nil {
				return out, fmt.Errorf("msg %d: unmarshal MsgPayForFibre: %w", m.index, err)
			}
			p, err := chargePayment(base, PaymentSettlement, &msg.PaymentPromise, msg.Signer)
			if err != nil {
				return out, fmt.Errorf("msg %d: %w", m.index, err)
			}
			out = append(out, p)
		case msgTimeoutTypeURL:
			var msg fibretypes.MsgPaymentPromiseTimeout
			if err := msg.Unmarshal(m.value); err != nil {
				return out, fmt.Errorf("msg %d: unmarshal MsgPaymentPromiseTimeout: %w", m.index, err)
			}
			p, err := chargePayment(base, PaymentTimeout, &msg.PaymentPromise, msg.Signer)
			if err != nil {
				return out, fmt.Errorf("msg %d: %w", m.index, err)
			}
			out = append(out, p)
		case msgDepositTypeURL:
			var msg fibretypes.MsgDepositToEscrow
			if err := msg.Unmarshal(m.value); err != nil {
				return out, fmt.Errorf("msg %d: unmarshal MsgDepositToEscrow: %w", m.index, err)
			}
			p := base
			p.Kind = PaymentDeposit
			p.Publisher = msg.Signer
			p.Denom, p.AmountUtia = coinAmount(msg.Amount)
			out = append(out, p)
		case msgWithdrawalTypeURL:
			var msg fibretypes.MsgRequestWithdrawal
			if err := msg.Unmarshal(m.value); err != nil {
				return out, fmt.Errorf("msg %d: unmarshal MsgRequestWithdrawal: %w", m.index, err)
			}
			p := base
			p.Kind = PaymentWithdrawalRequest
			p.Publisher = msg.Signer
			p.Denom, p.AmountUtia = coinAmount(msg.Amount)
			out = append(out, p)
		}
	}
	return out, nil
}

func chargePayment(base Payment, kind string, pp *fibretypes.PaymentPromise, processor string) (Payment, error) {
	hash, gas, amount, err := promiseCharge(pp)
	if err != nil {
		return Payment{}, err
	}
	owner, err := accountFromPubKey(pp)
	if err != nil {
		return Payment{}, err
	}
	p := base
	p.Kind = kind
	p.Publisher = owner
	p.Processor = processor
	p.PromiseHash = hash
	p.Namespace = hex.EncodeToString(pp.Namespace)
	p.BlobSize = pp.BlobSize
	p.GasUnits = gas
	p.Denom = "utia"
	p.AmountUtia = amount
	return p, nil
}

// withdrawalsExecuted pulls the begin-block payouts out of a block's
// finalize events. Typed-event attribute values are JSON: the signer is a
// quoted string, the amount a {denom, amount} object.
func withdrawalsExecuted(evs []abci.Event, blk *Block, now time.Time) ([]Payment, error) {
	var out []Payment
	n := 0
	for _, ev := range evs {
		if ev.Type != eventWithdrawExecutedType {
			continue
		}
		p := Payment{
			SchemaVersion: PaymentSchemaVersion,
			Kind:          PaymentWithdrawalExecuted,
			Height:        blk.Height,
			Time:          blk.Time.UTC(),
			TxIndex:       -1,
			MsgIndex:      n,
			RecordedAt:    now,
		}
		for _, a := range ev.Attributes {
			switch a.Key {
			case eventAttrSigner:
				p.Publisher = strings.Trim(a.Value, `"`)
			case eventAttrAmount:
				var c struct {
					Denom  string `json:"denom"`
					Amount string `json:"amount"`
				}
				if err := json.Unmarshal([]byte(a.Value), &c); err != nil {
					return out, fmt.Errorf("event %d: amount %q: %w", n, a.Value, err)
				}
				p.Denom = c.Denom
				v, err := strconv.ParseUint(c.Amount, 10, 64)
				if err != nil {
					return out, fmt.Errorf("event %d: amount %q: %w", n, c.Amount, err)
				}
				p.AmountUtia = v
			}
		}
		if p.Publisher == "" {
			return out, fmt.Errorf("event %d: %s without a signer", n, ev.Type)
		}
		p.DedupeKey = fmt.Sprintf("h%d:executed:%d", blk.Height, n)
		out = append(out, p)
		n++
	}
	return out, nil
}

// recordEconomy appends every escrow movement in a block. Failed transactions
// are skipped: they move nothing. Decode errors are logged, not fatal, so an
// unexpected message shape cannot stop the scan of publications.
func (s *Scanner) recordEconomy(blk *Block, res *BlockResults, h int64) int {
	now := time.Now().UTC()
	recorded := 0
	for i, raw := range blk.Txs {
		if res.TxCodes[i] != 0 {
			continue
		}
		txHash := hexstr(cmttypes.Tx(raw).Hash())
		ps, err := paymentsInTx(raw, blk, i, txHash, now)
		if err != nil {
			s.log.Printf("h=%d tx=%d (%s): economy record: %v", h, i, txHash[:12], err)
		}
		for _, p := range ps {
			if err := s.store.AppendPayment(p); err != nil {
				s.log.Fatalf("h=%d tx=%d: append payment: %v", h, i, err)
			}
			recorded++
			s.log.Printf("PAYMENT h=%d %s publisher=%s amount=%d%s%s", h, p.Kind, p.Publisher, p.AmountUtia, p.Denom,
				map[bool]string{true: " promise=" + trunc(p.PromiseHash, 12), false: ""}[p.PromiseHash != ""])
		}
	}
	ps, err := withdrawalsExecuted(res.FinalizeEvts, blk, now)
	if err != nil {
		s.log.Printf("h=%d: economy record (begin-block): %v", h, err)
	}
	for _, p := range ps {
		if err := s.store.AppendPayment(p); err != nil {
			s.log.Fatalf("h=%d: append payment: %v", h, err)
		}
		recorded++
		s.log.Printf("PAYMENT h=%d %s publisher=%s amount=%d%s", h, p.Kind, p.Publisher, p.AmountUtia, p.Denom)
	}
	return recorded
}

func trunc(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
