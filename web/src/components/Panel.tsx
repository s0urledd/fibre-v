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
/**
 * The three kinds of evidence a figure can rest on. They are different
 * claims, and a reader should never have to guess which one a number is:
 * a count of what the chain recorded, bytes this observer fetched and
 * verified, or what this observer's own network saw from one place.
 */
export const EVIDENCE: Record<"chain" | "verified" | "observed", { label: string; title: string }> = {
  chain: { label: "chain", title: "Chain record: a count of something the chain recorded. Nothing here was measured by this observer." },
  verified: { label: "verified", title: "Verified response: bytes this observer fetched and verified against the on-chain commitment, or a certificate checked against the validator\u2019s consensus key." },
  observed: { label: "observed", title: "Vantage observation: what this observer\u2019s own network saw from one location. It says nothing about any shard." },
};

export function Cell({ label, value, unit, sub, detail, tone, info, loading, evidence }: {
  label: string;
  value: ReactNode;
  unit?: string;
  sub?: ReactNode;
  detail?: string;
  tone?: "ok" | "fault" | "absent";
  info?: ReactNode;
  loading?: boolean;
  /** which kind of evidence the figure rests on; printed as a small tag by the label */
  evidence?: keyof typeof EVIDENCE;
}) {
  const vcls = ["value", tone ?? "", loading ? "loading" : ""].filter(Boolean).join(" ");
  return (
    <div className="cell" aria-busy={loading || undefined}>
      <span className="label">
        {label}
        {evidence && <span className={`evidence evidence--${evidence}`} title={EVIDENCE[evidence].title}>{EVIDENCE[evidence].label}</span>}
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
