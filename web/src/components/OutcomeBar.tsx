import { type Obligations, int, leftOut } from "@/lib/api";

/**
 * Every obligation in the period as one bar: served, broken, pending, and
 * what the rate leaves out after the window closed — sampled out by the
 * probe budget, and (a line under the key, only when there is any) not
 * observed at the end of the window. The buckets sum
 * to the total, so the bar is the whole population and the rate's
 * denominator (served + broken) is visible as the first two segments.
 */
export default function OutcomeBar({ o, absent }: { o: Obligations | null | undefined; /** nothing to measure yet: dashes, not zeros */ absent?: boolean }) {
  const total = absent ? 0 : o?.total ?? 0;
  const { sampled, notObserved } = leftOut(o);
  const segs: [string, string, number, string][] = [
    ["s", "Served", o?.served ?? 0, "Kept: a probe near the end of the window returned the shard and none faulted."],
    ["b", "Broken", o?.broken ?? 0, "A shard the validator endorsed was missing or did not verify while the obligation held."],
    ["p", "Pending", o?.pending ?? 0, "The retention window has not ended yet: no verdict yet."],
    ["u", "Sampled out", sampled, "Not probed: the probe budget drew the blob out of its sample (committed in advance, checkable). Never counted either way."],
  ];
  return (
    <>
      <div className="bar" role="img" aria-label={total ? segs.map(([, l, n]) => `${l} ${int(n)}`).join(", ") : "no obligation in this period"}>
        {total > 0 && segs.map(([c, l, n]) => n > 0 && <i key={c} className={c} style={{ width: `${(n / total * 100).toFixed(2)}%` }} title={`${l} ${int(n)}`} />)}
        {total > 0 && notObserved > 0 && <i className="n" style={{ width: `${(notObserved / total * 100).toFixed(2)}%` }} title={`Not observed ${int(notObserved)}`} />}
      </div>
      <div className="key okey">
        {segs.map(([c, l, n, t]) => <div key={c} title={t}><span className={"sw " + c} />{l}<span className="v">{absent ? "—" : int(n)}</span></div>)}
      </div>
      {!absent && notObserved > 0 && <p className="okey-note" title="The window closed without a reading near its end. Never counted either way.">and {int(notObserved)} not observed at the end of its window</p>}
    </>
  );
}
