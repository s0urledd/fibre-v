import Link from "next/link";
import { type Probe, utc } from "@/lib/api";
import { verdictDef, Mark } from "./Verdict";

/**
 * One row per assigned validator, one dot per probe, placed by the time it
 * ran between settlement and the end of the grace period. Colour and shape
 * follow the verdict marks used everywhere else on the site.
 */
export default function Timeline({ probes, validators, settled, mustServeUntil, graceEnd }: {
  probes: Probe[];
  validators: { address: string; row_count: number; moniker?: string }[];
  settled: string;
  mustServeUntil: string;
  graceEnd: string;
}) {
  const t0 = new Date(settled).getTime();
  const tMsu = new Date(mustServeUntil).getTime();
  const tGrace = new Date(graceEnd).getTime();
  const last = Math.max(tGrace, ...probes.map((p) => new Date(p.started_at).getTime()));
  const t1 = last + (last - t0) * 0.04;
  const W = 900, L = 176, R = 24, rowH = 20, top = 44;
  const H = top + validators.length * rowH + 26;
  const x = (t: number) => L + ((t - t0) / Math.max(1, t1 - t0)) * (W - L - R);
  const byVal = new Map<string, Probe[]>();
  for (const p of probes) {
    if (!byVal.has(p.validator_address)) byVal.set(p.validator_address, []);
    byVal.get(p.validator_address)!.push(p);
  }
  const marks: [number, string][] = [[t0, "settled"], [tMsu, "must serve until"], [tGrace, "grace end"]];
  const bottom = top + validators.length * rowH;
  return (
    <div className="timeline">
      <svg viewBox={`0 0 ${W} ${H}`} width="100%" height={H} preserveAspectRatio="xMinYMid meet" role="img" aria-label="probe timeline">
        <rect x={x(t0)} y={top - 8} width={x(tMsu) - x(t0)} height={validators.length * rowH + 12} fill="var(--wash)" rx={3} />
        {(() => {
          // Two marks closer than a label's width share the top edge, so the
          // second one moves up a line instead of printing over the first.
          let lastX = -1e9, lifted = false;
          return marks.map(([t, label], i) => {
            const cx = x(t);
            lifted = cx - lastX < label.length * 6 ? !lifted : false;
            lastX = cx;
            return (
              <g key={label}>
                <line x1={cx} x2={cx} y1={top - 8} y2={bottom + 4} stroke="var(--line-2)" strokeDasharray="2 3" />
                <text x={cx} y={top - 14 - (lifted ? 12 : 0)} fontSize="10" fill="var(--text-3)" textAnchor={i === 0 ? "start" : "middle"}>{label}</text>
              </g>
            );
          });
        })()}
        {validators.map((v, i) => {
          const y = top + i * rowH + rowH / 2;
          const ps = byVal.get(v.address) ?? [];
          return (
            <g key={v.address}>
              <text x={4} y={y + 4} fontSize="11" fontFamily="ui-monospace, Menlo, monospace" fill="var(--text-2)">
                <title>{v.address}</title>
                <a href={`/validator/?addr=${v.address}`}>{label(v)}</a>
                <tspan fill="var(--text-3)"> {v.row_count}</tspan>
              </text>
              <line x1={L} x2={W - R} y1={y} y2={y} stroke="var(--line)" />
              {ps.map((p) => {
                const d = verdictDef(p.classification);
                const cx = x(new Date(p.started_at).getTime());
                const t = `${d.label} · ${p.schedule_label} · ${utc(p.started_at)} · ${p.outcome} · ${p.rows_returned}/${p.rows_expected} rows · ${p.total_duration_ms} ms${p.raw_error ? " · " + p.raw_error : ""}`;
                return (
                  <g key={p.scheduled_at} transform={`translate(${cx} ${y})`}>
                    <title>{t}</title>
                    <circle r={7} fill="var(--card)" />
                    {d.tier === "kept" && <circle r={4} fill="var(--ok)" />}
                    {d.tier === "fault" && <path d="M0 -4.6 L4.4 3.4 H-4.4 Z" fill="var(--fault)" />}
                    {d.tier === "hold" && <circle r={4} fill="var(--hold)" />}
                    {d.tier === "held" && <circle r={3.6} fill="none" stroke="var(--text-3)" strokeWidth={1.3} />}
                    {d.tier === "gap" && <path d="M-3.5 0 H3.5" stroke="var(--text-3)" strokeWidth={1.3} strokeLinecap="round" />}
                  </g>
                );
              })}
            </g>
          );
        })}
        <text x={L} y={H - 6} fontSize="10" fill="var(--text-3)">{utc(settled)}</text>
        <text x={W - R} y={H - 6} fontSize="10" fill="var(--text-3)" textAnchor="end">{utc(new Date(t1).toISOString())}</text>
      </svg>
      <ul className="tl-key">
        <li><Mark tier="kept" /> served</li>
        <li><Mark tier="held" /> unproven</li>
        <li><Mark tier="hold" /> unreachable</li>
        <li><Mark tier="fault" /> fault</li>
        <li><Mark tier="gap" /> not probed</li>
        <li className="faint">rows assigned after each name · <Link href="/methodology/#verdicts">definitions</Link></li>
      </ul>
    </div>
  );
}

function label(v: { address: string; moniker?: string }) {
  const m = (v.moniker ?? "").trim();
  if (!m) return v.address.slice(0, 10) + "…";
  return m.length > 16 ? m.slice(0, 15) + "…" : m;
}
