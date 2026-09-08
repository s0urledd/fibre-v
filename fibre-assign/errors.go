package assign

import "fmt"

// ParamsError means ProtocolParams failed validation before any computation.
type ParamsError struct{ msg string }

func (e *ParamsError) Error() string { return "fibre assign: invalid params: " + e.msg }

// DuplicateAddressError means two validators in the input set share a consensus
// address. The real validator set cannot contain this, so it is rejected rather
// than guessed at.
type DuplicateAddressError struct{ Address Address }

func (e *DuplicateAddressError) Error() string {
	return fmt.Sprintf("fibre assign: duplicate validator address %s in set", e.Address)
}

// OverflowError means the int64 row-count arithmetic would overflow for some
// validator (originalRows * votingPower * denominator, or the total voting
// power sum). The reference implementation wraps silently here; this package
// refuses, because a wrapped row count assigns the wrong rows and would blame
// the wrong validator. Not reachable with realistic staking-reduced voting
// power.
type OverflowError struct {
	Where       string // "row_count" or "total_voting_power"
	VotingPower int64
}

func (e *OverflowError) Error() string {
	return fmt.Sprintf("fibre assign: int64 overflow in %s (voting power %d)", e.Where, e.VotingPower)
}
