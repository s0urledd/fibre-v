"use client";
import { Suspense, useState } from "react";
import Link from "next/link";
import { useSearchParams } from "next/navigation";
import { useApi, type Validator, type Probe, type Window, type Rate, type RecordThrough, type Obligations, type ClassCounts, type Meta, type EndpointCheck, int, pctOf, bytes, ago, utcWord, hhmmss, dateUTC, whenUTC, shortMid, undecided, notFound, badRequest, MIN_RATED, API_BASE, provisionalNow, type ProvisionalFaults, type NetworkReference } from "@/lib/api";
import { useWindow, WindowSwitch, windowLabel } from "@/lib/window";
import StatusLine from "@/components/StatusLine";
import { Metric, Metrics } from "@/components/Metrics";
import OutcomeBar from "@/components/OutcomeBar";
import Copy from "@/components/Copy";
import { endpoint } from "@/components/Validators";
import { HostingChip } from "@/components/Hosting";
import { DISPUTE_URL, SELF_VALIDATOR } from "@/lib/site";
import Heatmap from "@/components/Heatmap";
import PreLive from "@/components/PreLive";
import Diagnosis from "@/components/Diagnosis";
import Info from "@/components/Info";
import type { Heatmap as HeatmapData } from "@/lib/signing";

type Span = { window: Window; serve_rate: Rate; probe_count: number; obligations: Obligations; classes: ClassCounts; provisional_faults?: ProvisionalFaults };
type Detail = {
  window: Window;
  record_through?: RecordThrough;
  validator: Validator;
  windows: Span[];
  recent_probes: Probe[];
  recent_probes_truncated?: boolean;
  /** set when this window rests partly on the daily rollup (the "all" window past the raw retention) */
  rolled_up?: { raw_from: string; days: number; note: string };
  /** schedule points, over all time, that no rate counts: the observer's own correlated failures */
  suspect_points: { at: string; label: string; reason: string }[];
  /** the newest heartbeat against this validator's host, stage by stage */
  last_endpoint_check?: EndpointCheck;
  /** served over rated probes per UTC day and schedule point */
  heatmap?: HeatmapData;
  /** the network's service rate over the same window from the same vantage; absent on a pinned window */
  network_reference?: NetworkReference;
};

/** a fraction as the site prints a share */
const pctFrac = (f: number) => (f >= 1 ? "100%" : `${(f * 100).toFixed(1)}%`);

/** the verdict as a word and a mark; the classification is the observer's, never re-derived here */
const WORDS: Record<string, [string, string]> = {
  HEALTHY: ["Served", "ok"], FAULT: ["Broken", "fault"], UNATTESTED: ["Unsigned", "unsigned"], NOT_PROBED: ["Not probed", "gone"],
  EXPECTED_GONE: ["Expected gone", "gone"], UNREACHABLE: ["Unreachable", "other"], NOT_REGISTERED: ["No endpoint", "none"],
  IDENTITY_EXPIRED: ["Certificate expired", "other"], IDENTITY_MISMATCH: ["Wrong certificate", "other"], THROTTLED: ["Rate limited", "other"],
  SERVER_ERROR: ["Server error", "other"], RETENTION_UNVERIFIED: ["Deadline unverified", "gone"], TOLERATED: ["Tolerated", "other"],
  UNREACHABLE_POST_WINDOW: ["Unreachable after window", "gone"], SERVED_PAST_WINDOW: ["Served after window", "gone"],
  SHADOWED_SHARD: ["Shadowed", "other"], UNMATCHED_GENUINE: ["Unmatched genuine rows", "other"], PROBE_ERROR: ["Probe error", "gone"],
  EXPECTED_UNASSIGNED: ["Unassigned", "gone"], SERVING_UNASSIGNED: ["Serving unassigned", "other"],
};
const wordOf = (cls: string): [string, string] => WORDS[cls] ?? [cls.toLowerCase().replace(/_/g, " "), "other"];
/** an unsigned probe is not rated either way; the word says what came back, and nothing more */
const probeWord = (p: Probe): [string, string] => {
  if (p.classification === "UNATTESTED") return (p.outcome === "SERVED_OK" || p.outcome === "PARTIAL") ? ["Served, unsigned", "unsigned"] : ["Unsigned", "unsigned"];
  return wordOf(p.classification);
};
const PHASE: Record<string, string> = { in_window: "in window", grace: "grace", post: "after window" };
const POINT: Record<string, string> = { w1: "w1 · 12% of the window", w2: "w2 · 45%", w3: "w3 · 72%", w4: "w4 · within 2 min 30 s of the deadline" };
const identityWord: Record<string, string> = { verified: "verified", expired: "expired", mismatch: "not this validator’s key", no_tls: "no TLS", unverified: "unverified", unreachable: "unreachable" };

/**
 * What the newest probe rows add up to, by what came back.
 *
 * This replaced a line that read "No failed probe among the newest 50 rows"
 * over fifty rows of "tcp refused", next to a Broken figure of six. Both were
 * true in the narrow sense — none of the fifty was a FAULT, the six were older
 * — and together they told an operator their endpoint was fine when it had
 * not answered in a day. So the rows are counted by outcome group, every
 * group that occurs is named, and "broken" is always named, zero included,
 * because it is the one group that is a fault.
 *
 * The groups are read off the observer's own classification and the wire
 * outcome, never re-judged: broken is FAULT outside a suspect point and
 * nothing else. Served leads and broken closes the list, so the one count
 * that is a fault is always in the same place.
 */
const REACH_FAIL = new Set(["DNS_FAIL", "TCP_REFUSED", "TCP_TIMEOUT", "TCP_UNREACHABLE", "TLS_HANDSHAKE_FAIL", "RPC_UNAVAILABLE", "RPC_ERROR"]);
const GROUPS = ["served", "unreachable", "certificate rejected", "no endpoint", "pruned after the window", "not found, unsigned", "answered with an error", "not counted", "other", "broken"] as const;
type Group = (typeof GROUPS)[number];
function groupOf(p: Probe, suspect: boolean): Group {
  if (suspect) return "not counted";
  if (p.classification === "FAULT") return "broken";
  if (p.classification === "HEALTHY" || p.outcome === "SERVED_OK") return "served";
  if (p.classification === "NOT_REGISTERED") return "no endpoint";
  if (p.classification.startsWith("IDENTITY_")) return "certificate rejected";
  if (REACH_FAIL.has(p.outcome)) return "unreachable";
  if (p.outcome === "NOT_FOUND" && p.phase !== "in_window") return "pruned after the window";
  if (p.outcome === "NOT_FOUND" && p.classification === "UNATTESTED") return "not found, unsigned";
  if (p.classification === "SERVER_ERROR" || p.classification === "THROTTLED") return "answered with an error";
  return "other";
}
/** "Newest 50 probes: 43 served, 7 unreachable, 0 broken" */
function evidenceSummary(rows: { g: Group }[]): string {
  const n = new Map<Group, number>();
  for (const r of rows) n.set(r.g, (n.get(r.g) ?? 0) + 1);
  const parts = GROUPS.filter((g) => g === "broken" || (n.get(g) ?? 0) > 0).map((g) => `${int(n.get(g) ?? 0)} ${g}`);
  return `Newest ${int(rows.length)} probe${rows.length === 1 ? "" : "s"}: ${parts.join(", ")}`;
}
/** the recent-evidence table's filter: every row, the FAULT rows (broken), or every row whose shard did not come back */
type EvFilter = "all" | "failed" | "notserved";

function Page() {
  const addr = useSearchParams().get("addr") ?? "";
  const [win, setWin] = useWindow("24h");
  const [evFilter, setEvFilter] = useState<EvFilter>("all");
  const { data: meta, error: metaErr } = useApi<Meta>("/v1/meta");
  const d = useApi<Detail>(addr ? `/v1/validators/${addr}?window=${win}` : null);
  const notLive = !!meta?.app_version && !meta.fibre_active;
  if (!addr) return <p className="notice">Open a validator from the <Link href="/">overview</Link>, or add <code>?addr=</code> with its consensus, operator (<code>celestiavaloper1…</code>) or account address to the address.</p>;
  const data = d.data;
  if (!data) {
    return (
      <>
        <div className="head"><div><p className="crumb"><Link href="/">Validators</Link> › …</p><h1>{notFound(d) ? "Validator not found" : badRequest(d) ? "Not a validator address" : d.error ? "Validator" : "Loading…"}</h1></div></div>
        <StatusLine meta={meta} metaError={metaErr} snap={null} client={{ error: d.error, fetchedAt: d.fetchedAt, status: d.status }} />
        {notFound(d) && <p className="notice">No validator with the address <span className="mono">{addr}</span> is on record: neither in the staking set nor in any probe. Check the address, or open one from the <Link href="/">overview</Link>.</p>}
        {badRequest(d) && <p className="notice"><span className="mono">{addr}</span> is not a validator address ({d.error}). Open a validator from the <Link href="/">overview</Link>.</p>}
      </>
    );
  }
  const v = data.validator;
  const o = v.obligations;
  const decided = o ? o.served + o.broken : 0;
  const und = undecided(o);
  const e = endpoint(v);
  const bonded = !v.jailed && (!v.bond_status || v.bond_status === "BOND_STATUS_BONDED");
  const rw = v.reachability_window;
  const faults = v.faults ?? v.classes?.FAULT ?? 0;
  const self = !!SELF_VALIDATOR && [v.address, v.cons_address, v.operator_address].some((a) => !!a && a.toLowerCase() === SELF_VALIDATOR);
  const suspect = new Map((data.suspect_points ?? []).map((s) => [s.at, s.reason.replace(",", " and ")]));
  const probes = [...data.recent_probes].sort((a, b) => b.started_at.localeCompare(a.started_at));
  // a failed probe at a point the observer does not trust itself at is not this validator's
  const lastFault = probes.find((p) => p.classification === "FAULT" && !suspect.has(p.scheduled_at));
  const lastOk = probes.find((p) => p.classification === "HEALTHY");
  // Each row with its outcome group, once: the summary counts them and the
  // filter selects on the same judgement, so the two cannot disagree.
  const grouped = probes.map((p) => ({ p, g: groupOf(p, suspect.has(p.scheduled_at)) }));
  const failedN = grouped.filter((r) => r.g === "broken").length;
  // "Not served" is every row outside the served group, not every row short
  // of HEALTHY: an unsigned probe that got its shard back reads "Served,
  // unsigned", and a filter called Not served must not list it.
  const notServedN = grouped.filter((r) => r.g !== "served").length;
  const shown = evFilter === "failed" ? grouped.filter((r) => r.g === "broken")
    : evFilter === "notserved" ? grouped.filter((r) => r.g !== "served") : grouped;
  // Every failed row of the period, for when the broken obligations are older
  // than the newest rows this page carries. /v1/probes filters on the class
  // the rows are published with, and since bounds it to the period.
  const failedHref = `${API_BASE}/v1/probes?validator=${v.address}&class=FAULT${data.window.start ? `&since=${encodeURIComponent(data.window.start)}` : ""}&limit=1000`;
  const showFailed = () => setEvFilter("failed");
  const points = v.serve_rate_by_point ?? [];
  const defaultPoints = points.length === 4 && points.every((p, i) => p.key === `w${i + 1}`);
  const measuring = !!o && o.total > 0 && decided < MIN_RATED && o.pending > 0;
  const att = v.attestation;
  const sig = v.signing;
  // Broken obligations whose faults are all younger than the settling
  // period: counted, and still able to be withdrawn. provisionalNow drops
  // them once `until` passes, so a cached answer does not keep the badge.
  const prov = provisionalNow(v.provisional_faults);
  const ref = data.network_reference;
  const refText = ref && ref.median_rate != null ? `network median ${pctFrac(ref.median_rate)}` : ref && ref.pooled_rate.den > 0 ? `network ${pctOf(ref.pooled_rate.num, ref.pooled_rate.den)}` : "";
  const refTitle = ref ? `The same period from the same location: the median service rate over ${int(ref.validators)} validator${ref.validators === 1 ? "" : "s"} with at least ${int(ref.min_rated)} assessed obligations${ref.median_rate == null ? " (none yet)" : ""}; every obligation together, ${ref.pooled_rate.den > 0 ? `${pctOf(ref.pooled_rate.num, ref.pooled_rate.den)} (${int(ref.pooled_rate.num)}/${int(ref.pooled_rate.den)})` : "none assessed"}.` : undefined;

  return (
    <>
      <div className="head">
        <div>
          <p className="crumb"><Link href={win === "24h" ? "/" : `/?window=${win}`}>Validators</Link> › {v.moniker || shortMid(v.cons_address || v.address, 18, 4)}</p>
          <h1>{v.moniker || <span className="mono">{shortMid(v.cons_address || v.address, 22, 6)}</span>}{self && <span className="ours" title="Huginn Tech runs both this validator and Tensile. It is measured by the same code as every other validator, never filtered or adjusted.">runs Tensile</span>}</h1>
          <div className="chips">
            <span className="state" title={e.title}><i className={"dot " + e.dot} />{e.word}</span>
            {v.host && <span title={v.identity_reason || "The consensus-key check on the newest handshake."}>TLS identity <b className="word">{identityWord[v.identity_status] ?? v.identity_status}</b></span>}
            {v.host && <span><span className="mono">{v.host}</span>{v.endpoint_since && <> · registered since {dateUTC(v.endpoint_since)}</>}</span>}
            {v.host && <HostingChip h={v.hosting} />}
            {!v.host && v.last_host && <span title="The registration stays on chain; the validator left the bonded provider list.">last endpoint <span className="mono">{v.last_host}</span>{v.endpoint_closed_at && <> · left the bonded list {dateUTC(v.endpoint_closed_at)}</>}</span>}
            {sig && sig.assigned > 0
              ? <span title={`Settled promises in this period that assigned this validator rows and carry its verified signature, ${pctOf(sig.signed, sig.assigned)}. Publishers stop collecting signatures at two thirds of stake, so 100% is not expected and a missing signature is not a fault.`}>signed <b className="word">{int(sig.signed)} / {int(sig.assigned)}</b> promises<SignedInfo /></span>
              : att && att.blob_coverage.den > 0 && <span title="Assigned blobs in this period whose settled promise carries this validator’s verified signature. Publishers stop collecting signatures at two thirds of stake, so 100% is not expected and a missing signature is not a fault.">signed <b className="word">{int(att.attested_blobs)} / {int(att.blob_coverage.den)}</b> blobs<SignedInfo /></span>}
            {(v.timeouts_enforced ?? 0) > 0 && <span title="MsgPaymentPromiseTimeout submitted by this validator’s operator account in the period: abandoned promises reported so the escrow was charged. The chain pays nothing for it.">{int(v.timeouts_enforced)} timeout{v.timeouts_enforced === 1 ? "" : "s"} enforced</span>}
          </div>
          <div className="idkv">
            {v.cons_address && <span title={v.cons_address}><span className="mono">{shortMid(v.cons_address, 22, 6)}</span><Copy text={v.cons_address} label="consensus address" /></span>}
            {v.operator_address && <span title={v.operator_address}><span className="mono">{shortMid(v.operator_address, 22, 6)}</span><Copy text={v.operator_address} label="operator address" /></span>}
            <span title={`consensus address, hex: ${v.address}`}><span className="mono">{shortMid(v.address, 10, 6)}</span><Copy text={v.address} label="hex address" /></span>
            <span title="An Atom feed of this validator’s endpoint changes: registered or changed host, bonded-list changes, unreachable and recovered, certificate problems, first fault. Paste the link into any feed reader; nothing is stored about you."><a href={`${API_BASE}/v1/validators/${v.address}/feed.atom`} type="application/atom+xml">Subscribe (Atom)</a> <span className="soft">· endpoint changes in any feed reader</span></span>
            {v.website && <span><a href={v.website} rel="nofollow noopener noreferrer" target="_blank">{v.website.replace(/^https?:\/\//, "").replace(/\/$/, "")}</a></span>}
          </div>
        </div>
        <WindowSwitch value={win} onChange={setWin} />
      </div>
      <PreLive meta={meta} />
      <StatusLine meta={meta} metaError={metaErr} snap={{ record_through: data.record_through, window: data.window }} client={{ error: d.error, fetchedAt: d.fetchedAt, status: d.status }} measuring={measuring} />
      {/* the conclusion before the evidence; before activation there is nothing to conclude, and StatusLine says so */}
      {!notLive && <Diagnosis v={v} check={data.last_endpoint_check} meta={meta} decided={decided} provisional={prov}
        failedShown={failedN} onShowFailed={showFailed} failedHref={failedHref} />}

      <Metrics>
        <Metric label="Service rate"
          value={notLive || !o || o.total === 0 || decided === 0 ? "—" : pctOf(o.served, decided)}
          tone={notLive || !o || decided === 0 ? "absent" : undefined}
          help={notLive ? " " : !o || o.total === 0 ? "no obligation in this period" : decided === 0 ? "awaiting results" : `${int(o.served)} / ${int(decided)} assessed${refText ? ` · ${refText}` : ""}`}
          title={notLive ? undefined : refTitle} />
        <Metric label="Broken obligations"
          value={notLive ? "—" : int(o?.broken ?? 0)} tone={notLive ? "absent" : (o?.broken ?? 0) > 0 ? "fault" : !o || o.total === 0 ? "absent" : undefined}
          help={notLive ? " " : (o?.broken ?? 0) > 0 ? `${int(faults)} failed probe row${faults === 1 ? "" : "s"}${prov > 0 ? ` · ${int(prov)} provisional` : ""}` : faults > 0 ? `${int(faults)} failed probe row${faults === 1 ? "" : "s"} · none broken in this period` : "none in this period"}
          title={prov > 0 ? `${int(prov)} of these rest only on failed probes younger than ${Math.round((v.provisional_faults?.settling_seconds ?? 1800) / 60)} minutes. They count in the rate now, and become final at ${whenUTC(v.provisional_faults!.until)} unless evidence still arriving withdraws them: the rest of the schedule point's probes, or an x/fibre params change not yet reconciled.` : (o?.broken ?? 0) > 0 ? "One obligation counts once, however many probes of it failed. The probe rows are in the evidence below." : faults > 0 ? "A failed probe of an obligation still inside its retention window is not a verdict yet; the obligation is decided at the end of the window." : undefined} />
        <Metric label="Undecided" value={notLive ? "—" : int(und)} tone={notLive || !o || o.total === 0 ? "absent" : undefined} help={notLive ? " " : `${int(o?.pending ?? 0)} pending`} />
        <Metric label="Reachability"
          value={!bonded ? "—" : rw && rw.den > 0 ? int(rw.num) : "—"} den={bonded && rw && rw.den > 0 ? int(rw.den) : undefined}
          tone={!bonded || !rw || rw.den === 0 ? "absent" : undefined}
          help={!bonded ? "out of the bonded list · no handshake" : rw && rw.den > 0 ? `now ${e.word.toLowerCase()}` : "no handshake yet"}
          title="TLS handshakes completed over handshakes attempted with the registered endpoint in the period, from one location. Not signing uptime." />
        <Metric label="Throughput"
          value={v.serve_bytes_per_second == null ? "—" : `${bytes(v.serve_bytes_per_second)}/s`} tone={v.serve_bytes_per_second == null ? "absent" : undefined}
          help={v.serve_bytes_per_second == null ? "no download yet" : `${int(v.serve_throughput_sample)} download${v.serve_throughput_sample === 1 ? "" : "s"} · one location`} />
      </Metrics>

      <section className="band" id="outcomes">
        <div>
          <h2>Obligation outcomes</h2>
          <OutcomeBar o={o} absent={notLive} />
          <table className="periods">
            <thead><tr><th>Period</th><th>Service rate</th><th>Broken</th><th>Pending</th></tr></thead>
            <tbody>
              {data.windows.map((w) => {
                const wo = w.obligations, wd = wo.served + wo.broken;
                const name = w.window.name;
                return (
                  <tr key={name} className={name === win ? "on" : undefined}>
                    <td><button type="button" className="rowlink" aria-pressed={name === win} onClick={() => setWin(name as typeof win)} title={`show the ${windowLabel(name)} period`}>{windowLabel(name)}</button></td>
                    <td>{notLive || wd === 0 ? "—" : <>{pctOf(wo.served, wd)}<span className="den"> · {int(wo.served)}/{int(wd)}</span></>}</td>
                    <td>{notLive ? "—" : wo.broken > 0 ? <><span className="word fault">{int(wo.broken)}</span>{provisionalNow(w.provisional_faults) > 0 && <span className="den" title="Counted, and still settling: see the broken obligations figure above."> · {int(provisionalNow(w.provisional_faults))} provisional</span>}</> : "0"}</td>
                    <td>{notLive ? "—" : int(wo.pending)}</td>
                  </tr>
                );
              })}
            </tbody>
          </table>
          {data.rolled_up && <p className="rolled">Rolled up after 90 days: figures before {data.rolled_up.raw_from} come from the daily rollup ({int(data.rolled_up.days)} days); by-point, signature and throughput figures cover the raw rows from then on.</p>}
        </div>
        <div>
          <h2>Through the retention window</h2>
          <p className="sub">Served probes over rated probes at each point of the window</p>
          {points.length === 0 ? <p className="errs">No rated probe in this period.</p> : (
            <div className="pts">
              <div className="h">Point</div><div className="h n">Served</div><div className="h n">Served / rated</div>
              {points.map((p) => (
                <div key={p.key} style={{ display: "contents" }}>
                  <div>{defaultPoints ? POINT[p.key] ?? p.key : p.key}</div>
                  <div className="n">{p.serve_rate.den ? pctOf(p.serve_rate.num, p.serve_rate.den) : "—"}</div>
                  <div className="n soft">{p.serve_rate.den ? `${int(p.serve_rate.num)} / ${int(p.serve_rate.den)}` : "no rated probe"}</div>
                </div>
              ))}
            </div>
          )}
          <p className="errs">
            {lastFault
              ? <>Last failed probe <b>{whenUTC(lastFault.started_at)}</b> · <code>{lastFault.raw_error || lastFault.classification_reason || lastFault.outcome}</code> · blob <Link className="mono" href={`/blob/?hash=${lastFault.promise_hash}`}>{lastFault.promise_hash.slice(0, 10)}…</Link></>
              : probes.length === 0 ? <>No probe of this validator on record yet</>
              : <>{evidenceSummary(grouped)}{(o?.broken ?? 0) > 0 && <> · the broken obligations are older: <a href={failedHref}>failed rows in the API →</a></>}</>}
            <br />Last successful probe <b>{lastOk ? whenUTC(lastOk.started_at) : "—"}</b> · last failed handshake <b>{v.last_unreachable_at ? whenUTC(v.last_unreachable_at) : "none on record"}</b>
          </p>
          {data.last_endpoint_check && <EndpointCheckLine c={data.last_endpoint_check} />}
        </div>
      </section>

      <section className="hm-band" id="calendar">
        <h2>Day by day</h2>
        <p className="sub">Served over rated probes at each point of the retention window, per UTC day</p>
        <Heatmap data={data.heatmap} />
      </section>

      <section id="evidence">
        <div className="vhead">
          <div><h2>Recent evidence</h2><p className="sub">{probes.length > 0 ? evidenceSummary(grouped) : "No probe rows yet"}, as classified by the observer{data.recent_probes_truncated ? " · the rest in the API" : ""}</p></div>
          <div className="tools">
            {probes.length > 0 && (
              <div className="seg" role="group" aria-label="show rows">
                <button type="button" aria-pressed={evFilter === "all"} onClick={() => setEvFilter("all")}>All <span className="n">{int(probes.length)}</span></button>
                <button type="button" aria-pressed={evFilter === "failed"} onClick={() => setEvFilter("failed")} title="Rows the observer classified FAULT: not found while the promise still held. The only rows that are a fault.">Failed <span className="n">{int(failedN)}</span></button>
                <button type="button" aria-pressed={evFilter === "notserved"} onClick={() => setEvFilter("notserved")} title="Every row whose shard did not come back: unreachable, no endpoint, certificate rejected, pruned after the window, errors and failures alike. Only the failed ones are faults.">Not served <span className="n">{int(notServedN)}</span></button>
              </div>
            )}
            <a className="dis" href={evFilter === "failed" ? failedHref : `${API_BASE}/v1/probes?validator=${v.address}&limit=1000`}>{evFilter === "failed" ? "Every failed row →" : "Full history →"}</a>
          </div>
        </div>
        <div className="tablewrap">
          <table className="marks">
            <thead><tr><th>Started</th><th>Blob</th><th>Point</th><th>Verdict</th><th>Outcome</th><th className="num">Rows</th><th className="num">ms</th><th className="go" /></tr></thead>
            <tbody>
              {probes.length === 0 && <tr className="empty"><td colSpan={8}>No probe of this validator on record yet.</td></tr>}
              {probes.length > 0 && shown.length === 0 && <tr className="empty"><td colSpan={8}>{evFilter === "failed"
                ? <>No failed row among the newest {int(probes.length)}.{(o?.broken ?? 0) > 0 && <> The broken obligations in this period are older: <a href={failedHref}>failed rows in the API →</a></>}</>
                : <>Every one of the newest {int(probes.length)} rows got its shard back.</>}</td></tr>}
              {shown.map(({ p }) => {
                const sus = suspect.get(p.scheduled_at);
                // at a point the observer does not trust itself at, nothing is this validator's: no red, no verdict word
                const [word0, mk] = sus ? ["Not counted", "gone"] : probeWord(p);
                // a fault younger than the settling period counts, and can still be withdrawn
                const provisional = !sus && p.classification === "FAULT" && !!p.provisional;
                const word = provisional ? `${word0} · provisional` : word0;
                const notes = [
                  provisional && "provisional: younger than the settling period, so the rest of this schedule point or a params change not yet reconciled can still withdraw it; counted meanwhile",
                  p.attested === false && "no signature from this validator on this promise, so the probe is outside the rate",
                  p.retry_first_outcome && `first attempt ${p.retry_first_outcome}, retried once from the same location`,
                  p.host_changed && `the validator re-registered during the window: the upload went to ${p.host_at_settlement}${p.settlement_host_outcome ? `; asked as evidence, the old host answered ${p.settlement_host_outcome}${p.settlement_host_served ? " with the exact rows" : ""}` : ""}`,
                  p.amended_at && p.classification_at_probe && `filed as ${p.classification_at_probe} at the probe and judged ${p.classification} once every promise that could have answered was on record`,
                  p.rpc_code && `gRPC ${p.rpc_code}`,
                  p.shadowed_by && `answered from promise ${p.shadowed_by.slice(0, 10)}…`,
                  p.observer_build && `observer build ${p.observer_build}`,
                ].filter(Boolean).join(" · ");
                return (
                  <tr key={`${p.vantage}|${p.promise_hash}|${p.scheduled_at}`} className={sus ? "suspect" : p.classification === "FAULT" ? "fault-row" : undefined}
                    title={sus ? `At this point ${sus} of the validators probed failed at once. From one location that cannot be told from this observer's own network, so nothing at this point counts in any figure. Filed as ${p.classification.toLowerCase().replace(/_/g, " ")}.` : notes || undefined}>
                    <td title={utcWord(p.started_at)}>{hhmmss(p.started_at)}<span className="soft"> · {dateUTC(p.started_at)}</span></td>
                    <td><Link className="mono" href={`/blob/?hash=${p.promise_hash}`}>{p.promise_hash.slice(0, 10)}…</Link></td>
                    <td>{p.schedule_label} <span className="soft">· {PHASE[p.phase] ?? p.phase.replace("_", " ")}</span></td>
                    <td title={p.classification_reason || undefined}><span className={"mk " + mk} /> <span className={"word" + (mk === "fault" ? " fault" : "")}>{word}</span></td>
                    <td className="soft">{p.outcome.toLowerCase().replace(/_/g, " ")}{p.classification === "FAULT" && !sus && p.raw_error && <> · <code>{p.raw_error}</code></>}{p.attested === false && <> · unsigned</>}</td>
                    <td className="num">{p.rows_expected ? `${int(p.rows_returned)} / ${int(p.rows_expected)}` : "—"}</td>
                    <td className="num">{int(p.total_duration_ms)}</td>
                    <td className="go"><Link href={`/blob/?hash=${p.promise_hash}`} aria-label="open the blob">→</Link></td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      </section>
    </>
  );
}

/**
 * What "signed" means, one tap away. The same sentence as the overview
 * table's Signed column, so the two pages never explain one figure two ways:
 * a missing signature is the quorum rule working, not the validator failing.
 */
function SignedInfo() {
  return (
    <Info label="Signed">
      <p>Settled promises in this period that assigned this validator rows and carry its verified signature.</p>
      <p>Publishers stop collecting signatures at two thirds of stake, so roughly a third of validators miss any given promise by design. A low rate is normal; only a sustained 0 with a reachable host is worth a look.</p>
    </Info>
  );
}

export default function ValidatorPage() {
  return <Suspense fallback={<p className="crumb" style={{ paddingTop: 22 }}>Loading…</p>}><Page /></Suspense>;
}

/**
 * The newest handshake with this validator's Fibre host, stage by stage:
 * what an operator setting up a server needs, and cannot see from their own
 * machine — whether the name resolves, the port opens, TLS completes and the
 * certificate carries this validator's key, and the error it stopped on.
 */
function EndpointCheckLine({ c }: { c: EndpointCheck }) {
  const stages: [string, boolean, string][] = [
    ["DNS", c.dns_ok, ""],
    ["TCP", c.tcp_ok, c.tcp_ok ? `${c.tcp_ms} ms` : ""],
    ["TLS", c.tls_ok, c.tls_ok ? `${c.tls_ms} ms` : ""],
    ["identity", c.identity_ok, c.identity_ok ? "" : c.identity_reason ?? ""],
  ];
  // stages after the first failure were never reached; say so rather than "failed"
  const first = stages.findIndex(([, ok]) => !ok);
  return (
    <p className="errs endpoint-check">
      Endpoint check <b>{whenUTC(c.at)}</b> · <span className="mono">{c.host}</span> ·{" "}
      {stages.map(([name, ok, note], i) => (
        <span key={name} className={"stage " + (ok ? "ok" : first >= 0 && i > first ? "skip" : "bad")}>
          {name} {ok ? "✓" : first >= 0 && i > first ? "–" : "✗"}{note && <> {note}</>}{i < stages.length - 1 && " · "}
        </span>
      ))}
      {c.raw_error && <><br /><code>{c.raw_error}</code></>}
    </p>
  );
}
