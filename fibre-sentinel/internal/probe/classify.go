package probe

// Outcome is the raw, mechanism-level result of a single probe. It says what
// happened on the wire, not whether that is acceptable — that is Classification.
type Outcome string

const (
	OutcomeServedOK       Outcome = "SERVED_OK"       // shard returned, rows verify against commitment AND assignment
	OutcomeNotFound       Outcome = "NOT_FOUND"       // server answered "no such shard"
	OutcomeWrongRows      Outcome = "WRONG_ROWS"      // rows returned but not the assigned set (ShardMap.Verify failed)
	OutcomeInvalidRows    Outcome = "INVALID_ROWS"    // rows returned but fail commitment verification
	OutcomePartial        Outcome = "PARTIAL"         // fewer rows than assigned, otherwise valid
	OutcomeDNSFail        Outcome = "DNS_FAIL"        // host name did not resolve
	OutcomeTCPRefused     Outcome = "TCP_REFUSED"     // connection actively refused
	OutcomeTCPTimeout     Outcome = "TCP_TIMEOUT"     // TCP connect timed out
	OutcomeTCPUnreachable Outcome = "TCP_UNREACHABLE" // network/host unreachable, DNS ok
	OutcomeTLSFail        Outcome = "TLS_HANDSHAKE_FAIL"
	OutcomeIdentityFail   Outcome = "IDENTITY_FAIL"   // handshake ok, consensus-key binding rejected
	OutcomeRPCUnavailable Outcome = "RPC_UNAVAILABLE" // gRPC Unavailable after a good TLS handshake
	OutcomeRPCError       Outcome = "RPC_ERROR"       // some other gRPC error
	OutcomeProbeError     Outcome = "PROBE_ERROR"     // the probe itself failed (bug / config), not the target
	OutcomeMissed         Outcome = "MISSED"          // scheduled point elapsed before the prober could run it
	OutcomeReachable      Outcome = "REACHABLE"       // DNS/TCP/TLS/identity all fine; download deliberately skipped (heartbeat or policy backoff)
)

// Classification is the Sentinel's verdict on one measurement, given the probe
// phase and whether the validator was assigned this shard. It is a fact about
// this one probe, not a score — aggregation is a separate, later view.
type Classification string

const (
	// ClassHealthy: assigned validator served its exact rows while under
	// obligation (or still serving in grace/post — over-serving is fine).
	ClassHealthy Classification = "HEALTHY"
	// ClassFault: assigned validator failed its retention obligation — not
	// found, unreachable, wrong identity, or corrupt/wrong data, BEFORE
	// must_serve_until.
	ClassFault Classification = "FAULT"
	// ClassTolerated: NOT_FOUND / unreachable in the grace window right after
	// must_serve_until. Within measured prune lag; not held against the
	// validator.
	ClassTolerated Classification = "TOLERATED"
	// ClassExpectedGone: NOT_FOUND in the post window — the blob is supposed to
	// be pruned by now.
	ClassExpectedGone Classification = "EXPECTED_GONE"
	// ClassExpectedUnassigned: NOT_FOUND from a validator that was never
	// assigned this shard. Normal.
	ClassExpectedUnassigned Classification = "EXPECTED_UNASSIGNED"
	// ClassServedPastWindow: assigned validator still serving well after the
	// obligation ended. Not a fault; noted because it affects disk accounting.
	ClassServedPastWindow Classification = "SERVED_PAST_WINDOW"
	// ClassServingUnassigned: a NOT-assigned validator returned a shard.
	// Unexpected; flagged for review (misassignment or a validator over-serving).
	ClassServingUnassigned Classification = "SERVING_UNASSIGNED"
	// ClassUnreachablePostWindow: assigned validator unreachable after its
	// obligation ended. Recorded, not a retention fault.
	ClassUnreachablePostWindow Classification = "UNREACHABLE_POST_WINDOW"
	// ClassProbeError: the probe could not be carried out (our side).
	ClassProbeError Classification = "PROBE_ERROR"
	// ClassNotProbed: the scheduled point elapsed before it could be probed.
	ClassNotProbed Classification = "NOT_PROBED"
)

// served reports whether an outcome means "the shard came back".
func (o Outcome) served() bool {
	return o == OutcomeServedOK || o == OutcomePartial
}

// reachFailure reports whether an outcome is a reachability/transport failure
// (as opposed to a clean protocol answer like NOT_FOUND).
func (o Outcome) reachFailure() bool {
	switch o {
	case OutcomeDNSFail, OutcomeTCPRefused, OutcomeTCPTimeout, OutcomeTCPUnreachable,
		OutcomeTLSFail, OutcomeRPCUnavailable, OutcomeRPCError:
		return true
	}
	return false
}

// Classify applies the taxonomy. assigned is whether this validator holds rows
// for this commitment; phase is derived from the ACTUAL probe start time.
func Classify(assigned bool, phase Phase, o Outcome) (Classification, string) {
	if o == OutcomeProbeError {
		return ClassProbeError, "probe could not be carried out"
	}
	if o == OutcomeMissed {
		return ClassNotProbed, "scheduled point elapsed before the prober ran it"
	}
	if o == OutcomeReachable {
		// The endpoint answered and proved its identity; no retention verdict
		// was attempted. Recorded as an observer-side gap for the shard.
		return ClassNotProbed, "reachable; download skipped by policy"
	}

	if !assigned {
		switch {
		case o == OutcomeNotFound:
			return ClassExpectedUnassigned, "validator not assigned this shard; NOT_FOUND expected"
		case o.served():
			return ClassServingUnassigned, "validator returned a shard it was not assigned — misassignment or over-serving"
		case o.reachFailure():
			return ClassExpectedUnassigned, "validator not assigned this shard; reachability not required"
		default:
			return ClassExpectedUnassigned, "validator not assigned this shard"
		}
	}

	// assigned validator.
	switch phase {
	case PhaseInWindow:
		switch {
		case o == OutcomeServedOK:
			return ClassHealthy, "served assigned rows, verified against commitment and assignment"
		case o == OutcomeNotFound:
			return ClassFault, "assigned shard not found while under retention obligation"
		case o == OutcomeWrongRows:
			return ClassFault, "served rows outside its assignment"
		case o == OutcomeInvalidRows || o == OutcomePartial:
			return ClassFault, "served rows that do not verify against the commitment"
		case o == OutcomeIdentityFail:
			return ClassFault, "TLS identity is not the endorsed consensus key"
		case o.reachFailure():
			return ClassFault, "assigned validator unreachable while under retention obligation"
		default:
			return ClassFault, "unexpected outcome under retention obligation"
		}

	case PhaseGrace:
		switch {
		case o == OutcomeServedOK:
			return ClassHealthy, "still serving just past must_serve_until"
		case o == OutcomeNotFound:
			return ClassTolerated, "NOT_FOUND within prune-lag tolerance after must_serve_until"
		case o == OutcomeIdentityFail:
			return ClassFault, "TLS identity is not the endorsed consensus key"
		case o == OutcomeWrongRows || o == OutcomeInvalidRows:
			return ClassFault, "served bad data"
		case o.reachFailure():
			return ClassTolerated, "unreachable within prune-lag tolerance"
		default:
			return ClassTolerated, "post-deadline, within tolerance"
		}

	default: // PhasePost
		switch {
		case o == OutcomeNotFound:
			return ClassExpectedGone, "blob pruned as expected after must_serve_until + tolerance"
		case o == OutcomeServedOK:
			return ClassServedPastWindow, "still serving well after the obligation ended"
		case o == OutcomeIdentityFail:
			return ClassFault, "TLS identity is not the endorsed consensus key"
		case o == OutcomeWrongRows || o == OutcomeInvalidRows:
			return ClassServedPastWindow, "serving data past the window, and it does not verify"
		case o.reachFailure():
			return ClassUnreachablePostWindow, "unreachable after the obligation ended"
		default:
			return ClassExpectedGone, "post-window"
		}
	}
}
