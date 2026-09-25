"use client";
import type { ReactNode } from "react";
import { type Validator, type EndpointCheck, type Meta, int, ago, whenUTC, dateUTC } from "@/lib/api";

/**
 * The first thing on a validator's page: what state its Fibre endpoint is in,
 * in plain words, and what the operator can do about it.
 *
 * The page below is evidence — stage marks, rates, probe rows — and an
 * operator who arrives from a link in a chat wants the conclusion first. That
 * conclusion is derived here from fields the API already publishes and from
 * nothing else: the chain's own words (jailed, bond status, x/valaddr host)
 * first, then the newest handshake stage by stage, then the certificate check,
 * then broken obligations. It never re-classifies a probe.
 *
 * Two rules the copy keeps:
 *
 * - It never states a fault the data does not show. Only a broken obligation
 *   is a fault, and only that line is ever red. Unreachable, a lapsed
 *   certificate or a missing registration are states, and the text says so,
 *   because from one location an unreachable endpoint can be this observer's
 *   own path.
 * - It says what was observed before what to do, and the advice is the
 *   general shape of the fix, pointing at the Celestia docs for the steps. A
 *   remote observer cannot see the operator's machine, and a confident
 *   specific instruction built on a guess would be worse than none.
 */

/** Celestia's own operator guide for the Fibre server; the anchors are its section ids. */
export const FIBRE_DOCS = "https://docs.celestia.org/operate/consensus-validators/fibre";
const REGISTER_DOCS = `${FIBRE_DOCS}#register-the-public-address`;
const TLS_DOCS = `${FIBRE_DOCS}#transport-security-tls`;
const TROUBLESHOOT_DOCS = `${FIBRE_DOCS}#troubleshoot-startup`;

/** identity reasons that mean "the right key, a lapsed window", as the API's identityStatus groups them */
const CLOCK_REASONS = new Set(["cert_not_yet_valid"]);

type Tone = "ok" | "hold" | "none";
type State = { tone: Tone; title: string; body: ReactNode; todo?: ReactNode };

/** host:port → [host, port]; a bare host keeps Fibre's default port */
function split(host: string): [string, string] {
  const m = /^\[?(.*?)\]?:(\d+)$/.exec(host);
  return m ? [m[1], m[2]] : [host, "7980"];
}

function Docs({ href, children }: { href: string; children: ReactNode }) {
  return <a href={href} rel="noopener noreferrer" target="_blank">{children}</a>;
}

function state(v: Validator, c: EndpointCheck | undefined, decided: number): State {

  if (v.jailed) {
    return {
      tone: "none", title: "Jailed: no checks while out of the bonded set",
      body: <>The chain has jailed this validator, which takes it out of the bonded provider list. Publishers do not upload to it and this observer does not contact its Fibre endpoint{v.last_host && <> (last registered <span className="mono">{v.last_host}</span>)</>}{v.endpoint_closed_at && <>, so no check here is newer than {whenUTC(v.endpoint_closed_at)}</>}.</>,
      todo: <>Shards it signed for before it was jailed are still owed until their retention windows end. Once it is unjailed and back in the bonded set, checks resume on their own.</>,
    };
  }
  if (v.bond_status && v.bond_status !== "BOND_STATUS_BONDED") {
    const word = v.bond_status.replace("BOND_STATUS_", "").toLowerCase();
    return {
      tone: "none", title: `${word[0].toUpperCase()}${word.slice(1)}: no checks while out of the bonded set`,
      body: <>Only bonded validators are in the Fibre provider list, so publishers do not upload to this one and this observer does not contact its endpoint.</>,
      todo: <>Shards it signed for while bonded are still owed until their retention windows end.</>,
    };
  }
  if (!v.host) {
    return {
      tone: "none", title: "No Fibre endpoint registered",
      body: <>x/valaddr has no Fibre host for this validator, so publishers have no address to upload its shards to and this observer has no endpoint to check: its probe rows read “No endpoint”. That is not a fault, and none of them counts against it.{v.last_host && <> The last host seen registered was <span className="mono">{v.last_host}</span>{v.endpoint_closed_at && <>, until {dateUTC(v.endpoint_closed_at)}</>}.</>}</>,
      todo: <>To register, run the Fibre server, then submit <code>celestia-appd tx valaddr set-host &lt;public-host&gt;:7980 --from &lt;validator-account-key&gt;</code> from the validator’s operator account. Checks start once the registration is on chain. Steps: <Docs href={REGISTER_DOCS}>Register the public address</Docs> in the Celestia docs.</>,
    };
  }
  const [name, port] = split(v.host);
  const host = <span className="mono">{v.host}</span>;
  if (v.reachable === null) {
    return {
      tone: "none", title: "Registered, not checked yet",
      body: <>{host} is registered in x/valaddr. This observer has not attempted a handshake with it yet; it checks every registered endpoint every five minutes.</>,
    };
  }
  if (v.reachable === false) {
    // The newest heartbeat, when it is about the host registered now. A check
    // against an older host says nothing about this one.
    const chk = c && c.host === v.host ? c : undefined;
    const at = chk ? <>At {whenUTC(chk.at)}</> : <>At the newest check</>;
    const err = chk?.raw_error ? <> (<code>{chk.raw_error}</code>)</> : null;
    const notFault = <>{v.also_failed_from && <> A check from a second location failed too.</>} Not counted as broken.{v.last_reachable_at ? <> The last successful handshake was {ago(v.last_reachable_at)}.</> : <> No handshake has succeeded in this period.</>}</>;
    const refused = chk && (chk.outcome === "TCP_REFUSED" || /refused/i.test(chk.raw_error ?? ""));
    if (chk && !chk.dns_ok) {
      return {
        tone: "hold", title: "The host name does not resolve",
        body: <>{at}, <span className="mono">{name}</span> returned no address to this observer{err}.{notFault}</>,
        todo: <>Check that the DNS record exists and is published on public DNS. A publisher resolving the same name is likely to fail the same way. Registering an IP address instead of a name (<code>set-host &lt;ip&gt;:{port}</code>) also works.</>,
      };
    }
    if (chk && !chk.tcp_ok && refused) {
      return {
        tone: "hold", title: `Connection refused on port ${port}`,
        body: <>{at}, the address answered but refused the connection on port {port}{err}: nothing was accepting connections there, or a firewall rejected it.{notFault}</>,
        todo: <>Check that the Fibre server is running, that it listens on port {port} on a public interface rather than only on 127.0.0.1, and that the firewall accepts inbound TCP on {port} from any address. <Docs href={TROUBLESHOOT_DOCS}>Troubleshoot startup</Docs> in the Celestia docs covers a server that does not come up.</>,
      };
    }
    if (chk && !chk.tcp_ok && (chk.outcome === "TCP_TIMEOUT" || /time(d)? ?out/i.test(chk.raw_error ?? ""))) {
      return {
        tone: "hold", title: `No answer on port ${port}`,
        body: <>{at}, the connection to {host} got no answer{err}: packets were dropped rather than refused.{notFault}</>,
        todo: <>This is usually a firewall or cloud security group that does not allow inbound TCP on port {port}, or a host that is down. The port has to be open to any address: publishers connect from anywhere.</>,
      };
    }
    if (chk && !chk.tcp_ok) {
      // no route, network unreachable and the like: neither refused nor timed out
      return {
        tone: "hold", title: `Could not connect on port ${port}`,
        body: <>{at}, the connection to {host} failed before it was accepted{err}.{notFault}</>,
        todo: <>Check that the host is up and that its address is reachable from the public internet on port {port}.</>,
      };
    }
    if (chk && !chk.tls_ok) {
      return {
        tone: "hold", title: "TCP connects, TLS does not",
        body: <>{at}, port {port} accepted the connection but the TLS handshake did not complete{err}.{notFault}</>,
        todo: <>Something other than the Fibre server may be listening on the port, or a proxy in front of it is not passing TLS through. The Fibre server terminates TLS itself, with a certificate endorsed by the validator’s consensus key: see <Docs href={TLS_DOCS}>Transport security</Docs> in the Celestia docs.</>,
      };
    }
    return {
      tone: "hold", title: "Unreachable at the newest check",
      body: <>{host} did not complete a TLS handshake with this observer at the newest check{v.last_seen_at && <> ({ago(v.last_seen_at)})</>}{err}.{notFault}</>,
    };
  }
  const reason = v.identity_reason ? <> (<code>{v.identity_reason}</code>)</> : null;
  if (v.identity_status === "expired") {
    return {
      tone: "hold", title: "Reachable, but the certificate endorsement has lapsed",
      body: <>{host} completes TLS, but the consensus-key endorsement in its certificate is outside its validity window{reason}. Fibre clients check that window and reject the certificate, so publishers will not upload to this server until it is renewed. This is not counted as a broken obligation.</>,
      todo: <>The Fibre server gets its certificate endorsed through the validator’s signing service when it starts. Check that its signing connection works{v.identity_reason && CLOCK_REASONS.has(v.identity_reason) ? " and that the server’s clock is right: the window has not started yet by this observer’s clock" : " and the host clock is right"}, then restart the server for a fresh endorsement. See <Docs href={TLS_DOCS}>Transport security</Docs>.</>,
    };
  }
  if (v.identity_status === "mismatch") {
    return {
      tone: "hold", title: "Reachable, but the certificate is not this validator’s",
      body: <>{host} completes TLS, but its certificate is not endorsed by this validator’s consensus key{reason}. Fibre clients check the endorsement against the validator set and reject this one, so publishers will not upload to this server. This is not counted as a broken obligation.</>,
      todo: <>Check that the Fibre server’s signing connection goes to this validator’s own signer (the one holding its consensus key) with the right chain ID, and that the registered host is this validator’s server and not another’s. See <Docs href={TLS_DOCS}>Transport security</Docs>.</>,
    };
  }
  if (v.identity_status !== "verified") {
    return {
      tone: "none", title: "Reachable; certificate not checked yet",
      body: <>{host} completed a TLS handshake {v.last_seen_at && ago(v.last_seen_at)}, but no consensus-key check was recorded on it.</>,
    };
  }
  if (v.confirmed_from) {
    return {
      tone: "hold", title: "Reachable from a second location only",
      body: <>The newest check from this observer’s main location did not complete a TLS handshake with {host}, but a check from a second location {v.last_seen_at ? ago(v.last_seen_at) : "within the last 15 minutes"} did, with a certificate endorsed by this validator’s consensus key. It counts as reachable.</>,
      todo: <>If some publishers cannot reach it either, look for firewall rules, geo-blocking or routing that treat source addresses differently: the port has to be open to any address.</>,
    };
  }
  const o = v.obligations;
  return {
    tone: "ok", title: "Reachable, and the certificate is this validator’s",
    body: <>{host} completed a TLS handshake with a certificate endorsed by this validator’s consensus key {v.last_seen_at ? ago(v.last_seen_at) : "at the newest check"}.{o && decided > 0 && o.broken === 0 && <> {int(o.served)} of {int(decided)} assessed obligation{decided === 1 ? "" : "s"} in this period {o.served === 1 ? "was" : "were"} served.</>}</>,
  };
}

export default function Diagnosis({ v, check, decided, provisional, failedShown, onShowFailed, failedHref }: {
  v: Validator;
  check?: EndpointCheck;
  meta?: Meta | null;
  /** served + broken obligations in the period */
  decided: number;
  /** broken obligations still settling (provisionalNow) */
  provisional: number;
  /** failed probe rows among the recent evidence on this page */
  failedShown: number;
  /** switch the evidence table to its failed rows and bring it into view */
  onShowFailed: () => void;
  /** every failed probe row of the period, in the API */
  failedHref: string;
}) {
  const s = state(v, check, decided);
  const broken = v.obligations?.broken ?? 0;
  const tone = broken > 0 && s.tone === "ok" ? "fault" : s.tone;
  return (
    <section className={"diag " + tone} aria-label="Endpoint status">
      <p className="diag-h"><i className={"dot " + (s.tone === "ok" ? "ok" : s.tone === "hold" ? "hold" : "none")} />{s.title}</p>
      <p>{s.body}</p>
      {s.todo && <p className="diag-do"><b>What to do.</b> {s.todo}</p>}
      {broken > 0 && (
        <p className="diag-fault">
          <span className="mk fault" /> <b>{int(broken)} broken obligation{broken === 1 ? "" : "s"} in this period</b>{provisional > 0 && <> ({int(provisional)} still settling)</>}: promises this validator signed for where it answered “not found” while it was still obliged to serve the shard.{" "}
          {failedShown > 0
            ? <a href="#evidence" onClick={onShowFailed}>Show the {int(failedShown)} failed probe row{failedShown === 1 ? "" : "s"} below →</a>
            : <>None of the newest probe rows below is one of them; <a href={failedHref}>the failed rows are in the API →</a></>}
        </p>
      )}
    </section>
  );
}
