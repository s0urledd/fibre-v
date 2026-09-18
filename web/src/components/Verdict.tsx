/**
 * Verdict marks.
 *
 * Three channels in priority order: the word, then the shape, then the colour.
 * The word is always present and never abbreviated to an icon. The shape is one
 * of four, because fifteen distinguishable shapes at ten pixels is not
 * achievable and pretending otherwise produces a legend nobody reads. The
 * colour is third and there are only two of them.
 *
 * What the tiers encode is the thing a reader actually needs, which is not the
 * class but what the class does to the rate:
 *
 *   kept    counted in the numerator                       HEALTHY
 *   fault   counted against — the only accusation          FAULT
 *   hold    held out: we could not complete the measurement UNREACHABLE, IDENTITY_EXPIRED
 *   held    held out: nothing was owed, or not by this promise
 *   gap     not observed at all — a gap, never a verdict    NOT_PROBED, PROBE_ERROR
 *
 * FAULT owns the only pointed shape in the system and the only status colour
 * allowed to touch a word, so an accusation is pre-attentive and survives total
 * colour loss, and no later change can quietly promote another class into the
 * alarm channel without someone noticing the triangle.
 */

export type Tier = "kept" | "fault" | "hold" | "held" | "gap";

type Def = { label: string; tier: Tier; def: string };

const VERDICTS: Record<string, Def> = {
  HEALTHY: {
    label: "healthy", tier: "kept",
    def: "Assigned rows served correctly while the promise held.",
  },
  FAULT: {
    label: "fault", tier: "fault",
    def: "An identity-verified endpoint, for a shard it signed for, said it has no such shard, returned bytes that do not verify against the commitment, or returned rows outside its assignment. The only class that counts against a validator.",
  },
  UNREACHABLE: {
    label: "unreachable", tier: "hold",
    def: "This site could not complete a conversation with the endpoint while the validator was under obligation. From one location that is not distinguishable from a route, firewall or peering problem on this site's own path, so it is recorded and shown but kept out of the serve rate.",
  },
  IDENTITY_EXPIRED: {
    label: "identity expired", tier: "hold",
    def: "The certificate is endorsed by the right consensus key, but its signed validity window has lapsed or has not started. A renewal running late, not someone else answering on this endpoint.",
  },
  IDENTITY_MISMATCH: {
    label: "bad certificate", tier: "hold",
    def: "The certificate is not endorsed by this validator's consensus key, so no client can download from the endpoint. A statement about the endpoint, shown as its status; not about any shard, so outside the serve rate.",
  },
  SERVER_ERROR: {
    label: "server error", tier: "hold",
    def: "The endpoint was reached and answered with an application error instead of the shard. It did not say it lacks the shard; from one probe that is not distinguishable from a transient fault, so it is shown beside the rate, not inside it.",
  },
  THROTTLED: {
    label: "rate limited", tier: "hold",
    def: "The endpoint was reached and refused the download with a rate limit. That says nothing about the shard, so it is shown beside the rate, not inside it, and the prober backs off from a validator that says so.",
  },
  UNATTESTED: {
    label: "unattested", tier: "held",
    def: "The settled promise carries no verified signature from this validator, so nothing on chain proves it ever stored the shard. Whatever the probe found is recorded but kept out of the serve rate, in both directions.",
  },
  NOT_REGISTERED: {
    label: "not registered", tier: "held",
    def: "The validator had no Fibre host in x/valaddr when the probe ran, so nobody could fetch its rows. Jailing and unbonding remove a provider from the bonded list while the chain keeps the registration, so this is a registry state, not a refusal to serve.",
  },
  SHADOWED_SHARD: {
    label: "shadowed shard", tier: "held",
    def: "The rows returned are genuine rows of this blob and are exactly the set another settled promise over the same blob assigns to this validator. DownloadShard is addressed by the commitment alone and the store serves the first shard by promise-hash order, so that promise answers in this one's place; the validator has no way to tell them apart.",
  },
  UNMATCHED_GENUINE: {
    label: "unmatched genuine rows", tier: "held",
    def: "The rows returned are genuine rows of this blob but match no settled promise's assignment for this validator. The store serves the first shard by promise-hash order, and a shard uploaded for a promise that never settled is on disk until its prune and never on chain, so a validator can answer with it honestly. Not a fault the evidence supports; held out of the rate and counted beside it, with the row indices on the row.",
  },
  TOLERATED: {
    label: "tolerated", tier: "held",
    def: "Not found or unreachable just after must_serve_until, within the measured prune lag. Not counted against the validator.",
  },
  EXPECTED_GONE: {
    label: "expected gone", tier: "held",
    def: "Not found after the window plus tolerance. Correct behaviour.",
  },
  SERVED_PAST_WINDOW: {
    label: "served after window", tier: "held",
    def: "Still serving after the obligation ended. Not a fault.",
  },
  UNREACHABLE_POST_WINDOW: {
    label: "unreachable after window", tier: "held",
    def: "Unreachable after the obligation ended. Not a retention fault.",
  },
  EXPECTED_UNASSIGNED: {
    label: "unassigned", tier: "held",
    def: "Validator was not assigned this shard.",
  },
  SERVING_UNASSIGNED: {
    label: "serving unassigned", tier: "held",
    def: "Validator returned a shard it was not assigned. Flagged for review.",
  },
  PROBE_ERROR: {
    label: "probe error", tier: "gap",
    def: "The observer's own probe failed. A gap, not a verdict.",
  },
  NOT_PROBED: {
    label: "not probed", tier: "gap",
    def: "The slot elapsed unprobed, or the policy sampled it out. A gap, not a verdict.",
  },
};

/** Every class, so no page can show a partial taxonomy. */
export const ALL_VERDICTS = Object.keys(VERDICTS);

export function verdictDef(cls: string): Def {
  return VERDICTS[cls] ?? { label: cls.toLowerCase().replace(/_/g, " "), tier: "gap", def: "" };
}

/** The colour a tier paints with, for callers that draw rather than compose. */
export function tierColor(tier: Tier): string {
  switch (tier) {
    case "fault": return "var(--fault)";
    case "hold": return "var(--hold)";
    case "kept": return "var(--text)";
    case "held": return "var(--text-2)";
    default: return "var(--text-3)";
  }
}

/**
 * The mark. Every shape is stroked in its own colour as well as filled, so it
 * keeps a boundary when the fill is the same value as the paper, and so the
 * shape rather than the fill is what survives a greyscale print.
 */
export function Mark({ tier, className }: { tier: Tier; className?: string }) {
  const cls = `mark mark--${tier}${className ? " " + className : ""}`;
  const common = { className: cls, viewBox: "0 0 10 10", "aria-hidden": true as const, focusable: "false" as const };
  switch (tier) {
    case "fault":   // the only pointed shape in the system
      return <svg {...common}><path d="M5 1 L9.2 8.6 H0.8 Z" fill="currentColor" /></svg>;
    case "kept":
      return <svg {...common}><circle cx="5" cy="5" r="3.6" fill="currentColor" /></svg>;
    case "hold":    // half disc: we saw half of what we needed to
      return <svg {...common}><circle cx="5" cy="5" r="3.6" fill="none" stroke="currentColor" strokeWidth="1.25" /><path d="M5 1.4 A3.6 3.6 0 0 1 5 8.6 Z" fill="currentColor" /></svg>;
    case "held":
      return <svg {...common}><circle cx="5" cy="5" r="3.4" fill="none" stroke="currentColor" strokeWidth="1.25" /></svg>;
    default:        // a gap is a dash, because nothing was observed to draw
      return <svg {...common}><path d="M1.4 5 H8.6" stroke="currentColor" strokeWidth="1.25" strokeLinecap="round" /></svg>;
  }
}

/** Mark plus word. The word is the primary channel and is never dropped. */
export default function Verdict({ cls, title }: { cls: string; title?: string }) {
  const d = verdictDef(cls);
  return (
    <span className={`verdict verdict--${d.tier}`} title={title ?? d.def}>
      <Mark tier={d.tier} />
      <span className="w">{d.label}</span>
    </span>
  );
}

/**
 * A count that is zero renders as a dot rather than a nought, so a column is
 * blank except where there is something to report. On the fault column that
 * means a single accusation in fifty rows is the only ink in its column.
 */
/**
 * A count of one tier. Zero and "nothing was rated" used to print the same
 * dot: `rated` says which it is, so a validator with a thousand rated probes
 * and no fault reads 0, and one nobody ever reached reads a dot.
 */
export function Count({ n, tier, rated }: { n: number | undefined; tier: Tier; rated?: boolean }) {
  if (!n) {
    if (rated) return <span className="nil zero" title="none in this window">0</span>;
    return <span className="nil" title="nothing rated in this window">·</span>;
  }
  return (
    <span className={`verdict verdict--${tier}`}>
      <Mark tier={tier} />
      <span className="w mono">{n.toLocaleString("en-US")}</span>
    </span>
  );
}

/**
 * The whole taxonomy, in tier order. This is rendered on the methodology page
 * and nowhere else: repeating fifteen definitions below every table on the site
 * is how the current pages came to be four thousand pixels tall, and two of
 * those copies had disagreed about which classes exist.
 */
export function Legend() {
  const order: Tier[] = ["kept", "fault", "hold", "held", "gap"];
  const sorted = [...ALL_VERDICTS].sort(
    (a, b) => order.indexOf(verdictDef(a).tier) - order.indexOf(verdictDef(b).tier),
  );
  return (
    <div className="legend">
      {sorted.map((c) => (
        <div key={c} id={`verdict-${c.toLowerCase()}`}>
          <Verdict cls={c} title="" />
          <span className="def">{verdictDef(c).def}</span>
        </div>
      ))}
    </div>
  );
}
