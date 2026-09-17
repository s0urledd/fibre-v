/**
 * The data square. A blob is erasure-coded into `total` rows of which `needed`
 * are enough to rebuild it (16,384 and 4,096 for blob v0). The square fills in
 * proportion to the distinct rows that came back; the rule across it is the
 * rebuild threshold. Cells are a proportion, not an index map: the API
 * publishes how many distinct rows came back, not which.
 */
const SIDE = 16;
const CELLS = SIDE * SIDE;

export default function Square({ served, needed, total }: { served: number; needed: number; total: number }) {
  if (!total || total <= 0) return null;
  const filled = Math.min(CELLS, Math.round((served / total) * CELLS));
  const threshold = Math.min(CELLS, Math.round((needed / total) * CELLS));
  const rows = Math.min(SIDE, threshold / SIDE);
  const cells = [];
  for (let i = 0; i < CELLS; i++) cells.push(<i key={i} className={i < filled ? "on" : ""} />);
  return (
    <div className="square-wrap" title={`${served.toLocaleString("en-US")} of ${total.toLocaleString("en-US")} coded rows came back; ${needed.toLocaleString("en-US")} are enough to rebuild the blob. Each cell is ${Math.round(total / CELLS)} rows, filled in order.`}>
      <div className="square" role="img"
        aria-label={`${served.toLocaleString("en-US")} of ${total.toLocaleString("en-US")} coded rows served; ${needed.toLocaleString("en-US")} are enough to rebuild the blob`}>
        {cells}
      </div>
      <span className="square-need" style={{ top: `${(rows / SIDE) * 100}%` }} />
    </div>
  );
}
