// Package hosting answers "whose network is this Fibre host on, and in which
// country", for every registered Fibre endpoint, from local database files
// only.
//
// # Why
//
// Celestia's Foundation Delegation Program asks delegation recipients not to
// run their infrastructure on Hetzner or OVH, and delegators in general care
// how concentrated the set is: if a third of the stake's Fibre hosts sit in
// one provider's network, one provider's bad day is the network's bad day.
// The chain says nothing about where a host runs. This package is the one
// place the observer says something about it, and it says it carefully.
//
// # What it reads
//
// Nothing over the network, ever. The addresses come from what the
// reachability heartbeat already resolved and recorded (the DNS step's
// detail on each reachability row, see probe.StepResult), so this package
// neither resolves names nor contacts anything. The IP-to-network mapping
// comes from files the operator downloads ahead of time
// (deploy/hosting-db.sh):
//
//   - iptoasn.com's ip2asn-combined.tsv.gz: IPv4 and IPv6 ranges to origin
//     AS number, AS description and the AS's registry country. Public Domain
//     (Open Data Commons PDDL v1.0), https://iptoasn.com/. Required: without
//     it the feature is off.
//   - DB-IP's IP to Country Lite CSV (dbip-country-lite-YYYY-MM.csv.gz):
//     ranges to the country the address is estimated to be used in.
//     CC BY 4.0, https://db-ip.com/db/download/ip-to-country-lite, and the
//     licence requires the attribution line the site prints ("IP Geolocation
//     by DB-IP"). Optional: without it the country is the AS registry's
//     country, and the API says so (country_basis).
//   - DB-IP's IP to City Lite CSV (dbip-city-lite-YYYY-MM.csv.gz): ranges
//     to country, state/province, city and the city's approximate
//     coordinates. Same licence and credit, https://db-ip.com/db/download/ip-to-city-lite.
//     Optional: without it the hosts are placed by country only and the
//     city fields are absent.
//
// All are looked up by streaming the file once per lookup pass against the
// handful of addresses in question, so the collector holds no table in
// memory between passes. A missing ASN file turns the feature off; the pass
// then clears whatever an earlier configuration stored, so the API never
// serves a lookup nobody can reproduce any more.
//
// # What it does not claim
//
// Everything here is "as resolved from this vantage". A DNS name can resolve
// differently elsewhere (GeoDNS), a proxy or a tunnel hides the machine
// behind it, an anycast prefix is announced from many places, and a
// geolocation database is an estimate. The provider bucket is a mapping from
// the origin AS number, with the list and its sources in providers.go; it is
// a statement about whose network announced the address, not about who owns
// the machine. No verdict is drawn from any of it.
package hosting
