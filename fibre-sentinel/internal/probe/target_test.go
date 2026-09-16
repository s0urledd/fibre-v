package probe

import (
	"testing"
	"time"
)

// A validator that leaves the bonded set keeps its Fibre registration on
// chain, and its retention obligation comes from the promise it signed rather
// than from its bonding status. Dropping its host on the first jailing would
// stop the evidence about a server that may well still be serving, and would
// do it silently.
func TestHostFor_FallsBackToTheLastRegisteredHost(t *testing.T) {
	r := NewResolver(nil, time.Minute)
	seen := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	r.lastKnown["aa"] = knownHost{host: "a.example:9090", at: seen}
	r.lastKnown["bb"] = knownHost{host: "b.example:9090", at: seen}

	bonded := map[string]string{"aa": "a.example:9090"}

	host, source, at := r.hostFor(bonded, "aa")
	if host != "a.example:9090" || source != "bonded" {
		t.Fatalf("bonded validator resolved to %q via %q", host, source)
	}
	if !at.Equal(seen) {
		t.Fatalf("bonded host seen at %s, want %s", at, seen)
	}

	// bb has been jailed or is unbonding: gone from the bonded list, still
	// registered on chain, still probed.
	host, source, at = r.hostFor(bonded, "bb")
	if host != "b.example:9090" {
		t.Fatalf("an unbonded validator lost its host: %q", host)
	}
	if source != "last_known" {
		t.Fatalf("host source %q, want last_known so the record says where it came from", source)
	}
	if !at.Equal(seen) {
		t.Fatalf("last-known host seen at %s, want %s", at, seen)
	}

	// a validator that never registered has no host, and that is a different
	// statement from "we stopped looking".
	if host, source, _ = r.hostFor(bonded, "cc"); host != "" || source != "" {
		t.Fatalf("a never-registered validator resolved to %q via %q", host, source)
	}
}

// Both caches must evict oldest-first rather than emptying themselves. A
// wholesale reset throws away exactly the entries the fallback exists for,
// and costs a round trip per height still in flight at the busiest moment.
func TestResolverCachesEvictOldestFirst(t *testing.T) {
	r := NewResolver(nil, time.Minute)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < maxLastKnownHosts+500; i++ {
		r.lastKnown[string(rune('a'+i%26))+time.Duration(i).String()] = knownHost{
			host: "h", at: base.Add(time.Duration(i) * time.Second),
		}
	}
	newestKey := string(rune('a'+(maxLastKnownHosts+499)%26)) + time.Duration(maxLastKnownHosts+499).String()
	r.evictLastKnown()
	if len(r.lastKnown) > maxLastKnownHosts {
		t.Fatalf("last-known map grew to %d, bound is %d", len(r.lastKnown), maxLastKnownHosts)
	}
	if len(r.lastKnown) == 0 {
		t.Fatal("eviction emptied the map instead of trimming it")
	}
	if _, ok := r.lastKnown[newestKey]; !ok {
		t.Fatal("the most recently confirmed host was evicted")
	}
}
