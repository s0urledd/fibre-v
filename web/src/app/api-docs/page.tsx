import { API_BASE } from "@/lib/api";

export const metadata = { title: "API · Fibre observer" };

const ROUTES: [string, string][] = [
  ["GET /v1/meta", "chain id, collector height, pinned celestia-app commit, protocol-params fingerprint, row counts, latest collector and prober run with liveness, vantage count."],
  ["GET /v1/network?window=24h|7d|30d|all", "registered endpoints, validators probed, reachability, serve rate, verdict counts, probe count, gap count, publications and bytes, reconstructable count."],
  ["GET /v1/validators?window=…", "one row per validator with a registered endpoint or at least one probe: endpoint, reachability, identity status, serve rate with counts, voting power, assigned rows and load band."],
  ["GET /v1/validators/{addr}", "addr is the 40-hex consensus address or celestiavalcons1…; adds 24h/7d/30d serve rates and the 50 most recent probes."],
  ["GET /v1/blobs?limit=&namespace=&before_height=", "newest publications first with probe counts and the reconstructability verdict."],
  ["GET /v1/blobs/{promise_hash}", "the publication, params in force, per-validator assignment, and every probe."],
  ["GET /v1/probes?validator=&blob=&since=&class=&limit=", "raw probe rows, newest first, at most 1000 per call."],
  ["GET /v1/runs?window=…", "observer run spans (start, last heartbeat, stop) used to render gaps."],
];

export default function ApiDocs() {
  return (
    <>
      <h1>API</h1>
      <p className="muted">Read-only JSON, versioned under <code>/v1/</code>. Base URL for this deployment: <code>{API_BASE}</code>. CORS is open; responses carry <code>Cache-Control: public, max-age=15</code>. No authentication, no rate limit yet; be reasonable.</p>
      <div className="tablewrap">
        <table>
          <caption>Routes</caption>
          <thead><tr><th>route</th><th>returns</th></tr></thead>
          <tbody>{ROUTES.map(([r, d]) => <tr key={r}><td className="mono">{r}</td><td style={{ whiteSpace: "normal" }}>{d}</td></tr>)}</tbody>
        </table>
      </div>
      <h2>Conventions</h2>
      <ul>
        <li>Every rate is an object <code>{"{num, den, value}"}</code>; <code>value</code> is null when <code>den</code> is 0. Never a bare percentage.</li>
        <li>Timestamps are RFC 3339 UTC. Identifiers are lower-case hex; consensus addresses are the 20-byte address in hex, with the bech32 form where the registry knows it.</li>
        <li>Windows are fixed lookbacks from the server's clock; the response echoes <code>window.start</code> and <code>window.end</code>.</li>
        <li>Classes are the prober's classification strings (see Methodology). <code>NOT_PROBED</code> and <code>PROBE_ERROR</code> are gaps and are never in a rate.</li>
        <li>The underlying files are also plain: <code>publications.jsonl</code>, <code>measurements.jsonl</code> and <code>reachability.jsonl</code> are append-only records the store ingests; the SQLite file can be copied and queried directly.</li>
      </ul>
    </>
  );
}
