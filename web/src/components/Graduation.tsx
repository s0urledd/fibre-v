import { type Rate, fmtPct, fmtCount } from "@/lib/api";
import Info from "./Info";

/**
 * Serve rate at each probe point inside the retention window. The points are
 * not evenly spaced (12%, 45%, 72%, 92% of the window by default), so a rate
 * that holds early and drops late means shards were pruned before the
 * deadline. Four cells, one per point; a point with no rated probe shows a
 * dash.
 */
const DEFAULT_KEYS = ["w1", "w2", "w3", "w4"];
const DEFAULT_AT = ["12%", "45%", "72%", "92%"];
const NTH = ["1st", "2nd", "3rd", "4th"];

export default function Graduation({ points }: { points: { key: string; serve_rate: Rate }[] }) {
  if (!points.some((p) => p.serve_rate.den > 0)) return null;
  const isDefault = points.length === DEFAULT_KEYS.length && points.every((p, i) => p.key === DEFAULT_KEYS[i]);
  return (
    <section className="grad">
      <span className="label">Through the retention window<Info label="Through the retention window">
        <p>Each blob a validator signed for is probed four times before its retention window ends{isDefault ? ", at 12%, 45%, 72% and 92% of the window" : ""}. This is the serve rate at each of those probes.</p>
        <p>A rate that holds early and drops late means shards were pruned before the deadline.</p>
      </Info></span>
      <div className="grad-grid">
        {points.map((p, i) => {
          const r = p.serve_rate;
          const absent = r.den === 0;
          return (
            <div key={p.key} className="grad-cell">
              <span className="k">{NTH[i] ?? `${i + 1}th`} probe{isDefault && <span className="at"> · at {DEFAULT_AT[i]} of the window</span>}</span>
              <span className={absent ? "v absent" : "v"}>{absent ? "—" : fmtPct(r)}</span>
              <span className="n">{absent ? "no rated probe" : fmtCount(r)}</span>
              {!absent && r.value !== null && (
                <span className="gauge-rail" aria-hidden="true"><span className="gauge" style={{ width: `${Math.max(1, r.value * 100)}%` }} /></span>
              )}
            </div>
          );
        })}
      </div>
    </section>
  );
}
