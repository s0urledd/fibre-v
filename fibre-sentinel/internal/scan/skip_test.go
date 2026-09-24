package scan

import (
	"context"
	"errors"
	"flag"
	"io"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestParseHeightRanges(t *testing.T) {
	for _, c := range []struct {
		in   string
		want []HeightRange
	}{
		{"", nil},
		{"   ", nil},
		{"5", []HeightRange{{5, 5}}},
		{"1234, 2000-2005", []HeightRange{{1234, 1234}, {2000, 2005}}},
		{" 10 - 12 ,7", []HeightRange{{10, 12}, {7, 7}}},
		{"9-9", []HeightRange{{9, 9}}},
	} {
		got, err := ParseHeightRanges(c.in)
		if err != nil || !reflect.DeepEqual(got, c.want) {
			t.Errorf("%q: got %v err %v, want %v", c.in, got, err, c.want)
		}
	}
	// Anything that does not parse is refused whole: a typo must never
	// skip the wrong height, or quietly skip nothing.
	for _, in := range []string{"abc", "5,", ",5", "5,,6", "0", "-5", "5-", "9-3", "1-2-3", "5;6", "1e3", "0x10"} {
		if got, err := ParseHeightRanges(in); err == nil {
			t.Errorf("%q: accepted as %v", in, got)
		}
	}
}

// The systemd unit passes -skip-heights=${SKIP_HEIGHTS}, and docker compose
// the same with a default of empty. An unset variable must come out as no
// skips, and must not eat the flag after it.
func TestAnEmptySkipHeightsFlagIsNoSkips(t *testing.T) {
	for _, args := range [][]string{
		{"-skip-heights=", "-follow"},
		{"-skip-heights", "", "-follow"},
		{"-follow"},
	} {
		fs := flag.NewFlagSet("sentinel-scan", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		skip := fs.String("skip-heights", "", "")
		follow := fs.Bool("follow", false, "")
		if err := fs.Parse(args); err != nil {
			t.Fatalf("%q: %v", args, err)
		}
		got, err := ParseHeightRanges(*skip)
		if err != nil || got != nil || !*follow || fs.NArg() != 0 {
			t.Fatalf("%q: skips=%v err=%v follow=%v rest=%q", args, got, err, *follow, fs.Args())
		}
	}
}

// A height the operator skipped is a scan gap of its own: it never merges
// into a run the node could not serve, nor the other way round, so each
// range keeps saying why it is there.
func TestAnOperatorSkipNeverMergesWithAnUnavailableGap(t *testing.T) {
	s := &Scanner{log: NewLogger(10)}
	unavail := &ErrHeightUnavailable{Height: 9, Err: errors.New("finalize block responses not persisted")}
	if !s.recordGap(9, unavail, time.Time{}) {
		t.Fatal("unavailable height not recorded")
	}
	s.addGap(10, SkipReason, "x", time.Time{})
	s.addGap(11, SkipReason, "x", time.Time{})
	if !s.recordGap(12, unavail, time.Time{}) {
		t.Fatal("unavailable height not recorded")
	}
	if len(s.gaps) != 3 || s.gaps[1].From != 10 || s.gaps[1].To != 11 || s.gaps[1].Reason != SkipReason ||
		s.gaps[0].To != 9 || s.gaps[2].From != 12 {
		t.Fatalf("gaps: %+v", s.gaps)
	}
}

// The escape hatch end to end, through Run as sentinel-scan drives it: the
// listed heights are not read (no block, no block_results), each is on
// record in state.json as a scan gap with the operator's reason and the
// block's time from its header, every other height is read as usual, and a
// restart with the flag still set — past the heights now — adds nothing.
func TestAnOperatorSkippedHeightIsAPublishedGapAndIsNotRead(t *testing.T) {
	node := newUpgradeNode(t, 1000, 1012, nil)
	dir := t.TempDir()
	skips := []HeightRange{{1005, 1005}, {1007, 1008}}
	run := func() *Scanner {
		t.Helper()
		s, err := New(Config{RPCURL: node.srv.URL, DataDir: dir, StartHeight: 1000, RPCTimeout: 2 * time.Second, SkipHeights: skips}, NewLogger(400))
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := s.Run(ctx); err != nil {
			t.Fatalf("run: %v", err)
		}
		return s
	}
	state := func() *PersistState {
		t.Helper()
		st, err := OpenStore(dir)
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		ps, err := st.LoadState()
		if err != nil || ps == nil {
			t.Fatalf("state: %v", err)
		}
		return ps
	}

	run()
	ps := state()
	if ps.LastScannedHeight != 1012 {
		t.Fatalf("last scanned %d, want 1012: the scan did not move past the skips", ps.LastScannedHeight)
	}
	if len(ps.Gaps) != 2 {
		t.Fatalf("gaps: %+v", ps.Gaps)
	}
	blockTime := func(h int64) time.Time {
		return time.Date(2026, 9, 24, 18, 0, 0, 0, time.UTC).Add(time.Duration(h) * 6 * time.Second)
	}
	for i, want := range skips {
		g := ps.Gaps[i]
		if g.From != want.From || g.To != want.To || g.Reason != SkipReason || !strings.Contains(g.Reason, "operator") {
			t.Fatalf("gap %d: %+v, want %v skipped by the operator", i, g, want)
		}
		if g.FromTime == nil || !g.FromTime.Equal(blockTime(want.From)) || g.ToTime == nil || !g.ToTime.Equal(blockTime(want.To)) {
			t.Fatalf("gap %d not placed on the chain's clock: %+v", i, g)
		}
	}
	for h := int64(1000); h <= 1012; h++ {
		listed := h == 1005 || h == 1007 || h == 1008
		b, r := node.count("block@"+itoa(h)), node.count("block_results@"+itoa(h))
		if listed && (b != 0 || r != 0) {
			t.Fatalf("skipped height %d was read: block=%d block_results=%d", h, b, r)
		}
		if !listed && (b != 1 || r != 1) {
			t.Fatalf("height %d not listed, yet read block=%d block_results=%d times", h, b, r)
		}
	}

	// Restart with the flag still set, the chain a little further on: the
	// skips are behind the cursor and change nothing.
	node.mu.Lock()
	node.tip = 1015
	node.mu.Unlock()
	s := run()
	ps2 := state()
	if ps2.LastScannedHeight != 1015 || !reflect.DeepEqual(ps2.Gaps, ps.Gaps) {
		t.Fatalf("after restart: last=%d gaps=%+v, want 1015 and the same two gaps", ps2.LastScannedHeight, ps2.Gaps)
	}
	// And a skipped height met again with its gap already on record (a
	// re-scan over it with the flag left set) is not recorded twice, nor
	// read.
	headers := node.count("header@1005")
	s.processBlock(context.Background(), 1005)
	if !reflect.DeepEqual(s.gaps, ps.Gaps) || node.count("header@1005") != headers || node.count("block_results@1005") != 0 {
		t.Fatalf("a second pass over a skipped height changed the record: %+v", s.gaps)
	}
}

func itoa(h int64) string { return strconv.FormatInt(h, 10) }
