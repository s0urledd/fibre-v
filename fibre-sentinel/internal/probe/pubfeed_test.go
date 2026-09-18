package probe

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/plsgiveup/fibre/fibre-sentinel/internal/scan"
)

// The shadow candidates are every promise the feed still holds, whatever
// phase it is in: a promise past its must_serve_until but not yet past its
// post-deadline probe still has a shard on disk, and the store answers from
// it. Filtering candidates by "in window" would file the older promise's
// rows as a fault in its last minutes.
func TestShadowersFor_KeepsPromisesPastTheirDeadline(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "publications.jsonl")
	now := time.Now().UTC()
	mk := func(hash string, msu time.Time, rows []int) scan.Publication {
		return scan.Publication{
			PromiseHash: hash, MustServeUntil: msu, SettlementTime: msu.Add(-4 * time.Hour),
			Promise: scan.PromiseFields{Commitment: "cc"},
			Assignment: scan.AssignmentTable{Validators: []scan.ValidatorAssignment{
				{Address: "v1", RowCount: len(rows), Rows: rows},
			}},
		}
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	enc := json.NewEncoder(f)
	// older promise: deadline passed a minute ago, shard still on disk
	if err := enc.Encode(mk("old", now.Add(-time.Minute), []int{1, 2})); err != nil {
		t.Fatal(err)
	}
	// the promise being probed: same blob, different rows
	if err := enc.Encode(mk("new", now.Add(time.Hour), []int{3, 4, 5})); err != nil {
		t.Fatal(err)
	}
	f.Close()

	feed := newPubFeed(path)
	if _, err := feed.refresh(); err != nil {
		t.Fatal(err)
	}
	cands := feed.shadowersFor("new", "cc", "v1")
	if len(cands) != 1 || cands[0].PromiseHash != "old" || len(cands[0].Rows) != 2 {
		t.Fatalf("candidates for the new promise = %+v, want the older promise past its deadline", cands)
	}
	if got := feed.shadowersFor("old", "cc", "v1"); len(got) != 1 || got[0].PromiseHash != "new" {
		t.Fatalf("candidates for the old promise = %+v, want the newer one", got)
	}
	if got := feed.shadowersFor("new", "cc", "v2"); len(got) != 0 {
		t.Fatalf("a validator with no rows in the other promise got candidates: %+v", got)
	}
	// forgetting the older promise (its post probe done, shard pruned) drops it
	feed.forget("old")
	if got := feed.shadowersFor("new", "cc", "v1"); len(got) != 0 {
		t.Fatalf("forgotten promise still a candidate: %+v", got)
	}
}
