"use client";
import { Suspense } from "react";
import Link from "next/link";
import { useSearchParams } from "next/navigation";
import { useApi, type Validator, type Probe, type Window, type Rate, type ClassCounts, utc, ago, shortHex } from "@/lib/api";
import Badge, { Legend } from "@/components/Badge";
import RateCell from "@/components/Rate";

type Detail = {
  validator: Validator;
  windows: { window: Window; serve_rate: Rate; rated_probe_count: number; classes: ClassCounts }[];
  recent_probes: Probe[];
};

function Page() {
  const addr = useSearchParams().get("addr") ?? "";
  const { data, error, loading } = useApi<Detail>(addr ? `/v1/validators/${addr}` : null);
  if (!addr) return <p className="notice">Open a validator from the <Link href="/">overview</Link>, or add <code>?addr=&lt;consensus address&gt;</code>.</p>;
  if (error) return <p className="notice err">{error}</p>;
  if (loading || !data) return <p className="muted">Loading…</p>;
  const v = data.validator;
  return (
    <>
      {/* The operator's own name first; the consensus address is the key
          every number below is joined on, so it stays directly underneath. */}
      <h1>{v.moniker || <span className="mono">{v.cons_address || v.address}</span>}</h1>
      {v.moniker && <p className="mono faint">{v.cons_address || v.address}</p>}
      {v.jailed && (
        <p className="notice">
          The chain has jailed this validator. That is the chain&rsquo;s own status, not something this site measured, and it does not
          release the validator from the shards it already signed for, so its rows below stand.
        </p>
      )}
      <dl className="kv">
        {v.operator_address && <><dt>operator address</dt><dd className="mono">{v.operator_address}</dd></>}
        {v.bond_status && <><dt>bond status</dt><dd className="mono" title="The chain's own status for this validator, not a measurement of this site.">{v.bond_status.replace("BOND_STATUS_", "").toLowerCase()}</dd></>}
        {v.website && <><dt>website</dt><dd><a href={v.website} rel="nofollow noopener noreferrer" target="_blank">{v.website}</a></dd></>}
        <dt>consensus address (hex)</dt><dd className="mono">{v.address}</dd>
        {v.cons_address && <><dt>consensus address</dt><dd className="mono">{v.cons_address}</dd></>}
        <dt>registered Fibre endpoint</dt><dd className="mono">{v.host || "— not registered"}{v.endpoint_since && <span className="muted"> since {utc(v.endpoint_since)}</span>}</dd>
        <dt>reachable (latest)</dt><dd>{v.reachable === null ? "not probed" : v.reachable ? "yes" : <span className="err">no</span>}{v.last_seen_at && <span className="muted"> · {utc(v.last_seen_at)} ({ago(v.last_seen_at)})</span>}</dd>
        <dt>TLS identity</dt><dd>{v.identity_status}{v.identity_reason && <span className="muted"> ({v.identity_reason})</span>}</dd>
        <dt>voting power</dt><dd className="mono">{v.voting_power.toLocaleString("en-US")}</dd>
        <dt>assigned rows (last)</dt><dd className="mono">{v.assigned_rows_last || "—"}{v.expected_load_band && <span className="muted"> · expected load band: {v.expected_load_band}</span>}</dd>
      </dl>

      <h2>Serve rate</h2>
      {v.attestation && (v.attestation.unattested_probes > 0 || v.attestation.unknown_probes > 0) && (
        <div className="notice">
          <strong>{v.attestation.unattested_probes.toLocaleString("en-US")} of this validator&rsquo;s probes are outside the rates below.</strong>{" "}
          A validator only owes a shard it stored, and the only on-chain proof it stored one is a signature on the settled promise
          that this observer verified against its consensus key. Where that proof is missing the probe is recorded as
          <em> unattested</em> and excluded in both directions, so a failure it was never proven to owe cannot count against it and a
          success it was never proven to owe cannot count for it.
          {v.attestation.unknown_probes > 0 && <> {v.attestation.unknown_probes.toLocaleString("en-US")} further probes predate signature verification and are counted under the older rules.</>}
        </div>
      )}
      <div className="tablewrap">
        <table>
          <caption>healthy / (healthy + fault) over assigned probes in the in-window and grace phases whose obligation the promise proves. Tolerated, unattested, expected gone and not probed are listed, never folded in.</caption>
          <thead><tr><th>window</th><th className="right">serve rate</th><th className="right">probes</th><th>verdicts</th></tr></thead>
          <tbody>
            {data.windows.map((w) => (
              <tr key={w.window.name}>
                <td className="mono">{w.window.name} <span className="faint">{utc(w.window.start)} →</span></td>
                <td className="right mono"><RateCell r={w.serve_rate} /></td>
                <td className="right mono">{w.rated_probe_count}</td>
                <td>{Object.entries(w.classes).sort().map(([k, n]) => <span key={k} style={{ marginRight: 8 }}><Badge cls={k} /> <span className="mono">{n}</span></span>)}{Object.keys(w.classes).length === 0 && <span className="muted">— (0 probes)</span>}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>

      <h2>Recent probes</h2>
      <div className="tablewrap">
        <table>
          <caption>Newest first, at most 50. Full history: <code>/v1/probes?validator={v.address}</code>.</caption>
          <thead><tr><th>started (UTC)</th><th>blob</th><th>point</th><th>phase</th><th>verdict</th><th>outcome</th><th className="right">rows</th><th className="right">ms</th><th>detail</th></tr></thead>
          <tbody>
            {data.recent_probes.length === 0 && <tr><td colSpan={9} className="muted">No probes for this validator yet.</td></tr>}
            {data.recent_probes.map((p) => (
              <tr key={p.promise_hash + p.scheduled_at}>
                <td className="mono">{utc(p.started_at)}</td>
                <td className="mono"><Link href={`/blob/?hash=${p.promise_hash}`}>{shortHex(p.promise_hash, 6)}</Link></td>
                <td className="mono">{p.schedule_label}</td>
                <td>{p.phase.replace("_", " ")}</td>
                <td><Badge cls={p.classification} title={p.classification_reason} /></td>
                <td className="mono">
                  {p.outcome}
                  {p.attested === false && <span className="muted" title="No verified signature on this promise, so the obligation is unproven and this probe is outside the serve rate."> (unproven)</span>}
                  {p.retry_first_outcome && (
                    <span className="faint" title={`The first attempt was ${p.retry_first_outcome}. The retry went to the same address from the same vantage, so a repeat is one observation twice, not two that agree.`}>
                      {" "}(retried after {p.retry_first_outcome})
                    </span>
                  )}
                </td>
                <td className="right mono">{p.rows_expected ? `${p.rows_returned}/${p.rows_expected}` : "—"}</td>
                <td className="right mono">{p.total_duration_ms}</td>
                <td className="muted" style={{ whiteSpace: "normal", maxWidth: 360 }}>{p.raw_error || p.classification_reason}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <Legend />
    </>
  );
}

export default function ValidatorPage() {
  return <Suspense fallback={<p className="muted">Loading…</p>}><Page /></Suspense>;
}
