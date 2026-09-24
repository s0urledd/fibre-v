package feed

import (
	"encoding/xml"
	"strings"
	"testing"
	"time"
)

var t0 = time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)

// beats builds a heartbeat series five minutes apart from a pattern:
// U up/verified, D down, E up/expired, M up/mismatch, ? up/unjudged.
func beats(pattern string) []Beat {
	var out []Beat
	for i, c := range pattern {
		b := Beat{At: t0.Add(time.Duration(i) * 5 * time.Minute), Host: "h:1"}
		switch c {
		case 'U':
			b.Reachable, b.Identity = true, "verified"
		case 'E':
			b.Reachable, b.Identity = true, "expired"
		case 'M':
			b.Reachable, b.Identity = true, "mismatch"
		case '?':
			b.Reachable = true
		}
		out = append(out, b)
	}
	return out
}

func kinds(evs []Event) string {
	var s []string
	for _, e := range evs {
		s = append(s, e.Kind)
	}
	return strings.Join(s, ",")
}

func TestTransitions(t *testing.T) {
	cases := []struct {
		pattern, want string
	}{
		{"UUUUUU", ""},                         // steady: nothing
		{"UUUDUUU", ""},                        // one dropped handshake is not an outage
		{"UUUDDUUU", ""},                       // nor two
		{"UUUDDDUUU", "unreachable,recovered"}, // three are
		{"DDDDUUU", "recovered"},               // down at the start is the baseline; recovery is news
		{"DDDDUU", ""},                         // recovery not yet confirmed
		{"UUUEEEUUU", "identity,identity-restored"},
		{"UUUEEEMMM", "identity,identity"}, // expired turning into a mismatch is a new fact
		{"UUUDDDDEEE", "unreachable,recovered,identity"},
		{"UUU???UUU", ""}, // unjudged beats neither start nor end an episode
		{"", ""},
	}
	for _, c := range cases {
		if got := kinds(Transitions(beats(c.pattern), 3)); got != c.want {
			t.Errorf("%q: got %q want %q", c.pattern, got, c.want)
		}
	}
	// dated at the first beat of the confirming run, not the confirming beat
	evs := Transitions(beats("UUUDDDDUUU"), 3)
	if !evs[0].At.Equal(t0.Add(15*time.Minute)) || !evs[1].At.Equal(t0.Add(35*time.Minute)) || !evs[1].Since.Equal(evs[0].At) {
		t.Fatalf("dates: %+v", evs)
	}
	// the same input gives the same events: IDs built from them are stable
	a, b := Transitions(beats("UUUDDDUUUEEEU"), 3), Transitions(beats("UUUDDDUUUEEEU"), 3)
	if kinds(a) != kinds(b) || !a[0].At.Equal(b[0].At) {
		t.Fatal("not deterministic")
	}
	// a longer series ending in the same state adds no entry for old changes
	longer := Transitions(beats("UUUDDDUUUUUUUUU"), 3)
	if len(longer) != 2 || !longer[0].At.Equal(a[0].At) {
		t.Fatalf("appending beats moved an existing event: %+v", longer)
	}
}

func TestBoundAndRender(t *testing.T) {
	now := t0.Add(40 * 24 * time.Hour)
	var es []Entry
	for i := 0; i < 80; i++ {
		at := now.Add(-time.Duration(i) * 12 * time.Hour)
		es = append(es, Entry{ID: TagID("obs.example", "2026", "tensile", "c", "v", "k", at.Format("20060102T150405Z")), Kind: "k", At: at,
			Title: "t <b>&", Summary: "s", Link: "/validator/?addr=v&x=1"})
	}
	b := Bound(es, now)
	if len(b) != MaxEntries {
		t.Fatalf("bound: %d", len(b))
	}
	if !b[0].At.Equal(now) {
		t.Fatal("newest first")
	}
	for _, e := range b {
		if now.Sub(e.At) > MaxAge {
			t.Fatalf("older than MaxAge: %v", e.At)
		}
	}
	f := Feed{ID: TagID("obs.example", "2026", "tensile", "c", "v"), Title: "T", SelfHref: "feed.atom", AltHref: "/validator/?addr=v", Entries: b}
	out, err := f.Render()
	if err != nil {
		t.Fatal(err)
	}
	// well-formed, namespaced Atom with escaped text
	var doc struct {
		XMLName xml.Name
		ID      string `xml:"id"`
		Updated string `xml:"updated"`
		Entries []struct {
			ID    string `xml:"id"`
			Title string `xml:"title"`
		} `xml:"entry"`
	}
	if err := xml.Unmarshal(out, &doc); err != nil {
		t.Fatalf("not XML: %v\n%s", err, out)
	}
	if doc.XMLName.Space != "http://www.w3.org/2005/Atom" || doc.XMLName.Local != "feed" || len(doc.Entries) != MaxEntries {
		t.Fatalf("shape: %+v", doc.XMLName)
	}
	if doc.Entries[0].Title != "t <b>&" || !strings.Contains(string(out), "t &lt;b&gt;&amp;") {
		t.Fatal("text not escaped")
	}
	if doc.Updated != now.Format(time.RFC3339) {
		t.Fatalf("updated %s", doc.Updated)
	}
	// rendering is byte-stable
	again, _ := f.Render()
	if string(again) != string(out) {
		t.Fatal("render not deterministic")
	}
}

func TestTagID(t *testing.T) {
	got := TagID("Obs.Example", "2026", "tensile", "mocha-5", "abc", "unreachable", "20260901T001500Z")
	if got != "tag:Obs.Example,2026:tensile/mocha-5/abc/unreachable/20260901T001500Z" {
		t.Fatal(got)
	}
	// nothing outside the tag grammar survives, whatever a chain id holds
	if g := TagID("h", "2026", "a b/c<d>"); strings.ContainsAny(g[len("tag:h,2026:"):], " <>/") {
		t.Fatal(g)
	}
}

func TestSpan(t *testing.T) {
	for d, want := range map[time.Duration]string{30 * time.Second: "30 s", 20 * time.Minute: "20 min",
		3*time.Hour + 20*time.Minute: "3 h 20 min", 2 * time.Hour: "2 h", 72 * time.Hour: "3 days"} {
		if got := Span(d); got != want {
			t.Errorf("%v: %q", d, got)
		}
	}
}
