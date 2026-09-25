package hosting

import "sort"

// Provider buckets. A bucket is a normalised name for "the AS that announced
// this address belongs to this company", chosen for the questions a
// delegator actually asks: the Foundation Delegation Program names Hetzner
// and OVH, and the large clouds are where concentration usually hides.
// Everything else is Other, which is a heterogeneous remainder rather than
// one entity (the concentration summary treats it that way), and an address
// with no usable lookup is Unknown.
const (
	ProviderHetzner      = "Hetzner"
	ProviderOVH          = "OVH"
	ProviderAWS          = "AWS"
	ProviderGCP          = "Google Cloud"
	ProviderAzure        = "Azure"
	ProviderDigitalOcean = "DigitalOcean"
	ProviderContabo      = "Contabo"
	ProviderVultr        = "Vultr"
	ProviderAkamaiLinode = "Akamai/Linode"
	ProviderCherry       = "Cherry Servers"
	ProviderMevspace     = "MEVSPACE"
	ProviderScaleway     = "Scaleway"
	ProviderLeaseweb     = "Leaseweb"
	ProviderVelia        = "velia.net"
	ProviderGTHost       = "GTHost"
	ProviderLatitude     = "Latitude.sh"
	ProviderTeraswitch   = "TeraSwitch"
	ProviderAruba        = "Aruba"
	ProviderOther        = "Other"
	ProviderUnknown      = "Unknown"
)

// providerASNs maps origin AS numbers to a bucket.
//
// Matching is by AS number only, never by the AS description: descriptions
// are free text and a substring match ("AMAZON") catches Brazilian ISPs named
// after the river, which the iptoasn data really does contain. Every number
// below was checked against the AS description in iptoasn.com's
// ip2asn-combined.tsv of 2026-09-24 (quoted in the comment) and can be
// re-checked at https://bgp.he.net/AS<number> or
// https://www.peeringdb.com/asn/<number>. The list is deliberately short:
// an AS that is left out lands in Other, which understates a provider's
// share rather than inventing one. Additions belong here with the same kind
// of evidence.
var providerASNs = map[uint32]string{
	// Hetzner Online GmbH: the dedicated/colo AS and the Hetzner Cloud ASes.
	24940:  ProviderHetzner, // HETZNER-AS
	213230: ProviderHetzner, // HETZNER-CLOUD2-AS
	212317: ProviderHetzner, // HETZNER-CLOUD3-AS
	215859: ProviderHetzner, // HETZNER-CLOUD4-AS

	// OVH SAS (OVHcloud).
	16276: ProviderOVH, // OVH
	35540: ProviderOVH, // OVH-TELECOM

	// Amazon Web Services. AS16509 carries EC2 and most AWS space; AS14618
	// is the original us-east-1 AS; AS8987 is GovCloud. AS7224 (AMAZON-AS)
	// is Amazon's corporate network and is left out on purpose.
	16509: ProviderAWS, // AMAZON-02
	14618: ProviderAWS, // AMAZON-AES
	8987:  ProviderAWS, // AWS-GOVCLOUD

	// Google. AS396982 is Google Cloud Platform's own AS; a good part of
	// Compute Engine's older external address space is still announced by
	// AS15169, which cannot be told apart from other Google services by AS
	// number alone. For a Fibre host (a server someone runs) Google Cloud is
	// the only plausible reading.
	396982: ProviderGCP, // GOOGLE-CLOUD-PLATFORM
	15169:  ProviderGCP, // GOOGLE
	19527:  ProviderGCP, // GOOGLE-2

	// Microsoft: AS8075 announces Azure's public address space.
	8075: ProviderAzure, // MICROSOFT-CORP-MSN-AS-BLOCK

	// DigitalOcean.
	14061:  ProviderDigitalOcean, // DIGITALOCEAN-ASN
	393406: ProviderDigitalOcean, // DIGITALOCEAN-AS393406

	// Contabo GmbH and Contabo Asia.
	51167:  ProviderContabo, // CONTABO
	40021:  ProviderContabo, // CONTABO-40021
	141995: ProviderContabo, // CAPL-AS-AP Contabo Asia Private Limited

	// Vultr (formerly Choopa).
	20473: ProviderVultr, // AS-VULTR
	46407: ProviderVultr, // AS-VULTR3

	// Linode, now Akamai Connected Cloud. Akamai's CDN ASes (AS20940 and
	// friends) are not here: a Fibre host behind them would be a proxy, and
	// calling that Akamai hosting would be a guess.
	63949: ProviderAkamaiLinode, // AKAMAI-LINODE-AP Akamai Connected Cloud

	// Added 2026-09-25 from what Mocha's Fibre hosts resolved to after
	// activation. With Hetzner and OVH out of favour (the delegation program
	// excludes them), stake had moved to providers this list did not name:
	// 92% of registered hosts read as "Other" while two Cherry Servers ASes
	// alone carried about a third of registered stake, a concentration the
	// panel could not show. Same evidence rule as above: every description
	// is iptoasn's, file of 2026-09-24.
	16125: ProviderCherry, // CHERRYSERVERS1-AS
	59642: ProviderCherry, // CHERRYSERVERS2-AS
	204770: ProviderCherry, // CHERRYSERVERS3-AS
	216444: ProviderCherry, // CHERRYSERVERS4-AS
	214159: ProviderCherry, // CHERRYSERVERS5-AS
	213896: ProviderCherry, // CHERRYSERVERS6-AS

	201814: ProviderMevspace, // MEVSPACE

	// Online SAS is Scaleway's operating company (formerly Online.net).
	12876: ProviderScaleway, // Online SAS

	// Leaseweb runs one AS per region.
	60781:  ProviderLeaseweb, // LEASEWEB-NL-AMS-01 Netherlands
	28753:  ProviderLeaseweb, // LEASEWEB-DE-FRA-10
	205544: ProviderLeaseweb, // LEASEWEB-UK-LON-11
	30633:  ProviderLeaseweb, // LEASEWEB-USA-WDC
	7203:   ProviderLeaseweb, // LEASEWEB-USA-SFO
	59253:  ProviderLeaseweb, // LEASEWEB-APAC-SIN-11 LEASEWEB SINGAPORE PTE. LTD.

	29066: ProviderVelia, // VELIANET-AS velia.net Internetdienste GmbH
	30083: ProviderVelia, // AS-30083-US-VELIA-NET

	// GTHost (Global Telehost) announces from two ASes under one name.
	63023: ProviderGTHost, // AS-GLOBALTELEHOST
	62563: ProviderGTHost, // AS-GLOBALTELEHOST

	396356: ProviderLatitude,   // LATITUDE-SH
	20326:  ProviderTeraswitch, // TERASWITCH
	31034:  ProviderAruba,      // ARUBA-ASN
}

// ProviderFor returns the bucket for an origin AS number. 0 (no AS, or not
// routed) is Unknown; any AS not in the list is Other.
func ProviderFor(asn uint32) string {
	if asn == 0 {
		return ProviderUnknown
	}
	if p, ok := providerASNs[asn]; ok {
		return p
	}
	return ProviderOther
}

// Providers lists every named bucket, in display order, then Other and
// Unknown. The UI uses the same order for its legend.
func Providers() []string {
	return []string{
		ProviderHetzner, ProviderOVH, ProviderAWS, ProviderGCP, ProviderAzure,
		ProviderDigitalOcean, ProviderContabo, ProviderVultr, ProviderAkamaiLinode,
		ProviderCherry, ProviderMevspace, ProviderScaleway, ProviderLeaseweb,
		ProviderVelia, ProviderGTHost, ProviderLatitude, ProviderTeraswitch, ProviderAruba,
		ProviderOther, ProviderUnknown,
	}
}

// ProviderASNs returns the mapping as sorted (asn, provider) pairs, for the
// API to publish next to the figures it produced: a reader checking a
// bucket should not have to read Go.
func ProviderASNs() []ASNProvider {
	out := make([]ASNProvider, 0, len(providerASNs))
	for a, p := range providerASNs {
		out = append(out, ASNProvider{ASN: a, Provider: p})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Provider != out[j].Provider {
			return out[i].Provider < out[j].Provider
		}
		return out[i].ASN < out[j].ASN
	})
	return out
}

// ASNProvider is one entry of the published mapping.
type ASNProvider struct {
	ASN      uint32 `json:"asn"`
	Provider string `json:"provider"`
}
