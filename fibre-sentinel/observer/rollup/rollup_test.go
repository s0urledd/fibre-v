package rollup_test

import (
	"context"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/plsgiveup/fibre/fibre-sentinel/observer/rollup"
	"github.com/plsgiveup/fibre/fibre-sentinel/observer/store"
	"github.com/plsgiveup/fibre/fibre-sentinel/observer/verdict"
)

func openStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "rollup.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// A steady validator's day repeats: the same eight HEALTHY probes today as
// yesterday. Those rolled days must both be counted. They were not, while
// the read grouped by the class JSON: the numeric columns were summed over
// the group and the classes of one day in it were taken once, so the serve
// rate published for that validator on the "all" window fell every time a
// day repeated. Nothing about the rollup may move a figure printed against
// an operator's name.
func TestLoad_RepeatedDailyClassMapIsNotCollapsed(t *testing.T) {
	st := openStore(t)
	db := st.DB()
	ctx := context.Background()
	ins := func(day, addr string, probes, gaps, faults, beats, beatsUp, identityUp int64, classes string) {
		t.Helper()
		if _, err := db.ExecContext(ctx, `INSERT INTO probe_daily
			(day, validator_address, probes, gaps, faults, classes_json, beats, beats_up, identity_up, computed_at)
			VALUES (?,?,?,?,?,?,?,?,?,?)`, day, addr, probes, gaps, faults, classes, beats, beatsUp, identityUp, "2026-01-03T00:00:00Z"); err != nil {
			t.Fatalf("insert %s %s: %v", day, addr, err)
		}
	}
	// v1 repeats itself; v2 does not. v1's certificate lapsed on the second
	// day, which is the case identity_up exists to carry past the prune.
	ins("2026-01-01", "v1", 4, 0, 0, 4, 4, 4, `{"HEALTHY":4}`)
	ins("2026-01-02", "v1", 4, 0, 0, 4, 4, 1, `{"HEALTHY":4}`)
	ins("2026-01-01", "v2", 1, 0, 1, 1, 0, 0, `{"FAULT":1}`)
	ins("2026-01-02", "v2", 2, 0, 2, 2, 0, 0, `{"FAULT":2}`)

	got, err := rollup.Load(ctx, db, time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC), "")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.Probes != 11 || got.Faults != 3 {
		t.Fatalf("probes=%d faults=%d, want 11/3", got.Probes, got.Faults)
	}
	if got.Classes["HEALTHY"] != 8 || got.Classes["FAULT"] != 3 {
		t.Fatalf("classes=%v, want HEALTHY:8 FAULT:3", got.Classes)
	}
	// The invariant that makes the serve rate readable: the class tally and
	// the probe count are two counts of the same rows.
	var summed int64
	for _, n := range got.Classes {
		summed += n
	}
	if summed != got.Probes {
		t.Fatalf("classes sum to %d but probes is %d", summed, got.Probes)
	}
	if v := got.ProbesByVal["v1"]; v == nil || v.Classes["HEALTHY"] != 8 || v.Probes != 8 {
		t.Fatalf("v1 = %+v, want 8 probes all HEALTHY", v)
	}
	// Endorsement is carried past the prune beside reachability, over the
	// same denominator (BeatsUp), so the two figures on one row cannot end
	// up covering different spans.
	if v := got.ProbesByVal["v1"]; v.IdentityUp != 5 || v.BeatsUp != 8 {
		t.Fatalf("v1 identity_up=%d beats_up=%d, want 5/8", v.IdentityUp, v.BeatsUp)
	}
	if got.IdentityUp != 5 {
		t.Fatalf("network identity_up = %d, want 5", got.IdentityUp)
	}
	if v := got.ProbesByVal["v2"]; v == nil || v.Classes["FAULT"] != 3 || v.Probes != 3 {
		t.Fatalf("v2 = %+v, want 3 probes all FAULT", v)
	}
	if got.Days != 2 {
		t.Fatalf("days = %d, want 2", got.Days)
	}
	// Restricted to one validator, the other's rows are not in the totals.
	one, err := rollup.Load(ctx, db, time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC), "v1")
	if err != nil {
		t.Fatalf("load one: %v", err)
	}
	if one.Probes != 8 || one.Classes["FAULT"] != 0 || len(one.ProbesByVal) != 1 {
		t.Fatalf("only=v1 gave probes=%d classes=%v vals=%d", one.Probes, one.Classes, len(one.ProbesByVal))
	}
}

// The obligation SQL is a constant, so verdict.EndSegmentDivisor is spelled
// into it by hand. If the Go constant moves and the query does not, the SQL
// and its Go twin would cut the retention window at two different places and
// publish two different serve rates from the same rows — the one divergence
// sentinel-recompute exists to catch, arriving as a silent disagreement
// between two implementations that are supposed to be one.
func TestTheSQLAndTheGoTwinCutTheWindowAtTheSamePoint(t *testing.T) {
	want := "/ " + strconv.FormatFloat(verdict.EndSegmentDivisor, 'f', 1, 64)
	if !strings.Contains(rollup.ObligationBuckets, want) {
		t.Fatalf("verdict.EndSegmentDivisor is %v, so the obligation SQL must divide by %q; it does not:\n%s",
			verdict.EndSegmentDivisor, want, rollup.ObligationBuckets)
	}
}

// The guard's denominator is the one number the SQL and the Go twin must
// agree on exactly: a class counted as "probed" by one and not the other
// would put the two implementations on different sides of the threshold at
// the same schedule point, and the observer would publish a fault that its
// own recompute says is suspect.
func TestTheSQLAndTheGoTwinExcludeTheSameClassesFromTheGuard(t *testing.T) {
	var want []string
	for _, c := range verdict.GuardSilentClasses {
		want = append(want, "'"+string(c)+"'")
	}
	sort.Strings(want)

	got := strings.Split(strings.Trim(rollup.GuardSilentSQL, "()"), ",")
	for i := range got {
		got[i] = strings.TrimSpace(got[i])
	}
	sort.Strings(got)

	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("guard denominator diverged:\n  Go:  %s\n  SQL: %s", strings.Join(want, ","), strings.Join(got, ","))
	}
}
