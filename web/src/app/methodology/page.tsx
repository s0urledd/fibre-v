import { Legend } from "@/components/Verdict";
import { DISPUTE_URL } from "@/lib/site";
import ProtocolParams from "@/components/ProtocolParams";

// The rules version, as verdict.MethodologyVersion in the Go code and
// methodology_version in /v1/meta and every export manifest. Bumped in the
// same change as any rule that can move a figure.
const METHODOLOGY_VERSION = "2026-09-25.2";

export const metadata = { title: "Methodology · Tensile · Celestia Fibre" };

export default function Methodology() {
  return (
    <div className="prose">
      <h1>Methodology</h1>
      <p className="muted">What Tensile measures and what each word on the site means. Version <b>{METHODOLOGY_VERSION}</b>, published with every figure as <code>methodology_version</code>.</p>

      <h2 id="why">What Tensile adds to the chain</h2>
      <p>The chain records who registered a Fibre host (<code>x/valaddr</code>), every <code>MsgPayForFibre</code> with its signatures, and the <code>x/fibre</code> parameters. It records nothing about service afterwards: whether an endpoint answers, whether a validator still serves a shard it signed for, or whether it pruned early. There is no serving proof and no slashing for not serving. Tensile checks that from outside, as an ordinary client, and publishes every row it bases a figure on.</p>

      <h2 id="promise">The obligation</h2>
      <p>A validator that signs a settled promise owes its assigned rows to anyone who asks until <code>must_serve_until = creation_timestamp + max(payment_promise_timeout, shard_retention)</code>, using the parameters in force when the blob settled.</p>

      <h2 id="params">Protocol parameters</h2>
      <p>Read from chain state and every <code>EventUpdateFibreParams</code>; protocol constants come from the pinned celestia-app build. Served at <code>/api/v1/params</code>.</p>
      <ProtocolParams />

      <h2 id="probe">One probe</h2>
      <p>Resolve the registered host, open TCP, complete TLS 1.3, check the certificate is endorsed by the validator&rsquo;s consensus key, call <code>DownloadShard</code>, verify every row against the blob commitment, and check the rows are exactly the assigned ones (<code>fibre-assign</code>, bit-identical to celestia-app). Each step is timed and recorded.</p>

      <h2 id="schedule">Schedule</h2>
      <p>Four probe points inside the window, at 12%, 45%, 72% and 92%, with the last moved to no more than 2m30s before the deadline. A grace point follows 30 s after the deadline and a post point 3.5 min after it. A shard missing at grace is tolerated: servers prune on a one-minute loop.</p>

      <h2 id="verdicts">Who is obliged, and what a fault is</h2>
      <p>A validator is obliged only if the settled promise carries its signature, because a Fibre server stores the shard before it signs. Tensile verifies every signature itself against the consensus key. Validators check them in <code>CheckTx</code> and <code>ProcessProposal</code>, but <code>validateValidatorSignatures</code> stops once two thirds of the stake has verified, so later entries are never checked by any node.</p>
      <p>A missing endorsement is <em>unproven</em>, never a failure: the publisher stops collecting at two thirds of stake, so on any blob about a third of the set has no signature on chain by design. Those probes are <strong>not endorsed</strong> and sit outside the rate in both directions.</p>
      <p><strong>FAULT is the only class that counts against a validator</strong>: from an endpoint with this validator&rsquo;s certificate, a shard it signed for was reported missing, or its bytes did not verify, while the obligation held. Unreachable, server errors, rate limits and certificate problems each have their own class and never count. A fault cannot rule out a power loss on the server; if that is what happened, the <a href={DISPUTE_URL} rel="noopener noreferrer" target="_blank">dispute route</a> puts the correction on the record.</p>
      <Legend />

      <h2 id="signing">Endorsements</h2>
      <p>A validator <em>endorses</em> a payment promise by signing it after storing its shard. <strong>Endorsed ⅔</strong> is the settled promises carrying a validator&rsquo;s verified endorsement over the promises that assigned it rows: how often it made the two-thirds quorum. The Blobs page shows each promise&rsquo;s endorsed share of stake against the quorum of <code>floor(total &times; 2 / 3)</code>. Neither is a duty: a missing endorsement is unproven, not a fault.</p>

      <h2 id="rates">Rates</h2>
      <ul>
        <li><strong>Service rate</strong> is counted per obligation (one validator, one shard): <em>kept</em> if no probe faulted and a probe in the last quarter of the window returned the shard, <em>broken</em> if any probe faulted. The rate is kept over kept plus broken, always shown with its counts, and not ranked below twenty obligations.</li>
        <li><strong>Not in the rate</strong>, counted beside it: unattested, unreachable, no registered host, lapsed or wrong certificate, server errors, rate limits, rows belonging to another promise over the same blob, and obligations whose end of window was not observed.</li>
        <li><strong>Provisional:</strong> a fault younger than 30 minutes is counted but marked, because it can still be withdrawn (a probe point that turns out to be an observer-wide failure, or the second location clearing it).</li>
        <li><strong>Reachability</strong> is completed handshakes over attempts, one every five minutes per endpoint. <strong>Flaky</strong> means one failed check after a success, still counted as reachable; two in a row is unreachable.</li>
        <li><strong>Blob availability</strong>: at the latest in-window point, do the rows that came back cover the rows needed to rebuild the blob (4096 of 16384 for version 0).</li>
        <li><strong>Throughput</strong> is the median bytes per second of the download step over healthy probes. No threshold is attached.</li>
        <li>Every rate is a ratio of sums, never an average of rates, and carries a 95% upper bound on the fault rate drawn around the obligation count.</li>
      </ul>

      <h2 id="gaps">Gaps</h2>
      <p>When Tensile could not probe (downtime, sampling, its own errors), the row is <strong>NOT_PROBED</strong> or <strong>PROBE_ERROR</strong>: shown, never a zero, never in a rate. A probe point where at least half the validators failed at once is treated as an observer-side problem and left out of every figure. Blocks the observer&rsquo;s node could not read are listed as scan gaps. Process health is at <code>/api/v1/health</code>.</p>

      <h2 id="sampling">Load and sampling</h2>
      <p>A probe downloads the whole shard, so Tensile caps its load per validator and overall. Above the cap, blobs are sampled at probability <em>p</em>, decided by <code>H(promise_hash || day_secret) &lt; p · 2^64</code>. Each day&rsquo;s secret is committed in advance at <code>/api/v1/sampling</code> and revealed seven days later, so anyone can check which blobs should have been probed. A sampled-out blob is recorded once, with its probability, and counts as not probed for every assigned validator at every point. After three failures or rate limits in a row a validator&rsquo;s downloads pause for twenty minutes; the skipped slots are recorded as not probed.</p>

      <h2 id="publishers">Publishers and fees</h2>
      <ul>
        <li>Every figure on the Publishers page is read from the chain: settlements, reported timeouts, escrow deposits, withdrawals and balances.</li>
        <li><strong>Fees</strong> are recomputed with the module&rsquo;s formula, <code>(650,000 + 45,000 × ⌈size / 256 KiB⌉)</code> gas at one utia per gas, since no event carries the amount.</li>
        <li><strong>Timeouts</strong> are a floor: only timeouts someone submitted are on chain.</li>
        <li id="withdrawals"><strong>Withdrawals</strong> are read from chain state with each escrow balance, because settlements can draw on queued withdrawals without emitting an event.</li>
      </ul>

      <h2 id="evidence">Evidence behind each figure</h2>
      <p>Every headline figure is tagged <strong>chain</strong> (recorded on chain), <strong>verified</strong> (bytes or certificates Tensile checked) or <strong>observed</strong> (what Tensile&rsquo;s network saw). Every snapshot names the block it was computed through (<code>record_through</code>). Daily exports are signed, <code>/api/v1/exports</code>, and <code>sentinel-recompute</code> re-derives every verdict and figure from them.</p>

      <h2 id="vantage">Two locations</h2>
      <p>Endpoints are checked every five minutes from two locations on two continents. A host counts as unreachable only when both fail; if the second location reaches it, it counts as reachable and its page says so. Retention faults are re-checked from the second location within twenty minutes: if the rows come back and verify there, the fault is withdrawn and filed as an observer-side failure, never counted as served. Rates over a period use the observer&rsquo;s own checks only. The locations are listed in <code>/api/v1/meta</code>.</p>

      <h2 id="not">What Tensile does not do</h2>
      <ul>
        <li>It is not an availability proof and produces no slashing evidence.</li>
        <li>It does not extrapolate: rows not probed are not counted as served.</li>
        <li>It ranks validators only by stored probe rows anyone can download from the API.</li>
      </ul>
    </div>
  );
}
