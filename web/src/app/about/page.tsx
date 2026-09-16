export const metadata = { title: "About · Fibre observer" };

export default function About() {
  return (
    <>
      <h1>About</h1>
      <p>An independent observer for Celestia Fibre (CIP-51). It reads what it needs from the chain, recomputes the rest, and probes validators&rsquo; registered Fibre endpoints from outside as an ordinary client. Nothing here trusts a validator&rsquo;s self-report.</p>
      <h2>Why it exists</h2>
      <p>Fibre records retention obligations on chain and never checks them. There is no serving proof, no challenge and no slashing: the protocol rests on an honest majority, and the Celestia core team has said in as many words that the final layer is social consensus and that the community is expected to build the dashboards that check whether registered hosts are reachable and whether validators are serving. This is that. Where there is no enforcement, a public and checkable record is the enforcement &mdash; which is an argument for making the record harder, not looser: <a href="/methodology/#why">what the chain cannot tell you</a>.</p>
      <h2>Who runs it</h2>
      <p>Huginn Tech, run by Gokay and Utku. Not affiliated with the Celestia Foundation or Celestia Labs. The operator also runs a Celestia validator. It is included in every headline number like any other validator, and every number can be recomputed without it from the public API (filter by consensus address). Where this watches from is published in <code>/v1/meta</code> under <code>vantage_info</code>, with each field labelled by what you can actually check. The egress addresses are the anchor: if you run a Fibre server, the connections you see from this site come from those addresses and from nowhere else. The autonomous system follows from them through public routing data, so you can confirm it without taking our word. The provider usually follows from the autonomous system. The city is the only field nothing proves, because geolocating an address is a guess. The autonomous system is the one that matters most in practice: if your network de-peers or rate-limits ours, every probe from here fails while you serve everyone else normally, and knowing which network we sit on is how you tell that apart from a problem of your own. One vantage means every reachability and serve-rate observation in this dataset is from a single network path: a route, firewall or peering problem between here and a validator is not distinguishable from a problem at the validator.</p>
      <h2>Probe load</h2>
      <p>The read path has no rate limiting yet, and the core team has said it is coming. This observer is built to be the kind of traffic such a limit should be able to recognise and let through: it probes only publications that exist on chain, only the validators that publication actually assigns rows to, on a schedule derived from the publication&rsquo;s own retention window, under published per-validator and global caps, sampling deterministically when a cap would be exceeded. Every one of those properties is checkable from chain state alone, which is what distinguishes a verifier from a scraper. The reasoning is written up at <a href="https://forum.celestia.org/t/rate-limiting-fibres-read-path-without-ip-allowlists/2295" rel="nofollow noopener noreferrer" target="_blank">rate limiting Fibre&rsquo;s read path without IP allowlists</a>. If a limit lands that this traffic does not fit, the policy here changes, not the limit.</p>
      <h2>Source and reproducibility</h2>
      <p>Everything is open source under Apache-2.0: <a href="https://github.com/plsgiveup/fibre">github.com/plsgiveup/fibre</a>. The README lists one command per claim: the TLS golden vectors, the assignment differential test against celestia-app, the prober's unit tests, and a devnet run with fault injection (60 measurements, zero misclassifications). The celestia-app commit the assignment constants are pinned to is printed in the footer.</p>
      <h2>Data</h2>
      <ul>
        <li>Raw probe rows are kept indefinitely and are available through the API (<code>/v1/probes</code>). Each row keeps the original measurement record.</li>
        <li>Probe load follows the published policy (see Methodology). A validator operator who wants to be probed less, or not at all, can ask by opening an issue at <a href="https://github.com/plsgiveup/fibre">github.com/plsgiveup/fibre</a>; the people running this site will honour that request. There is no machine-readable opt-out register yet, so no such request is recorded or shown anywhere in the API today. Until one exists, a validator that blocks this site's traffic will simply show up as UNREACHABLE rather than FAULT — and UNREACHABLE is deliberately kept out of the serve rate (see API conventions).</li>
        <li>Monikers, operator addresses and bond status come from the chain&rsquo;s own staking module rather than from an explorer&rsquo;s index, so nothing here inherits a third party&rsquo;s coverage, rate limits or terms. Every row is still keyed by consensus address, which is what everything joins on.</li>
        <li>No cookies, no analytics, no third-party requests from this page.</li>
      </ul>
      <h2>Limitations</h2>
      <ul>
        <li>One vantage. See the banner on every page.</li>
        <li>Before Fibre activates on this chain there are no publications, and the site shows exactly that.</li>
        <li>Probes are timed but not judged: throughput is published with no threshold, because from one location part of every millisecond is this site&rsquo;s own path.</li>
      </ul>
    </>
  );
}
