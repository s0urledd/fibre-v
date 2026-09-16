package scan

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

// members builds n validators with deterministic keys; the consensus address
// is sha256(pubkey)[:20], as CometBFT derives it.
func testMembers(t *testing.T, n int) ([]ValSetMember, []ed25519.PrivateKey) {
	t.Helper()
	out := make([]ValSetMember, n)
	keys := make([]ed25519.PrivateKey, n)
	for i := 0; i < n; i++ {
		seed := make([]byte, ed25519.SeedSize)
		seed[0] = byte(i + 1)
		priv := ed25519.NewKeyFromSeed(seed)
		pub := priv.Public().(ed25519.PublicKey)
		sum := sha256.Sum256(pub)
		out[i] = ValSetMember{Address: sum[:20], PubKey: pub, VotingPower: int64(100 - i)}
		keys[i] = priv
	}
	return out, keys
}

func addrOf(m ValSetMember) string { return hex.EncodeToString(m.Address) }

// The common case: a positional list with gaps, exactly as the reference
// client builds it. Only the validators whose signature verifies are attested.
func TestVerifyAttestations_PositionalWithGaps(t *testing.T) {
	members, keys := testMembers(t, 5)
	signBytes := []byte("promise sign bytes")

	sigs := make([][]byte, 5)
	sigs[0] = ed25519.Sign(keys[0], signBytes)
	sigs[2] = ed25519.Sign(keys[2], signBytes)
	sigs[3] = nil // did not sign
	sigs[4] = ed25519.Sign(keys[4], signBytes)

	a := verifyAttestations(signBytes, sigs, members)
	if a.Entries != 3 || a.Verified != 3 || a.Unmatched != 0 || a.OutOfPosition != 0 {
		t.Fatalf("counts: %+v", a)
	}
	for _, i := range []int{0, 2, 4} {
		if !a.Has(addrOf(members[i])) {
			t.Errorf("validator %d should be attested", i)
		}
	}
	for _, i := range []int{1, 3} {
		if a.Has(addrOf(members[i])) {
			t.Errorf("validator %d must not be attested: it never signed", i)
		}
	}
	if a.AttestedPower != members[0].VotingPower+members[2].VotingPower+members[4].VotingPower {
		t.Errorf("attested power = %d", a.AttestedPower)
	}
	if a.TotalPower != 100+99+98+97+96 {
		t.Errorf("total power = %d", a.TotalPower)
	}
}

// If the observer's ordering of the set differs from the publisher's, the
// positional hint fails and the full scan must still find the signer. A wrong
// mapping would accuse the wrong validator, so this must never fall back to
// index-based attribution.
func TestVerifyAttestations_WrongOrderStillAttributesCorrectly(t *testing.T) {
	members, keys := testMembers(t, 4)
	signBytes := []byte("promise sign bytes")

	// every entry is signed by the validator at the MIRRORED position
	sigs := make([][]byte, 4)
	for i := range sigs {
		sigs[i] = ed25519.Sign(keys[len(keys)-1-i], signBytes)
	}

	a := verifyAttestations(signBytes, sigs, members)
	if a.Verified != 4 {
		t.Fatalf("verified = %d, want 4", a.Verified)
	}
	if a.OutOfPosition == 0 {
		t.Error("out-of-position entries were not reported")
	}
	for i := range members {
		if !a.Has(addrOf(members[i])) {
			t.Errorf("validator %d signed (at a mirrored index) and must be attested", i)
		}
	}
}

// The chain stops verifying once the threshold is met, so trailing entries can
// be anything. Garbage must be counted, never attributed.
func TestVerifyAttestations_GarbageEntriesAttributeToNobody(t *testing.T) {
	members, keys := testMembers(t, 3)
	signBytes := []byte("promise sign bytes")

	sigs := [][]byte{
		ed25519.Sign(keys[0], signBytes),
		make([]byte, ed25519.SignatureSize), // 64 zero bytes: well-formed, verifies under nothing
		ed25519.Sign(keys[1], []byte("a different promise")),
	}
	a := verifyAttestations(signBytes, sigs, members)
	if a.Entries != 3 || a.Verified != 1 || a.Unmatched != 2 {
		t.Fatalf("counts: %+v", a)
	}
	if !a.Has(addrOf(members[0])) {
		t.Error("the one good signature should attest its signer")
	}
	if a.Has(addrOf(members[1])) || a.Has(addrOf(members[2])) {
		t.Error("garbage must not attest anyone")
	}
}

// A signature over different bytes must not attest, which is what protects the
// observer from a publisher replaying signatures between promises.
func TestVerifyAttestations_RejectsSignatureOverOtherBytes(t *testing.T) {
	members, keys := testMembers(t, 2)
	a := verifyAttestations([]byte("promise A"), [][]byte{ed25519.Sign(keys[0], []byte("promise B"))}, members)
	if a.Verified != 0 || a.Unmatched != 1 || len(a.Attested) != 0 {
		t.Fatalf("a signature over other bytes attested something: %+v", a)
	}
}

func TestVerifyAttestations_Degenerate(t *testing.T) {
	members, keys := testMembers(t, 2)
	if a := verifyAttestations(nil, [][]byte{ed25519.Sign(keys[0], []byte("x"))}, members); len(a.Attested) != 0 {
		t.Error("no sign bytes must attest nobody")
	}
	if a := verifyAttestations([]byte("x"), nil, members); a.Entries != 0 || a.TotalPower == 0 {
		t.Errorf("no signatures: %+v", a)
	}
	if a := verifyAttestations([]byte("x"), [][]byte{{1, 2, 3}}, members); a.Entries != 0 {
		t.Error("a malformed-length entry is not an entry")
	}
	// more entries than validators: the chain rejects such a message, but the
	// observer must not index out of range if it ever sees one
	if a := verifyAttestations([]byte("x"), make([][]byte, 9), members); a.Entries != 0 {
		t.Error("empty over-long list should be inert")
	}
	if a := (Attestation{}); a.Has("aa") {
		t.Error("zero Attestation must attest nobody")
	}
}
