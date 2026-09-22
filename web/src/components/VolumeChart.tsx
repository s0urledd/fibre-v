import { type Market, bytes, int } from "@/lib/api";

/**
 * Settled volume per UTC day over a fixed seven-day context: the padded
 * size the module charges for, from the chain's own records. The last day
 * is partial and drawn so; a day with nothing settled is a hairline, not
 * an absence.
 */
export default function VolumeChart({ market, days = 7 }: { market: Market | null; days?: number }) {
  if (!market) return <p className="vchart-empty">Loading…</p>;
  const end = new Date(market.window.end);
  const today = end.toISOString().slice(0, 10);
  const list: string[] = [];
  for (let i = days - 1; i >= 0; i--) list.push(new Date(end.getTime() - i * 86400_000).toISOString().slice(0, 10));
  const byDay = new Map(market.daily.map((d) => [d.day, d]));
  const max = Math.max(0, ...list.map((d) => byDay.get(d)?.bytes ?? 0));
  // the axis top: a round number of MiB (or GiB) above the largest day
  const mib = 1048576;
  const step = max >= 1024 * mib ? 512 * mib : max >= 200 * mib ? 100 * mib : 20 * mib;
  const top = Math.max(step, Math.ceil(max / step) * step);
  const label = (d: string) => new Date(d + "T00:00:00Z").toLocaleDateString("en-US", { month: "short", day: "numeric", timeZone: "UTC" });
  return (
    <div className="vchart" role="img" aria-label={`settled volume per day over the last ${days} days`}>
      <div className="y"><span>{bytes(top)}</span><span>{bytes(top / 2)}</span><span>0</span></div>
      <div className="plot">
        {list.map((d) => {
          const b = byDay.get(d);
          const v = b?.bytes ?? 0;
          const cls = "col" + (d === today ? " partial" : "") + (v === 0 ? " zero" : "");
          const title = `${d}${d === today ? " (partial day)" : ""} · ${bytes(v)} · ${int(b?.settlements ?? 0)} settlement${(b?.settlements ?? 0) === 1 ? "" : "s"}`;
          return <div key={d} className={cls} title={title}><i style={{ height: `${Math.max(v / top * 100, v ? 1 : 0).toFixed(1)}%` }} /></div>;
        })}
      </div>
      <div className="x">{list.map((d) => <span key={d}>{label(d)}</span>)}</div>
    </div>
  );
}
