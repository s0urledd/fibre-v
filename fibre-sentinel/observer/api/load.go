package api

import (
	"context"
	"time"

	"github.com/plsgiveup/fibre/fibre-sentinel/observer/store"
)

// Load is what the protocol asked of a validator: the rows of every settled
// blob its stake assigned it, which it has to receive from the publisher and
// keep for the retention window. Every figure is computed from the chain's
// own records (MsgPayForFibre and the assignment fibre-assign derives from
// the validator set at the promise height), nothing measured, so anyone can
// recompute it. Bytes are row data: a row is blob_size / original_rows,
// without the proofs and framing that travel with it.
//
// As with endorsement, an assignment that settled while the validator had
// no Fibre host is left out: it could not have received those rows.
type loadStats struct {
	// Promises and Rows are the settled promises in the window that assigned
	// this validator rows while it had a host, and the rows they assigned.
	Promises int64 `json:"promises"`
	Rows     int64 `json:"rows"`
	// Bytes is the row data those promises asked it to receive and store.
	Bytes int64 `json:"bytes"`
	// StoredBytes is the row data it has to hold right now: assignments whose
	// retention window has not ended, whatever the selected period.
	StoredBytes int64 `json:"stored_bytes"`
	// RowsPerBlob is its assignment on the newest settled promise; rows follow
	// stake, not blob size, so it is the same for every blob of that set.
	RowsPerBlob int64 `json:"rows_per_blob"`
}

// rowBytesSQL is the row data one assigned row carries: blob_size over the
// promise's original rows, as recorded with its assignment.
const rowBytesSQL = `(p.blob_size * 1.0 / NULLIF(json_extract(p.raw_json, '$.assignment.protocol_params.original_rows'), 0))`

// loadByValidator computes loadStats per validator over win, for one
// validator when only is set.
func (s *Server) loadByValidator(ctx context.Context, win Window, only string) (map[string]loadStats, error) {
	db := s.st.DB()
	filter, args := "", []any{win.startArg(), win.endArg()}
	if only != "" {
		filter = ` AND a.validator_address = ?`
		args = append(args, only)
	}
	out := map[string]loadStats{}
	rows, err := db.QueryContext(ctx, `SELECT a.validator_address, COUNT(*), COALESCE(SUM(a.row_count), 0),
			COALESCE(CAST(SUM(a.row_count * `+rowBytesSQL+`) AS INTEGER), 0)
		FROM assignments a JOIN publications p ON p.promise_hash = a.promise_hash
		WHERE `+signingPopulation+` AND a.row_count > 0 AND a.host_at_settlement IS NOT ''`+filter+`
		GROUP BY a.validator_address`, args...)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var addr string
		var l loadStats
		if err := rows.Scan(&addr, &l.Promises, &l.Rows, &l.Bytes); err != nil {
			rows.Close()
			return nil, err
		}
		out[addr] = l
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}

	// held now: retention not over, whatever the window
	nowArgs := []any{store.TS(time.Now().UTC())}
	if only != "" {
		nowArgs = append(nowArgs, only)
	}
	rows, err = db.QueryContext(ctx, `SELECT a.validator_address,
			COALESCE(CAST(SUM(a.row_count * `+rowBytesSQL+`) AS INTEGER), 0)
		FROM assignments a JOIN publications p ON p.promise_hash = a.promise_hash
		WHERE p.settlement_tx_code = 0 AND p.assignment_error = '' AND p.must_serve_until > ?
		  AND a.row_count > 0 AND a.host_at_settlement IS NOT ''`+filter+`
		GROUP BY a.validator_address`, nowArgs...)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var addr string
		var b int64
		if err := rows.Scan(&addr, &b); err != nil {
			rows.Close()
			return nil, err
		}
		l := out[addr]
		l.StoredBytes = b
		out[addr] = l
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}

	// rows on the newest settled promise
	var newest string
	if err := db.QueryRowContext(ctx, `SELECT promise_hash FROM publications
		WHERE settlement_tx_code = 0 AND assignment_error = ''
		ORDER BY settlement_height DESC, settlement_tx_index DESC LIMIT 1`).Scan(&newest); err != nil {
		return out, nil // nothing settled yet
	}
	lastArgs := []any{newest}
	lastFilter := ""
	if only != "" {
		lastFilter = ` AND a.validator_address = ?`
		lastArgs = append(lastArgs, only)
	}
	rows, err = db.QueryContext(ctx, `SELECT a.validator_address, a.row_count FROM assignments a
		WHERE a.promise_hash = ? AND a.row_count > 0`+lastFilter, lastArgs...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var addr string
		var n int64
		if err := rows.Scan(&addr, &n); err != nil {
			return nil, err
		}
		l := out[addr]
		l.RowsPerBlob = n
		out[addr] = l
	}
	return out, rows.Err()
}

// fillLoad sets Load on every row validatorRows built.
func (s *Server) fillLoad(ctx context.Context, win Window, only string, byAddr map[string]*validatorRow) error {
	load, err := s.loadByValidator(ctx, win, only)
	if err != nil {
		return err
	}
	for addr, v := range byAddr {
		v.Load = load[addr]
	}
	return nil
}
