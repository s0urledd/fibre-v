"use client";
import { Suspense, useState } from "react";
import Link from "next/link";
import { API_BASE, useApi, type Network, type Validator, type Meta, type Market, int, pctOf, bytes, undecided, MIN_RATED } from "@/lib/api";
import { useWindow, WindowSwitch, windowLabel } from "@/lib/window";
import StatusLine from "@/components/StatusLine";
import { Metric, Metrics } from "@/components/Metrics";
import OutcomeBar from "@/components/OutcomeBar";
import VolumeChart from "@/components/VolumeChart";
import Validators from "@/components/Validators";
import PreLive from "@/components/PreLive";
import { Concentration } from "@/components/Hosting";

/**
 * The overview: the network's obligations over the selected period, the
 * endpoints answering now, what was settled, and the validators. Every
 * figure maps to one field of /v1/network, /v1/validators or /v1/market;
 * the definitions are on the methodology page, one disclosure away.
 */
function Overview() {
  const [win, setWin] = useWindow("24h");
  const { data: meta, error: metaErr } = useApi<Meta>("/v1/meta");
  const net = useApi<Network>(`/v1/network?window=${win}`);
  const vals = useApi<{ validators: Validator[] }>(`/v1/validators?window=${win}`);
  const market = useApi<Market>("/v1/market?window=7d");
  const [disc, setDisc] = useState<"" | "outcomes" | "blobs">("");
  const [about, setAbout] = useState(false);

  const N = net.data;
  const rows = vals.data?.validators ?? [];
  const notLive = !!meta?.app_version && !meta.fibre_active;
  const o = N?.obligations;
  const decided = o ? o.served + o.broken : 0;
  const und = undecided(o);
  const brokenOn = rows.filter((v) => (v.obligations?.broken ?? 0) > 0).length;
  const reach = N?.reachability;
  const rc = N?.reconstructable;
  const judged = rc ? rc.yes + rc.degraded + rc.no : 0;
  const measuring = !!N && !notLive && !!o && o.total > 0 && decided < MIN_RATED && o.pending > 0;
  const period = windowLabel(win).toLowerCase() === "all" ? "all time" : `${win}`;
  // Fibre host registration among the bonded set, by count and by stake:
  // the first thing to watch after activation, and the ceiling on every
  // other figure, since a validator with no host cannot be probed at all.
  const reg = (() => {
    const bonded = rows.filter((v) => !v.jailed && (!v.bond_status || v.bond_status === "BOND_STATUS_BONDED"));
    const withHost = bonded.filter((v) => !!v.host);
    const power = bonded.reduce((s, v) => s + (v.voting_power || 0), 0);
    const hosted = withHost.reduce((s, v) => s + (v.voting_power || 0), 0);
    return { count: withHost.length, of: bonded.length, share: power > 0 ? pctOf(hosted, power) : "—" };
  })();

  return (
    <>
      <div className="title">
        {/* The product is the label and the claim is the headline; the network is already in the header's chip. */}
        <div><p className="eyebrow">Celestia Fibre</p><h1>Independent checks that validators serve the data they signed for.</h1><p className="lede"><button type="button" className="dis" aria-expanded={about} onClick={() => setAbout(!about)}>What is measured?</button></p>
          <p className="disc about" hidden={!about}>
            Fibre is Celestia's low-latency data path. Each validator signs for its share of a blob and must keep serving that share until the retention window ends; the chain records the signature, never the serving. Tensile downloads the shares from outside, as any client would, checks every row against the on-chain commitment, and publishes each result with the evidence behind it. An endpoint it cannot reach is never counted as a failure. <Link href="/methodology/">Methodology →</Link>
          </p></div>
        <WindowSwitch value={win} onChange={setWin} />
      </div>
      <PreLive meta={meta} />
      <StatusLine meta={meta} metaError={metaErr} snap={N} client={{ error: net.error, fetchedAt: net.fetchedAt, status: net.status }} measuring={measuring} />

      <Metrics>
        <Metric label="Service rate"
          value={!N || notLive || !o || o.total === 0 || decided === 0 ? "—" : pctOf(o.served, decided)}
          tone={!N || notLive || !o || decided === 0 ? "absent" : undefined}
          help={!N || notLive ? " " : !o || o.total === 0 ? "no obligation in this period" : decided === 0 ? "awaiting results" : `${int(o.served)} / ${int(decided)} assessed`} />
        <Metric label="Broken obligations"
          value={!N || notLive ? "—" : int(o?.broken ?? 0)}
          tone={!N || notLive ? "absent" : (o?.broken ?? 0) > 0 ? "fault" : undefined}
          help={!N || notLive ? " " : (o?.broken ?? 0) > 0 ? `on ${int(brokenOn)} validator${brokenOn === 1 ? "" : "s"}` : "none in this period"} />
        <Metric label="Undecided"
          value={!N || notLive ? "—" : int(und)}
          tone={!N || notLive ? "absent" : undefined}
          help={!N || notLive ? " " : `${int(o?.pending ?? 0)} pending`} />
        <Metric label="Hosts registered"
          value={!vals.data ? "—" : int(reg.count)}
          den={vals.data && reg.of > 0 ? int(reg.of) : undefined}
          tone={!vals.data || reg.count === 0 ? "absent" : undefined}
          title="Bonded validators with a Fibre host in x/valaddr, and the share of bonded voting power they hold. A validator without one cannot serve Fibre data."
          help={!vals.data ? " " : reg.count === 0 ? (notLive ? " " : "none yet")
            : <>{reg.share} of stake{reach && reach.den > 0 ? <> · {int(reach.num)} reachable now</> : null}</>} />
        <Metric label="Settled data"
          value={!N || notLive ? "—" : bytes(N.publication_bytes)}
          tone={!N || notLive || N.publications === 0 ? "absent" : undefined}
          help={!N || notLive ? " " : `${int(N.publications)} publication${N.publications === 1 ? "" : "s"} · ${period}`} />
      </Metrics>

      <section className="band" id="outcomes">
        <div>
          <h2>Obligation outcomes</h2>
          <OutcomeBar o={o} absent={!N || notLive} />
          <p className="blobs">
            <button type="button" className="dis" aria-expanded={disc === "outcomes"} onClick={() => setDisc(disc === "outcomes" ? "" : "outcomes")}>About these outcomes</button>
            <span className="sep">·</span>
            <button type="button" className="dis" aria-expanded={disc === "blobs"} onClick={() => setDisc(disc === "blobs" ? "" : "blobs")}>Blob availability →</button>
            {rc && judged > 0 && <span className="soft">{int(rc.yes)} / {int(judged)} fully served</span>}
          </p>
          <p className="disc" hidden={disc !== "outcomes"}>
            {o && o.total > 0
              ? <>The bar shows all <b>{int(o.total)}</b> obligations settled in the period — one per validator and blob it signed for. The service rate counts only the <b>{int(decided)}</b> assessed ones (served + broken); undecided (no reading at the end of the window {int(o.end_unobserved)}, never observed serving {int(o.unobserved)}{(o.held_param_unverified ?? 0) > 0 && <>, deadline unverified {int(o.held_param_unverified)}</>}) and pending stay outside it. Unreachable from here is never counted as broken.</>
              : <>No obligation settled in this period. An obligation is one validator and one blob it signed for; it is decided by the last probe before the retention deadline.</>}
            {" "}<Link href="/methodology/#rates">Methodology →</Link>
          </p>
          <p className="disc" hidden={disc !== "blobs"}>
            {rc && judged > 0
              ? <>Fully served <b>{int(rc.yes)} / {int(judged)}</b> judged blobs: every validator proven to hold a shard served its rows at the latest complete probe point. Rebuildable <b>{int(rc.yes + rc.degraded)} / {int(judged)}</b>: enough distinct rows were observed to rebuild the blob — an observation of rows, not an actual rebuild.{rc.pending > 0 && <> {int(rc.pending)} still in window.</>}{rc.unknown > 0 && <> {int(rc.unknown)} not judged.</>}</>
              : rc && rc.pending > 0 ? <>No blob judged yet: <b>{int(rc.pending)}</b> still inside the retention window.</>
              : <>No blob judged in this period.</>}
            {" "}Full breakdown in <Link href="/blobs/">Blobs →</Link>
          </p>
        </div>
        <div>
          <h2>Settled volume · 7 days</h2>
          <p className="sub">UTC days · last day partial · padded size as charged</p>
          <VolumeChart market={market.data} />
        </div>
      </section>

      <Concentration />

      <Validators rows={rows} window={win} notLive={notLive} loading={vals.loading} />
      <p className="tnote"><a href={`${API_BASE}/v1/feed.atom`} type="application/atom+xml">Network events (Atom)</a> · host registrations, bonded-list changes, first faults and observer incidents, for any feed reader. Each validator has its own feed on its page.</p>
      {notLive && meta && <p className="tnote">Fibre is not live on {meta.chain_id}: the chain runs app v{meta.app_version}{meta.fibre_app_version ? ` and Fibre needs v${meta.fibre_app_version}` : ""}. “Signalled” is x/signal’s word on whether the validator has signalled for the version that brings Fibre.</p>}
    </>
  );
}

export default function Page() {
  return <Suspense fallback={<div className="title"><div><p className="eyebrow">Celestia Fibre</p><h1>Independent checks that validators serve the data they signed for.</h1></div></div>}><Overview /></Suspense>;
}
