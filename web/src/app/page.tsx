"use client";
import { useState } from "react";
import Link from "next/link";
import { useApi, type Network, type Validator, type Meta, fmtCount, fmtRate, bytes, utc, ago, enoughToRank } from "@/lib/api";
import ValidatorTable from "@/components/ValidatorTable";
import Tile from "@/components/Tile";
import Graduation from "@/components/Graduation";
import { Mark } from "@/components/Verdict";
import Info from "@/components/Info";
import { boundTitle } from "@/components/Rate";

const WINDOWS = ["24h", "7d", "30d", "all"];

/**
 * The overview, in the order a reader asks: did the network serve (rate,
 * faults), is it up (uptime, endpoints), what did it carry (publications,
 * recoverable), then who — the validator table.
 */
export default function Overview() {
  const [win, setWin] = useState("24h");
  const { data: meta } = useApi<Meta>("/v1/meta");
  const { data: net, error: netErr, loading } = useApi<Network>(`/v1/network?window=${win}`);
  const { data: vals } = useApi<{ validators: Validator[] }>(`/v1/validators?window=${win}`);

  const notLive = !!(meta?.app_version && !meta.fibre_active);
  const noPubs = !!meta && meta.counts.Publications === 0;
  const faults = net?.classes?.FAULT ?? 0;
  const list = vals?.validators ?? [];
  const faulted = list.filter((v) => (v.classes.FAULT ?? 0) > 0).length;
  const busy = loading && !net;

  // Census of validators by their state now, each in exactly one segment.
  const unusable = (v: Validator) => v.reachable === false || v.identity_status === "mismatch" || v.identity_status === "no_tls";
  const seg = {
    fault: faulted,
    hold: list.filter((v) => (v.classes.FAULT ?? 0) === 0 && unusable(v)).length,
    gap: list.filter((v) => (v.classes.FAULT ?? 0) === 0 && !unusable(v) && v.probe_count === 0).length,
    unproven: list.filter((v) => (v.classes.FAULT ?? 0) === 0 && !unusable(v) && v.probe_count > 0 && v.serve_rate.den === 0).length,
  };
  const served = Math.max(0, list.length - seg.fault - seg.hold - seg.gap - seg.unproven);
  const pct = (n: number) => (list.length ? (n / list.length) * 100 : 0);

  const sr = net?.serve_rate;
  const rated = !!sr && sr.den > 0;
  const recon = net?.reconstructable;

  return (
    <>
      <div className="section-head">
        <h1>Network</h1>
        <span className="sample">
          {net && (net.window.start && !net.window.start.startsWith("0001-") ? `${utc(net.window.start)} → now` : "since the first record")}
          {net?.computed_at && <span title={`Snapshot taken ${utc(net.computed_at)}, computed in ${net.compute_ms} ms.`}> · taken {ago(net.computed_at)}</span>}
        </span>
        <span className="spacer" />
        <div className="pills" role="group" aria-label="window">
          {WINDOWS.map((w) => <button key={w} aria-pressed={win === w} onClick={() => setWin(w)}>{w}</button>)}
        </div>
      </div>

      {netErr && <div className="note hold"><span className="label">Observer</span><p>Cannot reach the observer API: {netErr}. Nothing below is current.</p></div>}

      <div className="tiles">
        <Tile hero label="Serve rate" loading={busy}
          value={!sr ? "—" : !rated ? "—" : enoughToRank(sr) ? fmtRate(sr) : fmtCount(sr)}
          tone={!rated ? "absent" : undefined}
          sub={!net ? undefined : !rated ? "nothing rated in this window"
            : <span title={boundTitle(sr!, net.serve_rate_by_obligation)}>{fmtCount(sr!)} probes · {net.serve_rate_by_obligation.den.toLocaleString("en-US")} obligations{!enoughToRank(sr) && " · under floor"}</span>}
          info={<>
            <p>Shards handed over, out of the shards the chain proves a validator stored, over probes taken while the obligation held.</p>
            <p>Everything the rate does not speak for — unreachable, unproven, unregistered — is shown under its own name, never folded in.</p>
            <p><Link href="/methodology/#verdicts">How a verdict is reached →</Link></p>
          </>} />
        <Tile label="Faults" loading={busy}
          value={!net ? "—" : faults > 0 ? <><Mark tier="fault" />{faults.toLocaleString("en-US")}</> : rated ? "none" : "—"}
          tone={faults > 0 ? "fault" : "absent"}
          sub={!net ? undefined : faults > 0 ? `across ${faulted} validator${faulted === 1 ? "" : "s"}` : rated ? "no proven shard went unserved" : "nothing rated"}
          info={<>
            <p><strong>A fault is two things at once:</strong> the observer reached the validator, and it failed to hand over a shard the chain proves it stored.</p>
            <p>The only count held against anyone.</p>
          </>} />
        <Tile label="Uptime" loading={busy}
          value={net?.reachability_window?.den ? fmtRate(net.reachability_window) : "—"}
          tone={net?.reachability_window?.den ? undefined : "absent"}
          sub={net?.reachability_window?.den ? `${net.reachability_window.den.toLocaleString("en-US")} heartbeats` : "no heartbeat yet"}
          info={<p>Heartbeats that completed TLS with a registered endpoint, over all heartbeats sent. Every endpoint is dialled every 10 minutes whether or not anything was assigned.</p>} />
        <Tile label="Endpoints" loading={busy}
          value={net ? net.registered_endpoints.toLocaleString("en-US") : "—"}
          sub={net ? `${net.reachability.num} reachable now · ${net.validators_probed} probed` : undefined}
          info={<p>Validators with a Fibre host in <code>x/valaddr</code>. Reachable now is the newest heartbeat or probe per endpoint that completed TLS.</p>} />
        <Tile label="Publications" loading={busy}
          value={net ? net.publications.toLocaleString("en-US") : "—"}
          sub={net ? `${bytes(net.publication_bytes)} uploaded` : undefined}
          info={<p>Every <code>MsgPayForFibre</code> settled on chain in the window, and the padded upload size they add up to.</p>} />
        <Tile label="Recoverable" loading={busy}
          value={recon && recon.recoverable.den > 0 ? fmtRate(recon.recoverable) : "—"}
          tone={recon && recon.recoverable.den > 0 ? undefined : "absent"}
          sub={recon && recon.recoverable.den > 0 ? `${recon.recoverable.num} of ${recon.recoverable.den} blobs · ${recon.rate.num} fully served` : "no blob judged yet"}
          info={<>
            <p>Blobs that could still be rebuilt from the rows that came back at the last in-window probe, over blobs with a verdict.</p>
            <p>Fully served means every validator the chain proves owed the blob answered. Recoverable only needs enough rows: any honest third of the stake holds enough.</p>
          </>} />
      </div>

      {net && (
        <p className="coverage">
          The serve rate speaks for {fmtCount(net.serve_rate_coverage)} in-window probes of an assigned shard.
          {net.serve_latency_p50_ms != null && <> A shard came back in {net.serve_latency_p50_ms.toLocaleString("en-US")} ms typically, {(net.serve_latency_p95_ms ?? 0).toLocaleString("en-US")} ms at p95 ({net.serve_latency_sample.toLocaleString("en-US")} probes).</>}
          <Info label="What the rate leaves out">
            <p><strong>Unattested</strong> — the chain proves no obligation. <strong>Unreachable</strong> — we could not complete a conversation. <strong>Not registered</strong> — no Fibre host on chain. <strong>Shadowed shard</strong> — another promise’s rows for the same blob. <strong>Identity expired</strong> — right key, lapsed certificate.</p>
            <p><Link href="/methodology/#what-this-excludes">Each one, and why →</Link></p>
          </Info>
        </p>
      )}

      {noPubs && (
        <div className="note">
          <span className="label">{notLive ? "Fibre is not live on this chain yet" : "Nothing to measure yet"}</span>
          <p>
            {notLive ? (
              <>
                {meta!.chain_id || "This chain"} runs app version {meta!.app_version}; Fibre needs {meta!.fibre_app_version || "10"}, so <code>x/fibre</code> and <code>x/valaddr</code> do not exist yet.
                The observer is following the chain at height {Number(meta!.chain_height || meta!.last_scanned_height || 0).toLocaleString("en-US")} and starts measuring at the upgrade.
                {list.length > 0 && ` The ${list.length} bonded validators below are listed by name from the staking module.`}
              </>
            ) : (
              <>
                No Fibre publication recorded yet. Collector at height {meta!.last_scanned_height || "?"} on {meta!.chain_id || "?"}.{" "}
                {meta!.counts.OpenEndpoints === 0
                  ? "Fibre is live and no validator has registered an endpoint yet."
                  : `${meta!.counts.OpenEndpoints} validators have registered an endpoint; reachability is checked every 10 minutes.`}
              </>
            )}
          </p>
        </div>
      )}

      {net?.vantage_health?.correlated && (
        <div className="note hold">
          <span className="label">This observer’s own measurement</span>
          <p>
            At {utc(net.vantage_health.at)} ({net.vantage_health.label}), {fmtCount(net.vantage_health.worst_point)} of the validators probed were unreachable at once.
            Validators fail independently; one network does not. None of it counts against any validator.
          </p>
        </div>
      )}

      {net && list.length > 0 && (
        <section className="card">
          <div className="card-head">
            <h2>Validators by state</h2>
            <span className="sample">{list.length} in the table · as of the newest heartbeat</span>
          </div>
          <div className="bar" role="img"
            aria-label={`${served} serving, ${seg.hold} unreachable or unendorsed, ${seg.unproven} nothing proven owed, ${seg.gap} not probed, ${seg.fault} faulted`}>
            {served > 0 && <i className="seg--served" style={{ width: `${pct(served)}%` }} />}
            {seg.hold > 0 && <i className="seg--hold" style={{ width: `${pct(seg.hold)}%` }} />}
            {seg.unproven > 0 && <i className="seg--unproven" style={{ width: `${pct(seg.unproven)}%` }} />}
            {seg.gap > 0 && <i className="seg--gap" style={{ width: `${pct(seg.gap)}%` }} />}
            {seg.fault > 0 && <i className="seg--fault" style={{ width: `${pct(seg.fault)}%` }} />}
          </div>
          <ul className="bar-key">
            <li><Mark tier="kept" /> <b>{served}</b> serving</li>
            <li title="Did not answer, or answered with a certificate its consensus key does not endorse."><Mark tier="hold" /> <b>{seg.hold}</b> unreachable or unendorsed</li>
            <li title="Probed, but the chain proves no obligation in this window."><Mark tier="held" /> <b>{seg.unproven}</b> nothing proven owed</li>
            <li><Mark tier="gap" /> <b>{seg.gap}</b> not probed</li>
            <li><Mark tier="fault" /> <b>{seg.fault}</b> faulted</li>
          </ul>
        </section>
      )}

      {net?.serve_rate_by_point && net.serve_rate_by_point.some((p) => p.serve_rate.den > 0) && (
        <section className="card">
          <Graduation points={net.serve_rate_by_point} />
        </section>
      )}

      <div className="section-head" id="validators" style={{ marginTop: "var(--s6)" }}>
        <h2 style={{ margin: 0 }}>Validators</h2>
        <span className="sample">{win} window</span>
        <span className="spacer" />
        {net && recon && <Link href="/blobs/" className="sample">every publication →</Link>}
      </div>
      {vals
        ? <ValidatorTable rows={vals.validators} notLive={notLive}
            caption={notLive
              ? `Every bonded validator on ${meta?.chain_id || "this chain"}, from the staking module.`
              : `Sorted worst first: faults, then unreachable, then partial outages. Below ${20} rated observations a validator is listed with its counts and not ranked.`} />
        : <p className="muted">Loading validators…</p>}
    </>
  );
}
