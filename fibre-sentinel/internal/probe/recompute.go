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
	var out Recomputed
	at := m.StartedAt
	if m.Outcome == OutcomeMissed {
		at = m.ScheduledAt
	}
	out.Phase = PhaseAtWindow(at, m.MustServeUntil, pruneTolerance)
	if m.Outcome == OutcomeNotFound {
		if p, regraded := notFoundPhase(m.FinishedAt, out.Phase, m.MustServeUntil, pruneTolerance); regraded {
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
		PinStale:           m.Observer != nil && m.Observer.PinStale,
	})
	return out
}
