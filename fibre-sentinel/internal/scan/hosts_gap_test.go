package scan

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	fibretypes "github.com/celestiaorg/celestia-app/v10/x/fibre/types"
	valaddrtypes "github.com/celestiaorg/celestia-app/v10/x/valaddr/types"
	abci "github.com/cometbft/cometbft/abci/types"
)

const pathProviderInfo = "/celestia.valaddr.v1.Query/FibreProviderInfo"

// A gap whose blocks were read, events and all — only a publication's
// validator set at its promise height was pruned — lost no registration.
// It used to count like any other gap: one pruned promise height made every
// later host_at_settlement unknown_gap, for every validator, for good.
func TestAGapThatReadTheEventsDoesNotMakeHostsUnknown(t *testing.T) {
	va := bech(t, "a")
	ha := hexOf(t, va)
	h := NewHostHistory()
	h.Seed(100, []FibreProvider{{ConsAddressBech32: va, Host: "a:1"}}, HostFromSeed)

	read := []ScanGap{{From: 150, To: 150, HostEventsRead: true}}
	if got, from := h.HostAt(ha, 400, 0, read); got != "a:1" || from != HostFromSeed {
		t.Fatalf("after a gap that read its events = %q (%s), want the seed", got, from)
	}
	// the same height as an event-losing gap: unknown, as before
	lost := []ScanGap{{From: 150, To: 150}}
	if got, from := h.HostAt(ha, 400, 0, lost); got != "" || from != HostUnknownGap {
		t.Fatalf("after a gap that lost events = %q (%s), want unknown_gap", got, from)
	}
	// LastHostLossGapEnd sees only the event-losing ones, and only below h
	gaps := []ScanGap{{From: 10, To: 20}, {From: 30, To: 30, HostEventsRead: true}, {From: 40, To: 45}}
	for _, c := range []struct {
		h    int64
		want int64
		ok   bool
	}{{15, 0, false}, {21, 20, true}, {35, 20, true}, {45, 20, true}, {46, 45, true}} {
		if got, ok := LastHostLossGapEnd(gaps, c.h); got != c.want || ok != c.ok {
			t.Errorf("LastHostLossGapEnd(%d) = %d %v, want %d %v", c.h, got, ok, c.want, c.ok)
		}
	}
}

// A re-seed after an event-losing gap: known again from the first height
// after the gap, never inside or before it; a validator whose own event
// falls between the gap and the read is not back-dated; the explicit
// "none" of a one-validator re-read stays "none"; replays add nothing.
func TestAReseedRestoresHostsAfterTheGapOnly(t *testing.T) {
	va, vb, vc := bech(t, "a"), bech(t, "b"), bech(t, "c")
	ha, hb, hc := hexOf(t, va), hexOf(t, vb), hexOf(t, vc)
	h := NewHostHistory()
	h.Seed(100, []FibreProvider{{ConsAddressBech32: va, Host: "a:1"}, {ConsAddressBech32: vb, Host: "b:1"}}, HostFromSeed)
	gaps := []ScanGap{{From: 200, To: 210}}
	// b re-registers after the gap, before the re-read
	h.AddTxEvent(215, 2, hb, "b:2")

	added := h.Reseed(211, 219, []FibreProvider{{ConsAddressBech32: va, Host: "a:2"}, {ConsAddressBech32: vb, Host: "b:2"}})
	if len(added) != 1 || added[0].ConsAddress != ha || added[0].Source != HostFromReseed || added[0].FromHeight != 211 || added[0].FromTxIndex != -1 {
		t.Fatalf("re-seed entries: %+v", added)
	}
	if !h.ReseededAt(211) || h.ReseededAt(212) {
		t.Fatal("ReseededAt does not track the re-seed")
	}
	cases := []struct {
		addr       string
		height     int64
		want, from string
	}{
		{ha, 150, "a:1", HostFromSeed},    // before the gap: the seed, as before
		{ha, 205, "", HostUnknownGap},     // inside the gap: still unknown
		{ha, 210, "", HostUnknownGap},     // the gap's last height
		{ha, 211, "a:2", HostFromReseed},  // right after the gap
		{ha, 5000, "a:2", HostFromReseed}, // and on
		{hb, 212, "", HostUnknownGap},     // b changed at 215: before it, unknown, not the re-read value
		{hb, 216, "b:2", HostFromEvent},   // after its own event: the event
		{hc, 300, "", HostUnknownGap},     // not in the re-read: absence is not "none"
	}
	for _, c := range cases {
		if got, from := h.HostAt(c.addr, c.height, 0, gaps); got != c.want || from != c.from {
			t.Errorf("HostAt(%s, %d) = %q (%s), want %q (%s)", c.addr[:4], c.height, got, from, c.want, c.from)
		}
	}
	// one validator read on its own: the chain says not registered
	if e, ok := h.ReseedOne(hc, "", 211, 299); !ok || e.Source != HostFromReseed {
		t.Fatalf("ReseedOne: %+v %v", e, ok)
	}
	if got, from := h.HostAt(hc, 300, 0, gaps); got != "" || from != HostNone {
		t.Errorf("explicit none after re-read = %q (%s)", got, from)
	}
	// a replay of the same re-seed adds nothing
	if again := h.Reseed(211, 219, []FibreProvider{{ConsAddressBech32: va, Host: "a:2"}}); len(again) != 0 {
		t.Fatalf("replayed re-seed added %+v", again)
	}
	// survives persistence
	seeded, at := h.Seeded()
	r := LoadHostHistory(h.Entries(), seeded, at)
	if got, from := r.HostAt(ha, 400, 0, gaps); got != "a:2" || from != HostFromReseed || !r.ReseededAt(211) {
		t.Errorf("reloaded = %q (%s)", got, from)
	}
}

// A pruned promise height is recorded as a gap that read its events, and
// never merges with a neighbouring gap that did not.
func TestAPublicationGapIsMarkedAndKeptApartFromEventLosingGaps(t *testing.T) {
	s := &Scanner{log: NewLogger(10)}
	unavail := &ErrHeightUnavailable{Height: 9, Err: errors.New("pruned")}
	if !s.recordGap(9, unavail, time.Time{}) || !s.recordPublicationGap(10, unavail, time.Time{}) ||
		!s.recordPublicationGap(11, unavail, time.Time{}) || !s.recordGap(12, unavail, time.Time{}) {
		t.Fatal("gap not recorded")
	}
	if len(s.gaps) != 3 || s.gaps[0].HostEventsRead || !s.gaps[1].HostEventsRead || s.gaps[1].From != 10 || s.gaps[1].To != 11 || s.gaps[2].HostEventsRead {
		t.Fatalf("gaps: %+v", s.gaps)
	}
}

// End to end through the scanner and a node: after a gap that lost events,
// the bonded registry is read once at the height before the scan's
// position and recorded from the gap's end; a failed read is not repeated
// on every block but is retried; a validator the bulk read missed is read
// on its own, once per gap.
func TestTheScannerReseedsTheHostHistoryAfterAnEventLosingGap(t *testing.T) {
	node := newFakeNode(t)
	bechA, addrA := consAddr(t, 0xaa)
	_, addrC := consAddr(t, 0xcc)
	params, _ := (&fibretypes.QueryParamsResponse{Params: fibretypes.DefaultParams()}).Marshal()
	seed, _ := (&valaddrtypes.QueryAllBondedFibreProvidersResponse{Providers: []valaddrtypes.FibreProvider{
		{ValidatorConsensusAddress: bechA, Info: valaddrtypes.FibreProviderInfo{Host: "a.example:9090"}},
	}}).Marshal()
	later, _ := (&valaddrtypes.QueryAllBondedFibreProvidersResponse{Providers: []valaddrtypes.FibreProvider{
		{ValidatorConsensusAddress: bechA, Info: valaddrtypes.FibreProviderInfo{Host: "a-moved.example:9090"}},
	}}).Marshal()
	notFound, _ := (&valaddrtypes.QueryFibreProviderInfoResponse{Found: false}).Marshal()
	var mu sync.Mutex // the node answers on its own goroutines
	failing := true
	var askedAt []int64
	node.answer[pathFibreParams] = okValue(params)
	node.answer[pathProviders] = func(h int64) abci.ResponseQuery {
		if h == 50 {
			return abci.ResponseQuery{Code: 0, Value: seed, Height: h}
		}
		mu.Lock()
		defer mu.Unlock()
		askedAt = append(askedAt, h)
		if failing {
			return abci.ResponseQuery{Code: 1, Codespace: "sdk", Log: "timeout", Height: h}
		}
		return abci.ResponseQuery{Code: 0, Value: later, Height: h}
	}
	node.answer[pathProviderInfo] = okValue(notFound)
	asked := func() []int64 {
		mu.Lock()
		defer mu.Unlock()
		return append([]int64(nil), askedAt...)
	}
	s := freshScanner(t, node, t.TempDir())
	defer s.store.Close()
	ctx := context.Background()
	if _, err := s.resume(ctx, 60); err != nil {
		t.Fatal(err)
	}

	// no event-losing gap: nothing is read
	s.gaps = []ScanGap{{From: 55, To: 55, HostEventsRead: true}}
	s.maybeReseedHosts(ctx, 60)
	if len(asked()) != 0 {
		t.Fatalf("re-read with no event-losing gap: %v", asked())
	}

	s.gaps = append(s.gaps, ScanGap{From: 60, To: 70})
	s.maybeReseedHosts(ctx, 71) // fails
	s.maybeReseedHosts(ctx, 72) // not retried on the next block
	if len(asked()) != 1 || asked()[0] != 70 {
		t.Fatalf("registry asked at %v, want once at 70 (the state after the gap's last block)", asked())
	}
	if _, from := s.hosts.HostAt(addrA, 80, 0, s.gaps); from != HostUnknownGap {
		t.Fatalf("a failed re-read made the host known: %s", from)
	}
	// while the bulk read fails, no validator is asked about on its own
	if s.reseedOne(ctx, addrA, 80) || node.count(pathProviderInfo) != 0 {
		t.Fatal("one-validator re-read tried before the bulk read for the gap worked")
	}
	mu.Lock()
	failing = false
	mu.Unlock()
	s.maybeReseedHosts(ctx, 71+inactiveRetryEvery) // retried on the cadence
	if len(asked()) != 2 || asked()[1] != 70+inactiveRetryEvery {
		t.Fatalf("registry asked at %v", asked())
	}
	if got, from := s.hosts.HostAt(addrA, 71, 0, s.gaps); got != "a-moved.example:9090" || from != HostFromReseed {
		t.Fatalf("after the re-seed = %q (%s)", got, from)
	}
	if got, from := s.hosts.HostAt(addrA, 65, 0, s.gaps); got != "" || from != HostUnknownGap {
		t.Fatalf("inside the gap after the re-seed = %q (%s)", got, from)
	}
	s.maybeReseedHosts(ctx, 500) // done: not asked again
	if len(asked()) != 2 {
		t.Fatalf("registry re-read after it succeeded: %v", asked())
	}
	// a new process knows it from the entries on record
	s.reseedFor = 0
	s.maybeReseedHosts(ctx, 600)
	if len(asked()) != 2 || s.reseedFor != 70 {
		t.Fatalf("after a restart: asked at %v, reseedFor=%d", asked(), s.reseedFor)
	}

	// c: not bonded at the read, known only from before the gap
	s.hosts.SeedOne(addrC, "c.example:9090", HostFromSeedLazy, 50)
	if !s.reseedOne(ctx, addrC, 200) {
		t.Fatal("one-validator re-read added nothing")
	}
	if got, from := s.hosts.HostAt(addrC, 200, 0, s.gaps); got != "" || from != HostNone {
		t.Fatalf("c after its re-read = %q (%s), want the chain's explicit none", got, from)
	}
	n := node.count(pathProviderInfo)
	s.reseedOne(ctx, addrC, 201)
	if node.count(pathProviderInfo) != n {
		t.Fatal("one-validator re-read repeated for the same gap")
	}
}
