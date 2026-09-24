package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/plsgiveup/fibre/fibre-sentinel/internal/scan"
	"github.com/plsgiveup/fibre/fibre-sentinel/observer/store"
)

const pubA = "celestia1d3mmg652pxj776dyqwlsrc93y64088g6ux8deq"

// fakeChain answers the escrow poll from fixed state, one "block" at a time.
type fakeChain struct {
	height    int64
	blockTime time.Time
	escrow    scan.Escrow
	queue     []scan.PendingWithdrawal
	queueErr  error
	answerAt  int64 // height the Withdrawals answer claims; 0 = the asked height
	asked     []int64
	headers   map[int64]time.Time
}

func (f *fakeChain) StatusAt(context.Context) (string, int64, time.Time, error) {
	return "test", f.height, f.blockTime, nil
}

func (f *fakeChain) EscrowAccount(_ context.Context, signer string, height int64) (scan.Escrow, error) {
	f.asked = append(f.asked, height)
	e := f.escrow
	e.Signer, e.Height = signer, height
	return e, nil
}

func (f *fakeChain) Withdrawals(_ context.Context, signer string, height int64) ([]scan.PendingWithdrawal, int64, error) {
	f.asked = append(f.asked, height)
	if f.queueErr != nil {
		return nil, 0, f.queueErr
	}
	at := height
	if f.answerAt != 0 {
		at = f.answerAt
	}
	return append([]scan.PendingWithdrawal(nil), f.queue...), at, nil
}

func (f *fakeChain) HeaderTime(_ context.Context, h int64) (time.Time, error) {
	if t, ok := f.headers[h]; ok {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("height %d is not available, lowest height is 500", h)
}

func quiet(string, ...any) {}

func openStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func pay(t *testing.T, st *store.Store, p scan.Payment) {
	t.Helper()
	p.SchemaVersion, p.Denom = 1, "utia"
	if p.Publisher == "" {
		p.Publisher = pubA
	}
	if _, err := st.UpsertPayment(p, []byte("{}")); err != nil {
		t.Fatal(err)
	}
}

type qrow struct {
	amount, first int64
	gone          sql.NullInt64
	outcome       sql.NullString
	paidHeight    sql.NullInt64
	lastSeen      int64
}

func queueRow(t *testing.T, st *store.Store, requested time.Time) qrow {
	t.Helper()
	var r qrow
	if err := st.DB().QueryRow(`SELECT amount_utia, first_amount_utia, gone_height, outcome, paid_height, last_seen_height
		FROM withdrawal_queue WHERE publisher = ? AND requested_at = ?`, pubA, store.TS(requested)).
		Scan(&r.amount, &r.first, &r.gone, &r.outcome, &r.paidHeight, &r.lastSeen); err != nil {
		t.Fatalf("row %s: %v", requested, err)
	}
	return r
}

// The whole life of two withdrawals, read the way the collector reads them:
// one is shrunk by a settlement shortfall and later paid, the other is
// consumed whole before it was ever payable. Neither fact is in any event.
func TestPollEscrowFollowsTheQueue(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	t0 := time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC)
	r1, r2 := t0, t0.Add(time.Minute)
	delay := 24 * time.Hour
	pay(t, st, scan.Payment{DedupeKey: "dep", Kind: scan.PaymentDeposit, Height: 90, Time: t0.Add(-time.Hour), AmountUtia: 1000})

	c := &fakeChain{height: 100, blockTime: t0.Add(2 * time.Minute),
		escrow: scan.Escrow{Denom: "utia", BalanceUtia: 1000, AvailableUtia: 700, Found: true},
		queue: []scan.PendingWithdrawal{
			{Signer: pubA, Denom: "utia", AmountUtia: 200, RequestedAt: r1, AvailableAt: r1.Add(delay)},
			{Signer: pubA, Denom: "utia", AmountUtia: 100, RequestedAt: r2, AvailableAt: r2.Add(delay)},
		}}
	p := pollEscrow(ctx, c, st, time.Now(), quiet)
	if p.Height != 100 || p.Escrows != 1 || p.Queues != 1 || p.Change.Opened != 2 {
		t.Fatalf("first poll: %+v", p)
	}
	for _, h := range c.asked {
		if h != 100 {
			t.Fatalf("every read of a poll must be at the tip height it started from; asked %v", c.asked)
		}
	}
	var wh sql.NullInt64
	var pending sql.NullInt64
	if err := st.DB().QueryRow(`SELECT withdrawals_height, pending_utia FROM escrow_accounts WHERE publisher = ?`, pubA).Scan(&wh, &pending); err != nil {
		t.Fatal(err)
	}
	if wh.Int64 != 100 || pending.Int64 != 300 {
		t.Fatalf("escrow row: withdrawals_height=%v pending=%v", wh, pending)
	}

	// Block 110, still before either is payable. The state now shows r1
	// shrunk and r2 gone. Only ReduceWithdrawalsForPayment lowers or
	// deletes a queued withdrawal before it is payable, so this is what
	// settlement shortfalls leave behind; the poll must read it as such
	// without any event saying so. (The module's own order is oldest
	// first; the observer does not rely on it, only on the state.)
	c.height, c.blockTime = 110, t0.Add(10*time.Minute)
	c.escrow.BalanceUtia, c.escrow.AvailableUtia = 650, 500
	c.queue = []scan.PendingWithdrawal{{Signer: pubA, Denom: "utia", AmountUtia: 150, RequestedAt: r1, AvailableAt: r1.Add(delay)}}
	p = pollEscrow(ctx, c, st, time.Now(), quiet)
	if p.Change.Reduced != 1 || p.Change.Closed != 1 || p.Resolved != 1 {
		t.Fatalf("second poll: %+v", p)
	}
	if r := queueRow(t, st, r1); r.amount != 150 || r.first != 200 || r.gone.Valid {
		t.Fatalf("r1 after the shortfall: %+v", r)
	}
	if r := queueRow(t, st, r2); !r.gone.Valid || r.gone.Int64 != 110 || r.outcome.String != store.WithdrawalConsumed {
		t.Fatalf("r2 missed before available_at must be consumed: %+v", r)
	}

	// A queue read that fails must leave the queue alone. An empty list
	// would have closed r1.
	c.height, c.blockTime = 120, t0.Add(20*time.Minute)
	c.queueErr = errors.New("rpc: connection reset")
	p = pollEscrow(ctx, c, st, time.Now(), quiet)
	if p.Escrows != 1 || p.Queues != 0 {
		t.Fatalf("failed queue read: %+v", p)
	}
	if r := queueRow(t, st, r1); r.gone.Valid || r.lastSeen != 110 {
		t.Fatalf("a failed read changed r1: %+v", r)
	}
	c.queueErr = nil

	// A node that answers from another height is not stored either.
	c.answerAt = 119
	if p = pollEscrow(ctx, c, st, time.Now(), quiet); p.Queues != 0 {
		t.Fatalf("answer from another height was stored: %+v", p)
	}
	c.answerAt = 0

	// Block 200, a day later: r1 has gone. It was payable, so it waits
	// for the scanner to have read the blocks its payout could be in.
	c.height, c.blockTime = 200, r1.Add(delay).Add(time.Hour)
	c.queue = nil
	c.escrow.BalanceUtia, c.escrow.AvailableUtia = 500, 500
	_ = st.SetMeta("last_scanned_height", "150", time.Now())
	p = pollEscrow(ctx, c, st, time.Now(), quiet)
	if p.Change.Closed != 1 || p.Resolved != 0 {
		t.Fatalf("third poll: %+v", p)
	}
	if r := queueRow(t, st, r1); !r.gone.Valid || r.outcome.Valid {
		t.Fatalf("r1 must wait for the scanner: %+v", r)
	}

	// The scanner reaches past 200 and the payout is on record: the only
	// payout of 150 to this account between heights 110 and 200.
	pay(t, st, scan.Payment{DedupeKey: "h180:executed:0", Kind: scan.PaymentWithdrawalExecuted, Height: 180, Time: r1.Add(delay).Add(6 * time.Second), TxIndex: -1, AmountUtia: 150})
	_ = st.SetMeta("last_scanned_height", "260", time.Now())
	p = pollEscrow(ctx, c, st, time.Now(), quiet)
	if p.Resolved != 1 {
		t.Fatalf("fourth poll: %+v", p)
	}
	if r := queueRow(t, st, r1); r.outcome.String != store.WithdrawalExecuted || r.paidHeight.Int64 != 180 {
		t.Fatalf("r1 must be attributed to the payout at 180: %+v", r)
	}

	// A read from a node behind the last one is ignored whole.
	c.height = 150
	c.queue = []scan.PendingWithdrawal{{Signer: pubA, Denom: "utia", AmountUtia: 150, RequestedAt: r1, AvailableAt: r1.Add(delay)}}
	if p = pollEscrow(ctx, c, st, time.Now(), quiet); p.Queues != 0 {
		t.Fatalf("stale read stored: %+v", p)
	}
	if r := queueRow(t, st, r1); r.outcome.String != store.WithdrawalExecuted {
		t.Fatalf("stale read reopened r1: %+v", r)
	}
}

// No publisher, no chain call.
func TestPollEscrowWithoutPublishers(t *testing.T) {
	st := openStore(t)
	c := &fakeChain{height: 1, blockTime: time.Now()}
	if p := pollEscrow(context.Background(), c, st, time.Now(), quiet); p.Publishers != 0 || len(c.asked) != 0 {
		t.Fatalf("poll with nobody to poll: %+v asked=%v", p, c.asked)
	}
}

// Params heights get their block time from the header; a pruned header
// leaves the height undated and costs no log line.
func TestFillParamTimes(t *testing.T) {
	st := openStore(t)
	t1 := time.Date(2026, 9, 24, 20, 15, 3, 0, time.UTC)
	if err := st.UpsertParams([]scan.ParamEntry{
		{FromHeight: 400, FromTxIndex: -1, Source: "seed"},
		{FromHeight: 600, FromTxIndex: 3, Source: "event"},
	}); err != nil {
		t.Fatal(err)
	}
	c := &fakeChain{headers: map[int64]time.Time{600: t1}}
	logged := 0
	n := fillParamTimes(context.Background(), c, st, func(string, ...any) { logged++ })
	if n != 1 || logged != 0 {
		t.Fatalf("dated %d, logged %d", n, logged)
	}
	var at sql.NullString
	if err := st.DB().QueryRow(`SELECT effective_from_time FROM params_history WHERE effective_from_height = 600`).Scan(&at); err != nil || at.String != store.TS(t1) {
		t.Fatalf("600: %v %v", at, err)
	}
	if err := st.DB().QueryRow(`SELECT effective_from_time FROM params_history WHERE effective_from_height = 400`).Scan(&at); err != nil || at.Valid {
		t.Fatalf("pruned 400 must stay undated: %v %v", at, err)
	}
	// Only the undated height is asked again.
	hs, _ := st.ParamHeightsWithoutTime(context.Background())
	if len(hs) != 1 || hs[0] != 400 {
		t.Fatalf("undated: %v", hs)
	}
}
