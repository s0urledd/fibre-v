package store

import (
	"path/filepath"
	"testing"
	"time"
)

// The two pace anchors advance like a slow clock: the older one always
// covers between twelve and twenty-four hours, so the block time the API
// derives from it is a measurement over that span. A tip below the older
// anchor means another chain, and both anchors start over.
func TestNotePaceKeepsAnOlderAnchorOfTwelveToTwentyFourHours(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "observer.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	t0 := time.Date(2026, 9, 22, 0, 0, 0, 0, time.UTC)
	note := func(h int64, at time.Time) {
		t.Helper()
		if err := st.NotePace(h, at, at); err != nil {
			t.Fatal(err)
		}
	}
	anchor := func(name string) (int64, time.Time) {
		t.Helper()
		h, at, err := st.paceAnchor(name)
		if err != nil {
			t.Fatal(err)
		}
		return h, at
	}
	note(1000, t0)
	if h, at := anchor("from"); h != 1000 || !at.Equal(t0) {
		t.Fatalf("from after the first poll: %d %v", h, at)
	}
	if h, _ := anchor("mid"); h != 0 {
		t.Fatalf("mid set on the first poll: %d", h)
	}
	note(2000, t0.Add(time.Hour))
	if h, _ := anchor("from"); h != 1000 {
		t.Fatalf("from moved within the step: %d", h)
	}
	note(16000, t0.Add(12*time.Hour))
	if h, at := anchor("mid"); h != 16000 || !at.Equal(t0.Add(12*time.Hour)) {
		t.Fatalf("mid after twelve hours: %d %v", h, at)
	}
	note(31000, t0.Add(25*time.Hour))
	if h, at := anchor("from"); h != 16000 || !at.Equal(t0.Add(12*time.Hour)) {
		t.Fatalf("from should be the old mid after the pair advanced: %d %v", h, at)
	}
	if h, _ := anchor("mid"); h != 31000 {
		t.Fatalf("mid after the pair advanced: %d", h)
	}
	// a lower tip is a different chain
	note(50, t0.Add(26*time.Hour))
	if h, _ := anchor("from"); h != 50 {
		t.Fatalf("from after a chain reset: %d", h)
	}
	if h, _ := anchor("mid"); h != 0 {
		t.Fatalf("mid should be cleared after a chain reset: %d", h)
	}
}
