import { type Rate, fmtRate, fmtCount, wilsonLower } from "@/lib/api";

// A rate is never shown without its denominator. With n < 30 the Wilson
// lower bound is printed so a 100% on three probes reads as what it is.
export default function RateCell({ r }: { r: Rate | null | undefined }) {
  if (!r || r.den === 0) return <span className="muted">— (0 probes)</span>;
  const lb = wilsonLower(r.num, r.den);
  return (
    <span title={lb !== null ? `95% lower bound ≥ ${(lb * 100).toFixed(1)}% (n=${r.den})` : undefined}>
      {fmtRate(r)} <span className="muted">({fmtCount(r)})</span>
      {r.den < 30 && lb !== null && <span className="faint"> ≥{(lb * 100).toFixed(0)}%</span>}
    </span>
  );
}
