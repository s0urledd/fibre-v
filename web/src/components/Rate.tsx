import { type Rate, fmtRate, fmtCount, faultRateUpper, enoughToRank, MIN_RATED } from "@/lib/api";

/**
 * A rate is never shown without its denominator, and never as a percentage
 * below MIN_RATED observations — the counts are shown instead.
 *
 * `obligations`, when given, is the same rate counted one observation per
 * (validator, blob) rather than one per probe. The interval is drawn around
 * that, because the in-window probes of one obligation are near copies of each
 * other and a bound drawn around the probe count claims precision the evidence
 * does not carry. The bound is on the FAULT rate: this site publishes
 * accusations, and an accusation has to be stated in the direction it accuses.
 */

function boundTitle(r: Rate, obligations?: Rate | null): string | undefined {
  const basis = obligations && obligations.den > 0 ? obligations : r;
  const ub = faultRateUpper(basis);
  if (ub === null) return undefined;
  const unit = basis === r ? "probes" : "obligations";
  return `at most ${(ub * 100).toFixed(1)}% of ${basis.den.toLocaleString("en-US")} ${unit} went unserved, 95% confidence`;
}

/**
 * The reading: the one element on a page permitted the largest size. Three
 * designed states rather than a value and two fallbacks, because an instrument
 * below its resolution shows the raw count — it does not invent a percentage.
 *
 *   no observations        —, in the dimmest ink, with its zero sample beside it
 *   below the floor        the counts at full size, plus the words UNDER FLOOR
 *   at or above the floor  the percentage
 *
 * `loading` renders a skeleton at exactly the width the number will occupy, so
 * the largest object on the page does not pop into existence on every load.
 */
export function Reading({ r, obligations, loading }: {
  r: Rate | null | undefined;
  obligations?: Rate | null;
  loading?: boolean;
}) {
  if (loading) {
    return (
      <p className="reading mono" aria-busy="true">
        <span className="skel skel--reading" aria-hidden="true">00.0%</span>
        <span className="sr-only">loading</span>
      </p>
    );
  }
  if (!r || r.den === 0) {
    return <p className="reading mono absent" title="no probe of an assigned shard has produced a verdict in this window">—</p>;
  }
  if (r.den < MIN_RATED) {
    return (
      <p className="reading mono" title={`fewer than ${MIN_RATED} rated observations: the counts, not a percentage`}>
        {fmtCount(r)}
        <span className="label state">under floor</span>
      </p>
    );
  }
  return <p className="reading mono" title={boundTitle(r, obligations)}>{fmtRate(r)}</p>;
}

/**
 * The rate in a table row: figure and sample as one object, with a hairline
 * gauge beneath. The gauge is drawn only above the ranking floor, because below
 * it there is no rate to gauge — only counts.
 */
export default function RateCell({ r, obligations }: { r: Rate | null | undefined; obligations?: Rate | null }) {
  if (!r || r.den === 0) return <span className="nil" title="no rated observation in this window">·</span>;
  const rankable = enoughToRank(r);
  return (
    <span className="gauge-track" title={boundTitle(r, obligations)}>
      <span className="rate">
        <span className="v">{fmtRate(r)}</span>
        <span className="n">{rankable ? fmtCount(r) : "under floor"}</span>
      </span>
      {rankable && r.value !== null && (
        <span className="gauge-rail" aria-hidden="true">
          <span className="gauge" style={{ width: `${Math.max(1, r.value * 100)}%` }} />
        </span>
      )}
    </span>
  );
}
