import { Legend } from "@/components/Badge";

export const metadata = { title: "Methodology · Fibre observer" };

export default function Methodology() {
  return (
    <>
      <h1>Methodology</h1>
      <p className="muted">What is measured, from where, and what each word on this site means. The source of truth is <code>docs/verdicts.md</code> and <code>docs/research/R4-probe-etiquette.md</code> in the repository; this page restates them.</p>

      <h2 id="promise">The promise being checked</h2>
      <p>When a client publishes a blob to Fibre, the validators sign that they received their assigned rows and the chain records the payment (<code>MsgPayForFibre</code>). Each signing validator then owes serving of its rows to anyone who asks until <code>must_serve_until = creation_timestamp + max(payment_promise_timeout, shard_retention)</code>, using the x/fibre parameters in force when the blob settled. The chain does not observe whether serving continues. This site does, from outside, as an ordinary client.</p>

      <h2 id="probe">One probe</h2>
      <p>A probe is one attempt to fetch one validator's shard for one blob: resolve the registered host (x/valaddr), open TCP, complete a TLS 1.3 handshake, check that the certificate's extension is endorsed by the validator's consensus key (<code>fibre-tlsverify</code>), call <code>DownloadShard</code>, verify every returned row against the blob commitment, and check that the returned row indices are exactly the ones assigned (<code>fibre-assign</code>, a bit-identical reimplementation of celestia-app's assignment, verified across about 890 differential scenarios). Each layer is timed and recorded separately.</p>

      <h2 id="schedule">Schedule</h2>
      <p>For every publication the prober plans four in-window points at 12%, 45%, 72% and 92% of the window, packed toward the deadline where early pruning shows, one grace point 30 s after <code>must_serve_until</code>, and one post point 3.5 min after it where "not found" is the expected answer. An honest server prunes on a one-minute loop with minute-resolution keys, so a shard outlives the deadline by about 1 to 2 minutes; the grace tolerance (2m30s) exists so that lag is never called a fault.</p>

      <h2 id="verdicts">Verdicts</h2>
      <p>Each probe gets exactly one class from the probe outcome, the phase of the window at the actual start time, and whether the validator was assigned the shard.</p>
      <Legend classes={["HEALTHY", "FAULT", "TOLERATED", "EXPECTED_GONE", "SERVED_PAST_WINDOW", "UNREACHABLE_POST_WINDOW", "EXPECTED_UNASSIGNED", "SERVING_UNASSIGNED", "PROBE_ERROR", "NOT_PROBED"]} />
      <p><strong>FAULT is the only class that counts against a validator.</strong> It means: while the promise held, the validator answered not found, was unreachable at any layer, presented a certificate its consensus key did not endorse, or returned wrong, partial or unverifiable rows.</p>

      <h2 id="rates">Rates</h2>
      <ul>
        <li><strong>Serve rate</strong> = HEALTHY / (HEALTHY + FAULT), over probes of assigned shards in the in-window and grace phases. TOLERATED, EXPECTED_GONE, post-window classes and gaps are listed next to the rate, never folded in.</li>
        <li><strong>Reachable</strong> = the latest heartbeat or probe of the endpoint completed TCP and TLS. Heartbeats dial every registered endpoint every 10 minutes without downloading, so the column stays live on days a validator has nothing assigned.</li>
        <li><strong>TLS identity</strong> = the consensus-key check on the latest handshake: verified, mismatch, no TLS, or unreachable.</li>
        <li><strong>Reconstructable</strong> (per blob) = at the latest in-window probe point, the union of row indices of validators whose probe was HEALTHY is at least <code>OriginalRows</code> (4096 of 16384 for blob version 0). "Degraded" means enough rows but not every assigned validator answered. Grace and post points are never used for this verdict.</li>
        <li>Every rate shows its numerator and denominator. Below 30 probes the Wilson 95% lower bound is printed too, because 3 of 3 is not 3000 of 3000.</li>
      </ul>

      <h2 id="gaps">Gaps</h2>
      <p>NOT_PROBED and PROBE_ERROR rows are the observer's own gaps: the slot elapsed while the observer was down, the probe policy sampled the blob out, or the probe itself failed. They are counted and shown, and never enter a rate. Observer run spans are recorded (<code>/v1/runs</code>), so downtime is rendered as a gap, not as zero.</p>

      <h2 id="sampling">Load policy and sampling</h2>
      <p>A probe transfers the validator's whole shard; there is no partial-row download in the protocol. To stay a small fraction of a validator's capacity, the prober enforces per-validator caps (requests per minute, bytes per hour and per day, scaled by assigned rows) and global caps. When the projected load for the next hour would exceed a cap, publications are sampled at probability <em>p</em> = cap / projected. The sample is deterministic and unpredictable: a blob is probed iff <code>H(promise_hash || day_secret) &lt; p · 2^64</code>, where <code>day_secret = HMAC(master, date)</code>. A commitment to each day's secret can be published in advance and the secret revealed afterwards, so anyone can audit that the sample was unbiased. A sampled-in blob gets its whole schedule; a sampled-out blob is recorded NOT_PROBED with the reason and <em>p</em>. Backoff only ever removes the download step after repeated transport failures; it never adds requests.</p>

      <h2 id="vantage">Vantage</h2>
      <p>All measurements on this site come from the vantage named in the footer. A failed probe means this vantage could not fetch the rows at that time; it is not proof the validator is down. A successful probe is not proof of availability from elsewhere. Adding vantages multiplies load on validators, so a second one comes after the policy above has been published.</p>

      <h2 id="not">What this site does not do</h2>
      <ul>
        <li>It is not an availability proof and produces no slashing evidence; Fibre has no on-chain penalty for not serving.</li>
        <li>It does not extrapolate: rows not probed are not counted as served.</li>
        <li>It does not rank validators by anything other than the stored probe rows you can download from the API.</li>
      </ul>
    </>
  );
}
