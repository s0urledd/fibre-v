"use client";
import Link from "next/link";
import { useEffect, useMemo, useRef, useState } from "react";
import { type Validator, type Rate, shortBech, ago, held, utc, bytesPerSecond, MIN_RATED, enoughToRank, API_BASE } from "@/lib/api";
import RateCell from "./Rate";
import { Count } from "./Verdict";
import Info from "./Info";
import { DISPUTE_URL, SELF_VALIDATOR } from "@/lib/site";

/**
 * The validator table. Eight columns, in the order an operator looks: who,
 * what state is it in right now, is it reachable, did it keep its
 * obligations, how fast, did it fault, how much stake. Two percentages, not
 * four: the rest is on the validator's own page.
 */

type SortKey = "power" | "serve" | "faults" | "reach" | "throughput";

const COLS: { key: SortKey; label: string; dir: 1 | -1; info: React.ReactNode | null }[] = [
  { key: "power", label: "Voting power", dir: -1, info: null },
  { key: "reach", label: "Reachability", dir: 1, info: <>
      <p>TLS handshakes completed, over handshakes attempted: one every 5 minutes with the registered Fibre endpoint, from one location. Nothing is downloaded. Whether the certificate is the right one is a separate figure, Endorsed, on the validator&rsquo;s page.</p>
      <p>This is not signing uptime. A validator can sign every block with its Fibre endpoint down, and the reverse.</p>
      <p>Under {MIN_RATED} handshakes the figure has no gauge and is not ranked: too few to lean on.</p>
    </> },
  { key: "serve", label: "Serve rate", dir: 1, info: <>
      <p>Obligations kept: shards this validator signed for and handed over at the last probe before the retention deadline, out of the obligations we saw kept or broken. One observation per shard, not per probe.</p>
      <p>An obligation we never saw served and never saw broken is counted next to the rate, not inside it. Under {MIN_RATED} obligations the figure has no gauge and is not ranked: too few to lean on.</p>
    </> },
  { key: "throughput", label: "Throughput", dir: -1, info: <>
      <p>Bytes handed over per second during the download itself, median over healthy probes. Connecting and checking the certificate are not in it.</p>
      <p>Measured from one location, so part of every figure is our own path.</p>
      <p>Under {MIN_RATED} healthy probes the figure is printed but not ranked.</p>
    </> },
  { key: "faults", label: "Faults", dir: -1, info: <>
      <p>The validator answered but did not hand over a shard it had signed for.</p>
      <p>Counted <strong>per obligation</strong>, like the serve rate: one per shard broken, not one per probe. The schedule visits the same shard four times, so a probe count of the same event reads about four times larger &mdash; and a four-figure number beside an operator&rsquo;s name is an accusation the record does not support. The probe count is on the row&rsquo;s tooltip and in the API.</p>
      <p>The only number counted against a validator. Unreachable, unproven and unregistered are not faults.</p>
      <p>What it claims is that the validator was reached and did not hand over a shard the chain records it as obliged to hold. It is not a finding of intent, and one thing it cannot rule out is a power cut: the Fibre server commits its shard markers without waiting for the disk, so a validator that lost power can answer <em>not found</em> for a shard still on it. <a href={DISPUTE_URL} rel="noopener noreferrer" target="_blank">How to dispute a verdict</a>.</p>
    </> },
];

const rv = (r: Rate | null | undefined) => (r && r.den > 0 && r.value !== null ? r.value : null);
const bonded = (v: Validator) => !v.jailed && (!v.bond_status || v.bond_status === "BOND_STATUS_BONDED");

// Whether a row is the validator this observer's own operator runs.
//
// A site that grades operators and is run by one of them should say which row
// is its own; leaving a reader to work it out from a moniker is not saying it.
// The match takes any of the three identifiers the API publishes, so an
// operator can set whichever one they have to hand. It marks and nothing
// else: no filter, no exclusion, no adjustment. The figures on that row come
// from the same code and the same record as every other, which is the only
// reason marking it is worth anything.
function isSelf(v: Validator): boolean {
  if (!SELF_VALIDATOR) return false;
  return [v.address, v.cons_address, v.operator_address]
    .some((a) => !!a && a.toLowerCase() === SELF_VALIDATOR);
}

// The faults cell prints broken obligations and the tooltip carries the probe
// count behind them, because the two answer different questions and the gap
// between them is not noise: four probes visit one shard, and a fault outside
// the retention window is a fault of a probe with no obligation to break.
function faultTitle(v: Validator): string {
  const broken = v.obligations?.broken ?? 0;
  const probes = v.faults ?? v.classes.FAULT ?? 0;
  const head = "Answered, but did not hand over a shard it had signed for.";
  if (broken === 0 && probes === 0) return head;
  const obl = `${broken.toLocaleString("en-US")} obligation${broken === 1 ? "" : "s"} broken`;
  const pr = `${probes.toLocaleString("en-US")} fault probe${probes === 1 ? "" : "s"}`;
  if (broken === 0) return `${head} ${pr}, none of them inside a retention window this validator was proven to be under, so no obligation is counted broken.`;
  return `${head} ${obl}, over ${pr}.`;
}
function keyValue(v: Validator, k: SortKey): number | null {
  switch (k) {
    case "power": return v.voting_power;
    // Under the floor a rate is printed dimmed and not ranked: it sorts with
    // the rows that have no rate at all, in either direction.
    case "serve": return enoughToRank(v.obligations?.rate) ? rv(v.obligations.rate) : null;
    // Broken obligations, not fault probes: one per shard broken. The probe
    // count ranks behind it, so a fault outside the retention window — where
    // it is no obligation to break — still sorts above a clean validator
    // instead of vanishing from the column.
    case "faults": return (v.obligations?.broken ?? 0) * 1e6 + (v.faults ?? v.classes.FAULT ?? 0);
    // Reachability ranks under the same floor as the serve rate. A validator
    // that registered an hour ago has a handful of handshakes, and this
    // column sorts worst-first: one failed handshake out of two would have
    // put it above operators that have been down all week on hundreds. The
    // cell already prints such a figure without a gauge; it must not sort by
    // it either.
    case "reach": return bonded(v) && enoughToRank(v.reachability_window) ? rv(v.reachability_window) : null;
    case "throughput": return v.serve_throughput_sample >= MIN_RATED ? (v.serve_bytes_per_second ?? null) : null;
    default: return null;
  }
}

// The state word: is the endpoint there right now. Liveness only; a fault
// has its own column and does not outrank a validator being down today.
// The chain's own words come first: a jailed or unbonded validator is out of
// the bonded provider list, so no handshake is attempted while it is out.
function status(v: Validator): { tone: "ok" | "hold" | ""; word: string; title: string } {
  if (v.jailed) return { tone: "", word: "jailed", title: "Jailed by the chain. Out of the bonded provider list, so no handshake is attempted; shards it signed for are still owed." };
  if (!bonded(v)) return { tone: "", word: "not bonded", title: `${v.bond_status!.replace("BOND_STATUS_", "").toLowerCase()} by the chain. Out of the bonded provider list, so no handshake is attempted.` };
  if (!v.host) return { tone: "", word: "no host", title: v.last_host ? `No open Fibre endpoint. Last registered ${v.last_host}, left the bonded list ${utc(v.endpoint_closed_at)}.` : "No Fibre endpoint registered in x/valaddr." };
  if (v.reachable === null) return { tone: "", word: "no handshake", title: "No handshake attempted yet." };
  if (v.reachable === false) {
    const since = held(v.last_reachable_at);
    return { tone: "hold", word: since ? `down · ${since}` : "down", title: `Handshake with ${v.host} failed at the last attempt${v.last_seen_at ? ` (${ago(v.last_seen_at)})` : ""}${since ? `; last completed ${ago(v.last_reachable_at)}` : ""}.` };
  }
  if (v.identity_status === "mismatch") return { tone: "hold", word: "bad cert", title: v.identity_reason || "Certificate not signed by this validator's consensus key." };
  if (v.identity_status === "expired") return { tone: "hold", word: "cert expired", title: v.identity_reason || "Certificate endorsed by the right key, but its signed validity window has lapsed." };
  if (v.identity_status === "no_tls") return { tone: "hold", word: "no tls", title: v.identity_reason || "TLS handshake failed." };
  if (v.identity_status === "unverified") return { tone: "hold", word: "unverified", title: "Handshake completed, but the certificate was not checked on the last attempt." };
  const since = held(v.last_unreachable_at);
  return { tone: "ok", word: since ? `up · ${since}` : "up", title: `Handshake completed at the last attempt${v.last_seen_at ? ` (${ago(v.last_seen_at)})` : ""}${since ? `; last failed ${ago(v.last_unreachable_at)}` : "; no failed handshake in this window"}.` };
}

/** avatar text: "node10" → N10, "Kiln" → KI, "P-OPS Team" → PT; the same
 *  rule on every page so one validator wears one badge. */
export function initialsOf(moniker: string | undefined, address: string): string {
  const m = (moniker || "").trim();
  if (!m) return address.slice(0, 2);
  const parts = m.split(/[\s._-]+/).filter(Boolean);
  const tail = m.match(/\d+$/);
  if (parts.length > 1) {
    // "node 10" keeps its number the way "node10" does
    if (parts.length === 2 && /^\d+$/.test(parts[1])) return (parts[0][0] + parts[1]).slice(0, 3);
    return parts[0][0] + parts[1][0];
  }
  return tail ? (m[0] + tail[0]).slice(0, 3) : m.slice(0, 2);
}

/** The validator's badge: the Keybase picture its operator set, served by
 *  our own API once the collector fetched it; initials until then, and
 *  again if the image fails to load. */
export function Avatar({ v }: { v: Validator }) {
  const [broken, setBroken] = useState(false);
  if (v.avatar_url && !broken) {
    return <img className="avatar" src={API_BASE + v.avatar_url} alt="" width={26} height={26} loading="lazy" decoding="async" onError={() => setBroken(true)} />;
  }
  return <span className="avatar" aria-hidden="true">{initialsOf(v.moniker, v.address)}</span>;
}

/** the sample line under an obligation rate: kept of decided, then what the
 *  rate does not speak for */
export function obligationSample(v: Validator): string | undefined {
  const o = v.obligations;
  if (!o || o.total === 0) return undefined;
  // Kept of decided, then the two things the rate does not speak for that a
  // reader must not miss. End-unobserved is on the validator's page.
  const parts = [`${o.served.toLocaleString("en-US")} / ${(o.served + o.broken).toLocaleString("en-US")}`];
  if (o.unobserved > 0) parts.push(`${o.unobserved.toLocaleString("en-US")} unobserved`);
  const throttled = v.classes?.THROTTLED ?? 0;
  if (throttled > 0) parts.push(`${throttled.toLocaleString("en-US")} throttled`);
  return parts.length > 1 ? parts.join(" · ") : undefined;
}

export default function ValidatorTable({ rows, notLive }: { rows: Validator[]; notLive?: boolean }) {
  const [q, setQ] = useState("");
  // A validator with an open Fibre endpoint: one in the bonded registry
  // now. A closed endpoint (the validator left the bonded list) is under
  // "All", with its last host on the row; the header counts the same set.
  const registered = useMemo(() => rows.filter((v) => !!v.host), [rows]);
  // The three states an operator scans for, as filters beside the two sets.
  // No MIN_RATED floor here, deliberately, and not by oversight. The floor
  // under the serve and reachability rates exists because a ratio needs a
  // denominator: "0.0% of one probe" is not a measurement. A fault is not a
  // ratio. One broken obligation is a shard the chain proves a validator
  // signed for and did not hand over when asked, reproducible by anyone who
  // repeats the probe, and a floor would hide exactly the finding this
  // observer exists to publish. What a single fault can still be is a power
  // cut: the Fibre server commits its shard markers without fsync, so a
  // validator that lost power can answer NotFound for a shard still on its
  // disk. That is a caveat to state where the fault is explained, not a
  // reason to suppress the row.
  const faulting = useMemo(() => rows.filter((v) => (v.obligations?.broken ?? 0) > 0 || (v.faults ?? v.classes.FAULT ?? 0) > 0), [rows]);
  const down = useMemo(() => rows.filter((v) => bonded(v) && !!v.host && v.reachable === false), [rows]);
  const noHost = useMemo(() => rows.filter((v) => bonded(v) && !v.host), [rows]);
  type Tab = "registered" | "all" | "faulting" | "down" | "nohost";
  const [tab, setTab] = useState<Tab | null>(null);
  const activeTab: Tab = tab ?? (registered.length > 0 ? "registered" : "all");
  const [sort, setSort] = useState<{ key: SortKey; dir: 1 | -1 }>({ key: "power", dir: -1 });

  const needle = q.trim().toLowerCase();
  const pool = activeTab === "registered" ? registered : activeTab === "faulting" ? faulting : activeTab === "down" ? down : activeTab === "nohost" ? noHost : rows;
  const matched = pool.filter((v) => !needle
    || v.address.includes(needle)
    || v.cons_address.includes(needle)
    || (v.moniker ?? "").toLowerCase().includes(needle)
    || (v.operator_address ?? "").toLowerCase().includes(needle)
    || (v.host || v.last_host || "").toLowerCase().includes(needle));

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
            title="Validators with an open Fibre endpoint: registered in x/valaddr and in the bonded provider list now.">Fibre endpoints<span className="n">{registered.length}</span></button>
          <button role="tab" aria-pressed={activeTab === "all"} onClick={() => setTab("all")}
            title="Every validator on record, bonded or not, whether or not it registered a Fibre endpoint.">All<span className="n">{rows.length}</span></button>
          <button role="tab" aria-pressed={activeTab === "faulting"} onClick={() => setTab("faulting")}
            title="Validators with at least one fault in this window: answered, but did not hand over a shard they had signed for.">Faulting<span className="n">{faulting.length}</span></button>
          <button role="tab" aria-pressed={activeTab === "down"} onClick={() => setTab("down")}
            title="Bonded validators whose registered endpoint failed the latest handshake.">Unreachable<span className="n">{down.length}</span></button>
          <button role="tab" aria-pressed={activeTab === "nohost"} onClick={() => setTab("nohost")}
            title="Bonded validators with no Fibre endpoint registered in x/valaddr.">No endpoint<span className="n">{noHost.length}</span></button>
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
              <tr><td colSpan={8} className="muted" style={{ padding: "var(--s4)" }}>
                {rows.length === 0
                  ? "No validators yet."
                  : needle ? `Nothing matches “${q}”.`
                  : activeTab === "faulting" ? "No validator faulted in this window."
                  : activeTab === "down" ? "Every registered endpoint answered the latest handshake."
                  : activeTab === "nohost" ? "Every bonded validator has registered a Fibre endpoint."
                  : "No validator has registered a Fibre endpoint yet."}
              </td></tr>
            )}
            {list.map((v, i) => {
              const st = status(v);
              const o = v.obligations;
              const rated = (v.serve_rate?.den ?? 0) > 0;
              return (
                <tr key={v.address}>
                  <td className="rank">{i + 1}</td>
                  <td className="col-pin">
                    <span className="who">
                      <Avatar v={v} />
                      <span>
                        <Link className="name" href={`/validator/?addr=${v.address}`}>
                          {v.moniker || (v.cons_address ? shortBech(v.cons_address) : v.address.slice(0, 8) + "…" + v.address.slice(-6))}
                        </Link>
                        <span className="addr" title={v.cons_address || v.address}>
                          {v.cons_address ? shortBech(v.cons_address) : v.address.slice(0, 12) + "…"}
                          {isSelf(v) && <span className="ours" title="This observer's own operator runs this validator. It is measured by the same code from the same rows as every other row here, and nothing about it is filtered, excluded or adjusted.">ours</span>}
                          {notLive && v.signaled_upgrade === true && <span className="ours" title="Signalled for the app version that brings Fibre (x/signal, a chain record).">signalled</span>}
                          {notLive && v.signaled_upgrade === false && <span className="ours" title="Has not signalled for the app version that brings Fibre (x/signal, a chain record).">not signalled</span>}
                        </span>
                      </span>
                    </span>
                  </td>
                  <td title={st.title}>
                    <span className="verdict"><i className={"dot " + st.tone} /><span className="w muted">{st.word}</span></span>
                  </td>
                  <td className="right mono">{v.voting_power.toLocaleString("en-US")}</td>
                  <td className="right">
                    {bonded(v)
                      ? <RateCell r={v.reachability_window} kind="reach" sample={v.reachability_window?.den ? `${v.reachability_window.den.toLocaleString("en-US")} handshakes` : undefined} />
                      : <span className="nil" title="Out of the bonded provider list: no handshake is attempted while it is out.">·</span>}
                  </td>
                  <td className="right">
                    <RateCell r={o?.rate} obligations={o?.rate} kind="serve" unreachable={o?.unobserved ?? 0} sample={obligationSample(v)} />
                  </td>
                  <td className="right" title={v.serve_bytes_per_second == null ? "No healthy probe with a byte count in this window." :
                    `Median over ${v.serve_throughput_sample.toLocaleString("en-US")} healthy probes, download step only.`}>
                    {v.serve_bytes_per_second == null
                      ? <span className="nil">·</span>
                      : <span className="rate"><span className="v">{bytesPerSecond(v.serve_bytes_per_second)}</span></span>}
                  </td>
                  <td className="right" title={faultTitle(v)}><Count n={v.obligations?.broken ?? 0} tier="fault" rated={rated} /></td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
      {notLive && <p className="coverage">Nothing measured yet: Fibre is not live on this chain. Names and voting power come from the staking module; &ldquo;signalled&rdquo; is x/signal&rsquo;s word on whether the validator has signalled for the version that brings Fibre.</p>}
    </>
  );
}
