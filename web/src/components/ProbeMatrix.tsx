import Link from "next/link";
import { type Probe, utc } from "@/lib/api";
import Verdict from "./Verdict";

/**
 * One row per assigned validator, one column per probe point, the latest
 * verdict in each cell. The points are the schedule (four inside the
 * retention window, one in grace, one after), so the columns read left to
 * right as time and the header carries the moment each point ran.
 */
export default function ProbeMatrix({ probes, validators }: {
  probes: Probe[];
  validators: { address: string; row_count: number; moniker?: string }[];
}) {
  // Columns in the order the points ran; a point's time is the earliest
  // probe scheduled for it.
  const points = new Map<string, string>();
  for (const p of probes) {
    const cur = points.get(p.schedule_label);
    if (!cur || p.scheduled_at < cur) points.set(p.schedule_label, p.scheduled_at);
  }
  const cols = [...points.entries()].sort((a, b) => a[1].localeCompare(b[1]));
  const cell = new Map<string, Probe>();
  for (const p of probes) {
    const k = p.validator_address + "|" + p.schedule_label;
    const cur = cell.get(k);
    if (!cur || p.started_at > cur.started_at) cell.set(k, p);
  }
  const phaseOf = (label: string) => probes.find((p) => p.schedule_label === label)?.phase ?? "";
  return (
    <div className="tablewrap">
      <table className="matrix">
        <thead>
          <tr>
            <th>validator</th>
            <th className="right">rows</th>
            {cols.map(([label, at]) => (
              <th key={label} className={"point " + phaseOf(label)} title={`${label}: ${utc(at)} (${phaseOf(label).replace("_", " ")})`}>
                {label}<span className="faint"> {at.slice(11, 16)}Z</span>
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {validators.map((v) => (
            <tr key={v.address}>
              <td>
                <Link href={`/validator/?addr=${v.address}`}>{v.moniker || <span className="mono">{v.address.slice(0, 12)}…</span>}</Link>
              </td>
              <td className="right mono">{v.row_count}</td>
              {cols.map(([label]) => {
                const p = cell.get(v.address + "|" + label);
                return (
                  <td key={label} className="point">
                    {p ? <Verdict cls={p.classification} title={`${p.classification_reason || p.classification} · ${utc(p.started_at)} · ${p.rows_returned}/${p.rows_expected} rows · ${p.total_duration_ms} ms`} /> : <span className="faint">—</span>}
                  </td>
                );
              })}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
