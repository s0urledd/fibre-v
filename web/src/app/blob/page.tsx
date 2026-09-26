"use client";
import { Suspense } from "react";
import Link from "next/link";
import { useSearchParams } from "next/navigation";
import { useApi, type Blob, type Probe, type Meta, int, bytes, tia, utcWord, hhmm, hhmmss, dur, shortMid, nsDisplay, notFound, API_BASE } from "@/lib/api";
import StatusLine from "@/components/StatusLine";
import { Metric, Metrics } from "@/components/Metrics";
import Copy from "@/components/Copy";

type Assignment = { validator_address: string; moniker?: string; voting_power: number; row_count: number; attested: boolean | null; host_at_settlement: string | null };
type Detail = {
  blob: Blob;
  params: { shard_retention_s: number; payment_promise_timeout_s: number };
  assignments: Assignment[] | null;
  probes: Probe[] | null;
  /** schedule points, by scheduled_at, the observer does not trust itself at: nothing there counts */
  suspect_points?: { at: string; label: string; reason: string }[] | null;
};

/** one mark per classification; the word is in the title and the legend */
const MARK: Record<string, [string, string]> = {
  HEALTHY: ["ok", "served"], FAULT: ["fault", "broken"], UNATTESTED: ["unsigned", "unsigned"], EXPECTED_GONE: ["gone", "expected gone after the window"],
  NOT_REGISTERED: ["none", "no endpoint"], NOT_PROBED: ["gone", "not probed"], SERVER_ERROR: ["other", "server error"], UNREACHABLE: ["other", "unreachable"],
  THROTTLED: ["other", "rate limited"], IDENTITY_EXPIRED: ["other", "certificate expired"], IDENTITY_MISMATCH: ["other", "wrong certificate"],
  TOLERATED: ["gone", "tolerated after the deadline"], UNREACHABLE_POST_WINDOW: ["gone", "unreachable after the window"], SERVED_PAST_WINDOW: ["gone", "served after the window"],
  RETENTION_UNVERIFIED: ["gone", "deadline unverified"], SHADOWED_SHARD: ["other", "shadowed by another promise"], UNMATCHED_GENUINE: ["other", "unmatched genuine rows"],
  PROBE_ERROR: ["gone", "probe error"], EXPECTED_UNASSIGNED: ["gone", "unassigned"], SERVING_UNASSIGNED: ["other", "serving unassigned"],
};
const markOf = (cls: string): [string, string] => MARK[cls] ?? ["other", cls.toLowerCase().replace(/_/g, " ")];
/** the mark for one probe: an unsigned probe says what came back and is not rated either way */
const probeMark = (p: Probe): [string, string] => {
  if (p.classification === "UNATTESTED") return ["unsigned", (p.outcome === "SERVED_OK" || p.outcome === "PARTIAL") ? "served, unsigned" : `unsigned · ${p.outcome.toLowerCase().replace(/_/g, " ")}`];
  return markOf(p.classification);
};

function Page() {
  const hash = useSearchParams().get("hash") ?? "";
  const { data: meta, error: metaErr } = useApi<Meta>("/v1/meta");
  const d = useApi<Detail>(hash ? `/v1/blobs/${hash}` : null);
  if (!hash) return <p className="notice">Open a blob from the <Link href="/blobs/">list</Link>, or add <code>?hash=&lt;promise hash&gt;</code> to the address.</p>;
  const data = d.data;
  if (!data) {
    return (
      <>
        <div className="head"><div><p className="crumb"><Link href="/blobs/">Blobs</Link> › {hash.slice(0, 10)}…</p><h1>{notFound(d) ? "Blob not recorded yet" : d.error ? "Blob" : "Loading…"}</h1></div></div>
        <StatusLine meta={meta} metaError={metaErr} snap={null} client={{ error: d.error, fetchedAt: d.fetchedAt, status: d.status }} />
        {notFound(d) && <p className="notice">No publication with the promise hash <span className="mono">{shortMid(hash, 10, 6)}</span> is on record. A blob appears here once the scanner has read the block that settled it; this page checks again every 30 seconds.</p>}
      </>
    );
  }
  const b = data.blob;
  const probes = data.probes ?? [];
  const assignments = data.assignments ?? [];
  // signatures are a fact of the settled promise, not of any probe: read them from the assignments
  const signedKnown = assignments.some((a) => a.attested != null);
  const signedN = assignments.filter((a) => a.attested === true).length;
  const suspectAt = new Map((data.suspect_points ?? []).map((sp) => [sp.at, sp.reason.replace(",", " and ")]));
  const rc = b.reconstructable;
  // drawn out of the sample: one record, with the probability it was drawn at
  const so = b.sampled_out;
  const soText = so ? `p=${so.p.toFixed(2)}` : "";
  const judged = !!rc && (rc.status === "yes" || rc.status === "degraded" || rc.status === "no");
  const over = new Date(b.must_serve_until).getTime() <= Date.now();
  const state: [string, string, string] =
    rc?.status === "yes" ? ["ok", "Fully served", `Every validator proven to hold a shard served its rows at ${rc.point}.`]
    : rc?.status === "degraded" ? ["hold", "Rebuildable, not fully served", `Enough distinct rows were observed to rebuild the blob, but not every validator proven to hold a shard served at ${rc.point}.`]
    : rc?.status === "no" ? ["hold", "Not rebuildable", `Fewer than the ${int(rc.needed_rows)} rows needed came back at ${rc.point}. Which validators answered is in the table below; unreachable from here is never counted as broken.`]
    : rc?.status === "pending" ? ["none", "Not judged yet", `${int(rc.probed_validators)} of ${int(rc.assigned_validators)} assigned validators have a result at ${rc.point}. A validator without a result is a gap, not a failure.`]
    : so ? ["none", "Sampled out", `Not probed: the load policy drew this blob out of its sample (${soText}).`]
    : !over ? ["none", "In window", "The retention window has not ended; nothing is judged before the last in-window point completes."]
    : ["none", "Not judged", b.probe_count === 0 ? "No probe has run for this blob." : "Row lists were not recorded for this publication, or no in-window point was completed."];

  // the probe points, in the order they ran; a point's time is the earliest probe scheduled for it
  const byLabel = new Map<string, { at: string; phase: string; cls: Record<string, number> }>();
  for (const p of probes) {
    const cur = byLabel.get(p.schedule_label);
    if (!cur) byLabel.set(p.schedule_label, { at: p.scheduled_at, phase: p.phase, cls: {} });
    else if (p.scheduled_at < cur.at) cur.at = p.scheduled_at;
  }
  // the newest probe per (validator, point) is the cell; its classification counts once per point
  const cell = new Map<string, Probe>();
  for (const p of probes) {
    const k = p.validator_address + "|" + p.schedule_label;
    const cur = cell.get(k);
    if (!cur || p.started_at > cur.started_at) cell.set(k, p);
  }
  for (const p of cell.values()) { const e = byLabel.get(p.schedule_label)!; e.cls[p.classification] = (e.cls[p.classification] ?? 0) + 1; }
  const order = [...byLabel.entries()].sort((a, c) => a[1].at.localeCompare(c[1].at)).map(([k]) => k);
  const t0 = new Date(b.settlement_time).getTime(), tEnd = new Date(b.must_serve_until).getTime();
  const tMax = Math.max(tEnd, ...order.map((k) => new Date(byLabel.get(k)!.at).getTime())) + 2 * 60000;
  const hasPost = order.some((k) => new Date(byLabel.get(k)!.at).getTime() > tEnd);
  // the window takes 60% of the axis when points fall after the deadline (those minutes are stretched into the rest), else all of it
  const winShare = hasPost ? 60 : 100;
  const x = (t: number) => (t <= tEnd ? Math.max(0, (t - t0) / (tEnd - t0)) * winShare : winShare + (t - tEnd) / (tMax - tEnd) * (100 - winShare)).toFixed(2) + "%";
  const lastIn = [...order].reverse().find((k) => new Date(byLabel.get(k)!.at).getTime() <= tEnd);
  const countLine = (k: string) => {
    const c = byLabel.get(k)!.cls;
    const sus = suspectAt.get(byLabel.get(k)!.at);
    if (sus) {
      const n = Object.values(c).reduce((a, x) => a + x, 0);
      return (
        <div key={k} title={`At this point ${sus} of the validators probed failed at once. From one location that cannot be told from this observer's own network, so nothing at this point counts in any figure.`}>
          <b>{k} <span className="soft">· {hhmm(byLabel.get(k)!.at).replace(" UTC", "")}</span></b>
          <span>{int(n)} rows</span><span>not counted</span>
        </div>
      );
    }
    const served = c.HEALTHY ?? 0, gone = (c.EXPECTED_GONE ?? 0) + (c.TOLERATED ?? 0), unsigned = c.UNATTESTED ?? 0, broken = c.FAULT ?? 0;
    const other = Object.entries(c).filter(([n]) => !["HEALTHY", "EXPECTED_GONE", "TOLERATED", "UNATTESTED", "FAULT"].includes(n)).reduce((s, [, n]) => s + n, 0);
    const post = byLabel.get(k)!.phase === "post";
    return (
      <div key={k}>
        <b>{k} <span className="soft">· {hhmm(byLabel.get(k)!.at).replace(" UTC", "")}</span></b>
        <span>{post ? `${int(gone)} expected gone` : `${int(served)} served`}</span>
        {broken > 0 && <span className="word fault">{int(broken)} broken</span>}
        {unsigned > 0 && <span>{int(unsigned)} unsigned</span>}
        {other > 0 && <span>{int(other)} other</span>}
      </div>
    );
  };
  const rows = [...assignments].sort((a, c) => c.voting_power - a.voting_power || a.validator_address.localeCompare(c.validator_address));
  const winLen = dur(b.settlement_time, b.must_serve_until);
  const fill = rc && rc.total_rows > 0 ? Math.min(100, rc.served_distinct_rows / rc.total_rows * 100) : 0;
  const tick = rc && rc.total_rows > 0 ? Math.min(100, rc.needed_rows / rc.total_rows * 100) : 0;

  return (
    <>
      <div className="head">
        <div>
          <p className="crumb"><Link href="/blobs/">Blobs</Link> › {b.promise_hash.slice(0, 10)}…</p>
          <h1>Blob <span className="mono">{shortMid(b.promise_hash, 10, 6)}</span><Copy text={b.promise_hash} label="promise hash" /></h1>
          <div className="chips">
            <span className="state" title={state[2]}><i className={"dot " + state[0]} />{state[1]}</span>
            <span title={utcWord(b.must_serve_until)}>{over ? <>Window over since <b className="word">{hhmm(b.must_serve_until)}</b></> : <>In window until <b className="word">{hhmm(b.must_serve_until)}</b></>}</span>
            <span title="The padded upload size the module charges for, not the payload.">{bytes(b.blob_size)}</span>
          </div>
        </div>
      </div>
      <dl className="facts">
        <div><dt>Publisher</dt><dd title={b.signer}><Link className="mono" href={`/publisher/?addr=${b.signer}`}>{shortMid(b.signer, 14, 6)}</Link></dd></div>
        <div><dt>Namespace</dt><dd title={b.namespace}><span className="mono">{nsDisplay(b.namespace)}</span><Copy text={b.namespace} label="namespace" /></dd></div>
        <div><dt>Commitment</dt><dd title={b.commitment}><span className="mono">{shortMid(b.commitment, 8, 6)}</span><Copy text={b.commitment} label="commitment" /></dd></div>
        <div><dt>Settled</dt><dd title={utcWord(b.settlement_time)}><span className="mono">#{int(b.settlement_height)}</span><span className="soft"> · {hhmm(b.settlement_time)}</span></dd></div>
        <div><dt>Created</dt><dd>{hhmmss(b.creation_timestamp)}</dd></div>
        {b.assignment_error && <div><dt>Assignment</dt><dd className="word">{b.assignment_error}</dd></div>}
      </dl>
      <StatusLine meta={meta} metaError={metaErr} snap={null} client={{ error: d.error, fetchedAt: d.fetchedAt }} />

      <Metrics>
        <Metric label="Rows observed" value={rc && (judged || rc.status === "pending") ? int(rc.served_distinct_rows) : "—"} tone={rc && (judged || rc.status === "pending") ? undefined : "absent"}
          help={rc && rc.total_rows > 0 ? `of ${int(rc.total_rows)} · ${int(rc.needed_rows)} needed` : "no in-window point completed"}
          title="Distinct rows that came back at the latest complete in-window point; the tick is how many rebuild the blob." />
        <Metric label="Validators served" value={rc && (judged || rc.status === "pending") ? int(rc.served_by_validators) : "—"} den={rc && (judged || rc.status === "pending") ? int(rc.assigned_validators) : undefined}
          tone={rc && (judged || rc.status === "pending") ? undefined : "absent"}
          help={rc && (judged || rc.status === "pending") ? `at ${rc.point} · ${hhmm(rc.point_at)}${rc.status === "pending" ? ` · ${int(rc.probed_validators)} with a result` : ""}` : "no in-window point completed"} />
        <Metric label="Signed" value={signedKnown ? int(signedN) : "—"} den={signedKnown ? int(assignments.length) : undefined}
          tone={signedKnown ? undefined : "absent"}
          help={signedKnown ? "unsigned is not a fault" : "signatures not recorded"}
          title="Assigned validators whose signature on the settled promise verified against their consensus key. The publisher stops collecting at two thirds of voting power, so about a third of the set is unsigned on any blob." />
        <Metric label="Service window" value={winLen} help={`${hhmm(b.settlement_time).replace(" UTC", "")} → ${hhmm(b.must_serve_until)}${over ? " · over" : ""}`}
          title={`creation + max(payment_promise_timeout ${data.params.payment_promise_timeout_s} s, shard_retention ${data.params.shard_retention_s} s)`} />
        <Metric label="Fee" value={b.charge ? tia(b.charge.fee_utia) : "—"} tone={b.charge ? undefined : "absent"}
          help={b.charge ? `${int(b.charge.gas_units)} gas · ${b.charge.timed_out ? "timed out" : b.charge.settled ? "settled" : "pending"}` : "recorded before payments were kept"} />
      </Metrics>

      <section className="band">
        <div>
          <h2>Service window</h2>
          <p className="sub">Settled {hhmm(b.settlement_time)} → deadline {hhmm(b.must_serve_until)} ({winLen})</p>
          {order.length === 0 ? <p className="errs">{so ? <>Sampled out ({soText}): not probed at any point.</> : "No probe has run for this blob yet."}</p> : (
            <>
              <div className="tl" role="img" aria-label={`probe points: ${order.join(", ")}`}>
                <div className="axis" /><div className="win" style={{ left: 0, width: `${winShare}%` }} />
                <div className="cut" style={{ left: `${winShare}%` }} />
                <div className="edge l">settled {hhmm(b.settlement_time).replace(" UTC", "")}</div>
                {hasPost && <div className="edge r">after the deadline · stretched</div>}
                <div className={"cutlbl" + (winShare > 85 ? " end" : "")} style={{ left: `${winShare}%` }}>deadline {hhmm(b.must_serve_until).replace(" UTC", "")}</div>
                {order.map((k) => {
                  const at = byLabel.get(k)!.at, t = new Date(at).getTime(), ph = byLabel.get(k)!.phase;
                  return (
                    <span key={k}>
                      <div className={"lbl" + (k === lastIn && hasPost ? " up" : "")} style={{ left: x(t) }}>{k}</div>
                      <div className={"pt" + (ph === "grace" ? " grace" : ph === "post" ? " post" : "")} style={{ left: x(t) }} title={`${k} · ${utcWord(at)}`} />
                    </span>
                  );
                })}
              </div>
              <div className="ptcounts">{order.map(countLine)}</div>
            </>
          )}
        </div>
        <div>
          <h2>Rows observed</h2>
          {rc && rc.total_rows > 0 && (judged || rc.status === "pending") ? (
            <>
              <p className="sub">At {rc.point} · {hhmm(rc.point_at)} · from {int(rc.served_by_validators)} validator{rc.served_by_validators === 1 ? "" : "s"}</p>
              <div className="meter" role="img" aria-label={`${int(rc.served_distinct_rows)} of ${int(rc.total_rows)} rows; ${int(rc.needed_rows)} needed`}>
                <i style={{ width: `${fill.toFixed(1)}%` }} /><div className="tick" style={{ left: `${tick.toFixed(1)}%` }} />
              </div>
              <div className="mnums"><span><b>{int(rc.served_distinct_rows)}</b> of {int(rc.total_rows)} rows</span><span>{int(rc.needed_rows)} needed to rebuild</span></div>
              <p className="errs" title="An observation of the rows that came back at one probe point, not an actual rebuild.">
                {rc.status === "yes" && <><b>Fully served.</b> Enough rows to rebuild, and all {int(rc.attested_validators)} signed validators served.</>}
                {rc.status === "degraded" && <><b>Rebuildable, not fully served.</b> {int(rc.attested_validators - rc.served_by_attested)} of {int(rc.attested_validators)} signed validators did not serve.</>}
                {rc.status === "no" && <><b>Not rebuildable</b> from the rows that came back at this point.</>}
                {rc.status === "pending" && <>{int(rc.probed_validators)} of {int(rc.assigned_validators)} validators have a result; waiting for the rest.</>}
              </p>
            </>
          ) : <p className="errs">{state[2]}</p>}
        </div>
      </section>

      <section>
        <div className="vhead">
          <div><h2>Assigned validators</h2><p className="sub">{int(rows.length)} validators assigned rows of this blob</p></div>
          <div className="tools"><a className="dis" href={`${API_BASE}/v1/blobs/${b.promise_hash}`} title="the raw record, probe rows included">Probe rows →</a></div>
        </div>
        <div className="tablewrap">
          <table className={"marks" + (order.length ? "" : " nopts")}>
            <thead><tr>
              <th className="col-pin">Validator</th><th className="num">Voting power</th><th className="num">Rows</th><th>Signed</th><th>Host at settlement</th>
              {order.map((k) => <th key={k} className={"m" + (suspectAt.has(byLabel.get(k)!.at) ? " soft" : "")} title={`${k} · ${utcWord(byLabel.get(k)!.at)}${suspectAt.has(byLabel.get(k)!.at) ? " · not counted: the observer does not trust itself at this point" : ""}`}>{k}</th>)}
              <th className="go" />
            </tr></thead>
            <tbody>
              {rows.length === 0 && <tr className="empty"><td colSpan={6 + order.length}>No assignment recorded{b.assignment_error ? `: ${b.assignment_error}` : ""}.</td></tr>}
              {rows.map((a) => (
                <tr key={a.validator_address}>
                  <td className="id col-pin"><Link className="mon" href={`/validator/?addr=${a.validator_address}`}>{a.moniker || shortMid(a.validator_address, 12, 4)}</Link></td>
                  <td className="num">{int(a.voting_power)}</td>
                  <td className="num">{int(a.row_count)}</td>
                  <td title={a.attested === true ? "Signature verified against the consensus key: proof of storage." : a.attested === false ? "No verified signature on the settled promise: unproven, not absent. The publisher stops collecting at two thirds of voting power." : "Recorded before the observer verified signatures."}>{a.attested === true ? "yes" : a.attested === false ? <span className="soft">no</span> : "—"}</td>
                  <td className="mono soft">{a.host_at_settlement ? a.host_at_settlement : a.host_at_settlement === "" ? <span title="no endpoint registered when the promise settled">—</span> : <span className="sans" title="the registry could not be read at that height">not read</span>}</td>
                  {order.map((k) => {
                    const p = cell.get(a.validator_address + "|" + k);
                    const sus = suspectAt.has(byLabel.get(k)!.at);
                    const m = !p ? ["none", "no row"] : sus ? ["gone", `not counted, observer-side · filed as ${p.classification.toLowerCase().replace(/_/g, " ")}`] : probeMark(p);
                    return <td key={k} className="m"><span className={"mk " + m[0]} title={`${k} · ${m[1]}${p ? ` · ${utcWord(p.started_at)} · ${int(p.rows_returned)} / ${int(p.rows_expected)} rows · ${int(p.total_duration_ms)} ms${p.raw_error ? ` · ${p.raw_error}` : ""}` : ""}`} /></td>;
                  })}
                  <td className="go"><Link href={`/validator/?addr=${a.validator_address}`} aria-label={`open ${a.moniker || a.validator_address}`}>→</Link></td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
        <p className="mklegend">
          <span><span className="mk ok" /> served</span>
          <span><span className="mk fault" /> broken</span>
          <span title="No verified signature on the settled promise, so not rated either way: the publisher stops collecting at two thirds of stake."><span className="mk unsigned" /> unsigned</span>
          <span title="Server error, unreachable, rate limited or a certificate problem. Never a fault."><span className="mk other" /> other</span>
          <span title="Expected gone after the window, not probed, or at a point where the observer does not trust itself. Never a fault."><span className="mk gone" /> not counted</span>
          <span><span className="mk none" /> no endpoint</span>
        </p>
      </section>
    </>
  );
}

export default function BlobPage() {
  return <Suspense fallback={<p className="crumb" style={{ paddingTop: 22 }}>Loading…</p>}><Page /></Suspense>;
}
