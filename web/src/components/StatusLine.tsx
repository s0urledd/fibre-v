"use client";
import Link from "next/link";
import { useState } from "react";
import { type Meta, type RecordThrough, type Window, type VantageHealth, type ScanGap, int, ago, since, span, utcWord, hhmm, apiFailing, API_BASE } from "@/lib/api";

/** what the page's own data stream says about the API right now */
export type Client = { error: string | null; fetchedAt: string | null; status?: number };

/** the part of a snapshot the status line reads */
export type Snapshot = {
  computed_at?: string;
  record_through?: RecordThrough;
  window?: Window;
  vantage_health?: VantageHealth;
};

const healthWord: Record<string, string> = { ok: "healthy", degraded: "degraded", down: "down" };

/**
 * One line under the title: the observer's own state, whether Fibre is live,
 * how fresh the snapshot is and through which block it was computed. Every
 * item is a fact with a field behind it; the longer reading of each is one
 * click away in the disclosure. Precedence when the line must carry one
 * message: API unreachable, then the observer's own health, then Fibre not
 * live, then the ordinary case.
 */
export default function StatusLine({ meta, metaError, snap, client, measuring }: {
  meta: Meta | null;
  metaError: string | null;
  snap: Snapshot | null;
  client: Client;
  /** Fibre is live but too little has been decided to rate anything yet */
  measuring?: boolean;
}) {
  const [open, setOpen] = useState(false);
  const rt = snap?.record_through;
  const checks = meta?.checks ?? [];
  const failing = checks.filter((c) => !c.ok);
  const ok = checks.filter((c) => c.ok).length;
  const notLive = !!meta?.app_version && !meta.fibre_active;
  // The chain is on the version that brings Fibre but the modules do not
  // answer yet: the upgrade height passed and the chain is halted for, or
  // just resuming from, the switch to the new binary.
  const activating = notLive && Number(meta!.app_version) >= Number(meta!.fibre_app_version || "10");
  // An answer that is not an outage (a 404 for a blob not recorded yet) is
  // the page's to explain; only a network error or a 5xx is the API down.
  const apiDown = apiFailing(client) || (!!metaError && !meta);
  // A stopped chain freezes every figure derived from its pace.
  const chainStale = (meta?.checks ?? []).some((c) => c.name === "chain_liveness" && !c.ok);
  const gaps: ScanGap[] = meta?.scan_gaps ?? [];
  const suspect = snap?.vantage_health?.suspect ?? [];
  const suspectRows = snap?.vantage_health?.suspect_rows ?? 0;
  const sig = meta?.upgrade_signal;
  const missing = sig?.missing_validators ?? [];
  const blocksLeft = sig?.blocks_remaining ?? 0;
  const eta = !chainStale && sig?.eta_seconds ? sig.eta_seconds : 0;
  const items: React.ReactNode[] = [];
  if (apiDown) {
    const err = client.error ?? metaError ?? "no answer";
    items.push(<span key="api"><i className="dot none" />API unreachable · <b>{err}</b></span>);
    if (client.fetchedAt) {
      items.push(<span key="asof" title={`last successful refresh ${utcWord(client.fetchedAt)}`}>Showing data as of <b>{hhmm(client.fetchedAt)}</b> · {ago(client.fetchedAt)}</span>);
    } else {
      items.push(<span key="none">No data yet · retrying every 30 s</span>);
    }
  } else {
    const h = meta?.health;
    items.push(
      <span key="obs" title={h ? checks.map((c) => `${c.name}: ${c.ok ? "ok" : c.detail}`).join(" · ") : undefined}>
        <i className={"dot " + (!h ? "none" : h === "ok" ? "ok" : "hold")} />
        Observer <b>{h ? healthWord[h] ?? h : "connecting…"}</b>{failing.length > 0 && <> · {failing.map((c) => c.name).join(", ")}</>}
      </span>,
    );
  }
  if (activating) {
    items.push(
      <span key="live" title="The chain has reached the app version that brings Fibre. Its modules answer once validators restart on the new binary and blocks resume.">
        <i className="dot hold" />Fibre <b>activating</b>{sig?.upgrade_height ? <> · upgrade height <span className="mono">#{int(sig.upgrade_height)}</span> reached</> : <> · app v{meta!.app_version}</>}, waiting for the Fibre modules
      </span>,
    );
  } else if (notLive) {
    items.push(
      <span key="live" title={eta ? `At the chain's pace over the last ${span(sig!.pace_window_s ?? 0)} (${sig!.block_time_s!.toFixed(2)} s per block). An estimate, not a promise.` : undefined}>
        <i className="dot hold" />Fibre <b>not live</b> · app v{meta!.app_version}{meta!.fibre_app_version ? `, needs v${meta!.fibre_app_version}` : ", needs a later version"}
        {sig?.upgrade_height ? <> · upgrade at <b className="mono">#{int(sig.upgrade_height)}</b>{blocksLeft ? <> · {int(blocksLeft)} block{blocksLeft === 1 ? "" : "s"} to go{eta ? <>, about <b>{span(eta)}</b></> : chainStale ? <>, chain stopped</> : ""}</> : ""}</> : ""}
      </span>,
    );
  } else if (measuring) {
    items.push(<span key="live"><i className="dot ok" />Fibre <b>live</b> · measuring</span>);
  }
  if (!apiDown && snap?.computed_at) {
    items.push(<span key="upd" title={`snapshot computed ${utcWord(snap.computed_at)}`}>Updated <b>{ago(snap.computed_at)}</b></span>);
  }
  if (rt?.height) {
    items.push(
      <span key="thr" title={`block time ${utcWord(rt.block_time)}${rt.chain_height ? ` · chain tip #${int(rt.chain_height)}` : ""}`}>
        Through <b className="mono">#{int(rt.height)}</b>{rt.block_time && <> · {since(rt.block_time) === "just now" ? "just now" : `${since(rt.block_time)} old`}</>}
      </span>,
    );
  }
  if (suspect.length > 0) {
    items.push(<span key="sus"><i className="dot hold" />{int(suspect.length)} probe point{suspect.length === 1 ? "" : "s"} left out</span>);
  }
  items.push(<span key="more"><button type="button" className="dis" aria-expanded={open} aria-controls="status-disc" onClick={() => setOpen((v) => !v)}>Status details</button></span>);

  const lag = rt?.chain_height && rt.height && rt.chain_height > rt.height ? rt.chain_height - rt.height : 0;
  return (
    <>
      <div className="status">{items}</div>
      <p className="disc" id="status-disc" hidden={!open}>
        {meta ? <>Checks <b>{ok} / {checks.length} passing</b>{failing.length > 0 && <> (failing: {failing.map((c) => `${c.name} — ${c.detail}`).join("; ")})</>}</> : <>Observer state <b>unknown</b>: the API has not answered yet</>}
        {meta?.app_version && <> · Fibre <b>{meta.fibre_active ? "live" : activating ? "activating" : "not live"}</b> on app v{meta.app_version}</>}
        {sig && !activating && <> · {(sig.share * 100).toFixed(1)}% of voting power has signalled for v{sig.version} (threshold {(sig.threshold_share * 100).toFixed(1)}%){missing.length > 0 && <>, {int(missing.length)} bonded not yet</>}{sig.upgrade_height ? <>, upgrade at #{int(sig.upgrade_height)}{blocksLeft ? <> ({int(blocksLeft)} block{blocksLeft === 1 ? "" : "s"} to go{eta ? <>, about {span(eta)} at {sig.block_time_s!.toFixed(2)} s per block measured over the last {span(sig.pace_window_s ?? 0)}</> : chainStale ? <>; the chain has stopped, so no estimate</> : ""})</> : ""}</> : ""}, read {ago(sig.polled_at)}</>}
        {rt?.height && <> · Record lag <b>{int(lag)} block{lag === 1 ? "" : "s"}</b>{rt.chain_height ? <> behind the tip #{int(rt.chain_height)}</> : ""}</>}
        {snap?.window && <> · Window <b>{snap.window.name === "all" || snap.window.start.startsWith("0001-") ? "since the first record" : `${utcWord(snap.window.start).slice(0, 16)} → ${utcWord(snap.window.end).slice(0, 16)} UTC`}</b></>}
        {meta?.vantage_info && <> · Observed from <b>{meta.vantage_info.name}</b>{meta.vantage_info.provider ? ` (${meta.vantage_info.provider}${meta.vantage_info.asn ? ` ${meta.vantage_info.asn}` : ""})` : ""}; “unreachable” from here never counts as broken.</>}
        {meta?.pin_status === "chain_ahead" && <> · The chain runs an app version above this observer’s assignment pin: <b>verdicts are withheld</b> until it is re-pinned.</>}
        {gaps.length > 0 && <> · <b>{int(gaps.length)} scan gap{gaps.length === 1 ? "" : "s"}</b> ({gaps.map((g) => `#${int(g.from)}–#${int(g.to)}`).join(", ")}): a publication settled in an unread block is unknown here, never counted served or unserved.</>}
        {suspect.length > 0 && <> · <b>{int(suspect.length)} probe point{suspect.length === 1 ? "" : "s"} left out</b> ({int(suspectRows)} rows): at least half of the validators asked {suspect.some((s) => s.reason.includes("fault")) ? "failed" : "were unreachable"} at once, which from one location cannot be told from this observer’s own network; nothing at those points counts in any figure. Rows kept: {suspect.map((s, i) => <span key={s.at}>{i > 0 ? ", " : ""}<a href={`${API_BASE}/v1/probes?at=${encodeURIComponent(s.at)}&limit=1000`}>{s.label} {hhmm(s.at)}</a></span>)}.</>}
        {" "}<Link href="/methodology/#evidence">Methodology →</Link>
      </p>
      {apiDown && client.fetchedAt && snap && (
        <p className="notice">
          The observer API has not answered since <b>{utcWord(client.fetchedAt)}</b> ({client.error ?? metaError}). Everything below is the last successful snapshot{snap.computed_at ? <>, computed {utcWord(snap.computed_at)}</> : ""}. This is an observer outage, not a Fibre network outage.
        </p>
      )}
      {apiDown && !client.fetchedAt && (
        <p className="notice">
          The observer API is not answering ({client.error ?? metaError}). Nothing can be shown until it does; the page retries every 30 seconds. This is an observer outage, not a Fibre network outage.
        </p>
      )}
    </>
  );
}
