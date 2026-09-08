"use client";
import { useEffect, useState } from "react";

// Base URL of observer-api. Same-origin "/api" is what deploy/Caddyfile
// proxies; override with NEXT_PUBLIC_API_BASE for local development.
export const API_BASE = (process.env.NEXT_PUBLIC_API_BASE ?? "/api").replace(/\/$/, "");

export type Rate = { num: number; den: number; value: number | null };
export type Window = { name: string; start: string; end: string };
export type ClassCounts = Record<string, number>;

export type Meta = {
  api_version: string;
  vantage: string;
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
  server_time: string;
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
  probe_count: number;
  classes: ClassCounts;
  publications: number;
  publication_bytes: number;
  reconstructable: Rate;
  probe_gaps: number;
};

export type Validator = {
  address: string;
  cons_address: string;
  host: string;
  endpoint_since: string | null;
  voting_power: number;
  last_seen_at: string | null;
  reachable: boolean | null;
  identity_status: string;
  identity_reason?: string;
  serve_rate: Rate;
  probe_count: number;
  classes: ClassCounts;
  assigned_rows_last: number;
  expected_load_band: string;
};

export type Probe = {
  vantage: string;
  promise_hash: string;
  validator_address: string;
  validator_host: string;
  assigned: boolean;
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
};

export type Reconstruct = {
  status: "yes" | "degraded" | "no" | "unknown";
  point: string;
  point_at: string;
  window_over: boolean;
  served_distinct_rows: number;
  needed_rows: number;
  served_by_validators: number;
  assigned_validators: number;
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

export function useApi<T>(path: string | null, refreshMs = 30000): Fetch<T> {
  const [state, setState] = useState<Fetch<T>>({ data: null, error: null, loading: !!path });
  useEffect(() => {
    if (!path) return;
    let stop = false;
    const load = async () => {
      try {
        const r = await fetch(API_BASE + path, { cache: "no-store" });
        if (!r.ok) {
          let msg = `${r.status}`;
          try { msg = (await r.json()).error ?? msg; } catch { /* keep status */ }
          if (!stop) setState({ data: null, error: msg, loading: false });
          return;
        }
        const j = (await r.json()) as T;
        if (!stop) setState({ data: j, error: null, loading: false });
      } catch (e) {
        if (!stop) setState({ data: null, error: e instanceof Error ? e.message : String(e), loading: false });
      }
    };
    load();
    const id = refreshMs > 0 ? setInterval(load, refreshMs) : undefined;
    return () => { stop = true; if (id) clearInterval(id); };
  }, [path, refreshMs]);
  return state;
}

// ---- formatting ----

export function fmtRate(r: Rate | undefined | null): string {
  if (!r || r.den === 0 || r.value === null) return "—";
  return (r.value * 100).toFixed(1) + "%";
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
// Wilson 95% lower bound of a proportion, printed next to small-n rates.
export function wilsonLower(num: number, den: number): number | null {
  if (den === 0) return null;
  const z = 1.96, p = num / den, n = den;
  const denom = 1 + (z * z) / n;
  const centre = p + (z * z) / (2 * n);
  const margin = z * Math.sqrt((p * (1 - p)) / n + (z * z) / (4 * n * n));
  return Math.max(0, (centre - margin) / denom);
}
