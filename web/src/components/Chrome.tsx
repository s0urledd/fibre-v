"use client";
import Link from "next/link";
import { usePathname } from "next/navigation";
import { useEffect, useState } from "react";
import { useApi, type Meta } from "@/lib/api";

/**
 * Sibling deployments of this observer on other networks, from
 * NEXT_PUBLIC_NETWORKS ("mainnet=https://observer.example.org,mocha=https://mocha.observer.example.org"),
 * baked in at build time. The one whose origin we are on is marked; the
 * others are links. One build serves every network, since each site talks
 * to its own same-origin /api.
 */
const NETWORKS: [string, string][] = (process.env.NEXT_PUBLIC_NETWORKS ?? "")
  .split(",").map((s) => s.trim()).filter(Boolean)
  .map((s) => { const i = s.indexOf("="); return i > 0 ? [s.slice(0, i).trim(), s.slice(i + 1).trim()] as [string, string] : null; })
  .filter((x): x is [string, string] => !!x);

function NetworkLinks() {
  const [origin, setOrigin] = useState("");
  useEffect(() => { setOrigin(window.location.origin); }, []);
  if (NETWORKS.length < 2) return null;
  return (
    <span className="networks" role="group" aria-label="network">
      {NETWORKS.map(([name, url]) => {
        const here = origin !== "" && url.replace(/\/$/, "") === origin;
        return here
          ? <span key={name} className="net on" aria-current="true">{name}</span>
          : <a key={name} className="net" href={url}>{name}</a>;
      })}
    </span>
  );
}

const NAV: [string, string][] = [
  ["/", "Overview"],
  ["/blobs/", "Blobs"],
  ["/publishers/", "Publishers"],
  ["/methodology/", "Methodology"],
];

/** The mark: a blob's rows, one of them this validator's shard. */
function Mark() {
  return (
    <svg className="mark-logo" width="16" height="16" viewBox="0 0 16 16" aria-hidden="true">
      <rect x="1" y="1" width="14" height="14" rx="2" fill="none" stroke="currentColor" strokeWidth="1.5" />
      <rect x="4" y="4" width="8" height="2" fill="currentColor" opacity=".35" />
      <rect x="4" y="7" width="8" height="2" fill="currentColor" />
      <rect x="4" y="10" width="8" height="2" fill="currentColor" opacity=".35" />
    </svg>
  );
}

type Theme = "system" | "light" | "dark";

/** Three states, in the order a reader expects: follow the OS, then light, then dark. */
function ThemeToggle() {
  const [theme, setTheme] = useState<Theme>("system");
  useEffect(() => {
    try {
      const t = localStorage.getItem("theme");
      if (t === "light" || t === "dark") setTheme(t);
    } catch { /* storage unavailable: stay on system */ }
  }, []);
  const apply = (t: Theme) => {
    setTheme(t);
    const root = document.documentElement;
    if (t === "system") delete root.dataset.theme; else root.dataset.theme = t;
    try { if (t === "system") localStorage.removeItem("theme"); else localStorage.setItem("theme", t); } catch { /* fine */ }
  };
  const next: Theme = theme === "system" ? "light" : theme === "light" ? "dark" : "system";
  const title = theme === "system" ? "Theme: follows your system. Click for light." : theme === "light" ? "Theme: light. Click for dark." : "Theme: dark. Click to follow your system.";
  return (
    <button type="button" className="theme" onClick={() => apply(next)} title={title} aria-label={title}>
      {theme === "dark" ? (
        <svg width="14" height="14" viewBox="0 0 16 16" aria-hidden="true"><path d="M13.5 9.5A6 6 0 0 1 6.5 2.5a6 6 0 1 0 7 7z" fill="currentColor" /></svg>
      ) : theme === "light" ? (
        <svg width="14" height="14" viewBox="0 0 16 16" aria-hidden="true"><circle cx="8" cy="8" r="3.2" fill="currentColor" /><g stroke="currentColor" strokeWidth="1.4" strokeLinecap="round"><path d="M8 1.5v2M8 12.5v2M1.5 8h2M12.5 8h2M3.4 3.4l1.4 1.4M11.2 11.2l1.4 1.4M3.4 12.6l1.4-1.4M11.2 4.8l1.4-1.4" /></g></svg>
      ) : (
        <svg width="14" height="14" viewBox="0 0 16 16" aria-hidden="true"><circle cx="8" cy="8" r="5.5" fill="none" stroke="currentColor" strokeWidth="1.4" /><path d="M8 2.5a5.5 5.5 0 0 1 0 11z" fill="currentColor" /></svg>
      )}
    </button>
  );
}

export function Header() {
  const { data: meta } = useApi<Meta>("/v1/meta", 30000);
  const path = usePathname();
  const health = meta?.health ?? (meta?.collector?.alive ? "ok" : "down");
  const comps = meta?.components ?? [];
  const dead = comps.filter((c) => !c.alive).map((c) => c.component);
  const failing = comps.filter((c) => c.alive && !c.ok).map((c) => c.component);
  const height = meta?.chain_height || meta?.last_scanned_height;
  const chipTitle = [
    health === "ok" ? "Every observer process is running." : health === "degraded"
      ? `Observer degraded: ${dead.length ? `not running: ${dead.join(", ")}` : ""}${dead.length && failing.length ? "; " : ""}${failing.length ? `failing: ${failing.join(", ")}` : ""}.`
      : "No observer process is running; figures are from the last run.",
    meta?.app_version ? (meta.fibre_active ? `App version ${meta.app_version}: Fibre is live.` : `App version ${meta.app_version}; Fibre needs ${meta.fibre_app_version || "10"}.`) : "",
  ].filter(Boolean).join(" ");
  return (
    <header className="top blur">
      <div className="wrap">
        <Link className="brand" href="/"><Mark />Fibrescope<span className="brand-sub">Celestia Fibre observer · by Huginn Tech</span></Link>
        <nav>
          {NAV.map(([href, name]) => (
            <Link key={href} href={href} className={(href === "/" ? path === "/" : path.startsWith(href)) ? "on" : ""}>{name}</Link>
          ))}
        </nav>
        <span className="spacer" />
        <span className="chips">
          <NetworkLinks />
          {meta ? (
            <span className="chip" title={chipTitle}>
              <i className={"dot " + (health === "ok" ? "ok" : health === "degraded" ? "hold" : "fault")} />
              {meta.chain_id || "chain ?"}{height ? ` · #${Number(height).toLocaleString("en-US")}` : ""}
            </span>
          ) : <span className="chip"><i className="dot" />connecting…</span>}
          <ThemeToggle />
        </span>
      </div>
    </header>
  );
}
