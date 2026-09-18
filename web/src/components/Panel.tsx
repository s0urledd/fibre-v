import type { ReactNode } from "react";
import Info from "./Info";

/**
 * A framed panel with a header bar: the unit the overview is built from. The
 * header names what the panel holds and where it comes from (measured here,
 * or read from the chain), the body is either a row of cells or a table.
 */
export function Panel({ title, live, right, children, className }: {
  title: ReactNode;
  live?: boolean;
  right?: ReactNode;
  children: ReactNode;
  className?: string;
}) {
  return (
    <section className={["panel", className ?? ""].filter(Boolean).join(" ")}>
      <div className="panel-head">
        <span className="panel-title">{title}</span>
        {live && <span className="live" title="Refreshed on a schedule while the observer runs.">live</span>}
        <span className="spacer" />
        {right && <span className="right">{right}</span>}
      </div>
      {children}
    </section>
  );
}

/** the change since the previous window, in the unit the figure is read in */
export type Delta = {
  /** signed change, already in display units (percentage points, ms, count) */
  value: number;
  /** what a rise means */
  goodWhen: "up" | "down";
  unit?: string;
  decimals?: number;
  title?: string;
};

function DeltaPill({ d }: { d: Delta }) {
  const dec = d.decimals ?? 1;
  const flat = Math.abs(d.value) < Math.pow(10, -dec) / 2;
  const tone = flat ? "flat" : (d.value > 0) === (d.goodWhen === "up") ? "good" : "bad";
  const sign = flat ? "" : d.value > 0 ? "+" : "−";
  return (
    <span className={"delta " + tone} title={d.title ?? "Change since the previous window of the same length."}>
      {flat ? "±0" : `${sign}${Math.abs(d.value).toFixed(dec)}`}{d.unit ?? ""}
    </span>
  );
}

/**
 * One figure in a panel: label, figure with its unit, the change since the
 * previous window when there is one, and one line of sample underneath. The
 * sample never wraps; its longer reading is the line's tooltip.
 */
export function Cell({ label, value, unit, sub, detail, delta, tone, info, loading }: {
  label: string;
  value: ReactNode;
  unit?: string;
  sub?: ReactNode;
  detail?: string;
  delta?: Delta | null;
  tone?: "ok" | "fault" | "absent";
  info?: ReactNode;
  loading?: boolean;
}) {
  const vcls = ["value", tone ?? "", loading ? "loading" : ""].filter(Boolean).join(" ");
  return (
    <div className="cell" aria-busy={loading || undefined}>
      <span className="label">
        {label}
        {info && <Info label={label}>{info}</Info>}
      </span>
      <span className={vcls}>
        <span className="figure">{loading ? "0000" : value}</span>
        {unit && !loading && <span className="unit">{unit}</span>}
        {delta && !loading && <DeltaPill d={delta} />}
      </span>
      <span className="sub" title={loading ? undefined : detail ?? (typeof sub === "string" ? sub : undefined)}>{loading || sub === undefined ? " " : sub}</span>
    </div>
  );
}
