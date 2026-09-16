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

export type Network = {
  window: Window;
  vantage: string;
  observed_from_one_location: boolean;
  registered_endpoints: number;
  validators_probed: number;
  reachability: Rate;
  serve_rate: Rate;
  /** how much of the rate's own population produced a verdict */
  serve_rate_coverage: Rate;
  /** one observation per (validator, blob); the basis for any interval */
  serve_rate_by_obligation: Rate;
  /** class -> probes the rate does not speak for */
  serve_rate_held_out: ClassCounts;
  serve_rate_excluded_classes: { class: string; reason: string }[];
  attestation: Attestation;
  probe_count: number;
  classes: ClassCounts;
  publications: number;
  publication_bytes: number;
  reconstructable: Reconstructable;
  probe_gaps: number;
  probe_gaps_by_outcome: ClassCounts;
  vantage_health: VantageHealth;
  serve_rate_by_point: { key: string; serve_rate: Rate }[];
};

/** the most correlated failure in the window: likely ours, not theirs */
export type VantageHealth = {
  worst_point: Rate;
  at?: string;
  label?: string;
  correlated: boolean;
  threshold: number;
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
  voting_power: number;
  last_seen_at: string | null;
  reachable: boolean | null;
  identity_status: string;
  identity_reason?: string;
  serve_rate: Rate;
  serve_rate_coverage: Rate;
  serve_rate_by_obligation: Rate;
  serve_rate_held_out: ClassCounts;
  attestation: Attestation;
  probe_count: number;
  classes: ClassCounts;
  assigned_rows_last: number;
  expected_load_band: string;
  /** newest publication: true proven to have stored it, false unproven, null not recorded */
  attested_last: boolean | null;
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

export function fmtRate(r: Rate | undefined | null): string {
  if (!r || r.den === 0 || r.value === null) return "—";
  // Too few observations to state as a percentage: show the counts instead.
  if (r.den < MIN_RATED) return fmtCount(r);
  return (r.value * 100).toFixed(1) + "%";
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
