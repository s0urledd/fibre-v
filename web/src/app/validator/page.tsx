"use client";
import { Suspense } from "react";
import Link from "next/link";
import { useSearchParams } from "next/navigation";
import { useApi, type Validator, type Probe, type Window, type Rate, type ClassCounts, fmtRate, fmtCount, enoughToRank, utc, ago, shortHex } from "@/lib/api";
import Verdict from "@/components/Verdict";
import RateCell from "@/components/Rate";
import Info from "@/components/Info";
import Graduation from "@/components/Graduation";
import Tile from "@/components/Tile";

type Detail = {
  window: Window;
  validator: Validator;
  windows: {
    window: Window;
    serve_rate: Rate;
    rated_probe_count: number;
    classes: ClassCounts;
    serve_rate_coverage: Rate;
    serve_rate_by_obligation: Rate;
    serve_rate_held_out: ClassCounts;
  }[];
  recent_probes: Probe[];
};

function Layer({ label, r, what, sample }: { label: string; r: Rate | null | undefined; what: React.ReactNode; sample?: string }) {
  const absent = !r || r.den === 0;
  return (
    <Tile label={label} info={what}
      value={absent ? "—" : fmtRate(r)}
      tone={absent ? "absent" : undefined}
      sub={absent ? "not observed in this window" : enoughToRank(r) ? (sample ?? fmtCount(r)) : `${fmtCount(r)} · under floor`} />
  );
}

function Page() {
  const addr = useSearchParams().get("addr") ?? "";
  const { data, error, loading } = useApi<Detail>(addr ? `/v1/validators/${addr}` : null);
  if (!addr) return <p className="notice">Open a validator from the <Link href="/">overview</Link>, or add <code>?addr=&lt;consensus address&gt;</code>.</p>;
  if (error) return <p className="notice err">{error}</p>;
  if (loading || !data) return <p className="muted">Loading…</p>;
  const v = data.validator;
  const unattested = v.attestation?.unattested_blobs ?? 0;
  const unknown = v.attestation?.unknown_blobs ?? 0;
  const points = v.serve_rate_by_point ?? [];
  // The last point that produced a verdict, not the last point in the list: a
  // schedule point with no rated probe is a gap, and reading a gap as "held to
  // the end: none" would accuse a validator of the observer's own silence.
  const last = [...points].reverse().find((p) => p.serve_rate.den > 0);
  return (
    <>
      <section className="card">
        <div className="card-head">
          <span className="who">
            <span className="avatar" aria-hidden="true">{(v.moniker || v.address).slice(0, 2)}</span>
            <span>
              <h1 style={{ margin: 0 }}>{v.moniker || <span className="mono">{v.cons_address || v.address}</span>}</h1>
              {v.moniker && <span className="addr mono faint">{v.cons_address || v.address}</span>}
            </span>
          </span>
          <span className="spacer" />
          <span className="chips">
            {v.reachable === true && v.identity_status === "verified" && <span className="chip ok"><i className="dot ok" />up</span>}
            {v.reachable === false && <span className="chip hold" title={`Could not reach ${v.host} at the last heartbeat.`}><i className="dot hold" />down</span>}
            {v.reachable === true && v.identity_status !== "verified" && <span className="chip hold" title={v.identity_reason}><i className="dot hold" />{v.identity_status.replace("_", " ")}</span>}
            {!v.host && <span className="chip">no Fibre endpoint</span>}
            {v.jailed && <span className="chip hold" title="Jailed by the chain. Shards it signed for are still owed.">jailed</span>}
            {v.bond_status && <span className="chip">{v.bond_status.replace("BOND_STATUS_", "").toLowerCase()}</span>}
          </span>
        </div>
        <dl className="kv">
          <dt>Fibre endpoint</dt><dd className="mono">{v.host || "— not registered"}{v.endpoint_since && <span className="muted"> · since {utc(v.endpoint_since)}</span>}</dd>
          <dt>last heartbeat</dt><dd>{v.reachable == null ? "not probed" : v.reachable ? "reached" : <span className="err">unreachable</span>}{v.last_seen_at && <span className="muted"> · {utc(v.last_seen_at)} ({ago(v.last_seen_at)})</span>}{v.last_unreachable_at && <span className="muted"> · last failed {ago(v.last_unreachable_at)}</span>}</dd>
          <dt>TLS identity</dt><dd>{v.identity_status}{v.identity_reason && <span className="muted"> ({v.identity_reason})</span>}</dd>
          <dt>voting power</dt><dd className="mono">{v.voting_power.toLocaleString("en-US")}{v.assigned_rows_last ? <span className="muted"> · {v.assigned_rows_last} rows per blob ({v.expected_load_band})</span> : null}</dd>
          {v.attestation && v.attestation.blob_coverage.den > 0 && (
            <><dt>signed blobs</dt><dd className="mono" title="Assigned blobs in this window whose settled promise carries this validator's signature. Publishers stop collecting signatures at two thirds of stake, so 100% is not expected.">
              {v.attestation.attested_blobs.toLocaleString("en-US")} of {v.attestation.blob_coverage.den.toLocaleString("en-US")}
              <span className="muted"> · {fmtRate(v.attestation.blob_coverage)}</span>
            </dd></>
          )}
          {v.operator_address && <><dt>operator</dt><dd className="mono">{v.operator_address}</dd></>}
          <dt>consensus (hex)</dt><dd className="mono">{v.address}</dd>
          {v.website && <><dt>website</dt><dd><a href={v.website} rel="nofollow noopener noreferrer" target="_blank">{v.website}</a></dd></>}
        </dl>
      </section>

      {/*
        The four layers, in the order an operator debugs them and in the order
        of how much evidence each rests on. The serve rate used to be the first
        thing on this page, which meant an operator whose endpoint had been
        down for a day read a rate built from the handful of obligations the
        chain happened to prove, instead of the flat statement that nothing
        could reach them.
      */}
      {/* The window these four figures cover, said rather than implied. The
          serve-rate table below lists all four spans, so without this the
          reader has no way to tell which one the readings above are from. */}
      <div className="section-head" style={{ marginTop: "var(--s5)" }}>
        <h2 style={{ margin: 0 }}>Service</h2>
        <span className="chip" title={`${utc(data.window.start)} → ${utc(data.window.end)}`}>{data.window.name}</span>
        <Info label="These five figures">
          <p>Five checks, in the order you would debug them.</p>
          <p>Uptime and Endorsed come from the 10-minute check of every registered endpoint. Served, Held to the end and Throughput only cover blobs this validator signed for.</p>
          <p>No figure has a threshold. Checks run from one location.</p>
        </Info>
      </div>
      <div className="tiles five">
        <Layer label="Uptime" r={v.reachability_window}
          sample={v.reachability_window?.den ? `${v.reachability_window.den.toLocaleString("en-US")} checks` : undefined}
          what={<>
            <p>Share of 10-minute checks where the endpoint completed a TLS handshake.</p>
            <p>Runs for every validator with a host in <code>x/valaddr</code>, assigned or not.</p>
          </>} />
        <Layer label="Endorsed" r={v.identity_rate_window}
          what={<>
            <p>Of the checks that saw a certificate, how many were signed by this validator&rsquo;s consensus key. Clients refuse the rest.</p>
            <p>Checks that never reached TLS are not counted here, so an outage is not reported twice.</p>
          </>} />
        <Layer label="Served" r={v.serve_rate}
          what={<>
            <p>Shards handed over, out of the shards this validator signed for, inside the retention window.</p>
            <p>Blobs without this validator&rsquo;s signature on chain are not counted either way.</p>
          </>} />
        <Layer label="Held to the end" r={last?.serve_rate}
          sample={last ? `${fmtCount(last.serve_rate)} at ${last.key}` : undefined}
          what={<>
            <p>The serve rate at the last probe point inside the retention window.</p>
            <p>If this is lower than the earlier points, shards were pruned before the deadline.</p>
          </>} />
        {/*
          Throughput, not duration. Assignments run from 148 rows to 4,096, so
          a validator carrying eight times the rows takes longer for the same
          quality of service — sorting the set on raw milliseconds puts the
          busiest validators at the top and calls them slow. Rows per second is
          what makes two validators comparable; the percentiles are printed
          underneath because they are what an operator recognises from their
          own logs.
        */}
        <Tile label="Throughput" unit="rows/s"
          value={v.serve_rows_per_second == null ? "—" : v.serve_rows_per_second.toLocaleString("en-US")}
          tone={v.serve_rows_per_second == null ? "absent" : undefined}
          sub={v.serve_latency_p50_ms != null
            ? `${v.serve_latency_p50_ms.toLocaleString("en-US")} ms typical · ${(v.serve_latency_p95_ms ?? 0).toLocaleString("en-US")} ms p95`
            : "not observed in this window"}
          info={<>
            <p>Rows delivered per second, from connect to verified rows, over healthy probes.</p>
            <p>Rows per second rather than milliseconds, because a bigger shard takes longer. Failed probes are not included.</p>
          </>} />
      </div>

      {points.length > 0 && <section className="card" style={{ marginTop: "var(--s4)" }}><Graduation points={points} /></section>}

      <h2>Serve rate</h2>
      {/*
        Counted in blobs, not in probes. Each obligation is probed at four
        schedule points, so the probe count reads four times larger than the
        thing it describes, and an operator seeing a four-figure number beside
        their own name reads an accusation where the chain is merely silent.
      */}
      {(unattested > 0 || unknown > 0) && (
        <p className="coverage">
          {unattested.toLocaleString("en-US")} blob{unattested === 1 ? "" : "s"} in this window carry no signature from this validator and are not in these rates.
          <Info label="Outside the rates">
            <p>A validator only owes a shard it signed for. The signature on the settled promise is the proof, and this site verifies it against the consensus key.</p>
            <p>Publishers stop collecting signatures at two thirds of voting power, so most validators are left without one on most blobs. That is normal.</p>
            <p>Those blobs are left out in both directions: no fault, and no credit.</p>
            {unknown > 0 && <p>{unknown.toLocaleString("en-US")} further blob{unknown === 1 ? "" : "s"} predate signature
              verification and are counted under the older rules.</p>}
            <p><Link href="/methodology/#quorum">How the quorum works</Link></p>
          </Info>
        </p>
      )}
      <div className="tablewrap">
        <table>
          <caption>healthy / (healthy + fault) over in-window probes of shards this validator signed for. Grace-period probes are recorded but not counted; other classes are listed beside the rate.</caption>
          <thead><tr><th>window</th><th className="right">serve rate</th><th className="right">probes</th><th className="right">coverage</th><th>verdicts</th></tr></thead>
          <tbody>
            {data.windows.map((w) => (
              <tr key={w.window.name}>
                <td className="mono">{w.window.name} <span className="faint">{utc(w.window.start)} →</span></td>
                <td className="right mono"><RateCell r={w.serve_rate} obligations={w.serve_rate_by_obligation} /></td>
                <td className="right mono">{w.rated_probe_count}</td>
                <td className="right mono faint" title={"this span's rate speaks for " + w.serve_rate_coverage.num + " of " + w.serve_rate_coverage.den + " in-window probes of an assigned shard"}>{w.serve_rate_coverage.den > 0 ? Math.round((w.serve_rate_coverage.value ?? 0) * 100) + "%" : "·"}</td>
                <td>{Object.entries(w.classes).sort().map(([k, n]) => <span key={k} style={{ marginRight: 8 }}><Verdict cls={k} /> <span className="mono">{n}</span></span>)}{Object.keys(w.classes).length === 0 && <span className="muted">— (0 probes)</span>}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>

      <h2>Recent probes</h2>
      <div className="tablewrap">
        <table>
          <caption>Newest 50. Full history: <code>/v1/probes?validator={v.address}</code></caption>
          <thead><tr><th>started (UTC)</th><th>blob</th><th>point</th><th>phase</th><th>verdict</th><th>outcome</th><th className="right">rows</th><th className="right">ms</th><th>detail</th></tr></thead>
          <tbody>
            {data.recent_probes.length === 0 && <tr><td colSpan={9} className="muted">No probes for this validator yet.</td></tr>}
            {data.recent_probes.map((p) => (
              <tr key={p.promise_hash + p.scheduled_at}>
                <td className="mono">{utc(p.started_at)}</td>
                <td className="mono"><Link href={`/blob/?hash=${p.promise_hash}`}>{shortHex(p.promise_hash, 6)}</Link></td>
                <td className="mono">{p.schedule_label}</td>
                <td>{p.phase.replace("_", " ")}</td>
                <td><Verdict cls={p.classification} title={p.classification_reason} /></td>
                <td className="mono">
                  {p.outcome}
                  {p.attested === false && <span className="muted" title="No signature from this validator on this promise, so the probe is not in the serve rate."> (unproven)</span>}
                  {p.retry_first_outcome && (
                    <span className="faint" title={`First attempt: ${p.retry_first_outcome}. Retried once from the same location.`}>
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
    </>
  );
}

export default function ValidatorPage() {
  return <Suspense fallback={<p className="muted">Loading…</p>}><Page /></Suspense>;
}
