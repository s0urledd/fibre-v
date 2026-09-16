/**
 * The data square.
 *
 * Celestia's own shape, holding Fibre's own numbers. A blob is erasure-coded
 * into `total_rows` coded rows of which `needed_rows` are enough to rebuild it —
 * 16,384 and 4,096 for blob version 0, a quarter. Every other figure on this
 * site is a rate; this is the one place the structure itself is drawn, which is
 * what makes the page recognisably about Celestia rather than merely coloured
 * like it.
 *
 * What it is honest about: the cells are a PROPORTION, not an index map. The
 * API publishes how many distinct rows came back, not which ones, so a filled
 * cell means "this much of the square was served", never "row 1,024 was served".
 * The alternative would be to draw arbitrary cells and let a reader believe
 * they name rows, and on a site whose whole argument is about what evidence
 * supports, that is not a trade worth making. The caption says so.
 */

const SIDE = 16;               // 16 x 16 = 256 cells
const CELLS = SIDE * SIDE;

export default function Square({ served, needed, total, label }: {
  served: number;
  needed: number;
  total: number;
  label?: string;
}) {
  if (!total || total <= 0) return null;
  const filled = Math.min(CELLS, Math.round((served / total) * CELLS));
  // The threshold is drawn as a boundary in the same units, so "enough" is a
  // place on the square rather than a number to compare in your head.
  const threshold = Math.min(CELLS, Math.round((needed / total) * CELLS));
  const enough = served >= needed;

  const cells = [];
  for (let i = 0; i < CELLS; i++) {
    cells.push(<i key={i} className={i < filled ? "on" : ""} />);
  }
  // The threshold is a boundary drawn across the square, not a difference in
  // fill: two fills a shade apart are not a line, and "enough" has to be a
  // place a reader can see at a glance rather than a shade they must compare.
  const rows = Math.min(SIDE, threshold / SIDE);
  return (
    <div className="square-block">
      <div className="square-wrap">
        <div
          className="square"
          role="img"
          aria-label={`${served.toLocaleString("en-US")} of ${total.toLocaleString("en-US")} coded rows observed served; ${needed.toLocaleString("en-US")} are enough to rebuild the blob`}
        >
          {cells}
        </div>
        <span className="square-need" style={{ top: `${(rows / SIDE) * 100}%` }}
          title={`${needed.toLocaleString("en-US")} rows: the fill has to reach this line for the blob to be rebuildable`} />
      </div>
      <div className="square-read">
        {label && <span className="label">{label}</span>}
        <p className="lead">
          {served.toLocaleString("en-US")} of {total.toLocaleString("en-US")} coded rows came back.{" "}
          {enough
            ? <>That is past the {needed.toLocaleString("en-US")} the blob needs to be rebuilt — the rule on the square.</>
            : <>The blob needs {needed.toLocaleString("en-US")}, so it is {(needed - served).toLocaleString("en-US")} rows short of the rule.</>}
        </p>
        <p className="sample">
          Each cell is {Math.round(total / CELLS).toLocaleString("en-US")} rows, filled in order. Cells show how much of the
          square was served, not which rows: the API publishes the count of distinct rows, not their indices.
        </p>
      </div>
    </div>
  );
}
