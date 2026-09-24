"use client";
import { useState } from "react";
import Link from "next/link";
import { useApi, type Market, type Publisher, fmtPct, fmtCount, fmtShare, bytes, utc, ago, tia, shortBech, publisherName, through } from "@/lib/api";
import { Panel, Cell } from "@/components/Panel";
import Chart, { calendar, CATEGORICAL, OTHER_COLOR, type Row, type Series } from "@/components/Chart";
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

  return (
    <>
      <div className="section-head">
        <h1>Publishers</h1>
        {m && (
          <Info label="About these figures">
            <p>Everything on this page is a count of something the chain recorded; none of it was measured by this observer.</p>
            <ul>{m.notes.map((n) => <li key={n}>{n}</li>)}</ul>
            <p className="mono">fee = ({m.price_formula.base_gas.toLocaleString("en-US")} + {m.price_formula.gas_per_chunk.toLocaleString("en-US")} × ⌈size / {bytes(m.price_formula.chunk_bytes)}⌉) gas × {m.price_formula.utia_per_gas} utia</p>
          </Info>
        )}
        <span className="spacer" />
        <div className="pills" role="group" aria-label="window">
          {WINDOWS.map((w) => <button key={w} aria-pressed={win === w} onClick={() => setWin(w)}>{w}</button>)}
        </div>
      </div>

      {error && <div className="note hold"><span className="label">Observer</span><p>Cannot reach the observer API: {error}. Nothing below is current.</p></div>}

      <Panel title="Market" right={<>from the chain&rsquo;s own records · <Link href="/methodology/#publishers">methodology →</Link></>}>
      <div className="cells three">
        <Cell label="Fees settled" loading={busy} evidence="chain"
          value={m ? tia(m.fees_settled_utia, { unit: false }) : "—"} unit={m ? "TIA" : undefined}
          tone={m && m.settlements === 0 ? "absent" : undefined}
          sub={m ? `${m.settlements.toLocaleString("en-US")} settlement${m.settlements === 1 ? "" : "s"} · ${bytes(m.bytes)}` : undefined}
          info={<>
            <p>What publishers paid for the blobs settled in this window, charged from their escrow when the <code>MsgPayForFibre</code> landed.</p>
            <p>The chain records no amount on a settlement. Each fee is recomputed from the blob&rsquo;s padded size with the module&rsquo;s own formula, which is exactly what it charges.</p>
            <p><Link href="/methodology/#publishers">Where these numbers come from</Link></p>
          </>} />
        <Cell label="Publishers" loading={busy} evidence="chain"
          value={m ? m.publishers_active.toLocaleString("en-US") : "—"}
          sub={m ? `${m.escrow_accounts} escrow account${m.escrow_accounts === 1 ? "" : "s"}` : undefined}
          info={<p>Accounts that settled at least one blob in the window. The account charged is the one whose key signed the promise, whoever broadcast the transaction.</p>} />
        <Cell label="Paid per MiB" loading={busy} evidence="chain"
          value={m?.paid_per_mib_utia != null ? tia(m.paid_per_mib_utia, { unit: false }) : "—"} unit={m?.paid_per_mib_utia != null ? "TIA" : undefined}
          tone={m?.paid_per_mib_utia == null ? "absent" : undefined}
          sub={m ? (m.paid_per_mib_utia != null ? "fees over bytes settled" : "nothing settled") : undefined}
          info={<>
            <p>Fees settled divided by bytes settled. It falls as blobs get larger: the fee has a fixed part, so a 64 KiB blob pays far more per byte than a 128 MiB one.</p>
            <p>Sizes are the padded upload size the module charges for, not the payload.</p>
          </>} />
        <Cell label="Timed out" loading={busy} evidence="chain"
          value={m ? (m.timeouts > 0 ? m.timeouts.toLocaleString("en-US") : "none") : "—"}
          tone={m && m.timeouts > 0 ? "fault" : "absent"}
          sub={m ? (m.timeouts > 0 ? `${tia(m.timed_out_utia)} charged` : "none reported") : undefined}
          detail={m && m.timeouts > 0 ? `${tia(m.timed_out_utia)} charged on abandoned promises, reported by ${m.timeout_processors} account${m.timeout_processors === 1 ? "" : "s"}.` : undefined}
          info={<>
            <p>Promises a publisher obtained signatures for and never settled, charged anyway once anyone submits the timeout; the chain pays nothing for doing so.</p>
            <p>This is a floor. A promise nobody reports leaves no trace on chain at all.</p>
          </>} />
        <Cell label="Settlement rate" loading={busy} evidence="chain"
          value={m?.settlement_rate.den ? fmtPct(m.settlement_rate) : "—"}
          tone={m?.settlement_rate.den ? undefined : "absent"}
          sub={m?.settlement_rate.den ? `${fmtCount(m.settlement_rate)} promises` : "nothing to rate"}
          info={<p>Settlements over settlements plus reported timeouts. Because unreported timeouts are invisible, this can only overstate how often publishers pay.</p>} />
        <Cell label="Escrow held" loading={busy} evidence="chain"
          value={m ? tia(m.escrow_total_utia ?? m.escrow_held_utia, { unit: false }) : "—"} unit={m ? "TIA" : undefined}
          tone={m && (m.escrow_total_utia ?? m.escrow_held_utia) === 0 ? "absent" : undefined}
          sub={m ? `${tia(m.deposits.utia)} deposited · ${tia(m.withdrawals_requested.utia)} requested out · ${tia(m.withdrawals_executed.utia)} paid out` : undefined}
          detail={m && m.withdrawals_requested.count > 0 ? `${m.withdrawals_requested.count.toLocaleString("en-US")} withdrawal request${m.withdrawals_requested.count === 1 ? "" : "s"} in the window, ${m.withdrawals_executed.count.toLocaleString("en-US")} paid out. A request pays out after the withdrawal delay; a settlement can shrink a queued request when the balance runs short, which the chain does not announce.` : undefined}
          info={<>
            {m?.escrow_total_utia != null
              ? <p>Every escrow on the chain: the balance of the x/fibre module account, which every deposit is paid into and every settlement, timeout and withdrawal is paid out of{m.escrow_total_at ? <>, read {ago(m.escrow_total_at)}</> : null}. {tia(m.escrow_held_utia)} of it belongs to the {m.escrow_accounts} account{m.escrow_accounts === 1 ? "" : "s"} this observer has seen publish.</p>
              : <p>The balance the chain holds for every publisher this observer has seen in a payment, read by state query, until the module account&rsquo;s total has been read.</p>}
            <p>Deposits, withdrawal requests and payouts are the window&rsquo;s.</p>
          </>} />
      </div>
      </Panel>

      {m && (() => {
        const days = calendar(m.window.start.startsWith("0001-") || m.window.name === "all"
          ? new Date((m.daily[0]?.day ?? m.window.end.slice(0, 10)) + "T00:00:00Z") : new Date(m.window.start), new Date(m.window.end));
        const byDay = new Map(m.daily.map((d) => [d.day, d]));
        const feeRows: Row[] = days.map((d) => {
          const b = byDay.get(d);
          return { x: d, label: d.slice(5), values: { fees: b?.fees_utia ?? 0 },
            note: b ? `${b.settlements} settlement${b.settlements === 1 ? "" : "s"} · ${bytes(b.bytes)}${b.timeouts ? ` · ${b.timeouts} timed out` : ""}` : "nothing settled" };
        });
        const pubs = m.top_publishers.map((p) => p.publisher);
        const series: Series[] = pubs.map((p, i) => ({ key: p, label: publisherName(m.top_publishers[i]), color: CATEGORICAL[i] }));
        if (m.other_publishers) series.push({ key: "", label: `${m.other_publishers.publishers} other`, color: OTHER_COLOR });
        const byteRows: Row[] = days.map((d) => {
          const values: Record<string, number> = {};
          for (const r of m.daily_by_publisher) if (r.day === d) values[r.publisher] = (values[r.publisher] ?? 0) + r.bytes;
          const b = byDay.get(d);
          for (const k of Object.keys(values)) values[k] = values[k] / (1 << 20); // MiB, so the axis steps are round
          return { x: d, label: d.slice(5), values, note: b ? `${b.settlements} settlement${b.settlements === 1 ? "" : "s"}` : "nothing settled" };
        });
        const mib = (v: number) => v >= 1024 ? `${(v / 1024).toFixed(2)} GiB` : v >= 100 ? `${Math.round(v)} MiB` : v >= 10 ? `${v.toFixed(1)} MiB` : `${v.toFixed(2)} MiB`;
        const axisTia = (v: number) => v === 0 ? "0" : v >= 100e6 ? Math.round(v / 1e6).toLocaleString("en-US") : v >= 1e6 ? (v / 1e6).toFixed(v % 1e6 ? 1 : 0) : (v / 1e6).toFixed(2);
        const axisMib = (v: number) => v >= 1024 ? `${(v / 1024).toFixed(v % 1024 ? 1 : 0)} GiB` : `${Number.isInteger(v) ? v : v.toFixed(1)} MiB`;
        return (
          <div className="charts">
            <div className="card">
              <Chart title="Fees settled per day (TIA)" series={[{ key: "fees", label: "fees", color: "var(--accent)" }]} rows={feeRows}
                fmt={(v) => tia(v)} fmtAxis={axisTia} />
            </div>
            <div className="card">
              <Chart title="Bytes settled per day, by publisher" series={series} rows={byteRows} fmt={mib} fmtAxis={axisMib} />
            </div>
          </div>
        );
      })()}

      <Panel title="Publishers" right={`${pubs.length} with an escrow movement in this window · sorted by fees`}>
      <div className="tablewrap">
        <table>
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
      </Panel>

    </>
  );
}
