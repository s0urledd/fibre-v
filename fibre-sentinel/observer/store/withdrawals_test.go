package store_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/plsgiveup/fibre/fibre-sentinel/internal/scan"
	"github.com/plsgiveup/fibre/fibre-sentinel/observer/store"
)

const wdPub = "celestia1d3mmg652pxj776dyqwlsrc93y64088g6ux8deq"

var wdT0 = time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC)

func wd(req time.Time, amt uint64) scan.PendingWithdrawal {
	return scan.PendingWithdrawal{Signer: wdPub, Denom: "utia", AmountUtia: amt, RequestedAt: req, AvailableAt: req.Add(24 * time.Hour)}
}

func observe(t *testing.T, st *store.Store, h int64, at time.Time, ws ...scan.PendingWithdrawal) store.WithdrawalChange {
	t.Helper()
	ch, err := st.ObserveWithdrawals(store.WithdrawalRead{Publisher: wdPub, Height: h, BlockTime: at, Withdrawals: ws}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return ch
}

func payout(t *testing.T, st *store.Store, key string, h int64, at time.Time, amt uint64) {
	t.Helper()
	if _, err := st.UpsertPayment(scan.Payment{SchemaVersion: 1, DedupeKey: key, Kind: scan.PaymentWithdrawalExecuted,
		Height: h, Time: at, TxIndex: -1, Publisher: wdPub, Denom: "utia", AmountUtia: amt}, []byte("{}")); err != nil {
		t.Fatal(err)
	}
}

func outcome(t *testing.T, st *store.Store, req time.Time) (string, string, sql.NullInt64) {
	t.Helper()
	var o sql.NullString
	var reason string
	var paid sql.NullInt64
	if err := st.DB().QueryRow(`SELECT outcome, outcome_reason, paid_height FROM withdrawal_queue WHERE publisher = ? AND requested_at = ?`,
		wdPub, store.TS(req)).Scan(&o, &reason, &paid); err != nil {
		t.Fatal(err)
	}
	return o.String, reason, paid
}

// Two withdrawals of the same amount leave the queue in the same interval
// and one payout of that amount is on record. Either could be the one paid
// (the other consumed); the store refuses to pick.
func TestResolveRefusesAnAmbiguousPayout(t *testing.T) {
	st := open(t)
	ctx := context.Background()
	a, b := wdT0, wdT0.Add(time.Minute)
	observe(t, st, 100, wdT0.Add(2*time.Minute), wd(a, 500), wd(b, 500))
	payable := a.Add(25 * time.Hour)
	observe(t, st, 300, payable)
	payout(t, st, "h250:executed:0", 250, a.Add(24*time.Hour+time.Minute), 500)
	if _, err := st.ResolveWithdrawals(ctx, 400); err != nil {
		t.Fatal(err)
	}
	for _, r := range []time.Time{a, b} {
		if o, reason, paid := outcome(t, st, r); o != store.WithdrawalUnattributed || paid.Valid || reason == "" {
			t.Fatalf("%s: outcome=%q paid=%v reason=%q", r, o, paid, reason)
		}
	}

	// The second payout arrives (ingested late). Now each withdrawal has
	// two candidates and each payout two claimants: still ambiguous, and
	// still said so rather than paired by guess.
	payout(t, st, "h251:executed:0", 251, a.Add(24*time.Hour+2*time.Minute), 500)
	if _, err := st.ResolveWithdrawals(ctx, 400); err != nil {
		t.Fatal(err)
	}
	if o, _, _ := outcome(t, st, a); o != store.WithdrawalUnattributed {
		t.Fatalf("a: %q", o)
	}
}

// An unattributed withdrawal is re-examined: once its payout is ingested it
// becomes executed, with the payout's height.
func TestResolveRetriesUnattributed(t *testing.T) {
	st := open(t)
	ctx := context.Background()
	a := wdT0
	observe(t, st, 100, wdT0.Add(time.Minute), wd(a, 700))
	observe(t, st, 300, a.Add(25*time.Hour))
	if n, err := st.ResolveWithdrawals(ctx, 400); err != nil || n != 1 {
		t.Fatalf("first resolve: n=%d err=%v", n, err)
	}
	if o, _, _ := outcome(t, st, a); o != store.WithdrawalUnattributed {
		t.Fatalf("no payout on record must be unattributed, got %q", o)
	}
	// Re-running with nothing new changes nothing.
	if n, _ := st.ResolveWithdrawals(ctx, 400); n != 0 {
		t.Fatalf("idempotent resolve changed %d", n)
	}
	// A payout outside (last_seen, gone] or before available_at is not it.
	payout(t, st, "early", 200, a.Add(time.Hour), 700)
	payout(t, st, "late", 301, a.Add(26*time.Hour), 700)
	payout(t, st, "other-amount", 250, a.Add(24*time.Hour+time.Minute), 699)
	if n, _ := st.ResolveWithdrawals(ctx, 400); n != 0 {
		t.Fatalf("a payout that cannot be this one was attributed (%d)", n)
	}
	payout(t, st, "right", 250, a.Add(24*time.Hour+6*time.Second), 700)
	if n, _ := st.ResolveWithdrawals(ctx, 400); n != 1 {
		t.Fatalf("payout not attributed (%d)", n)
	}
	if o, _, paid := outcome(t, st, a); o != store.WithdrawalExecuted || paid.Int64 != 250 {
		t.Fatalf("a: %q paid=%v", o, paid)
	}
	// And one payout is never attributed twice.
	var n int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM withdrawal_queue WHERE paid_key = 'right'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("paid_key 'right' claimed %d times (%v)", n, err)
	}
}

// Reads move forward: an older read is ignored whole, a newer read that
// still has a withdrawal recorded gone reopens it (the newer state wins),
// and a read at the same height applies again without double counting.
func TestObserveWithdrawalsOrdering(t *testing.T) {
	st := open(t)
	a := wdT0
	if ch := observe(t, st, 100, wdT0, wd(a, 10)); ch.Opened != 1 {
		t.Fatalf("open: %+v", ch)
	}
	if ch := observe(t, st, 100, wdT0, wd(a, 10)); ch.Opened+ch.Reduced+ch.Closed+ch.Reopened != 0 || ch.Stale {
		t.Fatalf("same read twice: %+v", ch)
	}
	if ch := observe(t, st, 110, wdT0.Add(time.Minute)); ch.Closed != 1 {
		t.Fatalf("close: %+v", ch)
	}
	if ch := observe(t, st, 105, wdT0, wd(a, 10)); !ch.Stale {
		t.Fatalf("older read applied: %+v", ch)
	}
	if ch := observe(t, st, 120, wdT0.Add(2*time.Minute), wd(a, 10)); ch.Reopened != 1 {
		t.Fatalf("reopen: %+v", ch)
	}
	var gone sql.NullInt64
	if err := st.DB().QueryRow(`SELECT gone_height FROM withdrawal_queue`).Scan(&gone); err != nil || gone.Valid {
		t.Fatalf("reopened row still gone: %v %v", gone, err)
	}
	// A read carrying another account's withdrawal is refused.
	other := wd(a.Add(time.Hour), 1)
	other.Signer = "celestia1zyg3zyg3zyg3zyg3zyg3zyg3zyg3zyg3shxjgz"
	if _, err := st.ObserveWithdrawals(store.WithdrawalRead{Publisher: wdPub, Height: 130, BlockTime: wdT0, Withdrawals: []scan.PendingWithdrawal{other}}, time.Now()); err == nil {
		t.Fatal("foreign withdrawal stored")
	}
	if _, err := st.ObserveWithdrawals(store.WithdrawalRead{Publisher: wdPub, Height: 0, BlockTime: wdT0}, time.Now()); err == nil {
		t.Fatal("read without a height accepted")
	}
}

// A withdrawal missed before its available_at was consumed: that is a
// proof from the begin-blocker's ordering, and needs no scanner progress.
func TestResolveConsumedNeedsNoScan(t *testing.T) {
	st := open(t)
	a := wdT0
	observe(t, st, 100, wdT0, wd(a, 10))
	observe(t, st, 110, wdT0.Add(time.Hour))
	if n, err := st.ResolveWithdrawals(context.Background(), 0); err != nil || n != 1 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if o, _, _ := outcome(t, st, a); o != store.WithdrawalConsumed {
		t.Fatalf("outcome %q", o)
	}
}
