"use client";
import { Suspense } from "react";
import Link from "next/link";
import { useSearchParams } from "next/navigation";
import { useApi, type Validator, type Probe, type Window, type Rate, type ClassCounts, fmtRate, fmtCount, enoughToRank, utc, ago, shortHex } from "@/lib/api";
import Verdict from "@/components/Verdict";
import RateCell from "@/components/Rate";
import Graduation from "@/components/Graduation";

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

/**
 * One layer of the service: what it answers, the figure, and what the figure
 * rests on. A layer with no observations shows a dash and says so — a page
 * about a named operator may not render "0%" where it means "we did not look".
 */
function Layer({ label, r, what, sample }: {
  label: string;
  r: Rate | null | undefined;
  what: string;
  sample?: string;
}) {
  const absent = !r || r.den === 0;
  return (
    <div>
      <span className="label">{label}</span>
      <span className={absent ? "fig absent" : "fig"}>{absent ? "—" : fmtRate(r)}</span>
      <span className="sample">
        {absent ? "not observed in this window" : enoughToRank(r) ? (sample ?? fmtCount(r)) : "under floor"}
      </span>
      <span className="what">{what}</span>
    </div>
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
      <h2>Service <span className="chip" title={`${utc(data.window.start)} → ${utc(data.window.end)}`}>{data.window.name}</span></h2>
      <div className="layers">
        <Layer label="Reachable" r={v.reachability_window}
          sample={v.reachability_window?.den ? `${v.reachability_window.den.toLocaleString("en-US")} checks` : undefined}
          what="This site completed TLS with the registered endpoint. Every ten minutes, whether or not anything was assigned." />
        <Layer label="Endorsed" r={v.identity_rate_window}
          what="Of the checks that saw a certificate, how many were signed by this validator's consensus key. A client refuses the rest." />
        <Layer label="Served" r={v.serve_rate}
          what="Of the shards the chain proves this validator stored, how many it handed over when asked." />
        <Layer label="Held to the end" r={last?.serve_rate}
          sample={last ? `${fmtCount(last.serve_rate)} at ${last.key}` : undefined}
          what="The same, at the last point of the window that produced a verdict. Below the earlier points means pruning before the deadline." />
      </div>
      <p className="coverage">
        Reachability and endorsement are measured on a fixed heartbeat, so they speak for every validator with a registered
        endpoint. Serving speaks only for obligations the chain proves, which is a smaller set, not the same one each window,
        and selected by which validators answered the publisher fast enough.{" "}
        <Link href="/methodology/#quorum">Why most validators are unproven →</Link>
      </p>

      {v.last_unreachable_at && (
        <p className="coverage">
          Last heartbeat that could not complete TLS: {utc(v.last_unreachable_at)} ({ago(v.last_unreachable_at)}).
          Half of that path is this site&rsquo;s own.
        </p>
      )}

      {points.length > 0 && <Graduation points={points} />}

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
      {/*
        Counted in blobs, not in probes. Each obligation is probed at four
        schedule points, so the probe count reads four times larger than the
        thing it describes, and an operator seeing a four-figure number beside
        their own name reads an accusation where the chain is merely silent.
      */}
      {(unattested > 0 || unknown > 0) && (
        <div className="notice">
          <strong>{unattested.toLocaleString("en-US")} blob{unattested === 1 ? "" : "s"} in this window are outside the rates below.</strong>{" "}
          A validator only owes a shard it stored, and the only on-chain proof it stored one is a signature on the settled promise
          that this observer verified against its consensus key. The publisher stops collecting signatures once two thirds of
          voting power has answered, so most of the set is left unproven on most blobs, by design and not by fault. Those
          obligations are excluded in both directions: a failure the validator was never proven to owe cannot count against it,
          and a success it was never proven to owe cannot count for it.{" "}
          <Link href="/methodology/#quorum">How the quorum works →</Link>
          {unknown > 0 && <> {unknown.toLocaleString("en-US")} further blob{unknown === 1 ? "" : "s"} predate signature verification and are counted under the older rules.</>}
        </div>
      )}
      <div className="tablewrap">
        <table>
          <caption>healthy / (healthy + fault) over assigned probes in the in-window and grace phases whose obligation the promise proves. Tolerated, unattested, expected gone and not probed are listed, never folded in.</caption>
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
                <td><Verdict cls={p.classification} title={p.classification_reason} /></td>
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
    </>
  );
}

export default function ValidatorPage() {
  return <Suspense fallback={<p className="muted">Loading…</p>}><Page /></Suspense>;
}
