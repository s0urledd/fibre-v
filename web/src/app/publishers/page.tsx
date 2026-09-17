"use client";
import { useState } from "react";
import Link from "next/link";
import { useApi, type Market, type Publisher, type PublisherShare, fmtPct, fmtCount, fmtShare, bytes, utc, ago, tia, shortBech, publisherName } from "@/lib/api";
import Tile from "@/components/Tile";
import Bars from "@/components/Bars";
import Info from "@/components/Info";

const WINDOWS = ["24h", "7d", "30d", "all"];

/**
 * The publisher side of Fibre: who pays for storage, what it costs, and
 * which promises were abandoned. Nothing on this page was measured by this
 * observer. Every figure is a count of something the chain recorded, and the
 * notes at the bottom say which ones are floors.
 */
export default function PublishersPage() {
  const [win, setWin] = useState("7d");
  const { data: m, error, loading } = useApi<Market>(`/v1/market?window=${win}`);
  const { data: list } = useApi<{ publishers: Publisher[] }>(`/v1/publishers?window=${win}`);
  const busy = loading && !m;
  const pubs = list?.publishers ?? [];
  const any = !!m && m.settlements + m.timeouts + m.deposits.count > 0;

  return (
    <>
      <div className="section-head">
        <h1>Publishers</h1>
        {m?.computed_at && (
          <span className="sample" title={`Snapshot taken ${utc(m.computed_at)}, computed in ${m.compute_ms} ms.`}>updated {ago(m.computed_at)}</span>
        )}
        <span className="spacer" />
        <div className="pills" role="group" aria-label="window">
          {WINDOWS.map((w) => <button key={w} aria-pressed={win === w} onClick={() => setWin(w)}>{w}</button>)}
        </div>
      </div>

      {error && <div className="note hold"><span className="label">Observer</span><p>Cannot reach the observer API: {error}. Nothing below is current.</p></div>}

      <div className="tiles six">
        <Tile hero label="Fees settled" loading={busy}
          value={m ? tia(m.fees_settled_utia, { unit: false }) : "—"} unit={m ? "TIA" : undefined}
          tone={m && m.settlements === 0 ? "absent" : undefined}
          sub={m ? `${m.settlements.toLocaleString("en-US")} settlement${m.settlements === 1 ? "" : "s"} · ${bytes(m.bytes)}` : undefined}
          info={<>
            <p>What publishers paid for the blobs settled in this window, charged from their escrow when the <code>MsgPayForFibre</code> landed.</p>
            <p>The chain records no amount on a settlement. Each fee is recomputed from the blob&rsquo;s padded size with the module&rsquo;s own formula, which is exactly what it charges.</p>
            <p><Link href="/methodology/#publishers">Where these numbers come from</Link></p>
          </>} />
        <Tile label="Publishers" loading={busy}
          value={m ? m.publishers_active.toLocaleString("en-US") : "—"}
          sub={m ? `${m.escrow_accounts} escrow account${m.escrow_accounts === 1 ? "" : "s"} known` : undefined}
          info={<p>Accounts that settled at least one blob in the window. The account charged is the one whose key signed the promise, whoever broadcast the transaction.</p>} />
        <Tile label="Paid per MiB" loading={busy}
          value={m?.paid_per_mib_utia != null ? tia(m.paid_per_mib_utia, { unit: false }) : "—"} unit={m?.paid_per_mib_utia != null ? "TIA" : undefined}
          tone={m?.paid_per_mib_utia == null ? "absent" : undefined}
          sub={m ? (m.paid_per_mib_utia != null ? "fees over bytes settled" : "nothing settled") : undefined}
          info={<>
            <p>Fees settled divided by bytes settled. It falls as blobs get larger: the fee has a fixed part, so a 64 KiB blob pays far more per byte than a 128 MiB one.</p>
            <p>Sizes are the padded upload size the module charges for, not the payload.</p>
          </>} />
        <Tile label="Timed out" loading={busy}
          value={m ? (m.timeouts > 0 ? m.timeouts.toLocaleString("en-US") : "none") : "—"}
          tone={m && m.timeouts > 0 ? "fault" : "absent"}
          sub={m ? (m.timeouts > 0 ? `${tia(m.timed_out_utia)} charged · reported by ${m.timeout_processors} account${m.timeout_processors === 1 ? "" : "s"}` : "no timeout reported") : undefined}
          info={<>
            <p>Promises a publisher obtained signatures for and never settled, charged anyway once someone submitted the timeout. Usually that is a validator that stored the shards for nothing.</p>
            <p>This is a floor. A promise nobody reports leaves no trace on chain at all.</p>
          </>} />
        <Tile label="Settlement rate" loading={busy}
          value={m?.settlement_rate.den ? fmtPct(m.settlement_rate) : "—"}
          tone={m?.settlement_rate.den ? undefined : "absent"}
          sub={m?.settlement_rate.den ? `${fmtCount(m.settlement_rate)} promises` : "nothing to rate"}
          info={<p>Settlements over settlements plus reported timeouts. Because unreported timeouts are invisible, this can only overstate how often publishers pay.</p>} />
        <Tile label="Escrow held" loading={busy}
          value={m ? tia(m.escrow_held_utia, { unit: false }) : "—"} unit={m ? "TIA" : undefined}
          tone={m && m.escrow_accounts === 0 ? "absent" : undefined}
          sub={m ? `${tia(m.deposits.utia)} deposited · ${tia(m.withdrawals_executed.utia)} withdrawn` : undefined}
          info={<>
            <p>The balance the chain currently holds for every publisher this observer has seen in a payment, read by state query. Deposits and withdrawals are the window&rsquo;s.</p>
            <p>There is no query for every escrow on the chain, so an account that deposited and never published is not here.</p>
          </>} />
      </div>

      {m && any && (
        <div className="card">
          <div className="card-head">
            <h2>By day</h2>
            <span className="sample">UTC days · {m.window.name === "all" ? "whole history" : `last ${m.window.name}`}</span>
          </div>
          <div className="bars-row">
            <Bars days={m.daily} value={(d) => d.fees_utia} fmt={(v) => tia(v)} label="Fees settled" />
            <Bars days={m.daily} value={(d) => d.bytes} fmt={bytes} label="Bytes settled" />
          </div>
        </div>
      )}

      {m && m.top_publishers.length > 0 && (
        <div className="card">
          <div className="card-head">
            <h2>Share of fees</h2>
            <span className="sample">top {m.top_publishers.length}{m.other_publishers ? ` + ${m.other_publishers.publishers} other` : ""}</span>
            {m.largest_poster && <span className="sample">· largest poster {publisherName(m.largest_poster)} ({bytes(m.largest_poster.bytes)})</span>}
          </div>
          <Shares parts={m.top_publishers} other={m.other_publishers} />
        </div>
      )}

      <div className="tablewrap" style={{ marginTop: "var(--s4)" }}>
        <table>
          <caption>{pubs.length} publisher{pubs.length === 1 ? "" : "s"} with an escrow movement in this window · sorted by fees</caption>
          <thead><tr>
            <th>publisher</th>
            <th className="right">blobs</th>
            <th className="right">bytes</th>
            <th className="right">of bytes</th>
            <th className="right">fees</th>
            <th className="right">of fees</th>
            <th className="right">per MiB</th>
            <th className="right">timed out</th>
            <th className="right">escrow</th>
            <th>first seen</th>
            <th>last seen</th>
          </tr></thead>
          <tbody>
            {pubs.length === 0 && <tr><td colSpan={11} className="muted">{list ? "No escrow movement recorded in this window." : "Loading…"}</td></tr>}
            {pubs.map((p) => (
              <tr key={p.publisher}>
                <td className="mono">
                  <Link href={`/publisher/?addr=${p.publisher}`} title={p.publisher}>{p.label ? <span className="sans">{p.label}</span> : shortBech(p.publisher)}</Link>
                  {p.label && <span className="faint"> {shortBech(p.publisher)}</span>}
                </td>
                <td className="right mono">{p.settlements.toLocaleString("en-US")}</td>
                <td className="right mono">{bytes(p.bytes)}</td>
                <td className="right mono faint">{fmtShare(p.bytes_share)}</td>
                <td className="right mono">{tia(p.fees_utia)}</td>
                <td className="right mono faint">{fmtShare(p.fees_share)}</td>
                <td className="right mono">{p.paid_per_mib_utia != null ? tia(p.paid_per_mib_utia) : "—"}</td>
                <td className={"right mono" + (p.timeouts > 0 ? " err" : " faint")} title={p.timeouts > 0 ? `${tia(p.timed_out_utia)} charged on abandoned promises` : "no timeout reported"}>{p.timeouts > 0 ? p.timeouts : "—"}</td>
                <td className="right mono" title={p.escrow ? (p.escrow.found ? `available ${tia(p.escrow.available_utia)} · read at height ${p.escrow.height.toLocaleString("en-US")}, ${ago(p.escrow.updated_at)}` : "no escrow account on chain") : "not polled yet"}>
                  {p.escrow ? (p.escrow.found ? tia(p.escrow.balance_utia) : <span className="faint">none</span>) : <span className="faint">—</span>}
                </td>
                <td className="mono faint" title={utc(p.first_seen_at)}>{ago(p.first_seen_at)}</td>
                <td className="mono faint" title={utc(p.last_seen_at)}>{ago(p.last_seen_at)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>

      {m && (
        <div className="note" style={{ marginTop: "var(--s5)" }}>
          <span className="label">What these numbers are <Info label="Price formula"><p>{m.price_formula.note}</p><p className="mono">base {m.price_formula.base_gas.toLocaleString("en-US")} gas · {m.price_formula.gas_per_chunk.toLocaleString("en-US")} gas per {bytes(m.price_formula.chunk_bytes)} · {m.price_formula.utia_per_gas} utia per gas</p></Info></span>
          <ul className="notes">
            {m.notes.map((n) => <li key={n}>{n}</li>)}
          </ul>
        </div>
      )}
    </>
  );
}

/** A single stacked bar of fee shares, top publishers then the rest. */
function Shares({ parts, other }: { parts: PublisherShare[]; other: PublisherShare | null }) {
  const all = other ? [...parts, { ...other, publisher: "", label: `${other.publishers} other` }] : parts;
  return (
    <div className="shares">
      <div className="shares-bar" role="img" aria-label="share of fees by publisher">
        {all.map((p, i) => (
          <span key={p.publisher || "other"} className={"seg" + (p.publisher ? "" : " other")} style={{ flexBasis: `${Math.max(0.5, (p.fees_share ?? 0) * 100)}%`, opacity: p.publisher ? 1 - i * 0.14 : undefined }}
            title={`${publisherName(p)} · ${tia(p.fees_utia)} · ${fmtShare(p.fees_share)} of fees · ${bytes(p.bytes)}`} />
        ))}
      </div>
      <ul className="shares-legend">
        {all.map((p, i) => (
          <li key={p.publisher || "other"}>
            <span className={"swatch" + (p.publisher ? "" : " other")} style={{ opacity: p.publisher ? 1 - i * 0.14 : undefined }} />
            <span className="who">{p.publisher ? <Link href={`/publisher/?addr=${p.publisher}`} className="mono" title={p.publisher}>{publisherName(p)}</Link> : <span className="muted">{p.label}</span>}</span>
            <span className="figs mono faint">{fmtShare(p.fees_share)} · {tia(p.fees_utia)} · {bytes(p.bytes)}</span>
          </li>
        ))}
      </ul>
    </div>
  );
}
