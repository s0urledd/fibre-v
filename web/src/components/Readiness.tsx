import Link from "next/link";
import { type Validator, int } from "@/lib/api";

/**
 * Can Fibre accept a blob at all? A MsgPayForFibre settles only with the
 * signatures of validators holding two thirds of voting power
 * (attested >= floor(total * 2 / 3), x/fibre), and a validator can sign only
 * if its Fibre server is up and registered. So until hosts carrying two
 * thirds of stake answer, nobody can publish, and every other figure on the
 * site stays empty for that reason rather than for want of publishers.
 *
 * "Reachable" is this observer's newest handshake with the registered host
 * (TCP, TLS and the validator's key): necessary for signing, not proof of it.
 * The figure is a readiness gauge, not a promise that an upload would pass.
 */
export default function Readiness({ rows }: { rows: Validator[] }) {
  const bonded = rows.filter((v) => !v.jailed && (!v.bond_status || v.bond_status === "BOND_STATUS_BONDED"));
  const total = bonded.reduce((s, v) => s + (v.voting_power || 0), 0);
  if (total === 0) return null;
  const registered = bonded.filter((v) => !!v.host);
  const reachable = registered.filter((v) => v.reachable === true);
  const regPower = registered.reduce((s, v) => s + (v.voting_power || 0), 0);
  const reachPower = reachable.reduce((s, v) => s + (v.voting_power || 0), 0);
  const quorum = Math.floor((total * 2) / 3);
  const ready = reachPower >= quorum;
  const pct = (n: number) => `${((100 * n) / total).toFixed(1)}%`;
  const w = (n: number) => `${Math.min(100, (100 * n) / total)}%`;
  const missing = bonded
    .filter((v) => !v.host)
    .sort((a, b) => (b.voting_power || 0) - (a.voting_power || 0))
    .slice(0, 5);

  return (
    <section className="band readiness" aria-labelledby="readiness-h">
      <div>
        <h2 id="readiness-h">Can Fibre accept blobs?</h2>
        <p className="ready-answer">
          {ready
            ? <><b>Yes.</b> Reachable hosts hold {pct(reachPower)} of stake, above the ⅔ a blob needs.</>
            : <><b>Not yet.</b> Reachable hosts hold {pct(reachPower)} of stake; a blob needs ⅔ ({pct(quorum)}) to settle.</>}
        </p>
        <div className="meter ready-meter" role="img"
          aria-label={`${pct(reachPower)} of stake reachable, ${pct(regPower)} registered, ${pct(quorum)} needed`}>
          <i style={{ width: w(reachPower) }} />
          <b className="reg" style={{ left: w(reachPower), width: `calc(${w(regPower)} - ${w(reachPower)})` }} />
          <span className="tick" style={{ left: w(quorum) }} />
          <span className="tl2" style={{ left: w(quorum) }}>⅔ needed</span>
        </div>
        <p className="sub ready-key">
          <span><i className="sw s" /> reachable {pct(reachPower)} · {int(reachable.length)} validators</span>
          <span><i className="sw reg" /> registered, not answering {pct(regPower - reachPower)} · {int(registered.length - reachable.length)}</span>
          <span><i className="sw p" /> no Fibre host {pct(total - regPower)} · {int(bonded.length - registered.length)}</span>
        </p>
      </div>
      {missing.length > 0 && (
        <div>
          <h2>Largest validators without a Fibre host</h2>
          <ol className="ready-missing">
            {missing.map((v) => (
              <li key={v.address}>
                <Link href={`/validator/?addr=${encodeURIComponent(v.cons_address || v.address)}`}>{v.moniker || v.operator_address || v.address}</Link>
                <span className="num">{pct(v.voting_power || 0)}</span>
              </li>
            ))}
          </ol>
        </div>
      )}
    </section>
  );
}
