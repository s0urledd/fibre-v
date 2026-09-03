package probe

import (
	"time"

	"github.com/plsgiveup/fibre/fibre-sentinel/internal/scan"
)

// Phase labels where in a publication's life a scheduled probe falls.
type Phase string

const (
	// PhaseInWindow: before must_serve_until. The validator is under its
	// retention obligation; NOT_FOUND here is a fault.
	PhaseInWindow Phase = "in_window"
	// PhaseGrace: from must_serve_until to must_serve_until + prune tolerance.
	// The blob may already be pruned (measured lag ~1-2 min); NOT_FOUND is
	// tolerated, SERVED_OK is still fine.
	PhaseGrace Phase = "grace"
	// PhasePost: after the tolerance. The blob is expected to be gone;
	// NOT_FOUND is the expected result.
	PhasePost Phase = "post"
)

// ScheduleConfig controls how probe times are derived from a publication.
// Nothing here is a fixed interval — the points are fractions of that
// publication's own retention window plus two points past its end.
type ScheduleConfig struct {
	// InWindowFractions are positions inside [settlement, must_serve_until],
	// as fractions of that span. They are clustered toward the end, where a
	// retention breach is most likely. Empty -> DefaultInWindowFractions.
	InWindowFractions []float64
	// GraceOffset is how far past must_serve_until the grace probe sits.
	GraceOffset time.Duration
	// PruneTolerance is how long after must_serve_until a NOT_FOUND is still
	// considered normal. Measured on the devnet at must_serve_until + ~1m45s
	// (60s prune loop + minute-resolution prune key); default 2m30s leaves
	// margin. This is also the grace/post phase boundary.
	PruneTolerance time.Duration
	// PostMargin is how far past (must_serve_until + PruneTolerance) the final
	// "expected gone" probe sits.
	PostMargin time.Duration
	// MinSpacing drops schedule points that would land within this of an
	// earlier one (keeps a very short window from generating a burst).
	MinSpacing time.Duration
}

// DefaultInWindowFractions: one early reading, then three clustered toward the
// deadline.
var DefaultInWindowFractions = []float64{0.12, 0.45, 0.72, 0.92}

// DefaultScheduleConfig fills the zero value.
func DefaultScheduleConfig() ScheduleConfig {
	return ScheduleConfig{
		InWindowFractions: DefaultInWindowFractions,
		GraceOffset:       30 * time.Second,
		PruneTolerance:    150 * time.Second,
		PostMargin:        60 * time.Second,
		MinSpacing:        20 * time.Second,
	}
}

func (c ScheduleConfig) withDefaults() ScheduleConfig {
	d := DefaultScheduleConfig()
	if len(c.InWindowFractions) == 0 {
		c.InWindowFractions = d.InWindowFractions
	}
	if c.GraceOffset <= 0 {
		c.GraceOffset = d.GraceOffset
	}
	if c.PruneTolerance <= 0 {
		c.PruneTolerance = d.PruneTolerance
	}
	if c.PostMargin <= 0 {
		c.PostMargin = d.PostMargin
	}
	if c.MinSpacing < 0 {
		c.MinSpacing = d.MinSpacing
	}
	return c
}

// SchedulePoint is one moment the Sentinel should probe a publication's
// assigned validators.
type SchedulePoint struct {
	At    time.Time
	Phase Phase
	// Label is a short human tag ("w1".."wN", "grace", "post").
	Label string
}

// ScheduleFor derives the probe schedule for one publication. The window is
// [settlement_time, must_serve_until]; must_serve_until itself comes from the
// record (creation + max(payment_promise_timeout, shard_retention)), so the
// schedule follows the on-chain params in force when the blob was published.
func ScheduleFor(p scan.Publication, cfg ScheduleConfig) []SchedulePoint {
	cfg = cfg.withDefaults()

	start := p.SettlementTime
	msu := p.MustServeUntil
	if !msu.After(start) {
		// Degenerate record (params or clocks odd): fall back to a 10m window
		// anchored at must_serve_until so we still get useful points.
		start = msu.Add(-10 * time.Minute)
	}
	span := msu.Sub(start)

	var pts []SchedulePoint
	for i, f := range cfg.InWindowFractions {
		if f <= 0 || f >= 1 {
			continue
		}
		pts = append(pts, SchedulePoint{
			At:    start.Add(time.Duration(float64(span) * f)),
			Phase: PhaseInWindow,
			Label: "w" + itoa(i+1),
		})
	}
	pts = append(pts,
		SchedulePoint{At: msu.Add(cfg.GraceOffset), Phase: PhaseGrace, Label: "grace"},
		SchedulePoint{At: msu.Add(cfg.PruneTolerance + cfg.PostMargin), Phase: PhasePost, Label: "post"},
	)

	// enforce ordering + MinSpacing.
	sortByTime(pts)
	if cfg.MinSpacing > 0 {
		out := pts[:0:0]
		var last time.Time
		for _, pt := range pts {
			if !last.IsZero() && pt.At.Sub(last) < cfg.MinSpacing {
				continue
			}
			out = append(out, pt)
			last = pt.At
		}
		pts = out
	}
	return pts
}

// PhaseAt classifies an arbitrary instant against a publication's window, using
// the same tolerance the schedule used. Used by the classifier on the ACTUAL
// probe start time (not the scheduled one).
func PhaseAt(t time.Time, p scan.Publication, cfg ScheduleConfig) Phase {
	cfg = cfg.withDefaults()
	return PhaseAtWindow(t, p.MustServeUntil, cfg.PruneTolerance)
}

// PhaseAtWindow is PhaseAt without a Publication — just the boundaries.
func PhaseAtWindow(t, mustServeUntil time.Time, pruneTolerance time.Duration) Phase {
	switch {
	case t.Before(mustServeUntil):
		return PhaseInWindow
	case !t.After(mustServeUntil.Add(pruneTolerance)):
		return PhaseGrace
	default:
		return PhasePost
	}
}

func sortByTime(pts []SchedulePoint) {
	for i := 1; i < len(pts); i++ {
		for j := i; j > 0 && pts[j].At.Before(pts[j-1].At); j-- {
			pts[j], pts[j-1] = pts[j-1], pts[j]
		}
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b [20]byte
	n := len(b)
	for i > 0 {
		n--
		b[n] = byte('0' + i%10)
		i /= 10
	}
	return string(b[n:])
}
