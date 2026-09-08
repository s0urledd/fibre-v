"use client";
import Link from "next/link";
import { useApi, type Meta, utc, ago } from "@/lib/api";

export function Header() {
  const { data: meta } = useApi<Meta>("/v1/meta", 30000);
  return (
    <header className="top">
      <div className="wrap">
        <Link className="brand" href="/">Fibre observer</Link>
        <nav>
          <Link href="/">Overview</Link>
          <Link href="/blobs/">Blobs</Link>
          <Link href="/methodology/">Methodology</Link>
          <Link href="/about/">About</Link>
          <Link href="/api-docs/">API</Link>
        </nav>
        <span className="spacer" />
        <span className="chain mono muted">
          {meta ? <>chain {meta.chain_id || "?"} · collector h{meta.last_scanned_height || "?"}</> : <>connecting to API…</>}
        </span>
      </div>
    </header>
  );
}

export function Banner() {
  const { data: meta } = useApi<Meta>("/v1/meta", 60000);
  const one = !meta || meta.observed_from_one_location;
  return (
    <div className="banner">
      <div className="wrap">
        {one ? (
          <>Observed from one location{meta ? ` (vantage “${meta.vantage}”)` : ""}. A failed probe means this vantage could not fetch the rows at that time; it is not proof the validator is down. A successful probe is not proof of availability from elsewhere. <Link href="/methodology/#vantage">How probing works →</Link></>
        ) : (
          <>Observed from {meta?.vantage_count} locations. <Link href="/methodology/#vantage">How probing works →</Link></>
        )}
      </div>
    </div>
  );
}

export function Footer() {
  const { data: meta, error } = useApi<Meta>("/v1/meta", 30000);
  const lastBeat = meta?.prober?.last_heartbeat_at ?? meta?.collector?.last_heartbeat_at ?? null;
  const stale = !!lastBeat && Date.now() - new Date(lastBeat).getTime() > 20 * 60 * 1000;
  return (
    <footer className={"foot" + (stale ? " stale" : "")}>
      <div className="wrap mono">
        {error && <>API unreachable: {error}. </>}
        {meta && (
          <>
            {stale && <strong>Data is stale (last observer heartbeat {ago(lastBeat)}). </strong>}
            collector h{meta.last_scanned_height || "?"} {meta.collector ? (meta.collector.alive ? "· collector alive" : "· collector stopped") : "· no collector run"}
            {meta.prober ? (meta.prober.alive ? " · prober alive" : " · prober stopped") : " · no prober run"}
            {" · vantage "}{meta.vantage}
            {meta.pinned_celestia_app_commit && <> · celestia-app {meta.pinned_celestia_app_commit.slice(0, 9)}</>}
            {" · server time "}{utc(meta.server_time)}
          </>
        )}
        {" · "}<a href="https://github.com/plsgiveup/fibre">source</a>
      </div>
    </footer>
  );
}
