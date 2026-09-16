package api

import (
	"context"
	"strconv"
	"sync"
)

// Per-publication verdicts, kept because they cannot change.
//
// /v1/blobs answers each row with a class tally and a reconstructability
// verdict, and both are per publication: the tally is one query, the verdict is
// five. A page of 200 was therefore 1,200 queries, and measured on a store with
// 260 publications and 85,000 probes it took 1.4s — for a page whose rows, past
// the first screen, describe obligations that ended hours ago and can never be
// rated differently again.
//
// The store is append-only where it matters. UpsertPublication and InsertProbe
// both end in ON CONFLICT DO NOTHING, so a publication row, its assignments and
// its probes are all immutable once written, and the ONLY thing that can move a
// publication's verdict is a probe row arriving that was not there before — a
// collector restarting with a backlog, say. That makes
//
//	(how many probes this publication has, the highest rowid among them)
//
// an exact fingerprint: it cannot stay the same across a change, and it cannot
// change without one. One query fetches it for a whole page.
//
// The one thing that is not immutable is the clock. A verdict carries
// window_over, which flips once when must_serve_until passes, so nothing is
// cached until it has flipped. That costs nothing: it is exactly the newest
// publications, the ones still being probed, whose verdict is not settled
// anyway.
//
// This is deliberately not the snapshot cache in snapshot.go. That one holds
// one value per window and refreshes it on a timer, which is right for an
// aggregate that changes a little with every probe. A per-publication verdict
// changes never or completely, so it wants exactness, not freshness.

// blobVerdict is everything /v1/blobs computes per publication.
type blobVerdict struct {
	fp      string // probe count and highest probe rowid, at the time this was computed
	classes classCounts
	total   int64
	rc      *reconstruct
}

// blobCacheMax bounds the map. At roughly 400 bytes a verdict this is a few
// megabytes, and it holds every publication of a busy week.
const blobCacheMax = 20000

type blobCache struct {
	mu sync.Mutex
	m  map[string]blobVerdict
}

func newBlobCache() *blobCache { return &blobCache{m: map[string]blobVerdict{}} }

// get returns the cached verdict if it was computed against exactly the probes
// the publication has now.
func (c *blobCache) get(hash, fp string) (blobVerdict, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.m[hash]
	if !ok || v.fp != fp {
		return blobVerdict{}, false
	}
	return v, true
}

func (c *blobCache) put(hash string, v blobVerdict) {
	c.mu.Lock()
	defer c.mu.Unlock()
	// Over the bound, start again rather than evict by age. A verdict costs six
	// queries to rebuild and the map only reaches this size on a store far
	// larger than one page, so a rare full rebuild is cheaper to hold in the
	// head than an eviction policy, and it cannot leak.
	if len(c.m) >= blobCacheMax {
		c.m = make(map[string]blobVerdict, blobCacheMax/2)
	}
	c.m[hash] = v
}

// probeFingerprints returns, for every publication in the selection, a string
// that changes if and only if its probe rows have changed. A publication with
// no probes at all is absent from the map and gets the zero fingerprint, which
// is still a fingerprint: it stops being the zero one the moment a probe lands.
func (s *Server) probeFingerprints(ctx context.Context, where string, limit int, args ...any) (map[string]string, error) {
	rows, err := s.st.DB().QueryContext(ctx, blobSel(where, limit)+`
		SELECT p.promise_hash, COUNT(*), COALESCE(MAX(p.rowid), 0)
		FROM probes p JOIN sel ON sel.promise_hash = p.promise_hash
		GROUP BY p.promise_hash`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var hash string
		var n, maxRowID int64
		if err := rows.Scan(&hash, &n, &maxRowID); err != nil {
			return nil, err
		}
		out[hash] = strconv.FormatInt(n, 10) + ":" + strconv.FormatInt(maxRowID, 10)
	}
	return out, rows.Err()
}
