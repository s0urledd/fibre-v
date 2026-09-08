import Link from "next/link";
import { type Probe, utc } from "@/lib/api";
import { badgeDef } from "./Badge";

// Probe timeline (R9 section 6.5): x = time from settlement through
// must_serve_until and the grace boundary; one row per assigned validator;
// one cell per probe, filled by verdict, glyph inside. NOT_PROBED cells are
// dashed outlines. No animation.
export default function Timeline({ probes, validators, settled, mustServeUntil, graceEnd }: {
  probes: Probe[];
  validators: { address: string; row_count: number }[];
  settled: string;
  mustServeUntil: string;
  graceEnd: string;
}) {
  const t0 = new Date(settled).getTime();
  const tMsu = new Date(mustServeUntil).getTime();
  const tGrace = new Date(graceEnd).getTime();
  const last = Math.max(tGrace, ...probes.map((p) => new Date(p.started_at).getTime()));
  const t1 = last + (last - t0) * 0.05;
  const W = 900, L = 150, R = 20, rowH = 22, top = 28;
  const H = top + validators.length * rowH + 30;
  const x = (t: number) => L + ((t - t0) / Math.max(1, t1 - t0)) * (W - L - R);
  const byVal = new Map<string, Probe[]>();
  for (const p of probes) {
    if (!byVal.has(p.validator_address)) byVal.set(p.validator_address, []);
    byVal.get(p.validator_address)!.push(p);
  }
  return (
    <div className="timeline">
      <svg width={W} height={H} role="img" aria-label="probe timeline">
        <rect x={x(t0)} y={top - 4} width={x(tMsu) - x(t0)} height={validators.length * rowH + 8} fill="var(--surface-2)" />
        {[[t0, "settled"], [tMsu, "must serve until"], [tGrace, "grace end"]].map(([t, label]) => (
          <g key={label as string}>
            <line x1={x(t as number)} x2={x(t as number)} y1={top - 12} y2={top + validators.length * rowH + 4} stroke="var(--border)" strokeDasharray="3 3" />
            <text x={x(t as number) + 3} y={top - 14} fontSize="10" fill="var(--text-2)">{label as string}</text>
          </g>
        ))}
        {validators.map((v, i) => {
          const y = top + i * rowH;
          const ps = byVal.get(v.address) ?? [];
          return (
            <g key={v.address}>
              <text x={4} y={y + 14} fontSize="11" fontFamily="ui-monospace, Menlo, monospace" fill="var(--text)">
                <a href={`/validator/?addr=${v.address}`}>{v.address.slice(0, 10)}…</a>
                <tspan fill="var(--text-2)"> {v.row_count}r</tspan>
              </text>
              <line x1={L} x2={W - R} y1={y + 10} y2={y + 10} stroke="var(--border)" />
              {ps.map((p) => {
                const b = badgeDef(p.classification);
                const cx = x(new Date(p.started_at).getTime());
                const gap = p.classification === "NOT_PROBED" || p.classification === "PROBE_ERROR";
                return (
                  <g key={p.scheduled_at}>
                    <title>{`${p.classification} · ${p.schedule_label} · ${utc(p.started_at)} · ${p.outcome} · ${p.rows_returned}/${p.rows_expected} rows · ${p.total_duration_ms} ms${p.raw_error ? " · " + p.raw_error : ""}`}</title>
                    <rect x={cx - 7} y={y + 2} width={14} height={16} rx={2}
                      fill={gap ? "transparent" : b.token} stroke={gap ? "var(--border)" : "var(--text)"} strokeDasharray={gap ? "2 2" : undefined} strokeWidth={0.5} />
                    <text x={cx} y={y + 14} fontSize="10" textAnchor="middle" fill={gap ? "var(--text-2)" : "var(--bg)"}>{b.glyph}</text>
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
