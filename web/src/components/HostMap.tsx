"use client";
import { useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import Link from "next/link";
import { type Validator, type Probe, useApi, ago, utcWord } from "@/lib/api";
import type { Hosting } from "@/lib/hosting";
import { GRID, toGrid, countryPoint, landPath } from "@/lib/map/project";
import { verdictDef } from "@/components/Verdict";
import { type EndpointState, endpointState, readiness, ReadyAnswer, ReadyMissing } from "@/components/Readiness";

/**
 * The overview's host map: every registered Fibre host of the bonded set,
 * placed by the country its address resolves into (or by hosting.lat/lon
 * once the API sends them), on a dot grid of the world's land. Hosts close
 * together on screen share one badge; the ring around it is split by
 * endpoint state. Under the map, the newest check (or, once blobs exist, the
 * newest probe verdict) and the counts; beside it, the readiness answer.
 */

type Geo = Hosting & { lat?: number; lon?: number; city?: string };
type Host = { v: Validator; state: EndpointState; share: number; cc: string; gx: number; gy: number; provider: string };
type Cluster = { id: string; hosts: Host[]; gx: number; gy: number };

const STATE_WORD: Record<EndpointState, string> = { reachable: "reachable", flaky: "flaky", unreachable: "unreachable", none: "no host" };
const STATE_VAR: Record<EndpointState, string> = { reachable: "var(--accent)", flaky: "var(--hold)", unreachable: "var(--map-down)", none: "var(--pending)" };
const ORDER: EndpointState[] = ["reachable", "flaky", "unreachable"];

let regionNames: Intl.DisplayNames | null | undefined;
function countryName(cc: string): string {
  if (regionNames === undefined) {
    try { regionNames = new Intl.DisplayNames(["en"], { type: "region" }); } catch { regionNames = null; }
  }
  try { return regionNames?.of(cc) ?? cc; } catch { return cc; }
}

function providerOf(h?: Hosting): string {
  if (!h || h.status === "unresolved") return "";
  if (h.provider && h.provider !== "Other" && h.provider !== "Unknown") return h.provider;
  const org = (h.as_org ?? "").split(/\s+/)[0].replace(/^AS-/i, "").replace(/-ASN?(-\d+)?$/i, "").replace(/\d+$/, "");
  if (!org) return "";
  return org === org.toUpperCase() && org.length > 3 ? org.charAt(0) + org.slice(1).toLowerCase() : org;
}

const valLink = (v: Validator) => `/validator/?addr=${encodeURIComponent(v.cons_address || v.address)}`;
const name = (v: Validator) => v.moniker || v.operator_address || v.address;
const fmtShare = (s: number) => { const p = s * 100; return p >= 0.1 ? `${p.toFixed(1)}%` : p > 0 ? "<0.1%" : "0%"; };

/** badge diameter in px: a little larger for more hosts, so a crowd reads as one */
const badge = (n: number, narrow: boolean) => Math.round((narrow ? 22 : 24) + Math.min(12, 3.2 * Math.sqrt(n - 1)));

/** hosts -> clusters: one per country (or city), then merged while two badges would touch on screen */
function cluster(hosts: Host[], pxPerUnit: number, narrow: boolean): Cluster[] {
  const byKey = new Map<string, Host[]>();
  for (const h of hosts) {
    const k = `${h.gx.toFixed(1)}|${h.gy.toFixed(1)}`;
    byKey.set(k, [...(byKey.get(k) ?? []), h]);
  }
  let cs: Cluster[] = [...byKey.entries()].map(([k, hs]) => ({ id: k, hosts: hs, gx: hs[0].gx, gy: hs[0].gy }));
  for (;;) {
    let best: [number, number, number] | null = null;
    for (let i = 0; i < cs.length; i++) for (let j = i + 1; j < cs.length; j++) {
      const d = Math.hypot(cs[i].gx - cs[j].gx, cs[i].gy - cs[j].gy) * pxPerUnit;
      // Neighbours may overlap a little before they merge, so a dense region keeps some shape.
      const need = ((badge(cs[i].hosts.length, narrow) + badge(cs[j].hosts.length, narrow)) / 2) * 0.9;
      if (d < need && (!best || d - need < best[2])) best = [i, j, d - need];
    }
    if (!best) break;
    const a = cs[best[0]], b = cs[best[1]], na = a.hosts.length, nb = b.hosts.length;
    const merged: Cluster = { id: `${a.id}+${b.id}`, hosts: [...a.hosts, ...b.hosts], gx: (a.gx * na + b.gx * nb) / (na + nb), gy: (a.gy * na + b.gy * nb) / (na + nb) };
    cs = cs.filter((_, i) => i !== best![0] && i !== best![1]).concat(merged);
  }
  for (const c of cs) c.hosts.sort((x, y) => (y.v.voting_power || 0) - (x.v.voting_power || 0));
  // Stable ids for a cluster whatever order the merges ran in.
  for (const c of cs) c.id = c.hosts.map((h) => h.v.address).sort().join(",");
  return cs.sort((x, y) => x.gx - y.gx);
}

function ring(hosts: Host[]): string {
  const n = hosts.length;
  let at = 0;
  const stops: string[] = [];
  for (const s of ORDER) {
    const k = hosts.filter((h) => h.state === s).length;
    if (!k) continue;
    const from = (at / n) * 360, to = ((at + k) / n) * 360;
    stops.push(`${STATE_VAR[s]} ${from}deg ${to}deg`);
    at += k;
  }
  return `conic-gradient(${stops.join(", ")})`;
}

function placeLabel(c: Cluster): string {
  const ccs = [...new Set(c.hosts.map((h) => h.cc))];
  if (ccs.length <= 2) return ccs.map(countryName).join(", ");
  return `${ccs.length} countries`;
}

function reducedMotion(): boolean {
  try { return window.matchMedia("(prefers-reduced-motion: reduce)").matches; } catch { return false; }
}

export default function HostMap({ rows, showReadiness }: { rows: Validator[]; showReadiness: boolean }) {
  const r = readiness(rows);
  const total = r.total;

  const { hosts, unplaced } = useMemo(() => {
    const hs: Host[] = [];
    let un = 0;
    for (const v of r.registered) {
      const g = v.hosting as Geo | undefined;
      const cc = (g?.country ?? "").toUpperCase();
      let ll: [number, number] | null = null;
      if (g && typeof g.lat === "number" && typeof g.lon === "number") ll = [g.lon, g.lat];
      else ll = countryPoint(cc);
      if (!ll) { un++; continue; }
      const [gx, gy] = toGrid(ll[0], ll[1]);
      hs.push({ v, state: endpointState(v), share: total > 0 ? (v.voting_power || 0) / total : 0, cc, gx, gy, provider: providerOf(v.hosting) });
    }
    return { hosts: hs, unplaced: un };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [rows]);

  // The map's rendered width decides which badges would overlap; the box
  // itself has a fixed aspect ratio, so measuring it never shifts the layout.
  const box = useRef<HTMLDivElement>(null);
  const [width, setWidth] = useState(760);
  useLayoutEffect(() => {
    const el = box.current;
    if (!el) return;
    setWidth(el.clientWidth || 760);
    const ro = new ResizeObserver(() => setWidth(el.clientWidth || 760));
    ro.observe(el);
    return () => ro.disconnect();
  }, []);
  const narrow = width < 560;
  const pxPerUnit = width / GRID.cols;
  const height = pxPerUnit * GRID.rows;
  const clusters = useMemo(() => cluster(hosts, pxPerUnit, narrow), [hosts, pxPerUnit, narrow]);
  const land = useMemo(landPath, []);

  // ---- open cluster (hover, focus, tap) ----
  const [open, setOpen] = useState<string | null>(null);
  const closeT = useRef<number | undefined>(undefined);
  const openNow = (id: string) => { window.clearTimeout(closeT.current); setOpen(id); };
  const closeSoon = () => { window.clearTimeout(closeT.current); closeT.current = window.setTimeout(() => setOpen(null), 160); };
  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => { if (e.key === "Escape") setOpen(null); };
    const onDown = (e: PointerEvent) => { if (!(e.target as Element)?.closest?.(".fm-c")) setOpen(null); };
    document.addEventListener("keydown", onKey);
    document.addEventListener("pointerdown", onDown);
    return () => { document.removeEventListener("keydown", onKey); document.removeEventListener("pointerdown", onDown); };
  }, [open]);

  // ---- live pill: newest checks, or newest verdicts once blobs exist ----
  const blobs = rows.some((v) => (v.obligations?.total ?? 0) > 0);
  const probes = useApi<{ probes: Probe[] }>(blobs ? "/v1/probes?limit=12" : null, 30000);
  type Live = { v: Validator; host?: Host; word: string; color: string; at: string };
  const live: Live[] = useMemo(() => {
    const byAddr = new Map(hosts.map((h) => [h.v.address.toLowerCase(), h]));
    const ps = (probes.data?.probes ?? []).filter((p) => byAddr.has(p.validator_address.toLowerCase()));
    if (blobs && ps.length > 0) {
      return ps.slice(0, 5).map((p) => {
        const h = byAddr.get(p.validator_address.toLowerCase())!;
        const d = verdictDef(p.classification || p.outcome);
        return { v: h.v, host: h, word: d.label, color: d.tier === "fault" ? "var(--fault)" : d.tier === "kept" ? "var(--accent)" : d.tier === "hold" ? "var(--hold)" : "var(--map-down)", at: p.started_at };
      });
    }
    return hosts.filter((h) => h.v.last_seen_at)
      .sort((a, b) => Date.parse(b.v.last_seen_at!) - Date.parse(a.v.last_seen_at!))
      .slice(0, 5)
      .map((h) => ({ v: h.v, host: h, word: STATE_WORD[h.state], color: STATE_VAR[h.state], at: h.v.last_seen_at! }));
  }, [hosts, blobs, probes.data]);

  const [li, setLi] = useState(0);
  const [, tick] = useState(0);
  useEffect(() => {
    if (live.length < 2 || reducedMotion()) { const t = window.setInterval(() => tick((n) => n + 1), 30000); return () => window.clearInterval(t); }
    const t = window.setInterval(() => setLi((i) => i + 1), 5000);
    return () => window.clearInterval(t);
  }, [live.length]);
  const cur = live.length ? live[li % live.length] : null;
  const liveCluster = cur?.host ? clusters.find((c) => c.hosts.includes(cur.host!))?.id : undefined;

  if (total === 0) return null;
  if (hosts.length === 0 && r.registered.length === 0) {
    // Nothing to place yet: the readiness answer alone, as before.
    return showReadiness ? (
      <section className="band readiness" aria-labelledby="readiness-h">
        <div><ReadyAnswer rows={rows} /></div>
        <div><ReadyMissing rows={rows} /></div>
      </section>
    ) : null;
  }

  // ---- counts ----
  const countries = new Set(hosts.map((h) => h.cc)).size;
  const providers = new Set(r.registered.map((v) => providerOf(v.hosting)).filter(Boolean)).size;
  const readyPct = r.pct(r.reachPower), needPct = r.pct(r.quorum);

  return (
    <section className={`band hostmap${showReadiness ? "" : " solo"}`} aria-labelledby="hostmap-h">
      <div className="fm-main">
        <div className="fm-head">
          <h2 id="hostmap-h">Fibre hosts</h2>
          <p className="fm-pill fm-stats">
            <span><b>{hosts.length + unplaced}</b> hosts</span>
            <span><b>{countries}</b> countries</span>
            <span><b>{providers}</b> providers</span>
            <span>stake ready <b>{readyPct}</b> / {needPct}</span>
          </p>
        </div>
        <div className="fm-box" ref={box} style={{ aspectRatio: `${GRID.cols} / ${GRID.rows}` }}>
          <svg className="fm-land" viewBox={`0 0 ${GRID.cols} ${GRID.rows}`} preserveAspectRatio="xMidYMid meet" aria-hidden="true" focusable="false">
            <path d={land} />
          </svg>
          <ul className="fm-clusters" aria-label="Fibre hosts by location">
            {clusters.map((c) => {
              const x = c.gx * pxPerUnit, y = c.gy * pxPerUnit, d = badge(c.hosts.length, narrow);
              const isOpen = open === c.id;
              const fault = c.hosts.some((h) => (h.v.obligations?.broken ?? 0) > 0);
              const place = placeLabel(c);
              const counts = ORDER.map((s) => [s, c.hosts.filter((h) => h.state === s).length] as const).filter(([, n]) => n > 0);
              // The list opens beside the badge on a wide map and under the map on a narrow one.
              const popW = narrow ? width : 300;
              const pop: React.CSSProperties = narrow
                ? { left: -x, top: height - y + 8, width: popW }
                : { [x > width * 0.6 ? "right" : "left"]: d / 2 + 8, [y > height * 0.5 ? "bottom" : "top"]: -d / 2, width: popW };
              return (
                <li key={c.id} className={`fm-c${isOpen ? " open" : ""}${liveCluster === c.id ? " live" : ""}`}
                  style={{ left: `${(100 * c.gx) / GRID.cols}%`, top: `${(100 * c.gy) / GRID.rows}%`, zIndex: isOpen ? 30 : undefined }}
                  onMouseEnter={() => openNow(c.id)} onMouseLeave={closeSoon}>
                  <button type="button" className="fm-b" aria-expanded={isOpen}
                    aria-label={`${place}: ${c.hosts.length} host${c.hosts.length === 1 ? "" : "s"}, ${counts.map(([s, n]) => `${n} ${STATE_WORD[s]}`).join(", ")}`}
                    style={{ width: d, height: d, background: ring(c.hosts) }}
                    onFocus={() => openNow(c.id)} onClick={() => openNow(c.id)}>
                    <span>{c.hosts.length}</span>
                    {fault && <i className="fm-fault" aria-hidden="true" />}
                  </button>
                  {isOpen && (
                    <div className="fm-pop" style={pop} role="group" aria-label={place}>
                      <p className="fm-pop-h"><b>{place}</b><span>{c.hosts.length} host{c.hosts.length === 1 ? "" : "s"}</span></p>
                      <ul>
                        {c.hosts.map((h) => (
                          <li key={h.v.address}>
                            <i className="fm-dot" style={{ background: STATE_VAR[h.state] }} title={STATE_WORD[h.state]} />
                            <Link href={valLink(h.v)} className="fm-name">{name(h.v)}</Link>
                            <span className="fm-share">{fmtShare(h.share)}</span>
                            <span className="fm-meta">
                              {STATE_WORD[h.state]}
                              {(h.v.obligations?.broken ?? 0) > 0 && <span className="fm-broken"> · {h.v.obligations.broken} broken</span>}
                              {h.provider && <> · {h.provider}</>}
                              {c.hosts.some((o) => o.cc !== h.cc) && <> · {h.cc}</>}
                              {h.v.last_seen_at && <> · <span title={utcWord(h.v.last_seen_at)}>{ago(h.v.last_seen_at)}</span></>}
                            </span>
                          </li>
                        ))}
                      </ul>
                    </div>
                  )}
                </li>
              );
            })}
          </ul>
        </div>
        <div className="fm-pills">
          {cur && (
            <p className="fm-pill fm-live" key={`${cur.v.address}|${cur.at}`}>
              <i className="fm-dot" style={{ background: cur.color }} />
              <Link href={valLink(cur.v)}>{name(cur.v)}</Link>
              {cur.host?.cc && <span>{countryName(cur.host.cc)}</span>}
              {cur.host?.provider && <span>{cur.host.provider}</span>}
              <span>{cur.word}</span>
              <span className="fm-ago" title={utcWord(cur.at)}>{ago(cur.at)}</span>
            </p>
          )}
          <p className="fm-key" aria-hidden="true">
            <span><i style={{ background: STATE_VAR.reachable }} />reachable</span>
            <span><i style={{ background: STATE_VAR.flaky }} />flaky</span>
            <span><i style={{ background: STATE_VAR.unreachable }} />unreachable</span>
          </p>
        </div>
      </div>
      {showReadiness && (
        <div className="fm-side">
          <ReadyAnswer rows={rows} />
          <ReadyMissing rows={rows} />
        </div>
      )}
    </section>
  );
}
