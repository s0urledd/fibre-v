-- Fibre observer store, schema version 1.
--
-- Conventions: timestamps are RFC 3339 UTC text; hex identifiers are lower
-- case; every derived row keeps the raw JSON record it came from so the
-- classification can be recomputed later. SQL stays inside the subset that
-- SQLite and Postgres share, so the same migrations run on both.

CREATE TABLE IF NOT EXISTS schema_migrations (
    version     INTEGER PRIMARY KEY,
    applied_at  TEXT NOT NULL
);

-- One row per process start of a component (collector, prober, api). The
-- dashboard renders the span between started_at and the last heartbeat as
-- "observed"; anything outside every span is a gap, never a zero.
CREATE TABLE IF NOT EXISTS observer_runs (
    id                INTEGER PRIMARY KEY,
    component         TEXT NOT NULL,
    vantage           TEXT NOT NULL,
    version           TEXT NOT NULL,
    started_at        TEXT NOT NULL,
    last_heartbeat_at TEXT NOT NULL,
    stopped_at        TEXT,
    stop_reason       TEXT
);
CREATE INDEX IF NOT EXISTS observer_runs_component ON observer_runs (component, started_at);

-- Byte offsets into the append-only JSONL files written by sentinel-scan and
-- sentinel-probe. Ingest is idempotent (primary keys below), so a cursor that
-- is behind only costs re-reading.
CREATE TABLE IF NOT EXISTS ingest_cursors (
    file        TEXT PRIMARY KEY,
    byte_offset INTEGER NOT NULL,
    line_no     INTEGER NOT NULL,
    updated_at  TEXT NOT NULL
);

-- x/fibre params as they became effective, copied from the scanner's history.
CREATE TABLE IF NOT EXISTS params_history (
    effective_from_height   INTEGER NOT NULL,
    effective_from_tx_index INTEGER NOT NULL,
    source                  TEXT NOT NULL,
    withdrawal_delay_s          INTEGER NOT NULL,
    payment_promise_timeout_s   INTEGER NOT NULL,
    payment_promise_height_window INTEGER NOT NULL,
    shard_retention_s           INTEGER NOT NULL,
    full_stake_storage_budget   INTEGER NOT NULL,
    PRIMARY KEY (effective_from_height, effective_from_tx_index)
);

-- One row per on-chain MsgPayForFibre (sentinel-scan Publication record).
CREATE TABLE IF NOT EXISTS publications (
    promise_hash              TEXT PRIMARY KEY,
    commitment                TEXT NOT NULL,
    blob_version              INTEGER NOT NULL,
    blob_size                 INTEGER NOT NULL,
    namespace                 TEXT NOT NULL,
    chain_id                  TEXT NOT NULL,
    promise_height            INTEGER NOT NULL,
    creation_timestamp        TEXT NOT NULL,
    signer                    TEXT NOT NULL,
    signer_public_key         TEXT NOT NULL,
    validator_signature_count INTEGER NOT NULL,
    settlement_height         INTEGER NOT NULL,
    settlement_time           TEXT NOT NULL,
    settlement_tx_hash        TEXT NOT NULL,
    settlement_tx_index       INTEGER NOT NULL,
    settlement_tx_code        INTEGER NOT NULL,
    must_serve_until          TEXT NOT NULL,
    must_serve_until_basis    TEXT NOT NULL,
    shard_retention_s         INTEGER NOT NULL,
    payment_promise_timeout_s INTEGER NOT NULL,
    assignment_error          TEXT NOT NULL DEFAULT '',
    protocol_params_fingerprint TEXT NOT NULL DEFAULT '',
    pinned_celestia_app       TEXT NOT NULL DEFAULT '',
    validator_set_height      INTEGER NOT NULL,
    total_voting_power        INTEGER NOT NULL,
    sigma_rows                INTEGER NOT NULL,
    distinct_rows             INTEGER NOT NULL,
    wrap_overlaps             INTEGER NOT NULL,
    validators_with_rows      INTEGER NOT NULL,
    recorded_at               TEXT NOT NULL,
    raw_json                  TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS publications_settlement ON publications (settlement_height);
CREATE INDEX IF NOT EXISTS publications_msu ON publications (must_serve_until);
CREATE INDEX IF NOT EXISTS publications_namespace ON publications (namespace);

-- One row per (publication, validator) with the recomputed row assignment.
CREATE TABLE IF NOT EXISTS assignments (
    promise_hash      TEXT NOT NULL,
    validator_address TEXT NOT NULL,   -- 20-byte consensus address, hex
    voting_power      INTEGER NOT NULL,
    row_count         INTEGER NOT NULL,
    rows_json         TEXT,            -- JSON array of row indices; NULL when the scanner ran with -rows=false
    PRIMARY KEY (promise_hash, validator_address)
);
CREATE INDEX IF NOT EXISTS assignments_validator ON assignments (validator_address);

-- Registered Fibre endpoints, as history. A row is opened the first time a
-- (validator, host) pair is seen in AllBondedFibreProviders and closed when it
-- stops appearing. There is no unregister message on chain, so this is the
-- only way to reconstruct endpoint changes.
CREATE TABLE IF NOT EXISTS endpoints (
    id                     INTEGER PRIMARY KEY,
    validator_cons_address TEXT NOT NULL,  -- celestiavalcons1...
    host                   TEXT NOT NULL,
    first_seen_at          TEXT NOT NULL,
    first_seen_height      INTEGER NOT NULL,
    last_seen_at           TEXT NOT NULL,
    last_seen_height       INTEGER NOT NULL,
    closed_at              TEXT,
    closed_height          INTEGER
);
CREATE INDEX IF NOT EXISTS endpoints_validator ON endpoints (validator_cons_address, closed_at);

-- One row per probe (sentinel-probe Measurement record). The dedupe key is
-- (vantage, promise_hash, validator_address, scheduled_at), the same key the
-- prober uses, so re-ingesting a file is a no-op.
CREATE TABLE IF NOT EXISTS probes (
    dedupe_key            TEXT PRIMARY KEY,
    vantage               TEXT NOT NULL,
    promise_hash          TEXT NOT NULL,
    commitment            TEXT NOT NULL,
    blob_version          INTEGER NOT NULL,
    must_serve_until      TEXT NOT NULL,
    validator_set_height  INTEGER NOT NULL,
    validator_address     TEXT NOT NULL,
    validator_host        TEXT NOT NULL,
    assigned              INTEGER NOT NULL,
    assigned_row_count    INTEGER NOT NULL,
    schedule_label        TEXT NOT NULL,
    scheduled_at          TEXT NOT NULL,
    started_at            TEXT NOT NULL,
    finished_at           TEXT NOT NULL,
    lateness_ms           INTEGER NOT NULL,
    dns_ok                INTEGER NOT NULL,
    dns_ms                INTEGER NOT NULL,
    tcp_ok                INTEGER NOT NULL,
    tcp_ms                INTEGER NOT NULL,
    tls_ok                INTEGER NOT NULL,
    tls_ms                INTEGER NOT NULL,
    tls_version           TEXT NOT NULL DEFAULT '',
    peer_cert_sha256      TEXT NOT NULL DEFAULT '',
    identity_ok           INTEGER NOT NULL,
    identity_reason       TEXT NOT NULL DEFAULT '',
    download_ok           INTEGER NOT NULL,
    download_ms           INTEGER NOT NULL,
    rows_returned         INTEGER NOT NULL,
    rows_expected         INTEGER NOT NULL,
    commitment_verified   INTEGER NOT NULL,
    assignment_verified   INTEGER NOT NULL,
    phase                 TEXT NOT NULL,
    outcome               TEXT NOT NULL,
    classification        TEXT NOT NULL,
    classification_reason TEXT NOT NULL DEFAULT '',
    raw_error             TEXT NOT NULL DEFAULT '',
    total_duration_ms     INTEGER NOT NULL,
    raw_json              TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS probes_validator_time ON probes (validator_address, started_at);
CREATE INDEX IF NOT EXISTS probes_promise ON probes (promise_hash, scheduled_at);
CREATE INDEX IF NOT EXISTS probes_class_time ON probes (classification, started_at);
CREATE INDEX IF NOT EXISTS probes_started ON probes (started_at);

-- Small key/value table for collector facts the API reports: chain_id,
-- last_scanned_height, protocol_params_fingerprint, pinned commit.
CREATE TABLE IF NOT EXISTS meta (
    key        TEXT PRIMARY KEY,
    value      TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
