// Package tlsverify is a standalone, dependency-free verifier for the Fibre
// validator-endorsed TLS identity ("celestia-fibre-tls-v1").
//
// A Fibre server presents a self-signed TLS certificate that carries a custom,
// non-critical X.509 extension under OID 1.3.6.1.4.1.66463.1.1. The extension
// binds the ephemeral TLS key to a validator's consensus identity: it holds a
// signed statement, produced by the validator's consensus key, that says "this
// TLS public key is mine for this validity window". There is no CA and no SAN
// chain; trust comes entirely from the consensus-key signature verifying under a
// public key the caller already knows from the chain's validator set.
//
// This package re-implements only the protocol framing and verification logic of
// celestia-app's internal fibre/internal/tlsid package, which cannot be imported
// from outside the celestia-app module. It does not re-implement cryptography:
// signatures are checked with crypto/ed25519, certificates are parsed with
// crypto/x509, and structures are decoded with encoding/asn1. The one piece of
// framing it reproduces by hand is cometbft's RawBytesMessageSignBytes envelope
// (see envelope.go), which is plain protobuf and varint work.
//
// Correctness is pinned by TestGoldenVectors, which runs every case in
// testdata/identity_vectors.json — the file copied verbatim from
// celestia-app/fibre/internal/tlsid/testdata (21 cases as of the copy). The
// single valid case must pass; each failure case must fail with the matching
// machine-readable Reason.
//
// # Usage
//
// For a client dialing a Fibre server, install VerifyConnection on a TLS 1.3
// config with InsecureSkipVerify set (the custom verifier replaces CA/hostname
// validation):
//
//	cfg := tlsverify.ClientTLSConfig(expectedConsensusKey, chainID)
//	conn, err := tls.Dial("tcp", host, cfg)
//
// VerifyConnection (not VerifyPeerCertificate) is used because it also runs on
// resumed TLS 1.3 sessions, so peer identity is re-checked on every connection.
//
// # Error classification
//
// Every failure returns a *VerificationError whose Reason is a stable, machine
// readable enum. Callers switch on Reason (or use ReasonOf) to build their own
// taxonomy; a Fibre Sentinel probe records the Reason, not a boolean.
//
// The verifier cannot, on its own, tell ReasonSignatureInvalid apart into
// "wrong validator key", "wrong chain ID", or "tampered signature" — from inside
// the check a bad signature is a bad signature. A caller that is certain of the
// chainID it passed can attribute a ReasonSignatureInvalid to the peer key.
package tlsverify
