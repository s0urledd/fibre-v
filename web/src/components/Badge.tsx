// Verdict badges: text always, glyph always, colour third (R9 section 6.3).
const BADGES: Record<string, { label: string; glyph: string; token: string; def: string }> = {
  HEALTHY: { label: "healthy", glyph: "●", token: "var(--status-healthy)", def: "Assigned rows served correctly while the promise held." },
  TOLERATED: { label: "tolerated", glyph: "◐", token: "var(--status-tolerated)", def: "Not found or unreachable just after must_serve_until, within the measured prune lag. Not counted against the validator." },
  FAULT: { label: "fault", glyph: "▲", token: "var(--status-fault)", def: "Assigned rows not served, wrong, unverifiable, or endpoint unreachable while the promise held." },
  EXPECTED_GONE: { label: "expected gone", glyph: "○", token: "var(--status-expected-gone)", def: "Not found after the window plus tolerance. Correct behaviour." },
  SERVED_PAST_WINDOW: { label: "served after window", glyph: "◎", token: "var(--status-post-window)", def: "Still serving after the obligation ended. Not a fault." },
  UNREACHABLE_POST_WINDOW: { label: "unreachable after window", glyph: "◇", token: "var(--status-post-window)", def: "Unreachable after the obligation ended. Not a retention fault." },
  EXPECTED_UNASSIGNED: { label: "unassigned", glyph: "○", token: "var(--status-expected-gone)", def: "Validator was not assigned this shard." },
  SERVING_UNASSIGNED: { label: "serving unassigned", glyph: "◈", token: "var(--status-post-window)", def: "Validator returned a shard it was not assigned. Flagged for review." },
  PROBE_ERROR: { label: "probe error", glyph: "—", token: "var(--text-3)", def: "The observer's own probe failed. A gap, not a verdict." },
  NOT_PROBED: { label: "not probed", glyph: "—", token: "var(--text-3)", def: "The slot elapsed unprobed, or the policy sampled it out. A gap, not a verdict." },
};

export function badgeDef(cls: string) {
  return BADGES[cls] ?? { label: cls.toLowerCase().replace(/_/g, " "), glyph: "?", token: "var(--text-3)", def: "" };
}

export default function Badge({ cls, title }: { cls: string; title?: string }) {
  const b = badgeDef(cls);
  const notProbed = cls === "NOT_PROBED" || cls === "PROBE_ERROR";
  return (
    <span className={"badge" + (notProbed ? " not-probed" : "")} style={{ ["--c" as string]: b.token }} title={title ?? b.def}>
      <span className="g" aria-hidden="true">{b.glyph}</span>
      {b.label}
    </span>
  );
}

export function Legend({ classes }: { classes?: string[] }) {
  const list = classes ?? ["HEALTHY", "TOLERATED", "FAULT", "EXPECTED_GONE", "UNREACHABLE_POST_WINDOW", "NOT_PROBED"];
  return (
    <div className="legend">
      {list.map((c) => (
        <span key={c}><Badge cls={c} /> <span className="muted">{badgeDef(c).def}</span></span>
      ))}
    </div>
  );
}
