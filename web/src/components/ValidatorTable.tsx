"use client";
import Link from "next/link";
import { useEffect, useMemo, useRef, useState } from "react";
import { type Validator, type Rate, shortBech, utc, ago, enoughToRank, fmtCount, fmtRate, MIN_RATED } from "@/lib/api";
import RateCell from "./Rate";
import { Count } from "./Verdict";
import Info from "./Info";

/**
 * The validator table. Nine columns, in the order a reader asks the questions:
 * who, how much stake, did it serve, did it fault, is it up, how fast, how
 * often the chain proved it owed anything, and is it answering right now.
 * Everything else is on the validator's own page.
 */

type SortKey = "worst" | "power" | "serve" | "faults" | "uptime" | "throughput" | "proven";

const COLS: { key: SortKey; label: string; dir: 1 | -1; info: React.ReactNode }[] = [
  { key: "power", label: "Voting power", dir: -1, info: <p>From the staking module at the newest assignment height. The share is of the validators in this table.</p> },
  { key: "serve", label: "Serve rate", dir: 1, info: <>
      <p>Of the shards the chain proves this validator stored, how many it handed over while the obligation held.</p>
      <p>Below {MIN_RATED} rated observations the counts are shown instead of a percentage and the validator is not ranked.</p>
    </> },
  { key: "faults", label: "Faults", dir: -1, info: <>
      <p><strong>The only count held against a validator.</strong> The observer reached it and it failed to hand over a shard the chain proves it stored.</p>
      <p>Unreachable, unproven and unregistered are each shown under their own name, never here.</p>
    </> },
  { key: "uptime", label: "Uptime", dir: 1, info: <>
      <p>Heartbeats that completed a TLS handshake with the registered endpoint, over heartbeats sent. Every registered endpoint is dialled every 10 minutes, assigned or not.</p>
      <p>One vantage: a dip can be a route problem on our side.</p>
    </> },
  { key: "throughput", label: "Throughput", dir: -1, info: <>
      <p>Rows handed over per second, dial to verified rows, over healthy probes.</p>
      <p>Rows per second rather than milliseconds: assignments run from 148 to 4,096 rows, so a bigger validator legitimately takes longer per shard.</p>
    </> },
  { key: "proven", label: "Proven", dir: 1, info: <>
      <p>Blobs in the window whose settled promise carries this validator&rsquo;s verified signature, over blobs it was assigned rows for.</p>
      <p>The publisher stops collecting signatures at two thirds of voting power, so nobody is at 100% by design. A validator that is rarely inside that quorum is answering the publisher slowly, or is far from where blobs are published.</p>
    </> },
];

// Rank by how bad the evidence is. A validator with too few rated probes to
// state a rate is listed with its counts, not ranked among the worst.
function severity(v: Validator): number {
  const faults = v.classes.FAULT ?? 0;
  if (faults > 0 && enoughToRank(v.serve_rate)) return 0;
  if (faults > 0) return 1;
  if (v.reachable === false) return 2;
  if (v.identity_status === "mismatch" || v.identity_status === "no_tls") return 2;
  if (v.reachability_window?.den > 0 && (v.reachability_window.value ?? 1) < 1) return 3;
  if (v.identity_rate_window?.den > 0 && (v.identity_rate_window.value ?? 1) < 1) return 3;
  if (v.probe_count === 0) return 5;
  return 4;
}
const rv = (r: Rate | null | undefined) => (r && r.den > 0 && r.value !== null ? r.value : null);
function keyValue(v: Validator, k: SortKey): number | null {
  switch (k) {
    case "power": return v.voting_power;
    case "serve": return enoughToRank(v.serve_rate) ? rv(v.serve_rate) : null;
    case "faults": return v.classes.FAULT ?? 0;
    case "uptime": return rv(v.reachability_window);
    case "throughput": return v.serve_rows_per_second ?? null;
    case "proven": return rv(v.attestation?.blob_coverage);
    default: return null;
  }
}

function proven(v: Validator): { r: Rate | null; title: string } {
  const a = v.attestation;
  if (!a || a.blob_coverage.den === 0) return { r: null, title: "No publication this validator was assigned rows for in this window." };
  return { r: a.blob_coverage, title: `${a.attested_blobs.toLocaleString("en-US")} of ${a.blob_coverage.den.toLocaleString("en-US")} assigned blobs carry a verified signature from this validator. Unproven is silence, not absence.` };
}

function live(v: Validator): { tone: "ok" | "hold" | ""; word: string; title: string } {
  if (!v.host) return { tone: "", word: "no host", title: "No Fibre endpoint registered in x/valaddr." };
  if (v.reachable === null) return { tone: "", word: "—", title: "Not probed yet." };
  if (v.reachable === false) return { tone: "hold", word: "down", title: `Could not reach ${v.host} at the last heartbeat${v.last_seen_at ? ` (${ago(v.last_seen_at)})` : ""}.` };
  if (v.identity_status === "mismatch") return { tone: "hold", word: "bad cert", title: v.identity_reason || "Certificate not endorsed by this validator's consensus key." };
  if (v.identity_status === "no_tls") return { tone: "hold", word: "no tls", title: v.identity_reason || "TLS handshake failed." };
  return { tone: "ok", word: "up", title: `Reached at the last heartbeat${v.last_seen_at ? ` (${ago(v.last_seen_at)})` : ""}.` };
}

function initials(v: Validator): string {
  const m = (v.moniker || "").trim();
  if (!m) return v.address.slice(0, 2);
  const parts = m.split(/[\s._-]+/).filter(Boolean);
  return (parts.length > 1 ? parts[0][0] + parts[1][0] : m.slice(0, 2));
}

export default function ValidatorTable({ rows, caption, notLive }: { rows: Validator[]; caption: string; notLive?: boolean }) {
  const [q, setQ] = useState("");
  const registered = useMemo(() => rows.filter((v) => !!v.host), [rows]);
  const [tab, setTab] = useState<"registered" | "all" | null>(null);
  const activeTab = tab ?? (registered.length > 0 ? "registered" : "all");
  const [sort, setSort] = useState<{ key: SortKey; dir: 1 | -1 }>({ key: "worst", dir: 1 });

  const needle = q.trim().toLowerCase();
  const pool = activeTab === "registered" ? registered : rows;
  const matched = pool.filter((v) => !needle
    || v.address.includes(needle)
    || v.cons_address.includes(needle)
    || (v.moniker ?? "").toLowerCase().includes(needle)
    || (v.operator_address ?? "").toLowerCase().includes(needle)
    || v.host.toLowerCase().includes(needle));

  const ranked = [...matched].sort((a, b) => {
    if (sort.key === "worst") {
      const s = severity(a) - severity(b);
      if (s) return s;
      const av = keyValue(a, "serve") ?? 2, bv = keyValue(b, "serve") ?? 2;
      return av - bv || b.serve_rate.den - a.serve_rate.den || b.voting_power - a.voting_power;
    }
    const av = keyValue(a, sort.key), bv = keyValue(b, sort.key);
    if (av === null && bv === null) return b.voting_power - a.voting_power;
    if (av === null) return 1;
    if (bv === null) return -1;
    return (av - bv) * sort.dir || b.voting_power - a.voting_power;
  });

  // The order is frozen while the reader looks at it: a row that moves under
  // the cursor because a probe landed can hand a reader the wrong name.
  const [frozen, setFrozen] = useState<string[] | null>(null);
  const key = ranked.map((v) => v.address).join(",");
  const sig = useRef(key);
  const [moved, setMoved] = useState(false);
  useEffect(() => {
    if (sig.current === key) return;
    sig.current = key;
    if (frozen === null) return;
    setMoved(true);
  }, [key, frozen]);
  useEffect(() => { setFrozen(null); setMoved(false); }, [sort, needle, activeTab]);
  useEffect(() => { if (frozen === null && ranked.length) setFrozen(ranked.map((v) => v.address)); },
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [frozen, key]);
  let list = ranked;
  if (frozen && moved) {
    const pos = new Map(frozen.map((a, i) => [a, i]));
    list = [...ranked].sort((a, b) => (pos.get(a.address) ?? 1e9) - (pos.get(b.address) ?? 1e9));
  }
  const total = pool.reduce((s, v) => s + v.voting_power, 0);

  const clickSort = (k: SortKey, dflt: 1 | -1) =>
    setSort((s) => (s.key === k ? { key: k, dir: (s.dir * -1) as 1 | -1 } : { key: k, dir: dflt }));
  const arrow = (k: SortKey) => sort.key === k ? <span className="arrow" aria-hidden="true">{sort.dir === 1 ? "▲" : "▼"}</span> : null;

  return (
    <>
      <div className="toolbar">
        <div className="tabs" role="tablist">
          <button role="tab" aria-pressed={activeTab === "registered"} onClick={() => setTab("registered")}
            title="Validators with a Fibre endpoint in x/valaddr.">Fibre endpoints<span className="n">{registered.length}</span></button>
          <button role="tab" aria-pressed={activeTab === "all"} onClick={() => setTab("all")}
            title="Every bonded validator, whether or not it registered a Fibre endpoint.">All bonded<span className="n">{rows.length}</span></button>
        </div>
        <button className="btn" aria-pressed={sort.key === "worst"} onClick={() => setSort({ key: "worst", dir: 1 })}
          title="Faults first, then unreachable, then partial outages, then everyone else.">worst first</button>
        <span className="spacer" />
        {moved && (
          <button className="btn" onClick={() => { setMoved(false); setFrozen(ranked.map((v) => v.address)); }}
            title="New probes changed the ranking. The table kept its order so nothing moved while you read it.">order changed — reorder</button>
        )}
        <input type="search" placeholder="Search name, address, host" value={q} onChange={(e) => setQ(e.target.value)} aria-label="search validators" />
      </div>
      <div className="tablewrap">
        <table>
          <caption>{caption}{needle && ` · ${list.length} of ${pool.length} match`}</caption>
          <thead>
            <tr>
              <th className="rank">#</th>
              <th className="col-pin">Validator</th>
              {COLS.map((c) => (
                <th key={c.key} className="right">
                  <button className="sort" aria-pressed={sort.key === c.key} onClick={() => clickSort(c.key, c.dir)} title={`Sort by ${c.label.toLowerCase()}`}>
                    {c.label}{arrow(c.key)}
                  </button>
                  <Info label={c.label}>{c.info}</Info>
                </th>
              ))}
              <th>Live<Info label="Live">
                <p>The last heartbeat: up if TCP and TLS completed and the certificate was endorsed, down if not. Never counted against anyone.</p>
              </Info></th>
            </tr>
          </thead>
          <tbody>
            {list.length === 0 && (
              <tr><td colSpan={9} className="muted">
                {rows.length === 0
                  ? "No validators yet: nothing registered and nothing probed."
                  : needle ? `Nothing matches “${q}”.` : "No validator has registered a Fibre endpoint yet."}
              </td></tr>
            )}
            {list.map((v, i) => {
              const p = proven(v);
              const l = live(v);
              const share = total > 0 ? (v.voting_power / total) * 100 : 0;
              return (
                <tr key={v.address}>
                  <td className="rank">{i + 1}</td>
                  <td className="col-pin">
                    <span className="who">
                      <span className="avatar" aria-hidden="true">{initials(v)}</span>
                      <span>
                        <Link className="name" href={`/validator/?addr=${v.address}`}>
                          {v.moniker || (v.cons_address ? shortBech(v.cons_address) : v.address.slice(0, 8) + "…" + v.address.slice(-6))}
                          {v.jailed && <span className="chip hold" title="Jailed by the chain. It still owes the shards it signed for.">jailed</span>}
                        </Link>
                        <span className="addr" title={v.cons_address || v.address}>
                          {v.cons_address ? shortBech(v.cons_address) : v.address.slice(0, 12) + "…"}{v.host ? ` · ${v.host}` : ""}
                        </span>
                      </span>
                    </span>
                  </td>
                  <td className="right mono power" title={`${share.toFixed(2)}% of the voting power in this table`}>
                    {v.voting_power.toLocaleString("en-US")}
                    <span className="share">{share.toFixed(1)}%</span>
                  </td>
                  <td className="right"><RateCell r={v.serve_rate} obligations={v.serve_rate_by_obligation} /></td>
                  <td className="right" title="Reached, and failed to hand over a shard the chain proves it stored."><Count n={v.classes.FAULT} tier="fault" /></td>
                  <td className="right">
                    <RateCell r={v.reachability_window} sample={v.reachability_window?.den ? `${v.reachability_window.den.toLocaleString("en-US")} checks` : undefined} />
                  </td>
                  <td className="right" title={v.serve_rows_per_second == null ? "No healthy probe of an assigned shard in this window." :
                    `${v.serve_latency_p50_ms?.toLocaleString("en-US") ?? "—"} ms typical, ${v.serve_latency_p95_ms?.toLocaleString("en-US") ?? "—"} ms at p95, over ${v.serve_latency_sample.toLocaleString("en-US")} healthy probes.`}>
                    {v.serve_rows_per_second == null
                      ? <span className="nil">·</span>
                      : <span className="rate"><span className="v">{v.serve_rows_per_second.toLocaleString("en-US")}</span><span className="n">rows/s</span></span>}
                  </td>
                  <td className="right" title={p.title}>
                    {p.r ? <span className="rate"><span className="v">{fmtRate(p.r)}</span><span className="n">{fmtCount(p.r)} blobs</span></span> : <span className="nil">·</span>}
                  </td>
                  <td title={l.title}>
                    <span className="verdict"><i className={"dot " + l.tone} /><span className="w muted">{l.word}</span></span>
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
      {notLive && <p className="coverage">Nothing has been measured yet: Fibre is not live on this chain. Names and voting power come from the staking module.</p>}
    </>
  );
}
