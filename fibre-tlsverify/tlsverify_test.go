package tlsverify

import (
	"crypto/ed25519"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"
	"time"
)

// ---- golden vector schema (subset of celestia-app's identity_vectors.json) ----

type goldenFile struct {
	Description string          `json:"description"`
	Constants   goldenConstants `json:"constants"`
	Cases       []goldenCase    `json:"cases"`
}

type goldenConstants struct {
	ExtensionOID             string `json:"extension_oid"`
	SignUniqueID             string `json:"sign_unique_id"`
	SignPrefix               string `json:"sign_prefix"`
	EnvelopePrefix           string `json:"envelope_prefix"`
	BindingVersion           int    `json:"binding_version"`
	MaxIdentityExtensionSize int    `json:"max_identity_extension_size"`
	MaxPayloadDERSize        int    `json:"max_payload_der_size"`
	MaxCertValiditySeconds   int64  `json:"max_cert_validity_seconds"`
	ClockSkewSeconds         int64  `json:"clock_skew_seconds"`
}

type goldenCase struct {
	Name string `json:"name"`

	// Producer-side fields — present unless the case corrupts/omits that layer.
	ChainID     string `json:"chain_id"`
	PayloadDER  string `json:"payload_der"`
	SignInput   string `json:"sign_input"`
	SignedBytes string `json:"signed_bytes"`

	// Verifier inputs + expected verdict.
	CertDER              string `json:"cert_der"`
	VerifierChainID      string `json:"verifier_chain_id"`
	VerifierConsensusPub string `json:"verifier_consensus_pub"`
	VerifyAt             int64  `json:"verify_at"`
	Expected             struct {
		Valid bool   `json:"valid"`
		Error string `json:"error"`
	} `json:"expected"`
}

const vectorsPath = "testdata/identity_vectors.json"

func loadGolden(t *testing.T) goldenFile {
	t.Helper()
	data, err := os.ReadFile(vectorsPath)
	if err != nil {
		t.Fatalf("read %s: %v", vectorsPath, err)
	}
	var f goldenFile
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatalf("parse %s: %v", vectorsPath, err)
	}
	if len(f.Cases) == 0 {
		t.Fatalf("no cases in %s", vectorsPath)
	}
	return f
}

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("bad hex %q: %v", s, err)
	}
	return b
}

// enumReasons maps the stable error enum published in the vectors to the set of
// this package's Reasons that satisfy it. Most are 1:1; the collapsed upstream
// enums map to a set.
var enumReasons = map[string]map[Reason]bool{
	"extension_missing":       {ReasonExtensionMissing: true},
	"extension_too_large":     {ReasonExtensionTooLarge: true},
	"extension_malformed":     {ReasonExtensionMalformed: true},
	"extension_trailing_data": {ReasonExtensionTrailingData: true},
	"payload_empty":           {ReasonPayloadEmpty: true},
	"payload_too_large":       {ReasonPayloadTooLarge: true},
	"signature_empty":         {ReasonSignatureEmpty: true},
	"payload_malformed":       {ReasonPayloadMalformed: true},
	"binding_trailing_data":   {ReasonBindingTrailingData: true},
	"unsupported_version":     {ReasonUnsupportedVersion: true},
	"signature_invalid":       {ReasonSignatureInvalid: true},
	"tls_key_mismatch":        {ReasonTLSKeyMismatch: true},
	"window_empty":            {ReasonWindowEmpty: true},
	"window_too_long":         {ReasonWindowTooLong: true},
	"outside_validity_window": {ReasonCertNotYetValid: true, ReasonCertExpired: true},
	"cert_window_mismatch":    {ReasonCertWindowMismatch: true},
	"eku_missing":             {ReasonEKUMissing: true},
}

// caseNameReason pins the two cases where this package refines an upstream
// enum: the split of "outside_validity_window" into not-yet-valid vs expired.
var caseNameReason = map[string]Reason{
	"expired":       ReasonCertExpired,
	"not_yet_valid": ReasonCertNotYetValid,
}

// wantValid / wantInvalid are the case counts in the committed
// identity_vectors.json copied from celestia-app main. If a future copy adds or
// removes cases, update these deliberately.
const (
	wantValid   = 1
	wantInvalid = 20
)

// TestGoldenVectors runs every committed case through VerifyCertificateAt:
// the one valid case must pass; each failure case must fail with a Reason that
// satisfies its published error enum.
func TestGoldenVectors(t *testing.T) {
	f := loadGolden(t)

	var valid, invalid int
	for _, c := range f.Cases {
		c := c
		t.Run(c.Name, func(t *testing.T) {
			cert, err := x509.ParseCertificate(mustHex(t, c.CertDER))
			if err != nil {
				t.Fatalf("parse cert_der: %v", err)
			}
			pub := ed25519.PublicKey(mustHex(t, c.VerifierConsensusPub))
			now := time.Unix(c.VerifyAt, 0)

			gotErr := VerifyCertificateAt(cert, pub, c.VerifierChainID, now)

			if c.Expected.Valid {
				valid++
				if gotErr != nil {
					t.Fatalf("expected valid, got error: %v", gotErr)
				}
				return
			}

			invalid++
			if gotErr == nil {
				t.Fatalf("expected failure %q, got nil", c.Expected.Error)
			}
			reason, ok := ReasonOf(gotErr)
			if !ok {
				t.Fatalf("error is not a *VerificationError: %v", gotErr)
			}
			allowed, known := enumReasons[c.Expected.Error]
			if !known {
				t.Fatalf("test gap: no Reason mapping for enum %q", c.Expected.Error)
			}
			if !allowed[reason] {
				t.Fatalf("enum %q: got Reason %q, want one of %v (err: %v)",
					c.Expected.Error, reason, keys(allowed), gotErr)
			}
			if want, pinned := caseNameReason[c.Name]; pinned && reason != want {
				t.Fatalf("case %q: got Reason %q, want refined %q", c.Name, reason, want)
			}
		})
	}

	if valid != wantValid {
		t.Errorf("expected %d valid case(s), saw %d", wantValid, valid)
	}
	if invalid != wantInvalid {
		t.Errorf("expected %d failure cases, saw %d", wantInvalid, invalid)
	}
}

// TestGoldenVectorConstants asserts this package's protocol constants match the
// committed vector file — a drift check against celestia-app.
func TestGoldenVectorConstants(t *testing.T) {
	c := loadGolden(t).Constants

	check := func(name string, got, want any) {
		if got != want {
			t.Errorf("%s: have %v, vectors say %v", name, got, want)
		}
	}
	check("extension_oid", ExtensionOID, c.ExtensionOID)
	check("sign_unique_id", SignUniqueID, c.SignUniqueID)
	check("sign_prefix", SignPrefix, c.SignPrefix)
	check("envelope_prefix", envelopePrefix, c.EnvelopePrefix)
	check("binding_version", BindingVersion, c.BindingVersion)
	check("max_identity_extension_size", MaxIdentityExtensionSize, c.MaxIdentityExtensionSize)
	check("max_payload_der_size", MaxPayloadDERSize, c.MaxPayloadDERSize)
	check("max_cert_validity_seconds", int64(MaxCertValidity/time.Second), c.MaxCertValiditySeconds)
	check("clock_skew_seconds", int64(ClockSkew/time.Second), c.ClockSkewSeconds)
}

// TestEnvelopeMatchesVectors proves the hand-rolled RawBytesMessageSignBytes
// reproduction is byte-exact against upstream, with no cryptography involved:
// for every case that carries producer bytes, rebuild sign_input and the signed
// envelope from payload_der + chain_id and compare to the committed hex.
func TestEnvelopeMatchesVectors(t *testing.T) {
	f := loadGolden(t)
	seen := 0
	for _, c := range f.Cases {
		if c.PayloadDER == "" || c.SignedBytes == "" {
			continue // case corrupts/omits the signed layer
		}
		seen++
		payload := mustHex(t, c.PayloadDER)

		gotSignInput := signInputBytes(payload)
		if want := mustHex(t, c.SignInput); !bytesEqual(gotSignInput, want) {
			t.Errorf("%s: sign_input mismatch\n got %x\nwant %x", c.Name, gotSignInput, want)
		}

		gotEnvelope := rawBytesMessageSignBytes(c.ChainID, SignUniqueID, gotSignInput)
		if want := mustHex(t, c.SignedBytes); !bytesEqual(gotEnvelope, want) {
			t.Errorf("%s: signed_bytes mismatch\n got %x\nwant %x", c.Name, gotEnvelope, want)
		}
	}
	if seen < 15 {
		t.Fatalf("only %d cases carried producer bytes; expected most of them", seen)
	}
}

// TestInspectSurvivesFailures: Inspect should still surface the claimed validity
// window for a cert that fails full verification (e.g. the expired case), so a
// probe can record what the peer presented.
func TestInspectSurvivesFailures(t *testing.T) {
	f := loadGolden(t)
	for _, c := range f.Cases {
		if c.Name != "expired" {
			continue
		}
		cert, err := x509.ParseCertificate(mustHex(t, c.CertDER))
		if err != nil {
			t.Fatal(err)
		}
		id, err := Inspect(cert)
		if err != nil {
			t.Fatalf("Inspect on expired cert: %v", err)
		}
		if id.NotAfter.IsZero() || !id.NotAfter.After(id.NotBefore) {
			t.Fatalf("Inspect returned nonsense window: %+v", id)
		}
		// And full verification must still reject it as expired.
		if err := VerifyCertificateAt(cert, ed25519.PublicKey(mustHex(t, c.VerifierConsensusPub)),
			c.VerifierChainID, time.Unix(c.VerifyAt, 0)); err == nil {
			t.Fatal("expired cert verified")
		} else if r, _ := ReasonOf(err); r != ReasonCertExpired {
			t.Fatalf("expired cert: Reason %q", r)
		}
		return
	}
	t.Fatal("no 'expired' case found")
}

// TestVerifyConnectionAndConfig exercises the tls.Config wiring against the
// valid case's certificate.
func TestVerifyConnectionAndConfig(t *testing.T) {
	f := loadGolden(t)
	var valid goldenCase
	for _, c := range f.Cases {
		if c.Name == "valid" {
			valid = c
		}
	}
	cert, err := x509.ParseCertificate(mustHex(t, valid.CertDER))
	if err != nil {
		t.Fatal(err)
	}
	pub := ed25519.PublicKey(mustHex(t, valid.VerifierConsensusPub))

	// VerifyCertificate uses time.Now(); the valid case is dated 2025-2027 so it
	// is in force today. If this test ever runs after 2027-01-01 it will fail —
	// acceptable for a golden fixture.
	if err := VerifyCertificate(cert, pub, valid.VerifierChainID); err != nil {
		t.Fatalf("VerifyCertificate: %v", err)
	}

	cfg := ClientTLSConfig(pub, valid.VerifierChainID)
	if cfg.MinVersion != 0x0304 { // tls.VersionTLS13
		t.Errorf("MinVersion = %#x, want TLS 1.3", cfg.MinVersion)
	}
	if !cfg.InsecureSkipVerify {
		t.Error("InsecureSkipVerify must be set (auth is via VerifyConnection)")
	}
	if cfg.VerifyConnection == nil {
		t.Fatal("VerifyConnection not installed")
	}
}

// TestReasonHelpers checks the error API surface.
func TestReasonHelpers(t *testing.T) {
	_, err := VerifyAndInspectAt(nil, make(ed25519.PublicKey, ed25519.PublicKeySize), "x", time.Now())
	if r, ok := ReasonOf(err); !ok || r != ReasonBadInput {
		t.Fatalf("nil cert: got (%q,%v), want (bad_input,true)", r, ok)
	}
	if _, ok := ReasonOf(nil); ok {
		t.Error("ReasonOf(nil) should be !ok")
	}
	if _, ok := ReasonOf(os.ErrNotExist); ok {
		t.Error("ReasonOf(foreign error) should be !ok")
	}
}

func keys(m map[Reason]bool) []Reason {
	out := make([]Reason, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func bytesEqual(a, b []byte) bool {
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
