import { type Rate, fmtPct, fmtCount, faultRateUpper, enoughToRank, MIN_RATED } from "@/lib/api";

/**
 * A rate in a table cell: the percentage, the count under it, and a gauge.
 * Below MIN_RATED observations the percentage is dimmed and the gauge is not
 * drawn, so 3 of 3 does not read like 300 of 300. The tooltip carries the
 * Wilson 95% upper bound on the fault rate, around one observation per
 * (validator, blob) when that is available.
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
    <span className="gauge-track" title={(boundTitle(r, obligations) ?? "") + (rankable ? "" : ` Fewer than ${MIN_RATED} observations.`)}>
      <span className="rate">
        <span className={rankable ? "v" : "v dim"}>{fmtPct(r)}</span>
        <span className="n">{sample ?? fmtCount(r)}</span>
      </span>
      {rankable && r.value !== null && (
        <span className="gauge-rail" aria-hidden="true">
          <span className={"gauge " + gauge} style={{ width: `${Math.max(1, r.value * 100)}%` }} />
        </span>
      )}
    </span>
  );
}
