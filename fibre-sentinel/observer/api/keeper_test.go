package api

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// A snapshot nobody reads must still be refreshed as its TTL runs out:
// otherwise the reader who finally triggers the refresh is handed the old
// figure whole, and on a quiet site that figure can be a day old.
func TestKeeperRefreshesAStaleWindowWithoutARead(t *testing.T) {
	var n atomic.Int64
	c := newSnapshotCache("t", func(ctx context.Context, win Window) (int64, error) {
		return n.Add(1), nil
	})
	now := time.Now()
	c.refreshDue(nil, now)
	if got := n.Load(); got != int64(len(warmWindows)) {
		t.Fatalf("an empty cache: %d computations, want one per window (%d)", got, len(warmWindows))
	}
	// nothing is due inside the TTL
	c.refreshDue(nil, now)
	if got := n.Load(); got != int64(len(warmWindows)) {
		t.Fatalf("a fresh cache was recomputed: %d computations", got)
	}
	// age the 24h window past its TTL and only that one is recomputed
	c.mu.Lock()
	c.entries["24h"].at = now.Add(-2 * time.Hour)
	c.mu.Unlock()
	c.refreshDue(nil, time.Now())
	if got := n.Load(); got != int64(len(warmWindows))+1 {
		t.Fatalf("after aging 24h: %d computations, want %d", got, len(warmWindows)+1)
	}
	c.mu.Lock()
	at := c.entries["24h"].at
	c.mu.Unlock()
	if time.Since(at) > time.Minute {
		t.Errorf("the stale 24h snapshot was not replaced (taken %s)", at)
	}
}

// A window being refreshed by a reader is left alone: the keeper must not
// stack a second computation of the same aggregate behind it.
func TestKeeperSkipsAWindowAlreadyRefreshing(t *testing.T) {
	var n atomic.Int64
	c := newSnapshotCache("t", func(ctx context.Context, win Window) (int64, error) {
		return n.Add(1), nil
	})
	c.mu.Lock()
	for _, w := range warmWindows {
		c.refreshing[w] = true
	}
	c.mu.Unlock()
	c.refreshDue(nil, time.Now())
	if got := n.Load(); got != 0 {
		t.Fatalf("%d computations started behind ones already running", got)
	}
}

// Every figure computed before Fibre went live describes a chain without
// Fibre. At activation those snapshots are dropped, not served for the rest
// of their TTL as "nothing happened".
func TestActivationDropsPreActivationSnapshots(t *testing.T) {
	s := newSnapshotServer(t)
	if err := s.st.SetMeta("fibre_active", "no", time.Now()); err != nil {
		t.Fatal(err)
	}
	var n atomic.Int64
	c := newSnapshotCache("market", func(ctx context.Context, win Window) (int64, error) {
		return n.Add(1), nil
	})
	c.revision = s.activationRevision
	ctx := context.Background()
	win := testWindow("30d")
	if v, _, _, err := c.get(ctx, nil, win); err != nil || v != 1 {
		t.Fatalf("first read: %d, %v", v, err)
	}
	if v, _, _, _ := c.get(ctx, nil, win); v != 1 {
		t.Fatalf("a read inside the TTL recomputed: %d", v)
	}
	if err := s.st.SetMeta("fibre_active", "yes", time.Now()); err != nil {
		t.Fatal(err)
	}
	if v, _, _, err := c.get(ctx, nil, win); err != nil || v != 2 {
		t.Fatalf("after activation the pre-activation snapshot was served (%d, %v)", v, err)
	}
}

// The verdict snapshots carry both invalidations: a hold and activation.
func TestSnapshotRevisionCarriesHoldsAndActivation(t *testing.T) {
	s := newSnapshotServer(t)
	before := s.snapshotRevision()
	if err := s.st.SetMeta("fibre_active", "yes", time.Now()); err != nil {
		t.Fatal(err)
	}
	if s.snapshotRevision() == before {
		t.Error("activation did not change the revision the network and validator snapshots are served under")
	}
}
