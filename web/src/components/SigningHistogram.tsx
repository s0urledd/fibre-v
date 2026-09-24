import { type SigningDistribution } from "@/lib/signing";
import { int, pctOf } from "@/lib/api";

/**
 * How much voting power each settled promise in the period collected, as
 * verified here: one horizontal bar per share bucket, with the chain's
 * two-thirds quorum drawn as a rule between the bucket below it and the ones
 * above. Horizontal so the labels stay readable at phone width.
 *
 * Nothing here is drawn in the fault colour. Mass just above two thirds is
 * the publisher doing what it is built to do (stop collecting at the quorum),
 * and a promise below the line is one this observer could not match to the
 * quorum from its own reading of the validator set, which is a statement
 * about the observer's view rather than about any validator.
 */
export default function SigningHistogram({ data }: { data: SigningDistribution | null }) {
  if (!data) return <p className="vchart-empty">Loading…</p>;
  if (data.promises === 0) {
    return <p className="vchart-empty">{data.unknown > 0 ? `${int(data.unknown)} settled promise${data.unknown === 1 ? "" : "s"} in this period, all recorded before signatures were verified.` : "No settled promise in this period."}</p>;
  }
  const max = Math.max(1, ...data.buckets.map((b) => b.count));
  return (
    <div className="sighist">
      <ul role="list" aria-label="settled promises by the share of voting power whose signature verified">
        {data.buckets.map((b, i) => {
          const title = `${b.label} of total voting power signed: ${int(b.count)} of ${int(data.promises)} promises (${pctOf(b.count, data.promises)})${b.above_threshold ? "" : ". Below the quorum as this observer verified the signatures; not a validator fault"}`;
          const quorum = i > 0 && !data.buckets[i - 1].above_threshold && b.above_threshold;
          return (
            <li key={b.key} className={(b.above_threshold ? "above" : "below") + (quorum ? " quorum" : "")} title={title}>
              <span className="l">{b.label}</span>
              <span className="bar"><i style={{ width: b.count ? `max(2px, ${(b.count / max * 100).toFixed(1)}%)` : "0" }} /></span>
              <span className="n">{int(b.count)}</span>
            </li>
          );
        })}
      </ul>
      <p className="sub">
        {int(data.meets_threshold.num)} / {int(data.promises)} at or above the ⅔ quorum
        {data.signers_median != null && <> · median {int(data.signers_median)} signer{data.signers_median === 1 ? "" : "s"} per promise</>}
        {data.unknown > 0 && <> · {int(data.unknown)} recorded before verification, not shown</>}
      </p>
    </div>
  );
}
