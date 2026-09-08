"use client";
import { Suspense, useState } from "react";
import Link from "next/link";
import { useSearchParams } from "next/navigation";
import { useApi, type Blob, utc, ago, shortHex, nsDisplay, bytes } from "@/lib/api";

function recon(b: Blob): string {
  const r = b.reconstructable;
  if (!r || r.status === "unknown") return b.probe_count === 0 ? "not probed" : "unknown";
  return r.status + (r.window_over ? " (window over)" : "");
}

function Page() {
  const nsParam = useSearchParams().get("namespace") ?? "";
  const [ns, setNs] = useState(nsParam);
  const q = ns.trim() ? `/v1/blobs?limit=200&namespace=${encodeURIComponent(ns.trim())}` : "/v1/blobs?limit=200";
  const { data, error, loading } = useApi<{ blobs: Blob[] }>(q);
  return (
    <>
      <h1>Blobs</h1>
      <p className="muted">One row per <code>MsgPayForFibre</code> settled on chain. Newest first. Reconstructable means at least the needed number of distinct rows were served at the last in-window probe.</p>
      <div className="controls">
        <input type="search" placeholder="namespace (56 hex)" value={ns} onChange={(e) => setNs(e.target.value)} aria-label="namespace filter" />
      </div>
      {error && <p className="notice err">{error}</p>}
      {loading && !data && <p className="muted">Loading…</p>}
      {data && (
        <div className="tablewrap">
          <table>
            <caption>{data.blobs.length} publications{ns.trim() && ` in namespace ${ns.trim()}`}</caption>
            <thead><tr><th>settled (UTC)</th><th className="right">height</th><th>promise</th><th>namespace</th><th className="right">size</th><th className="right">validators</th><th>must serve until</th><th className="right">probes</th><th>reconstructable</th></tr></thead>
            <tbody>
              {data.blobs.length === 0 && <tr><td colSpan={9} className="muted">No publications recorded{ns.trim() ? " in this namespace" : ""}.</td></tr>}
              {data.blobs.map((b) => (
                <tr key={b.promise_hash}>
                  <td className="mono" title={ago(b.settlement_time)}>{utc(b.settlement_time)}</td>
                  <td className="right mono">{b.settlement_height.toLocaleString("en-US")}</td>
                  <td className="mono"><Link href={`/blob/?hash=${b.promise_hash}`}>{shortHex(b.promise_hash, 6)}</Link></td>
                  <td className="mono" title={b.namespace}>{nsDisplay(b.namespace)}</td>
                  <td className="right mono">{bytes(b.blob_size)}</td>
                  <td className="right mono">{b.validators_with_rows}</td>
                  <td className="mono">{utc(b.must_serve_until)}</td>
                  <td className="right mono">{b.probe_count}</td>
                  <td>{recon(b)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </>
  );
}

export default function BlobsPage() {
  return <Suspense fallback={<p className="muted">Loading…</p>}><Page /></Suspense>;
}
