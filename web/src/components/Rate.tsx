import { type Rate, fmtPct, fmtCount, faultRateUpper, enoughToRank, MIN_RATED } from "@/lib/api";

/**
 * A rate in a table cell: the percentage, the count under it, and a gauge.
 * Below MIN_RATED observations the gauge is not drawn and the row is not
 * ranked, so 3 of 3 does not read like 300 of 300; the figure itself keeps
 * the normal text colour. The tooltip carries the
 * Wilson 95% upper bound on the fault rate, around one observation per
 * (validator, blob) when that is available.
 */
export function boundTitle(r: Rate, obligations?: Rate | null): string | undefined {
  const perObligation = !!obligations && obligations.den > 0;
  const basis = perObligation ? obligations! : r;
  const ub = faultRateUpper(basis);
  if (ub === null) return undefined;
  const unit = perObligation ? "obligations" : "probes";
  return `At most ${(ub * 100).toFixed(1)}% of ${basis.den.toLocaleString("en-US")} ${unit} went unserved (95% confidence).`;
}

export default function RateCell({ r, obligations, gauge = "ok", sample, unreachable = 0 }: {
  r: Rate | null | undefined;
  obligations?: Rate | null;
  gauge?: "ok" | "hold";
  sample?: string;
  /** observations the rate does not speak for (obligations never seen
   *  served, probes that could not reach the validator); drawn as an amber
   *  share of the gauge so a rate over few decided observations does not look
   *  like a clean record */
  unreachable?: number;
}) {
  if (!r || r.den === 0) {
    if (unreachable > 0) return <span className="nil" title={`Nothing decided in this window: ${unreachable.toLocaleString("en-US")} observation${unreachable === 1 ? "" : "s"} the rate cannot speak for.`}>·</span>;
    return <span className="nil" title="no observation in this window">·</span>;
  }
  const rankable = enoughToRank(r);
  const total = r.den + unreachable;
  const pct = (n: number) => `${Math.max(n > 0 ? 1 : 0, (n / total) * 100)}%`;
  return (
    <span className="gauge-track" title={(boundTitle(r, obligations) ?? "") + (rankable ? "" : ` Fewer than ${MIN_RATED} observations.`)}>
      <span className="rate">
        <span className={rankable ? "v" : "v dim"}>{fmtPct(r)}</span>
        <span className="n">{sample ?? fmtCount(r)}</span>
      </span>
      {(rankable || unreachable > 0) && r.value !== null && (
        <span className="gauge-rail split" aria-hidden="true">
          <span className={"gauge " + gauge} style={{ width: pct(r.num) }} />
          {r.den - r.num > 0 && <span className="gauge fault" style={{ width: pct(r.den - r.num) }} />}
          {unreachable > 0 && <span className="gauge hold" style={{ width: pct(unreachable) }} />}
        </span>
      )}
    </span>
  );
}
