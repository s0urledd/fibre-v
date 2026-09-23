package probe

import "time"

// Recomputed is a row's phase and classification re-derived from its own
// fields, as sentinel-recompute does over the record.
type Recomputed struct {
	Phase          Phase
	Classification Classification
	Reason         string
}

// Recompute re-derives the phase and classification of a stored row from
// the fields the row carries and the prune tolerance the prober ran with
// (from the run's recorded configuration). The phase is taken at the
// row's start, or at its scheduled time for a slot that was never run
// (MISSED), and a NOT_FOUND started in the window is regraded forward
// from its finish plus NotFoundGuard exactly as Run does at the RPC's
// return; the finish is at most the classification's own microseconds
// after that moment, so a row can differ only when its RPC returned
// within that of a phase boundary.
func (m Measurement) Recompute(pruneTolerance time.Duration) Recomputed {
	return m.RecomputeWith(pruneTolerance, m.MustServeUntil)
}

// RecomputeWith is Recompute under an explicit deadline, as the correction
// pass uses it once a params range has been verified and the deadline the
// row was stamped with turns out to be later than the one the server used.
// Passing m.MustServeUntil gives exactly Recompute.
func (m Measurement) RecomputeWith(pruneTolerance time.Duration, mustServeUntil time.Time) Recomputed {
	var out Recomputed
	at := m.StartedAt
	if m.Outcome == OutcomeMissed {
		at = m.ScheduledAt
	}
	out.Phase = PhaseAtWindow(at, mustServeUntil, pruneTolerance)
	if m.Outcome == OutcomeNotFound {
		if p, regraded := notFoundPhase(m.FinishedAt, out.Phase, mustServeUntil, pruneTolerance, m.ClockOffsetMS); regraded {
			out.Phase = p
		}
	}
	out.Classification, out.Reason = Classify(Evidence{
		Assigned:           m.Assigned,
		Attested:           m.Attested,
		AttestationUnknown: m.AttestationUnknown || m.SchemaVersion < AttestationSchemaVersion,
		Phase:              out.Phase,
		Outcome:            m.Outcome,
		CommitmentVerified: m.Download.CommitmentVerified,
		Shadowed:           m.Download.ShadowedBy != "",
		ShadowUncertain:    m.Download.ShadowedBy == "" && m.Download.ShadowGap != "",
		IdentityStale:      m.Identity.Stale,
		// Run threads this into Classify and Recompute did not, so a row
		// that came back genuine-but-short re-derived as a plain
		// UNMATCHED_GENUINE with the wrong reason. Same source as Run's.
		RowsSubsetOfOwn: m.Download.RowsSubsetOfAssignment,
		PinStale:        m.Observer != nil && m.Observer.PinStale,
	})
	return out
}
