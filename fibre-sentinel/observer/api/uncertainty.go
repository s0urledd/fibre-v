package api

import (
	"context"

	"github.com/plsgiveup/fibre/fibre-sentinel/internal/scan"
	"github.com/plsgiveup/fibre/fibre-sentinel/observer/store"
)

// MetaParamHoldsRev is the meta key the collector bumps whenever a hold is
// raised or lifted or a correction moves a verdict. It is what the cached
// aggregates key their validity on.
const MetaParamHoldsRev = store.MetaParamHoldsRev

// paramHoldsRevision is one cheap indexed row read, taken once per cached
// window read. It has to be a read and not a push because the collector
// writes the database and this process only reads it: there is no channel
// between them but the store itself.
func (s *Server) paramHoldsRevision() string {
	v, err := s.st.Meta(MetaParamHoldsRev)
	if err != nil {
		// Unreadable: treat every cached snapshot as suspect rather than
		// serve one that a hold may already have contradicted. A database
		// this process cannot read is an outage either way.
		return "unreadable"
	}
	return v
}

// paramUncertainty is one range as /v1/meta publishes it.
type paramUncertainty struct {
	ID                  string `json:"id"`
	Kind                string `json:"kind"` // silent_change | check_skipped
	FromHeight          int64  `json:"from_height"`
	ToHeight            int64  `json:"to_height"`
	EffectiveFromHeight int64  `json:"effective_from_height,omitempty"`
	IntervalStartKnown  bool   `json:"interval_start_known"`
	Direction           string `json:"direction,omitempty"`
	WindowBeforeS       int64  `json:"window_before_s,omitempty"`
	WindowAfterS        int64  `json:"window_after_s,omitempty"`
	// Holds says whether this range is still withholding verdicts. A
	// check_skipped range never does: the observer could not look, which is
	// not evidence that anything changed.
	Holds                bool   `json:"holds"`
	PublicationsAffected int64  `json:"publications_affected"`
	AffectedIsFloor      bool   `json:"publications_affected_is_floor"`
	DetectedAt           string `json:"detected_at"`
	ToTime               string `json:"to_time,omitempty"`
	Resolution           string `json:"resolution,omitempty"` // verified | unresolvable
	ResolvedAt           string `json:"resolved_at,omitempty"`
	HeightsRead          int64  `json:"heights_read,omitempty"`
	ResolveError         string `json:"resolve_error,omitempty"`
	LastError            string `json:"last_error,omitempty"`
}

// retentionUncertainty is the one-line summary beside every rate that is
// missing rows because of a range. Omitted entirely when nothing is held,
// so a healthy deployment publishes nothing extra.
type retentionUncertainty struct {
	OpenRanges       int64  `json:"open_ranges"`
	PublicationsHeld int64  `json:"publications_held"`
	ProbesHeld       int64  `json:"probes_held"`
	Note             string `json:"note"`
}

const retentionUncertaintyNote = "x/fibre params changed without an event in one or more ranges of heights, and this observer has not read the params at every height in them. " +
	"must_serve_until is computed from those params, so for the publications whose upload falls inside a range it cannot say when the obligation ended, and publishes no serve verdict for them — neither the fault nor the credit. " +
	"The ranges are in /v1/meta.param_uncertainty. A range closes when the params at every height in it have been read; the verdicts then return as corrections, and a corrected deadline can only be earlier than the recorded one, never later."

func paramUncertaintyOf(u scan.ParamUncertainty) paramUncertainty {
	out := paramUncertainty{
		ID: u.ID, Kind: u.Kind, FromHeight: u.FromHeight, ToHeight: u.ToHeight,
		EffectiveFromHeight: u.EffectiveFromHeight, IntervalStartKnown: u.IntervalStartKnown,
		Direction: u.Direction, WindowBeforeS: u.WindowBeforeS, WindowAfterS: u.WindowAfterS,
		Holds: u.Holds(), PublicationsAffected: u.PublicationsAffected, AffectedIsFloor: u.IsFloor,
		DetectedAt: u.DetectedAt.UTC().Format(store.TimeLayout), Resolution: u.Resolution,
		HeightsRead: u.HeightsRead, ResolveError: u.ResolveError, LastError: u.LastError,
	}
	if u.ToTime != nil {
		out.ToTime = u.ToTime.UTC().Format(store.TimeLayout)
	}
	if u.ResolvedAt != nil {
		out.ResolvedAt = u.ResolvedAt.UTC().Format(store.TimeLayout)
	}
	return out
}

// retentionUncertaintyNow summarises what is currently withheld, or nil
// when nothing is.
func (s *Server) retentionUncertaintyNow(ctx context.Context) *retentionUncertainty {
	pubs, probes, ranges, err := s.st.HeldCounts(ctx)
	if err != nil || ranges == 0 {
		return nil
	}
	return &retentionUncertainty{OpenRanges: ranges, PublicationsHeld: pubs, ProbesHeld: probes, Note: retentionUncertaintyNote}
}
