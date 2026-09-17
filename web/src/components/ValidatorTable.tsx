"use client";
import Link from "next/link";
import { useEffect, useMemo, useRef, useState } from "react";
import { type Validator, type Rate, shortBech, ago, MIN_RATED } from "@/lib/api";
import RateCell from "./Rate";
import { Count } from "./Verdict";
import Info from "./Info";

/**
 * The validator table. Eight columns, in the order an operator looks: who,
 * what state is it in right now, is it up, did it serve, did it fault, how
 * fast, how much stake. Two percentages, not four: the rest is on the
 * validator's own page.
 */

type SortKey = "power" | "serve" | "faults" | "uptime" | "throughput";

const COLS: { key: SortKey; label: string; dir: 1 | -1; info: React.ReactNode | null }[] = [
  { key: "uptime", label: "Uptime", dir: 1, info: <>
      <p>TLS handshakes completed, over handshakes attempted. We open a connection to the registered Fibre endpoint every 5 minutes and verify the certificate its consensus key endorsed; nothing is downloaded.</p>
      <p>Handshakes run from one location, so a dip can be a network problem on our side.</p>
    </> },
  { key: "serve", label: "Serve rate", dir: 1, info: <>
      <p>When we reached it: shards handed over, out of the shards this validator signed for, inside the retention window. Probes that could not reach it are listed beside the rate as unreachable; being down shows in Uptime.</p>
      <p>Under {MIN_RATED} rated probes the figure is dimmed: too few to lean on.</p>
    </> },
  { key: "faults", label: "Faults", dir: -1, info: <>
      <p>The validator answered but did not hand over a shard it had signed for.</p>
      <p>The only number counted against a validator. Unreachable, unproven and unregistered are not faults.</p>
    </> },
  { key: "throughput", label: "Throughput", dir: -1, info: <>
      <p>Rows delivered per second, from connect to verified rows, over healthy probes.</p>
      <p>Rows per second rather than milliseconds, because assignments run from 148 to 4,096 rows and a bigger shard takes longer.</p>
    </> },
  { key: "power", label: "Voting power", dir: -1, info: null },
];

const rv = (r: Rate | null | undefined) => (r && r.den > 0 && r.value !== null ? r.value : null);
function keyValue(v: Validator, k: SortKey): number | null {
  switch (k) {
    case "power": return v.voting_power;
    case "serve": return rv(v.serve_rate);
    case "faults": return v.classes.FAULT ?? 0;
    case "uptime": return rv(v.reachability_window);
    case "throughput": return v.serve_rows_per_second ?? null;
    default: return null;
  }
}

// The state word: what an operator looks at first. Faults win over
// everything, then the last check's result.
function status(v: Validator): { tone: "ok" | "hold" | "fault" | ""; word: string; title: string } {
  const faults = v.classes.FAULT ?? 0;
  if (faults > 0) return { tone: "fault", word: "faults", title: `${faults} probe${faults === 1 ? "" : "s"} where the validator answered but did not hand over a shard it had signed for.` };
  if (!v.host) return { tone: "", word: "no host", title: "No Fibre endpoint registered in x/valaddr." };
  if (v.reachable === null) return { tone: "", word: "no handshake", title: "No handshake attempted yet." };
  if (v.reachable === false) return { tone: "hold", word: "down", title: `Handshake with ${v.host} failed at the last attempt${v.last_seen_at ? ` (${ago(v.last_seen_at)})` : ""}.` };
  if (v.identity_status === "mismatch") return { tone: "hold", word: "bad cert", title: v.identity_reason || "Certificate not signed by this validator's consensus key." };
  if (v.identity_status === "no_tls") return { tone: "hold", word: "no tls", title: v.identity_reason || "TLS handshake failed." };
  return { tone: "ok", word: "up", title: `Handshake completed at the last attempt${v.last_seen_at ? ` (${ago(v.last_seen_at)})` : ""}.` };
}

function initials(v: Validator): string {
  const m = (v.moniker || "").trim();
  if (!m) return v.address.slice(0, 2);
  const parts = m.split(/[\s._-]+/).filter(Boolean);
  if (parts.length > 1) return parts[0][0] + parts[1][0];
  // "node10" → N10, "Kiln" → KI
  const tail = m.match(/\d+$/);
  return tail ? (m[0] + tail[0]).slice(0, 3) : m.slice(0, 2);
}

export default function ValidatorTable({ rows, notLive }: { rows: Validator[]; notLive?: boolean }) {
  const [q, setQ] = useState("");
  const registered = useMemo(() => rows.filter((v) => !!v.host), [rows]);
  const [tab, setTab] = useState<"registered" | "all" | null>(null);
  const activeTab = tab ?? (registered.length > 0 ? "registered" : "all");
  const [sort, setSort] = useState<{ key: SortKey; dir: 1 | -1 }>({ key: "power", dir: -1 });

  const needle = q.trim().toLowerCase();
  const pool = activeTab === "registered" ? registered : rows;
  const matched = pool.filter((v) => !needle
    || v.address.includes(needle)
    || v.cons_address.includes(needle)
    || (v.moniker ?? "").toLowerCase().includes(needle)
    || (v.operator_address ?? "").toLowerCase().includes(needle)
    || v.host.toLowerCase().includes(needle));

  const ranked = [...matched].sort((a, b) => {
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
        <span className="spacer" />
        {moved && (
          <button className="btn" onClick={() => { setMoved(false); setFrozen(ranked.map((v) => v.address)); }}
            title="New probes changed the ranking. The table kept its order while you read it.">order changed, reorder</button>
        )}
        <input type="search" placeholder="Search name, address, host" value={q} onChange={(e) => setQ(e.target.value)} aria-label="search validators" />
      </div>
      <div className="tablewrap">
        <table className="vtable">
          <thead>
            <tr>
              <th className="rank">#</th>
              <th className="col-pin">Validator</th>
              <th>Status</th>
              {COLS.map((c) => (
                <th key={c.key} className="right">
                  <button className="sort" aria-pressed={sort.key === c.key} onClick={() => clickSort(c.key, c.dir)} title={`Sort by ${c.label.toLowerCase()}`}>
                    {c.label}{arrow(c.key)}
                  </button>
                  {c.info && <Info label={c.label}>{c.info}</Info>}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {list.length === 0 && (
              <tr><td colSpan={8} className="muted">
                {rows.length === 0
                  ? "No validators yet."
                  : needle ? `Nothing matches “${q}”.` : "No validator has registered a Fibre endpoint yet."}
              </td></tr>
            )}
            {list.map((v, i) => {
              const st = status(v);
              return (
                <tr key={v.address}>
                  <td className="rank">{i + 1}</td>
                  <td className="col-pin">
                    <span className="who">
                      <span className="avatar" aria-hidden="true">{initials(v)}</span>
                      <span>
                        <Link className="name" href={`/validator/?addr=${v.address}`}>
                          {v.moniker || (v.cons_address ? shortBech(v.cons_address) : v.address.slice(0, 8) + "…" + v.address.slice(-6))}
                          {v.jailed && <span className="chip hold" title="Jailed by the chain. Shards it signed for are still owed.">jailed</span>}
                        </Link>
                        <span className="addr" title={v.cons_address || v.address}>
                          {v.cons_address ? shortBech(v.cons_address) : v.address.slice(0, 12) + "…"}
                        </span>
                      </span>
                    </span>
                  </td>
                  <td title={st.title}>
                    <span className="verdict"><i className={"dot " + st.tone} /><span className={"w " + (st.tone === "fault" ? "err" : "muted")}>{st.word}</span></span>
                  </td>
                  <td className="right">
                    <RateCell r={v.reachability_window} sample={v.reachability_window?.den ? `${v.reachability_window.den.toLocaleString("en-US")} handshakes` : undefined} />
                  </td>
                  <td className="right"><RateCell r={v.serve_rate} obligations={v.serve_rate_by_obligation} unreachable={v.serve_rate_held_out?.UNREACHABLE ?? 0}
                    sample={(v.serve_rate_held_out?.UNREACHABLE ?? 0) > 0 ? `${v.serve_rate.num} / ${v.serve_rate.den} · ${v.serve_rate_held_out.UNREACHABLE} unreachable` : undefined} /></td>
                  <td className="right" title="Answered, but did not hand over a shard it had signed for."><Count n={v.classes.FAULT} tier="fault" /></td>
                  <td className="right" title={v.serve_rows_per_second == null ? "No healthy probe of an assigned shard in this window." :
                    `${v.serve_latency_p50_ms?.toLocaleString("en-US") ?? "—"} ms typical, ${v.serve_latency_p95_ms?.toLocaleString("en-US") ?? "—"} ms at p95, over ${v.serve_latency_sample.toLocaleString("en-US")} healthy probes.`}>
                    {v.serve_rows_per_second == null
                      ? <span className="nil">·</span>
                      : <span className="rate"><span className="v">{v.serve_rows_per_second.toLocaleString("en-US")}</span><span className="n">rows/s</span></span>}
                  </td>
                  <td className="right mono">{v.voting_power.toLocaleString("en-US")}</td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
      {notLive && <p className="coverage">Nothing measured yet: Fibre is not live on this chain. Names and voting power come from the staking module.</p>}
    </>
  );
}
