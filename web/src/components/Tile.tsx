import type { ReactNode } from "react";
import Info from "./Info";

/**
 * One figure with its label, its unit and the sample it rests on. The tiles
 * across the top of the overview and the five service layers on a validator's
 * page are the same component, so a number reads the same everywhere.
 */
export default function Tile({ label, value, unit, sub, info, hero, tone, loading, badge, className }: {
  label: string;
  value: ReactNode;
  unit?: string;
  sub?: ReactNode;
  info?: ReactNode;
  hero?: boolean;
  tone?: "fault" | "absent";
  loading?: boolean;
  badge?: ReactNode;
  className?: string;
}) {
  const cls = ["tile", hero ? "hero" : "", loading ? "loading" : "", className ?? ""].filter(Boolean).join(" ");
  const vcls = ["value", tone ?? ""].filter(Boolean).join(" ");
  return (
    <div className={cls} aria-busy={loading || undefined}>
      <span className="label">
        {label}
        {info && <Info label={label}>{info}</Info>}
        {badge && <span className="badge">{badge}</span>}
      </span>
      <span className={vcls}>
        {loading ? "0000" : value}
        {unit && !loading && <span className="unit">{unit}</span>}
      </span>
      {sub !== undefined && <span className="sub">{loading ? " " : sub}</span>}
    </div>
  );
}
