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
        <span className="chain">
          {meta ? <>{meta.chain_id || "?"} · h{meta.chain_height || meta.last_scanned_height || "?"}</> : <>connecting…</>}
        </span>
      </div>
    </header>
  );
}

/**
 * Where this watches from.
 *
 * Not a dismissable notice and not styled as a warning: every reachability
 * verdict on this site is a statement about a network path and half of that
 * path is ours, so it is a permanent property of the instrument and it is drawn
 * as chrome. It carries --hold, the colour for "this observer could not
 * complete the measurement", and never --fault, because it qualifies our own
 * reach rather than accusing anyone.
 */
export function Banner() {
  const { data: meta } = useApi<Meta>("/v1/meta", 60000);
  const one = !meta || meta.observed_from_one_location;
  const v = meta?.vantage_info;
  const where = [v?.location, v?.provider, v?.asn].filter(Boolean).join(", ");
  return (
    <div className="vantage">
      <div className="wrap">
        {one ? (
          <>
            Observed from one location{where ? `: ${where}` : meta ? ` (vantage “${meta.vantage}”, undescribed)` : ""}. A failed
            probe means this vantage could not fetch the rows at that time; it is not proof the validator is down. A successful
            probe is not proof of availability from elsewhere.{" "}
            <Link href="/about/">Where this watches from</Link> · <Link href="/methodology/#vantage">What one vantage can say</Link>
          </>
        ) : (
          <>Observed from {meta?.vantage_count} locations. <Link href="/methodology/#vantage">How probing works</Link></>
        )}
      </div>
    </div>
  );
}

export function Footer() {
  const { data: meta, error } = useApi<Meta>("/v1/meta", 30000);
  const lastBeat = meta?.collector?.last_heartbeat_at ?? null;
  const stale = !!lastBeat && Date.now() - new Date(lastBeat).getTime() > 20 * 60 * 1000;
  return (
    <footer className={"foot" + (stale ? " stale" : "")}>
      <div className="wrap">
        {error && <>API unreachable: {error}. </>}
        {meta && (
          <>
            {stale && <strong>Data is stale — last observer heartbeat {ago(lastBeat)}. </strong>}
            collector h{meta.last_scanned_height || "?"} {meta.collector ? (meta.collector.alive ? "· alive" : "· stopped") : "· no run"}
            {meta.last_probe_at ? <> · last probe {ago(meta.last_probe_at)}</> : " · no probe yet"}
            {" · vantage "}{meta.vantage}
            {meta.pinned_celestia_app_commit && <> · celestia-app {meta.pinned_celestia_app_commit.slice(0, 9)}</>}
            {" · "}{utc(meta.server_time)}
          </>
        )}
        {" · "}<a href="https://github.com/plsgiveup/fibre">source</a>
      </div>
    </footer>
  );
}
