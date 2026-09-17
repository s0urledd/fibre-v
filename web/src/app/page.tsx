"use client";
import { useState } from "react";
import Link from "next/link";
import { useApi, type Network, type Validator, type Meta, fmtCount, fmtPct, bytes, utc, ago } from "@/lib/api";
import ValidatorTable from "@/components/ValidatorTable";
import Tile from "@/components/Tile";
import { Mark } from "@/components/Verdict";
import { boundTitle } from "@/components/Rate";

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

  const notLive = !!(meta?.app_version && !meta.fibre_active);
  const noPubs = !!meta && meta.counts.Publications === 0;
  const faults = net?.classes?.FAULT ?? 0;
  const list = vals?.validators ?? [];
  const faulted = list.filter((v) => (v.classes.FAULT ?? 0) > 0).length;
  const busy = loading && !net;

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
          value={!sr || !rated ? "—" : fmtPct(sr)}
          tone={!rated ? "absent" : undefined}
          sub={!net ? undefined : !rated ? "nothing rated in this window"
            : <span title={boundTitle(sr!, net.serve_rate_by_obligation)}>{fmtCount(sr!)} probes · {net.serve_rate_by_obligation.den.toLocaleString("en-US")} obligations</span>}
          info={<>
            <p>Shards handed over, out of the shards validators had signed for on chain. Only probes taken inside the retention window count.</p>
            <p>Unreachable, unproven and unregistered cases are listed separately and are not in this number.</p>
            <p><Link href="/methodology/#verdicts">How a probe is judged</Link></p>
          </>} />
        <Tile label="Faults" loading={busy}
          value={!net ? "—" : faults > 0 ? <><Mark tier="fault" />{faults.toLocaleString("en-US")}</> : rated ? "none" : "—"}
          tone={faults > 0 ? "fault" : "absent"}
          sub={!net ? undefined : faults > 0 ? `across ${faulted} validator${faulted === 1 ? "" : "s"}` : rated ? "no signed shard went unserved" : "nothing rated"}
          info={<>
            <p>The validator answered but did not hand over a shard it had signed for.</p>
            <p>This is the only number counted against a validator.</p>
          </>} />
        <Tile label="Uptime" loading={busy}
          value={net?.reachability_window?.den ? fmtPct(net.reachability_window) : "—"}
          tone={net?.reachability_window?.den ? undefined : "absent"}
          sub={net?.reachability_window?.den ? `${net.reachability_window.den.toLocaleString("en-US")} handshakes` : "no handshake yet"}
          info={<p>TLS handshakes completed, over handshakes attempted. We open a connection to every registered Fibre endpoint every 10 minutes and verify the certificate its consensus key endorsed; nothing is downloaded.</p>} />
        <Tile label="Endpoints" loading={busy}
          value={net ? net.registered_endpoints.toLocaleString("en-US") : "—"}
          sub={net ? `${net.reachability.num} answering now · ${net.validators_probed} probed` : undefined} />
        <Tile label="Publications" loading={busy}
          value={net ? net.publications.toLocaleString("en-US") : "—"}
          sub={net ? `${bytes(net.publication_bytes)} uploaded` : undefined} />
        <Tile label="Recoverable" loading={busy}
          value={recon && recon.recoverable.den > 0 ? fmtPct(recon.recoverable) : "—"}
          tone={recon && recon.recoverable.den > 0 ? undefined : "absent"}
          sub={recon && recon.recoverable.den > 0 ? `${recon.recoverable.num} of ${recon.recoverable.den} blobs · ${recon.rate.num} fully served` : "no blob judged yet"}
          info={<>
            <p>Blobs that could be rebuilt from the rows we fetched at the last probe inside the window.</p>
            <p>Fully served: every validator that signed for the blob answered. Recoverable: enough rows came back, whoever answered.</p>
          </>} />
      </div>

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
                  : `${meta!.counts.OpenEndpoints} validators have registered an endpoint. A TLS handshake is attempted with each every 10 minutes.`}
              </>
            )}
          </p>
        </div>
      )}

      {net?.vantage_health?.correlated && (
        <div className="note hold">
          <span className="label">Possible problem on our side</span>
          <p>
            At {utc(net.vantage_health.at)} ({net.vantage_health.label}), {fmtCount(net.vantage_health.worst_point)} validators were unreachable at the same time.
            That pattern usually means a network problem at the observer. It is not counted against anyone.
          </p>
        </div>
      )}

      <div className="section-head" id="validators" style={{ marginTop: "var(--s6)" }}>
        <h2 style={{ margin: 0 }}>Validators</h2>
        <span className="sample">{win} window</span>
        <span className="spacer" />
        {net && recon && <Link href="/blobs/" className="sample">all publications</Link>}
      </div>
      {vals
        ? <ValidatorTable rows={vals.validators} notLive={notLive} />
        : <p className="muted">Loading validators…</p>}
    </>
  );
}
