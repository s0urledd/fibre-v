package assign

import (
	"math"
	"math/bits"
	"math/rand/v2"
)

// ShardMap maps each validator's consensus address to the row indices it is
// assigned, in assignment order — the same order the reference produces them:
// consecutive positions of the shuffled index permutation starting at that
// validator's running offset, wrapping mod TotalRows.
//
// When the assigned counts sum to more than TotalRows (many validators pinned
// at the MinRowsPerValidator floor), later validators' windows wrap and overlap
// earlier ones, so a row index can appear in more than one validator's slice.
// A single validator never gets a duplicate (its count is capped at
// OriginalRows < TotalRows).
type ShardMap map[Address][]int

// Rows returns the row indices assigned to addr.
func (m ShardMap) Rows(addr Address) ([]int, bool) {
	r, ok := m[addr]
	return r, ok
}

// Assign computes the shard assignment for commitment over the validator set
// vals, using params. vals may be in any order.
//
// It reproduces celestia-app fibre/validator.Set.Assign:
//
//  1. sort vals into canonical order (voting power desc, address asc);
//  2. row count per validator = clamp(ceil(OriginalRows * power * ltDen /
//     (totalPower * ltNum)), MinRowsPerValidator, OriginalRows) — int64 math;
//  3. seed a ChaCha8 RNG with the 32-byte commitment, Fisher-Yates shuffle
//     [0, TotalRows) via the same math/rand/v2 primitives the reference uses;
//  4. walk validators in canonical order with a running offset, giving each a
//     contiguous window of the shuffled permutation of its row-count length,
//     indices taken mod TotalRows.
//
// Degenerate inputs (empty vals, TotalRows == 0, MinRowsPerValidator == 0)
// return an empty ShardMap and nil error, matching the reference guard.
// Otherwise params.Validate() must pass. A duplicate address is a
// *DuplicateAddressError; row-count int64 overflow is an *OverflowError.
func Assign(commitment [32]byte, vals []Validator, params ProtocolParams) (ShardMap, error) {
	if len(vals) == 0 || params.TotalRows == 0 || params.MinRowsPerValidator == 0 {
		return ShardMap{}, nil
	}
	if err := params.Validate(); err != nil {
		return nil, err
	}

	sorted, dup := canonicalOrder(vals)
	if dup != nil {
		return nil, &DuplicateAddressError{Address: *dup}
	}

	total, err := sumVotingPower(sorted)
	if err != nil {
		return nil, err
	}

	counts := make([]int, len(sorted))
	for i, v := range sorted {
		c, err := AssignedRows(v.VotingPower, total, params)
		if err != nil {
			return nil, err
		}
		counts[i] = c
	}

	perm := shuffledIndices(commitment, params.TotalRows)

	m := make(ShardMap, len(sorted))
	offset := 0
	for i, v := range sorted {
		rows := make([]int, counts[i])
		for j := range rows {
			rows[j] = perm[(offset+j)%params.TotalRows]
		}
		m[v.Address] = rows
		offset += counts[i]
	}
	return m, nil
}

// shuffledIndices returns [0, totalRows) Fisher-Yates-shuffled with a ChaCha8
// RNG seeded by commitment. This uses the exact math/rand/v2 primitives the
// reference calls, so the permutation is identical.
func shuffledIndices(commitment [32]byte, totalRows int) []int {
	var seed [32]byte
	copy(seed[:], commitment[:])
	rng := rand.New(rand.NewChaCha8(seed))

	idx := make([]int, totalRows)
	for i := range idx {
		idx[i] = i
	}
	rng.Shuffle(totalRows, func(i, j int) {
		idx[i], idx[j] = idx[j], idx[i]
	})
	return idx
}

// AssignedRows returns the row COUNT for a validator with votingPower in a set
// whose total voting power is totalVotingPower. Pure int64 arithmetic,
// reproducing fibre/validator.Set.AssignedRows:
//
//	num  = OriginalRows * votingPower * LivenessThreshold.Denominator
//	den  = totalVotingPower * LivenessThreshold.Numerator
//	rows = ceil(num / den)
//	return clamp(rows, MinRowsPerValidator, OriginalRows)
//
// Returns an *OverflowError if num would overflow int64 (the reference wraps
// silently). totalVotingPower must be positive.
func AssignedRows(votingPower, totalVotingPower int64, params ProtocolParams) (int, error) {
	if err := params.Validate(); err != nil {
		return 0, err
	}
	if totalVotingPower <= 0 {
		return 0, &ParamsError{"total voting power must be positive"}
	}

	// int64 conversions mirror fibre/validator.Set.AssignedRows exactly.
	num, ok := mul3(int64(params.OriginalRows), votingPower, int64(params.LivenessThreshold.Denominator))
	if !ok {
		return 0, &OverflowError{Where: "row_count", VotingPower: votingPower}
	}
	den, ok := mul2(totalVotingPower, int64(params.LivenessThreshold.Numerator))
	if !ok {
		return 0, &OverflowError{Where: "row_count", VotingPower: votingPower}
	}
	// ceil(num/den); num, den > 0 so num+den-1 must not overflow either.
	if num > math.MaxInt64-(den-1) {
		return 0, &OverflowError{Where: "row_count", VotingPower: votingPower}
	}
	rows := int((num + den - 1) / den)
	return clamp(rows, params.MinRowsPerValidator, params.OriginalRows), nil
}

func sumVotingPower(vals []Validator) (int64, error) {
	var total int64
	for _, v := range vals {
		if v.VotingPower <= 0 {
			return 0, &ParamsError{"every validator must have positive voting power"}
		}
		s, ok := add(total, v.VotingPower)
		// cometbft caps total voting power at MaxInt64/8; match that ceiling.
		if !ok || s > math.MaxInt64/8 {
			return 0, &OverflowError{Where: "total_voting_power", VotingPower: v.VotingPower}
		}
		total = s
	}
	return total, nil
}

func clamp(v, lo, hi int) int {
	if v < lo {
		v = lo
	}
	if v > hi {
		v = hi
	}
	return v
}

// checked int64 arithmetic (positive operands).

func add(a, b int64) (int64, bool) {
	s := a + b
	return s, (s >= a) && (s >= b)
}

func mul2(a, b int64) (int64, bool) {
	if a == 0 || b == 0 {
		return 0, true
	}
	hi, lo := bits.Mul64(uint64(a), uint64(b))
	if hi != 0 || lo > math.MaxInt64 {
		return 0, false
	}
	return int64(lo), true
}

func mul3(a, b, c int64) (int64, bool) {
	ab, ok := mul2(a, b)
	if !ok {
		return 0, false
	}
	return mul2(ab, c)
}
