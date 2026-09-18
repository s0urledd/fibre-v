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

/**
 * One figure in a panel: label, figure with its unit, and one line of sample
 * underneath. The sample never wraps; its longer reading is the line's
 * tooltip.
 */
export function Cell({ label, value, unit, sub, detail, tone, info, loading }: {
  label: string;
  value: ReactNode;
  unit?: string;
  sub?: ReactNode;
  detail?: string;
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
      </span>
      <span className="sub" title={loading ? undefined : detail ?? (typeof sub === "string" ? sub : undefined)}>{loading || sub === undefined ? " " : sub}</span>
    </div>
  );
}
