package hosting

import "sort"

// Member is one registered Fibre host as the concentration summary sees it:
// the validator's stake and where its host's primary address was placed.
type Member struct {
	Validator string // hex address, for tie-breaking only
	Stake     int64
	Info      *Info // nil or Status "unresolved": not placed
}

// Bucket is one provider, country or AS in the summary.
type Bucket struct {
	Key        string  `json:"key"`
	Label      string  `json:"label,omitempty"`    // AS description for an AS bucket
	Provider   string  `json:"provider,omitempty"` // for an AS bucket: the provider it maps to
	Hosts      int     `json:"hosts"`
	HostShare  float64 `json:"host_share"`
	Stake      int64   `json:"stake"`
	StakeShare float64 `json:"stake_share"`
}

// Nakamoto is the smallest number of entities whose hosts together carry
// more than one third of the registered hosts' stake: the count of
// providers, networks or countries whose simultaneous failure would take
// more than a third of that stake's Fibre endpoints with them. Count is nil
// when the entities that could be identified never reach a third (too much
// is unresolved, or everything is spread across the remainder).
type Nakamoto struct {
	Count    *int     `json:"count"`
	Entities []string `json:"entities"`
	Share    float64  `json:"share"` // their combined stake share
	Note     string   `json:"note"`
}

// Summary is the network concentration block of /v1/hosting.
type Summary struct {
	// Registered is every validator with an open Fibre endpoint that the
	// caller passed; Resolved the ones placed in a network.
	Registered    int     `json:"registered_hosts"`
	Resolved      int     `json:"resolved_hosts"`
	Unresolved    int     `json:"unresolved_hosts"`
	TotalStake    int64   `json:"total_stake"`
	ResolvedStake int64   `json:"resolved_stake"`
	ResolvedShare float64 `json:"resolved_stake_share"`
	// Every share below is over ALL registered hosts (and their stake), the
	// unresolved included: a denominator that silently dropped them would
	// inflate every provider's share.
	ByProvider []Bucket `json:"by_provider"`
	ByCountry  []Bucket `json:"by_country"`
	ByASN      []Bucket `json:"by_asn"`
	Nakamoto   struct {
		Provider Nakamoto `json:"provider"`
		ASN      Nakamoto `json:"asn"`
		Country  Nakamoto `json:"country"`
	} `json:"nakamoto_third"`
	// Basis is "stake" normally, "hosts" when no stake is known (every
	// member has zero), in which case the Nakamoto counts are over hosts.
	Basis string `json:"basis"`
}

// Concentrate builds the summary. Pure: everything it needs is in members.
func Concentrate(members []Member) Summary {
	var s Summary
	s.Registered = len(members)
	for _, m := range members {
		s.TotalStake += max(m.Stake, 0)
	}
	s.Basis = "stake"
	if s.TotalStake == 0 {
		s.Basis = "hosts"
	}

	type agg struct {
		label, provider string
		hosts           int
		stake           int64
	}
	prov, ctry, asn := map[string]*agg{}, map[string]*agg{}, map[string]*agg{}
	add := func(m map[string]*agg, k string, st int64) *agg {
		a := m[k]
		if a == nil {
			a = &agg{}
			m[k] = a
		}
		a.hosts++
		a.stake += st
		return a
	}
	for _, m := range members {
		st := max(m.Stake, 0)
		in := m.Info
		if in == nil || in.Status == "unresolved" || in.IP == "" {
			s.Unresolved++
			add(prov, ProviderUnknown, st)
			add(ctry, "", st)
			continue
		}
		s.Resolved++
		s.ResolvedStake += st
		p := in.Provider
		if p == "" {
			p = ProviderFor(in.ASN)
		}
		add(prov, p, st)
		add(ctry, in.Country, st)
		k := ""
		if in.ASN != 0 {
			k = "AS" + itoa(int64(in.ASN))
		}
		a := add(asn, k, st)
		a.label, a.provider = in.ASOrg, p
	}
	if s.TotalStake > 0 {
		s.ResolvedShare = float64(s.ResolvedStake) / float64(s.TotalStake)
	}

	buckets := func(m map[string]*agg) []Bucket {
		out := make([]Bucket, 0, len(m))
		for k, a := range m {
			b := Bucket{Key: k, Label: a.label, Provider: a.provider, Hosts: a.hosts, Stake: a.stake}
			if s.Registered > 0 {
				b.HostShare = float64(a.hosts) / float64(s.Registered)
			}
			if s.TotalStake > 0 {
				b.StakeShare = float64(a.stake) / float64(s.TotalStake)
			}
			out = append(out, b)
		}
		sort.Slice(out, func(i, j int) bool {
			if weight(out[i], s.Basis) != weight(out[j], s.Basis) {
				return weight(out[i], s.Basis) > weight(out[j], s.Basis)
			}
			return out[i].Key < out[j].Key
		})
		return out
	}
	s.ByProvider, s.ByCountry, s.ByASN = buckets(prov), buckets(ctry), buckets(asn)

	// Other and Unknown are remainders, not entities: a hundred small
	// providers failing together is not a scenario, so neither may count
	// as one entity toward the third. The AS view is the finer measure
	// and has no remainder bucket except "no AS".
	s.Nakamoto.Provider = nakamoto(s.ByProvider, s.Basis, map[string]bool{ProviderOther: true, ProviderUnknown: true},
		"named providers only; Other (every AS not in the provider list) and Unknown are not single entities and are not counted")
	s.Nakamoto.ASN = nakamoto(s.ByASN, s.Basis, map[string]bool{"": true},
		"origin networks (AS numbers); hosts without a resolved AS are not counted")
	s.Nakamoto.Country = nakamoto(s.ByCountry, s.Basis, map[string]bool{"": true},
		"countries; hosts without a country are not counted")
	return s
}

func weight(b Bucket, basis string) float64 {
	if basis == "hosts" {
		return b.HostShare
	}
	return b.StakeShare
}

// nakamoto walks the buckets largest first, skipping the excluded keys,
// until their share exceeds one third.
func nakamoto(bs []Bucket, basis string, skip map[string]bool, note string) Nakamoto {
	n := Nakamoto{Entities: []string{}, Note: note}
	if basis == "hosts" {
		n.Note += "; counted over hosts because no stake is on record"
	}
	for _, b := range bs {
		if skip[b.Key] {
			continue
		}
		n.Entities = append(n.Entities, b.Key)
		n.Share += weight(b, basis)
		// Strictly more than a third, with a tolerance so that summing
		// shares like 1/3 in floating point cannot tip exactly-a-third over.
		if exceedsThird(n.Share) {
			c := len(n.Entities)
			n.Count = &c
			return n
		}
	}
	// Never reached: report what the identifiable entities add up to, and
	// no count, rather than a count that means something else.
	n.Entities = []string{}
	return n
}

func exceedsThird(share float64) bool { return share*3 > 1+1e-12 }

func itoa(i int64) string {
	if i == 0 {
		return "0"
	}
	var b [20]byte
	n := len(b)
	for i > 0 {
		n--
		b[n] = byte('0' + i%10)
		i /= 10
	}
	return string(b[n:])
}
