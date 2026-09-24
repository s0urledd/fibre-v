import { type Meta, int, span } from "@/lib/api";
import { netName } from "./Chrome";

/**
 * Before Fibre is live on the chain, one line under the page title says so,
 * and the tiles below show a quiet dash instead of repeating it each. It
 * renders nothing once meta.fibre_active is true, and nothing while the
 * chain is halted for the upgrade (StatusLine has its own line for that).
 */
export function notLiveOf(meta: Meta | null | undefined): boolean {
  return !!meta?.app_version && !meta.fibre_active;
}

export default function PreLive({ meta }: { meta: Meta | null | undefined }) {
  if (!meta || !notLiveOf(meta)) return null;
  // the chain already runs the version that brings Fibre: StatusLine's "activating" line covers it
  if (Number(meta.app_version) >= Number(meta.fibre_app_version || "10")) return null;
  const sig = meta.upgrade_signal;
  const at = sig?.upgrade_height ? <> at <span className="mono">#{int(sig.upgrade_height)}</span>{sig.eta_seconds ? <> (about {span(sig.eta_seconds)})</> : null}</> : null;
  return (
    <p className="prelive">
      Fibre is not live on {netName(meta.chain_id)} yet. Figures appear once the first blob settles after the upgrade{at}.
    </p>
  );
}
