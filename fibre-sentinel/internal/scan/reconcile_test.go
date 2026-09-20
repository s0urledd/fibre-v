package scan

import (
	"errors"
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

	s.reconcileParamsWith(120, func() (fibretypes.Params, error) { return p, nil })
	if s.lastReconcile != 120 {
		t.Fatalf("a reconcile that read state should move the marker: got %d", s.lastReconcile)
	}

	for _, h := range []int64{180, 240, 300} {
		s.reconcileParamsWith(h, func() (fibretypes.Params, error) {
			return fibretypes.Params{}, errors.New("rpc: connection refused")
		})
		if s.lastReconcile != 120 {
			t.Fatalf("h=%d: a reconcile that could not read state moved the marker to %d", h, s.lastReconcile)
		}
	}

	// The outage ends. The interval the next successful reconcile would
	// report is (120, 360] — every height the failed checks did not cover.
	s.reconcileParamsWith(360, func() (fibretypes.Params, error) { return p, nil })
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
	})
	if s.lastReconcile != 0 {
		t.Fatalf("marker after a failed first reconcile: got %d, want 0 (unknown)", s.lastReconcile)
	}
}
