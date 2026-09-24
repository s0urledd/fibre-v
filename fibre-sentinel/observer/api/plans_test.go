package api

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/plsgiveup/fibre/fibre-sentinel/observer/rollup"
	"github.com/plsgiveup/fibre/fibre-sentinel/observer/store"
)

// apiPartial are partial indexes a plan may walk whole: each holds only the
// rows its query wants.
var apiPartial = []string{"publications_unassignable"}

// TestHotQueriesUseIndexes pins the plans of the queries the API runs per
// request or per snapshot refresh that used to walk a whole table: the
// latest reachability answer per validator, the newest assignment per
// validator, the vantage count, the unassignable count, the publisher list,
// and the effective-class tallies, which migration 19 had pushed off their
// covering indexes. A plan regression here is invisible on a test store and
// costs seconds per request on a real one, so the plan is the assertion.
func TestHotQueriesUseIndexes(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "o.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()

	cls := rollup.EffectiveClass("")
	const lo, hi = "2026-09-01T00:00:00.000Z", "2026-09-08T00:00:00.000Z"
	type c struct {
		name string
		q    string
		args []any
		want []string
	}
	var cases []c
	for _, tb := range []struct{ table, ok, idx string }{
		{"reachability", `outcome <> 'PROBE_ERROR'`, "reachability_latest_answer"},
		{"probes", `outcome NOT IN ('MISSED','PROBE_ERROR')`, "probes_latest_answer"},
	} {
		for _, v := range []struct{ only, asOf string }{{"", ""}, {"ab", ""}, {"", hi}, {"ab", hi}} {
			q, args := latestAnswerSQL(tb.table, tb.ok, "x", v.only, v.asOf)
			cases = append(cases, c{"latest answer " + tb.table + " only=" + v.only + " asOf=" + v.asOf, q, args,
				[]string{"USING INDEX " + tb.idx + " (validator_address=?)"}})
		}
	}
	for _, only := range []string{"", "ab"} {
		q, args := latestAssignmentSQL(only)
		cases = append(cases, c{"latest assignment only=" + only, q, args,
			[]string{"assignments_validator_height (validator_address=?)"}})
	}
	cases = append(cases,
		c{"vantage count", vantageCountSQL, nil, []string{"COVERING INDEX probes_vantage", "COVERING INDEX reachability_vantage"}},
		c{"unassignable", `SELECT COUNT(*) FROM publications WHERE assignment_error != ''`, nil,
			[]string{"publications_unassignable"}},
		c{"unassignable recent", `SELECT COUNT(*) FROM publications WHERE settlement_height >= ? AND settlement_time >= ? AND assignment_error != ''`,
			[]any{1, lo}, []string{"publications_unassignable (settlement_height>?)"}},
		c{"publishers", publisherRowsSQL(""), []any{lo, hi}, []string{"payments_time (time>? AND time<?)", "payments_publisher_time (publisher=?)"}},
		c{"publisher", publisherRowsSQL(" AND p.publisher = ?"), []any{lo, hi, "celestia1x"}, []string{"payments_publisher_time (publisher=?"}},
		c{"class tally", `SELECT ` + cls + `, COUNT(*) FROM probes WHERE started_at >= ? AND started_at <= ? AND assigned = 1 AND phase = 'in_window' GROUP BY 1`,
			[]any{lo, hi}, []string{"COVERING INDEX probes_"}},
		c{"per-validator class tally", `SELECT validator_address, ` + cls + `, COUNT(*) FROM probes WHERE started_at >= ? AND started_at <= ? AND assigned = 1 AND phase = 'in_window' GROUP BY 1, 2`,
			[]any{lo, hi}, []string{"COVERING INDEX probes_"}},
		c{"faults per validator", `SELECT validator_address, COUNT(*) FROM probes WHERE started_at >= ? AND started_at <= ? AND assigned = 1 AND ` + cls + ` = 'FAULT' GROUP BY validator_address`,
			[]any{lo, hi}, []string{"COVERING INDEX probes_"}},
		c{"one validator's tally", `SELECT ` + cls + `, COUNT(*) FROM probes WHERE validator_address = ? AND assigned = 1 AND phase = 'in_window' AND started_at >= ? AND started_at <= ? GROUP BY 1`,
			[]any{"ab", lo, hi}, []string{"COVERING INDEX probes_validator_window"}},
	)
	for _, tc := range cases {
		plan, err := st.QueryPlan(ctx, tc.q, tc.args...)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		joined := strings.Join(plan, "\n")
		if bad := store.FullScans(plan, []string{"v", "m", "w", "vp", "vr"}, apiPartial); len(bad) > 0 {
			t.Errorf("%s walks a whole table or index: %v\nplan:\n%s", tc.name, bad, joined)
		}
		for _, w := range tc.want {
			if !strings.Contains(joined, w) {
				t.Errorf("%s: plan does not use %q\nplan:\n%s", tc.name, w, joined)
			}
		}
		if strings.Contains(joined, "TEMP B-TREE FOR ORDER BY") && strings.HasPrefix(tc.name, "latest answer") {
			t.Errorf("%s sorts instead of reading the index in rowid order\nplan:\n%s", tc.name, joined)
		}
	}
}
