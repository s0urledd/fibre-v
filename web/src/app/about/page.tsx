export const metadata = { title: "About · Fibre observer" };

export default function About() {
  return (
    <>
      <h1>About</h1>
      <p>An independent observer for Celestia Fibre (CIP-51). It reads what it needs from the chain, recomputes the rest, and probes validators' registered Fibre endpoints from outside as an ordinary client. Nothing here trusts a validator's self-report.</p>
      <h2>Who runs it</h2>
      <p>[OPERATOR NAME], from vantage [CITY, PROVIDER, ASN]. Not affiliated with the Celestia Foundation or Celestia Labs. The operator also runs a Celestia validator; its row is labelled on the overview and included in every headline number, and every number can be recomputed without it from the API.</p>
      <h2>Source and reproducibility</h2>
      <p>Everything is open source under Apache-2.0: <a href="https://github.com/plsgiveup/fibre">github.com/plsgiveup/fibre</a>. The README lists one command per claim: the TLS golden vectors, the assignment differential test against celestia-app, the prober's unit tests, and a devnet run with fault injection (60 measurements, zero misclassifications). The celestia-app commit the assignment constants are pinned to is printed in the footer.</p>
      <h2>Data</h2>
      <ul>
        <li>Raw probe rows are kept indefinitely and are available through the API (<code>/v1/probes</code>). Each row keeps the original measurement record.</li>
        <li>Probe load follows the published policy (see Methodology). Validators who want to be moved to reachability-only probing can write to [CONTACT]; the change is recorded and shown.</li>
        <li>No cookies, no analytics, no third-party requests from this page.</li>
      </ul>
      <h2>Limitations</h2>
      <ul>
        <li>One vantage. See the banner on every page.</li>
        <li>Before Fibre activates on this chain there are no publications, and the site shows exactly that.</li>
        <li>Validator monikers and staking data are not shown yet; rows are keyed by consensus address. Use the explorer link for the rest.</li>
      </ul>
    </>
  );
}
