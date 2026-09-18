"use client";
import { useEffect, useState } from "react";

// Base URL of observer-api. Same-origin "/api" is what deploy/Caddyfile
// proxies; override with NEXT_PUBLIC_API_BASE for local development.
export const API_BASE = (process.env.NEXT_PUBLIC_API_BASE ?? "/api").replace(/\/$/, "");

export type Rate = { num: number; den: number; value: number | null };
/** How much of the serve rate's population the chain actually proves is obliged. */
export type Attestation = {
  attested_probes: number;
  unattested_probes: number;
  unknown_probes: number;
  coverage: Rate;
  /**
   * The same three counts per (validator, blob) obligation rather than per
   * probe. An obligation is probed at four schedule points, so the probe
   * counts run about four times these — and it is these that a page may show
   * an operator. "812 unattested probes" and "203 blobs carried no signature
   * from you" are the same fact, but only one of them is the fact.
   */
  attested_blobs: number;
  unattested_blobs: number;
  unknown_blobs: number;
  blob_coverage: Rate;
};
export type Window = { name: string; start: string; end: string };
export type ClassCounts = Record<string, number>;

export type Meta = {
  api_version: string;
  vantage: string;
  vantage_info: VantageInfo;
  vantage_count: number;
  observed_from_one_location: boolean;
  chain_id: string;
  /**
   * The chain's application version and whether it is high enough for x/fibre
   * and x/valaddr to exist. Below fibre_app_version the modules are not there
   * at all, so an empty registry says nothing about any validator: a page that
   * cannot tell "absent" from "empty" will imply the second while the first is
   * true.
   */
  app_version?: string;
  fibre_app_version?: string;
  fibre_active: boolean;
  /** the chain's tip as the collector last saw it; not how far the scanner has read */
  chain_height?: string;
  last_scanned_height: string;
  endpoints_height: string;
  protocol_params_fingerprint: string;
  pinned_celestia_app_commit: string;
  counts: { Publications: number; Assignments: number; Probes: number; OpenEndpoints: number; Runs: number };
  collector: RunStatus | null;
  prober: RunStatus | null;
  /** newest measurement's start time; the prober's only live signal (it writes JSONL, never this database) */
  last_probe_at: string | null;
  server_time: string;
  /** every observer process with its liveness; health is the /v1/health verdict */
  components: Component[];
  health: "ok" | "degraded" | "down";
  scan_gaps?: ScanGap[];
  /** matches | chain_ahead | chain_behind | unknown */
  pin_status: string;
  unassignable_publications: number;
};
/** where this observer watches from; the first three are operator-declared */
export type VantageInfo = {
  name: string;
  location?: string;
  provider?: string;
  asn?: string;
  egress_addresses?: string[];
  /** per-field: what a reader can actually check, and how */
  verifiability: Record<string, string>;
  complete: boolean;
};

export type RunStatus = { run_id: number; started_at: string; last_heartbeat_at: string; stopped_at: string | null; alive: boolean };

/** one observer process, from the status file it keeps in the data directory */
export type Component = {
  component: string;
  present: boolean;
  alive: boolean;
  ok: boolean;
  age_s: number;
  started_at?: string;
  updated_at?: string;
  stopped_at?: string;
  stop_reason?: string;
  last_ok_at?: string;
  last_error?: string;
  last_error_at?: string;
  height?: number;
  detail?: Record<string, unknown>;
  disk?: { free_bytes: number; total_bytes: number; free_share: number };
};

/** a height range the scanner could not read from its node */
export type ScanGap = { from: number; to: number; reason: string; last_error?: string; at: string; from_time?: string; to_time?: string };

export type Health = {
  status: "ok" | "degraded" | "down";
  checks: { name: string; ok: boolean; detail: string }[];
  components: Component[];
  scan_gaps?: ScanGap[];
  pin_status: string;
  server_time: string;
};

export type RolledUp = { raw_from: string; days: number; note: string };

export type Network = {
  /**
   * When this summary was computed and how long it took. It is a snapshot
   * refreshed on a schedule, not a live query — the figures are aggregates over
   * the whole window and recomputing them per reader is not something a public
   * site can afford — so the page shows its age rather than implying it is now.
   */
  computed_at?: string;
  compute_ms?: number;  window: Window;
  vantage: string;
  observed_from_one_location: boolean;
  registered_endpoints: number;
  validators_probed: number;
  /** a census of the endpoints as of their newest evidence */
  reachability: Rate;
  /** every heartbeat in the window that completed TLS, over every one sent */
  reachability_window: Rate;
  serve_rate: Rate;
  /** how much of the rate's own population produced a verdict */
  serve_rate_coverage: Rate;
  /** one observation per (validator, blob), judged by the newest probe; the headline */
  obligations: Obligations;
  /** obligations.rate, repeated */
  serve_rate_by_obligation: Rate;
  /** class -> probes the rate does not speak for */
  serve_rate_held_out: ClassCounts;
  serve_rate_excluded_classes: { class: string; reason: string }[];
  attestation: Attestation;
  probe_count: number;
  classes: ClassCounts;
  faults?: number; // FAULT of an assigned shard in any phase; the rate is in-window only
  publications: number;
  publication_bytes: number;
  reconstructable: Reconstructable;
  probe_gaps: number;
  probe_gaps_by_outcome: ClassCounts;
  vantage_health: VantageHealth;
  /** set when the window rests partly on the daily rollup: past the raw retention, "all" is the rollup for days before raw_from plus the raw rows */
  rolled_up?: RolledUp;
  serve_rate_by_point: { key: string; serve_rate: Rate }[];
  /** whole-probe duration, dial to verified rows, over HEALTHY probes */
  serve_latency_p50_ms: number | null;
  serve_latency_p95_ms: number | null;
  serve_latency_sample: number;
};

/** one schedule point the observer does not trust itself at */
export type SuspectPoint = {
  at: string;
  label: string;
  validators: number;
  unreachable: Rate;
  fault: Rate;
  /** "unreachable", "fault" or "unreachable,fault" */
  reason: string;
};

/**
 * Correlated failures in the window: likely ours, not theirs. Every probe at
 * a suspect point is left out of every rate, bucket and fault count.
 */
export type VantageHealth = {
  worst_point: Rate;
  at?: string;
  label?: string;
  correlated: boolean;
  threshold: number;
  fault_threshold: number;
  min_validators: number;
  suspect: SuspectPoint[];
  suspect_rows: number;
};

export type Reconstructable = {
  /** fully served, over publications with a verdict */
  rate: Rate;
  /** enough rows came back to rebuild the blob, whether or not everyone answered */
  recoverable: Rate;
  yes: number;
  degraded: number;
  no: number;
  pending: number;
  unknown: number;
  publications_in_window: number;
  publications_examined: number;
  sample_limit: number;
};

/**
 * One observation per (validator, blob) the settled promise proves, judged by
 * the newest in-window probe of it. Only served and broken enter the rate;
 * the rest says how many obligations the rate does not speak for.
 */
export type Obligations = {
  total: number;
  /** newest probe healthy, no fault anywhere */
  served: number;
  /** any probe a fault */
  broken: number;
  /** served earlier, newest probe produced no verdict */
  end_unobserved: number;
  /** never seen serving, never faulted */
  unobserved: number;
  /** ... and the endpoint completed TLS yet handed nothing over */
  unobserved_reachable: number;
  /** ... and it never completed TLS */
  unobserved_unreachable: number;
  /** ... and we never attempted the download (backoff, budget, a missed slot) */
  unobserved_not_probed: number;
  /** the retention window has not ended: no verdict yet, outside the rate */
  pending: number;
  /** served / (served + broken) */
  rate: Rate;
};

export type Validator = {
  address: string;
  cons_address: string;
  /** the name the operator set in the staking module, read from the chain */
  moniker?: string;
  operator_address?: string;
  keybase_identity?: string;
  website?: string;
  /** the chain's own words about the validator, unlike everything we measure */
  jailed: boolean;
  bond_status?: string;
  host: string;
  endpoint_since: string | null;
  /** for a validator with no open endpoint: what was registered, and when it left the bonded list */
  last_host?: string;
  endpoint_closed_at?: string;
  voting_power: number;
  last_seen_at: string | null;
  reachable: boolean | null;
  identity_status: string;
  identity_reason?: string;
  /**
   * How often this observer completed a TLS conversation with the endpoint
   * over the window, from the five-minute handshake. The one stability figure
   * here whose coverage does not depend on being assigned or attested
   * anything: a validator the publisher never collected a signature from
   * still gets 288 samples a day.
   */
  reachability_window: Rate;
  /** of the heartbeats that saw a certificate, how many were endorsed */
  identity_rate_window: Rate;
  last_unreachable_at: string | null;
  last_reachable_at: string | null;
  serve_rate: Rate;
  serve_rate_coverage: Rate;
  /** one observation per (validator, blob), judged by the newest probe; the headline */
  obligations: Obligations;
  /** obligations.rate, repeated */
  serve_rate_by_obligation: Rate;
  serve_rate_held_out: ClassCounts;
  attestation: Attestation;
  probe_count: number;
  classes: ClassCounts;
  faults?: number; // FAULT of an assigned shard in any phase; the rate is in-window only
  assigned_rows_last: number;
  expected_load_band: string;
  /** this validator's serve rate per schedule point: early vs late retention */
  serve_rate_by_point: { key: string; serve_rate: Rate }[] | null;
  /**
   * How long this observer waited for a shard it did get. The percentiles are
   * the whole probe — dial, TLS, DownloadShard, row verification — over the
   * HEALTHY probes of the window.
   *
   * serve_bytes_per_second is the one to compare between validators: the
   * median transfer rate over the download step alone. Assignments run from
   * 148 rows to 4,096, so a large validator legitimately takes longer for the
   * same quality of service, and the fixed cost of dial, handshake and
   * identity check would flatter it if the whole probe were the basis.
   */
  serve_latency_p50_ms: number | null;
  serve_latency_p95_ms: number | null;
  serve_latency_sample: number;
  serve_bytes_per_second: number | null;
  /** healthy probes that carried a byte count; older records do not */
  serve_throughput_sample: number;
  /** newest publication: true proven to have stored it, false unproven, null not recorded */
  attested_last: boolean | null;
  /**
   * The height whose validator set the row counts and voting power above were
   * computed from. A validator that has left the active set keeps its last
   * figures, and this says how stale they are.
   */
  assignment_height?: number;
  /**
   * MsgPaymentPromiseTimeout submitted by this validator's operator account
   * in the window. The chain pays nothing for it; above zero says the
   * operator runs the enforcement path at all.
   */
  timeouts_enforced?: number;
};

export type Probe = {
  vantage: string;
  promise_hash: string;
  validator_address: string;
  validator_host: string;
  assigned: boolean;
  /** true proven obliged, false unproven, null recorded before verification existed */
  attested: boolean | null;
  assigned_row_count: number;
  schedule_label: string;
  scheduled_at: string;
  started_at: string;
  phase: string;
  outcome: string;
  classification: string;
  classification_reason: string;
  rows_returned: number;
  rows_expected: number;
  total_duration_ms: number;
  tls_ok: boolean;
  identity_ok: boolean;
  raw_error?: string;
  retry_first_outcome?: string;
  clock_offset_ms?: number;
  /** the evidence behind the verdict, on rows that carry it (schema 9 and later) */
  row_indices?: number[];
  rows_sha256?: string;
  rpc_code?: string;
  shadowed_by?: string;
  observer_build?: string;
  app_version?: number;
  /** the verdict the row was stamped with, when the collector's late shadow judgement replaced it */
  classification_at_probe?: string;
  amended_at?: string;
  shadow_gap?: string;
  /** where the upload went; host_changed when the host probed differs (the validator re-registered during the window) */
  host_at_settlement?: string;
  host_changed?: boolean;
  /** the evidence probe of the settlement host, run when the current host did not serve; never the verdict */
  settlement_host_outcome?: string;
  settlement_host_served?: boolean;
};

// Below this many rated probes a percentage is noise dressed as a
// measurement, so the tables print the counts instead and the ranking leaves
// the validator out. One unlucky probe used to render "0.0%" next to a named
// validator and sort it above one with a hundred real faults.
export const MIN_RATED = 20;

export type Reconstruct = {
  status: "yes" | "degraded" | "no" | "pending" | "unknown";
  point: string;
  point_at: string;
  window_over: boolean;
  served_distinct_rows: number;
  needed_rows: number;
  served_by_validators: number;
  assigned_validators: number;
  /** assigned validators with a real result at the point; "pending" while short of assigned_validators */
  probed_validators: number;
  /** the blob's encoded row count (16384 for blob v0) */
  total_rows: number;
  /** assigned validators the settled promise proves stored the blob: the denominator for "yes" */
  attested_validators: number;
  attestation_known: boolean;
  served_by_attested: number;
};

export type Blob = {
  charge?: Charge | null;
  promise_hash: string;
  commitment: string;
  namespace: string;
  blob_size: number;
  signer: string;
  settlement_height: number;
  settlement_time: string;
  creation_timestamp: string;
  must_serve_until: string;
  validators_with_rows: number;
  sigma_rows: number;
  distinct_rows: number;
  assignment_error?: string;
  probe_count: number;
  classes: ClassCounts;
  reconstructable: Reconstruct | null;
};

/**
 * The fee side of one promise, from the payments table. Null when the
 * publication was ingested before payments were recorded.
 */
export type Charge = {
  fee_utia: number;
  gas_units: number;
  publisher: string;
  settled: boolean;
  timed_out: boolean;
  processor?: string;
};

/** a count and a total in utia, the shape every money figure takes */
export type Sum = { count: number; utia: number };

export type PriceFormula = { base_gas: number; gas_per_chunk: number; chunk_bytes: number; utia_per_gas: number; note: string };

export type PublisherShare = {
  publisher: string;
  label?: string;
  fees_utia: number;
  fees_share: number | null;
  bytes: number;
  bytes_share: number | null;
  settlements: number;
  publishers?: number;
};

export type DayBucket = { day: string; fees_utia: number; bytes: number; settlements: number; timeouts: number; timed_out_utia: number };
/** one publisher's share of one day; publisher is empty for the folded "other" */
export type DayPublisher = { day: string; publisher: string; label?: string; fees_utia: number; bytes: number; settlements: number };

/**
 * The publisher side of Fibre over a window. Every figure is something the
 * chain recorded; none was measured here. `timeouts` is a floor: a promise
 * nobody reports leaves no trace.
 */
export type Market = {
  window: Window;
  vantage: string;
  computed_at?: string;
  compute_ms?: number;
  source: string;
  settlements: number;
  fees_settled_utia: number;
  bytes: number;
  publishers_active: number;
  paid_per_mib_utia: number | null;
  timeouts: number;
  timed_out_utia: number;
  settlement_rate: Rate;
  timeout_processors: number;
  deposits: Sum;
  withdrawals_requested: Sum;
  withdrawals_executed: Sum;
  escrow_held_utia: number;
  escrow_accounts: number;
  daily: DayBucket[];
  daily_by_publisher: DayPublisher[];
  top_publishers: PublisherShare[];
  other_publishers: PublisherShare | null;
  largest_poster: PublisherShare | null;
  price_formula: PriceFormula;
  notes: string[];
};

export type Escrow = { found: boolean; balance_utia: number; available_utia: number; height: number; updated_at: string };

export type Publisher = {
  publisher: string;
  label?: string;
  label_source?: string;
  settlements: number;
  bytes: number;
  bytes_share: number | null;
  fees_utia: number;
  fees_share: number | null;
  paid_per_mib_utia: number | null;
  avg_blob_bytes: number | null;
  largest_blob_bytes: number;
  timeouts: number;
  timed_out_utia: number;
  first_seen_at: string;
  last_seen_at: string;
  escrow: Escrow | null;
};

export type Payment = {
  kind: "settlement" | "timeout" | "deposit" | "withdrawal_request" | "withdrawal_executed";
  height: number;
  time: string;
  tx_hash?: string;
  publisher: string;
  processor?: string;
  promise_hash?: string;
  namespace?: string;
  blob_size?: number;
  gas_units?: number;
  amount_utia: number;
  available_at?: string;
};

export type Fetch<T> = { data: T | null; error: string | null; loading: boolean };

// One in-flight request and one timer per (path, interval), however many
// components ask for it: the header, the banner, the footer and the page all
// want /v1/meta, which was four requests per interval per viewer.
type Sub = { subs: Set<(f: Fetch<unknown>) => void>; timer: ReturnType<typeof setInterval> | null; last: Fetch<unknown> };
const streams = new Map<string, Sub>();

async function fetchOnce(path: string): Promise<Fetch<unknown>> {
  try {
    const r = await fetch(API_BASE + path, { cache: "no-store" });
    if (!r.ok) {
      let msg = `${r.status}`;
      try { msg = (await r.json()).error ?? msg; } catch { /* keep status */ }
      return { data: null, error: msg, loading: false };
    }
    return { data: await r.json(), error: null, loading: false };
  } catch (e) {
    return { data: null, error: e instanceof Error ? e.message : String(e), loading: false };
  }
}

function subscribe(key: string, path: string, refreshMs: number, fn: (f: Fetch<unknown>) => void): () => void {
  let st = streams.get(key);
  if (!st) {
    st = { subs: new Set(), timer: null, last: { data: null, error: null, loading: true } };
    streams.set(key, st);
    const load = async () => {
      const next = await fetchOnce(path);
      const cur = streams.get(key);
      if (!cur) return;
      cur.last = next;
      cur.subs.forEach((s) => s(next));
    };
    load();
    if (refreshMs > 0) st.timer = setInterval(load, refreshMs);
  } else if (!st.last.loading) {
    // a later subscriber gets the current value at once
    fn(st.last);
  }
  st.subs.add(fn);
  return () => {
    const cur = streams.get(key);
    if (!cur) return;
    cur.subs.delete(fn);
    if (cur.subs.size === 0) {
      if (cur.timer) clearInterval(cur.timer);
      streams.delete(key);
    }
  };
}

export function useApi<T>(path: string | null, refreshMs = 30000): Fetch<T> {
  const [state, setState] = useState<Fetch<T>>({ data: null, error: null, loading: !!path });
  useEffect(() => {
    if (!path) return;
    return subscribe(`${refreshMs}|${path}`, path, refreshMs, (f) => setState(f as Fetch<T>));
  }, [path, refreshMs]);
  return state;
}

// ---- formatting ----

/** the percentage whenever there is anything to divide; the floor only decides
 *  emphasis. One decimal never rounds a record with a fault up to 100.0% or a
 *  record with a success down to 0.0%: those print as bounds. */
export function fmtPct(r: Rate | undefined | null): string {
  if (!r || r.den === 0 || r.value === null) return "—";
  const s = (r.value * 100).toFixed(1);
  if (s === "100.0" && r.num < r.den) return ">99.9%";
  if (s === "0.0" && r.num > 0) return "<0.1%";
  return s + "%";
}

/** whether a rate has enough observations behind it to rank or compare. */
export function enoughToRank(r: Rate | undefined | null): boolean {
  return !!r && r.den >= MIN_RATED;
}
export function fmtCount(r: Rate | undefined | null): string {
  if (!r) return "";
  return `${r.num.toLocaleString("en-US")} / ${r.den.toLocaleString("en-US")}`;
}
export function utc(s: string | null | undefined): string {
  if (!s) return "—";
  const d = new Date(s);
  if (isNaN(d.getTime())) return s;
  return d.toISOString().replace("T", " ").replace(/\.\d+Z$/, "Z");
}
/** how long since: "4 h", "12 min", "3 d"; for a state that has held since s */
export function held(s: string | null | undefined): string {
  if (!s) return "";
  const ms = Date.now() - new Date(s).getTime();
  if (isNaN(ms) || ms < 0) return "";
  const m = Math.round(ms / 60000);
  if (m < 60) return `${Math.max(m, 1)} min`;
  const h = Math.round(m / 60);
  if (h < 48) return `${h} h`;
  return `${Math.round(h / 24)} d`;
}
export function ago(s: string | null | undefined): string {
  if (!s) return "";
  const ms = Date.now() - new Date(s).getTime();
  if (isNaN(ms)) return "";
  const m = Math.round(ms / 60000);
  if (m < 1) return "just now";
  if (m < 60) return `${m} min ago`;
  const h = Math.round(m / 60);
  if (h < 48) return `${h} h ago`;
  return `${Math.round(h / 24)} d ago`;
}
export function shortHex(s: string, n = 8): string {
  if (!s || s.length <= 2 * n + 3) return s;
  return `${s.slice(0, n)} ••• ${s.slice(-n)}`;
}
export function shortBech(s: string): string {
  if (!s) return "";
  const i = s.indexOf("1");
  if (i < 0 || s.length < i + 5) return s;
  return `${s.slice(0, i)} ••• ${s.slice(-4)}`;
}
export function bytes(n: number): string {
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KiB`;
  if (n < 1024 * 1024 * 1024) return `${(n / 1024 / 1024).toFixed(1)} MiB`;
  return `${(n / 1024 / 1024 / 1024).toFixed(2)} GiB`;
}
/** a transfer rate, in the unit the size fits */
export function bytesPerSecond(n: number): string {
  return `${bytes(n)}/s`;
}
export function nsDisplay(ns: string): string {
  const stripped = ns.replace(/^(00)+/, "");
  return shortHex(stripped || ns, 6);
}
// Wilson 95% interval for a proportion. Both ends matter and they answer
// different questions: the lower bound on the serve rate is the charitable
// reading, the upper bound on the fault rate is the accusatory one. A table
// that publishes faults should show the bound on the claim it is making.
function wilson(num: number, den: number): [number, number] | null {
  if (den === 0) return null;
  const z = 1.96, p = num / den, n = den;
  const denom = 1 + (z * z) / n;
  const centre = p + (z * z) / (2 * n);
  const margin = z * Math.sqrt((p * (1 - p)) / n + (z * z) / (4 * n * n));
  return [Math.max(0, (centre - margin) / denom), Math.min(1, (centre + margin) / denom)];
}

export function wilsonLower(num: number, den: number): number | null {
  const w = wilson(num, den);
  return w && w[0];
}

/**
 * Upper bound on the fault rate: "at most this share of the obligations we
 * could judge went unserved, with 95% confidence". This is the direction an
 * accusation has to be stated in.
 *
 * Pass an obligation-level rate where one is available. The four in-window
 * probes of one (validator, blob) are near copies of each other, so a bound
 * drawn around the probe count claims far more precision than the evidence
 * carries.
 */
export function faultRateUpper(r: Rate | undefined | null): number | null {
  if (!r || r.den === 0) return null;
  const w = wilson(r.den - r.num, r.den);
  return w && w[1];
}

// ---- money ----

/** one TIA in utia */
export const UTIA = 1_000_000;

/**
 * utia as TIA with the precision the size of the figure deserves: a fee is
 * 0.695 TIA, a day is 12.4 TIA, an escrow is 6,000 TIA. Never more than
 * three decimals, never a bare "0" for a non-zero amount.
 */
export function tia(utia: number | null | undefined, opts: { unit?: boolean } = {}): string {
  if (utia == null) return "—";
  const v = utia / UTIA;
  let s: string;
  if (v === 0) s = "0";
  else if (Math.abs(v) >= 1000) s = Math.round(v).toLocaleString("en-US");
  else if (Math.abs(v) >= 100) s = v.toFixed(1);
  else if (Math.abs(v) >= 10) s = v.toFixed(2);
  else if (Math.abs(v) >= 0.001) s = v.toFixed(3);
  else s = `${utia} utia`;
  return opts.unit === false ? s : `${s} TIA`;
}

/** the publisher a row belongs to: its registry label if one exists, else the short address */
export function publisherName(p: { publisher: string; label?: string }): string {
  return p.label || shortBech(p.publisher);
}

export function fmtShare(v: number | null | undefined): string {
  if (v == null) return "—";
  const s = (v * 100).toFixed(1);
  if (s === "0.0" && v > 0) return "<0.1%";
  return s + "%";
}
