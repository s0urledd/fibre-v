// Package assign is a standalone, dependency-free reimplementation of Celestia
// Fibre's shard assignment: given a blob commitment, the consensus validator
// set at the blob's promise height, and the protocol parameters, it computes
// which erasure-coded row indices each validator is responsible for serving.
//
// It mirrors celestia-app's fibre/validator.Set.Assign. The reference is not
// importable from outside the celestia-app module, so the logic is reproduced
// here and pinned by a differential test (./reftest) that runs hundreds of
// random validator sets through both this package and the real Set.Assign and
// requires bit-identical ShardMaps.
//
// # What is and is not reproduced
//
// The ChaCha8 stream and the Fisher-Yates shuffle are NOT reimplemented — this
// package calls the same math/rand/v2 standard-library primitives the reference
// calls (rand.NewChaCha8 seeded with the 32-byte commitment, then rng.Shuffle),
// so that part is identical by construction. What is reproduced is the protocol
// logic around it: the integer row-count formula, the minRows floor and
// originalRows cap, the canonical validator ordering, and the offset walk with
// modulo wrap-around.
//
// # Version pinning — no silent default
//
// Two assignment inputs are neither on-chain nor observable at runtime:
// MinRowsPerValidator (148) and LivenessThreshold (1/3). They are constants
// compiled into a specific celestia-app build (`toml:"-"` — operators cannot
// change them either), and no node or Fibre-server RPC returns them. `fibre
// version` prints only a version string.
//
// Because there is no runtime signal, Assign takes a ProtocolParams argument
// that the caller MUST supply — there is no function that fills it in. This
// package offers exactly one pre-filled value, ParamsV10BlobV0, and passing it
// is an explicit assertion by the caller: "I have confirmed this network runs a
// celestia-app whose fibre/protocol_params.go and fibre/blob.go match
// PinnedCelestiaAppCommit." A version string alone cannot establish that — a
// v10 patch could change the constants — so ParamsV10BlobV0 carries the exact
// commit, and ProtocolParams.Fingerprint lets a caller cross-check a params
// value obtained from any second source before trusting it.
//
// # Determinism and overflow
//
// The assignment is a pure function of (commitment, validator set, params).
// Height matters only in that it selects the validator set. The row-count
// formula is int64 arithmetic; the reference overflows silently on adversarial
// voting powers, this package returns an *OverflowError instead (realistic
// staking-reduced powers are ~6 orders of magnitude from the limit).
package assign
