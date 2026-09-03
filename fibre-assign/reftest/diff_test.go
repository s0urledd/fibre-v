// Package reftest runs the standalone fibre-assign implementation and
// celestia-app's real fibre/validator.Set.Assign side by side over many random
// validator sets and requires bit-identical ShardMaps.
package reftest

import (
	"fmt"
	"math/rand/v2"
	"sort"
	"testing"

	refval "github.com/celestiaorg/celestia-app/v10/fibre/validator"
	cmted25519 "github.com/cometbft/cometbft/crypto/ed25519"
	cmtmath "github.com/cometbft/cometbft/libs/math"
	core "github.com/cometbft/cometbft/types"
	fa "github.com/plsgiveup/fibre/fibre-assign"
)

var refLT = cmtmath.Fraction{Numerator: 1, Denominator: 3}

// toFA converts a cometbft consensus address ([]byte, 20 bytes) to fa.Address.
func toFA(a []byte) fa.Address {
	var out fa.Address
	copy(out[:], a)
	return out
}

// buildPair constructs the same validator set in both representations. Keys are
// deterministic (GenPrivKeyFromSecret) so a failing scenario is reproducible.
func buildPair(t *testing.T, scenario int, powers []int64) (refval.Set, []fa.Validator) {
	t.Helper()
	cvals := make([]*core.Validator, len(powers))
	favals := make([]fa.Validator, len(powers))
	for i, p := range powers {
		priv := cmted25519.GenPrivKeyFromSecret([]byte(fmt.Sprintf("reftest/%d/%d", scenario, i)))
		pub := priv.PubKey()
		cvals[i] = core.NewValidator(pub, p)

		addr, err := fa.AddressFromEd25519PubKey(pub.Bytes())
		if err != nil {
			t.Fatalf("AddressFromEd25519PubKey: %v", err)
		}
		if addr != toFA(cvals[i].Address) {
			t.Fatalf("address derivation mismatch: fa=%s core=%x", addr, cvals[i].Address)
		}
		favals[i] = fa.Validator{Address: addr, VotingPower: p}
	}
	return refval.Set{ValidatorSet: core.NewValidatorSet(cvals), Height: 1}, favals
}

// compare requires refMap and myMap to be identical: same validators, same row
// slices in the same order.
func compare(t *testing.T, tag string, refMap refval.ShardMap, myMap fa.ShardMap) {
	t.Helper()
	if len(refMap) != len(myMap) {
		t.Fatalf("%s: validator count ref=%d mine=%d", tag, len(refMap), len(myMap))
	}
	for v, refRows := range refMap {
		addr := toFA(v.Address)
		myRows, ok := myMap[addr]
		if !ok {
			t.Fatalf("%s: validator %x missing from mine", tag, v.Address)
		}
		if len(refRows) != len(myRows) {
			t.Fatalf("%s: validator %x row count ref=%d mine=%d", tag, v.Address, len(refRows), len(myRows))
		}
		for i := range refRows {
			if refRows[i] != myRows[i] {
				t.Fatalf("%s: validator %x row[%d] ref=%d mine=%d\nref=%v\nmine=%v",
					tag, v.Address, i, refRows[i], myRows[i], head(refRows), head(myRows))
			}
		}
	}
}

func head(s []int) []int {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}

func randCommitment(rng *rand.Rand) [32]byte {
	var c [32]byte
	for i := range c {
		c[i] = byte(rng.UintN(256))
	}
	return c
}

// powerDistributions returns a labelled generator per named distribution.
func powerDistributions(rng *rand.Rand, n int) (string, []int64) {
	powers := make([]int64, n)
	switch rng.IntN(6) {
	case 0: // all equal (ordering ambiguity stress)
		for i := range powers {
			powers[i] = 1 + int64(rng.IntN(3))
		}
		return "equal", powers
	case 1: // uniform random
		for i := range powers {
			powers[i] = 1 + int64(rng.IntN(1_000))
		}
		return "uniform", powers
	case 2: // one whale + dust (minRows floor binds for the dust)
		powers[0] = 100_000 + int64(rng.IntN(900_000))
		for i := 1; i < n; i++ {
			powers[i] = 1 + int64(rng.IntN(5))
		}
		return "whale+dust", powers
	case 3: // power-law-ish
		for i := range powers {
			powers[i] = 1 + int64(rng.IntN(1<<uint(1+rng.IntN(16))))
		}
		return "powerlaw", powers
	case 4: // all 1 (max wrap-around when n is large)
		for i := range powers {
			powers[i] = 1
		}
		return "all-ones", powers
	default: // realistic staking-reduced magnitudes
		for i := range powers {
			powers[i] = 1_000 + int64(rng.IntN(50_000_000))
		}
		return "realistic", powers
	}
}

// TestDifferential_V10BlobV0: random validator sets, blob-v0 parameters.
func TestDifferential_V10BlobV0(t *testing.T) {
	rng := rand.New(rand.NewChaCha8([32]byte{'d', 'i', 'f', 'f', 'v', '0'}))
	const scenarios = 400

	for s := 0; s < scenarios; s++ {
		n := 1 + rng.IntN(140)
		dist, powers := powerDistributions(rng, n)
		commitment := randCommitment(rng)

		refSet, favals := buildPair(t, s, powers)
		refMap := refSet.Assign(commitment, fa.ParamsV10BlobV0.TotalRows, fa.ParamsV10BlobV0.OriginalRows, fa.ParamsV10BlobV0.MinRowsPerValidator, refLT)

		myMap, err := fa.Assign(commitment, favals, fa.ParamsV10BlobV0)
		if err != nil {
			t.Fatalf("scenario %d (%s, n=%d): fa.Assign: %v", s, dist, n, err)
		}
		compare(t, fmt.Sprintf("s=%d %s n=%d", s, dist, n), refMap, myMap)
	}
}

// TestDifferential_RandomParams: also vary totalRows/originalRows/minRows/
// livenessThreshold, exercising the row-count arithmetic across shapes
// (mirrors set_test.go's small-parameter Assign calls).
func TestDifferential_RandomParams(t *testing.T) {
	rng := rand.New(rand.NewChaCha8([32]byte{'d', 'i', 'f', 'f', 'r', 'p'}))
	const scenarios = 300

	for s := 0; s < scenarios; s++ {
		n := 1 + rng.IntN(60)
		_, powers := powerDistributions(rng, n)
		commitment := randCommitment(rng)

		originalRows := 4 + rng.IntN(400)
		parity := rng.IntN(3 * originalRows)
		totalRows := originalRows + parity
		minRows := 1 + rng.IntN(originalRows)
		ltDen := 2 + rng.IntN(6)
		ltNum := 1 + rng.IntN(ltDen-1)
		refFrac := cmtmath.Fraction{Numerator: uint64(ltNum), Denominator: uint64(ltDen)}
		myParams := fa.ProtocolParams{
			OriginalRows:        originalRows,
			TotalRows:           totalRows,
			MinRowsPerValidator: minRows,
			LivenessThreshold:   fa.Fraction{Numerator: uint64(ltNum), Denominator: uint64(ltDen)},
		}

		refSet, favals := buildPair(t, 10_000+s, powers)
		refMap := refSet.Assign(commitment, totalRows, originalRows, minRows, refFrac)

		myMap, err := fa.Assign(commitment, favals, myParams)
		if err != nil {
			t.Fatalf("scenario %d (n=%d, K=%d N=%d min=%d lt=%d/%d): %v",
				s, n, originalRows, parity, minRows, ltNum, ltDen, err)
		}
		compare(t, fmt.Sprintf("rp s=%d n=%d K=%d total=%d min=%d lt=%d/%d",
			s, n, originalRows, totalRows, minRows, ltNum, ltDen), refMap, myMap)
	}
}

// TestDifferential_EdgeCases: the specific shapes the task calls out.
func TestDifferential_EdgeCases(t *testing.T) {
	rng := rand.New(rand.NewChaCha8([32]byte{'e', 'd', 'g', 'e'}))

	type tc struct {
		name   string
		powers []int64
	}
	cases := []tc{
		{"single", []int64{7}},
		{"single-whale", []int64{9_000_000}},
		{"two-equal", []int64{5, 5}},
		{"equal-20", rep(20, 5)},
		{"equal-200-wraps", rep(200, 1)}, // Σ counts >> totalRows
		{"whale+99dust", append([]int64{20_000_000}, rep(99, 1)...)},
		{"three-1-2-3", []int64{1, 2, 3}},
		{"liveness-ceil-4", rep(4, 1)},
		{"100-equal", rep(100, 1)},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			for round := 0; round < 8; round++ {
				commitment := randCommitment(rng)
				refSet, favals := buildPair(t, 20_000+round, c.powers)
				refMap := refSet.Assign(commitment, fa.ParamsV10BlobV0.TotalRows, fa.ParamsV10BlobV0.OriginalRows, fa.ParamsV10BlobV0.MinRowsPerValidator, refLT)
				myMap, err := fa.Assign(commitment, favals, fa.ParamsV10BlobV0)
				if err != nil {
					t.Fatalf("%s round %d: %v", c.name, round, err)
				}
				compare(t, fmt.Sprintf("%s round=%d", c.name, round), refMap, myMap)

				// Sanity: for the wrap case confirm a row is shared across validators.
				if c.name == "equal-200-wraps" {
					if !hasCrossValidatorDup(myMap) {
						t.Fatalf("%s: expected wrap-around overlap", c.name)
					}
				}
			}
		})
	}
}

// TestDifferential_ShuffledInputOrder: fa.Assign must canonicalise, so feeding
// the validators in a random order still matches the reference (which sorts via
// core.NewValidatorSet).
func TestDifferential_ShuffledInputOrder(t *testing.T) {
	rng := rand.New(rand.NewChaCha8([32]byte{'s', 'h', 'u', 'f'}))
	for s := 0; s < 120; s++ {
		n := 2 + rng.IntN(40)
		_, powers := powerDistributions(rng, n)
		commitment := randCommitment(rng)

		refSet, favals := buildPair(t, 30_000+s, powers)
		refMap := refSet.Assign(commitment, fa.ParamsV10BlobV0.TotalRows, fa.ParamsV10BlobV0.OriginalRows, fa.ParamsV10BlobV0.MinRowsPerValidator, refLT)

		shuffled := append([]fa.Validator(nil), favals...)
		rng.Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })

		myMap, err := fa.Assign(commitment, shuffled, fa.ParamsV10BlobV0)
		if err != nil {
			t.Fatal(err)
		}
		compare(t, fmt.Sprintf("shuf s=%d n=%d", s, n), refMap, myMap)
	}
}

func rep(n int, v int64) []int64 {
	s := make([]int64, n)
	for i := range s {
		s[i] = v
	}
	return s
}

func hasCrossValidatorDup(m fa.ShardMap) bool {
	seen := map[int]struct{}{}
	// deterministic iteration for a stable check
	addrs := make([]fa.Address, 0, len(m))
	for a := range m {
		addrs = append(addrs, a)
	}
	sort.Slice(addrs, func(i, j int) bool {
		for k := range addrs[i] {
			if addrs[i][k] != addrs[j][k] {
				return addrs[i][k] < addrs[j][k]
			}
		}
		return false
	})
	for _, a := range addrs {
		for _, r := range m[a] {
			if _, ok := seen[r]; ok {
				return true
			}
			seen[r] = struct{}{}
		}
	}
	return false
}
