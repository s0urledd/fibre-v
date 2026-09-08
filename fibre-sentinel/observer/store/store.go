// Package store is the observer's relational store: SQLite for the single-node
// MVP, with a schema kept inside the subset SQLite and Postgres share.
//
// The store never computes verdicts. It keeps the raw records the sentinel
// tools already produce (publications.jsonl, measurements.jsonl), plus the
// facts the dashboard needs that those files do not carry: endpoint history,
// observer run spans (for gaps), and ingest cursors.
package store

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/plsgiveup/fibre/fibre-sentinel/internal/probe"
	"github.com/plsgiveup/fibre/fibre-sentinel/internal/scan"
)

//go:embed schema.sql
var schemaSQL string

// SchemaVersion is bumped whenever schema.sql changes shape.
const SchemaVersion = 1

// Store wraps one SQLite database.
type Store struct {
	db *sql.DB
}

// Open opens or creates the SQLite database at path, applies the pragmas the
// observer relies on (WAL, busy timeout) and the schema. ":memory:" is
// accepted for tests.
func Open(path string) (*Store, error) {
	dsn := path
	if path != ":memory:" {
		dsn = "file:" + path + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(ON)"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	// One writer at a time: SQLite serialises writers anyway, and a single
	// connection avoids "database is locked" between our own goroutines.
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// DB exposes the underlying handle for read-only queries (the API).
func (s *Store) DB() *sql.DB { return s.db }

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate() error {
	// Drop SQL comments before splitting on ";" so a semicolon inside a
	// comment cannot cut a statement in half.
	var sb strings.Builder
	for _, line := range strings.Split(schemaSQL, "\n") {
		if i := strings.Index(line, "--"); i >= 0 {
			line = line[:i]
		}
		sb.WriteString(line)
		sb.WriteByte('\n')
	}
	for _, stmt := range strings.Split(sb.String(), ";") {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" {
			continue
		}
		if _, err := s.db.Exec(stmt); err != nil {
			return fmt.Errorf("schema: %w\n%s", err, stmt)
		}
	}
	_, err := s.db.Exec(`INSERT OR IGNORE INTO schema_migrations (version, applied_at) VALUES (?, ?)`,
		SchemaVersion, ts(time.Now()))
	return err
}

func ts(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// ---- observer runs (gap tracking) ----

// StartRun records a process start and returns the run id.
func (s *Store) StartRun(component, vantage, version string, now time.Time) (int64, error) {
	res, err := s.db.Exec(`INSERT INTO observer_runs (component, vantage, version, started_at, last_heartbeat_at)
		VALUES (?, ?, ?, ?, ?)`, component, vantage, version, ts(now), ts(now))
	if err != nil {
		return 0, fmt.Errorf("start run: %w", err)
	}
	return res.LastInsertId()
}

// Heartbeat extends a run's observed span.
func (s *Store) Heartbeat(runID int64, now time.Time) error {
	_, err := s.db.Exec(`UPDATE observer_runs SET last_heartbeat_at = ? WHERE id = ?`, ts(now), runID)
	return err
}

// StopRun closes a run cleanly. A run without stopped_at whose heartbeat is
// stale is a crash; the dashboard treats the span after the last heartbeat as
// a gap either way.
func (s *Store) StopRun(runID int64, now time.Time, reason string) error {
	_, err := s.db.Exec(`UPDATE observer_runs SET stopped_at = ?, last_heartbeat_at = ?, stop_reason = ? WHERE id = ?`,
		ts(now), ts(now), reason, runID)
	return err
}

// ---- ingest cursors ----

// Cursor returns the byte offset and line number already ingested for file.
func (s *Store) Cursor(file string) (offset, line int64, err error) {
	err = s.db.QueryRow(`SELECT byte_offset, line_no FROM ingest_cursors WHERE file = ?`, file).Scan(&offset, &line)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, 0, nil
	}
	return offset, line, err
}

// SetCursor persists the ingest position for file.
func (s *Store) SetCursor(file string, offset, line int64, now time.Time) error {
	_, err := s.db.Exec(`INSERT INTO ingest_cursors (file, byte_offset, line_no, updated_at) VALUES (?, ?, ?, ?)
		ON CONFLICT(file) DO UPDATE SET byte_offset = excluded.byte_offset, line_no = excluded.line_no, updated_at = excluded.updated_at`,
		file, offset, line, ts(now))
	return err
}

// ---- meta ----

// SetMeta writes one key.
func (s *Store) SetMeta(key, value string, now time.Time) error {
	_, err := s.db.Exec(`INSERT INTO meta (key, value, updated_at) VALUES (?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`, key, value, ts(now))
	return err
}

// Meta reads one key ("" if absent).
func (s *Store) Meta(key string) (string, error) {
	var v string
	err := s.db.QueryRow(`SELECT value FROM meta WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, err
}

// ---- params history ----

// UpsertParams stores the scanner's param history. Idempotent.
func (s *Store) UpsertParams(entries []scan.ParamEntry) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, e := range entries {
		p := e.ParamsJSON
		if _, err := tx.Exec(`INSERT INTO params_history
			(effective_from_height, effective_from_tx_index, source, withdrawal_delay_s, payment_promise_timeout_s,
			 payment_promise_height_window, shard_retention_s, full_stake_storage_budget)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(effective_from_height, effective_from_tx_index) DO NOTHING`,
			e.FromHeight, e.FromTxIndex, e.Source, p.WithdrawalDelaySeconds, p.PaymentPromiseTimeoutSeconds,
			p.PaymentPromiseHeightWindow, p.ShardRetentionSeconds, p.FullStakeStorageBudget); err != nil {
			return fmt.Errorf("params h=%d: %w", e.FromHeight, err)
		}
	}
	return tx.Commit()
}

// ---- publications and assignments ----

// UpsertPublication stores one scanner record and its per-validator
// assignment rows in one transaction. raw is the JSONL line as read from the
// file; it is kept verbatim for provenance. Re-inserting the same promise
// hash is a no-op.
func (s *Store) UpsertPublication(p scan.Publication, raw []byte) (inserted bool, err error) {
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	a := p.Assignment
	res, err := tx.Exec(`INSERT INTO publications
		(promise_hash, commitment, blob_version, blob_size, namespace, chain_id, promise_height, creation_timestamp,
		 signer, signer_public_key, validator_signature_count, settlement_height, settlement_time, settlement_tx_hash,
		 settlement_tx_index, settlement_tx_code, must_serve_until, must_serve_until_basis, shard_retention_s,
		 payment_promise_timeout_s, assignment_error, protocol_params_fingerprint, pinned_celestia_app,
		 validator_set_height, total_voting_power, sigma_rows, distinct_rows, wrap_overlaps, validators_with_rows,
		 recorded_at, raw_json)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(promise_hash) DO NOTHING`,
		p.PromiseHash, p.Promise.Commitment, p.Promise.BlobVersion, p.Promise.BlobSize, p.Promise.Namespace,
		p.Promise.ChainID, p.Promise.Height, ts(p.Promise.CreationTimestamp),
		p.Signer, p.Promise.SignerPublicKey, p.ValidatorSignatureCount, p.SettlementHeight, ts(p.SettlementTime),
		p.SettlementTxHash, p.SettlementTxIndex, p.SettlementTxCode, ts(p.MustServeUntil), p.MustServeUntilBasis,
		p.ParamsAtPublication.ShardRetentionSeconds, p.ParamsAtPublication.PaymentPromiseTimeoutSeconds,
		a.Error, a.ProtocolParams.Fingerprint, a.ProtocolParams.PinnedCelestiaApp,
		a.ValidatorSetHeight, a.TotalVotingPower, a.Sigma, a.Distinct, a.WrapOverlaps, a.ValidatorsWithRows,
		ts(p.RecordedAt), string(raw))
	if err != nil {
		return false, fmt.Errorf("publication %s: %w", p.PromiseHash, err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return false, tx.Commit()
	}
	for _, v := range a.Validators {
		var rowsJSON any
		if v.Rows != nil {
			b, err := json.Marshal(v.Rows)
			if err != nil {
				return false, err
			}
			rowsJSON = string(b)
		}
		if _, err := tx.Exec(`INSERT INTO assignments (promise_hash, validator_address, voting_power, row_count, rows_json)
			VALUES (?, ?, ?, ?, ?) ON CONFLICT(promise_hash, validator_address) DO NOTHING`,
			p.PromiseHash, v.Address, v.VotingPower, v.RowCount, rowsJSON); err != nil {
			return false, fmt.Errorf("assignment %s/%s: %w", p.PromiseHash, v.Address, err)
		}
	}
	return true, tx.Commit()
}

// ---- probes ----

// InsertProbe stores one prober measurement. Re-inserting the same dedupe key
// is a no-op, so a file can be re-ingested safely.
func (s *Store) InsertProbe(m probe.Measurement, raw []byte) (inserted bool, err error) {
	res, err := s.db.Exec(`INSERT INTO probes
		(dedupe_key, vantage, promise_hash, commitment, blob_version, must_serve_until, validator_set_height,
		 validator_address, validator_host, assigned, assigned_row_count, schedule_label, scheduled_at, started_at,
		 finished_at, lateness_ms, dns_ok, dns_ms, tcp_ok, tcp_ms, tls_ok, tls_ms, tls_version, peer_cert_sha256,
		 identity_ok, identity_reason, download_ok, download_ms, rows_returned, rows_expected, commitment_verified,
		 assignment_verified, phase, outcome, classification, classification_reason, raw_error, total_duration_ms, raw_json)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(dedupe_key) DO NOTHING`,
		m.DedupeKey(), m.Vantage, m.PromiseHash, m.Commitment, m.BlobVersion, ts(m.MustServeUntil), m.ValidatorSetHeight,
		m.ValidatorAddress, m.ValidatorHost, b2i(m.Assigned), m.AssignedRowCount, m.ScheduleLabel, ts(m.ScheduledAt),
		ts(m.StartedAt), ts(m.FinishedAt), m.LatenessMS,
		b2i(m.DNS.OK), m.DNS.DurationMS, b2i(m.TCP.OK), m.TCP.DurationMS, b2i(m.TLS.OK), m.TLS.DurationMS,
		m.TLS.Version, m.TLS.PeerCertSHA256, b2i(m.Identity.OK), m.Identity.Reason,
		b2i(m.Download.OK), m.Download.DurationMS, m.Download.RowsReturned, m.Download.RowsExpected,
		b2i(m.Download.CommitmentVerified), b2i(m.Download.AssignmentVerified),
		string(m.Phase), string(m.Outcome), string(m.Classification), m.ClassificationReason, m.RawError,
		m.TotalDurationMS, string(raw))
	if err != nil {
		return false, fmt.Errorf("probe %s: %w", m.DedupeKey(), err)
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// ---- endpoints ----

// ObserveEndpoints reconciles one AllBondedFibreProviders snapshot with the
// endpoint history: unseen (validator, host) pairs open a row, pairs seen
// again extend last_seen, open rows missing from the snapshot are closed.
// Returns how many rows were opened and closed.
func (s *Store) ObserveEndpoints(ctx context.Context, providers []scan.FibreProvider, height int64, now time.Time) (opened, closed int, err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback()

	type key struct{ addr, host string }
	open := map[key]int64{}
	rows, err := tx.Query(`SELECT id, validator_cons_address, host FROM endpoints WHERE closed_at IS NULL`)
	if err != nil {
		return 0, 0, err
	}
	for rows.Next() {
		var id int64
		var k key
		if err := rows.Scan(&id, &k.addr, &k.host); err != nil {
			rows.Close()
			return 0, 0, err
		}
		open[k] = id
	}
	rows.Close()

	seen := map[key]bool{}
	for _, p := range providers {
		k := key{p.ConsAddressBech32, p.Host}
		seen[k] = true
		if id, ok := open[k]; ok {
			if _, err := tx.Exec(`UPDATE endpoints SET last_seen_at = ?, last_seen_height = ? WHERE id = ?`, ts(now), height, id); err != nil {
				return 0, 0, err
			}
			continue
		}
		if _, err := tx.Exec(`INSERT INTO endpoints (validator_cons_address, host, first_seen_at, first_seen_height, last_seen_at, last_seen_height)
			VALUES (?, ?, ?, ?, ?, ?)`, k.addr, k.host, ts(now), height, ts(now), height); err != nil {
			return 0, 0, err
		}
		opened++
	}
	for k, id := range open {
		if seen[k] {
			continue
		}
		if _, err := tx.Exec(`UPDATE endpoints SET closed_at = ?, closed_height = ? WHERE id = ?`, ts(now), height, id); err != nil {
			return 0, 0, err
		}
		closed++
	}
	return opened, closed, tx.Commit()
}

// Endpoint is one open or closed endpoint-history row.
type Endpoint struct {
	ID                   int64
	ValidatorConsAddress string
	Host                 string
	FirstSeenAt          string
	FirstSeenHeight      int64
	LastSeenAt           string
	LastSeenHeight       int64
	ClosedAt             *string
}

// CurrentEndpoints lists open endpoint rows.
func (s *Store) CurrentEndpoints(ctx context.Context) ([]Endpoint, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, validator_cons_address, host, first_seen_at, first_seen_height, last_seen_at, last_seen_height, closed_at
		FROM endpoints WHERE closed_at IS NULL ORDER BY validator_cons_address`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Endpoint
	for rows.Next() {
		var e Endpoint
		if err := rows.Scan(&e.ID, &e.ValidatorConsAddress, &e.Host, &e.FirstSeenAt, &e.FirstSeenHeight, &e.LastSeenAt, &e.LastSeenHeight, &e.ClosedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// Counts is a quick health summary used by tests and the collector log.
type Counts struct {
	Publications, Assignments, Probes, OpenEndpoints, Runs int64
}

// Count returns row counts of the main tables.
func (s *Store) Count(ctx context.Context) (Counts, error) {
	var c Counts
	q := func(dst *int64, sqlText string) error { return s.db.QueryRowContext(ctx, sqlText).Scan(dst) }
	if err := q(&c.Publications, `SELECT COUNT(*) FROM publications`); err != nil {
		return c, err
	}
	if err := q(&c.Assignments, `SELECT COUNT(*) FROM assignments`); err != nil {
		return c, err
	}
	if err := q(&c.Probes, `SELECT COUNT(*) FROM probes`); err != nil {
		return c, err
	}
	if err := q(&c.OpenEndpoints, `SELECT COUNT(*) FROM endpoints WHERE closed_at IS NULL`); err != nil {
		return c, err
	}
	if err := q(&c.Runs, `SELECT COUNT(*) FROM observer_runs`); err != nil {
		return c, err
	}
	return c, nil
}
