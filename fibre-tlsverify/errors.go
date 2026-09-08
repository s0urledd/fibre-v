package tlsverify

import (
	"errors"
	"fmt"
)

// Reason is a stable, machine-readable classification of a verification outcome.
// The string values are part of this package's contract: probes and dashboards
// key on them. They mirror the error enum published in
// specs/src/fibre_tls_identity.md and testdata/identity_vectors.json, with two
// deliberate refinements (see the constants).
type Reason string

const (
	// ReasonBadInput: the call itself is malformed — nil/short expected key,
	// empty chain ID, nil certificate. Not a statement about the peer.
	ReasonBadInput Reason = "bad_input"

	// ReasonNoCertificate: the TLS peer presented no certificate at all.
	ReasonNoCertificate Reason = "no_certificate"

	// ReasonExtensionMissing: the leaf certificate carries no extension under
	// the fibre identity OID.
	ReasonExtensionMissing Reason = "extension_missing"

	// ReasonExtensionTooLarge: the extension value exceeds MaxIdentityExtensionSize.
	ReasonExtensionTooLarge Reason = "extension_too_large"

	// ReasonExtensionMalformed: the extension value is not a DER SignedIdentity
	// (also used when raw peer certificate bytes fail to parse).
	ReasonExtensionMalformed Reason = "extension_malformed"

	// ReasonExtensionTrailingData: bytes remain after the SignedIdentity DER.
	ReasonExtensionTrailingData Reason = "extension_trailing_data"

	// ReasonPayloadEmpty: SignedIdentity.payload is a zero-length OCTET STRING.
	ReasonPayloadEmpty Reason = "payload_empty"

	// ReasonPayloadTooLarge: SignedIdentity.payload exceeds MaxPayloadDERSize.
	ReasonPayloadTooLarge Reason = "payload_too_large"

	// ReasonSignatureEmpty: SignedIdentity.signature is a zero-length OCTET STRING.
	ReasonSignatureEmpty Reason = "signature_empty"

	// ReasonPayloadMalformed: SignedIdentity.payload is not a DER BindingPayload.
	ReasonPayloadMalformed Reason = "payload_malformed"

	// ReasonBindingTrailingData: bytes remain after the BindingPayload DER
	// inside the signed payload.
	ReasonBindingTrailingData Reason = "binding_trailing_data"

	// ReasonUnsupportedVersion: BindingPayload.version is not 1.
	ReasonUnsupportedVersion Reason = "unsupported_version"

	// ReasonSignatureInvalid: the endorsement signature does not verify under
	// the expected consensus key over the recomputed envelope. This one Reason
	// covers a wrong expected key, a wrong chain ID in the envelope, and a
	// tampered signature — the verifier cannot distinguish them. A caller that
	// trusts its own chainID input can read this as "not the expected validator".
	ReasonSignatureInvalid Reason = "signature_invalid"

	// ReasonTLSKeyMismatch: the certificate's public key is not the tlsPubKey
	// that was signed in the binding.
	ReasonTLSKeyMismatch Reason = "tls_key_mismatch"

	// ReasonWindowEmpty: signed notAfter is not after signed notBefore.
	ReasonWindowEmpty Reason = "window_empty"

	// ReasonWindowTooLong: the signed window is longer than MaxCertValidity.
	ReasonWindowTooLong Reason = "window_too_long"

	// ReasonCertNotYetValid: verification instant precedes notBefore - clockSkew.
	// Upstream folds this into a single "outside validity window" error; split
	// here so a probe can tell a clock problem from an expiry.
	ReasonCertNotYetValid Reason = "cert_not_yet_valid"

	// ReasonCertExpired: verification instant is after notAfter + clockSkew.
	// See ReasonCertNotYetValid on the split.
	ReasonCertExpired Reason = "cert_expired"

	// ReasonCertWindowMismatch: the certificate's own NotBefore/NotAfter do not
	// equal the signed window.
	ReasonCertWindowMismatch Reason = "cert_window_mismatch"

	// ReasonEKUMissing: the certificate lacks the serverAuth extended key usage.
	ReasonEKUMissing Reason = "eku_missing"
)

// VerificationError is returned by every failing call in this package. The
// Reason field is the machine-readable classification; Error() adds human
// context and any wrapped decoder error.
type VerificationError struct {
	Reason Reason
	msg    string
	err    error // wrapped low-level error (asn1, x509), may be nil
}

func (e *VerificationError) Error() string {
	switch {
	case e.err != nil && e.msg != "":
		return fmt.Sprintf("fibre tls identity [%s]: %s: %v", e.Reason, e.msg, e.err)
	case e.err != nil:
		return fmt.Sprintf("fibre tls identity [%s]: %v", e.Reason, e.err)
	case e.msg != "":
		return fmt.Sprintf("fibre tls identity [%s]: %s", e.Reason, e.msg)
	default:
		return fmt.Sprintf("fibre tls identity [%s]", e.Reason)
	}
}

func (e *VerificationError) Unwrap() error { return e.err }

// Is reports a match on Reason, so errors.Is(err, &VerificationError{Reason: R})
// works as a terse check.
func (e *VerificationError) Is(target error) bool {
	t, ok := target.(*VerificationError)
	return ok && t.Reason == e.Reason
}

// ReasonOf extracts the Reason from an error produced by this package. ok is
// false for a nil error or one that did not originate here.
func ReasonOf(err error) (r Reason, ok bool) {
	var ve *VerificationError
	if errors.As(err, &ve) {
		return ve.Reason, true
	}
	return "", false
}

func failf(r Reason, format string, a ...any) *VerificationError {
	return &VerificationError{Reason: r, msg: fmt.Sprintf(format, a...)}
}

func wrapErr(r Reason, msg string, err error) *VerificationError {
	return &VerificationError{Reason: r, msg: msg, err: err}
}
