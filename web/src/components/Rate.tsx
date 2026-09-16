import { type Rate, fmtRate, fmtCount, faultRateUpper, MIN_RATED } from "@/lib/api";

/**
 * A rate is never shown without its denominator, and never as a percentage
 * below MIN_RATED observations — fmtRate prints the counts instead.
 *
 * `obligations`, when given, is the same rate counted one observation per
 * (validator, blob) rather than one per probe. The interval is drawn around
 * that, because the four in-window probes of one obligation are near copies
 * of each other and a bound drawn around the probe count claims precision the
 * evidence does not carry. The bound shown is the upper bound on the FAULT
 * rate: this site publishes accusations, and an accusation has to be stated
 * in the direction it accuses.
 */
export default function RateCell({ r, obligations }: { r: Rate | null | undefined; obligations?: Rate | null }) {
  if (!r || r.den === 0) return <span className="muted">— (0 probes)</span>;
  const basis = obligations && obligations.den > 0 ? obligations : r;
  const ub = faultRateUpper(basis);
  const unit = basis === r ? "probes" : "obligations";
  const title = ub !== null
    ? `at most ${(ub * 100).toFixed(1)}% of ${basis.den} ${unit} went unserved, 95% confidence`
    : undefined;
  return (
    <span title={title}>
      {fmtRate(r)} <span className="muted">({fmtCount(r)})</span>
      {r.den < MIN_RATED && <span className="faint" title={`fewer than ${MIN_RATED} rated probes`}> (few)</span>}
      {ub !== null && r.den >= MIN_RATED && basis.num < basis.den && (
        <span className="faint"> ≤{(ub * 100).toFixed(0)}% bad</span>
      )}
    </span>
  );
}
