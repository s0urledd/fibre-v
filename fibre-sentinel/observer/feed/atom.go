// Package feed renders the observer's Atom feeds and derives their entries
// from stored rows.
//
// A feed here is a read-only view of what the store already holds: no
// subscription is stored, nothing is pushed, and a feed reader polling the
// URL is the whole mechanism. What makes that work for a reader is that the
// same state change always produces the same entry: every entry's ID is a
// tag: URI (RFC 4151) built from the validator, the kind of change and the
// moment it happened, never from the time of the request, so re-polling,
// an API restart or a snapshot refresh cannot make a reader see an event
// twice.
//
// The derivation (derive.go) is pure: rows in, events out. The API does the
// querying and the HTTP.
package feed

import (
	"bytes"
	"encoding/xml"
	"sort"
	"strings"
	"time"
)

// Entry is one Atom entry.
type Entry struct {
	ID      string
	Kind    string // the category term, e.g. "unreachable"
	Title   string
	Summary string
	Link    string // alternate link; may be site-relative
	At      time.Time
}

// Feed is one Atom document.
type Feed struct {
	ID       string
	Title    string
	Subtitle string
	// SelfHref and AltHref may be relative. Atom resolves a relative link
	// against the document's own address (RFC 4287 §2, xml:base absent), and
	// the API cannot know the public prefix the site's proxy strips (/api),
	// so relative is the only form that is right behind every deployment.
	SelfHref string
	AltHref  string
	Author   string
	Updated  time.Time
	Entries  []Entry
}

// Bounds on every feed: the newest MaxEntries entries of the last MaxAge.
// A feed is a notification channel, not an archive; the API has the rows.
const (
	MaxEntries = 50
	MaxAge     = 30 * 24 * time.Hour
)

// Bound sorts entries newest first, drops those older than now-MaxAge and
// keeps at most MaxEntries. Ties are broken by ID so the order, and hence
// the bytes and the ETag, are deterministic.
func Bound(es []Entry, now time.Time) []Entry {
	cut := now.Add(-MaxAge)
	out := es[:0:0]
	for _, e := range es {
		if !e.At.Before(cut) && !e.At.After(now.Add(time.Minute)) {
			out = append(out, e)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].At.Equal(out[j].At) {
			return out[i].At.After(out[j].At)
		}
		return out[i].ID < out[j].ID
	})
	if len(out) > MaxEntries {
		out = out[:MaxEntries]
	}
	return out
}

// TagID builds a tag: URI. authority is a DNS name the publisher controls
// (the API uses the host it was reached at), date the year the scheme was
// minted under that authority, and parts the path-like specific. Parts are
// cleaned of characters a tag specific may not hold.
func TagID(authority, date string, parts ...string) string {
	for i, p := range parts {
		parts[i] = cleanTag(p)
	}
	return "tag:" + cleanTag(authority) + "," + date + ":" + strings.Join(parts, "/")
}

func cleanTag(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '-', r == '.', r == '_', r == '~', r == ':', r == '@', r == '+', r == '=':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	return b.String()
}

// Atom XML shapes. encoding/xml escapes every text node, so a moniker or a
// host full of markup cannot inject anything into a reader.
type xmlLink struct {
	Rel  string `xml:"rel,attr"`
	Type string `xml:"type,attr,omitempty"`
	Href string `xml:"href,attr"`
}

type xmlText struct {
	Type string `xml:"type,attr"`
	Body string `xml:",chardata"`
}

type xmlCategory struct {
	Term string `xml:"term,attr"`
}

type xmlEntry struct {
	ID        string       `xml:"id"`
	Title     xmlText      `xml:"title"`
	Updated   string       `xml:"updated"`
	Published string       `xml:"published"`
	Link      *xmlLink     `xml:"link,omitempty"`
	Category  *xmlCategory `xml:"category,omitempty"`
	Summary   xmlText      `xml:"summary"`
}

type xmlFeed struct {
	XMLName  xml.Name  `xml:"http://www.w3.org/2005/Atom feed"`
	ID       string    `xml:"id"`
	Title    xmlText   `xml:"title"`
	Subtitle *xmlText  `xml:"subtitle,omitempty"`
	Updated  string    `xml:"updated"`
	Links    []xmlLink `xml:"link"`
	Author   struct {
		Name string `xml:"name"`
	} `xml:"author"`
	Generator string     `xml:"generator"`
	Entries   []xmlEntry `xml:"entry"`
}

func atomTime(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// Render writes the feed as an Atom 1.0 document. Updated defaults to the
// newest entry's time, so the document only changes when an entry does.
func (f Feed) Render() ([]byte, error) {
	upd := f.Updated
	for _, e := range f.Entries {
		if e.At.After(upd) {
			upd = e.At
		}
	}
	x := xmlFeed{ID: f.ID, Title: xmlText{Type: "text", Body: f.Title}, Updated: atomTime(upd), Generator: "Tensile"}
	if f.Subtitle != "" {
		x.Subtitle = &xmlText{Type: "text", Body: f.Subtitle}
	}
	if f.SelfHref != "" {
		x.Links = append(x.Links, xmlLink{Rel: "self", Type: "application/atom+xml", Href: f.SelfHref})
	}
	if f.AltHref != "" {
		x.Links = append(x.Links, xmlLink{Rel: "alternate", Type: "text/html", Href: f.AltHref})
	}
	x.Author.Name = f.Author
	if x.Author.Name == "" {
		x.Author.Name = "Tensile observer"
	}
	for _, e := range f.Entries {
		xe := xmlEntry{ID: e.ID, Title: xmlText{Type: "text", Body: e.Title}, Updated: atomTime(e.At), Published: atomTime(e.At),
			Summary: xmlText{Type: "text", Body: e.Summary}}
		if e.Link != "" {
			xe.Link = &xmlLink{Rel: "alternate", Type: "text/html", Href: e.Link}
		}
		if e.Kind != "" {
			xe.Category = &xmlCategory{Term: e.Kind}
		}
		x.Entries = append(x.Entries, xe)
	}
	var buf bytes.Buffer
	buf.WriteString(xml.Header)
	enc := xml.NewEncoder(&buf)
	enc.Indent("", "  ")
	if err := enc.Encode(x); err != nil {
		return nil, err
	}
	buf.WriteByte('\n')
	return buf.Bytes(), nil
}
