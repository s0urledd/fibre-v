package api

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/plsgiveup/fibre/fibre-sentinel/observer/store"
)

// The snapshot's whole purpose is that a reader is not made to wait for a
// window aggregate, so what has to be true is: the second reader does not
// recompute, the age is published rather than implied, and a window's own
// snapshot is its own.

func testWindow(name string) Window { return windowFor(name, time.Now()) }

func newSnapshotServer(t *testing.T) *Server {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "observer.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	s := &Server{st: st, vantage: "test"}
	s.net = newSnapshotCache("network", s.computeNetwork)
	return s
}

func TestSnapshotServesWithoutRecomputing(t *testing.T) {
	s := newSnapshotServer(t)
	ctx := context.Background()
	win := testWindow("24h")

	first, firstAt, ms, err := s.net.get(ctx, nil, win)
	if err != nil {
		t.Fatal(err)
	}
	if firstAt.IsZero() {
		t.Error("the snapshot does not say when it was computed, so its age cannot be published")
	}
	_ = ms

	// Take the snapshot's identity before the second call and after it: a
	// second reader inside the TTL must be handed the same computation.
	s.net.mu.Lock()
	before := s.net.entries[win.Name]
	s.net.mu.Unlock()

	second, secondAt, _, err := s.net.get(ctx, nil, win)
	if err != nil {
		t.Fatal(err)
	}
	s.net.mu.Lock()
	after := s.net.entries[win.Name]
	s.net.mu.Unlock()

	if before != after {
		t.Error("a second request inside the TTL recomputed the snapshot")
	}
	if !secondAt.Equal(firstAt) {
		t.Errorf("the second read reports a different computation time: %v then %v", firstAt, secondAt)
	}
	if first != second {
		t.Error("a second read inside the TTL returned a different value")
	}
}

func TestSnapshotIsPerWindow(t *testing.T) {
	s := newSnapshotServer(t)
	ctx := context.Background()
	for _, name := range []string{"24h", "7d"} {
		win := testWindow(name)
		resp, _, _, err := s.net.get(ctx, nil, win)
		if err != nil {
			t.Fatal(err)
		}
		if resp.Window.Name != name {
			t.Errorf("asked for window %q and got %q", name, resp.Window.Name)
		}
	}
	s.net.mu.Lock()
	defer s.net.mu.Unlock()
	if len(s.net.entries) != 2 {
		t.Errorf("two windows produced %d snapshots", len(s.net.entries))
	}
}

// A stale snapshot is served as it stands while its replacement is computed, so
// the reader of a stale window waits no longer than the reader of a fresh one.
func TestStaleSnapshotIsServedWhileRefreshing(t *testing.T) {
	s := newSnapshotServer(t)
	ctx := context.Background()
	win := testWindow("24h")
	if _, _, _, err := s.net.get(ctx, nil, win); err != nil {
		t.Fatal(err)
	}

	// Age the snapshot past its TTL.
	s.net.mu.Lock()
	snap := s.net.entries[win.Name]
	stale := snap.at.Add(-2 * ttlFor(win.Name))
	snap.at = stale
	s.net.mu.Unlock()

	_, at, _, err := s.net.get(ctx, nil, win)
	if err != nil {
		t.Fatal(err)
	}
	if !at.Equal(stale) {
		t.Errorf("a stale snapshot was not served as it stands: taken %v, wanted %v", at, stale)
	}

	// The refresh it triggered should replace the snapshot shortly. On an empty
	// store that is fast; the deadline is generous because this asserts that a
	// refresh happens, not how quickly.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		s.net.mu.Lock()
		got := s.net.entries[win.Name]
		s.net.mu.Unlock()
		if got != nil && got.at.After(stale) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Error("a stale snapshot was served but no refresh replaced it")
}

func TestSnapshotTTLGrowsWithTheWindow(t *testing.T) {
	// A minute of new data moves a day's figure and does not move a month's, so
	// the month must not be recomputed as often as the day.
	if ttlFor("24h") >= ttlFor("7d") || ttlFor("7d") >= ttlFor("30d") || ttlFor("30d") >= ttlFor("all") {
		t.Errorf("TTLs do not increase with the window: 24h=%v 7d=%v 30d=%v all=%v",
			ttlFor("24h"), ttlFor("7d"), ttlFor("30d"), ttlFor("all"))
	}
	if ttlFor("something-new") != ttlFor("all") {
		t.Error("an unrecognised window should get the most conservative refresh rate, not the cheapest")
	}
}

// A cache younger than the long windows' TTLs refreshes them faster: a fresh
// deployment's "all" window is minutes of data, not months.
func TestSnapshotTTLScalesWithCacheAge(t *testing.T) {
	c := newSnapshotCache("t", func(context.Context, Window) (int, error) { return 0, nil })
	if got := c.ttl("all"); got != time.Minute {
		t.Fatalf("new cache: ttl(all) = %s, want 1m", got)
	}
	c.born = time.Now().Add(-100 * time.Minute)
	if got := c.ttl("all"); got != 10*time.Minute {
		t.Fatalf("100 min old: ttl(all) = %s, want 10m", got)
	}
	c.born = time.Now().Add(-48 * time.Hour)
	if got := c.ttl("all"); got != ttlFor("all") {
		t.Fatalf("two days old: ttl(all) = %s, want %s", got, ttlFor("all"))
	}
	if got := c.ttl("24h"); got != time.Minute {
		t.Fatalf("24h never below its own floor: %s", got)
	}
}
