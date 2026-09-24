package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/plsgiveup/fibre/fibre-sentinel/internal/scan"
)

// The withdrawal queue as x/fibre holds it, kept as history.
//
// The payments table has both withdrawal events, and neither is the queue.
// A request event says a withdrawal was queued; an executed event says an
// amount was paid out, with no requested_at to say which request it paid.
// In between, a settlement or timeout that finds the available balance
// short silently reduces the queued withdrawals, oldest first, down to
// nothing if need be (x/fibre/keeper/msg_server.go:313-320, which calls
// keeper.go:487-512 at the pinned commit). No event announces it.
//
// So the collector reads the queue itself, with x/fibre's Withdrawals
// query, for every publisher it polls, and this file turns successive reads
// into history:
//
//   - A withdrawal is identified by (publisher, requested_at). The module
//     keys it that way and refuses a second request in the same block
//     (msg_server.go:91-95), so the pair names one withdrawal for life.
//   - Its amount can only go down while it is queued, and only through
//     ReduceWithdrawalsForPayment (keeper.go:501-502). A read that finds a
//     smaller amount than the first sighting is a settlement shortfall
//     taken out of it; first_amount_utia keeps what was first seen.
//   - It leaves the queue in exactly one of two ways: the begin-blocker
//     pays it once block time reaches available_at (abci.go:56-114), or a
//     shortfall consumes it whole (keeper.go:497-499). The first read that
//     no longer has it records gone_height and that block's time.
//
// Which of the two happened is settled afterwards (ResolveWithdrawals):
//
//   - consumed: the block it was first missed at is earlier than its
//     available_at. The begin-blocker cannot have paid it by then
//     (abci.go:68-71 stops at the first withdrawal not yet available), so
//     the only remaining path is a settlement shortfall. This is a proof,
//     not a guess.
//   - executed: exactly one payout event for this publisher, of exactly the
//     last-seen amount, at or after available_at, inside the heights
//     between the last read that had it and the first that did not, and
//     no other candidate withdrawal of the publisher could claim the same
//     event. Only then is request→payout delay published for it.
//   - unattributed: everything else. A withdrawal a settlement reduced
//     after the last read and the begin-blocker then paid carries a
//     smaller amount than was ever seen; two identical withdrawals paid in
//     the same interval cannot be told apart. Neither is resolved by
//     guessing, and an unattributed row is re-examined every pass, since
//     the payout it is missing may simply not be ingested yet.

// Outcomes of a withdrawal that left the queue.
const (
	WithdrawalExecuted     = "executed"
	WithdrawalConsumed     = "consumed"
	WithdrawalUnattributed = "unattributed"
)

// withdrawalQueueMigration is schema version 21. It is referenced from the
// migrations list in store.go; keeping the statements here keeps them next
// to the only code that writes them.
var withdrawalQueueMigration = migration{
	version: 21,
	note:    "the withdrawal queue read from x/fibre state (events cannot rebuild it), and the block time each params value took effect at",
	stmts: []string{
		// One row per withdrawal ever seen queued. gone_height is NULL
		// while it is still queued: "pending" is exactly that test.
		//
		// first_seen_* and last_seen_* are the reads that bracket the
		// sightings, not the request itself: requested_at is the chain's
		// own timestamp and is always exact; first_seen_height is only
		// when this observer first looked. The payout search window is
		// (last_seen_height, gone_height], because the payout, if any,
		// happened after the last read that still had the withdrawal and
		// no later than the first read that did not.
		//
		// paid_key is the payments row an execution was attributed to,
		// so no payout can be attributed twice.
		`CREATE TABLE IF NOT EXISTS withdrawal_queue (
			publisher          TEXT NOT NULL,
			requested_at       TEXT NOT NULL,
			available_at       TEXT NOT NULL,
			denom              TEXT NOT NULL DEFAULT '',
			first_amount_utia  INTEGER NOT NULL,
			amount_utia        INTEGER NOT NULL,
			first_seen_height  INTEGER NOT NULL,
			first_seen_at      TEXT NOT NULL,
			last_seen_height   INTEGER NOT NULL,
			last_seen_at       TEXT NOT NULL,
			gone_height        INTEGER,
			gone_at            TEXT,
			outcome            TEXT,
			outcome_reason     TEXT NOT NULL DEFAULT '',
			paid_key           TEXT,
			paid_height        INTEGER,
			paid_at            TEXT,
			paid_utia          INTEGER,
			recorded_at        TEXT NOT NULL,
			PRIMARY KEY (publisher, requested_at)
		)`,
		`CREATE INDEX IF NOT EXISTS withdrawal_queue_open ON withdrawal_queue (gone_height, available_at)`,
		`CREATE INDEX IF NOT EXISTS withdrawal_queue_paid ON withdrawal_queue (paid_at)`,
		`CREATE INDEX IF NOT EXISTS withdrawal_queue_paid_key ON withdrawal_queue (paid_key)`,
		// The read that produced a publisher's queue, beside the escrow
		// read it was taken with. When withdrawals_height equals height
		// the two came from the same state, and balance - available must
		// equal pending_utia: the module keeps that invariant
		// (msg_server.go:107-112 locks the amount out of available,
		// abci.go:109-111 takes it out of balance on payout, and
		// msg_server.go:313-320 reduces the queue by exactly the part of a
		// charge available could not cover). The API publishes the check.
		`ALTER TABLE escrow_accounts ADD COLUMN withdrawals_height INTEGER`,
		`ALTER TABLE escrow_accounts ADD COLUMN withdrawals_at     TEXT`,
		`ALTER TABLE escrow_accounts ADD COLUMN pending_utia       INTEGER`,
		// Block time of effective_from_height, filled by the collector
		// from the header. NULL until read, and left NULL if the node has
		// pruned the header: an unknown time is published as unknown.
		`ALTER TABLE params_history ADD COLUMN effective_from_time TEXT`,
	},
}

// WithdrawalRead is one successful Withdrawals query for one publisher.
// Height and BlockTime are the state it was read from; a failed query must
// never become a WithdrawalRead, because an empty list closes every open
// row of the publisher.
type WithdrawalRead struct {
	Publisher   string
	Height      int64
	BlockTime   time.Time
	Withdrawals []scan.PendingWithdrawal
}

// WithdrawalChange is what one read changed.
type WithdrawalChange struct {
	Opened   int // first sighting of a withdrawal
	Reduced  int // a queued amount went down: a settlement shortfall took part of it
	Closed   int // no longer queued at this read
	Reopened int // seen again after being recorded gone (only a read from a node behind the last one can do this; see ObserveWithdrawals)
	// Stale is set when the read is older than the last one stored for
	// the publisher and was therefore ignored as a whole.
	Stale bool
}

// ObserveWithdrawals folds one read of a publisher's queue into the history,
// in one transaction.
//
// Reads must move forward. A read at a height below the last one stored for
// this publisher (a lagging node behind a load balancer, say) is ignored as a
// whole: applying it would close withdrawals that later state still has, or
// resurrect ones already gone. A read at the same height is applied again,
// harmlessly. A withdrawal that reappears at a height above the one it was
// recorded gone at contradicts the module (a deleted key cannot come back
// with the same requested_at) and is reopened rather than hidden: the
// reading from the newer state wins.
func (s *Store) ObserveWithdrawals(r WithdrawalRead, now time.Time) (WithdrawalChange, error) {
	var ch WithdrawalChange
	if r.Publisher == "" || r.Height <= 0 || r.BlockTime.IsZero() {
		return ch, fmt.Errorf("withdrawal read without a publisher, height or block time")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return ch, err
	}
	defer tx.Rollback()

	var lastRead sql.NullInt64
	if err := tx.QueryRow(`SELECT withdrawals_height FROM escrow_accounts WHERE publisher = ?`, r.Publisher).Scan(&lastRead); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return ch, fmt.Errorf("last read of %s: %w", r.Publisher, err)
	}
	// Rows can exist without an escrow row having recorded the read (the
	// escrow row is written by UpsertEscrowAccount and may be missing);
	// the rows' own heights are the second guard.
	// Two plain aggregates rather than a scalar MAX(a, b): the schema stays
	// inside the SQL SQLite and Postgres share, and Postgres spells that
	// GREATEST.
	var lastSeen, lastGone sql.NullInt64
	if err := tx.QueryRow(`SELECT MAX(last_seen_height), MAX(gone_height) FROM withdrawal_queue WHERE publisher = ?`, r.Publisher).Scan(&lastSeen, &lastGone); err != nil {
		return ch, fmt.Errorf("last row of %s: %w", r.Publisher, err)
	}
	behind := func(v sql.NullInt64) bool { return v.Valid && r.Height < v.Int64 }
	if behind(lastRead) || behind(lastSeen) || behind(lastGone) {
		ch.Stale = true
		return ch, nil
	}

	at := ts(r.BlockTime)
	seen := make(map[string]bool, len(r.Withdrawals))
	var pending int64
	for _, w := range r.Withdrawals {
		if w.Signer != "" && w.Signer != r.Publisher {
			return ch, fmt.Errorf("withdrawal of %s in the read of %s", w.Signer, r.Publisher)
		}
		key := ts(w.RequestedAt)
		seen[key] = true
		amt := int64(w.AmountUtia)
		pending += amt
		var cur int64
		var gone sql.NullInt64
		err := tx.QueryRow(`SELECT amount_utia, gone_height FROM withdrawal_queue WHERE publisher = ? AND requested_at = ?`, r.Publisher, key).Scan(&cur, &gone)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			if _, err := tx.Exec(`INSERT INTO withdrawal_queue
				(publisher, requested_at, available_at, denom, first_amount_utia, amount_utia,
				 first_seen_height, first_seen_at, last_seen_height, last_seen_at, recorded_at)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				r.Publisher, key, ts(w.AvailableAt), w.Denom, amt, amt, r.Height, at, r.Height, at, ts(now)); err != nil {
				return ch, fmt.Errorf("withdrawal %s@%s: %w", r.Publisher, key, err)
			}
			ch.Opened++
			continue
		case err != nil:
			return ch, fmt.Errorf("withdrawal %s@%s: %w", r.Publisher, key, err)
		}
		if gone.Valid {
			// Guarded above: r.Height >= every gone_height of the
			// publisher. Equal means the same state said both "gone" and
			// "queued", which only a replayed read at the same height
			// after a reopen can produce; treat it like a strictly newer
			// read and let the queued answer stand.
			if _, err := tx.Exec(`UPDATE withdrawal_queue SET gone_height = NULL, gone_at = NULL, outcome = NULL,
					outcome_reason = '', paid_key = NULL, paid_height = NULL, paid_at = NULL, paid_utia = NULL
				WHERE publisher = ? AND requested_at = ?`, r.Publisher, key); err != nil {
				return ch, err
			}
			ch.Reopened++
		}
		if amt < cur {
			ch.Reduced++
		}
		if _, err := tx.Exec(`UPDATE withdrawal_queue SET amount_utia = ?, last_seen_height = ?, last_seen_at = ?
			WHERE publisher = ? AND requested_at = ?`, amt, r.Height, at, r.Publisher, key); err != nil {
			return ch, err
		}
	}

	// Every open row the read no longer has has left the queue at or
	// before this height.
	rows, err := tx.Query(`SELECT requested_at FROM withdrawal_queue WHERE publisher = ? AND gone_height IS NULL`, r.Publisher)
	if err != nil {
		return ch, err
	}
	var gone []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			rows.Close()
			return ch, err
		}
		if !seen[k] {
			gone = append(gone, k)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return ch, err
	}
	for _, k := range gone {
		if _, err := tx.Exec(`UPDATE withdrawal_queue SET gone_height = ?, gone_at = ? WHERE publisher = ? AND requested_at = ?`,
			r.Height, at, r.Publisher, k); err != nil {
			return ch, err
		}
		ch.Closed++
	}

	if _, err := tx.Exec(`UPDATE escrow_accounts SET withdrawals_height = ?, withdrawals_at = ?, pending_utia = ? WHERE publisher = ?`,
		r.Height, at, pending, r.Publisher); err != nil {
		return ch, err
	}
	return ch, tx.Commit()
}

// withdrawalCandidate is a gone withdrawal whose outcome is not final.
type withdrawalCandidate struct {
	publisher, requestedAt, availableAt, goneAt string
	amount, lastSeen, goneHeight                int64
	// events are the unclaimed payouts this row could be.
	events []string
}

// ResolveWithdrawals decides, for every withdrawal that has left the queue
// and has no final outcome, whether it was paid or consumed (see the top of
// this file for the rules). scannedThrough is the scanner's checkpoint as
// the collector last ingested it: a payout is only searched for once the
// scanner has read every block it could be in, so a withdrawal waits rather
// than being called unattributed for a block nobody has read yet.
//
// It returns how many rows changed outcome.
func (s *Store) ResolveWithdrawals(ctx context.Context, scannedThrough int64) (int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	rows, err := tx.QueryContext(ctx, `SELECT publisher, requested_at, available_at, gone_at, amount_utia, last_seen_height, gone_height
		FROM withdrawal_queue
		WHERE gone_height IS NOT NULL AND (outcome IS NULL OR outcome = ?)
		ORDER BY publisher, available_at, requested_at`, WithdrawalUnattributed)
	if err != nil {
		return 0, err
	}
	var cands []*withdrawalCandidate
	for rows.Next() {
		c := &withdrawalCandidate{}
		if err := rows.Scan(&c.publisher, &c.requestedAt, &c.availableAt, &c.goneAt, &c.amount, &c.lastSeen, &c.goneHeight); err != nil {
			rows.Close()
			return 0, err
		}
		cands = append(cands, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}

	changed := 0
	set := func(c *withdrawalCandidate, outcome, reason string, paidKey any, paidHeight any, paidAt any, paidUtia any) error {
		res, err := tx.ExecContext(ctx, `UPDATE withdrawal_queue SET outcome = ?, outcome_reason = ?, paid_key = ?, paid_height = ?, paid_at = ?, paid_utia = ?
			WHERE publisher = ? AND requested_at = ? AND (outcome IS NULL OR outcome <> ? OR outcome_reason <> ?)`,
			outcome, reason, paidKey, paidHeight, paidAt, paidUtia, c.publisher, c.requestedAt, outcome, reason)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			changed++
		}
		return nil
	}

	// First pass: what each row could be.
	claims := map[string]int{} // payments.dedupe_key → how many rows could claim it
	var searchable []*withdrawalCandidate
	for _, c := range cands {
		if c.goneAt < c.availableAt {
			// Missed before it could be paid: a settlement shortfall
			// consumed it. Final.
			if err := set(c, WithdrawalConsumed,
				"left the queue at a block ("+c.goneAt+") before its available_at ("+c.availableAt+"): the begin-blocker cannot have paid it, so a settlement or timeout that found the available balance short consumed it (x/fibre msg_server.go:313-320)",
				nil, nil, nil, nil); err != nil {
				return changed, err
			}
			continue
		}
		if scannedThrough < c.goneHeight {
			continue // the payout may be in a block nobody has read yet
		}
		erows, err := tx.QueryContext(ctx, `SELECT dedupe_key FROM payments
			WHERE kind = 'withdrawal_executed' AND publisher = ? AND height > ? AND height <= ?
			  AND time >= ? AND amount_utia = ?
			  AND dedupe_key NOT IN (SELECT paid_key FROM withdrawal_queue WHERE paid_key IS NOT NULL)
			ORDER BY height, msg_index`, c.publisher, c.lastSeen, c.goneHeight, c.availableAt, c.amount)
		if err != nil {
			return changed, err
		}
		for erows.Next() {
			var k string
			if err := erows.Scan(&k); err != nil {
				erows.Close()
				return changed, err
			}
			c.events = append(c.events, k)
			claims[k]++
		}
		erows.Close()
		if err := erows.Err(); err != nil {
			return changed, err
		}
		searchable = append(searchable, c)
	}

	// Second pass: attribute only what is unambiguous.
	for _, c := range searchable {
		switch {
		case len(c.events) == 1 && claims[c.events[0]] == 1:
			var h int64
			var at string
			var amt int64
			if err := tx.QueryRowContext(ctx, `SELECT height, time, amount_utia FROM payments WHERE dedupe_key = ?`, c.events[0]).Scan(&h, &at, &amt); err != nil {
				return changed, err
			}
			if err := set(c, WithdrawalExecuted,
				"paid by the begin-blocker: the only payout of the last-seen amount to this account between the read that last had it and the read that first did not",
				c.events[0], h, at, amt); err != nil {
				return changed, err
			}
		case len(c.events) == 0:
			if err := set(c, WithdrawalUnattributed,
				"left the queue after it became payable, but no payout of the last-seen amount is recorded between the two reads: a settlement may have reduced it after the last read and the begin-blocker then paid the smaller amount, or consumed it whole; the chain does not say which",
				nil, nil, nil, nil); err != nil {
				return changed, err
			}
		default:
			if err := set(c, WithdrawalUnattributed,
				"more than one payout could be this withdrawal's, or another withdrawal could claim the same payout; not resolved by guessing",
				nil, nil, nil, nil); err != nil {
				return changed, err
			}
		}
	}
	return changed, tx.Commit()
}

// ParamHeightsWithoutTime lists the heights of params_history rows whose
// block time has not been read yet, oldest first.
func (s *Store) ParamHeightsWithoutTime(ctx context.Context) ([]int64, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT effective_from_height FROM params_history
		WHERE effective_from_time IS NULL ORDER BY effective_from_height`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var h int64
		if err := rows.Scan(&h); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// SetParamTime records the block time of height on every params_history row
// that takes effect there. A time already recorded is never overwritten: a
// block's time does not change.
func (s *Store) SetParamTime(height int64, t time.Time) error {
	if t.IsZero() {
		return fmt.Errorf("params h=%d: zero block time", height)
	}
	_, err := s.db.Exec(`UPDATE params_history SET effective_from_time = ?
		WHERE effective_from_height = ? AND effective_from_time IS NULL`, ts(t), height)
	return err
}
