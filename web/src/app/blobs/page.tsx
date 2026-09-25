"use client";
import { Suspense, useState } from "react";
import Link from "next/link";
import { Panel } from "@/components/Panel";
import { useSearchParams } from "next/navigation";
import PreLive from "@/components/PreLive";
import { unit } from "@/components/Unit";
import { useApi, type Meta, type Market, type Blob, type NamespaceRow, utc, ago, shortHex, nsDisplay, bytes, int } from "@/lib/api";
import { Mark, type Tier } from "@/components/Verdict";
import VolumeChart from "@/components/VolumeChart";

// Reconstructability as a mark and a word, in the same channel the verdicts
// use, so "degraded" on this page means what "held out" means everywhere else.
function recon(b: Blob): { word: string; tier: Tier; title: string } {
  const r = b.reconstructable;
  if (b.sampled_out && (!r || r.status === "unknown")) {
    return { word: "sampled out", tier: "gap", title: `The load policy drew this blob out of its sample at p=${b.sampled_out.p.toFixed(2)}: not probed at any point, recorded once.` };
  }
  if (!r || r.status === "unknown") {
    return b.probe_count === 0
      ? { word: "not probed", tier: "gap", title: "No probe has run for this blob." }
      : { word: "unknown", tier: "gap", title: "Row lists were not recorded for this publication, or no in-window point has been probed." };
  }
  if (r.status === "pending") {
    return { word: "not judged yet", tier: "gap", title: `No in-window point is complete yet: ${r.probed_validators} of ${r.assigned_validators} assigned validators have a result at point ${r.point}. A validator without a row is a gap in observation, not a failure to serve.` };
  }
  if (r.status === "yes") {
    return { word: "fully served", tier: "kept", title: `${r.served_distinct_rows.toLocaleString("en-US")} distinct rows served; every validator the promise proves owed this blob answered at point ${r.point}.` };
  }
  if (r.status === "degraded") {
    return { word: "rebuildable", tier: "hold", title: `Enough rows came back to rebuild the blob, but not every validator the promise proves owed it answered at point ${r.point}.` };
  }
  // Not a fault: which validators did not answer, and whether that was this observer's own path, is on the blob's page.
  return { word: "not rebuildable", tier: "hold", title: `Fewer than the ${r.needed_rows.toLocaleString("en-US")} rows needed came back at point ${r.point}. Unreachable from here is never counted as broken.` };
}

function Page() {
  const nsParam = useSearchParams().get("namespace") ?? "";
  const [ns, setNs] = useState(nsParam);
  const [limit, setLimit] = useState(50);
  const q = ns.trim() ? `/v1/blobs?limit=${limit}&namespace=${encodeURIComponent(ns.trim())}` : `/v1/blobs?limit=${limit}`;
  const { data, error, loading } = useApi<{ blobs: Blob[] }>(q);
  const { data: meta } = useApi<Meta>("/v1/meta");
  const nss = useApi<{ namespaces: NamespaceRow[] }>("/v1/namespaces?limit=20");
  const market = useApi<Market>("/v1/market?window=7d");
  return (
    <>
      <div className="section-head">
        <h1>Blobs</h1>
        <span className="spacer" />
        <input type="search" placeholder="Filter by namespace (56 hex)" value={ns} onChange={(e) => setNs(e.target.value)} aria-label="namespace filter" />
      </div>
      <PreLive meta={meta} />
      {error && !data && <p className="notice">The observer API is not answering ({error}); the page retries every 30 seconds. This is an observer outage, not a Fibre network outage.</p>}
      {error && data && <p className="sample">Showing the last list received; the API is not answering right now ({error}).</p>}
      {loading && !data && <p className="muted">Loading…</p>}
      <Panel title="Settled volume · 7 days" right="UTC days · padded size">
        <VolumeChart market={market.data} />
      </Panel>
      {data && (
        <Panel title="Publications" right={<>{data.blobs.length} newest{ns.trim() && ` in namespace ${ns.trim()}`}
              {data.blobs.length >= limit && limit < 500 && <> · <button className="btn" onClick={() => setLimit(Math.min(500, limit * 4))}>show more</button></>}</>}>
        <div className="tablewrap">
          <table>
            <thead><tr><th>promise</th><th>settled (UTC)</th><th className="right">height</th><th>namespace</th><th className="right">size</th><th className="right">validators</th><th className="right" title="Share of stake whose signature over the promise verified. A blob settles at two thirds.">signed</th><th className="right">probes</th><th>serve until</th><th>availability</th></tr></thead>
            <tbody>
              {data.blobs.length === 0 && <tr><td colSpan={10} className="muted">No publications recorded{ns.trim() ? " in this namespace" : ""}.</td></tr>}
              {data.blobs.map((b) => (
                <tr key={b.promise_hash}>
                  <td className="mono"><Link href={`/blob/?hash=${b.promise_hash}`}>{shortHex(b.promise_hash, 6)}</Link></td>
                  <td className="mono" title={ago(b.settlement_time)}>{utc(b.settlement_time)}</td>
                  <td className="right mono">{b.settlement_height.toLocaleString("en-US")}</td>
                  <td className="mono" title={b.namespace}>{nsDisplay(b.namespace)}</td>
                  <td className="right mono">{unit(bytes(b.blob_size))}</td>
                  <td className="right mono">{b.validators_with_rows}</td>
                  <td className="right mono">{b.attested_voting_power != null && b.total_voting_power ? `${(100 * b.attested_voting_power / b.total_voting_power).toFixed(1)}%` : "—"}</td>
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
      {nss.data && nss.data.namespaces.length > 0 && (
        <Panel title="Namespaces" right={ns.trim() ? <button className="btn" onClick={() => setNs("")}>show all</button> : undefined}>
          <div className="tablewrap">
            <table>
              <thead><tr><th>namespace</th><th className="right">data</th><th className="right">blobs</th><th className="right">last 24h</th><th className="right">accounts</th><th>first seen</th><th>last blob</th></tr></thead>
              <tbody>
                {nss.data.namespaces.map((n) => (
                  <tr key={n.namespace} className={ns.trim().toLowerCase() === n.namespace ? "on" : undefined}>
                    <td title={n.namespace}><button className="rowlink mono" onClick={() => setNs(n.namespace)}>{nsDisplay(n.namespace)}</button></td>
                    <td className="right">{bytes(n.bytes)}</td>
                    <td className="right">{int(n.blobs)}</td>
                    <td className="right">{n.blobs_24h > 0 ? <>{bytes(n.bytes_24h)} <span className="soft">· {int(n.blobs_24h)}</span></> : "—"}</td>
                    <td className="right">{int(n.accounts)}</td>
                    <td title={utc(n.first_seen)}>{ago(n.first_seen)}</td>
                    <td title={utc(n.last_blob)}>{ago(n.last_blob)}</td>
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
