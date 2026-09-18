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
	"os"
	"runtime"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/plsgiveup/fibre/fibre-sentinel/internal/probe"
	"github.com/plsgiveup/fibre/fibre-sentinel/internal/scan"
)

//go:embed schema.sql
var schemaSQL string

// SchemaVersion is the schema this binary expects. schema.sql is the frozen
// version-1 baseline; every later version is a numbered entry in migrations,
// applied in order. A fresh database therefore takes exactly the same path as
// an upgraded one — baseline, then every migration — so the two end up
// identical in shape and the migration code is exercised by every test run
// rather than only on upgrade day.
const SchemaVersion = 9

// migration is one numbered step above the baseline. The statements run in a
// single transaction: SQLite supports transactional DDL, so a failed step
// leaves the database at the previous version rather than half-migrated.
type migration struct {
	version int
	note    string
	stmts   []string
}

// migrations must stay append-only and in ascending order. Never edit a
// released entry: a database that already applied it will not re-run it.
var migrations = []migration{
	{
		version: 2,
		note:    "verified attestation: which validators a settled promise proves stored the blob",
		stmts: []string{
			// Nullable on purpose. A row written before this migration, or
			// ingested from a record whose schema_version predates the
			// attestation field, has no attestation evidence either way.
			// NULL is "unknown"; 0 would claim the validator did not attest,
			// which the record does not say.
			`ALTER TABLE assignments ADD COLUMN attested INTEGER`,
			`ALTER TABLE probes ADD COLUMN attested INTEGER`,
			`ALTER TABLE publications ADD COLUMN attested_with_rows INTEGER`,
			`ALTER TABLE publications ADD COLUMN attested_voting_power INTEGER`,
			`ALTER TABLE publications ADD COLUMN signature_entries INTEGER`,
			`ALTER TABLE publications ADD COLUMN signatures_verified INTEGER`,
			`ALTER TABLE publications ADD COLUMN signatures_unmatched INTEGER`,
			`ALTER TABLE publications ADD COLUMN signatures_out_of_position INTEGER`,
			`CREATE INDEX IF NOT EXISTS assignments_attested ON assignments (promise_hash, attested)`,
		},
	},
	{
		version: 3,
		note:    "endpoint rows say why they closed: the observer cannot tell deregistration from unbonding",
		stmts: []string{
			// The only signal behind a closure is that the (validator, host)
			// pair stopped appearing in AllBondedFibreProviders. That happens
			// on every jailing and every unbonding without the operator
			// touching its Fibre registration, and x/valaddr has no
			// deregistration message at all, so "closed" never meant
			// "deregistered". The column says what was actually observed.
			`ALTER TABLE endpoints ADD COLUMN closed_reason TEXT`,
			`UPDATE endpoints SET closed_reason = 'left_bonded_provider_list' WHERE closed_at IS NOT NULL AND closed_reason IS NULL`,
		},
	},
	{
		version: 4,
		note:    "validator identities from the staking module, so rows carry the name the operator chose",
		stmts: []string{
			// Read from the chain's own staking module, not from an explorer
			// API: an observer whose validator names come from somebody
			// else's index is that much less independent, and it inherits
			// that index's rate limits, terms and coverage gaps.
			//
			// The key is the 20-byte consensus address in lower-case hex,
			// the same identifier every probe row and assignment already
			// uses, so no join needs a bech32 conversion.
			`CREATE TABLE IF NOT EXISTS validator_identities (
				cons_address     TEXT PRIMARY KEY,
				operator_address TEXT NOT NULL DEFAULT '',
				moniker          TEXT NOT NULL DEFAULT '',
				identity         TEXT NOT NULL DEFAULT '',
				website          TEXT NOT NULL DEFAULT '',
				tokens           TEXT NOT NULL DEFAULT '',
				jailed           INTEGER NOT NULL DEFAULT 0,
				status           TEXT NOT NULL DEFAULT '',
				first_seen_at    TEXT NOT NULL,
				updated_at       TEXT NOT NULL
			)`,
			`CREATE INDEX IF NOT EXISTS validator_identities_moniker ON validator_identities (moniker)`,
		},
	},
	{
		version: 5,
		note:    "covering index for the in-window window aggregates the network summary runs",
		stmts: []string{
			// Every rate on /v1/network is an aggregate over the same
			// population — probes of an assigned shard, in window, since a
			// timestamp — and there were five separate scans of it per
			// request with no index to seek by. Measured on a store with
			// 2,200 publications and 714,000 probes, /v1/network?window=7d
			// took 27.9s.
			//
			// The leading columns are the two equalities and the range, in
			// that order, so SQLite can seek instead of scanning the table.
			// The rest are there so it never has to: every column these
			// aggregates read is in the index, which is what turns a scan of
			// forty-column rows into a scan of the index alone. Measured on
			// the same store: attestation 1.63s to 0.20s, the per-point
			// breakdown 2.37s to 0.89s, the correlated-failure guard 2.41s
			// to 0.88s, the verdict tally 1.72s to 0.50s.
			//
			// It costs about 200 bytes a probe. `outcome` is deliberately not
			// in it: it is the widest column none of these queries reads, and
			// the one query that does read it seeks by promise_hash instead.
			`CREATE INDEX IF NOT EXISTS probes_window ON probes
				(assigned, phase, started_at, classification, schedule_label,
				 attested, validator_address, promise_hash, scheduled_at)`,
		},
	},
	{
		version: 6,
		note:    "the same covering index led by validator, for the per-validator page",
		stmts: []string{
			// probes_window above leads with the two equalities and the range,
			// which is right for the network aggregates and wrong for a page
			// about one validator: validator_address sits seventh, so a query
			// for one validator seeks to the start of the window and then walks
			// every in-window probe of every validator, discarding all but its
			// own. On an 85,000-probe store that made /v1/validators/{addr}
			// 1.4s, and it is the page an operator opens about themselves.
			//
			// This is the same column set led by validator_address. Measured on
			// that store: the per-validator class tally 13.2ms to 0.4ms and the
			// obligation rate 10.8ms to 0.9ms, with the detail page computing
			// eight of them (four windows, two queries each).
			//
			// probes_validator_time is kept: it orders by started_at directly,
			// which this one cannot, and the recent-probes list needs that.
			`CREATE INDEX IF NOT EXISTS probes_validator_window ON probes
				(validator_address, assigned, phase, started_at, classification,
				 schedule_label, attested, promise_hash, scheduled_at)`,
		},
	},
	{
		version: 7,
		note:    "the escrow side of x/fibre: who paid what, and which promises were abandoned",
		stmts: []string{
			// One row per escrow movement the chain recorded: a settlement
			// (MsgPayForFibre), a timeout (MsgPaymentPromiseTimeout, the
			// same charge, submitted by whoever held the abandoned promise),
			// a deposit, a withdrawal request and a withdrawal payout. The
			// amount of a settlement or timeout is not in any chain event; it
			// is recomputed from the promise's blob_size with the module's
			// own gas formula, which is exactly what the module charges.
			//
			// publisher is the account the module charged (derived from the
			// promise's signer key), whoever broadcast the transaction.
			// processor is that broadcaster: the publisher for a settlement,
			// anyone for a timeout.
			`CREATE TABLE IF NOT EXISTS payments (
				dedupe_key    TEXT PRIMARY KEY,
				kind          TEXT NOT NULL,
				height        INTEGER NOT NULL,
				time          TEXT NOT NULL,
				tx_hash       TEXT NOT NULL DEFAULT '',
				tx_index      INTEGER NOT NULL DEFAULT -1,
				msg_index     INTEGER NOT NULL DEFAULT 0,
				publisher     TEXT NOT NULL,
				processor     TEXT NOT NULL DEFAULT '',
				promise_hash  TEXT NOT NULL DEFAULT '',
				namespace     TEXT NOT NULL DEFAULT '',
				blob_size     INTEGER NOT NULL DEFAULT 0,
				gas_units     INTEGER NOT NULL DEFAULT 0,
				denom         TEXT NOT NULL DEFAULT '',
				amount_utia   INTEGER NOT NULL DEFAULT 0,
				available_at  TEXT,
				raw_json      TEXT NOT NULL
			)`,
			`CREATE INDEX IF NOT EXISTS payments_time ON payments (time)`,
			`CREATE INDEX IF NOT EXISTS payments_publisher_time ON payments (publisher, time)`,
			`CREATE INDEX IF NOT EXISTS payments_kind_time ON payments (kind, time)`,
			`CREATE INDEX IF NOT EXISTS payments_promise ON payments (promise_hash)`,
			`CREATE INDEX IF NOT EXISTS payments_processor ON payments (processor, kind)`,
			// A publisher's escrow balance as the chain holds it, read by
			// state query (there is no list-all query, so only publishers
			// the payments table already knows are polled). available is
			// balance minus a pending withdrawal.
			`CREATE TABLE IF NOT EXISTS escrow_accounts (
				publisher      TEXT PRIMARY KEY,
				found          INTEGER NOT NULL DEFAULT 0,
				denom          TEXT NOT NULL DEFAULT '',
				balance_utia   INTEGER NOT NULL DEFAULT 0,
				available_utia INTEGER NOT NULL DEFAULT 0,
				height         INTEGER NOT NULL DEFAULT 0,
				updated_at     TEXT NOT NULL
			)`,
		},
	},
	{
		version: 8,
		note:    "bytes handed over per probe, so a transfer rate can be stated over the download alone",
		stmts: []string{
			// Throughput used to be rows per second over the whole probe:
			// dial, TLS, identity check, download, verification. The fixed
			// cost of the first three is amortised over a big shard and not
			// over a small one, so the figure rose with stake by
			// construction, and a row is as wide as its blob's square, so
			// rows/s was not comparable across blobs either. Bytes over the
			// download step alone answer both. Nullable: a record written
			// before the field existed says nothing about size.
			`ALTER TABLE probes ADD COLUMN bytes_returned INTEGER`,
		},
	},
	{
		version: 9,
		note:    "the evidence behind a verdict, on the row: returned row indices and digest, the gRPC code, the shadowing promise, and the code that judged it",
		stmts: []string{
			// A classification is a function of the wire result and the
			// code. Without the returned indices nobody can re-run the
			// assignment check behind a WRONG_ROWS or PARTIAL verdict;
			// without the digest an INVALID_ROWS claim is "we saw it";
			// without the gRPC code SERVER_ERROR and THROTTLED are a
			// substring match on free text; without the build and pin the
			// verdict cannot be traced to the code that made it. All
			// nullable: rows from before the fields existed say nothing.
			`ALTER TABLE probes ADD COLUMN row_indices TEXT`,
			`ALTER TABLE probes ADD COLUMN rows_sha256 TEXT`,
			`ALTER TABLE probes ADD COLUMN rpc_code TEXT`,
			`ALTER TABLE probes ADD COLUMN shadowed_by TEXT`,
			`ALTER TABLE probes ADD COLUMN observer_build TEXT`,
			`ALTER TABLE probes ADD COLUMN app_version INTEGER`,
		},
	},
}

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

// OpenReadOnly opens an existing database for queries only: no migration,
// no DDL, every statement runs under PRAGMA query_only. The API uses it so a
// read-only process cannot race the collector's schema setup or write by
// accident. It fails if the database does not exist or its schema is not the
// version this binary knows.
func OpenReadOnly(path string) (*Store, error) {
	if path == ":memory:" {
		return nil, errors.New("read-only open needs a file")
	}
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("observer database %s: %w (start the collector first)", path, err)
	}
	// Tuning, in the order it matters.
	//
	// query_only is the safety property this constructor exists for: the file
	// is still opened read-write at the OS level, which is what keeps WAL
	// working (a WAL reader writes to the -shm file), and every statement is
	// refused if it would write.
	//
	// cache_size is negative, which SQLite reads as kibibytes rather than
	// pages: 48 MiB per connection. The aggregates here scan hundreds of
	// thousands of index entries and the 2 MiB default evicts most of the
	// index between one query and the next.
	//
	// mmap_size lets reads come from the page cache without a copy into
	// SQLite's own. 1 GiB is a ceiling, not a reservation: only pages actually
	// touched are mapped.
	dsn := "file:" + path + "?_pragma=busy_timeout(5000)&_pragma=query_only(1)" +
		"&_pragma=cache_size(-49152)&_pragma=mmap_size(1073741824)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	// Readers run concurrently. The writable Open above pins the pool to one
	// connection because SQLite serialises writers anyway and a single
	// connection avoids "database is locked" between our own goroutines; that
	// reasoning does not carry over here, and copying it did real damage. In
	// WAL mode any number of readers proceed at once without blocking each
	// other, and this process never writes. With the pool at one, a background
	// snapshot refresh — seconds of aggregate over hundreds of thousands of
	// rows — held the only connection, so every unrelated request behind it
	// (a blob page, the footer's counts) waited for the whole refresh. Each
	// connection carries its own page cache, so the count is bounded rather
	// than left to grow with concurrency.
	conns := runtime.NumCPU()
	if conns < 4 {
		conns = 4
	}
	if conns > 8 {
		conns = 8
	}
	db.SetMaxOpenConns(conns)
	db.SetMaxIdleConns(conns)
	// Every version from 1 to SchemaVersion must be present, not just the
	// highest: a database with a gap is missing that migration's columns, and
	// MAX alone would wave it through.
	var highest, distinct int
	if err := db.QueryRow(`SELECT COALESCE(MAX(version), 0), COUNT(DISTINCT version) FROM schema_migrations`).
		Scan(&highest, &distinct); err != nil {
		db.Close()
		return nil, fmt.Errorf("read schema version: %w (is this an observer database?)", err)
	}
	if highest > SchemaVersion {
		// A newer collector has added columns this binary does not know. The
		// queries here might still run, and the first one that does not would
		// fail at request time on a public site; refuse now instead.
		db.Close()
		return nil, fmt.Errorf("database schema version %d is newer than this binary's %d: upgrade this binary", highest, SchemaVersion)
	}
	if highest != SchemaVersion || distinct != SchemaVersion {
		db.Close()
		return nil, fmt.Errorf("schema version %d (%d of %d migrations applied), this binary expects %d: "+
			"run the collector once to migrate, or upgrade this binary", highest, distinct, SchemaVersion, SchemaVersion)
	}
	return &Store{db: db}, nil
}

// DB exposes the underlying handle for read-only queries (the API).
func (s *Store) DB() *sql.DB { return s.db }

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate() error {
	// The baseline is idempotent (every statement is CREATE ... IF NOT
	// EXISTS), so running it against an existing database is a no-op and
	// against a new one creates version 1.
	for _, stmt := range splitSQL(schemaSQL) {
		if _, err := s.db.Exec(stmt); err != nil {
			return fmt.Errorf("schema: %w\n%s", err, stmt)
		}
	}
	if _, err := s.db.Exec(`INSERT OR IGNORE INTO schema_migrations (version, applied_at) VALUES (1, ?)`,
		ts(time.Now())); err != nil {
		return fmt.Errorf("record baseline: %w", err)
	}

	applied, err := s.appliedVersions()
	if err != nil {
		return err
	}
	var highest int
	for v := range applied {
		if v > highest {
			highest = v
		}
	}
	if highest > SchemaVersion {
		return fmt.Errorf("database schema version %d is newer than this binary's %d: "+
			"upgrade the binary, or point it at a different database", highest, SchemaVersion)
	}

	for _, m := range migrations {
		if m.version > SchemaVersion {
			return fmt.Errorf("migration %d is above SchemaVersion %d: bump the constant", m.version, SchemaVersion)
		}
		if applied[m.version] {
			continue
		}
		if err := s.applyMigration(m); err != nil {
			return err
		}
	}

	// Every version from 1 to SchemaVersion must now be recorded. A gap means
	// migrations is missing an entry, which would let OpenReadOnly's version
	// check pass over a database that never got the columns.
	applied, err = s.appliedVersions()
	if err != nil {
		return err
	}
	for v := 1; v <= SchemaVersion; v++ {
		if !applied[v] {
			return fmt.Errorf("schema version %d is missing from migrations", v)
		}
	}
	return nil
}

func (s *Store) appliedVersions() (map[int]bool, error) {
	rows, err := s.db.Query(`SELECT version FROM schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("read schema versions: %w", err)
	}
	defer rows.Close()
	out := map[int]bool{}
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out[v] = true
	}
	return out, rows.Err()
}

func (s *Store) applyMigration(m migration) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, stmt := range m.stmts {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("migration %d (%s): %w\n%s", m.version, m.note, err, stmt)
		}
	}
	if _, err := tx.Exec(`INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)`,
		m.version, ts(time.Now())); err != nil {
		return fmt.Errorf("migration %d: record: %w", m.version, err)
	}
	return tx.Commit()
}

// splitSQL turns a schema file into executable statements. Comments are
// stripped before splitting on ";" so a semicolon inside a comment cannot cut
// a statement in half.
func splitSQL(src string) []string {
	var sb strings.Builder
	for _, line := range strings.Split(src, "\n") {
		sb.WriteString(stripSQLComment(line))
		sb.WriteByte('\n')
	}
	var out []string
	for _, stmt := range strings.Split(sb.String(), ";") {
		if stmt = strings.TrimSpace(stmt); stmt != "" {
			out = append(out, stmt)
		}
	}
	return out
}

// TimeLayout is the fixed-width UTC layout every timestamp column uses, so
// that string comparison in SQL (>=, ORDER BY, MAX) is chronological.
// RFC3339Nano trims trailing zeros, which breaks that: "...:00Z" sorts after
// "...:00.5Z".
const TimeLayout = "2006-01-02T15:04:05.000000000Z"

// TS formats a time for a timestamp column or a comparison argument.
func TS(t time.Time) string { return t.UTC().Format(TimeLayout) }

func ts(t time.Time) string { return TS(t) }

// stripSQLComment removes a trailing "--" comment from one SQL line, ignoring
// a "--" that falls inside a single-quoted string literal (a DEFAULT or a
// CHECK constraint may legitimately contain one).
func stripSQLComment(line string) string {
	inStr := false
	for i := 0; i < len(line); i++ {
		switch {
		case line[i] == '\'':
			// '' inside a literal is an escaped quote, which this toggle
			// handles correctly: it closes and immediately reopens.
			inStr = !inStr
		case !inStr && line[i] == '-' && i+1 < len(line) && line[i+1] == '-':
			return line[:i]
		}
	}
	return line
}

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
	// A record written before AttestationSchemaVersion has no attestation
	// evidence, so these columns stay NULL. Writing 0 would assert that no
	// validator attested, which the record does not say.
	att := func(v int64) any {
		if !p.HasAttestation() {
			return nil
		}
		return v
	}
	res, err := tx.Exec(`INSERT INTO publications
		(promise_hash, commitment, blob_version, blob_size, namespace, chain_id, promise_height, creation_timestamp,
		 signer, signer_public_key, validator_signature_count, settlement_height, settlement_time, settlement_tx_hash,
		 settlement_tx_index, settlement_tx_code, must_serve_until, must_serve_until_basis, shard_retention_s,
		 payment_promise_timeout_s, assignment_error, protocol_params_fingerprint, pinned_celestia_app,
		 validator_set_height, total_voting_power, sigma_rows, distinct_rows, wrap_overlaps, validators_with_rows,
		 recorded_at, raw_json,
		 attested_with_rows, attested_voting_power, signature_entries, signatures_verified,
		 signatures_unmatched, signatures_out_of_position)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?,
		        ?, ?, ?, ?, ?, ?)
		ON CONFLICT(promise_hash) DO NOTHING`,
		p.PromiseHash, p.Promise.Commitment, p.Promise.BlobVersion, p.Promise.BlobSize, p.Promise.Namespace,
		p.Promise.ChainID, p.Promise.Height, ts(p.Promise.CreationTimestamp),
		p.Signer, p.Promise.SignerPublicKey, p.ValidatorSignatureCount, p.SettlementHeight, ts(p.SettlementTime),
		p.SettlementTxHash, p.SettlementTxIndex, p.SettlementTxCode, ts(p.MustServeUntil), p.MustServeUntilBasis,
		p.ParamsAtPublication.ShardRetentionSeconds, p.ParamsAtPublication.PaymentPromiseTimeoutSeconds,
		a.Error, a.ProtocolParams.Fingerprint, a.ProtocolParams.PinnedCelestiaApp,
		a.ValidatorSetHeight, a.TotalVotingPower, a.Sigma, a.Distinct, a.WrapOverlaps, a.ValidatorsWithRows,
		ts(p.RecordedAt), string(raw),
		att(int64(a.AttestedWithRows)), att(a.AttestedVotingPower), att(int64(a.SignatureEntries)),
		att(int64(a.SignaturesVerified)), att(int64(a.SignaturesUnmatched)), att(int64(a.SignaturesOutOfPosition)))
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
		var attested any
		if p.HasAttestation() {
			attested = b2i(v.Attested)
		}
		if _, err := tx.Exec(`INSERT INTO assignments (promise_hash, validator_address, voting_power, row_count, rows_json, attested)
			VALUES (?, ?, ?, ?, ?, ?) ON CONFLICT(promise_hash, validator_address) DO NOTHING`,
			p.PromiseHash, v.Address, v.VotingPower, v.RowCount, rowsJSON, attested); err != nil {
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
		 assignment_verified, phase, outcome, classification, classification_reason, raw_error, total_duration_ms, raw_json,
		 attested, bytes_returned, row_indices, rows_sha256, rpc_code, shadowed_by, observer_build, app_version)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?,
		        ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(dedupe_key) DO NOTHING`,
		m.DedupeKey(), m.Vantage, m.PromiseHash, m.Commitment, m.BlobVersion, ts(m.MustServeUntil), m.ValidatorSetHeight,
		m.ValidatorAddress, m.ValidatorHost, b2i(m.Assigned), m.AssignedRowCount, m.ScheduleLabel, ts(m.ScheduledAt),
		ts(m.StartedAt), ts(m.FinishedAt), m.LatenessMS,
		b2i(m.DNS.OK), m.DNS.DurationMS, b2i(m.TCP.OK), m.TCP.DurationMS, b2i(m.TLS.OK), m.TLS.DurationMS,
		m.TLS.Version, m.TLS.PeerCertSHA256, b2i(m.Identity.OK), m.Identity.Reason,
		b2i(m.Download.OK), m.Download.DurationMS, m.Download.RowsReturned, m.Download.RowsExpected,
		b2i(m.Download.CommitmentVerified), b2i(m.Download.AssignmentVerified),
		string(m.Phase), string(m.Outcome), string(m.Classification), m.ClassificationReason, m.RawError,
		m.TotalDurationMS, string(raw),
		probeAttested(m), probeBytes(m),
		nullIfEmpty(rowIndicesJSON(m)), nullIfEmpty(m.Download.RowsSHA256), nullIfEmpty(m.Download.RPCCode),
		nullIfEmpty(m.Download.ShadowedBy), nullIfEmpty(observerBuild(m)), observerAppVersion(m))
	if err != nil {
		return false, fmt.Errorf("probe %s: %w", m.DedupeKey(), err)
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// nullIfEmpty stores "" as NULL: an absent fact, not an empty one.
func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// rowIndicesJSON is the returned row indices as a JSON array, "" when none.
func rowIndicesJSON(m probe.Measurement) string {
	if len(m.Download.RowIndices) == 0 {
		return ""
	}
	b, err := json.Marshal(m.Download.RowIndices)
	if err != nil {
		return ""
	}
	return string(b)
}

func observerBuild(m probe.Measurement) string {
	if m.Observer == nil {
		return ""
	}
	return m.Observer.Build
}

func observerAppVersion(m probe.Measurement) any {
	if m.Observer == nil || m.Observer.AppVersion == 0 {
		return nil
	}
	return m.Observer.AppVersion
}

// probeBytes maps the bytes handed over to a nullable column. A record from
// before the field existed carries zero, and zero bytes beside a positive row
// count is not a measurement: it stays NULL so no rate is drawn over it.
func probeBytes(m probe.Measurement) any {
	if m.Download.BytesReturned <= 0 {
		return nil
	}
	return m.Download.BytesReturned
}

// probeAttested maps a measurement's attestation to its nullable column. A
// record written before AttestationSchemaVersion carries no evidence, so the
// column stays NULL: its Attested is false only because the field did not
// exist, and the API must not read that as "this validator did not attest".
func probeAttested(m probe.Measurement) any {
	if !m.HasAttestation() {
		return nil
	}
	return b2i(m.Attested)
}

// ---- endpoints ----

// ObserveEndpoints reconciles one AllBondedFibreProviders snapshot with the
// endpoint history: unseen (validator, host) pairs open a row, pairs seen
// again extend last_seen, open rows missing from the snapshot are closed.
// Returns how many rows were opened and closed.
//
// "Closed" means only that the pair stopped appearing in the bonded provider
// list, which is what closed_reason records. It is not deregistration:
// x/valaddr has no message for that, and its msg server rejects an empty
// host, so a registration once made stays on chain. Jailing and unbonding
// both remove a provider from the bonded list while the entry survives, so
// treating a closure as the operator withdrawing its endpoint would be
// reading an event that cannot happen.
func (s *Store) ObserveEndpoints(ctx context.Context, providers []scan.FibreProvider, height int64, now time.Time) (opened, closed int, err error) {
	evs, err := s.ObserveEndpointEvents(ctx, providers, height, now)
	for _, e := range evs {
		if e.Kind == EndpointOpened {
			opened++
		} else {
			closed++
		}
	}
	return opened, closed, err
}

// Endpoint event kinds, as written to registry.jsonl.
const (
	EndpointOpened = "endpoint_opened"
	EndpointClosed = "endpoint_closed"
)

// EndpointEvent is one change to the Fibre endpoint registry as this
// observer saw it: a (validator, host) pair appearing in or leaving
// AllBondedFibreProviders. The collector appends these to registry.jsonl so
// the endpoint history, which has no other source than the live polls,
// survives a rebuild of the database from the JSONL files.
type EndpointEvent struct {
	Kind        string    `json:"kind"`
	ConsAddress string    `json:"validator_cons_address"`
	Host        string    `json:"host"`
	Height      int64     `json:"height"`
	At          time.Time `json:"at"`
	Reason      string    `json:"reason,omitempty"`
}

// ObserveEndpointEvents is ObserveEndpoints returning what changed.
func (s *Store) ObserveEndpointEvents(ctx context.Context, providers []scan.FibreProvider, height int64, now time.Time) ([]EndpointEvent, error) {
	var events []EndpointEvent
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	type key struct{ addr, host string }
	open := map[key]int64{}
	rows, err := tx.Query(`SELECT id, validator_cons_address, host FROM endpoints WHERE closed_at IS NULL`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id int64
		var k key
		if err := rows.Scan(&id, &k.addr, &k.host); err != nil {
			rows.Close()
			return nil, err
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
				return nil, err
			}
			continue
		}
		if _, err := tx.Exec(`INSERT INTO endpoints (validator_cons_address, host, first_seen_at, first_seen_height, last_seen_at, last_seen_height)
			VALUES (?, ?, ?, ?, ?, ?)`, k.addr, k.host, ts(now), height, ts(now), height); err != nil {
			return nil, err
		}
		events = append(events, EndpointEvent{Kind: EndpointOpened, ConsAddress: k.addr, Host: k.host, Height: height, At: now.UTC()})
	}
	for k, id := range open {
		if seen[k] {
			continue
		}
		if _, err := tx.Exec(`UPDATE endpoints SET closed_at = ?, closed_height = ?, closed_reason = ? WHERE id = ?`,
			ts(now), height, "left_bonded_provider_list", id); err != nil {
			return nil, err
		}
		events = append(events, EndpointEvent{Kind: EndpointClosed, ConsAddress: k.addr, Host: k.host, Height: height, At: now.UTC(), Reason: "left_bonded_provider_list"})
	}
	return events, tx.Commit()
}

// ReplayEndpointEvent applies one registry.jsonl record. It is idempotent:
// an open already present (by the same first_seen time, or by a later live
// poll) and a close already applied are no-ops, so the file can be tailed
// from zero as often as needed. Returns whether a row changed.
func (s *Store) ReplayEndpointEvent(e EndpointEvent) (bool, error) {
	switch e.Kind {
	case EndpointOpened:
		var n int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM endpoints WHERE validator_cons_address = ? AND host = ?
			AND (first_seen_at = ? OR closed_at IS NULL)`, e.ConsAddress, e.Host, ts(e.At)).Scan(&n); err != nil {
			return false, err
		}
		if n > 0 {
			return false, nil
		}
		_, err := s.db.Exec(`INSERT INTO endpoints (validator_cons_address, host, first_seen_at, first_seen_height, last_seen_at, last_seen_height)
			VALUES (?, ?, ?, ?, ?, ?)`, e.ConsAddress, e.Host, ts(e.At), e.Height, ts(e.At), e.Height)
		return err == nil, err
	case EndpointClosed:
		reason := e.Reason
		if reason == "" {
			reason = "left_bonded_provider_list"
		}
		res, err := s.db.Exec(`UPDATE endpoints SET closed_at = ?, closed_height = ?, closed_reason = ?
			WHERE validator_cons_address = ? AND host = ? AND closed_at IS NULL AND first_seen_at <= ?`,
			ts(e.At), e.Height, reason, e.ConsAddress, e.Host, ts(e.At))
		if err != nil {
			return false, err
		}
		n, _ := res.RowsAffected()
		return n > 0, nil
	default:
		return false, fmt.Errorf("unknown endpoint event kind %q", e.Kind)
	}
}

// UpsertValidatorIdentities stores what the staking module says about each
// validator. It is upsert-only: a validator that stops appearing keeps its
// last known name, because a row in the probe tables with no name is worse
// than a row with a stale one, and the chain does not forget validators.
// UpsertPayment inserts one escrow movement; a key already present is left
// alone (the scanner's dedupe key is stable across re-scans).
func (s *Store) UpsertPayment(p scan.Payment, raw []byte) (inserted bool, err error) {
	if p.DedupeKey == "" || p.Publisher == "" || p.Kind == "" {
		return false, fmt.Errorf("payment without dedupe_key, publisher or kind")
	}
	var avail any
	if p.AvailableAt != nil {
		avail = ts(*p.AvailableAt)
	}
	res, err := s.db.Exec(`INSERT INTO payments
		(dedupe_key, kind, height, time, tx_hash, tx_index, msg_index, publisher, processor,
		 promise_hash, namespace, blob_size, gas_units, denom, amount_utia, available_at, raw_json)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(dedupe_key) DO NOTHING`,
		p.DedupeKey, p.Kind, p.Height, ts(p.Time), p.TxHash, p.TxIndex, p.MsgIndex, p.Publisher, p.Processor,
		p.PromiseHash, p.Namespace, int64(p.BlobSize), int64(p.GasUnits), p.Denom, int64(p.AmountUtia), avail, string(raw))
	if err != nil {
		return false, fmt.Errorf("payment %s: %w", p.DedupeKey, err)
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// UpsertEscrowAccount records what the chain currently holds for one
// publisher. A publisher not found on chain is stored with found=0 and zero
// balances, so the page can say "no escrow" rather than nothing.
func (s *Store) UpsertEscrowAccount(e scan.Escrow, now time.Time) error {
	if e.Signer == "" {
		return fmt.Errorf("escrow account without a signer")
	}
	_, err := s.db.Exec(`INSERT INTO escrow_accounts
		(publisher, found, denom, balance_utia, available_utia, height, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(publisher) DO UPDATE SET
			found          = excluded.found,
			denom          = excluded.denom,
			balance_utia   = excluded.balance_utia,
			available_utia = excluded.available_utia,
			height         = excluded.height,
			updated_at     = excluded.updated_at`,
		e.Signer, b2i(e.Found), e.Denom, int64(e.BalanceUtia), int64(e.AvailableUtia), e.Height, ts(now))
	if err != nil {
		return fmt.Errorf("escrow account %s: %w", e.Signer, err)
	}
	return nil
}

// Publishers lists every account the payments table has seen as an escrow
// owner, for the collector's escrow poll.
func (s *Store) Publishers() ([]string, error) {
	rows, err := s.db.Query(`SELECT DISTINCT publisher FROM payments ORDER BY publisher`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) UpsertValidatorIdentities(ids []scan.ValidatorIdentity, now time.Time) (int, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	n := 0
	for _, v := range ids {
		if v.ConsAddressHex == "" {
			// No consensus key this build could parse, so nothing to join
			// against. Counted as skipped rather than stored under a blank
			// key, which would collide every such validator into one row.
			continue
		}
		if _, err := tx.Exec(`INSERT INTO validator_identities
			(cons_address, operator_address, moniker, identity, website, tokens, jailed, status, first_seen_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(cons_address) DO UPDATE SET
				operator_address = excluded.operator_address,
				moniker          = excluded.moniker,
				identity         = excluded.identity,
				website          = excluded.website,
				tokens           = excluded.tokens,
				jailed           = excluded.jailed,
				status           = excluded.status,
				updated_at       = excluded.updated_at`,
			strings.ToLower(v.ConsAddressHex), v.OperatorAddress, v.Moniker, v.Identity, v.Website,
			v.Tokens, b2i(v.Jailed), v.Status, ts(now), ts(now)); err != nil {
			return 0, fmt.Errorf("validator identity %s: %w", v.ConsAddressHex, err)
		}
		n++
	}
	return n, tx.Commit()
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

// ---- reachability ----

// InsertReachability stores one heartbeat measurement. Idempotent on
// (vantage, validator, scheduled_at).
func (s *Store) InsertReachability(m probe.Measurement, raw []byte) (inserted bool, err error) {
	key := m.Vantage + "|" + m.ValidatorAddress + "|" + m.ScheduledAt.UTC().Format(time.RFC3339Nano)
	res, err := s.db.Exec(`INSERT INTO reachability
		(dedupe_key, vantage, validator_address, validator_host, height, scheduled_at, started_at,
		 dns_ok, tcp_ok, tcp_ms, tls_ok, tls_ms, peer_cert_sha256, identity_ok, identity_reason,
		 outcome, raw_error, total_duration_ms, raw_json)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(dedupe_key) DO NOTHING`,
		key, m.Vantage, m.ValidatorAddress, m.ValidatorHost, m.ValidatorSetHeight, ts(m.ScheduledAt), ts(m.StartedAt),
		b2i(m.DNS.OK), b2i(m.TCP.OK), m.TCP.DurationMS, b2i(m.TLS.OK), m.TLS.DurationMS, m.TLS.PeerCertSHA256,
		b2i(m.Identity.OK), m.Identity.Reason, string(m.Outcome), m.RawError, m.TotalDurationMS, string(raw))
	if err != nil {
		return false, fmt.Errorf("reachability %s: %w", key, err)
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}
