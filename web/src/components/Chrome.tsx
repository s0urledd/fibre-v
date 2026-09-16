"use client";
import Link from "next/link";
import { usePathname } from "next/navigation";
import { useApi, type Meta, utc, ago } from "@/lib/api";

const NAV: [string, string][] = [
  ["/", "Overview"],
  ["/blobs/", "Blobs"],
  ["/methodology/", "Methodology"],
  ["/api-docs/", "API"],
  ["/about/", "About"],
];

export function Header() {
  const { data: meta } = useApi<Meta>("/v1/meta", 30000);
  const path = usePathname();
  const alive = meta?.collector?.alive ?? false;
  const height = meta?.chain_height || meta?.last_scanned_height;
  return (
    <header className="top blur">
      <div className="wrap">
        <Link className="brand" href="/"><i aria-hidden="true" />Muninn<span className="brand-sub">Fibre observer</span></Link>
        <nav>
          {NAV.map(([href, name]) => (
            <Link key={href} href={href} className={(href === "/" ? path === "/" : path.startsWith(href)) ? "on" : ""}>{name}</Link>
          ))}
        </nav>
        <span className="spacer" />
        <span className="chips">
          {meta ? (
            <>
              <span className="chip" title={alive ? "Collector is following the chain." : "Collector stopped; figures are from its last run."}>
                <i className={"dot " + (alive ? "ok" : "hold")} />
                {meta.chain_id || "chain ?"}{height ? ` · #${Number(height).toLocaleString("en-US")}` : ""}
              </span>
              {meta.app_version && (
                meta.fibre_active
                  ? <span className="chip ok" title={`App version ${meta.app_version}: x/fibre and x/valaddr are live.`}>Fibre live</span>
                  : <span className="chip hold" title={`App version ${meta.app_version}; Fibre needs ${meta.fibre_app_version || "10"}.`}>Fibre not live · v{meta.app_version}</span>
              )}
            </>
          ) : <span className="chip"><i className="dot" />connecting…</span>}
        </span>
      </div>
    </header>
  );
}

/**
 * Where this watches from. One line, always shown: every reachability figure
 * on the site is a statement about one network path.
 */
export function Banner() {
  const { data: meta } = useApi<Meta>("/v1/meta", 60000);
  const v = meta?.vantage_info;
  const where = [v?.location, v?.provider, v?.asn].filter(Boolean).join(" · ");
  const one = !meta || meta.observed_from_one_location;
  return (
    <div className="vantage">
      <div className="wrap">
        <i className="dot" aria-hidden="true" />
        {one ? (
          <span>
            All checks run from one location{where ? ` (${where})` : meta ? ` (vantage “${meta.vantage}”, not described)` : ""}.
            A failed check can be a network problem on our side.
          </span>
        ) : (
          <span>Checks run from {meta?.vantage_count} locations.</span>
        )}
        <Link href="/about/">Details</Link>
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
        {error && <span>API unreachable: {error}.</span>}
        {stale && <strong>Data is stale: last collector heartbeat {ago(lastBeat)}.</strong>}
        {meta && (
          <>
            <span>collector #{meta.last_scanned_height || "?"} {meta.collector ? (meta.collector.alive ? "· alive" : "· stopped") : "· no run"}</span>
            <span>{meta.last_probe_at ? `last probe ${ago(meta.last_probe_at)}` : "no probe yet"}</span>
            <span>vantage {meta.vantage}</span>
            {meta.pinned_celestia_app_commit && <span>celestia-app {meta.pinned_celestia_app_commit.slice(0, 9)}</span>}
            <span>{utc(meta.server_time)}</span>
          </>
        )}
        <a href="https://github.com/plsgiveup/fibre">source</a>
      </div>
    </footer>
  );
}
