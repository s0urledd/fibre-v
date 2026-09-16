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
	// OutcomeServerError: the validator was reached, completed TLS, proved its
	// identity and answered the RPC with an application error (gRPC Internal
	// or Unknown). It is NOT a reachability failure: calling it "unreachable"
	// would be factually wrong about a server the observer just talked to.
	OutcomeServerError Outcome = "SERVER_ERROR"
	// OutcomeRPCDeadline: the download did not finish within the observer's
	// own deadline (base + size-scaled). Never a verdict about the validator.
	OutcomeRPCDeadline Outcome = "RPC_DEADLINE"
	// OutcomeNoHost: the validator has no fibre host registered in x/valaddr,
	// so nobody can fetch its rows.
	OutcomeNoHost     Outcome = "NO_REGISTERED_HOST"
	OutcomeRPCError   Outcome = "RPC_ERROR"   // some other gRPC error
	OutcomeProbeError Outcome = "PROBE_ERROR" // the probe itself failed (bug / config), not the target
	OutcomeMissed     Outcome = "MISSED"      // scheduled point elapsed before the prober could run it
	OutcomeReachable  Outcome = "REACHABLE"   // DNS/TCP/TLS/identity all fine; download deliberately skipped (heartbeat or policy backoff)
)

// AllOutcomes is every outcome the prober can record. The taxonomy test walks
// it so a new outcome cannot reach a default arm unnoticed.
var AllOutcomes = []Outcome{
	OutcomeServedOK, OutcomeNotFound, OutcomeWrongRows, OutcomeInvalidRows, OutcomePartial,
	OutcomeDNSFail, OutcomeTCPRefused, OutcomeTCPTimeout, OutcomeTCPUnreachable, OutcomeTLSFail,
	OutcomeIdentityFail, OutcomeRPCUnavailable, OutcomeServerError, OutcomeRPCDeadline,
	OutcomeNoHost, OutcomeRPCError, OutcomeProbeError, OutcomeMissed, OutcomeReachable,
}

// Classification is the Sentinel's verdict on one measurement, given the probe
// phase and whether the validator was assigned this shard. It is a fact about
// this one probe, not a score — aggregation is a separate, later view.
type Classification string

const (
	// ClassHealthy: assigned validator served its exact rows while under
	// obligation (or still serving in grace/post — over-serving is fine).
	ClassHealthy Classification = "HEALTHY"
	// ClassFault: the observer reached this validator and it failed to hand
	// over a shard it was proven to hold, or it answered as someone else.
	// This is the only class that counts against a validator, and it requires
	// an answer: a validator the observer could not reach is not in here,
	// because "we could not get to it" and "it did not serve" are different
	// statements and only the second is about the validator.
	ClassFault Classification = "FAULT"
	// ClassUnreachable: assigned and attested, under obligation, and the
	// observer could not complete a conversation with the endpoint at all.
	// Recorded in full and shown next to the serve rate, but kept out of it:
	// from one vantage this is indistinguishable from a route, firewall or
	// peering problem on the observer's own side, and publishing it as a
	// retention failure would be an accusation the evidence does not support.
	ClassUnreachable Classification = "UNREACHABLE"
	// ClassNotRegistered: the validator has no Fibre host in x/valaddr at the
	// moment of the probe. Nobody can fetch its rows, but this is a registry
	// state (jailing and unbonding remove a provider from the bonded list
	// while the chain keeps the entry), not a refusal to serve.
	ClassNotRegistered Classification = "NOT_REGISTERED"
	// ClassShadowedShard: the rows that came back are genuine rows of this
	// blob — they verify against the commitment — but their indices are not
	// the ones this promise assigns. DownloadShard is addressed by commitment
	// alone and a validator's store keeps one shard per commitment, so a
	// second promise over the same blob makes an honest validator answer with
	// the other promise's rows. Never a fault: the validator cannot tell the
	// two promises apart, because the protocol gives it no way to.
	ClassShadowedShard Classification = "SHADOWED_SHARD"
	// ClassIdentityExpired: the TLS certificate is endorsed by the right
	// consensus key, but the signed validity window has lapsed or has not
	// started. That is endpoint hygiene, not impersonation and not a
	// retention failure, so it is reported on its own rather than folded into
	// the serve rate.
	ClassIdentityExpired Classification = "IDENTITY_EXPIRED"
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

	// ClassUnattested: the validator is assigned rows for this blob, but the
	// settled promise carries no verified signature from it, so there is no
	// proof it ever received the shard. Absence of a signature is not proof
	// that it did not: the publisher stops collecting at the safety threshold
	// and keeps delivering in the background. These probes are reported
	// separately and are counted neither for nor against the validator.
	ClassUnattested Classification = "UNATTESTED"
)

// CountsAgainst reports whether a class is held against the validator. Exactly
// one class is, and every rate in the API is built from this predicate rather
// than from a list repeated at each call site.
func (c Classification) CountsAgainst() bool { return c == ClassFault }

// CountsFor reports whether a class is evidence the validator kept its promise.
func (c Classification) CountsFor() bool { return c == ClassHealthy }

// Rated reports whether a class belongs in the serve rate at all. Everything
// else is published beside the rate with its own name, never folded in.
func (c Classification) Rated() bool { return c.CountsFor() || c.CountsAgainst() }

// served reports whether an outcome means "the shard came back".
func (o Outcome) served() bool {
	return o == OutcomeServedOK || o == OutcomePartial
}

// reachFailure reports whether an outcome means the observer never got a
// usable answer out of the endpoint. SERVER_ERROR is deliberately absent: the
// validator was reached and answered.
func (o Outcome) reachFailure() bool {
	switch o {
	case OutcomeDNSFail, OutcomeTCPRefused, OutcomeTCPTimeout, OutcomeTCPUnreachable,
		OutcomeTLSFail, OutcomeRPCUnavailable, OutcomeRPCError:
		return true
	}
	return false
}

// Evidence is everything the taxonomy needs about one probe. It is a struct
// rather than a list of bare booleans so that adding a field cannot silently
// reorder an existing call.
type Evidence struct {
	// Assigned: fibre-assign gives this validator rows for this commitment.
	Assigned bool
	// Attested: the settled promise carries a signature from this validator
	// that the observer verified against its consensus key, which is the only
	// on-chain proof that it ever stored the shard.
	Attested bool
	// Phase is derived from the ACTUAL probe start time.
	Phase Phase
	// Outcome is what happened on the wire.
	Outcome Outcome
	// CommitmentVerified: the rows that came back are genuine rows of this
	// blob. With WRONG_ROWS or PARTIAL this is the whole question: rows that
	// verify against the commitment but carry the wrong indices mean the
	// validator answered from a different promise over the same blob, which
	// it has no way to avoid. Rows that do not verify mean corrupt data.
	CommitmentVerified bool
	// IdentityStale: the certificate is endorsed by the right consensus key
	// but its signed validity window has lapsed or not yet started. Only
	// meaningful when Outcome is IDENTITY_FAIL.
	IdentityStale bool
}

// Classify applies the taxonomy.
func Classify(in Evidence) (Classification, string) {
	o := in.Outcome

	// Observer-side first: none of these say anything about the validator.
	if o == OutcomeProbeError {
		return ClassProbeError, "probe could not be carried out"
	}
	if o == OutcomeMissed {
		return ClassNotProbed, "scheduled point elapsed before the prober ran it"
	}
	if o == OutcomeRPCDeadline {
		return ClassProbeError, "download did not finish within the observer's deadline; no retention verdict"
	}
	if o == OutcomeReachable {
		// The endpoint answered and proved its identity; no retention verdict
		// was attempted. Recorded as an observer-side gap for the shard.
		return ClassNotProbed, "reachable; download skipped by policy"
	}

	// Identity is a property of the endpoint, not of one shard, so it is
	// judged before anything that depends on holding this blob.
	if o == OutcomeIdentityFail {
		if in.IdentityStale {
			return ClassIdentityExpired, "certificate is endorsed by the right consensus key but its signed validity window has lapsed"
		}
		return ClassFault, "TLS identity is not the endorsed consensus key"
	}

	// No registered host is a registry state, not a refusal. Judged before
	// assignment because it is true of the endpoint either way.
	if o == OutcomeNoHost {
		if !in.Assigned {
			return ClassExpectedUnassigned, "validator not assigned this shard and has no registered Fibre host"
		}
		return ClassNotRegistered, "no Fibre host registered for this validator at the time of the probe, so nobody could fetch its rows"
	}

	if in.Assigned && !in.Attested {
		// No verified signature over this promise, so nothing proves this
		// validator was ever sent the shard. Serving it anyway proves it has
		// it; failing to serve it proves nothing at all. Counted neither for
		// nor against: excluding only the failures would inflate every rate.
		if o.served() {
			return ClassUnattested, "served the shard although the settled promise carries no verified signature from this validator"
		}
		return ClassUnattested, "assigned rows, but the settled promise carries no verified signature from this validator: nothing proves it ever received the shard"
	}

	if !in.Assigned {
		switch {
		case o == OutcomeNotFound:
			return ClassExpectedUnassigned, "validator not assigned this shard; NOT_FOUND expected"
		case o.served() || o == OutcomeWrongRows || o == OutcomeInvalidRows:
			return ClassServingUnassigned, "validator returned data for a shard it was not assigned — misassignment or over-serving"
		case o.reachFailure() || o == OutcomeServerError:
			return ClassExpectedUnassigned, "validator not assigned this shard; reachability not required"
		default:
			return ClassExpectedUnassigned, "validator not assigned this shard"
		}
	}

	// Rows came back and verify against the commitment, but their indices are
	// not this promise's assignment. The validator is answering from another
	// promise over the same blob and has no way to tell them apart, in any
	// phase. Never a fault.
	if (o == OutcomeWrongRows || o == OutcomePartial) && in.CommitmentVerified {
		return ClassShadowedShard, "returned rows of this blob that verify against the commitment but are not the indices this promise assigns; DownloadShard is addressed by commitment alone, so another promise over the same blob answers in its place"
	}

	// assigned and attested validator.
	switch in.Phase {
	case PhaseInWindow:
		switch {
		case o == OutcomeServedOK:
			return ClassHealthy, "served assigned rows, verified against commitment and assignment"
		case o == OutcomeNotFound:
			return ClassFault, "assigned shard not found while under retention obligation"
		case o == OutcomeInvalidRows:
			return ClassFault, "returned bytes that do not verify against the blob commitment"
		case o == OutcomeWrongRows || o == OutcomePartial:
			return ClassFault, "returned rows that verify against neither the commitment nor this promise's assignment"
		case o == OutcomeServerError:
			return ClassFault, "endpoint reached and identity verified; the server returned an internal error instead of the shard"
		case o.reachFailure():
			return ClassUnreachable, "could not complete a conversation with the endpoint while it was under obligation; from one vantage this is not distinguishable from a problem on the observer's own path"
		default:
			// An outcome the taxonomy does not know cannot be an accusation.
			return ClassProbeError, "unrecognised probe outcome; no retention verdict"
		}

	case PhaseGrace:
		switch {
		case o == OutcomeServedOK:
			return ClassHealthy, "still serving just past must_serve_until"
		case o == OutcomeNotFound:
			return ClassTolerated, "NOT_FOUND within prune-lag tolerance after must_serve_until"
		case o == OutcomeInvalidRows:
			return ClassFault, "returned bytes that do not verify against the blob commitment"
		case o == OutcomeWrongRows || o == OutcomePartial:
			return ClassFault, "returned rows that verify against neither the commitment nor this promise's assignment"
		case o == OutcomeServerError:
			return ClassTolerated, "server error within prune-lag tolerance after must_serve_until"
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
		case o == OutcomeInvalidRows:
			return ClassFault, "returned bytes that do not verify against the blob commitment, after the window"
		case o == OutcomeWrongRows:
			// DownloadShard performs no assignment check at all: assignment is
			// enforced only at upload. Accusing a validator of serving rows
			// outside an assignment the download path never enforces, after
			// its obligation has ended, is not a claim the protocol supports.
			return ClassServedPastWindow, "served rows of this blob outside this promise's assignment after the obligation ended; DownloadShard enforces no assignment, so this is not a rule the validator broke"
		case o == OutcomePartial:
			return ClassServedPastWindow, "still serving (part of the shard) after the obligation ended"
		case o == OutcomeServerError:
			return ClassUnreachablePostWindow, "server error after the obligation ended"
		case o.reachFailure():
			return ClassUnreachablePostWindow, "unreachable after the obligation ended"
		default:
			return ClassExpectedGone, "post-window"
		}
	}
}
