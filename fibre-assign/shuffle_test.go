package assign

import "testing"

// The assignment's determinism rests on math/rand/v2 producing a fixed ChaCha8
// stream per seed and a fixed Fisher-Yates order — both are documented
// guarantees that hold across Go versions. These vectors pin that: if a future
// Go toolchain ever changed either, this fails here (in the zero-dependency
// suite) instead of silently diverging from a celestia-app built with that
// toolchain.
//
// Values captured under Go 1.23 and reconfirmed under Go 1.26 via ./reftest.
func TestChaCha8ShufflePinned(t *testing.T) {
	if got := shuffledIndices([32]byte{}, 16); !intsEqual(got, []int{8, 13, 12, 0, 15, 14, 3, 10, 7, 2, 4, 11, 5, 1, 6, 9}) {
		t.Fatalf("zero-seed / n=16 permutation drifted: %v", got)
	}
	if got := shuffledIndices(testCommitment, 32); !intsEqual(got, []int{
		17, 19, 28, 24, 1, 23, 13, 25, 18, 0, 15, 29, 3, 30, 20, 4,
		10, 8, 9, 22, 26, 16, 2, 7, 11, 27, 6, 5, 31, 14, 12, 21,
	}) {
		t.Fatalf("testCommitment / n=32 permutation drifted: %v", got)
	}
}

func intsEqual(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
