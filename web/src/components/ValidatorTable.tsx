"use client";
import Link from "next/link";
import { useState } from "react";
import { type Validator, shortBech, utc, ago, enoughToRank, fmtCount, MIN_RATED } from "@/lib/api";
import RateCell from "./Rate";

const IDENT: Record<string, string> = {
  verified: "TLS identity verified against the consensus key",
  mismatch: "TLS certificate is not endorsed by this validator's consensus key",
  no_tls: "TLS handshake failed",
  unverified: "TLS fine, no identity verdict recorded yet",
  unreachable: "endpoint unreachable at the last probe",
  unknown: "never probed",
};

// Rank by how bad the evidence is, not by whether any bad evidence exists.
// The old rule put every validator with a single fault at the top, so one
// unlucky probe outranked a hundred real ones, and that is the row a reader
// screenshots. A validator with too few rated probes to state a rate is not
// ranked among the worst at all: it is listed with its counts.
function severity(v: Validator): number {
  const c = v.classes;
  const faults = c.FAULT ?? 0;
  if (faults > 0 && enoughToRank(v.serve_rate)) return 0;
  if (faults > 0) return 1; // real faults, but too little evidence to rate
  if (v.reachable === false) return 2;
  if ((c.UNREACHABLE ?? 0) > 0) return 3;
  if ((c.TOLERATED ?? 0) > 0) return 4;
  if (v.probe_count === 0) return 6;
  return 5;
}

// Within a severity band, worse rate first, then more evidence first.
function worse(a: Validator, b: Validator): number {
  const av = enoughToRank(a.serve_rate) ? (a.serve_rate.value ?? 2) : 2;
  const bv = enoughToRank(b.serve_rate) ? (b.serve_rate.value ?? 2) : 2;
  return av - bv || b.serve_rate.den - a.serve_rate.den;
}

// What the promise proves about this validator's obligation, for the column
// and its tooltip. "unproven" is never an accusation: it says the chain is
// silent, not that the validator failed.
function attestedCell(v: Validator): { text: string; title: string } {
  const a = v.attestation;
  if (v.attested_last === true) return { text: "proven", title: "The newest publication carries this validator's signature, verified against its consensus key. A Fibre server writes the shard before it signs, so that signature is proof of storage." };
  if (v.attested_last === false) {
    const n = a?.unattested_probes ?? 0;
    return { text: "unproven", title: `The newest publication carries no verified signature from this validator, so nothing on chain proves it stored that shard. The publisher stops collecting signatures once it has a safe quorum, so this is silence, not absence.${n ? ` ${n} probes in this window are excluded from the serve rate for that reason.` : ""}` };
  }
  return { text: "—", title: "No publication with attestation recorded for this validator yet." };
}

export default function ValidatorTable({ rows, caption }: { rows: Validator[]; caption: string }) {
  const [q, setQ] = useState("");
  const [sort, setSort] = useState<"severity" | "power" | "rate">("severity");
  const needle = q.trim().toLowerCase();
  let list = rows.filter((v) => !needle || v.address.includes(needle) || v.cons_address.includes(needle) || v.host.toLowerCase().includes(needle));
  list = [...list].sort((a, b) => {
    if (sort === "power") return b.voting_power - a.voting_power || a.address.localeCompare(b.address);
    if (sort === "rate") return worse(a, b) || b.voting_power - a.voting_power;
    return severity(a) - severity(b) || worse(a, b) || b.voting_power - a.voting_power;
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
              <th>Obligation</th>
              <th className="right">Fault</th>
              <th className="right">Unreachable</th>
              <th className="right">Not probed</th>
              <th className="right">Voting power</th>
              <th className="right">Rows (band)</th>
              <th className="right">Last probe</th>
            </tr>
          </thead>
          <tbody>
            {list.length === 0 && (
              <tr><td colSpan={12} className="muted">{rows.length === 0 ? "No validators seen yet: no registered Fibre endpoint and no probe." : `No validator matches “${q}”. Try the consensus address or the Fibre host.`}</td></tr>
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
                <td className="right mono" title={enoughToRank(v.serve_rate)
                  ? `${fmtCount(v.serve_rate)} probes the validator was proven to owe`
                  : `only ${v.serve_rate.den} rated probes: too few to state as a percentage (floor ${MIN_RATED})`}>
                  <RateCell r={v.serve_rate} obligations={v.serve_rate_by_obligation} />
                </td>
                <td className={attestedCell(v).text === "unproven" ? "muted" : ""} title={attestedCell(v).title}>{attestedCell(v).text}</td>
                <td className="right mono">{v.classes.FAULT ?? 0}</td>
                <td className="right mono" title="Probes where this site could not complete a conversation with the endpoint. From one location that is not distinguishable from a problem on this site's own path, so it is kept out of the serve rate.">{v.classes.UNREACHABLE ?? 0}</td>
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
