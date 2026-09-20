package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/plsgiveup/fibre/fibre-sentinel/internal/scan"
)

// Correction is one append-only line of corrections.jsonl: a deadline that
// moved after a params range was verified, or a probe row re-graded against
// the moved deadline. It is the twin of Amendment, with two deliberate
// differences.
//
// It is keyed on (target, uncertainty range) rather than on the target
// alone, because a row can sit inside more than one range and each is
// judged on its own. And its tables carry no foreign key to probes, so the
// retention prune cannot delete the record of a verdict this observer once
// published and later withdrew — which is what happens to probe_amendments
// today, through its ON DELETE CASCADE.
type Correction struct {
	SchemaVersion int    `json:"schema_version"`
	Kind          string `json:"kind"` // publication_deadline | probe_verdict
	UncertaintyID string `json:"uncertainty_id"`
	PromiseHash   string `json:"promise_hash"`

	FromMustServeUntil time.Time `json:"from_must_serve_until"`
	ToMustServeUntil   time.Time `json:"to_must_serve_until"`

	// publication_deadline only
	FromBasis string `json:"from_basis,omitempty"`
	ToBasis   string `json:"to_basis,omitempty"`

	// probe_verdict only
	DedupeKey          string    `json:"dedupe_key,omitempty"`
	ValidatorAddress   string    `json:"validator_address,omitempty"`
	ScheduledAt        time.Time `json:"scheduled_at,omitempty"`
	FromPhase          string    `json:"from_phase,omitempty"`
	ToPhase            string    `json:"to_phase,omitempty"`
	FromClassification string    `json:"from_classification,omitempty"`
	ToClassification   string    `json:"to_classification,omitempty"`
	PruneToleranceS    int64     `json:"prune_tolerance_s,omitempty"`

	Reason   string    `json:"reason"`
	JudgedAt time.Time `json:"judged_at"`
}

// The kinds of correction line.
const (
	CorrectionPublicationDeadline = "publication_deadline"
	CorrectionProbeVerdict        = "probe_verdict"
	// CorrectionRangeComplete closes a range: every deadline and verdict it
	// covers has been re-derived, so it stops withholding. It is a record
	// line rather than database-only state because a rebuild from the
	// export, and sentinel-recompute reading the same files, have to reach
	// the same holds as the live store. Written only after every other
	// correction for the range has landed.
	CorrectionRangeComplete = "range_corrected"
)

// CorrectionSchemaVersion versions corrections.jsonl.
const CorrectionSchemaVersion = 1

// UpsertParamUncertainty records one range, or latches the resolution of a
// range already on record.
//
// Everything but the resolution is written once. The resolution moves
// forward only: open to unresolvable to verified. unresolvable is not
// terminal, because a range the scanner's own node could not answer for is
// exactly the range an archive node can close later, and refusing that
// would make one pruned node a permanent hole in the record. verified is
// terminal: a range that has been read at every height cannot become
// unread, so nothing may move it back.
//
// The holds column is NOT the record's Holds() alone. It is that AND the
// corrections not having landed yet, because verifying a range and having
// applied what it proves are two different facts and only the second one
// releases a row. Re-ingesting a line for a range already corrected must
// not re-raise its hold, which is what the corrected_at term in the
// conflict clause is for.
func (s *Store) UpsertParamUncertainty(u scan.ParamUncertainty, raw []byte) (bool, error) {
	if u.ID == "" {
		return false, fmt.Errorf("param uncertainty without an id")
	}
	res, err := s.db.Exec(`INSERT INTO param_uncertainty
			(id, chain_id, kind, from_height, to_height, effective_from_height, interval_start_known,
			 direction, window_before_s, window_after_s, publications_affected, publications_affected_is_floor,
			 detected_at, to_time, last_error, holds, resolution, resolved_at, resolve_method, heights_read,
			 resolve_error, raw_json)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET
			holds          = (excluded.holds AND param_uncertainty.corrected_at IS NULL),
			resolution     = excluded.resolution,
			resolved_at    = excluded.resolved_at,
			resolve_method = excluded.resolve_method,
			heights_read   = excluded.heights_read,
			resolve_error  = excluded.resolve_error,
			raw_json       = excluded.raw_json
		WHERE param_uncertainty.resolution <> ? AND excluded.resolution <> ''`,
		u.ID, u.ChainID, u.Kind, u.FromHeight, u.ToHeight, u.EffectiveFromHeight, boolInt(u.IntervalStartKnown),
		u.Direction, u.WindowBeforeS, u.WindowAfterS, u.PublicationsAffected, boolInt(u.IsFloor),
		ts(u.DetectedAt), nullTime(u.ToTime), u.LastError, boolInt(u.Holds()), u.Resolution,
		nullTime(u.ResolvedAt), u.ResolveMethod, u.HeightsRead, u.ResolveError, string(raw),
		scan.ResolutionVerified)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// HoldingRanges is every range whose verdicts are still withheld: a silent
// params change whose corrections have not all landed. A verified range
// stays in this set until the corrector finishes with it — which is the
// whole point, since the corrector is what reads this set. Filtering
// verified ranges out of it, as the first cut did, left the corrector
// permanently empty while the rows it would have corrected were already
// released.
func (s *Store) HoldingRanges(ctx context.Context) ([]scan.ParamUncertainty, error) {
	return s.paramRanges(ctx, `WHERE holds = 1`)
}

// ParamRanges is every range on record, newest first, for the disclosure at
// /v1/meta.
func (s *Store) ParamRanges(ctx context.Context) ([]ParamRange, error) {
	return s.ParamRangeRows(ctx, `ORDER BY from_height DESC, to_height DESC`)
}

func (s *Store) paramRanges(ctx context.Context, clause string) ([]scan.ParamUncertainty, error) {
	rs, err := s.ParamRangeRows(ctx, clause)
	if err != nil {
		return nil, err
	}
	out := make([]scan.ParamUncertainty, 0, len(rs))
	for _, r := range rs {
		out = append(out, r.ParamUncertainty)
	}
	return out, nil
}

// ParamRange is a stored range together with the two facts the record
// cannot carry: whether it is still withholding, and when its corrections
// landed. Holds is the stored column, not the record's Holds(), because
// only the store knows whether the corrections have been applied.
type ParamRange struct {
	scan.ParamUncertainty
	Holds       bool
	CorrectedAt string
}

// ParamRangeRows is paramRanges with those two facts kept.
func (s *Store) ParamRangeRows(ctx context.Context, clause string) ([]ParamRange, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT raw_json, holds, COALESCE(corrected_at, '') FROM param_uncertainty `+clause)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ParamRange
	for rows.Next() {
		var raw string
		var holds int
		var corrected string
		if err := rows.Scan(&raw, &holds, &corrected); err != nil {
			return nil, err
		}
		var u scan.ParamUncertainty
		if err := json.Unmarshal([]byte(raw), &u); err != nil {
			return nil, fmt.Errorf("decode stored param uncertainty: %w", err)
		}
		out = append(out, ParamRange{ParamUncertainty: u, Holds: holds == 1, CorrectedAt: corrected})
	}
	return out, rows.Err()
}

// StaleDeadline is a probe row whose must_serve_until disagrees with its
// publication's. publications.must_serve_until only ever moves through
// ApplyPublicationCorrection, so disagreement means exactly one thing: this
// row was graded against a deadline this observer has since withdrawn.
//
// This is the invariant the whole mechanism rests on, and stating it as a
// property rather than as an event is what closes the case the range-based
// framing could not. The prober schedules from publications.jsonl, which is
// append-only and still carries the deadline the scanner stamped, so it
// keeps producing measurements against the old deadline for as long as the
// old window runs — hours after the range that corrected it was closed.
// "This range has been corrected" says nothing about those rows. "This
// row's deadline disagrees with its publication's" says everything, whenever
// the row arrived.
const StaleDeadline = `prb.must_serve_until <> (SELECT pb.must_serve_until FROM publications pb WHERE pb.promise_hash = prb.promise_hash)`

// SyncParamHolds recomputes which publications and probe rows have a
// deadline this observer cannot vouch for. It sets the flag where it
// belongs and clears it where it no longer does, in one idempotent pass, so
// the flag cannot drift and cannot stick.
//
// A probe row is held for either of two reasons, and the union matters:
// its publication sits in a range that still withholds, or its own deadline
// is stale. The second covers every row that arrives after its range
// closed, which the first cannot see at all.
//
// Both directions in one pass matters too: a publication can sit inside two
// overlapping ranges, which is what a crash between the record and the
// scan cursor produces, and clearing the flag when one of them closes
// would un-hold rows the other still covers.
func (s *Store) SyncParamHolds(ctx context.Context) (int64, error) {
	const held = `SELECT pb.promise_hash FROM publications pb
		JOIN param_uncertainty u ON u.holds = 1
		WHERE pb.promise_height - 1 <= u.to_height AND pb.settlement_height >= u.from_height`
	const rowHeld = `(promise_hash IN (SELECT promise_hash FROM publications WHERE retention_unverified = 1)
		OR EXISTS (SELECT 1 FROM probes prb WHERE prb.dedupe_key = probes.dedupe_key AND ` + StaleDeadline + `))`
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var changed int64
	for _, q := range []string{
		`UPDATE publications SET retention_unverified = 1 WHERE retention_unverified = 0 AND promise_hash IN (` + held + `)`,
		`UPDATE publications SET retention_unverified = 0 WHERE retention_unverified = 1 AND promise_hash NOT IN (` + held + `)`,
		`UPDATE probes SET retention_unverified = 1 WHERE retention_unverified = 0 AND ` + rowHeld,
		`UPDATE probes SET retention_unverified = 0 WHERE retention_unverified = 1 AND NOT ` + rowHeld,
	} {
		res, err := tx.ExecContext(ctx, q)
		if err != nil {
			return 0, err
		}
		n, _ := res.RowsAffected()
		changed += n
	}
	return changed, tx.Commit()
}

// StaleRow is one probe row graded against a deadline that has since been
// withdrawn, with the deadline its publication now carries and the range
// that moved it.
type StaleRow struct {
	ProbeRowForCorrection
	PromiseHash   string
	Deadline      time.Time
	UncertaintyID string
}

// StaleDeadlineRows is every row whose deadline disagrees with its
// publication's, whenever it arrived and whatever range corrected the
// publication. This is the corrector's standing work: a range closing does
// not stop rows arriving against the old deadline, so nothing keyed on a
// range can be the thing that finds them.
func (s *Store) StaleDeadlineRows(ctx context.Context, limit int) ([]StaleRow, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT prb.dedupe_key, prb.validator_address, prb.scheduled_at, prb.phase,
			prb.classification, prb.must_serve_until, prb.raw_json, prb.promise_hash,
			pb.must_serve_until,
			COALESCE((SELECT c.uncertainty_id FROM publication_corrections c
			          WHERE c.promise_hash = prb.promise_hash ORDER BY c.judged_at DESC LIMIT 1), '')
		FROM probes prb JOIN publications pb ON pb.promise_hash = prb.promise_hash
		WHERE `+StaleDeadline+`
		ORDER BY prb.started_at
		LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []StaleRow
	for rows.Next() {
		var r StaleRow
		var sched, msu, deadline string
		if err := rows.Scan(&r.DedupeKey, &r.ValidatorAddress, &sched, &r.Phase, &r.Classification,
			&msu, &r.RawJSON, &r.PromiseHash, &deadline, &r.UncertaintyID); err != nil {
			return nil, err
		}
		r.ScheduledAt, _ = time.Parse(TimeLayout, sched)
		r.MustServeUntil, _ = time.Parse(TimeLayout, msu)
		var err error
		if r.Deadline, err = time.Parse(TimeLayout, deadline); err != nil {
			continue // the publication's own deadline will not parse; nothing to grade against
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// HeldCounts is how much is currently withheld, for the disclosure.
func (s *Store) HeldCounts(ctx context.Context) (publications, probes, ranges int64, err error) {
	err = s.db.QueryRowContext(ctx, `SELECT
		(SELECT COUNT(*) FROM publications WHERE retention_unverified = 1),
		(SELECT COUNT(*) FROM probes       WHERE retention_unverified = 1),
		(SELECT COUNT(*) FROM param_uncertainty WHERE holds = 1)`).Scan(&publications, &probes, &ranges)
	return
}

// PublicationInRange is one publication a verified range covers, with what
// a correction needs to re-derive its deadline.
type PublicationInRange struct {
	PromiseHash       string
	PromiseHeight     int64
	SettlementHeight  int64
	SettlementTxIndex int
	CreationTimestamp time.Time
	MustServeUntil    time.Time
	Basis             string
	// AsStamped is what the scanner stamped: must_serve_until_at_scan when
	// a correction has moved the deadline, the deadline itself otherwise.
	AsStamped time.Time
	// Unreadable is set when the row's own timestamps will not parse, so it
	// cannot be re-derived. Reported rather than dropped: a publication the
	// corrector cannot reach keeps its range withholding.
	Unreadable bool
}

// PublicationsCoveredBy returns every publication whose upload interval
// overlaps a range.
//
// It deliberately does not skip a publication that already carries a
// correction for this range. A publication's deadline is corrected before
// its rows are re-graded, so a crash or an I/O error between the two leaves
// the deadline moved and the rows untouched; skipping on the deadline
// correction alone made that state permanent, because no later pass would
// look at the publication again. Each row carries its own correction key
// and is skipped individually, and the publication correction is idempotent
// on (promise_hash, uncertainty_id).
//
// AsStamped is must_serve_until_at_scan, NULL until a correction moved it.
// The clamp has to be drawn against that rather than against the current
// value, or a second pass would compare a corrected deadline with itself,
// find no change, and return before reaching the rows.
func (s *Store) PublicationsCoveredBy(ctx context.Context, u scan.ParamUncertainty) ([]PublicationInRange, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT promise_hash, promise_height, settlement_height, settlement_tx_index,
			creation_timestamp, must_serve_until, must_serve_until_basis,
			COALESCE(must_serve_until_at_scan, must_serve_until)
		FROM publications
		WHERE promise_height - 1 <= ? AND settlement_height >= ?
		ORDER BY settlement_height`, u.ToHeight, u.FromHeight)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PublicationInRange
	for rows.Next() {
		var p PublicationInRange
		var created, msu, stamped string
		if err := rows.Scan(&p.PromiseHash, &p.PromiseHeight, &p.SettlementHeight, &p.SettlementTxIndex, &created, &msu, &p.Basis, &stamped); err != nil {
			return nil, err
		}
		if p.CreationTimestamp, err = time.Parse(TimeLayout, created); err != nil {
			p.Unreadable = true
		}
		if p.MustServeUntil, err = time.Parse(TimeLayout, msu); err != nil {
			p.Unreadable = true
		}
		if p.AsStamped, err = time.Parse(TimeLayout, stamped); err != nil {
			p.AsStamped = p.MustServeUntil
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ProbeRowForCorrection is one stored probe row rebuilt far enough to be
// re-graded under a moved deadline.
type ProbeRowForCorrection struct {
	DedupeKey        string
	ValidatorAddress string
	ScheduledAt      time.Time
	Phase            string
	Classification   string
	MustServeUntil   time.Time
	RawJSON          string
}

// ProbeRowsOf returns the probe rows of one publication that have not been
// corrected for this range yet.
func (s *Store) ProbeRowsOf(ctx context.Context, promiseHash, uncertaintyID string) ([]ProbeRowForCorrection, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT dedupe_key, validator_address, scheduled_at, phase, classification, must_serve_until, raw_json
		FROM probes
		WHERE promise_hash = ?
		  AND dedupe_key NOT IN (SELECT dedupe_key FROM probe_corrections WHERE uncertainty_id = ?)
		ORDER BY scheduled_at`, promiseHash, uncertaintyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ProbeRowForCorrection
	for rows.Next() {
		var r ProbeRowForCorrection
		var sched, msu string
		if err := rows.Scan(&r.DedupeKey, &r.ValidatorAddress, &sched, &r.Phase, &r.Classification, &msu, &r.RawJSON); err != nil {
			return nil, err
		}
		r.ScheduledAt, _ = time.Parse(TimeLayout, sched)
		r.MustServeUntil, _ = time.Parse(TimeLayout, msu)
		out = append(out, r)
	}
	return out, rows.Err()
}

// ApplyPublicationCorrection moves one publication's deadline to the one a
// verified range supports and logs the move. The deadline it was stamped
// with is kept beside the corrected one; NULL in must_serve_until_at_scan
// means uncorrected, never "the same as".
func (s *Store) ApplyPublicationCorrection(c Correction) (bool, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`UPDATE publications SET
			must_serve_until_at_scan       = COALESCE(must_serve_until_at_scan, must_serve_until),
			must_serve_until_basis_at_scan = COALESCE(must_serve_until_basis_at_scan, must_serve_until_basis),
			must_serve_until               = ?,
			must_serve_until_basis         = ?,
			corrected_at                   = ?
		WHERE promise_hash = ?`, ts(c.ToMustServeUntil), c.ToBasis, ts(c.JudgedAt), c.PromiseHash)
	if err != nil {
		return false, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return false, ErrNoSuchRow
	}
	if _, err := tx.Exec(`INSERT INTO publication_corrections
			(promise_hash, uncertainty_id, from_must_serve_until, to_must_serve_until, from_basis, to_basis, reason, judged_at)
		VALUES (?,?,?,?,?,?,?,?) ON CONFLICT(promise_hash, uncertainty_id) DO NOTHING`,
		c.PromiseHash, c.UncertaintyID, ts(c.FromMustServeUntil), ts(c.ToMustServeUntil), c.FromBasis, c.ToBasis, c.Reason, ts(c.JudgedAt)); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

// ApplyProbeCorrection moves one row's phase and verdict to the ones a
// verified range supports, keeping what it was stamped with beside them.
//
// The latch is corrected_at, not amended_at: a row can carry both a late
// shadow amendment and a deadline correction, and probe_amendments' single
// dedupe_key primary key cannot hold two. That is why this has its own
// table, keyed on (dedupe_key, uncertainty_id).
func (s *Store) ApplyProbeCorrection(c Correction) (bool, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`UPDATE probes SET
			must_serve_until_at_probe = COALESCE(must_serve_until_at_probe, must_serve_until),
			phase_at_probe            = COALESCE(phase_at_probe, phase),
			classification_at_probe   = COALESCE(classification_at_probe, classification),
			must_serve_until          = ?,
			phase                     = ?,
			classification            = ?,
			classification_reason     = ?,
			corrected_at              = ?
		WHERE dedupe_key = ?`, ts(c.ToMustServeUntil), c.ToPhase, c.ToClassification, c.Reason, ts(c.JudgedAt), c.DedupeKey)
	if err != nil {
		return false, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return false, ErrNoSuchRow
	}
	if _, err := tx.Exec(`INSERT INTO probe_corrections
			(dedupe_key, uncertainty_id, promise_hash, validator_address, scheduled_at, from_phase, to_phase,
			 from_classification, to_classification, from_must_serve_until, to_must_serve_until, reason, prune_tolerance_s, judged_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(dedupe_key, uncertainty_id) DO NOTHING`,
		c.DedupeKey, c.UncertaintyID, c.PromiseHash, c.ValidatorAddress, ts(c.ScheduledAt), c.FromPhase, c.ToPhase,
		c.FromClassification, c.ToClassification, ts(c.FromMustServeUntil), ts(c.ToMustServeUntil), c.Reason, c.PruneToleranceS, ts(c.JudgedAt)); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

// MarkRangeCorrected records that every publication a verified range covers
// has been re-derived against it, so the range stops holding.
func (s *Store) MarkRangeCorrected(ctx context.Context, id string, at time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE param_uncertainty
		SET corrected_at = COALESCE(corrected_at, ?), holds = 0
		WHERE id = ? AND resolution = ?`, ts(at), id, scan.ResolutionVerified)
	return err
}

// CorrectedRanges is every range whose corrections have landed, for the
// derivation a rebuild and sentinel-recompute run from the record.
func (s *Store) CorrectedRanges(ctx context.Context) (map[string]bool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM param_uncertainty WHERE corrected_at IS NOT NULL`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// OpenDaysWithHeldPromises reports whether any promise settled in [lo, hi]
// is still held, which is what stops a day being rolled and then pruned
// while a correction could still reach its rows.
func (s *Store) HeldPromisesSettledBetween(ctx context.Context, lo, hi string) (int64, error) {
	var n int64
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM publications
		WHERE retention_unverified = 1 AND settlement_time >= ? AND settlement_time <= ?`, lo, hi).Scan(&n)
	return n, err
}

// ParamHistory is the scanner's param history as the collector last
// ingested it from state.json. A correction re-derives a deadline against
// this plus the values a verified range proved, which is the same pair
// sentinel-recompute works from, so the two cannot reach different answers.
func (s *Store) ParamHistory(ctx context.Context) ([]scan.ParamEntry, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT effective_from_height, effective_from_tx_index, source,
			withdrawal_delay_s, payment_promise_timeout_s, payment_promise_height_window,
			shard_retention_s, full_stake_storage_budget
		FROM params_history ORDER BY effective_from_height, effective_from_tx_index`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []scan.ParamEntry
	for rows.Next() {
		var e scan.ParamEntry
		var wd, timeout, retention int64
		var window, budget uint64
		if err := rows.Scan(&e.FromHeight, &e.FromTxIndex, &e.Source, &wd, &timeout, &window, &retention, &budget); err != nil {
			return nil, err
		}
		e.ParamsJSON = scan.ParamsSnapshot{
			WithdrawalDelaySeconds:       wd,
			PaymentPromiseTimeoutSeconds: timeout,
			PaymentPromiseHeightWindow:   window,
			ShardRetentionSeconds:        retention,
			FullStakeStorageBudget:       budget,
			EffectiveFromHeight:          e.FromHeight,
			EffectiveFromTxIndex:         e.FromTxIndex,
			Source:                       e.Source,
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func nullTime(t *time.Time) any {
	if t == nil || t.IsZero() {
		return nil
	}
	return ts(*t)
}
