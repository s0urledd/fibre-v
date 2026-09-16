import { type Rate, fmtRate, fmtCount, faultRateUpper, enoughToRank, MIN_RATED } from "@/lib/api";

/**
 * A rate in a table cell: the figure, the sample under it, and a gauge. Below
 * MIN_RATED observations the counts are printed instead of a percentage and no
 * gauge is drawn. The tooltip carries the Wilson 95% upper bound on the fault
 * rate, around one observation per (validator, blob) when that is available.
 */
export function boundTitle(r: Rate, obligations?: Rate | null): string | undefined {
  const basis = obligations && obligations.den > 0 ? obligations : r;
  const ub = faultRateUpper(basis);
  if (ub === null) return undefined;
  const unit = basis === r ? "probes" : "obligations";
  return `At most ${(ub * 100).toFixed(1)}% of ${basis.den.toLocaleString("en-US")} ${unit} went unserved (95% confidence).`;
}

export default function RateCell({ r, obligations, gauge = "ok", sample }: {
  r: Rate | null | undefined;
  obligations?: Rate | null;
  gauge?: "ok" | "hold";
  sample?: string;
}) {
  if (!r || r.den === 0) return <span className="nil" title="no observation in this window">·</span>;
  const rankable = enoughToRank(r);
  return (
    <span className="gauge-track" title={boundTitle(r, obligations) ?? `${fmtCount(r)}; fewer than ${MIN_RATED} observations, so no percentage`}>
      <span className="rate">
        <span className="v">{fmtRate(r)}</span>
        <span className="n">{rankable ? (sample ?? fmtCount(r)) : "under floor"}</span>
      </span>
      {rankable && r.value !== null && (
        <span className="gauge-rail" aria-hidden="true">
          <span className={"gauge " + gauge} style={{ width: `${Math.max(1, r.value * 100)}%` }} />
        </span>
      )}
    </span>
  );
}
