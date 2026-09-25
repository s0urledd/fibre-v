package verdict

import (
	"testing"
	"time"

	"github.com/plsgiveup/fibre/fibre-sentinel/internal/probe"
)

// The rule, cell by cell: only verified rows from the other vantage, taken
// within the window after the fault, clear it; a failure there confirms it;
// anything else is no answer and the fault stands.
func TestConfirmFault(t *testing.T) {
	at := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		name  string
		after time.Duration
		cls   probe.Classification
		want  ConfirmResult
	}{
		{"verified rows soon after", 3 * time.Minute, probe.ClassHealthy, ConfirmCleared},
		{"verified rows at the edge of the window", ConfirmWindow, probe.ClassHealthy, ConfirmCleared},
		{"verified rows after the window", ConfirmWindow + time.Second, probe.ClassHealthy, ConfirmNone},
		{"before the fault", -time.Second, probe.ClassHealthy, ConfirmNone},
		{"not found there too", 3 * time.Minute, probe.ClassFault, ConfirmConfirmed},
		{"unreachable from there", 3 * time.Minute, probe.ClassUnreachable, ConfirmConfirmed},
		{"server error there", 3 * time.Minute, probe.ClassServerError, ConfirmConfirmed},
		{"genuine rows, not the assignment", 3 * time.Minute, probe.ClassUnmatchedGenuine, ConfirmConfirmed},
		{"the confirming probe could not run", 3 * time.Minute, probe.ClassProbeError, ConfirmNone},
		{"not probed", 3 * time.Minute, probe.ClassNotProbed, ConfirmNone},
		{"late failure", ConfirmWindow + time.Minute, probe.ClassFault, ConfirmNone},
	} {
		got := ConfirmFault(at, Confirmation{Vantage: "de-1", StartedAt: at.Add(c.after), Classification: c.cls})
		if got != c.want {
			t.Errorf("%s: %d, want %d", c.name, got, c.want)
		}
	}
	// Only HEALTHY clears: no other class is evidence the rows were there.
	for _, cls := range probe.AllClassifications {
		got := ConfirmFault(at, Confirmation{StartedAt: at.Add(time.Minute), Classification: cls})
		if (got == ConfirmCleared) != (cls == probe.ClassHealthy) {
			t.Errorf("%s: %d", cls, got)
		}
	}
}

// Clearing is a verdict about the observer's own reading, and the class it
// lands in is the observer-side one: never a credit, never in the rate.
func TestAClearedFaultIsNeitherForNorAgainst(t *testing.T) {
	if ClearedClass.Rated() {
		t.Fatalf("%s is in the rate", ClearedClass)
	}
	for _, g := range []probe.Classification{ClearedClass} {
		found := false
		for _, s := range GuardSilentClasses {
			found = found || s == g
		}
		if !found {
			t.Errorf("%s is not one of the classes the guard reads as silent", g)
		}
	}
}

// An answer takes minutes to come back (the vantage's poll, the pull timer
// each way, the collector's pass); the window leaves them inside the
// settling period, so a fault is withdrawn while it is still provisional.
func TestTheConfirmWindowFitsInsideTheSettlingPeriod(t *testing.T) {
	const returnTrip = 5 * time.Minute
	if ConfirmWindow+returnTrip > FaultSettling {
		t.Fatalf("ConfirmWindow %s plus %s for the answer to come back exceeds FaultSettling %s", ConfirmWindow, returnTrip, FaultSettling)
	}
}

// Several vantages: a clearing answer wins, and the first by name is named.
func TestConfirmFaultByFoldsVantages(t *testing.T) {
	at := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	cs := []Confirmation{
		{Vantage: "us-2", StartedAt: at.Add(time.Minute), Classification: probe.ClassFault},
		{Vantage: "de-1", StartedAt: at.Add(2 * time.Minute), Classification: probe.ClassHealthy},
		{Vantage: "sg-1", StartedAt: at.Add(time.Minute), Classification: probe.ClassUnreachable},
	}
	cleared, confirmed := ConfirmFaultBy(at, cs)
	if cleared != "de-1" || confirmed != "sg-1" {
		t.Errorf("cleared %q confirmed %q, want de-1 and sg-1", cleared, confirmed)
	}
	if cleared, confirmed := ConfirmFaultBy(at, nil); cleared != "" || confirmed != "" {
		t.Errorf("no answers: %q %q", cleared, confirmed)
	}
}
