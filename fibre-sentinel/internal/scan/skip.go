package scan

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Operator skips: the escape hatch out of a crash loop.
//
// processBlock exits (log.Fatalf) on a block it cannot make sense of: a
// params event it cannot parse, a malformed provider registration, a
// publication it cannot build for a reason other than the node lacking the
// height, a block whose tx and result counts disagree. Each of those is a
// defect or a format change this build does not know, and guessing past it
// would record something wrong. But under systemd Restart=always the
// scanner comes back, resumes at the same height and dies on the same
// block, forever: the feed stops, and nothing short of a new build moves it.
//
// -skip-heights is the way through that does not need a build. A height
// listed there is not read at all; it is recorded as a scan gap, exactly
// like a height the node could not serve, with a reason saying the operator
// skipped it. A gap means "a publication settled here is unknown to this
// observer", never counted served or unserved, and the list is published
// (state.json, the API's scan_gaps, the health check), so a skip is public
// and never silent. Fixing the build and re-scanning the height afterwards
// is the same procedure as for any other gap (deploy/README.md, Runbook).

// SkipReason is the ScanGap.Reason of a height the operator skipped. A gap
// never merges across different reasons, so a skip next to a height the node
// could not serve stays its own range and keeps saying why.
const SkipReason = "skipped by the operator (-skip-heights): the block was not read; publications, params changes, host registrations and escrow movements in it are unknown to this observer"

// HeightRange is an inclusive run of heights, From <= To.
type HeightRange struct {
	From int64
	To   int64
}

func (r HeightRange) String() string {
	if r.From == r.To {
		return strconv.FormatInt(r.From, 10)
	}
	return fmt.Sprintf("%d-%d", r.From, r.To)
}

// ParseHeightRanges reads the -skip-heights value: comma-separated heights
// and inclusive ranges "a-b", spaces allowed around each item, e.g.
// "1234, 2000-2005". An empty (or all-blank) value is no skips, which is
// what an unset SKIP_HEIGHTS in the systemd unit expands to. Anything else
// that does not parse is an error rather than a partial list: a typo that
// quietly skipped the wrong height, or nothing, is exactly what this flag
// must never do.
func ParseHeightRanges(spec string) ([]HeightRange, error) {
	if strings.TrimSpace(spec) == "" {
		return nil, nil
	}
	var out []HeightRange
	for _, item := range strings.Split(spec, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			return nil, fmt.Errorf("empty item in %q", spec)
		}
		from, to := item, item
		if i := strings.Index(item, "-"); i >= 0 {
			from, to = strings.TrimSpace(item[:i]), strings.TrimSpace(item[i+1:])
		}
		a, err := strconv.ParseInt(from, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("%q: not a height or a range a-b", item)
		}
		b, err := strconv.ParseInt(to, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("%q: not a height or a range a-b", item)
		}
		if a < 1 || b < 1 {
			return nil, fmt.Errorf("%q: heights start at 1", item)
		}
		if a > b {
			return nil, fmt.Errorf("%q: range runs backwards", item)
		}
		out = append(out, HeightRange{From: a, To: b})
	}
	return out, nil
}

// skipListed reports whether the operator listed h in -skip-heights.
func (s *Scanner) skipListed(h int64) bool {
	for _, r := range s.cfg.SkipHeights {
		if h >= r.From && h <= r.To {
			return true
		}
	}
	return false
}

// gapCovers reports whether h is already inside a recorded gap.
func (s *Scanner) gapCovers(h int64) bool {
	for _, g := range s.gaps {
		if h >= g.From && h <= g.To {
			return true
		}
	}
	return false
}

// skipHeight records an operator-skipped height as a scan gap without
// reading the block's contents. The header alone is asked for once, without
// retries, so the gap can be placed on the chain's clock; if even that fails
// the gap carries no block time and readers fall back to the scanner's own
// clock (ScanGap.Spans), the conservative direction.
//
// Idempotent: the flag may stay set long after the scanner has passed the
// height. resume() never comes back to a height below the persisted cursor,
// and gaps are persisted in the same state.json write as the cursor, so a
// height is normally met once; should it be met again with its gap already
// on record (a re-scan over it with the flag still set), nothing is added.
func (s *Scanner) skipHeight(ctx context.Context, h int64) {
	if s.gapCovers(h) {
		s.log.Printf("SKIP h=%d: listed in -skip-heights and already on record as a gap; not read", h)
		return
	}
	var bt time.Time
	if t, err := s.chain.headerTime(ctx, h); err == nil {
		bt = t
		s.lastBlockTime = t.UTC()
	} else {
		s.log.Printf("SKIP h=%d: header time not read (%v); the gap is placed on the scanner's clock", h, err)
	}
	s.addGap(h, SkipReason, "not read: height listed in -skip-heights", bt)
	s.log.Printf("WARNING: SKIP h=%d not scanned: listed in -skip-heights by the operator; recorded as a scan gap and published (%d gap ranges so far)", h, len(s.gaps))
	s.status.Error(fmt.Sprintf("operator skipped h=%d (-skip-heights): published as a scan gap", h))
}

// skipHint is appended to every processBlock exit an operator skip can get
// past, so the crash dump at the top of each restart names the way out.
func skipHint(h int64) string {
	return fmt.Sprintf("; to move past this height, restart with -skip-heights %d (SKIP_HEIGHTS=%d in the env file); it is published as a scan gap, see deploy/README.md", h, h)
}
