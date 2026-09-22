import type { ReactNode } from "react";

/**
 * One headline figure: the name, the value with its denominator beside it,
 * and at most one short helper line. Nothing about formulas or fields; the
 * definitions live on the methodology page.
 */
export function Metric({ label, value, den, help, tone, title }: {
  label: string;
  value: ReactNode;
  /** printed after the value in the quieter colour: "/ 60" */
  den?: ReactNode;
  help?: ReactNode;
  tone?: "fault" | "absent" | "words";
  title?: string;
}) {
  return (
    <div className="metric" title={title}>
      <div className="label">{label}</div>
      <div className={"value num" + (tone ? " " + tone : "")}>{value}{den != null && <span className="den"> / {den}</span>}</div>
      <div className="help">{help ?? " "}</div>
    </div>
  );
}

export function Metrics({ children }: { children: ReactNode }) {
  return <section className="metrics">{children}</section>;
}
