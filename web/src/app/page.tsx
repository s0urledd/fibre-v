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
          <div className="tile"><div className="k">Serve rate</div><div className="v mono">{fmtRate(net.serve_rate)}</div><div className="d">healthy {fmtCount(net.serve_rate)} of healthy+fault, assigned, in window</div></div>
          <div className="tile"><div className="k">Proven obliged</div><div className="v mono">{fmtRate(net.attestation?.coverage)}</div><div className="d">{fmtCount(net.attestation?.coverage)} probes where the promise proves the validator stored the shard</div></div>
          <div className="tile"><div className="k">Reconstructable</div><div className="v mono">{fmtRate(net.reconstructable)}</div><div className="d">{fmtCount(net.reconstructable)} publications at their last in-window probe</div></div>
          <div className="tile"><div className="k">Publications</div><div className="v mono">{net.publications.toLocaleString("en-US")}</div><div className="d">{bytes(net.publication_bytes)} padded upload size</div></div>
          <div className="tile"><div className="k">Probes</div><div className="v mono">{net.probe_count.toLocaleString("en-US")}</div><div className="d">{net.probe_gaps} not probed or probe error (gaps)</div></div>
        </div>
      )}
      {net && (
        <p className="muted">
          Verdicts in window (assigned, in-window and grace): {Object.entries(net.classes).sort().map(([k, v]) => `${k.toLowerCase().replace(/_/g, " ")} ${v}`).join(" · ") || "none"}. Serve rate = <RateCell r={net.serve_rate} />.
        </p>
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
      <h2>Validators</h2>
      {vals ? <ValidatorTable rows={vals.validators} caption={`Every validator with a registered Fibre endpoint or at least one probe. Rates over the ${win} window; counts are probes.`} /> : <p className="muted">Loading validators…</p>}
      <Legend />
    </>
  );
}
