"use client";
import { useState } from "react";
import Link from "next/link";
import { useApi, type Network, type Validator, type Meta, type Market, fmtCount, fmtPct, bytes, utc, ago, tia } from "@/lib/api";
import ValidatorTable from "@/components/ValidatorTable";
import { Panel, Cell } from "@/components/Panel";
import { Mark } from "@/components/Verdict";
import { boundTitle, band } from "@/components/Rate";

const WINDOWS = ["24h", "7d", "30d", "all"];

/**
 * The overview: six network figures, then the validator table. Everything
 * else (per-point rates, latency, the census bar) lives on the validator and
 * methodology pages.
 */
export default function Overview() {
  const [win, setWin] = useState("24h");
  const { data: meta } = useApi<Meta>("/v1/meta");
  const { data: net, error: netErr, loading } = useApi<Network>(`/v1/network?window=${win}`);
  const { data: vals } = useApi<{ validators: Validator[] }>(`/v1/validators?window=${win}`);
  const { data: market } = useApi<Market>(`/v1/market?window=${win}`);

  const notLive = !!(meta?.app_version && !meta.fibre_active);
  const noPubs = !!meta && meta.counts.Publications === 0;
  const faults = net?.faults ?? net?.classes?.FAULT ?? 0;
  const list = vals?.validators ?? [];
  const faulted = list.filter((v) => (v.faults ?? v.classes.FAULT ?? 0) > 0).length;
  const busy = loading && !net;

  const sr = net?.serve_rate;
  const rated = !!sr && sr.den > 0;
  const suspect = net?.vantage_health?.suspect ?? [];
  const incidents = suspect.filter((s) => s.reason.includes("fault"));
  const ourSide = suspect.filter((s) => !s.reason.includes("fault"));
  const ob = net?.obligations;
  const decided = !!ob && ob.rate.den > 0;

  return (
    <>
      <div className="head-row">
        <h1 className="sr-only">Network</h1>
        {net?.computed_at && (
          <span className="sample" title={`${net.window.start && !net.window.start.startsWith("0001-") ? `${utc(net.window.start)} → now` : "since the first record"}. Snapshot taken ${utc(net.computed_at)}, computed in ${net.compute_ms} ms.`}>
            updated {ago(net.computed_at)}
          </span>
        )}
        <span className="spacer" />
        <div className="pills" role="group" aria-label="window">
          {WINDOWS.map((w) => <button key={w} aria-pressed={win === w} onClick={() => setWin(w)}>{w}</button>)}
        </div>
      </div>

      {netErr && <div className="note hold"><span className="label">Observer</span><p>Cannot reach the observer API: {netErr}. Nothing below is current.</p></div>}
      {meta && meta.health !== "ok" && (
        <div className="note hold">
          <span className="label">{meta.health === "down" ? "Observer is not running" : "Observer degraded"}</span>
          <p>
            {meta.components.filter((c) => !c.alive).map((c) => `${c.component}: ${c.present ? (c.stopped_at ? `stopped (${c.stop_reason || "?"})` : `no update for ${Math.round(c.age_s / 60)} min`) : "never started"}`).join(" · ")}
            {meta.components.some((c) => !c.alive) && meta.components.some((c) => c.alive && !c.ok) ? " · " : ""}
            {meta.components.filter((c) => c.alive && !c.ok).map((c) => `${c.component}: ${c.last_error || "failing"}`).join(" · ")}
{". "}Figures from a stopped process stay on the page as they were; the sample counts say how old they are.
          </p>
        </div>
      )}
      {meta && (meta.scan_gaps?.length ?? 0) > 0 && (
        <div className="note hold">
          <span className="label">Blocks this observer could not read</span>
          <p>{meta.scan_gaps!.map((g) => g.from === g.to ? `#${g.from.toLocaleString("en-US")}` : `#${g.from.toLocaleString("en-US")}–${g.to.toLocaleString("en-US")}`).join(", ")}: the RPC node could not serve them (pruned, or ABCI responses discarded). A publication settled in one of them is unknown here and was never probed.</p>
        </div>
      )}
      {meta && meta.pin_status === "chain_ahead" && (
        <div className="note hold">
          <span className="label">Chain upgraded past this build</span>
          <p>The chain runs app version {meta.app_version}; this observer&rsquo;s row-assignment constants are pinned to an earlier major. Until the pin is bumped, which validator holds which rows may be computed wrongly. {meta.unassignable_publications > 0 ? `${meta.unassignable_publications.toLocaleString("en-US")} publication(s) already have no assignment and are not probed.` : ""}</p>
        </div>
      )}

      <Panel title="Service" live={!!net && meta?.health === "ok"} right={net ? <>{win === "all" ? "since the first record" : `${win} window`} · <Link href="/methodology/#verdicts">methodology →</Link></> : undefined}>
        <div className="cells three">
          <Cell label="Serve rate" loading={busy}
            value={!ob || !decided ? "—" : fmtPct(ob.rate)}
            tone={!decided ? "absent" : band(ob!.rate, "serve") === "ok" ? "ok" : band(ob!.rate, "serve") === "fault" ? "fault" : undefined}
            sub={!net ? undefined : !decided ? (ob && ob.unobserved > 0 ? `${ob.unobserved.toLocaleString("en-US")} unobserved` : "nothing decided")
              : `${ob!.served.toLocaleString("en-US")} / ${(ob!.served + ob!.broken).toLocaleString("en-US")} kept`}
            detail={!net || !ob ? undefined : !decided ? (ob.unobserved > 0 ? `${ob.unobserved.toLocaleString("en-US")} obligations in the window, none observed serving.` : "No obligation decided in this window.")
              : `${ob.served.toLocaleString("en-US")} of ${(ob.served + ob.broken).toLocaleString("en-US")} obligations kept${ob.unobserved > 0 ? `; ${ob.unobserved.toLocaleString("en-US")} never observed serving, counted beside the rate` : ""}. ${boundTitle(ob.rate, ob.rate) ?? ""} Per probe: ${fmtPct(sr)} over ${fmtCount(sr)}.`}
            info={<>
              <p>Obligations kept: a shard a validator signed for on chain, handed over at the last probe before its retention deadline. One observation per shard, not per probe.</p>
              <p>An obligation we never saw served and never saw broken is counted beside the rate, not inside it. Unreachable, unproven and unregistered cases are never faults.</p>
              <p><Link href="/methodology/#verdicts">How a probe is judged</Link></p>
            </>} />
          <Cell label="Faults" loading={busy}
            value={!net ? "—" : faults > 0 ? faults.toLocaleString("en-US") : rated ? "0" : "—"}
            tone={faults > 0 ? "fault" : "absent"}
            sub={!net ? undefined : faults > 0 ? <><b>{ob!.broken.toLocaleString("en-US")} broken</b> · {faulted} validator{faulted === 1 ? "" : "s"}</> : rated ? "no unserved shard" : "nothing rated"}
            detail={!net ? undefined : faults > 0 ? `${faults.toLocaleString("en-US")} probe${faults === 1 ? "" : "s"} where a validator answered but did not hand over a shard it had signed for; ${ob!.broken.toLocaleString("en-US")} obligations broken across ${faulted} validator${faulted === 1 ? "" : "s"}.` : rated ? "No signed shard went unserved in this window." : "Nothing rated in this window."}
            info={<>
              <p>The validator answered but did not hand over a shard it had signed for.</p>
              <p>This is the only number counted against a validator.</p>
            </>} />
          <Cell label="Reachability" loading={busy}
            value={net?.reachability_window?.den ? fmtPct(net.reachability_window) : "—"}
            tone={net?.reachability_window?.den ? undefined : "absent"}
            sub={net?.reachability_window?.den ? `${net.reachability_window.den.toLocaleString("en-US")} handshakes` : "no handshake yet"}
            info={<>
              <p>TLS handshakes completed, over handshakes attempted: one every 5 minutes with every registered Fibre endpoint, from one location. Nothing is downloaded.</p>
              <p>This is not signing uptime. A validator can sign every block with its Fibre endpoint down, and the reverse.</p>
            </>} />
        </div>
      </Panel>

      <Panel title="Chain" right={<>from the chain&rsquo;s own records · <Link href="/publishers/">publishers →</Link></>}>
        <div className="cells four">
          <Cell label="Publications" loading={busy}
            value={net ? net.publications.toLocaleString("en-US") : "—"}
            sub={net ? `${bytes(net.publication_bytes)} uploaded` : undefined} />
          <Cell label="Signed shards" loading={busy}
            value={net?.attestation?.blob_coverage?.den ? fmtPct(net.attestation.blob_coverage) : "—"}
            tone={net?.attestation?.blob_coverage?.den ? undefined : "absent"}
            sub={net?.attestation?.blob_coverage?.den ? `${net.attestation.attested_blobs.toLocaleString("en-US")} / ${net.attestation.blob_coverage.den.toLocaleString("en-US")} · two-thirds quorum` : "no assignment yet"}
            detail={net?.attestation?.blob_coverage?.den ? `${net.attestation.attested_blobs.toLocaleString("en-US")} of ${net.attestation.blob_coverage.den.toLocaleString("en-US")} assigned shards carry their validator's signature on chain. Publishers stop collecting signatures at two thirds of stake, so this is the size of the quorum in practice, not a duty anyone missed.` : undefined}
            info={<>
              <p>Assigned shards whose validator&rsquo;s signature reached the chain. Only these are proven stored and count in the serve rate.</p>
              <p>Publishers stop collecting signatures at two thirds of stake, so this is the size of the quorum in practice, not a duty anyone missed.</p>
            </>} />
          <Cell label="Endpoints" loading={busy}
            value={net ? net.registered_endpoints.toLocaleString("en-US") : "—"}
            sub={net ? `${net.reachability.num} answering now · ${net.validators_probed} probed` : undefined}
            detail={net ? `${net.registered_endpoints.toLocaleString("en-US")} registered Fibre endpoints; ${net.reachability.num} answering the latest handshake; ${net.validators_probed} probed in this window.` : undefined} />
          <Cell label="Fees settled" loading={busy}
            value={market ? tia(market.fees_settled_utia, { unit: false }) : "—"} unit={market ? "TIA" : undefined}
            tone={market && market.settlements === 0 ? "absent" : undefined}
            sub={market ? `${market.publishers_active} publisher${market.publishers_active === 1 ? "" : "s"}${market.timeouts > 0 ? ` · ${market.timeouts} timed out` : ""}` : undefined}
            info={<>
              <p>What publishers paid for the blobs settled in this window, from the chain&rsquo;s own records. Not a measurement of ours.</p>
              <p><Link href="/publishers/">Publishers</Link></p>
            </>} />
        </div>
      </Panel>

      {net?.rolled_up && (
        <p className="muted rolled">
          Rolled up after 90 days: figures before {net.rolled_up.raw_from} come from the daily per-validator rollup ({net.rolled_up.days.toLocaleString("en-US")} days); latency, by-point, attestation and recoverability cover the raw rows from then on.
        </p>
      )}
      {noPubs && (
        <div className="note">
          <span className="label">{notLive ? "Fibre is not live on this chain yet" : "Nothing to measure yet"}</span>
          <p>
            {notLive ? (
              <>
                {meta!.chain_id || "This chain"} is on app version {meta!.app_version}. Fibre arrives with version {meta!.fibre_app_version || "10"}.
                The observer is following the chain at height {Number(meta!.chain_height || meta!.last_scanned_height || 0).toLocaleString("en-US")} and starts measuring after the upgrade.
                {list.length > 0 && ` The ${list.length} bonded validators below come from the staking module.`}
              </>
            ) : (
              <>
                No Fibre publication recorded yet. Collector at height {meta!.last_scanned_height || "?"} on {meta!.chain_id || "?"}.{" "}
                {meta!.counts.OpenEndpoints === 0
                  ? "Fibre is live; no validator has registered an endpoint yet."
                  : `${meta!.counts.OpenEndpoints} validators have registered an endpoint. A TLS handshake is attempted with each every 5 minutes.`}
              </>
            )}
          </p>
        </div>
      )}

      {net && ourSide.length > 0 && (
        <div className="note hold">
          <span className="label">Problem on our side at {ourSide.length} probe point{ourSide.length === 1 ? "" : "s"}</span>
          <p>
            {ourSide.slice(0, 3).map((s) => `${utc(s.at)} (${s.label}): ${fmtCount(s.unreachable)} validators unreachable at once`).join("; ")}
            {ourSide.length > 3 ? `; and ${ourSide.length - 3} more` : ""}.
            {" "}Validators fail independently; half the set unreachable at the same minute is a network problem at the observer.
            The probes at these points are left out of every rate, bucket and fault count on this site.
          </p>
        </div>
      )}
      {net && incidents.length > 0 && (
        <div className="note hold">
          <span className="label">Network incident: {incidents.length} probe point{incidents.length === 1 ? "" : "s"} where half the set faulted at once</span>
          <p>
            {incidents.slice(0, 4).map((s, i) => (
              <span key={s.at}>{i > 0 ? "; " : ""}{utc(s.at)} ({s.label}): {fmtCount(s.fault)} validators faulted <a href={`/api/v1/probes?at=${encodeURIComponent(s.at)}&limit=1000`}>rows</a></span>
            ))}
            {incidents.length > 4 ? `; and ${incidents.length - 4} more` : ""}.
            {" "}This observer&rsquo;s assignment pin matched the chain at the time, so these faults are not an observer error;
            operators losing data at the same minute points at the network, a release, or the observer&rsquo;s coder. No validator&rsquo;s rate counts these points; the rows are kept and linked.
          </p>
        </div>
      )}

      <Panel title={<span id="validators">Validator set</span>} className="validators"
        right={list.length > 0 ? <>{list.length} bonded · {list.filter((v) => !!v.host).length} with a Fibre endpoint · <Link href="/blobs/">all publications →</Link></> : undefined}>
        {vals
          ? <ValidatorTable rows={vals.validators} notLive={notLive} />
          : <p className="muted" style={{ padding: "var(--s4)" }}>Loading validators…</p>}
      </Panel>
    </>
  );
}
