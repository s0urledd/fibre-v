package scan

import (
	"testing"

	abci "github.com/cometbft/cometbft/abci/types"
	"github.com/cosmos/cosmos-sdk/types/bech32"
)

func bech(t *testing.T, hexAddr string) string {
	t.Helper()
	raw := make([]byte, 20)
	for i := range raw {
		raw[i] = hexAddr[i%len(hexAddr)]
	}
	s, err := bech32.ConvertAndEncode("celestiavalcons", raw)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func hexOf(t *testing.T, b string) string {
	t.Helper()
	h, err := consHexOf(b)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func regEvent(addr, host string) abci.Event {
	return abci.Event{Type: eventSetFibreProviderInfo, Attributes: []abci.EventAttribute{
		{Key: attrValidatorConsAddress, Value: addr}, {Key: attrHost, Value: host},
	}}
}

// host_at_settlement is the newest registration at or before the settlement
// tx: two in one block are ordered by tx index, an event at or after the
// start overrides the seed, the seed stands only with no event, a validator
// nobody named has no host, and a scan gap between the newest entry and the
// settlement makes the answer unknown rather than the seed's.
func TestHostHistory(t *testing.T) {
	va, vb, vc := bech(t, "a"), bech(t, "b"), bech(t, "c")
	ha, hb := hexOf(t, va), hexOf(t, vb)
	h := NewHostHistory()
	h.Seed(100, []FibreProvider{{ConsAddressBech32: va, Host: "a-seed:1"}, {ConsAddressBech32: vb, Host: "b-seed:1"}}, HostFromSeed)

	// a parsed event lands as an entry effective after its tx
	addr, host, ok, err := parseSetFibreProviderInfo(regEvent(va, "a-new:1"))
	if err != nil || !ok || addr != ha || host != "a-new:1" {
		t.Fatalf("parse: %q %q %v %v", addr, host, ok, err)
	}
	if _, _, ok, _ := parseSetFibreProviderInfo(abci.Event{Type: "other"}); ok {
		t.Fatal("foreign event parsed as a registration")
	}
	if _, _, _, err := parseSetFibreProviderInfo(abci.Event{Type: eventSetFibreProviderInfo}); err == nil {
		t.Fatal("registration without attributes accepted")
	}
	if _, added := h.AddTxEvent(120, 3, ha, "a-new:1"); !added {
		t.Fatal("event not added")
	}
	if _, added := h.AddTxEvent(120, 3, ha, "a-new:1"); added {
		t.Fatal("replayed event added twice")
	}
	// twice in one block: tx order decides
	h.AddTxEvent(130, 1, hb, "b-first:1")
	h.AddTxEvent(130, 4, hb, "b-second:1")

	cases := []struct {
		addr       string
		height     int64
		tx         int
		want, from string
	}{
		{ha, 110, 0, "a-seed:1", HostFromSeed},   // before the event: seed
		{ha, 120, 3, "a-seed:1", HostFromSeed},   // the registering tx itself
		{ha, 120, 4, "a-new:1", HostFromEvent},   // the next tx in the block
		{ha, 500, 0, "a-new:1", HostFromEvent},   // long after
		{vb, 130, 2, "b-first:1", HostFromEvent}, // between the two
		{vb, 130, 9, "b-second:1", HostFromEvent},
		{vb, 131, 0, "b-second:1", HostFromEvent},
		{vc, 200, 0, "", HostUnknownNoSeed}, // seeded, but nobody asked about vc: unknown, not "none"
	}
	for _, c := range cases {
		addr := c.addr
		if len(addr) > 40 {
			addr = hexOf(t, addr)
		}
		got, from := h.HostAt(addr, c.height, c.tx, nil)
		if got != c.want || from != c.from {
			t.Errorf("HostAt(%s, %d, %d) = %q (%s), want %q (%s)", addr[:4], c.height, c.tx, got, from, c.want, c.from)
		}
	}

	// a gap between the newest entry and the settlement: unknown, never the
	// seed or the last event; a gap before the newest entry does not matter
	gaps := []ScanGap{{From: 300, To: 310}}
	if got, from := h.HostAt(ha, 400, 0, gaps); got != "" || from != HostUnknownGap {
		t.Errorf("after a gap = %q (%s), want unknown_gap", got, from)
	}
	if got, from := h.HostAt(ha, 250, 0, gaps); got != "a-new:1" || from != HostFromEvent {
		t.Errorf("before the gap = %q (%s)", got, from)
	}
	h.AddTxEvent(350, 0, ha, "a-after-gap:1")
	if got, from := h.HostAt(ha, 400, 0, gaps); got != "a-after-gap:1" || from != HostFromEvent {
		t.Errorf("event after the gap = %q (%s), want the event", got, from)
	}
	// a validator only the seed knows, with a gap since the seed: unknown
	if got, from := h.HostAt(hb, 400, 0, gaps); got != "" || from != HostUnknownGap {
		t.Errorf("seeded validator across a gap = %q (%s)", got, from)
	}
	// the gap right at the settlement height counts; one after it does not
	if _, from := h.HostAt(hb, 305, 0, gaps); from != HostUnknownGap {
		t.Errorf("settlement inside the gap = %s", from)
	}
	if _, from := h.HostAt(hb, 299, 0, gaps); from != HostFromEvent {
		t.Errorf("settlement before the gap = %s", from)
	}

	// a lazy seed: the chain asked once, at the seed height on; an explicit
	// empty answer is "none", and neither is re-asked (Known)
	vd := hexOf(t, bech(t, "d"))
	if h.Known(vd) {
		t.Fatal("unasked validator reported known")
	}
	h.SeedOne(vd, "d-lazy:1", HostFromSeedLazy, 100)
	if got, from := h.HostAt(vd, 200, 0, nil); got != "d-lazy:1" || from != HostFromSeedLazy || !h.Known(vd) {
		t.Errorf("lazy seed = %q (%s) known=%v", got, from, h.Known(vd))
	}
	ve := hexOf(t, bech(t, "e"))
	h.SeedOne(ve, "", HostFromSeedLazy, 100)
	if got, from := h.HostAt(ve, 200, 0, nil); got != "" || from != HostNone || !h.Known(ve) {
		t.Errorf("explicit none = %q (%s) known=%v", got, from, h.Known(ve))
	}
	// a seed read at the tip (state at the start pruned) holds from the
	// tip on; a settlement before it with no event is unknown, never the
	// tip's value
	c := NewHostHistory()
	c.Seed(5000, []FibreProvider{{ConsAddressBech32: va, Host: "a-now:1"}}, HostFromSeedCurrent)
	if got, from := c.HostAt(ha, 4000, 0, nil); got != "" || from != HostUnknownNoSeed {
		t.Errorf("before a current seed = %q (%s)", got, from)
	}
	if got, from := c.HostAt(ha, 5000, 0, nil); got != "a-now:1" || from != HostFromSeedCurrent {
		t.Errorf("at the current seed = %q (%s)", got, from)
	}
	c.SeedOne(hb, "b-now:1", HostFromSeedCurrent, 5200)
	if _, from := c.HostAt(hb, 5100, 0, nil); from != HostUnknownNoSeed {
		t.Errorf("before a lazy current seed = %s", from)
	}

	// no seed: nothing on record is unknown, not "no host"
	u := NewHostHistory()
	if _, from := u.HostAt(ha, 100, 0, nil); from != HostUnknownNoSeed {
		t.Errorf("unseeded = %s", from)
	}
	u.AddTxEvent(90, 0, ha, "a:1")
	if got, from := u.HostAt(ha, 100, 0, nil); got != "a:1" || from != HostFromEvent {
		t.Errorf("unseeded with an event = %q (%s)", got, from)
	}

	// persistence round trip keeps everything, including the seed marker
	seeded, at := h.Seeded()
	r := LoadHostHistory(h.Entries(), seeded, at)
	if got, from := r.HostAt(hb, 130, 9, nil); got != "b-second:1" || from != HostFromEvent {
		t.Errorf("reloaded = %q (%s)", got, from)
	}
	if got, from := r.HostAt(ve, 200, 0, nil); got != "" || from != HostNone {
		t.Errorf("reloaded explicit none lost: %q (%s)", got, from)
	}
}
