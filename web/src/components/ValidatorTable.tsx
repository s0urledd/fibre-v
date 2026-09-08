"use client";
import Link from "next/link";
import { useState } from "react";
import { type Validator, shortBech, utc, ago } from "@/lib/api";
import RateCell from "./Rate";

const IDENT: Record<string, string> = {
  verified: "TLS identity verified against the consensus key",
  mismatch: "TLS certificate is not endorsed by this validator's consensus key",
  no_tls: "TLS handshake failed",
  unreachable: "endpoint unreachable at the last probe",
  unknown: "never probed",
};

function severity(v: Validator): number {
  const c = v.classes;
  if ((c.FAULT ?? 0) > 0) return 0;
  if (v.reachable === false) return 1;
  if ((c.TOLERATED ?? 0) > 0) return 2;
  if (v.probe_count === 0) return 4;
  return 3;
}

export default function ValidatorTable({ rows, caption }: { rows: Validator[]; caption: string }) {
  const [q, setQ] = useState("");
  const [sort, setSort] = useState<"severity" | "power" | "rate">("severity");
  const needle = q.trim().toLowerCase();
  let list = rows.filter((v) => !needle || v.address.includes(needle) || v.cons_address.includes(needle) || v.host.toLowerCase().includes(needle));
  list = [...list].sort((a, b) => {
    if (sort === "power") return b.voting_power - a.voting_power || a.address.localeCompare(b.address);
    if (sort === "rate") return (a.serve_rate.value ?? 2) - (b.serve_rate.value ?? 2) || b.serve_rate.den - a.serve_rate.den;
    return severity(a) - severity(b) || (a.serve_rate.value ?? 2) - (b.serve_rate.value ?? 2) || b.voting_power - a.voting_power;
  });
  return (
    <>
      <div className="controls">
        <input type="search" placeholder="consensus address (hex or celestiavalcons…), host" value={q} onChange={(e) => setQ(e.target.value)} aria-label="search validators" />
        <span className="muted">sort:</span>
        {(["severity", "power", "rate"] as const).map((s) => (
          <button key={s} className={sort === s ? "on" : ""} onClick={() => setSort(s)}>{s === "severity" ? "worst first" : s === "power" ? "voting power" : "serve rate"}</button>
        ))}
      </div>
      <div className="tablewrap">
        <table>
          <caption>{caption}{needle && ` · ${list.length} of ${rows.length} match`}</caption>
          <thead>
            <tr>
              <th>Validator</th>
              <th>Fibre endpoint</th>
              <th>Reachable</th>
              <th>TLS identity</th>
              <th className="right">Serve rate</th>
              <th className="right">Fault</th>
              <th className="right">Tolerated</th>
              <th className="right">Not probed</th>
              <th className="right">Voting power</th>
              <th className="right">Rows (band)</th>
              <th className="right">Last probe</th>
            </tr>
          </thead>
          <tbody>
            {list.length === 0 && (
              <tr><td colSpan={11} className="muted">{rows.length === 0 ? "No validators seen yet: no registered Fibre endpoint and no probe." : `No validator matches “${q}”. Try the consensus address or the Fibre host.`}</td></tr>
            )}
            {list.map((v) => (
              <tr key={v.address}>
                <td className="mono">
                  <Link href={`/validator/?addr=${v.address}`}>{v.cons_address ? shortBech(v.cons_address) : v.address.slice(0, 8) + " ••• " + v.address.slice(-8)}</Link>
                  <div className="faint">{v.address.slice(0, 12)}…</div>
                </td>
                <td className="mono">{v.host || <span className="muted">— not registered</span>}</td>
                <td>{v.reachable === null ? <span className="muted">not probed</span> : v.reachable ? "yes" : <span className="err">no</span>}</td>
                <td title={v.identity_reason || IDENT[v.identity_status]}>{v.identity_status === "mismatch" || v.identity_status === "no_tls" ? <span className="err">{v.identity_status}</span> : v.identity_status}</td>
                <td className="right mono"><RateCell r={v.serve_rate} /></td>
                <td className="right mono">{v.classes.FAULT ?? 0}</td>
                <td className="right mono">{v.classes.TOLERATED ?? 0}</td>
                <td className="right mono">{(v.classes.NOT_PROBED ?? 0) + (v.classes.PROBE_ERROR ?? 0)}</td>
                <td className="right mono">{v.voting_power.toLocaleString("en-US")}</td>
                <td className="right mono">{v.assigned_rows_last ? `${v.assigned_rows_last} (${v.expected_load_band})` : "—"}</td>
                <td className="right mono" title={utc(v.last_seen_at)}>{v.last_seen_at ? ago(v.last_seen_at) : "—"}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </>
  );
}
