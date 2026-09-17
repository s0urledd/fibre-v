"use client";
import Link from "next/link";
import { usePathname } from "next/navigation";
import { useApi, type Meta } from "@/lib/api";

const NAV: [string, string][] = [
  ["/", "Overview"],
  ["/blobs/", "Blobs"],
  ["/methodology/", "Methodology"],
];

export function Header() {
  const { data: meta } = useApi<Meta>("/v1/meta", 30000);
  const path = usePathname();
  const alive = meta?.collector?.alive ?? false;
  const height = meta?.chain_height || meta?.last_scanned_height;
  return (
    <header className="top blur">
      <div className="wrap">
        <Link className="brand" href="/"><i aria-hidden="true" />Fibrescope<span className="brand-sub">Celestia Fibre observer · by Huginn Tech</span></Link>
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
