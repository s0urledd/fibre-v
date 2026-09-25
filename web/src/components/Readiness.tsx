import Link from "next/link";
import { type Validator } from "@/lib/api";

/**
 * Can Fibre accept a blob at all? A MsgPayForFibre settles only with the
 * signatures of validators holding two thirds of voting power
 * (attested >= floor(total * 2 / 3), x/fibre), and a validator can sign only
 * if its Fibre server is up and registered. So until hosts carrying two
 * thirds of stake answer, nobody can publish, and every other figure on the
 * site stays empty for that reason rather than for want of publishers.
 *
 * "Ready" is this observer's newest handshake with the registered host: it
 * answered TLS with a certificate endorsed by the validator's consensus key.
 * A host that answers with any other certificate is not counted, because a
 * publisher's client will not upload to it. Necessary for signing, not proof
 * of it: a readiness gauge, not a promise that an upload would pass.
 */

/** the endpoint's standing, from the API's endpoint_state when it sends one */
export type EndpointState = "reachable" | "flaky" | "unreachable" | "none";
type WithState = Validator & { endpoint_state?: EndpointState };

export function endpointState(v: Validator): EndpointState {
  const s = (v as WithState).endpoint_state;
  if (s === "reachable" || s === "flaky" || s === "unreachable" || s === "none") return s;
  if (!v.host) return "none";
  if (v.reachable === true) {
    // Answering now, but missing a noticeable share of the window's handshakes.
    const w = v.reachability_window;
    return w && w.den >= 12 && w.value !== null && w.value < 0.9 ? "flaky" : "reachable";
  }
  return "unreachable";
}

/** answering now, whatever the certificate */
export function answering(v: Validator): boolean {
  const s = endpointState(v);
  return s === "reachable" || s === "flaky";
}

/** answering with the validator's own certificate: what the ⅔ figure counts */
export function ready(v: Validator): boolean {
  return answering(v) && v.identity_status === "verified";
}

export function bondedOf(rows: Validator[]): Validator[] {
  return rows.filter((v) => !v.jailed && (!v.bond_status || v.bond_status === "BOND_STATUS_BONDED"));
}

export function readiness(rows: Validator[]) {
  const bonded = bondedOf(rows);
  const total = bonded.reduce((s, v) => s + (v.voting_power || 0), 0);
  const registered = bonded.filter((v) => !!v.host);
  const reachable = registered.filter(ready);
  const regPower = registered.reduce((s, v) => s + (v.voting_power || 0), 0);
  const reachPower = reachable.reduce((s, v) => s + (v.voting_power || 0), 0);
  const quorum = Math.floor((total * 2) / 3);
  const missing = bonded
    .filter((v) => !v.host)
    .sort((a, b) => (b.voting_power || 0) - (a.voting_power || 0))
    .slice(0, 5);
  const pct = (n: number) => (total > 0 ? `${((100 * n) / total).toFixed(1)}%` : "—");
  return { bonded, total, registered, reachable, regPower, reachPower, quorum, ready: total > 0 && reachPower >= quorum, missing, pct };
}

/** the answer, the meter with its ⅔ tick, and the key */
export function ReadyAnswer({ rows, headingId = "readiness-h" }: { rows: Validator[]; headingId?: string }) {
  const r = readiness(rows);
  if (r.total === 0) return null;
  const { pct, reachPower, regPower, quorum, total } = r;
  const w = (n: number) => `${Math.min(100, (100 * n) / total)}%`;
  return (
    <>
      <h2 id={headingId}>Fibre quorum</h2>
      <p className="ready-answer" title="Ready: a registered host that answers TLS with a certificate endorsed by its validator's consensus key. A blob settles with signatures from ⅔ of stake.">
        <b>{r.ready ? "Reached" : "Not reached"}</b> · {pct(reachPower)} of stake ready, ⅔ needed
      </p>
      <div className="meter ready-meter" role="img"
        aria-label={`${pct(reachPower)} of stake ready, ${pct(regPower)} registered, ${pct(quorum)} needed`}>
        <i style={{ width: w(reachPower) }} />
        <b className="reg" style={{ left: w(reachPower), width: `calc(${w(regPower)} - ${w(reachPower)})` }} />
        <span className="tick" style={{ left: w(quorum) }} />
        <span className="tl2" style={{ left: w(quorum) }}>⅔ needed</span>
      </div>
    </>
  );
}

/** the five largest validators with no Fibre host */
export function ReadyMissing({ rows }: { rows: Validator[] }) {
  const r = readiness(rows);
  if (r.total === 0 || r.missing.length === 0) return null;
  return (
    <>
      <h2>Largest validators without a Fibre host</h2>
      <ol className="ready-missing">
        {r.missing.map((v) => (
          <li key={v.address}>
            <Link href={`/validator/?addr=${encodeURIComponent(v.cons_address || v.address)}`}>{v.moniker || v.operator_address || v.address}</Link>
            <span className="num">{r.pct(v.voting_power || 0)}</span>
          </li>
        ))}
      </ol>
    </>
  );
}

export default function Readiness({ rows }: { rows: Validator[] }) {
  const r = readiness(rows);
  if (r.total === 0) return null;
  return (
    <section className="band readiness" aria-labelledby="readiness-h">
      <div><ReadyAnswer rows={rows} /></div>
      {r.missing.length > 0 && <div><ReadyMissing rows={rows} /></div>}
    </section>
  );
}
