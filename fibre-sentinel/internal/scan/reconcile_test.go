package scan

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	fibretypes "github.com/celestiaorg/celestia-app/v10/x/fibre/types"
)

// A reconcile whose state read failed has narrowed nothing: the interval a
// silent param change could have landed in still starts where it did. If the
// marker moved anyway, the next successful reconcile would report an interval
// beginning after the failed check and the heights in between would drop out
// of the only record that says which publications carry a deadline computed
// from the old params.
func TestAFailedReconcileDoesNotNarrowTheUncertaintyInterval(t *testing.T) {
	p := params(10*time.Minute, 30*time.Minute, 13*time.Hour)
	s := &Scanner{log: NewLogger(10), startHeight: 100, params: NewParamHistory(100, p)}

	s.reconcileParamsWith(120, func() (fibretypes.Params, error) { return p, nil }, nil)
	if s.lastReconcile != 120 {
		t.Fatalf("a reconcile that read state should move the marker: got %d", s.lastReconcile)
	}

	for _, h := range []int64{180, 240, 300} {
		s.reconcileParamsWith(h, func() (fibretypes.Params, error) {
			return fibretypes.Params{}, errors.New("rpc: connection refused")
		}, nil)
		if s.lastReconcile != 120 {
			t.Fatalf("h=%d: a reconcile that could not read state moved the marker to %d", h, s.lastReconcile)
		}
	}

	// The outage ends. The interval the next successful reconcile would
	// report is (120, 360] — every height the failed checks did not cover.
	s.reconcileParamsWith(360, func() (fibretypes.Params, error) { return p, nil }, nil)
	if s.lastReconcile != 360 {
		t.Fatalf("marker after recovery: got %d, want 360", s.lastReconcile)
	}
}

// With no reconcile on record the interval is the whole scan, and a failed
// first check must not turn that into a narrow one.
func TestAFailedFirstReconcileLeavesTheIntervalAtTheWholeScan(t *testing.T) {
	p := params(10*time.Minute, 30*time.Minute, 13*time.Hour)
	s := &Scanner{log: NewLogger(10), startHeight: 100, params: NewParamHistory(100, p)}

	s.reconcileParamsWith(120, func() (fibretypes.Params, error) {
		return fibretypes.Params{}, errors.New("rpc: connection refused")
	}, nil)
	if s.lastReconcile != 0 {
		t.Fatalf("marker after a failed first reconcile: got %d, want 0 (unknown)", s.lastReconcile)
	}
}

// storeFor gives a Scanner a real store so emitUncertainty has somewhere to
// write, and returns the path of the record file.
func storeFor(t *testing.T, s *Scanner) (*Store, string) {
	t.Helper()
	dir := t.TempDir()
	st, err := OpenStore(dir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	s.store = st
	return st, filepath.Join(dir, "param_uncertainty.jsonl")
}

func readUncertainty(t *testing.T, path string) []ParamUncertainty {
	t.Helper()
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var out []ParamUncertainty
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if line == "" {
			continue
		}
		var u ParamUncertainty
		if err := json.Unmarshal([]byte(line), &u); err != nil {
			t.Fatalf("decode %q: %v", line, err)
		}
		out = append(out, u)
	}
	return out
}

// A silent shortening must leave behind a record a machine can act on, not
// only a log line. Before this the whole fact lived in a WARNING that
// nothing read: not in the export, not in the database, not in the API.
func TestASilentShorteningWritesARecordAndClosesItByReadingEveryHeight(t *testing.T) {
	old := params(10*time.Minute, 4*time.Hour, 13*time.Hour)
	shorter := params(10*time.Minute, 1*time.Hour, 13*time.Hour)
	s := &Scanner{log: NewLogger(10), chainID: "mocha-5", startHeight: 100, params: NewParamHistory(100, old)}
	_, path := storeFor(t, s)
	s.store.SetSettledCoverFrom(100)

	s.reconcileParamsWith(120, func() (fibretypes.Params, error) { return old, nil },
		func(int64) (fibretypes.Params, error) { return old, nil })
	if got := readUncertainty(t, path); len(got) != 0 {
		t.Fatalf("a reconcile that found nothing wrote %d record(s)", len(got))
	}

	// The change is in force from height 150; the reconcile at 180 is the
	// first read that can see it.
	readAt := func(at int64) (fibretypes.Params, error) {
		if at >= 150 {
			return shorter, nil
		}
		return old, nil
	}
	s.reconcileParamsWith(180, func() (fibretypes.Params, error) { return shorter, nil }, readAt)

	got := readUncertainty(t, path)
	if len(got) != 1 {
		t.Fatalf("want one record, got %d", len(got))
	}
	u := got[0]
	if u.Kind != UncertaintySilentChange || u.FromHeight != 121 || u.ToHeight != 180 {
		t.Fatalf("range = %s %d-%d, want silent_change 121-180", u.Kind, u.FromHeight, u.ToHeight)
	}
	if u.ID != "mocha-5:silent_change:121-180" {
		t.Fatalf("id = %q", u.ID)
	}
	if u.Direction != "shorter" || u.EffectiveFromHeight != 181 || !u.IntervalStartKnown {
		t.Fatalf("direction=%q effective_from=%d start_known=%v", u.Direction, u.EffectiveFromHeight, u.IntervalStartKnown)
	}
	if u.WindowBeforeS != int64(4*time.Hour/time.Second) || u.WindowAfterS != int64(time.Hour/time.Second) {
		t.Fatalf("windows = %d -> %d", u.WindowBeforeS, u.WindowAfterS)
	}
	if u.Resolution != ResolutionVerified {
		t.Fatalf("resolution = %q (%s)", u.Resolution, u.ResolveError)
	}
	// Every height from FromHeight-1 through ToHeight: 120..180.
	if u.HeightsRead != 61 {
		t.Fatalf("heights_read = %d, want 61", u.HeightsRead)
	}
	if len(u.Values) != 2 || u.Values[0].FromHeight != 120 || u.Values[1].FromHeight != 150 {
		t.Fatalf("values = %+v, want the old value from 120 and the new one from 150", u.Values)
	}
	if !u.Holds() != true {
		t.Fatal("a verified range must not hold anything")
	}
	// The proven value is in the history at the height it was really in
	// force from, not at the h+1 the single read could vouch for.
	if e := s.params.at(150, 0); e == nil || e.Source != "verified" || e.Params.ShardRetention != time.Hour {
		t.Fatalf("history at 150 = %+v, want the verified shorter value", e)
	}
}

// A range the node cannot answer for is recorded unresolvable and keeps its
// hold. A partial read is not a partial proof: the heights it skipped are
// exactly where a third value could hide, so nothing read is kept.
func TestARangeTheNodeCannotAnswerForStaysHeld(t *testing.T) {
	old := params(10*time.Minute, 4*time.Hour, 13*time.Hour)
	shorter := params(10*time.Minute, time.Hour, 13*time.Hour)
	s := &Scanner{log: NewLogger(10), chainID: "mocha-5", startHeight: 100, params: NewParamHistory(100, old), lastReconcile: 120}
	_, path := storeFor(t, s)
	s.store.SetSettledCoverFrom(100)

	s.reconcileParamsWith(180, func() (fibretypes.Params, error) { return shorter, nil },
		func(at int64) (fibretypes.Params, error) {
			if at > 140 {
				return fibretypes.Params{}, errors.New("height 141 is not available, lowest height is 160")
			}
			return old, nil
		})

	got := readUncertainty(t, path)
	if len(got) != 1 {
		t.Fatalf("want one record, got %d", len(got))
	}
	u := got[0]
	if u.Resolution != ResolutionUnresolvable {
		t.Fatalf("resolution = %q, want unresolvable", u.Resolution)
	}
	if !u.Holds() {
		t.Fatal("an unresolvable silent change must still hold its verdicts")
	}
	if len(u.Values) != 0 {
		t.Fatalf("a partial read kept %d value(s); it proves nothing about the heights it skipped", len(u.Values))
	}
	if u.HeightsRead != 21 || !strings.Contains(u.ResolveError, "height 141") {
		t.Fatalf("heights_read=%d err=%q", u.HeightsRead, u.ResolveError)
	}
}

// A run of failed checks is one blind stretch, not one per check. Writing a
// record every sixty blocks through an outage would bury the one that
// matters under hundreds that say the same thing.
func TestAFailedReconcileRecordsTheBlindStretchOncePerRun(t *testing.T) {
	p := params(10*time.Minute, 4*time.Hour, 13*time.Hour)
	s := &Scanner{log: NewLogger(10), chainID: "mocha-5", startHeight: 100, params: NewParamHistory(100, p), lastReconcile: 120}
	_, path := storeFor(t, s)
	s.store.SetSettledCoverFrom(100)

	fail := func() (fibretypes.Params, error) { return fibretypes.Params{}, errors.New("rpc: connection refused") }
	for _, h := range []int64{180, 240, 300} {
		s.reconcileParamsWith(h, fail, nil)
	}
	got := readUncertainty(t, path)
	if len(got) != 1 {
		t.Fatalf("three failed checks in one run wrote %d record(s), want 1", len(got))
	}
	if got[0].Kind != UncertaintyCheckSkipped || got[0].FromHeight != 121 || got[0].ToHeight != 180 {
		t.Fatalf("record = %s %d-%d", got[0].Kind, got[0].FromHeight, got[0].ToHeight)
	}
	if got[0].Holds() {
		t.Fatal("a check that did not happen is not evidence that anything changed; it must not hold")
	}
	// The run ends; a later one is its own record.
	s.reconcileParamsWith(360, func() (fibretypes.Params, error) { return p, nil }, nil)
	if s.reconcileFailingSince != 0 {
		t.Fatalf("reconcileFailingSince = %d after a check that read state", s.reconcileFailingSince)
	}
	s.reconcileParamsWith(420, fail, nil)
	if got := readUncertainty(t, path); len(got) != 2 {
		t.Fatalf("a second run wrote %d record(s) in total, want 2", len(got))
	}
}

// A range wider than one pass will read is recorded unresolvable without
// reading anything, rather than stalling the block loop for hours.
func TestARangeTooWideToReadIsRecordedUnresolvable(t *testing.T) {
	old := params(10*time.Minute, 4*time.Hour, 13*time.Hour)
	shorter := params(10*time.Minute, time.Hour, 13*time.Hour)
	s := &Scanner{log: NewLogger(10), chainID: "mocha-5", startHeight: 1, params: NewParamHistory(1, old)}
	_, path := storeFor(t, s)
	reads := 0
	s.reconcileParamsWith(maxVerifyHeights+500, func() (fibretypes.Params, error) { return shorter, nil },
		func(int64) (fibretypes.Params, error) { reads++; return old, nil })
	if reads != 0 {
		t.Fatalf("read %d height(s) for a range it had already decided not to read", reads)
	}
	got := readUncertainty(t, path)
	if len(got) != 1 || got[0].Resolution != ResolutionUnresolvable || !got[0].Holds() {
		t.Fatalf("record = %+v", got)
	}
}
