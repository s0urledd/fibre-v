import { type Rate, fmtRate, fmtCount, enoughToRank } from "@/lib/api";

/**
 * The retention window, drawn as a graduated scale.
 *
 * This is the one figure on the site whose x-axis is Fibre's own mechanism
 * rather than a category. The prober visits each obligation several times
 * inside its retention window, and the points are deliberately not evenly
 * spaced: the default schedule clusters toward the deadline, because that is
 * where a retention breach shows. So a rate that is sound at the first tick and
 * poor at the last means shards pruned before the deadline, while one that is
 * poor throughout means something else entirely. The pooled serve rate cannot
 * tell those apart, and a four-category bar chart would not either.
 *
 * The axis is built from the data. Neither the number of points nor their
 * positions is fixed: `InWindowFractions` is configurable, `InWindowFractions(n)`
 * accepts any n, and `MinSpacing` drops points that would land too close
 * together on a short window. The API sends labels and rates but not positions,
 * so true positions are used only when the labels are exactly the default set
 * this repository ships; otherwise the ticks are evenly spaced and the caption
 * says they are ordered rather than placed.
 */

// fibre-sentinel/internal/probe/schedule.go: DefaultInWindowFractions.
const DEFAULT_KEYS = ["w1", "w2", "w3", "w4"];
const DEFAULT_FRACTIONS = [0.12, 0.45, 0.72, 0.92];

export default function Graduation({ points }: { points: { key: string; serve_rate: Rate }[] }) {
  const live = points.filter((p) => p.serve_rate.den > 0);
  if (live.length === 0) return null;

  const isDefault = points.length === DEFAULT_KEYS.length
    && points.every((p, i) => p.key === DEFAULT_KEYS[i]);
  const at = (i: number) =>
    isDefault ? DEFAULT_FRACTIONS[i] : points.length === 1 ? 0.5 : i / (points.length - 1);

  const W = 720, H = 92, L = 8, R = 56, BASE = 56;
  const x = (f: number) => L + f * (W - L - R);

  return (
    <section className="grad">
      <span className="label">Through the retention window</span>
      <div className="grad-scroll">
      <svg viewBox={`0 0 ${W} ${H}`} className="grad-svg" role="img"
        aria-label={`Serve rate at each schedule point: ${live.map((p) => `${p.key} ${fmtRate(p.serve_rate)}`).join(", ")}`}>
        {/* the obligation's own lifetime, settled on the left, deadline on the right */}
        <line x1={L} x2={W - R} y1={BASE} y2={BASE} stroke="var(--edge)" strokeWidth="1" />
        <text x={L} y={H - 6} fontSize="10" fill="var(--text-3)">settled</text>
        <text x={W - R} y={H - 6} fontSize="10" fill="var(--text-3)" textAnchor="end">must serve until</text>
        <line x1={W - R} x2={W - R} y1={BASE - 8} y2={BASE + 4} stroke="var(--text-3)" strokeWidth="1" />

        {points.map((p, i) => {
          const cx = x(at(i));
          const r = p.serve_rate;
          if (r.den === 0) {
            // A point with no observations is a gap in the schedule, not a zero.
            return (
              <g key={p.key}>
                <line x1={cx} x2={cx} y1={BASE - 5} y2={BASE} stroke="var(--text-3)" strokeWidth="1" strokeDasharray="1 2" />
                <text x={cx} y={BASE + 16} fontSize="10" fill="var(--text-3)" textAnchor="middle">{p.key}</text>
              </g>
            );
          }
          // The fault share hangs below the rule, so the eye reads the shortfall
          // rather than having to subtract it from the rate above.
          const bad = r.value === null ? 0 : 1 - r.value;
          const drop = Math.max(bad > 0 ? 2 : 0, bad * 22);
          return (
            <g key={p.key}>
              <title>{`${p.key}: ${fmtRate(r)} of ${fmtCount(r)} probes`}</title>
              <line x1={cx} x2={cx} y1={BASE - 8} y2={BASE} stroke="var(--edge)" strokeWidth="1" />
              <text x={cx} y={BASE - 14} fontSize="13" fill="var(--text)" textAnchor="middle"
                fontFamily="var(--mono)" fontWeight={enoughToRank(r) ? 400 : 300}>
                {fmtRate(r)}
              </text>
              {drop > 0 && (
                <line x1={cx} x2={cx} y1={BASE} y2={BASE + drop} stroke="var(--fault)" strokeWidth="2" />
              )}
              <text x={cx} y={BASE + 16 + drop} fontSize="10" fill="var(--text-3)" textAnchor="middle">{p.key}</text>
            </g>
          );
        })}
        <text x={W - R + 6} y={BASE - 14} fontSize="10" fill="var(--text-3)">100%</text>
      </svg>
      </div>
      <p className="sample">
        {isDefault
          ? "Ticks at 12%, 45%, 72% and 92% of each retention window, so they are not interchangeable."
          : `${points.length} schedule points, drawn in order: this chain's probe schedule is not the default set, and the API does not publish the positions.`}
        {" "}A rate that is sound early and poor late means shards pruned before the deadline. The red hairline under each tick is the shortfall.
      </p>
    </section>
  );
}
