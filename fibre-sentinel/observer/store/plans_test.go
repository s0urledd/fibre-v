package store

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/plsgiveup/fibre/fibre-sentinel/internal/probe"
	"github.com/plsgiveup/fibre/fibre-sentinel/internal/scan"
)

// heldPartial are the partial indexes the hold statements may walk whole:
// each holds only rows that are held (or corrected), so walking one costs
// what is held, not what is stored.
var heldPartial = []string{"publications_held", "publications_corrected", "probes_held"}

// TestHotQueriesUseIndexes pins the plans of the statements the collector
// runs every pass. Each used to walk the whole probes table — SyncParamHolds
// inside a write transaction — which a test-sized store never notices and a
// production one pays for every ten seconds, so the plan is what is asserted,
// not a timing. The API's per-request queries have the same test in package
// api.
func TestHotQueriesUseIndexes(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "o.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()

	cases := []struct {
		name string
		q    string
		args []any
		want []string // substrings the plan must contain
	}{
		{"sync: publications raised", syncParamHoldsStmts[0], nil,
			[]string{"param_uncertainty_holding", "publications_settlement"}},
		{"sync: publications cleared", syncParamHoldsStmts[1], nil,
			[]string{"publications_held", "param_uncertainty_holding"}},
		{"sync: probe rows raised", syncParamHoldsStmts[2], nil,
			[]string{"INTEGER PRIMARY KEY (rowid=?)", "publications_held", "publications_corrected", "probes_promise"}},
		{"sync: probe rows cleared", syncParamHoldsStmts[3], nil,
			[]string{"probes_held"}},
		{"corrector sweep", staleDeadlineRowsSQL, []any{20000},
			[]string{"publications_corrected", "probes_promise"}},
		{"counts: new rows only", `SELECT COUNT(*), COALESCE(MAX(rowid), ?) FROM probes WHERE rowid > ?`, []any{0, 0},
			[]string{"INTEGER PRIMARY KEY (rowid>?)"}},
	}
	for _, c := range cases {
		plan, err := st.QueryPlan(ctx, c.q, c.args...)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		joined := strings.Join(plan, "\n")
		if bad := FullScans(plan, nil, heldPartial); len(bad) > 0 {
			t.Errorf("%s walks a whole table or index: %v\nplan:\n%s", c.name, bad, joined)
		}
		for _, w := range c.want {
			if !strings.Contains(joined, w) {
				t.Errorf("%s: plan does not use %q\nplan:\n%s", c.name, w, joined)
			}
		}
	}
}

// TestCountKeepsUpWithInsertsAndPrunes: Count keeps running totals, so it
// has to agree with a plain COUNT(*) after rows arrive, and again after the
// retention prune deletes some — which it learns of from raw_from moving.
func TestCountKeepsUpWithInsertsAndPrunes(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "o.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	exact := func() (p, a, pr int64) {
		t.Helper()
		if err := st.db.QueryRow(`SELECT (SELECT COUNT(*) FROM publications), (SELECT COUNT(*) FROM assignments), (SELECT COUNT(*) FROM probes)`).
			Scan(&p, &a, &pr); err != nil {
			t.Fatal(err)
		}
		return
	}
	check := func(when string) {
		t.Helper()
		c, err := st.Count(ctx)
		if err != nil {
			t.Fatal(err)
		}
		p, a, pr := exact()
		if c.Publications != p || c.Assignments != a || c.Probes != pr {
			t.Fatalf("%s: Count says %d/%d/%d, the tables hold %d/%d/%d", when, c.Publications, c.Assignments, c.Probes, p, a, pr)
		}
	}
	base := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	addProbes := func(hash string, n int, day time.Time) {
		for i := 0; i < n; i++ {
			at := day.Add(time.Duration(i) * time.Minute)
			m := probe.Measurement{Vantage: "t", PromiseHash: hash, ValidatorAddress: "v1",
				ScheduledAt: at, StartedAt: at, FinishedAt: at, MustServeUntil: at}
			if _, err := st.InsertProbe(m, []byte(`{}`)); err != nil {
				t.Fatal(err)
			}
		}
	}
	check("empty store")
	pub := scan.Publication{PromiseHash: "p1", SettlementHeight: 10,
		Assignment: scan.AssignmentTable{Validators: []scan.ValidatorAssignment{{Address: "v1"}, {Address: "v2"}}}}
	if _, err := st.UpsertPublication(pub, []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	addProbes("p1", 5, base)
	check("first rows")
	addProbes("p1", 3, base.Add(24*time.Hour))
	check("rows since the last count")

	// The prune: delete a day and move raw_from, as rollup.Run does.
	if _, err := st.db.Exec(`DELETE FROM probes WHERE started_at < ?`, TS(base.Add(24*time.Hour))); err != nil {
		t.Fatal(err)
	}
	if err := st.SetMeta(metaRawFrom, "2026-09-02", time.Now()); err != nil {
		t.Fatal(err)
	}
	check("after a prune")
	addProbes("p1", 2, base.Add(48*time.Hour))
	check("rows after the prune")
}

// TestAssignmentsCarryTheirSettlementHeight: the copy migration 22 made is
// written on insert, so the validators table's seek finds new assignments.
func TestAssignmentsCarryTheirSettlementHeight(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "o.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	pub := scan.Publication{PromiseHash: "p1", SettlementHeight: 4242,
		Assignment: scan.AssignmentTable{Validators: []scan.ValidatorAssignment{{Address: "v1"}}}}
	if _, err := st.UpsertPublication(pub, []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	var h int64
	if err := st.db.QueryRow(`SELECT settlement_height FROM assignments WHERE promise_hash = 'p1'`).Scan(&h); err != nil {
		t.Fatal(err)
	}
	if h != 4242 {
		t.Fatalf("assignment settlement_height %d, want the publication's 4242", h)
	}
}
