"use client";
import { Suspense } from "react";
import Link from "next/link";
import { useSearchParams } from "next/navigation";
import { useApi, type Blob, type Probe, utc, ago, bytes, nsDisplay } from "@/lib/api";
import Verdict from "@/components/Verdict";
import Timeline from "@/components/Timeline";
import Square from "@/components/Square";

type Detail = {
  blob: Blob;
  params: { shard_retention_s: number; payment_promise_timeout_s: number };
  assignments: { validator_address: string; moniker?: string; voting_power: number; row_count: number; attested: boolean | null }[];
  probes: Probe[];
};

function Recon({ b }: { b: Blob }) {
  const r = b.reconstructable;
  if (!r || r.status === "unknown") {
    return <div className="notice"><strong>Reconstructable: unknown.</strong> <span className="muted">{b.probe_count === 0 ? "No probe has run for this blob yet." : "Row lists were not recorded for this publication, or no in-window point has been probed."}</span></div>;
  }
  if (r.status === "pending") {
    return <div className="notice"><strong>Reconstructable: pending.</strong> <span className="muted">No in-window point is complete yet: {r.probed_validators} of {r.assigned_validators} assigned validators have a result at point <span className="mono">{r.point}</span>. A validator without a row is a gap in observation, not a failure to serve.</span></div>;
  }
  // The encoded row count comes from the API (blob v0: 16,384); never assume a
  // ratio. The square is the figure; the sentence under it is the qualification.
  const total = r.total_rows > 0 ? r.total_rows : r.needed_rows;
  return (
    <div className="figure">
      <span className="label">Reconstructable: {r.status}</span>
      <Square served={r.served_distinct_rows} needed={r.needed_rows} total={total}
        label={`at point ${r.point}`} />
      <p className="sample">
        {r.served_by_validators} of {r.assigned_validators} assigned validators served at point{" "}
        <span className="mono">{r.point}</span>
        {r.attestation_known && <>, {r.served_by_attested} of the {r.attested_validators} the promise proves stored it</>}.
        {" "}Based on rows observed from one location at the last in-window probe ({utc(r.point_at)}); rows not probed are not counted.
        {r.window_over && " The retention window has since ended, so shards may be pruned as specified."}
        {" "}Degraded means enough rows were served but not every validator proven to owe this blob answered.
        {r.attestation_known && r.attested_validators < r.assigned_validators && (
          <> {r.assigned_validators - r.attested_validators} of the {r.assigned_validators} assigned validators carry no verified
          signature on this promise, so nothing proves they ever stored it. They cannot demote this verdict by staying quiet.</>
        )}
      </p>
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
        <dt>publisher</dt><dd className="mono">{b.signer}</dd>
        <dt>settled</dt><dd className="mono">height {b.settlement_height.toLocaleString("en-US")} · {utc(b.settlement_time)} ({ago(b.settlement_time)})</dd>
        <dt>created</dt><dd className="mono">{utc(b.creation_timestamp)}</dd>
        <dt>must serve until</dt><dd className="mono">{utc(b.must_serve_until)} <span className="muted">= creation + max(payment_promise_timeout {data.params.payment_promise_timeout_s}s, shard_retention {data.params.shard_retention_s}s)</span></dd>
        <dt>assignment</dt><dd className="mono">{b.validators_with_rows} validators · {b.sigma_rows} rows assigned · {b.distinct_rows} distinct{b.assignment_error && <span className="err"> · {b.assignment_error}</span>}</dd>
      </dl>
      </section>
      <section className="card"><Recon b={b} /></section>
      <h2>Probe timeline</h2>
      {data.probes.length === 0 ? <p className="muted">No probes yet.</p> : (
        <Timeline probes={data.probes} validators={byPower.map((a) => ({ address: a.validator_address, row_count: a.row_count, moniker: a.moniker }))} settled={b.settlement_time} mustServeUntil={b.must_serve_until} graceEnd={graceEnd} />
      )}
      
      <h2>Assigned validators</h2>
      <div className="tablewrap">
        <table>
          <caption>Rows recomputed with fibre-assign from the validator set at the promise height; last verdict per validator.</caption>
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
