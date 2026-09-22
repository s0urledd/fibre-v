"use client";
import Link from "next/link";
import { usePathname } from "next/navigation";
import { useEffect, useRef, useState } from "react";
import { useApi, type Meta, int, ago, utcWord } from "@/lib/api";
import { SOURCE_URL } from "@/lib/site";

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

const NAV: [string, string][] = [
  ["/", "Overview"],
  ["/blobs/", "Blobs"],
  ["/publishers/", "Publishers"],
  ["/methodology/", "Methodology"],
];

/** The mark: a blob's rows, one of them this validator's shard. */
function Mark() {
  return (
    <svg className="mark-logo" width="22" height="22" viewBox="0 0 16 16" aria-hidden="true">
      <rect x="1" y="1" width="14" height="14" rx="2.5" fill="none" stroke="currentColor" strokeWidth="1.5" />
      <rect x="4" y="4" width="8" height="1.6" fill="currentColor" opacity=".35" />
      <rect x="4" y="7.2" width="8" height="1.6" fill="currentColor" />
      <rect x="4" y="10.4" width="8" height="1.6" fill="currentColor" opacity=".35" />
    </svg>
  );
}

type Theme = "light" | "dark";

/** Two states, sun and moon. The first visit starts from the system
 *  preference; from then on the choice is the reader's and is remembered. */
function ThemeToggle() {
  const [theme, setTheme] = useState<Theme>("light");
  useEffect(() => {
    let t: string | null = null;
    try { t = localStorage.getItem("theme"); } catch { /* storage unavailable */ }
    if (t !== "light" && t !== "dark") {
      t = window.matchMedia?.("(prefers-color-scheme: dark)").matches ? "dark" : "light";
    }
    setTheme(t as Theme);
    document.documentElement.dataset.theme = t;
  }, []);
  const apply = (t: Theme) => {
    setTheme(t);
    document.documentElement.dataset.theme = t;
    try { localStorage.setItem("theme", t); } catch { /* fine */ }
  };
  const next: Theme = theme === "light" ? "dark" : "light";
  const title = theme === "light" ? "Theme: light. Switch to dark." : "Theme: dark. Switch to light.";
  return (
    <button type="button" className="theme" onClick={() => apply(next)} title={title} aria-label={title}>
      {theme === "dark" ? (
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true"><path d="M20 14.5A8 8 0 0 1 9.5 4a8 8 0 1 0 10.5 10.5z" /></svg>
      ) : (
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" aria-hidden="true"><circle cx="12" cy="12" r="4" /><path d="M12 2.5v2.5M12 19v2.5M2.5 12H5M19 12h2.5M4.9 4.9l1.8 1.8M17.3 17.3l1.8 1.8M4.9 19.1l1.8-1.8M17.3 6.7l1.8-1.8" /></svg>
      )}
    </button>
  );
}

/** "mocha-5" as the chip prints it; the mainnet chain id is "celestia" */
function netName(id: string | undefined): string {
  if (!id) return "network";
  if (id === "celestia" || /^mainnet$/i.test(id)) return "Mainnet";
  return id.charAt(0).toUpperCase() + id.slice(1);
}

/**
 * The network chip: which chain this observer watches, with the observer's
 * own state as its dot. It opens as a menu: this site's network, any sibling
 * deployment configured in NEXT_PUBLIC_NETWORKS as a link, and Mainnet as a
 * placeholder until Fibre is there and an observer follows it.
 */
function NetworkChip({ meta, error }: { meta: Meta | null; error: string | null }) {
  const [origin, setOrigin] = useState("");
  const box = useRef<HTMLDetailsElement>(null);
  useEffect(() => { setOrigin(window.location.origin); }, []);
  // a click elsewhere or Escape closes the menu, as a menu is expected to
  useEffect(() => {
    const close = (e: Event) => { const el = box.current; if (el?.open && !(e instanceof KeyboardEvent ? false : el.contains(e.target as Node))) el.open = false; };
    const key = (e: KeyboardEvent) => { if (e.key === "Escape" && box.current?.open) box.current.open = false; };
    document.addEventListener("click", close);
    document.addEventListener("keydown", key);
    return () => { document.removeEventListener("click", close); document.removeEventListener("keydown", key); };
  }, []);
  const health = meta?.health;
  const dot = error && !meta ? "none" : !meta ? "none" : health === "ok" ? "ok" : "hold";
  const title = !meta
    ? (error ? `Observer API unreachable: ${error}` : "Connecting to the observer API…")
    : [
      `network ${meta.chain_id}`,
      error ? `observer state unknown: API unreachable (${error})` : `observer ${health === "ok" ? "healthy" : health}`,
      meta.chain_height ? `chain tip #${int(Number(meta.chain_height))}` : "",
      meta.app_version ? (meta.fibre_active ? `Fibre live on app v${meta.app_version}` : `Fibre not live: app v${meta.app_version}`) : "",
    ].filter(Boolean).join(" · ");
  const label = meta ? netName(meta.chain_id) : "connecting…";
  const others = NETWORKS.filter(([, url]) => origin === "" || url.replace(/\/$/, "") !== origin);
  const mainnetHere = meta?.chain_id === "celestia";
  const mainnetLinked = others.some(([name]) => netName(name) === "Mainnet");
  return (
    <details className="net menu" title={title} ref={box}>
      <summary><i className={"dot " + dot} />{label}</summary>
      <div className="list" role="menu">
        <span aria-current="true">{label}<i className="tagx">this site</i></span>
        {others.map(([name, url]) => <a key={name} href={url} role="menuitem">{netName(name)}</a>)}
        {!mainnetHere && !mainnetLinked && (
          <span className="soon" aria-disabled="true" title="Fibre is not on mainnet yet. This observer will follow it there.">Mainnet<i className="tagx">soon</i></span>
        )}
      </div>
    </details>
  );
}

export function Header() {
  const { data: meta, error } = useApi<Meta>("/v1/meta", 30000);
  const path = usePathname();
  return (
    <header className="top">
      <div className="wrap">
        <Link className="brand" href="/"><Mark />Fibrescope</Link>
        <nav aria-label="site">
          {NAV.map(([href, name]) => (
            <Link key={href} href={href} className={(href === "/" ? path === "/" : path.startsWith(href)) ? "on" : ""}>{name}</Link>
          ))}
        </nav>
        <div className="right">
          <NetworkChip meta={meta} error={error} />
          <span className="divider" aria-hidden="true" />
          <ThemeToggle />
        </div>
      </div>
    </header>
  );
}

/**
 * The footer: the three kinds of evidence every figure on the site rests on,
 * and who runs the observer. Nothing that changes with the data.
 */
export function Footer() {
  const { data: meta } = useApi<Meta>("/v1/meta", 30000);
  const pinned = meta?.pinned_celestia_app_commit ? meta.pinned_celestia_app_commit.slice(0, 7) : "";
  return (
    <footer className="foot">
      <div>
        <Link href="/methodology/#evidence" title="A count of something the chain recorded; nothing measured here.">Chain records</Link><span>·</span>
        <Link href="/methodology/#evidence" title="Bytes fetched and verified against the on-chain commitment, or a certificate checked against the validator's consensus key.">Verified responses</Link><span>·</span>
        <Link href="/methodology/#evidence" title="What this observer's own network saw from one location.">Observer measurements</Link>
      </div>
      <div title={meta?.server_time ? `Observer time ${utcWord(meta.server_time)}${meta.last_probe_at ? ` · newest probe ${ago(meta.last_probe_at)}` : ""}` : undefined}>
        Fibrescope · Celestia Fibre observer · by Huginn Tech · independent observation
        {pinned && <span title={`Assignment pinned to celestia-app ${meta!.pinned_celestia_app_commit} · pin ${meta!.pin_status}`}> · pin {pinned}</span>}
        <span>·</span><a href={SOURCE_URL} rel="noopener noreferrer" target="_blank">source</a>
      </div>
    </footer>
  );
}
