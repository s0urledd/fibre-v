// Package probe turns the scanner's publications into scheduled probes and raw
// measurements — the layer that makes a Fibre publication into an actual
// observation.
//
// # Schedule
//
// ScheduleFor derives probe times for one publication from its own retention
// window [settlement_time, must_serve_until], where must_serve_until came from
// the record as creation_timestamp + max(payment_promise_timeout,
// shard_retention). It is never a fixed interval: a few points inside the
// window (clustered toward the deadline, where a retention breach shows), one
// grace point just past must_serve_until, and one post point past the measured
// prune-lag tolerance where NOT_FOUND is the expected answer.
//
// The pending-probe queue is never stored. The Prober re-derives it every cycle
// from publications.jsonl and the existing measurements.jsonl, so a restart
// resumes exactly. Every wait is bounded (Config.MaxSleep between cycles; a
// per-layer timeout on every probe step); the loop never blocks indefinitely
// and a SIGINT/SIGTERM stops it cleanly.
//
// # Probe
//
// Run performs a layered probe against one validator, timing and judging each
// layer on its own: L1 DNS resolution, L2 TCP connect, L3 TLS 1.3 handshake
// (raw), L3 identity (fibre-tlsverify consensus-key binding on the peer cert),
// L4 retrievability (DownloadShard, verify returned rows against the commitment
// with pkg/rsema1d and against the recomputed fibre-assign assignment).
//
// # Measurement
//
// Each probe appends one Measurement to measurements.jsonl: the vantage, the
// time, every layer's separate duration and result, the identity verdict, the
// rows returned and their verification results, and the raw error text. No
// scores — a reliability view is derived from these records later.
//
// # Taxonomy
//
// Classify keeps the error classes apart. An assigned validator that answers
// NOT_FOUND (or is unreachable, or fails identity) BEFORE must_serve_until is a
// FAULT. The same NOT_FOUND in the grace window right after must_serve_until is
// TOLERATED (within the ~1-2 min prune lag measured on the devnet). After the
// tolerance it is EXPECTED_GONE. A validator that was never assigned the shard
// answering NOT_FOUND is EXPECTED_UNASSIGNED. Every measurement carries its
// class and the reason.
package probe
