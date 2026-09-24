"use client";
import { Fragment } from "react";
import { bytes, shortHex, span, useApi, utc } from "@/lib/api";
import type { ParamEntry, Params } from "@/lib/withdrawals";

/**
 * The x/fibre parameters this site computes every deadline from, and the
 * protocol constants it pins, from /v1/params. Lives on the methodology page
 * because that is where every rule that uses them is defined:
 * must_serve_until is creation + max(payment_promise_timeout,
 * shard_retention), and the assignment floor and row counts decide who owes
 * which rows. Nothing here is typed into the page; the chain values come
 * from the observer's store, the constants from the pinned code.
 */

const LABEL: Record<string, string> = {
  withdrawal_delay: "withdrawal delay",
  payment_promise_timeout: "payment promise timeout",
  payment_promise_height_window: "promise height window",
  shard_retention: "shard retention",
  full_stake_storage_budget: "full-stake storage budget",
};

function values(e: ParamEntry) {
  return [
    ["shard_retention", span(e.shard_retention_s)],
    ["payment_promise_timeout", span(e.payment_promise_timeout_s)],
    ["withdrawal_delay", span(e.withdrawal_delay_s)],
    ["payment_promise_height_window", `${e.payment_promise_height_window.toLocaleString("en-US")} blocks`],
    ["full_stake_storage_budget", bytes(e.full_stake_storage_budget_bytes)],
  ] as const;
}

function since(e: ParamEntry): string {
  const at = e.effective_from_time ? ` · ${utc(e.effective_from_time)}` : " · time not read";
  const where = `height ${e.effective_from_height.toLocaleString("en-US")}${e.effective_from_tx_index >= 0 ? `, after tx ${e.effective_from_tx_index - 1}` : ""}`;
  return e.source === "seed" ? `in force at ${where}, where the record begins${at}` : `from ${where}${at}`;
}

export default function ProtocolParams() {
  const { data: p, error } = useApi<Params>("/v1/params", 300000);
  if (!p) return <p className="muted">{error ? `The observer API is not answering (${error}); the values are at /v1/params.` : "Loading…"}</p>;
  const c = p.current;
  const k = p.protocol;
  return (
    <>
      {c ? (
        <>
          <p><strong>On chain, now</strong> <span className="muted">({since(c)})</span></p>
          <dl className="kv">
            {values(c).map(([key, v]) => (
              <Fragment key={key}><dt>{LABEL[key]}</dt><dd className="mono">{v}{c.changed.includes(key) && <span className="chip" style={{ marginLeft: 8 }}>changed here</span>}</dd></Fragment>
            ))}
            {p.derived && <><dt>serving window</dt><dd className="mono">{span(p.derived.must_serve_window_s)} <span className="muted">= max(timeout, retention), from each promise&rsquo;s creation</span></dd></>}
          </dl>
          {p.history.length > 1 && (
            <div className="tablewrap">
              <table>
                <thead><tr><th>from height</th><th>block time (UTC)</th><th>source</th><th>changed</th></tr></thead>
                <tbody>
                  {[...p.history].reverse().map((e) => (
                    <tr key={`${e.effective_from_height}-${e.effective_from_tx_index}`}>
                      <td className="mono">{e.effective_from_height.toLocaleString("en-US")}</td>
                      <td className="mono">{e.effective_from_time ? utc(e.effective_from_time) : <span className="faint">not read</span>}</td>
                      <td>{e.source === "seed" ? "start of record" : e.source === "finalize" ? "end of block (governance)" : "transaction"}</td>
                      <td>{e.changed.length ? e.changed.map((f) => {
                        const v = values(e).find(([key]) => key === f);
                        return `${LABEL[f] ?? f} → ${v ? v[1] : "?"}`;
                      }).join(", ") : <span className="faint">—</span>}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
          {p.history.length <= 1 && <p className="muted">No change recorded since the record began.</p>}
        </>
      ) : (
        <p className="muted">No x/fibre parameters on record yet: the scanner seeds them the first time it reads a block where Fibre exists.</p>
      )}
      <p><strong>Pinned in code</strong> <span className="muted">(celestia-app <span className="mono">{shortHex(k.pinned_celestia_app_commit, 6)}</span>; exposed by no RPC, so read from the build this site runs)</span></p>
      <dl className="kv">
        <dt>rows</dt><dd className="mono">{k.original_rows.toLocaleString("en-US")} original + {k.parity_rows.toLocaleString("en-US")} parity = {k.total_rows.toLocaleString("en-US")}</dd>
        <dt>max blob size</dt><dd className="mono">{bytes(k.max_blob_size_bytes)} <span className="muted">· rows are {bytes(k.min_row_size_bytes)} or wider, up to {bytes(k.max_row_size_bytes)}</span></dd>
        <dt>rows per validator</dt><dd className="mono">{k.min_rows_per_validator} to {k.max_rows_per_validator.toLocaleString("en-US")}</dd>
        <dt>thresholds</dt><dd className="mono">liveness {k.liveness_threshold} · safety {k.safety_threshold}</dd>
        <dt>param bounds</dt><dd className="mono">{Object.entries(k.param_bounds).map(([name, b]) => `${LABEL[name] ?? name} ${b.min_s != null ? span(b.min_s) : "0"}–${span(b.max_s)}`).join(" · ")}</dd>
        <dt>assignment pin</dt><dd className="mono">{shortHex(k.assignment_fingerprint, 6)} {k.fingerprint_matches == null ? <span className="muted">· scanner has not reported one</span> : k.fingerprint_matches ? <span className="muted">· the scanner runs the same pin</span> : <span className="err">· the scanner reports a different pin</span>}</dd>
      </dl>
    </>
  );
}
