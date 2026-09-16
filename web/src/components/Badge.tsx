// Verdict badges: text always, glyph always, colour third (R9 section 6.3).
const BADGES: Record<string, { label: string; glyph: string; token: string; def: string }> = {
  HEALTHY: { label: "healthy", glyph: "●", token: "var(--status-healthy)", def: "Assigned rows served correctly while the promise held." },
  TOLERATED: { label: "tolerated", glyph: "◐", token: "var(--status-tolerated)", def: "Not found or unreachable just after must_serve_until, within the measured prune lag. Not counted against the validator." },
  FAULT: { label: "fault", glyph: "▲", token: "var(--status-fault)", def: "This site reached the validator and it failed to hand over a shard the chain proves it stored: no such shard, unverifiable bytes, a server error, or a TLS identity that is not the endorsed consensus key. The only class that counts against a validator." },
  EXPECTED_GONE: { label: "expected gone", glyph: "○", token: "var(--status-expected-gone)", def: "Not found after the window plus tolerance. Correct behaviour." },
  SERVED_PAST_WINDOW: { label: "served after window", glyph: "◎", token: "var(--status-post-window)", def: "Still serving after the obligation ended. Not a fault." },
  UNREACHABLE_POST_WINDOW: { label: "unreachable after window", glyph: "◇", token: "var(--status-post-window)", def: "Unreachable after the obligation ended. Not a retention fault." },
  EXPECTED_UNASSIGNED: { label: "unassigned", glyph: "○", token: "var(--status-expected-gone)", def: "Validator was not assigned this shard." },
  SERVING_UNASSIGNED: { label: "serving unassigned", glyph: "◈", token: "var(--status-post-window)", def: "Validator returned a shard it was not assigned. Flagged for review." },
  UNREACHABLE: { label: "unreachable", glyph: "◇", token: "var(--status-tolerated)", def: "This site could not complete a conversation with the endpoint while the validator was under obligation. From one location that is not distinguishable from a route, firewall or peering problem on this site's own path, so it is recorded and shown but kept out of the serve rate." },
  NOT_REGISTERED: { label: "not registered", glyph: "○", token: "var(--text-3)", def: "The validator had no Fibre host in x/valaddr when the probe ran, so nobody could fetch its rows. Jailing and unbonding remove a provider from the bonded list while the chain keeps the registration, so this is a registry state, not a refusal to serve." },
  SHADOWED_SHARD: { label: "shadowed shard", glyph: "◈", token: "var(--status-post-window)", def: "The rows returned are genuine rows of this blob but not the indices this promise assigns. DownloadShard is addressed by the commitment alone and a store keeps one shard per commitment, so a second promise over the same blob answers in its place. The validator has no way to tell them apart." },
  IDENTITY_EXPIRED: { label: "identity expired", glyph: "◐", token: "var(--status-tolerated)", def: "The certificate is endorsed by the right consensus key, but its signed validity window has lapsed or has not started. A renewal running late, not someone else answering on this endpoint." },
  UNATTESTED: { label: "unattested", glyph: "◌", token: "var(--text-3)", def: "The settled promise carries no verified signature from this validator, so nothing on chain proves it ever stored the shard. Whatever the probe found is recorded but kept out of the serve rate, in both directions." },
  PROBE_ERROR: { label: "probe error", glyph: "—", token: "var(--text-3)", def: "The observer's own probe failed. A gap, not a verdict." },
  NOT_PROBED: { label: "not probed", glyph: "—", token: "var(--text-3)", def: "The slot elapsed unprobed, or the policy sampled it out. A gap, not a verdict." },
};

export function badgeDef(cls: string) {
  return BADGES[cls] ?? { label: cls.toLowerCase().replace(/_/g, " "), glyph: "?", token: "var(--text-3)", def: "" };
}

export default function Badge({ cls, title }: { cls: string; title?: string }) {
  const b = badgeDef(cls);
  const notProbed = ["NOT_PROBED", "PROBE_ERROR", "UNATTESTED", "NOT_REGISTERED"].includes(cls);
  return (
    <span className={"badge" + (notProbed ? " not-probed" : "")} style={{ ["--c" as string]: b.token }} title={title ?? b.def}>
      <span className="g" aria-hidden="true">{b.glyph}</span>
      {b.label}
    </span>
  );
}

export function Legend({ classes }: { classes?: string[] }) {
  const list = classes ?? ["HEALTHY", "FAULT", "UNREACHABLE", "UNATTESTED", "SHADOWED_SHARD", "NOT_REGISTERED", "IDENTITY_EXPIRED", "TOLERATED", "EXPECTED_GONE", "UNREACHABLE_POST_WINDOW", "NOT_PROBED"];
  return (
    <div className="legend">
      {list.map((c) => (
        <span key={c}><Badge cls={c} /> <span className="muted">{badgeDef(c).def}</span></span>
      ))}
    </div>
  );
}
