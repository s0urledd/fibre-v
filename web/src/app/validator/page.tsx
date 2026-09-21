"use client";
import { Suspense, useState } from "react";
import Link from "next/link";
import { useSearchParams } from "next/navigation";
import { useApi, type Validator, type Probe, type Window, type Rate, fmtPct, fmtCount, utc, ago, shortHex, bytesPerSecond, through, type RecordThrough, API_BASE } from "@/lib/api";
import { Avatar } from "@/components/ValidatorTable";
import Verdict, { Mark } from "@/components/Verdict";
import Info from "@/components/Info";
import Graduation from "@/components/Graduation";
import { Panel, Cell } from "@/components/Panel";
import { band } from "@/components/Rate";

type Detail = {
  window: Window;
  record_through?: RecordThrough;
  validator: Validator;
  recent_probes: Probe[];
  /** set when this window rests partly on the daily rollup (the "all" window past the raw retention) */
  rolled_up?: { raw_from: string; days: number; note: string };
  /** schedule points, over all time, that no rate counts: the observer's own correlated failures */
  suspect_points: { at: string; label: string; reason: string }[];
};

// the same words the overview table uses for the same states
function identityWord(status: string): string {
  switch (status) {
    case "mismatch": return "bad cert";
    case "expired": return "cert expired";
    case "no_tls": return "no tls";
    case "unverified": return "unverified";
    default: return status.replace(/_/g, " ");
  }
}

function Layer({ label, r, what, sample, kind, evidence }: { label: string; r: Rate | null | undefined; what: React.ReactNode; sample?: string; kind?: "serve" | "reach"; evidence?: "chain" | "verified" | "observed" }) {
  const absent = !r || r.den === 0;
  const b = kind ? band(r, kind) : "";
  return (
    <Cell label={label} info={what} evidence={evidence}
      value={absent ? "—" : fmtPct(r)}
      tone={absent ? "absent" : b === "ok" ? "ok" : b === "fault" ? "fault" : undefined}
      sub={absent ? "not observed" : (sample ?? fmtCount(r))} />
  );
}

function Page() {
  const addr = useSearchParams().get("addr") ?? "";
  const [win, setWin] = useState("24h");
  const { data, error, loading } = useApi<Detail>(addr ? `/v1/validators/${addr}?window=${win}` : null);
  if (!addr) return <p className="notice">Open a validator from the <Link href="/">overview</Link>, or add <code>?addr=&lt;consensus address&gt;</code>.</p>;
  if (error) return <p className="notice err">{error}</p>;
  if (loading || !data) return <p className="muted">Loading…</p>;
  const v = data.validator;
  const unattested = v.attestation?.unattested_blobs ?? 0;
  const unknown = v.attestation?.unknown_blobs ?? 0;
  const points = v.serve_rate_by_point ?? [];
  const o = v.obligations;
  const decided = !!o && o.rate.den > 0;
  // Three counts of the same events, kept apart. `broken` is obligations: one
  // per shard this validator signed for and did not hand over, and the number
  // this page puts beside its name. `faults` is every FAULT probe of an
  // assigned shard in any phase, which the schedule makes several times
  // larger for the same shard; `inWindowFaults` is the in-window part of it,
  // and the serve rate's denominator is HEALTHY + FAULT over those. Printing
  // one over the other read "2 of 0" whenever a fault fell outside the window.
  const broken = o?.broken ?? 0;
  const faults = v.faults ?? v.classes?.FAULT ?? 0;
  const inWindowFaults = v.classes?.FAULT ?? 0;
  const rated = (v.serve_rate?.den ?? 0) > 0;
  const suspect = new Map((data.suspect_points ?? []).map((s) => [s.at, s.reason.replace(",", " and ")]));
  const heldOut = Object.entries(v.serve_rate_held_out ?? {}).filter(([, n]) => n > 0)
    .sort((a, b) => b[1] - a[1]).map(([c, n]) => `${n.toLocaleString("en-US")} ${c.toLowerCase().replace(/_/g, " ")}`);
  return (
    <>
      <section className="card">
        <div className="card-head">
          <span className="who">
            <Avatar v={v} />
            <span>
              <h1 style={{ margin: 0 }}>{v.moniker || <span className="mono">{v.cons_address || v.address}</span>}</h1>
              {v.moniker && <span className="addr mono faint">{v.cons_address || v.address}</span>}
            </span>
          </span>
          <span className="spacer" />
          <span className="chips">
            {v.reachable === true && v.identity_status === "verified" && <span className="chip ok"><i className="dot ok" />up</span>}
            {v.reachable === false && <span className="chip hold" title={`Could not reach ${v.host} at the last attempt.`}><i className="dot hold" />down</span>}
            {v.reachable === true && v.identity_status !== "verified" && <span className="chip hold" title={v.identity_reason}><i className="dot hold" />{identityWord(v.identity_status)}</span>}
            {!v.host && !v.last_host && <span className="chip">no Fibre endpoint</span>}
            {v.jailed && <span className="chip" title="Jailed by the chain. Out of the bonded provider list, so no handshake is attempted; shards it signed for are still owed.">jailed</span>}
            {v.bond_status && v.bond_status !== "BOND_STATUS_BONDED" && <span className="chip">{v.bond_status.replace("BOND_STATUS_", "").toLowerCase()}</span>}
          </span>
        </div>
        <dl className="kv">
          <dt>Fibre endpoint</dt><dd className="mono">
            {v.host || (v.last_host ? <>{v.last_host}<span className="muted"> · left the bonded list {utc(v.endpoint_closed_at)}; the registration stays on chain</span></> : "— not registered")}
            {v.host && v.endpoint_since && <span className="muted"> · since {utc(v.endpoint_since)}</span>}
          </dd>
          <dt>last handshake</dt><dd>{v.reachable == null ? "none yet" : v.reachable ? "completed" : <span className="err">failed</span>}{v.last_seen_at && <span className="muted"> · {utc(v.last_seen_at)} ({ago(v.last_seen_at)})</span>}{v.last_unreachable_at && <span className="muted"> · last failed {ago(v.last_unreachable_at)}</span>}</dd>
          <dt>TLS identity</dt><dd>{v.identity_status}{v.identity_reason && <span className="muted"> ({v.identity_reason})</span>}</dd>
          <dt>voting power</dt><dd className="mono">{v.voting_power.toLocaleString("en-US")}{v.assigned_rows_last ? <span className="muted"> · {v.assigned_rows_last} rows per blob ({v.expected_load_band})</span> : null}</dd>
          {v.attestation && v.attestation.blob_coverage.den > 0 && (
            <><dt>signed blobs</dt><dd className="mono" title="Assigned blobs in this window whose settled promise carries this validator's signature. Publishers stop collecting signatures at two thirds of stake, so 100% is not expected.">
              {v.attestation.attested_blobs.toLocaleString("en-US")} of {v.attestation.blob_coverage.den.toLocaleString("en-US")}
              <span className="muted"> · {fmtPct(v.attestation.blob_coverage)}</span>
            </dd></>
          )}
          {(v.timeouts_enforced ?? 0) > 0 && (
            <><dt>timeouts enforced</dt><dd className="mono" title="MsgPaymentPromiseTimeout submitted by this validator's operator account in the window: promises it held that the publisher abandoned, reported so the escrow was charged. The chain pays nothing for it.">
              {v.timeouts_enforced!.toLocaleString("en-US")} <span className="muted">abandoned promise{v.timeouts_enforced === 1 ? "" : "s"} reported to the chain</span>
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
      <div className="head-row" style={{ marginTop: "var(--s5)" }}>
        <span className="sample" title={`${utc(data.window.start)} → ${utc(data.window.end)}.${through(data.record_through) ? " " + through(data.record_through)!.title : ""}`}>{data.window.name} window{through(data.record_through) && <> · {through(data.record_through)!.text}</>}</span>
        <span className="spacer" />
        <div className="pills" role="group" aria-label="window">
          {["24h", "7d", "30d", "all"].map((w) => <button key={w} aria-pressed={win === w} onClick={() => setWin(w)}>{w}</button>)}
        </div>
      </div>
      <Panel title={<>Service<Info label="These four figures">
          <p>Four figures, in the order you would debug them.</p>
          <p>Reachability and Endorsed come from a TLS handshake with the endpoint every 5 minutes. Faults and Obligations only cover blobs this validator signed for.</p>
          <p>No figure has a threshold. Checks run from one location.</p>
        </Info></>} right={<>{data.window.name} window · <Link href="/methodology/#verdicts">methodology →</Link></>}>
      <div className="cells four">
        <Layer label="Reachability" kind="reach" evidence="observed" r={v.reachability_window}
          sample={v.reachability_window?.den ? `${v.reachability_window.den.toLocaleString("en-US")} handshakes` : undefined}
          what={<>
            <p>TLS handshakes completed, over handshakes attempted: one every 5 minutes, from one location. Nothing is downloaded.</p>
            <p>This is not signing uptime. A validator can sign every block with its Fibre endpoint down, and the reverse.</p>
          </>} />
        <Layer label="Endorsed" kind="reach" evidence="verified" r={v.identity_rate_window}
          what={<>
            <p>Of the handshakes that reached a certificate, how many were signed by this validator&rsquo;s consensus key. Clients refuse the rest.</p>
            <p>Handshakes that never reached a certificate are not counted here, so an outage is not reported twice.</p>
          </>} />
        <Cell label="Faults" evidence="verified"
          value={broken > 0 ? <><Mark tier="fault" />{broken.toLocaleString("en-US")}</> : faults > 0 ? "0" : rated ? "0" : "—"}
          tone={broken > 0 ? "fault" : "absent"}
          sub={broken > 0
            ? `obligation${broken === 1 ? "" : "s"} broken, over ${faults.toLocaleString("en-US")} fault probe${faults === 1 ? "" : "s"}`
            : faults > 0 ? `${faults.toLocaleString("en-US")} fault probe${faults === 1 ? "" : "s"}, none under a proven obligation`
            : rated ? `${v.serve_rate.den.toLocaleString("en-US")} rated probes` : "nothing rated"}
          detail={broken > 0
            ? `${broken.toLocaleString("en-US")} shard${broken === 1 ? "" : "s"} this validator signed for and did not hand over. The ${faults.toLocaleString("en-US")} fault probe${faults === 1 ? "" : "s"} behind that number are the readings: the schedule visits each shard several times in window${inWindowFaults === faults ? "" : `, and ${(faults - inWindowFaults).toLocaleString("en-US")} of them fell past the deadline, where there is no obligation to break`}. The serve rate is drawn from ${v.serve_rate.den.toLocaleString("en-US")} rated in-window probes.`
            : faults > 0 ? `${faults.toLocaleString("en-US")} fault probe${faults === 1 ? "" : "s"}, none of them inside a retention window this validator was proven to be under, so no obligation is counted broken.`
            : undefined}
          info={<>
            <p>The validator answered but did not hand over a shard it had signed for.</p>
            <p>Counted <strong>per obligation</strong>: one per shard broken, the same population the serve rate is drawn from. The probe readings behind it are several per shard and are on the detail line, not in this number.</p>
            <p>The only number counted against a validator. Unreachable, unproven and unregistered are not faults.</p>
            <p>A fault can also be recorded after the deadline, when a shard that is gone comes back wrong. There is no obligation to break there, so it is a fault probe and not a broken obligation.</p>
          </>} />
        <Cell label="Obligations" evidence="verified"
          value={decided ? fmtPct(o!.rate) : "—"}
          tone={!decided ? "absent" : band(o!.rate, "serve") === "ok" ? "ok" : band(o!.rate, "serve") === "fault" ? "fault" : undefined}
          sub={!o || o.total === 0 ? "none proven"
            : decided ? `${o.served.toLocaleString("en-US")} / ${(o.served + o.broken).toLocaleString("en-US")} kept`
            : `${o.total.toLocaleString("en-US")} proven, none observed`}
          detail={!o || o.total === 0 ? "No obligation proven in this window."
            : decided ? `${o.served.toLocaleString("en-US")} of ${(o.served + o.broken).toLocaleString("en-US")} obligations kept${o.unobserved > 0 ? `; ${o.unobserved.toLocaleString("en-US")} never observed serving, listed below` : ""}.`
            : `${o.total.toLocaleString("en-US")} obligations proven, none observed serving.`}
          info={<>
            <p>Shards this validator signed for, one observation each, judged by the last probe before the retention deadline: kept if it was handed over then, broken if any probe was a fault.</p>
            <p>Obligations never seen served and never seen broken are listed below, not in the rate.</p>
          </>} />
      </div>
      </Panel>

      <Panel title="Throughput" right="download step only, from one location">
      <div className="cells four">
        <Cell label="Throughput" evidence="observed"
          value={v.serve_bytes_per_second == null ? "—" : bytesPerSecond(v.serve_bytes_per_second)}
          tone={v.serve_bytes_per_second == null ? "absent" : undefined}
          sub={v.serve_bytes_per_second == null ? "not observed" : `median over ${v.serve_throughput_sample.toLocaleString("en-US")} healthy probes`}
          info={<>
            <p>Bytes handed over per second during the download itself, median over healthy probes. Connecting and checking the certificate are not in it.</p>
            <p>Measured from one location, so the absolute figure includes our own path; validators measured from the same place at the same time compare.</p>
          </>} />
      </div>
      </Panel>

      {data.rolled_up && (
        <p className="muted rolled">
          Rolled up after 90 days: figures before {data.rolled_up.raw_from} come from the daily rollup ({data.rolled_up.days.toLocaleString("en-US")} days); by-point, attestation and throughput figures cover the raw rows from then on.
        </p>
      )}
      {o && o.total > 0 && (
        <p className="coverage">
          {o.total.toLocaleString("en-US")} proven obligation{o.total === 1 ? "" : "s"} in this window:
          {" "}{o.served.toLocaleString("en-US")} kept, {o.broken.toLocaleString("en-US")} broken
          {o.end_unobserved > 0 && <>, {o.end_unobserved.toLocaleString("en-US")} served early with no reading at the end of the window</>}
          {o.pending > 0 && <>, {o.pending.toLocaleString("en-US")} still inside the retention window</>}
          {o.unobserved > 0 && <>, <strong>{o.unobserved.toLocaleString("en-US")} never observed serving</strong>
            {" "}({[o.unobserved_reachable > 0 && `${o.unobserved_reachable} reachable, nothing handed over`,
                   o.unobserved_unreachable > 0 && `${o.unobserved_unreachable} unreachable`,
                   o.unobserved_not_probed > 0 && `${o.unobserved_not_probed} not probed by us`].filter(Boolean).join(" · ")})</>}.
          <Info label="Never observed serving">
            <p>An obligation we could never see kept: no probe of it came back with the shard, and none came back with a fault either.</p>
            <p><em>Reachable, nothing handed over</em> means the endpoint completed a TLS handshake and then answered with an error, a rate limit, or a certificate no client would accept. <em>Unreachable</em> means it never completed one. <em>Not probed</em> means we skipped the download ourselves: backoff after repeated failures, a load cap, or a slot that elapsed.</p>
            <p>None of these is a fault. All of them are why the rate above may say less than it seems to. An obligation still inside its retention window has no verdict yet and is not in the rate either.</p>
            <p>Only obligations the settled promise proves are counted: a blob without this validator&rsquo;s verified signature is nothing to keep or break.</p>
          </Info>
        </p>
      )}

      {points.some((p) => p.serve_rate.den > 0) && <section className="card" style={{ marginTop: "var(--s4)" }}><Graduation points={points} /></section>}

      {rated && (
        <p className="coverage">
          Per probe: <strong>{fmtPct(v.serve_rate)}</strong> ({fmtCount(v.serve_rate)}){heldOut.length > 0 && ` · ${heldOut.join(" · ")}`}.
          <Info label="Per probe">
            <p>The same shards counted one probe at a time: healthy over healthy plus fault, inside the retention window. Four probes of one shard are near copies of each other, so this number looks more certain than it is; the obligation figure above is the one to lean on.</p>
            <p>Beside it, the probes the rate does not speak for, by class.</p>
          </Info>
        </p>
      )}

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
      <Panel title="Recent probes" right={<>newest 50 · <a href={`${API_BASE}/v1/probes?validator=${v.address}&limit=1000`}>full history</a></>} className="probes">
      <div className="tablewrap">
        <table>
          <thead><tr><th>started (UTC)</th><th>blob</th><th>point</th><th>phase</th><th>verdict</th><th>outcome</th><th className="right">rows</th><th className="right">ms</th></tr></thead>
          <tbody>
            {data.recent_probes.length === 0 && <tr><td colSpan={8} className="muted">No probes for this validator yet.</td></tr>}
            {data.recent_probes.map((p) => {
              const sus = suspect.get(p.scheduled_at);
              return (
              <tr key={`${p.vantage}|${p.promise_hash}|${p.scheduled_at}`} className={sus ? "suspect" : undefined}
                title={sus ? `At this point ${sus} of the validators probed failed at once. That is the observer's problem, not theirs: nothing at this point counts in any rate.` : undefined}>
                <td className="mono">{utc(p.started_at)}</td>
                <td className="mono"><Link href={`/blob/?hash=${p.promise_hash}`}>{shortHex(p.promise_hash, 6)}</Link></td>
                <td className="mono">{p.schedule_label}</td>
                <td>{p.phase.replace("_", " ")}</td>
                <td><Verdict cls={p.classification} title={p.classification_reason} />{sus && <span className="faint" title="excluded from every rate: correlated failure at this point"> (not counted)</span>}</td>
                <td className="mono" title={p.raw_error || p.classification_reason}>
                  {p.outcome}
                  {p.attested === false && <span className="muted" title="No signature from this validator on this promise, so the probe is not in the serve rate."> (unproven)</span>}
                  {p.retry_first_outcome && (
                    <span className="faint" title={`First attempt: ${p.retry_first_outcome}. Retried once from the same location.`}>
                      {" "}(retried after {p.retry_first_outcome})
                    </span>
                  )}
                  {p.host_changed && (
                    <span className="faint" title={`The validator re-registered during the window: the upload went to ${p.host_at_settlement}, this probe went to the host registered now.${p.settlement_host_outcome ? ` Asked as evidence, the old host answered ${p.settlement_host_outcome}${p.settlement_host_served ? " with the exact rows: the data was left behind, not lost" : ""}.` : ""}`}>
                      {" "}(host changed{p.settlement_host_served ? "; old host still serves" : p.settlement_host_outcome ? `; old host ${p.settlement_host_outcome}` : ""})
                    </span>
                  )}
                  {p.amended_at && p.classification_at_probe && (
                    <span className="faint" title={`Filed as ${p.classification_at_probe} at the probe and judged ${p.classification} once every promise that could have answered was on record (${p.amended_at}).`}>
                      {" "}(judged late)
                    </span>
                  )}
                </td>
                <td className="right mono">{p.rows_expected ? `${p.rows_returned}/${p.rows_expected}` : "—"}</td>
                <td className="right mono" title={[p.rpc_code && `gRPC ${p.rpc_code}`, p.shadowed_by && `answered from promise ${shortHex(p.shadowed_by, 6)}`, p.row_indices && `rows ${p.row_indices.length <= 6 ? p.row_indices.join(",") : p.row_indices.slice(0, 6).join(",") + "…"}`, p.rows_sha256 && `sha256 ${p.rows_sha256.slice(0, 12)}…`, p.observer_build && `build ${p.observer_build}`].filter(Boolean).join(" · ") || undefined}>{p.total_duration_ms}</td>
              </tr>
              );
            })}
          </tbody>
        </table>
      </div>
      </Panel>
    </>
  );
}

export default function ValidatorPage() {
  return <Suspense fallback={<p className="muted">Loading…</p>}><Page /></Suspense>;
}
