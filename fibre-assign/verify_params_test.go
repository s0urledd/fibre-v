package assign

import (
	"testing"
)

// ---- ported from celestia-app fibre/validator/set_test.go TestShardMap_Verify ----

func TestShardMap_Verify(t *testing.T) {
	p := ProtocolParams{OriginalRows: 4, TotalRows: 12, MinRowsPerValidator: 1, LivenessThreshold: Fraction{1, 3}}
	vals := mkVals(1, 1, 1)
	m := mustAssign(t, testCommitment, vals, p)

	t.Run("valid", func(t *testing.T) {
		for a, rows := range m {
			idx := make([]uint32, len(rows))
			for i, r := range rows {
				idx[i] = uint32(r)
			}
			if err := m.Verify(a, idx); err != nil {
				t.Fatalf("%s: %v", a, err)
			}
		}
	})

	t.Run("too few rows", func(t *testing.T) {
		a := vals[0].Address
		if err := m.Verify(a, []uint32{uint32(m[a][0])}); err == nil || !contains(err.Error(), "expected") {
			t.Fatalf("want 'expected' error, got %v", err)
		}
	})

	t.Run("wrong row", func(t *testing.T) {
		a := vals[0].Address
		idx := make([]uint32, len(m[a]))
		for i, r := range m[a] {
			idx[i] = uint32(r)
		}
		idx[0] = 999
		if err := m.Verify(a, idx); err == nil || !contains(err.Error(), "not assigned") {
			t.Fatalf("want 'not assigned' error, got %v", err)
		}
	})

	t.Run("duplicate rows", func(t *testing.T) {
		a := vals[0].Address
		idx := make([]uint32, len(m[a]))
		for i := range idx {
			idx[i] = uint32(m[a][0])
		}
		if err := m.Verify(a, idx); err == nil || !contains(err.Error(), "duplicate row") {
			t.Fatalf("want 'duplicate row' error, got %v", err)
		}
	})

	t.Run("not in map", func(t *testing.T) {
		var missing Address
		missing[0] = 0xff
		if err := m.Verify(missing, nil); err == nil || !contains(err.Error(), "not in shard map") {
			t.Fatalf("want 'not in shard map', got %v", err)
		}
	})
}

func TestShardMap_IsAssigned(t *testing.T) {
	m := mustAssign(t, testCommitment, mkVals(1, 1, 1), ParamsV10BlobV0)
	for a := range m {
		if !m.IsAssigned(a) {
			t.Fatalf("%s should be assigned", a)
		}
	}
	var absent Address
	absent[0] = 0xaa
	if m.IsAssigned(absent) {
		t.Fatal("absent address reported assigned")
	}
}

// ---- params: no silent default, explicit pin, fingerprint cross-check ----

func TestParamsV10BlobV0_Fingerprint(t *testing.T) {
	// A byte-for-byte copy fingerprints the same; any single-field change does not.
	same := ProtocolParams{
		OriginalRows:        4096,
		TotalRows:           16384,
		MinRowsPerValidator: 148,
		LivenessThreshold:   Fraction{1, 3},
	}
	if ParamsV10BlobV0.Fingerprint() != same.Fingerprint() {
		t.Fatal("identical params fingerprint differently")
	}
	muts := []ProtocolParams{
		{OriginalRows: 4095, TotalRows: 16384, MinRowsPerValidator: 148, LivenessThreshold: Fraction{1, 3}},
		{OriginalRows: 4096, TotalRows: 16385, MinRowsPerValidator: 148, LivenessThreshold: Fraction{1, 3}},
		{OriginalRows: 4096, TotalRows: 16384, MinRowsPerValidator: 147, LivenessThreshold: Fraction{1, 3}},
		{OriginalRows: 4096, TotalRows: 16384, MinRowsPerValidator: 148, LivenessThreshold: Fraction{1, 4}},
		{OriginalRows: 4096, TotalRows: 16384, MinRowsPerValidator: 148, LivenessThreshold: Fraction{2, 3}},
	}
	for i, m := range muts {
		if m.Fingerprint() == ParamsV10BlobV0.Fingerprint() {
			t.Fatalf("mutation %d collides with the pin fingerprint", i)
		}
	}
}

func TestPinnedConstants(t *testing.T) {
	if PinnedCelestiaAppVersion != "v10" {
		t.Errorf("PinnedCelestiaAppVersion = %q", PinnedCelestiaAppVersion)
	}
	if len(PinnedCelestiaAppCommit) != 40 {
		t.Errorf("PinnedCelestiaAppCommit is not a full SHA: %q", PinnedCelestiaAppCommit)
	}
}

func TestAddressFromEd25519PubKey(t *testing.T) {
	if _, err := AddressFromEd25519PubKey(make([]byte, 31)); err == nil {
		t.Fatal("want error for 31-byte key")
	}
	a, err := AddressFromEd25519PubKey(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	// sha256(32 zero bytes) = 66687aadf862bd776c8fc18b8e9f8e2008971485...
	// first 20 bytes is the consensus address.
	if got := a.String(); got != "66687aadf862bd776c8fc18b8e9f8e2008971485" {
		t.Fatalf("address = %s, want 66687aadf862bd776c8fc18b8e9f8e2008971485", got)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
