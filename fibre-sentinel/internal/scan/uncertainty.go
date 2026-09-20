package scan

import (
	"strconv"
	"time"
)

// ParamUncertaintySchemaVersion versions param_uncertainty.jsonl on its own.
// SchemaVersion versions Publication and is entangled with
// AttestationSchemaVersion; a record that shares neither should not be
// hostage to either.
const ParamUncertaintySchemaVersion = 1

// The kinds of range this observer can be blind over.
const (
	// UncertaintySilentChange: a periodic state read disagreed with the
	// event history, so an x/fibre params change landed with no event
	// somewhere in this range. Every publication whose upload could have
	// fallen in it carries a must_serve_until computed from params that may
	// not be the ones the server read. This kind HOLDS verdicts.
	UncertaintySilentChange = "silent_change"
	// UncertaintyCheckSkipped: the state read itself failed, so the range a
	// change could be hiding in widened. Recorded so a reader can see the
	// blind stretch even when the check that ends it finds nothing. This
	// kind holds nothing: it says the observer could not look, not that
	// anything changed.
	UncertaintyCheckSkipped = "check_skipped"
)

// The outcomes of trying to close a range. There is no partial credit: a
// read that skipped heights proves nothing about the heights it skipped,
// and the whole reason the two endpoints are not enough is that a third
// value could have been in force between them.
const (
	// ResolutionOpen is the zero value: not yet attempted, or attempted
	// and neither proven nor abandoned.
	ResolutionOpen = ""
	// ResolutionVerified: x/fibre params were read at every height in
	// [FromHeight-1, ToHeight] and the values are on the record, so the
	// deadlines the range covers can be corrected against the proven
	// timeline. It does NOT by itself end the withholding — see Holds.
	ResolutionVerified = "verified"
	// ResolutionUnresolvable: the node cannot answer for those heights.
	// Nothing can be corrected against it, so its hold is permanent unless
	// an archive node closes it later; the latch allows unresolvable to
	// become verified, only not the reverse.
	ResolutionUnresolvable = "unresolvable"
)

// ResolvedValue is one params value proven to have been in force from
// FromHeight until the next entry. It is the output of reading every height
// in a range and collapsing equal consecutive answers.
type ResolvedValue struct {
	FromHeight int64          `json:"from_height"`
	Params     ParamsSnapshot `json:"params"`
}

// ParamUncertainty is one range of heights over which this observer cannot
// say which x/fibre params were in force, together with what came of trying
// to close it. It is the machine-readable form of what reconcileParams used
// to emit only as a log line.
//
// The range is (last check that actually read state, h]. A failed read
// leaves the marker where it was, so an outage widens the range rather than
// hiding part of it, and two overlapping records after a crash are correct
// because the union of the ranges is the truth.
type ParamUncertainty struct {
	SchemaVersion int    `json:"schema_version"`
	ID            string `json:"id"` // the natural key: chain:kind:from-to
	ChainID       string `json:"chain_id"`
	Kind          string `json:"kind"`
	FromHeight    int64  `json:"from_height"` // inclusive
	ToHeight      int64  `json:"to_height"`   // inclusive
	// EffectiveFromHeight is where the scanner recorded the newly observed
	// value: h+1, the earliest point it can vouch for without a read at
	// every height. Zero for check_skipped.
	EffectiveFromHeight int64 `json:"effective_from_height,omitempty"`
	// IntervalStartKnown is false when no reconcile was ever on record, so
	// FromHeight is the scan's own start rather than a previous check.
	IntervalStartKnown bool `json:"interval_start_known"`

	Before        ParamsSnapshot `json:"before,omitempty"` // the history's value at ToHeight
	After         ParamsSnapshot `json:"after,omitempty"`  // what state answered at ToHeight
	Direction     string         `json:"direction,omitempty"`
	WindowBeforeS int64          `json:"window_before_s,omitempty"`
	WindowAfterS  int64          `json:"window_after_s,omitempty"`

	// PublicationsAffected counts the publications this process appended
	// with a settlement height in the range. IsFloor is true when the range
	// starts before this process's first append, in which case the number
	// is a floor and not a total.
	PublicationsAffected int64 `json:"publications_affected"`
	IsFloor              bool  `json:"publications_affected_is_floor"`

	DetectedAt time.Time  `json:"detected_at"`
	ToTime     *time.Time `json:"to_time,omitempty"`
	LastError  string     `json:"last_error,omitempty"`

	// Resolution and what follows it are written in the same line as the
	// range, because the scanner tries to close a range the moment it opens
	// one: sixty heights is about a second of reads against a node that
	// still has that state, which is nearly always the node it is already
	// following. A range that could not be closed is written open and can
	// be closed later by a second record with the same ID.
	Resolution         string          `json:"resolution,omitempty"`
	ResolvedAt         *time.Time      `json:"resolved_at,omitempty"`
	ResolveMethod      string          `json:"resolve_method,omitempty"`
	HeightsRead        int64           `json:"heights_read,omitempty"`
	Values             []ResolvedValue `json:"values,omitempty"`
	ResolveError       string          `json:"resolve_error,omitempty"`
	NodeEarliestHeight int64           `json:"node_earliest_height,omitempty"`
}

// Key is the natural key. A range is identified by what it covers, so a
// crash between the record and the cursor writes an overlapping record
// rather than a duplicate one, and a later attempt to close the same range
// carries the same ID.
func (u ParamUncertainty) Key(chainID string) string {
	return chainID + ":" + u.Kind + ":" + strconv.FormatInt(u.FromHeight, 10) + "-" + strconv.FormatInt(u.ToHeight, 10)
}

// Holds reports whether this kind of range withholds the verdicts it
// covers at all. A check that did not happen is not evidence that anything
// changed, so check_skipped never withholds; a silent change always does.
//
// Verifying the params is NOT what ends the withholding, and this is the
// distinction the first cut of this code got wrong. Reading every height
// tells the observer what the deadline should have been; it does not move
// the deadlines already stamped on the publications, or re-grade the rows
// drawn against them. Until those corrections have actually landed, the
// store still holds the wrong deadline and the rows still carry the
// verdicts drawn from it. So a range stops withholding only once its
// corrections are complete, which is the collector's state and not the
// record's: see store.MarkRangeCorrected and the range_corrected line in
// corrections.jsonl, which is what carries that fact into the record so a
// rebuild and sentinel-recompute reach the same holds.
func (u ParamUncertainty) Holds() bool {
	return u.Kind == UncertaintySilentChange
}

// Covers reports whether a publication's upload could have fallen inside
// this range. The upload happens somewhere between the block before the
// promise height — the chain accepts a promise one block ahead of the
// validating node's latest, so the server may have read the state before it
// — and the settlement tx. The two intervals overlap, or the range says
// nothing about this publication.
func (u ParamUncertainty) Covers(promiseHeight, settlementHeight int64) bool {
	return promiseHeight-1 <= u.ToHeight && settlementHeight >= u.FromHeight
}

// ResolvedHistory returns a copy of a param history with every value a
// verified range proved inserted at the first height it was seen at.
//
// Placement is (height, -1): in force from the first tx of that block. A
// state read answers for a whole block and cannot say which tx inside it
// the change landed after, and the earlier of the two placements is the
// safe one — adding an entry can only add candidates to the set
// MustServeUntilForPromise takes its earliest bound over, and a larger set
// has an earliest bound no later than a smaller one.
//
// That is a statement about the bound, not about every deadline: an entry
// placed before the promise height can replace the value that was in force
// there rather than joining it, and if the replacement is longer the
// deadline moves later. The correction pass clamps for that; see
// CorrectedDeadline.
func ResolvedHistory(base []ParamEntry, us []ParamUncertainty) *ParamHistory {
	h := LoadParamHistory(base)
	for _, u := range us {
		if u.Resolution != ResolutionVerified {
			continue
		}
		for _, v := range u.Values {
			h.add(v.FromHeight, -1, "verified", v.Params.toParams())
		}
	}
	return h
}

// CorrectedDeadline is the deadline a verified range supports for one
// publication, and whether it moved.
//
// It never moves a deadline later. Verifying the params tells the observer
// what was in force; it does not tell it when the upload landed, and a
// deadline that moves later can turn a validator that read clean into a
// FAULT on evidence the observer did not have when it published the clean
// reading. The direction this observer will move a published verdict in is
// the one that withdraws an accusation. A lengthened window that goes
// uncorrected costs a fault this observer would otherwise have found; that
// is stated in docs/verdicts.md rather than quietly taken.
func CorrectedDeadline(recorded, recomputed time.Time) (time.Time, bool) {
	if recomputed.Before(recorded) {
		return recomputed, true
	}
	return recorded, false
}
