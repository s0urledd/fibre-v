import { type Obligations, int, undecided } from "@/lib/api";

/**
 * Every obligation in the period as one bar: served, broken, undecided,
 * pending — the buckets sum to the total, so the bar is the whole population
 * and the kept rate's denominator (served + broken) is visible as the first
 * two segments.
 */
export default function OutcomeBar({ o }: { o: Obligations | null | undefined }) {
  const total = o?.total ?? 0;
  const und = undecided(o);
  const segs: [string, string, number][] = [["s", "Served", o?.served ?? 0], ["b", "Broken", o?.broken ?? 0], ["u", "Undecided", und], ["p", "Pending", o?.pending ?? 0]];
  return (
    <>
      <div className="bar" role="img" aria-label={total ? segs.map(([, l, n]) => `${l} ${int(n)}`).join(", ") : "no obligation in this period"}>
        {total > 0 && segs.map(([c, l, n]) => n > 0 && <i key={c} className={c} style={{ width: `${(n / total * 100).toFixed(2)}%` }} title={`${l} ${int(n)}`} />)}
      </div>
      <div className="key">
        {segs.map(([c, l, n]) => <div key={c}><span className={"sw " + c} />{l}<span className="v">{int(n)}</span></div>)}
      </div>
    </>
  );
}
