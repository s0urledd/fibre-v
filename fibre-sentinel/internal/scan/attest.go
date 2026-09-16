package scan

import (
	"crypto/ed25519"
	"encoding/hex"
	"strings"
)

// Attestation records which validators the settled promise proves stored the
// blob's shards, established by verifying the message's signatures rather than
// by trusting them.
//
// Why the observer verifies rather than counts: the chain's own check of
// MsgPayForFibre's validator signatures runs in the ante handler and is
// skipped in ExecModeFinalize and on a node-local cache hit
// (celestia-app x/fibre/ante/ante.go), and the message server never repeats
// it. A settled transaction therefore carries no state-machine guarantee that
// its signature entries are valid, so an observer that counted them would be
// trusting the publisher.
//
// Why it matters: the signature is a receipt. A Fibre server writes the shard
// to its store BEFORE it signs (celestia-app fibre/server_upload.go), so a
// verified signature is proof that this validator held the shard. The absence
// of one is NOT proof of the opposite: the reference client snapshots the
// signature list the moment the safety threshold is reached and keeps
// delivering to the remaining validators in the background
// (fibre/client_upload.go), so a validator can hold a shard its signature
// never reached the chain for. Absence means "unproven", never "absent".
type Attestation struct {
	// Attested is the set of consensus addresses (20-byte, lowercase hex)
	// whose signature over this promise verified.
	Attested map[string]bool
	// Entries is how many non-empty signature entries the message carried.
	Entries int
	// Verified is how many of those entries verified against a validator in
	// the set at the promise height.
	Verified int
	// Unmatched is how many non-empty entries verified against no validator.
	// The chain stops verifying once the threshold is met, so trailing entries
	// can be arbitrary bytes; a non-zero count here is informational, not a
	// fault of any validator.
	Unmatched int
	// OutOfPosition counts entries that verified against a validator other
	// than the one at their index. The list is documented as positional over
	// the validator set, so a non-zero count means the observer's view of that
	// ordering differs from the publisher's and the positional hint is not
	// load-bearing (the verification is what decides).
	OutOfPosition int
	// AttestedPower and TotalPower are voting power, for reporting how much of
	// the set the proof covers.
	AttestedPower int64
	TotalPower    int64
}

// Has reports whether this validator's storage of the shard is proven.
func (a Attestation) Has(consAddrHex string) bool {
	if a.Attested == nil {
		return false
	}
	return a.Attested[strings.ToLower(consAddrHex)]
}

// verifyAttestations checks every signature entry against the validator set at
// the promise height and returns who is proven to have stored the shard.
//
// The entry at index i is tried against the validator at position i first: the
// reference implementation builds the list positionally over the validator set
// (celestia-app fibre/validator/signature_set.go, "Signatures returns collected
// signatures ordered by validator set position ... Validators that did not sign
// have nil entries"). That is only a hint. When it fails the entry is tried
// against every remaining validator, so a mismatch between the observer's
// ordering and the publisher's costs time, never correctness.
func verifyAttestations(signBytes []byte, sigs [][]byte, members []ValSetMember) Attestation {
	a := Attestation{Attested: map[string]bool{}}
	for _, m := range members {
		a.TotalPower += m.VotingPower
	}
	if len(signBytes) == 0 || len(members) == 0 {
		return a
	}

	keyOf := func(m ValSetMember) string { return strings.ToLower(hex.EncodeToString(m.Address)) }
	verifies := func(m ValSetMember, sig []byte) bool {
		if len(m.PubKey) != ed25519.PublicKeySize {
			return false
		}
		return ed25519.Verify(ed25519.PublicKey(m.PubKey), signBytes, sig)
	}

	for i, sig := range sigs {
		if len(sig) != ed25519.SignatureSize {
			continue // empty placeholder, or malformed: the chain accepts both
		}
		a.Entries++

		if i < len(members) && verifies(members[i], sig) {
			addr := keyOf(members[i])
			if !a.Attested[addr] {
				a.Attested[addr] = true
				a.AttestedPower += members[i].VotingPower
			}
			a.Verified++
			continue
		}

		matched := false
		for j, m := range members {
			if j == i {
				continue // already tried
			}
			if !verifies(m, sig) {
				continue
			}
			addr := keyOf(m)
			if !a.Attested[addr] {
				a.Attested[addr] = true
				a.AttestedPower += m.VotingPower
			}
			a.Verified++
			a.OutOfPosition++
			matched = true
			break
		}
		if !matched {
			a.Unmatched++
		}
	}
	return a
}
