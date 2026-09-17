"use client";
import { Suspense } from "react";
import Link from "next/link";
import { useSearchParams } from "next/navigation";
import { useApi, type Blob, type Probe, utc, ago, bytes, nsDisplay, tia, shortBech } from "@/lib/api";
import Verdict from "@/components/Verdict";
import Timeline from "@/components/Timeline";
import Square from "@/components/Square";
import Info from "@/components/Info";

type Detail = {
  blob: Blob;
  params: { shard_retention_s: number; payment_promise_timeout_s: number };
  assignments: { validator_address: string; moniker?: string; voting_power: number; row_count: number; attested: boolean | null }[];
  probes: Probe[];
};

function Recon({ b }: { b: Blob }) {
  const r = b.reconstructable;
  if (!r || r.status === "unknown") {
    return <div className="note"><span className="label">Reconstructable: unknown</span><p>{b.probe_count === 0 ? "No probe has run for this blob yet." : "No in-window probe point has been completed yet."}</p></div>;
  }
  if (r.status === "pending") {
    return <div className="note"><span className="label">Reconstructable: pending</span><p>{r.probed_validators} of {r.assigned_validators} assigned validators have a result at probe {r.point}. A validator without a result is a gap, not a failure.</p></div>;
  }
  const total = r.total_rows > 0 ? r.total_rows : r.needed_rows;
  const tone = r.status === "yes" ? "ok" : r.status === "degraded" ? "hold" : "fault";
  const word = r.status === "yes" ? "yes" : r.status === "degraded" ? "degraded" : "no";
  return (
    <div className="recon">
      <div className="card-head">
        <span className="label">Reconstructable</span>
        <span className={"chip " + tone}>{word}</span>
        <span className="sample">at probe {r.point} · {utc(r.point_at)}{r.window_over && " · window closed since"}</span>
        <span className="spacer" />
        <Info label="Reconstructable">
          <p>Whether the blob could be rebuilt from the rows that came back at the last probe inside the retention window. Rows not probed are not counted; everything is measured from one location.</p>
          <p><strong>yes</strong>: enough rows, and every validator that signed for the blob answered. <strong>degraded</strong>: enough rows, but a signed validator did not answer. <strong>no</strong>: fewer rows than the blob needs.</p>
          {r.attestation_known && r.attested_validators < r.assigned_validators && (
            <p>{r.assigned_validators - r.attested_validators} of the {r.assigned_validators} assigned validators carry no signature on this promise; they cannot lower this verdict by staying quiet.</p>
          )}
        </Info>
      </div>
      <div className="recon-body">
        <Square served={r.served_distinct_rows} needed={r.needed_rows} total={total} />
        <div className="recon-figs">
          <div className="tile">
            <span className="label">Rows served</span>
            <span className="value">{r.served_distinct_rows.toLocaleString("en-US")}<span className="unit">/ {total.toLocaleString("en-US")}</span></span>
            <span className="sub">{r.needed_rows.toLocaleString("en-US")} needed to rebuild</span>
          </div>
          <div className="tile">
            <span className="label">Validators served</span>
            <span className="value">{r.served_by_validators}<span className="unit">/ {r.assigned_validators}</span></span>
            <span className="sub">{r.attestation_known ? `${r.served_by_attested} of ${r.attested_validators} that signed` : "signatures not recorded"}</span>
          </div>
        </div>
      </div>
    </div>
  );
}

function Page() {
  const hash = useSearchParams().get("hash") ?? "";
  const { data, error, loading } = useApi<Detail>(hash ? `/v1/blobs/${hash}` : null);
  if (!hash) return <p className="notice">Open a blob from the <Link href="/blobs/">list</Link>, or add <code>?hash=&lt;promise hash&gt;</code>.</p>;
  if (error) return <p className="notice err">{error}</p>;
  if (loading || !data) return <p className="muted">Loading…</p>;
  const b = data.blob;
  const graceEnd = new Date(new Date(b.must_serve_until).getTime() + 150 * 1000).toISOString();
  const byPower = [...data.assignments].sort((a, c) => c.voting_power - a.voting_power || a.validator_address.localeCompare(c.validator_address));
  const lastByVal = new Map<string, Probe>();
  for (const p of data.probes) {
    const cur = lastByVal.get(p.validator_address);
    if (!cur || p.started_at > cur.started_at) lastByVal.set(p.validator_address, p);
  }
  return (
    <>
      <section className="card">
      <div className="card-head"><h1 className="mono" style={{ margin: 0 }}>{b.promise_hash.slice(0, 16)}…</h1><span className="chip">{nsDisplay(b.namespace)}</span><span className="chip">{bytes(b.blob_size)}</span></div>
      <dl className="kv">
        <dt>promise hash</dt><dd className="mono">{b.promise_hash}</dd>
        <dt>commitment</dt><dd className="mono">{b.commitment}</dd>
        <dt>namespace</dt><dd className="mono">{nsDisplay(b.namespace)} <span className="faint">{b.namespace}</span></dd>
        <dt>size</dt><dd className="mono">{bytes(b.blob_size)} <span className="muted">padded upload size</span></dd>
        <dt>publisher</dt><dd className="mono"><Link href={`/publisher/?addr=${b.signer}`}>{b.signer}</Link></dd>
        <dt>fee</dt><dd className="mono">{b.charge
          ? <>{tia(b.charge.fee_utia)} <span className="muted">· {b.charge.gas_units.toLocaleString("en-US")} gas at 1 utia/gas, from the padded size · {b.charge.settled ? "settled from escrow" : "not settled"}{b.charge.timed_out && <span className="err"> · timed out{b.charge.processor && b.charge.processor !== b.signer ? `, reported by ${shortBech(b.charge.processor)}` : ""}</span>}</span></>
          : <span className="muted">not recorded (publication ingested before payments were)</span>}</dd>
        <dt>settled</dt><dd className="mono">height {b.settlement_height.toLocaleString("en-US")} · {utc(b.settlement_time)} ({ago(b.settlement_time)})</dd>
        <dt>created</dt><dd className="mono">{utc(b.creation_timestamp)}</dd>
        <dt>must serve until</dt><dd className="mono">{utc(b.must_serve_until)} <span className="muted">= creation + max(payment_promise_timeout {data.params.payment_promise_timeout_s}s, shard_retention {data.params.shard_retention_s}s)</span></dd>
        <dt>assignment</dt><dd className="mono">{b.validators_with_rows} validators · {b.sigma_rows} rows assigned · {b.distinct_rows} distinct{b.assignment_error && <span className="err"> · {b.assignment_error}</span>}</dd>
      </dl>
      </section>
      <section className="card"><Recon b={b} /></section>
      <h2>Probes</h2>
      {data.probes.length === 0 ? <p className="muted">No probes yet.</p> : (
        <Timeline probes={data.probes} validators={byPower.map((a) => ({ address: a.validator_address, row_count: a.row_count, moniker: a.moniker }))} settled={b.settlement_time} mustServeUntil={b.must_serve_until} graceEnd={graceEnd} />
      )}
      
      <h2>Assigned validators</h2>
      <div className="tablewrap">
        <table>
          <caption>Rows recomputed with fibre-assign from the validator set at the promise height. Last verdict per validator.</caption>
          <thead><tr><th>validator</th><th className="right">voting power</th><th className="right">rows</th><th>obligation</th><th>last verdict</th><th>last probe (UTC)</th><th className="right">rows served</th></tr></thead>
          <tbody>
            {byPower.map((a) => {
              const p = lastByVal.get(a.validator_address);
              return (
                <tr key={a.validator_address}>
                  <td>
                    <Link href={`/validator/?addr=${a.validator_address}`}>{a.moniker || <span className="mono">{a.validator_address.slice(0, 12)}…</span>}</Link>
                    {a.moniker && <div className="faint mono">{a.validator_address.slice(0, 12)}…</div>}
                  </td>
                  <td className="right mono">{a.voting_power.toLocaleString("en-US")}</td>
                  <td className="right mono">{a.row_count}</td>
                  <td className={a.attested === false ? "muted" : ""} title={a.attested === true
                    ? "This validator's signature on the settled promise verified against its consensus key. The Fibre server writes the shard before it signs, so the signature is proof of storage."
                    : a.attested === false
                      ? "The settled promise carries no verified signature from this validator. The publisher stops collecting signatures once it has a safe quorum, so this means unproven, not absent: the validator may well hold the shard."
                      : "Recorded before the observer verified signatures."}>
                    {a.attested === true ? "proven" : a.attested === false ? "unproven" : "—"}
                  </td>
                  <td>{p ? <Verdict cls={p.classification} title={p.classification_reason} /> : <Verdict cls="NOT_PROBED" />}</td>
                  <td className="mono">{p ? `${utc(p.started_at)} (${p.schedule_label})` : "—"}</td>
                  <td className="right mono">{p && p.rows_expected ? `${p.rows_returned}/${p.rows_expected}` : "—"}</td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
    </>
  );
}

export default function BlobPage() {
  return <Suspense fallback={<p className="muted">Loading…</p>}><Page /></Suspense>;
}
