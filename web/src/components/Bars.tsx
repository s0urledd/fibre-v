import { type DayBucket } from "@/lib/api";

/**
 * One bar per UTC day. Days with nothing recorded are drawn as an empty slot
 * rather than left out, so a quiet week reads as a quiet week and not as a
 * shorter chart. The scale is the largest day; the head names it, so every
 * bar is readable against a value the chart reaches.
 */
export default function Bars({ days, value, fmt, label, tone }: {
  days: DayBucket[];
  value: (d: DayBucket) => number;
  fmt: (v: number) => string;
  label: string;
  tone?: "accent" | "fault";
}) {
  if (days.length === 0) {
    return <div className="bars empty"><span className="label">{label}</span><p className="muted">nothing recorded in this window</p></div>;
  }
  const byDay = new Map(days.map((d) => [d.day, d]));
  const first = new Date(days[0].day + "T00:00:00Z").getTime();
  const last = new Date(days[days.length - 1].day + "T00:00:00Z").getTime();
  const filled: { day: string; v: number; d?: DayBucket }[] = [];
  for (let t = first; t <= last; t += 86400_000) {
    const day = new Date(t).toISOString().slice(0, 10);
    const d = byDay.get(day);
    filled.push({ day, v: d ? value(d) : 0, d });
  }
  const max = Math.max(1, ...filled.map((f) => f.v));
  const total = filled.reduce((a, f) => a + f.v, 0);
  const n = filled.length;
  const every = Math.max(1, Math.ceil(n / 7));
  return (
    <div className={"bars " + (tone ?? "accent")}>
      <div className="bars-head">
        <span className="label">{label}</span>
        <span className="sample">{n} day{n === 1 ? "" : "s"} · peak {fmt(max)} · total {fmt(total)}</span>
      </div>
      <div className="bars-plot" role="img" aria-label={`${label}, ${n} days, peak ${fmt(max)}`}>
        {filled.map((f, i) => (
          <div key={f.day} className="slot" title={`${f.day}: ${fmt(f.v)}${f.d ? ` · ${f.d.settlements} settlement${f.d.settlements === 1 ? "" : "s"}${f.d.timeouts ? ` · ${f.d.timeouts} timed out` : ""}` : " · nothing recorded"}`}>
            <div className={"bar" + (f.v > 0 ? "" : " quiet")} style={{ height: f.v > 0 ? `${Math.max(2, (f.v / max) * 100)}%` : "2px" }} />
            <span className={"tick" + ((i % every === 0 || i === n - 1) ? "" : " hidden")}>{f.day.slice(5)}</span>
          </div>
        ))}
      </div>
    </div>
  );
}
