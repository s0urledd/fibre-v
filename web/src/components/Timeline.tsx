import Link from "next/link";
import { type Probe, utc } from "@/lib/api";
import { verdictDef, tierColor } from "./Verdict";

// Probe timeline (R9 section 6.5): x = time from settlement through
// must_serve_until and the grace boundary; one row per assigned validator;
// one cell per probe, filled by verdict, glyph inside. NOT_PROBED cells are
// dashed outlines. No animation.
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
  const t1 = last + (last - t0) * 0.05;
  const W = 900, L = 150, R = 20, rowH = 22, top = 40;
  const H = top + validators.length * rowH + 30;
  const x = (t: number) => L + ((t - t0) / Math.max(1, t1 - t0)) * (W - L - R);
  const byVal = new Map<string, Probe[]>();
  for (const p of probes) {
    if (!byVal.has(p.validator_address)) byVal.set(p.validator_address, []);
    byVal.get(p.validator_address)!.push(p);
  }
  return (
    <div className="timeline">
      <svg viewBox={`0 0 ${W} ${H}`} width="100%" height={H} preserveAspectRatio="xMinYMid meet" role="img" aria-label="probe timeline">
        <rect x={x(t0)} y={top - 4} width={x(tMsu) - x(t0)} height={validators.length * rowH + 8} fill="var(--paper-2)" />
        {(() => {
          const marks: [number, string][] = [[t0, "settled"], [tMsu, "must serve until"], [tGrace, "grace end"]];
          let lastX = -1e9, tier = 0;
          return marks.map(([t, label]) => {
            const cx = x(t);
            // ~5.5px per character at 10px: enough to know when two collide.
            tier = cx - lastX < label.length * 5.5 ? 1 - tier : 0;
            lastX = cx;
            return (
              <g key={label}>
                <line x1={cx} x2={cx} y1={top - 12} y2={top + validators.length * rowH + 4} stroke="var(--rule)" strokeDasharray="3 3" />
                <text x={cx + 3} y={top - 14 - tier * 11} fontSize="10" fill="var(--text-2)">{label}</text>
              </g>
            );
          });
        })()}
        {validators.map((v, i) => {
          const y = top + i * rowH;
          const ps = byVal.get(v.address) ?? [];
          return (
            <g key={v.address}>
              <text x={4} y={y + 14} fontSize="11" fontFamily="ui-monospace, Menlo, monospace" fill="var(--text)">
                <title>{v.address}</title>
                <a href={`/validator/?addr=${v.address}`}>{label(v)}</a>
                <tspan fill="var(--text-2)"> {v.row_count}r</tspan>
              </text>
              <line x1={L} x2={W - R} y1={y + 10} y2={y + 10} stroke="var(--rule)" />
              {ps.map((p) => {
                const d = verdictDef(p.classification);
                const fill = tierColor(d.tier);
                const cx = x(new Date(p.started_at).getTime());
                const gap = d.tier === "gap";
                return (
                  <g key={p.scheduled_at}>
                    <title>{`${p.classification} · ${p.schedule_label} · ${utc(p.started_at)} · ${p.outcome} · ${p.rows_returned}/${p.rows_expected} rows · ${p.total_duration_ms} ms${p.raw_error ? " · " + p.raw_error : ""}`}</title>
                    <rect x={cx - 7} y={y + 2} width={14} height={16} rx={1}
                      fill={gap ? "transparent" : "var(--paper)"} stroke={gap ? "var(--edge)" : fill}
                      strokeDasharray={gap ? "2 2" : undefined} strokeWidth={1} />
                    <g transform={`translate(${cx - 5} ${y + 5})`} fill="none" stroke={fill} strokeWidth={1.25}>
                      {d.tier === "fault" && <path d="M5 1 L9.2 8.6 H0.8 Z" fill={fill} stroke="none" />}
                      {d.tier === "kept" && <circle cx={5} cy={5} r={3.2} fill={fill} stroke="none" />}
                      {d.tier === "hold" && <><circle cx={5} cy={5} r={3.2} /><path d="M5 1.8 A3.2 3.2 0 0 1 5 8.2 Z" fill={fill} stroke="none" /></>}
                      {d.tier === "held" && <circle cx={5} cy={5} r={3.2} />}
                      {d.tier === "gap" && <path d="M1.8 5 H8.2" strokeLinecap="round" />}
                    </g>
                  </g>
                );
              })}
            </g>
          );
        })}
        <text x={L} y={H - 8} fontSize="10" fill="var(--text-2)">{utc(settled)}</text>
        <text x={W - R} y={H - 8} fontSize="10" fill="var(--text-2)" textAnchor="end">{utc(new Date(t1).toISOString())}</text>
      </svg>
      <p className="faint">Rows: assigned validators, ordered by voting power. Hover a cell for the probe's outcome, rows and error. <Link href="/methodology/#verdicts">Verdict definitions</Link>.</p>
    </div>
  );
}

// label names the row the way the table below it does: the validator's own
// moniker when the chain gives us one, the address otherwise. The label column
// is 150px of monospace, so a long moniker is cut; the full address is in the
// <title> either way, so nothing here is the only place a row is identified.
function label(v: { address: string; moniker?: string }) {
  const m = (v.moniker ?? "").trim();
  if (!m) return v.address.slice(0, 10) + "\u2026";
  return m.length > 14 ? m.slice(0, 13) + "\u2026" : m;
}
