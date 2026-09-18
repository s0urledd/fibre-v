"use client";
import { Suspense, useState } from "react";
import Link from "next/link";
import { Panel } from "@/components/Panel";
import { useSearchParams } from "next/navigation";
import { useApi, type Blob, utc, ago, shortHex, nsDisplay, bytes } from "@/lib/api";
import { Mark, type Tier } from "@/components/Verdict";

// Reconstructability as a mark and a word, in the same channel the verdicts
// use, so "degraded" on this page means what "held out" means everywhere else.
function recon(b: Blob): { word: string; tier: Tier; title: string } {
  const r = b.reconstructable;
  if (!r || r.status === "unknown") {
    return b.probe_count === 0
      ? { word: "not probed", tier: "gap", title: "No probe has run for this blob." }
      : { word: "unknown", tier: "gap", title: "Row lists were not recorded for this publication, or no in-window point has been probed." };
  }
  if (r.status === "pending") {
    return { word: "pending", tier: "gap", title: `No in-window point is complete yet: ${r.probed_validators} of ${r.assigned_validators} assigned validators have a result at point ${r.point}. A validator without a row is a gap in observation, not a failure to serve.` };
  }
  if (r.status === "yes") {
    return { word: "yes", tier: "kept", title: `${r.served_distinct_rows.toLocaleString("en-US")} distinct rows served; every validator the promise proves owed this blob answered at point ${r.point}.` };
  }
  if (r.status === "degraded") {
    return { word: "degraded", tier: "hold", title: `Enough rows came back to rebuild the blob, but not every validator the promise proves owed it answered at point ${r.point}.` };
  }
  return { word: "no", tier: "fault", title: `Fewer than the ${r.needed_rows.toLocaleString("en-US")} rows needed came back at point ${r.point}.` };
}

function Page() {
  const nsParam = useSearchParams().get("namespace") ?? "";
  const [ns, setNs] = useState(nsParam);
  const [limit, setLimit] = useState(50);
  const q = ns.trim() ? `/v1/blobs?limit=${limit}&namespace=${encodeURIComponent(ns.trim())}` : `/v1/blobs?limit=${limit}`;
  const { data, error, loading } = useApi<{ blobs: Blob[] }>(q);
  return (
    <>
      <div className="section-head">
        <h1>Blobs</h1>
        <span className="sample">one row per <code>MsgPayForFibre</code>, newest first</span>
        <span className="spacer" />
        <input type="search" placeholder="Filter by namespace (56 hex)" value={ns} onChange={(e) => setNs(e.target.value)} aria-label="namespace filter" />
      </div>
      {error && <p className="notice err">{error}</p>}
      {loading && !data && <p className="muted">Loading…</p>}
      {data && (
        <Panel title="Publications" right={<>{data.blobs.length} newest{ns.trim() && ` in namespace ${ns.trim()}`}
              {data.blobs.length >= limit && limit < 500 && <> · <button className="btn" onClick={() => setLimit(Math.min(500, limit * 4))}>show more</button></>}</>}>
        <div className="tablewrap">
          <table>
            <thead><tr><th>promise</th><th>settled (UTC)</th><th className="right">height</th><th>namespace</th><th className="right">size</th><th className="right">validators</th><th className="right">probes</th><th>serve until</th><th>reconstructable</th></tr></thead>
            <tbody>
              {data.blobs.length === 0 && <tr><td colSpan={9} className="muted">No publications recorded{ns.trim() ? " in this namespace" : ""}.</td></tr>}
              {data.blobs.map((b) => (
                <tr key={b.promise_hash}>
                  <td className="mono"><Link href={`/blob/?hash=${b.promise_hash}`}>{shortHex(b.promise_hash, 6)}</Link></td>
                  <td className="mono" title={ago(b.settlement_time)}>{utc(b.settlement_time)}</td>
                  <td className="right mono">{b.settlement_height.toLocaleString("en-US")}</td>
                  <td className="mono" title={b.namespace}>{nsDisplay(b.namespace)}</td>
                  <td className="right mono">{bytes(b.blob_size)}</td>
                  <td className="right mono">{b.validators_with_rows}</td>
                  <td className="right mono">{b.probe_count}</td>
                  <td className="mono faint" title={`must serve until ${utc(b.must_serve_until)}`}>{ago(b.must_serve_until)}</td>
                  <td>{(() => { const rc = recon(b); return (
                    <span className={`verdict verdict--${rc.tier}`} title={rc.title}>
                      <Mark tier={rc.tier} /><span className="w">{rc.word}</span>
                    </span>); })()}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
        </Panel>
      )}
    </>
  );
}

export default function BlobsPage() {
  return <Suspense fallback={<p className="muted">Loading…</p>}><Page /></Suspense>;
}
