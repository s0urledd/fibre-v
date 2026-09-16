"use client";
import Link from "next/link";
import { useEffect, useRef, useState } from "react";
import { type Validator, shortBech, utc, enoughToRank, fmtCount, fmtRate, MIN_RATED } from "@/lib/api";
import RateCell from "./Rate";
import { Count, Mark } from "./Verdict";

/**
 * Seven columns here; the rest are on the validator's own page. The previous
 * table carried thirteen and measured 1,638px inside a 1,128px column, which
 * put the serve rate and the fault count — the two numbers this product exists
 * to publish — behind a horizontal scroll on a laptop. They are now columns two
 * and three, immediately beside the pinned identity column, so no viewport can
 * hide them.
 *
 * Three columns are gone rather than moved.
 *
 * "Tolerated" was always zero: TOLERATED is a grace-phase class and every rate
 * query filters phase = 'in_window', so the count could never be anything else.
 *
 * "Unattested" was a probe count, and an obligation is probed at four schedule
 * points, so it read four times larger than the thing it described. An
 * operator looking at "812" beside their own name reads an accusation; the
 * true statement was "203 blobs carried no signature from you", which is not
 * an accusation at all — the publisher stops collecting signatures at two
 * thirds of voting power, and everyone past that point is left unproven by
 * design. It belongs in the methodology, and the Obligation column already
 * carries the fact with the blob counts in its tooltip.
 *
 * "Unreachable" was also a probe count, and only over windows the validator
 * happened to be assigned something in. Uptime replaces it: the heartbeat
 * samples every registered endpoint every ten minutes whether or not it was
 * assigned anything, so it answers "is the Fibre service running" with
 * coverage that does not depend on attestation. That is the question the
 * table exists for.
 *
 * "Last probe" went the same way, to make room for Throughput without widening
 * the table past its column: Uptime already says whether this site is getting
 * answers from the endpoint, and it says it over the whole window rather than
 * at one instant, so the timestamp was the weaker of the two. It is still on
 * the validator's own page.
 */

// Rank by how bad the evidence is, not by whether any bad evidence exists. A
// validator with too few rated probes to state a rate is not ranked among the
// worst at all: it is listed with its counts.
function severity(v: Validator): number {
  const faults = v.classes.FAULT ?? 0;
  if (faults > 0 && enoughToRank(v.serve_rate)) return 0;
  if (faults > 0) return 1; // real faults, but too little evidence to rate
  if (v.reachable === false) return 2;
  // An endpoint that answers with a certificate its consensus key does not
  // endorse is as unusable to a client as one that does not answer, and
  // nothing in the serve rate says so: the publisher could not upload to it
  // either, so it is never proven to owe anything and its rate stays empty.
  if (v.identity_status === "mismatch" || v.identity_status === "no_tls") return 2;
  // A service that was down, or unendorsed, for part of the window ranks above
  // one that was neither, whether or not it was assigned anything then.
  if (v.reachability_window?.den > 0 && (v.reachability_window.value ?? 1) < 1) return 3;
  if (v.identity_rate_window?.den > 0 && (v.identity_rate_window.value ?? 1) < 1) return 3;
  if (v.probe_count === 0) return 5;
  return 4;
}

// Within a band, worse rate first, then more evidence first.
function worse(a: Validator, b: Validator): number {
  const av = enoughToRank(a.serve_rate) ? (a.serve_rate.value ?? 2) : 2;
  const bv = enoughToRank(b.serve_rate) ? (b.serve_rate.value ?? 2) : 2;
  return av - bv || b.serve_rate.den - a.serve_rate.den;
}

// What the promise proves about this validator's obligation. "unproven" is
// never an accusation: it says the chain is silent, not that the validator
// failed. Three states, because null means "predates verification".
function attested(v: Validator): { text: string; cls: string; title: string } {
  if (v.attested_last === true) {
    return { text: "proven", cls: "", title: "The newest publication carries this validator's signature, verified against its consensus key. A Fibre server writes the shard before it signs, so that signature is proof of storage." };
  }
  if (v.attested_last === false) {
    const n = v.attestation?.unattested_blobs ?? 0;
    return { text: "unproven", cls: "muted", title: `The newest publication carries no verified signature from this validator, so nothing on chain proves it stored that shard. The publisher stops collecting signatures once two thirds of voting power has answered, and everyone past that point is left unproven by design, so this is silence rather than absence.${n ? ` ${n} blob${n === 1 ? "" : "s"} in this window are outside the serve rate for that reason, in both directions.` : ""}` };
  }
  return { text: "—", cls: "faint", title: "No publication with attestation recorded for this validator yet." };
}

/**
 * Uptime: how often this site completed a TLS conversation with the endpoint
 * over the window, from the heartbeat that runs every ten minutes for every
 * registered validator.
 *
 * Deliberately not RateCell. That component draws a 95% upper bound on the
 * FAULT rate, because a serve rate is published as an accusation and an
 * accusation is stated in the direction it accuses. Uptime is not an
 * accusation: half of every path measured here is this site's own, and a
 * confidence bound drawn around it would dress a statement about a route as a
 * statement about an operator. So it shows the figure and the sample it rests
 * on, and nothing more.
 */
function Uptime({ v }: { v: Validator }) {
  const r = v.reachability_window;
  if (!r || r.den === 0) return <span className="nil" title="no heartbeat for this endpoint in this window">·</span>;
  return (
    <span className="rate">
      <span className="v">{fmtRate(r)}</span>
      <span className="n">{enoughToRank(r) ? `${r.den.toLocaleString("en-US")} checks` : "under floor"}</span>
    </span>
  );
}

function uptimeTitle(v: Validator): string {
  const r = v.reachability_window;
  if (!r || r.den === 0) {
    return v.host
      ? "No reachability heartbeat has completed for this endpoint in this window."
      : "No Fibre host registered in x/valaddr, so there is nothing to reach.";
  }
  const id = v.identity_rate_window;
  const parts = [
    `${fmtCount(r)} heartbeats completed TLS. Every registered endpoint is checked every ten minutes, whether or not it was assigned anything, so this figure does not depend on the chain proving an obligation.`,
  ];
  if (id && id.den > 0 && id.num < id.den) {
    parts.push(`On ${(id.den - id.num).toLocaleString("en-US")} of those the certificate was not endorsed by this validator's consensus key, so a client would have refused it.`);
  }
  if (v.last_unreachable_at) parts.push(`Last failed heartbeat ${utc(v.last_unreachable_at)}.`);
  parts.push("Half of every path measured here is this site's own, so a dip is not by itself a statement about the operator.");
  return parts.join(" ");
}

/**
 * Throughput, and why the column is rows per second rather than milliseconds.
 *
 * Assigned rows run from the 148-row floor to the 4,096-row ceiling, so one
 * validator can be carrying eight times another's bytes for the same blob. On
 * the measured fixture the validator with the highest median duration (550ms)
 * was the fastest on the network once its 1,208 rows were accounted for, and
 * the genuinely slow one — a seventh of everyone else's throughput — sat in the
 * middle of a duration sort. A milliseconds column would have named the wrong
 * validator, on a page that publishes accusations.
 */
function throughputTitle(v: Validator): string {
  if (v.serve_rows_per_second == null) {
    return "No probe of an assigned shard came back in this window, so there is nothing to time.";
  }
  const p50 = v.serve_latency_p50_ms?.toLocaleString("en-US") ?? "—";
  const p95 = v.serve_latency_p95_ms?.toLocaleString("en-US") ?? "—";
  return `Rows handed over per second, from dial to rows verified against the blob commitment, over ${v.serve_latency_sample.toLocaleString("en-US")} probes that came back healthy. ` +
    `A whole probe took ${p50} ms typically and ${p95} ms at the 95th percentile. ` +
    `Rows per second is the comparable figure: assignments run from 148 rows to 4,096, so a validator carrying more rows takes longer for the same service. ` +
    `No threshold is attached to any of this — part of every millisecond is this site's own path.`;
}

// The state word on the identity line, so a reader learns the endpoint's
// condition without a column of its own. Never --fault: none of these is an
// accusation, and the observer's own reach is half of every one of them.
function endpointState(v: Validator): { word: string; title: string } | null {
  if (v.reachable == null) return { word: "never probed", title: "No reachability probe has completed for this endpoint." };
  if (!v.host) return { word: "no host", title: "No Fibre host registered in x/valaddr, so nobody could fetch this validator's rows." };
  if (v.reachable === false) return { word: "unreachable now", title: `This site could not reach ${v.host} at the last heartbeat. From one location that is not distinguishable from a problem on this site's own path.` };
  if (v.identity_status === "mismatch") return { word: "identity mismatch", title: v.identity_reason || "The TLS certificate is not endorsed by this validator's consensus key." };
  if (v.identity_status === "no_tls") return { word: "no tls", title: v.identity_reason || "The TLS handshake failed." };
  return null;
}

export default function ValidatorTable({ rows, caption }: { rows: Validator[]; caption: string }) {
  const [q, setQ] = useState("");
  const [sort, setSort] = useState<"severity" | "power" | "rate">("severity");

  const needle = q.trim().toLowerCase();
  const matched = rows.filter((v) => !needle
    || v.address.includes(needle)
    || v.cons_address.includes(needle)
    || (v.moniker ?? "").toLowerCase().includes(needle)
    || (v.operator_address ?? "").toLowerCase().includes(needle)
    || v.host.toLowerCase().includes(needle));

  const ranked = [...matched].sort((a, b) => {
    if (sort === "power") return b.voting_power - a.voting_power || a.address.localeCompare(b.address);
    if (sort === "rate") return worse(a, b) || b.voting_power - a.voting_power;
    return severity(a) - severity(b) || worse(a, b) || b.voting_power - a.voting_power;
  });

  /**
   * The order is frozen while the reader is looking at it.
   *
   * This page polls every thirty seconds and publishes accusations against
   * named operators. A row that moves under the cursor mid-read — because a
   * probe landed and a validator changed severity band — can hand a reader the
   * wrong name, which is the same class of error as the ones the taxonomy
   * exists to prevent. So a new ranking is computed but not applied: the table
   * keeps the order it had and says the order has changed, and the reader
   * decides when to take it.
   */
  const [frozen, setFrozen] = useState<string[] | null>(null);
  const key = ranked.map((v) => v.address).join(",");
  const sig = useRef(key);
  const [moved, setMoved] = useState(false);
  useEffect(() => {
    if (sig.current === key) return;
    sig.current = key;
    if (frozen === null) return;     // nothing pinned yet: take the new order
    setMoved(true);
  }, [key, frozen]);
  // The first completed render pins the order; changing sort or search retakes it.
  useEffect(() => { setFrozen(null); setMoved(false); }, [sort, needle]);
  useEffect(() => { if (frozen === null && ranked.length) setFrozen(ranked.map((v) => v.address)); },
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [frozen, key]);

  let list = ranked;
  if (frozen && moved) {
    const pos = new Map(frozen.map((a, i) => [a, i]));
    list = [...ranked].sort((a, b) => (pos.get(a.address) ?? 1e9) - (pos.get(b.address) ?? 1e9));
  }
  const total = rows.reduce((s, v) => s + v.voting_power, 0);

  return (
    <>
      <div className="controls">
        <input type="search" placeholder="moniker, consensus address, operator address, host"
          value={q} onChange={(e) => setQ(e.target.value)} aria-label="search validators" />
        <span className="faint">sort</span>
        {(["severity", "power", "rate"] as const).map((s) => (
          <button key={s} aria-pressed={sort === s} onClick={() => setSort(s)}>
            {s === "severity" ? "worst first" : s === "power" ? "voting power" : "serve rate"}
          </button>
        ))}
        {moved && (
          <button onClick={() => { setMoved(false); setFrozen(ranked.map((v) => v.address)); }}
            title="New probes have changed the ranking. The table is holding its previous order so a row does not move while you are reading it.">
            order changed — reorder
          </button>
        )}
      </div>
      <div className="tablewrap">
        <table>
          <caption>
            {caption}{needle && ` · ${list.length} of ${rows.length} match`}
          </caption>
          <thead>
            <tr>
              <th className="col-pin">Validator</th>
              <th className="right">Serve rate</th>
              <th className="right">Fault</th>
              <th className="right">Uptime</th>
              <th className="right">Throughput</th>
              <th>Obligation</th>
              <th className="right">Voting power</th>
            </tr>
          </thead>
          <tbody>
            {list.length === 0 && (
              <tr><td colSpan={7} className="muted">
                {rows.length === 0
                  ? "No validators seen yet: no registered Fibre endpoint and no probe."
                  : `No validator matches “${q}”. Try the consensus address or the Fibre host.`}
              </td></tr>
            )}
            {list.map((v) => {
              const a = attested(v);
              const st = endpointState(v);
              const share = total > 0 ? (v.voting_power / total) * 100 : 0;
              return (
                <tr key={v.address}>
                  <td className="col-pin">
                    <Link className="name" href={`/validator/?addr=${v.address}`}>
                      {v.moniker || (v.cons_address ? shortBech(v.cons_address) : v.address.slice(0, 8) + " ••• " + v.address.slice(-8))}
                    </Link>
                    {v.jailed && <span className="chip" title="The chain has jailed this validator. It still owes the shards it signed for, so it stays in this table.">jailed</span>}
                    {st && <span className="chip" title={st.title}><Mark tier="hold" /> {st.word}</span>}
                    <span className="addr" title={v.cons_address || v.address}>
                      {v.cons_address ? shortBech(v.cons_address) : v.address.slice(0, 12) + "…"}
                    </span>
                  </td>
                  <td className="right" title={enoughToRank(v.serve_rate)
                    ? `${fmtCount(v.serve_rate)} probes the chain proves the validator owed`
                    : `only ${v.serve_rate.den} rated probes: too few to state as a percentage (floor ${MIN_RATED})`}>
                    <RateCell r={v.serve_rate} obligations={v.serve_rate_by_obligation} />
                  </td>
                  <td className="right" title="Reached, and failed to hand over a shard the chain proves it stored. The only class counted against a validator.">
                    <Count n={v.classes.FAULT} tier="fault" />
                  </td>
                  <td className="right" title={uptimeTitle(v)}>
                    <Uptime v={v} />
                  </td>
                  <td className="right" title={throughputTitle(v)}>
                    {v.serve_rows_per_second == null
                      ? <span className="nil" title="no probe of an assigned shard came back in this window">·</span>
                      : <span className="rate">
                          <span className="v">{v.serve_rows_per_second.toLocaleString("en-US")}</span>
                          <span className="n">rows/s</span>
                        </span>}
                  </td>
                  <td className={a.cls} title={a.title}>{a.text}</td>
                  <td className="right mono" title={`${share.toFixed(2)}% of the voting power in this table`}>
                    {v.voting_power.toLocaleString("en-US")}
                    <span className="n faint"> {share.toFixed(1)}%</span>
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
    </>
  );
}
