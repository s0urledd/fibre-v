"use client";
import { useState } from "react";
import Link from "next/link";
import { useApi, type Network, type Validator, type Meta, fmtCount, bytes, utc } from "@/lib/api";
import ValidatorTable from "@/components/ValidatorTable";
import { Reading } from "@/components/Rate";
import Graduation from "@/components/Graduation";
import { Mark } from "@/components/Verdict";

const WINDOWS = ["24h", "7d", "30d", "all"];

/**
 * The overview is one reading and its calibration.
 *
 * What used to be here was nine tiles of identical weight, in which the serve
 * rate, verdict coverage, attestation coverage and the probe count were
 * typographically indistinguishable — so the number the product exists to
 * publish looked exactly like the number describing how much of the population
 * it speaks for. The methodology figures now live on /methodology and on each
 * validator's page, with one summary line here, which is what a conventional
 * dashboard does and what the user asked for.
 *
 * The two things kept on this page that a conventional dashboard would not
 * keep: the definition of a fault, stated unconditionally, and the
 * correlated-failure warning. The first because a page that publishes
 * accusations has to say what one means on the page where it publishes them —
 * the old page hid that sentence inside a notice that only rendered when
 * something was held out, so on a clean network it never appeared at all. The
 * second because it is a correctness warning about this observer's own data,
 * not a methodology figure.
 */
export default function Overview() {
  const [win, setWin] = useState("24h");
  const { data: meta } = useApi<Meta>("/v1/meta");
  const { data: net, error: netErr, loading } = useApi<Network>(`/v1/network?window=${win}`);
  const { data: vals } = useApi<{ validators: Validator[] }>(`/v1/validators?window=${win}`);

  const noPubs = meta && meta.counts.Publications === 0;
  const faults = net?.classes?.FAULT ?? 0;
  const list = vals?.validators ?? [];
  const faulted = list.filter((v) => (v.classes.FAULT ?? 0) > 0).length;

  // The bar is a census of validators by their condition now, not a tally of
  // everything that ever happened to them. Counting a validator as unreachable
  // because one probe in a 24-hour window failed put 29 of 60 in that segment
  // on a network where two endpoints were actually down — which reads as a
  // broken network and is not what the data says. Each validator lands in
  // exactly one segment, worst first, so the segments sum to the population.
  const seg = {
    fault: faulted,
    hold: list.filter((v) => (v.classes.FAULT ?? 0) === 0 && v.reachable === false).length,
    gap: list.filter((v) => (v.classes.FAULT ?? 0) === 0 && v.reachable !== false && v.probe_count === 0).length,
    unproven: list.filter((v) => (v.classes.FAULT ?? 0) === 0 && v.reachable !== false && v.probe_count > 0 && v.serve_rate.den === 0).length,
  };
  const served = Math.max(0, list.length - seg.fault - seg.hold - seg.gap - seg.unproven);
  const pct = (n: number) => (list.length ? (n / list.length) * 100 : 0);

  return (
    <>
      <div className="range">
        <span className="label">Window</span>
        {WINDOWS.map((w) => (
          <button key={w} aria-pressed={win === w} onClick={() => setWin(w)}>{w}</button>
        ))}
        {net && (
          <span className="bounds sample">
            {net.window.start && !net.window.start.startsWith("0001-") ? utc(net.window.start) : "beginning"} → {utc(net.window.end)}
          </span>
        )}
      </div>

      {netErr && (
        <div className="plate-note">
          <span className="label">This observer</span>
          <p>Cannot reach the observer API: {netErr}. Nothing below is current.</p>
        </div>
      )}

      <section className="plate">
        <div className="plate-head">
          <span className="label">Serve rate</span>
          <span className="label">Faults</span>
        </div>
        <div className="plate-body">
          <div className="plate-main">
            <Reading r={net?.serve_rate} obligations={net?.serve_rate_by_obligation} loading={loading && !net} />
            <span className="sample">
              {net
                ? <>{fmtCount(net.serve_rate)} probes · {net.serve_rate_by_obligation.den.toLocaleString("en-US")} obligations</>
                : <>&nbsp;</>}
            </span>
            <p className="lead">
              Probes where the chain proves the validator stored the shard and this observer reached
              it. A fault is those two things and nothing less.{" "}
              <Link href="/methodology/#verdicts">How a verdict is reached</Link>
            </p>
          </div>
          <div className="plate-side">
            {/* Three states, not two. "none" is a claim — it says every probe
                that could have found a fault did not — and it may only be made
                when probes were actually rated. On a chain where Fibre is not
                active yet, or in a window with nothing measured, the honest
                answer is the same dash the reading gives, because "no faults"
                on zero evidence reads as "everything is fine" when what
                happened is that nothing happened. */}
            {!net ? (
              <p className="counter none"><span className="skel skel--sub" aria-hidden="true">00</span></p>
            ) : faults > 0 ? (
              <>
                <p className="counter">
                  <Mark tier="fault" />
                  {faults.toLocaleString("en-US")}
                </p>
                <span className="sample">across {faulted} validator{faulted === 1 ? "" : "s"}</span>
              </>
            ) : net.serve_rate.den > 0 ? (
              <>
                <p className="counter none">none</p>
                <span className="sample">no validator failed a shard it was proven to hold</span>
              </>
            ) : (
              <>
                <p className="counter none">—</p>
                <span className="sample">nothing rated in this window</span>
              </>
            )}
          </div>
        </div>
      </section>

      {net && (
        <p className="coverage">
          This rate speaks for {fmtCount(net.serve_rate_coverage)} in-window probes of an assigned shard.{" "}
          <Link href="/methodology/#what-this-excludes">What it leaves out →</Link>
        </p>
      )}

      {noPubs && (
        <div className="plate-note">
          <span className="label">Nothing to measure yet</span>
          <p>
            No Fibre publication recorded. Collector at height {meta.last_scanned_height || "?"}, following{" "}
            chain {meta.chain_id || "?"}.{" "}
            {meta.counts.OpenEndpoints === 0
              ? "No validator has registered a Fibre endpoint yet — x/valaddr is empty or not active on this chain."
              : `${meta.counts.OpenEndpoints} validators have registered a Fibre endpoint; reachability is probed every 10 minutes.`}
          </p>
        </div>
      )}

      {net?.vantage_health?.correlated && (
        <div className="plate-warn">
          <span className="label">This observer&rsquo;s own measurement</span>
          <p>
            At {utc(net.vantage_health.at)} ({net.vantage_health.label}), {fmtCount(net.vantage_health.worst_point)} of the
            validators probed at that moment were unreachable at once. Validators fail independently; one network does not.
            None of it counts against any validator, and the reachability figures below are not worth much until it is explained.
          </p>
        </div>
      )}

      {net && list.length > 0 && (
        <section className="bar-block">
          <span className="label">Validators</span>
          <span className="sample">
            {net.registered_endpoints} with a registered Fibre endpoint · {net.validators_probed} probed in this window
          </span>
          <div className="bar" role="img"
            aria-label={`${served} serving, ${seg.hold} unreachable now, ${seg.unproven} with nothing proven owed, ${seg.gap} not probed, ${seg.fault} faulted`}>
            {served > 0 && <i className="seg--served" style={{ width: `${pct(served)}%` }} />}
            {seg.hold > 0 && <i className="seg--hold" style={{ width: `${pct(seg.hold)}%` }} />}
            {seg.unproven > 0 && <i className="seg--unproven" style={{ width: `${pct(seg.unproven)}%` }} />}
            {seg.gap > 0 && <i className="seg--gap" style={{ width: `${pct(seg.gap)}%` }} />}
            {seg.fault > 0 && <i className="seg--fault" style={{ width: `${pct(seg.fault)}%` }} />}
          </div>
          <ul className="bar-key">
            <li><Mark tier="kept" /> <b>{served}</b> serving</li>
            <li><Mark tier="hold" /> <b>{seg.hold}</b> unreachable now</li>
            <li><Mark tier="held" /> <b>{seg.unproven}</b> nothing proven owed</li>
            <li><Mark tier="gap" /> <b>{seg.gap}</b> not probed</li>
            <li><Mark tier="fault" /> <b>{seg.fault}</b> faulted</li>
          </ul>
        </section>
      )}

      {net?.serve_rate_by_point && <Graduation points={net.serve_rate_by_point} />}

      {net && (
        <p className="coverage">
          {net.publications.toLocaleString("en-US")} publications in this window, {bytes(net.publication_bytes)} of padded upload.{" "}
          {net.reconstructable.recoverable.den > 0 && (
            <>
              Of {net.reconstructable.recoverable.den.toLocaleString("en-US")} examined,{" "}
              {fmtCount(net.reconstructable.recoverable)} could still be rebuilt from the rows that came back,
              and {fmtCount(net.reconstructable.rate)} had every validator the chain proves owed them answer.{" "}
            </>
          )}
          <Link href="/blobs/">Every publication →</Link>
        </p>
      )}

      <h2>Validators</h2>
      {vals
        ? <ValidatorTable rows={vals.validators}
            caption={`Every validator with a registered Fibre endpoint or at least one probe, over the ${win} window. Six further columns are on each validator's page.`} />
        : <p className="muted">Loading validators…</p>}
    </>
  );
}
