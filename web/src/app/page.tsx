"use client";
import { useState } from "react";
import { useApi, type Network, type Validator, type Meta, fmtRate, fmtCount, bytes, utc } from "@/lib/api";
import ValidatorTable from "@/components/ValidatorTable";
import RateCell from "@/components/Rate";
import { Legend } from "@/components/Badge";

const WINDOWS = ["24h", "7d", "30d", "all"];

export default function Overview() {
  const [win, setWin] = useState("24h");
  const { data: meta } = useApi<Meta>("/v1/meta");
  const { data: net, error: netErr, loading } = useApi<Network>(`/v1/network?window=${win}`);
  const { data: vals } = useApi<{ validators: Validator[] }>(`/v1/validators?window=${win}`);

  const noPubs = meta && meta.counts.Publications === 0;
  return (
    <>
      <h1>Network overview</h1>
      <p className="muted">
        Does each validator keep serving the Fibre shards it signed for, for the whole retention window? Every number below is a count of stored probes; nothing is smoothed.
      </p>
      <div className="controls">
        <span className="muted">window:</span>
        {WINDOWS.map((w) => <button key={w} className={win === w ? "on" : ""} onClick={() => setWin(w)}>{w}</button>)}
        {net && <span className="faint mono">{net.window.start && !net.window.start.startsWith("0001-") ? utc(net.window.start) : "beginning"} → {utc(net.window.end)}</span>}
      </div>
      {netErr && <div className="notice err">Cannot reach the observer API: {netErr}</div>}
      {loading && !net && <p className="muted">Loading…</p>}
      {noPubs && (
        <div className="notice">
          <strong>0 Fibre publications recorded.</strong> Collector at height {meta.last_scanned_height || "?"}, following chain {meta.chain_id || "?"}.
          {meta.counts.OpenEndpoints === 0 ? " No validator has registered a Fibre endpoint yet (x/valaddr is empty or not active on this chain)." : ` ${meta.counts.OpenEndpoints} validators have registered a Fibre endpoint; reachability is probed every 10 minutes.`}
        </div>
      )}
      {net && (
        <div className="tiles">
          <div className="tile"><div className="k">Validators with a Fibre endpoint</div><div className="v mono">{net.registered_endpoints}</div><div className="d">{net.validators_probed} probed in window</div></div>
          <div className="tile"><div className="k">Reachable now</div><div className="v mono">{fmtRate(net.reachability)}</div><div className="d">{fmtCount(net.reachability)} endpoints, latest heartbeat or probe</div></div>
          <div className="tile">
            <div className="k">Serve rate</div>
            <div className="v mono">{fmtRate(net.serve_rate)}</div>
            <div className="d">{fmtCount(net.serve_rate)} probes where the validator answered, of those it was proven to owe, in window</div>
          </div>
          <div className="tile">
            <div className="k">Verdict coverage</div>
            <div className="v mono">{fmtRate(net.serve_rate_coverage)}</div>
            <div className="d">{fmtCount(net.serve_rate_coverage)} in-window probes of an owed shard that produced a verdict either way</div>
          </div>
          <div className="tile"><div className="k">Proven obliged</div><div className="v mono">{fmtRate(net.attestation?.coverage)}</div><div className="d">{fmtCount(net.attestation?.coverage)} probes where the promise proves the validator stored the shard</div></div>
          <div className="tile">
            <div className="k">Fully served</div>
            <div className="v mono">{fmtRate(net.reconstructable.rate)}</div>
            <div className="d">
              {fmtCount(net.reconstructable.rate)} publications where every validator proven to owe the blob served at the last complete in-window probe
              {net.reconstructable.degraded > 0 && <> · {net.reconstructable.degraded.toLocaleString("en-US")} degraded (rows all present, someone quiet)</>}
              {net.reconstructable.publications_examined < net.reconstructable.publications_in_window && (
                <> · over the newest {net.reconstructable.publications_examined.toLocaleString("en-US")} of {net.reconstructable.publications_in_window.toLocaleString("en-US")} in the window</>
              )}
            </div>
          </div>
          <div className="tile">
            <div className="k">Recoverable</div>
            <div className="v mono">{fmtRate(net.reconstructable.recoverable)}</div>
            <div className="d">{fmtCount(net.reconstructable.recoverable)} publications where enough distinct rows came back to rebuild the blob</div>
          </div>
          <div className="tile"><div className="k">Publications</div><div className="v mono">{net.publications.toLocaleString("en-US")}</div><div className="d">{bytes(net.publication_bytes)} padded upload size</div></div>
          <div className="tile"><div className="k">Probes</div><div className="v mono">{net.probe_count.toLocaleString("en-US")}</div><div className="d">{net.probe_gaps} not probed or probe error (gaps)</div></div>
        </div>
      )}
      {net && (
        <p className="muted">
          Verdicts in window (assigned, in-window and grace): {Object.entries(net.classes).sort().map(([k, v]) => `${k.toLowerCase().replace(/_/g, " ")} ${v}`).join(" · ") || "none"}. Serve rate = <RateCell r={net.serve_rate} obligations={net.serve_rate_by_obligation} />, counted over {net.serve_rate_by_obligation.den.toLocaleString("en-US")} obligations (one validator, one blob) rather than over probes, because the four in-window probes of one obligation are near copies of each other.
        </p>
      )}
      {net && net.vantage_health?.correlated && (
        <div className="notice err">
          <strong>Treat this window&rsquo;s reachability with suspicion.</strong>{" "}
          At {utc(net.vantage_health.at)} ({net.vantage_health.label}), {fmtCount(net.vantage_health.worst_point)} of the validators probed at
          that moment were unreachable at once. Validators fail independently; this site&rsquo;s own network does not. A whole set failing
          together is far more likely to be a route, resolver or peering problem here than that many operators going down at the same time.
          None of it counts against any validator, but the reachability figures on this page are not worth much until it is explained.
        </div>
      )}
      {net && net.serve_rate_held_out && Object.keys(net.serve_rate_held_out).length > 0 && (
        <div className="notice">
          <strong>What the serve rate does not say.</strong>{" "}
          A fault means this site reached the validator and it failed to hand over a shard the chain proves it stored. Everything else is
          counted under its own name and kept out of the rate, in both directions, because the evidence does not support the accusation:
          <ul>
            {net.serve_rate_excluded_classes
              .filter((e) => (net.serve_rate_held_out[e.class] ?? 0) > 0)
              .map((e) => (
                <li key={e.class}>
                  <strong>{e.class.toLowerCase().replace(/_/g, " ")}</strong> — {net.serve_rate_held_out[e.class].toLocaleString("en-US")} probes. {e.reason}.
                </li>
              ))}
          </ul>
        </div>
      )}
      {net && net.attestation && (net.attestation.unattested_probes > 0 || net.attestation.unknown_probes > 0) && (
        <div className="notice">
          <strong>The serve rate above speaks for {net.attestation.attested_probes.toLocaleString("en-US")} of {(net.attestation.attested_probes + net.attestation.unattested_probes + net.attestation.unknown_probes).toLocaleString("en-US")} probes.</strong>{" "}
          A validator is only obliged to serve a shard it stored, and the only on-chain proof it stored one is a signature on the
          settled promise that this observer verified against the validator&rsquo;s consensus key.
          {net.attestation.unattested_probes > 0 && <> {net.attestation.unattested_probes.toLocaleString("en-US")} probes carry no such proof: the publisher stops collecting
          signatures once it has a safe quorum, so a validator can hold a shard whose signature never reached the chain. Those probes are
          recorded as <em>unattested</em> and excluded from the rate in both directions, so neither a success nor a failure can move a number the
          validator was never proven to owe.</>}
          {net.attestation.unknown_probes > 0 && <> {net.attestation.unknown_probes.toLocaleString("en-US")} probes predate signature verification and are counted under the older rules.</>}
        </div>
      )}
      {net && net.serve_rate_by_point?.some((p) => p.serve_rate.den > 0) && (
        <p className="muted">
          By schedule point (the four in-window probes sit at 12%, 45%, 72% and 92% of each retention window, so they are not
          interchangeable):{" "}
          {net.serve_rate_by_point
            .filter((p) => p.serve_rate.den > 0)
            .map((p) => `${p.key} ${fmtRate(p.serve_rate)}`)
            .join(" · ")}
          . A rate that is fine early and poor late means shards pruned before the deadline; one that is poor throughout means
          something else. The pooled number above cannot tell those apart.
        </p>
      )}
      <h2>Validators</h2>
      {vals ? <ValidatorTable rows={vals.validators} caption={`Every validator with a registered Fibre endpoint or at least one probe. Rates over the ${win} window; counts are probes.`} /> : <p className="muted">Loading validators…</p>}
      <Legend />
    </>
  );
}
