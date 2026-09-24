package export

// On-chain anchoring of an export (optional).
//
// A signature says who published an export; it does not say when. An
// operator holding the key could build a different export for a past day,
// sign it, and swap it in, and nothing in the signature would show it. An
// anchor fixes the manifest digest in time: the day's digest goes into a
// regular Celestia blob (MsgPayForBlobs) under a dedicated namespace, and the
// block that includes it is a public, timestamped record that this digest
// existed then. Anyone can list the namespace and compare.
//
// This file only builds the blob's payload, so that the builder's tests and
// the command that submits it (cmd/sentinel-anchor) agree on its shape. The
// payload is small canonical JSON (keys in a fixed order, no whitespace),
// about 500 bytes: one or two 512-byte shares, next to the cheapest blob
// there is.

import (
	"encoding/json"
	"errors"
)

// AnchorNamespaceID is the default 10-byte version-0 namespace ID anchors
// are published under: the ASCII bytes "tensileexp". Any observer may use
// it; the payload names the vantage and the signing key, so anchors from two
// observers in one namespace are not confused.
const AnchorNamespaceID = "tensileexp"

// AnchorPayloadType versions the payload.
const AnchorPayloadType = "tensile-export-anchor/v1"

// AnchorPayload is the blob content.
type AnchorPayload struct {
	Type           string `json:"type"`
	Vantage        string `json:"vantage"`
	Day            string `json:"day"`
	Export         string `json:"export"`
	ManifestSHA256 string `json:"manifest_sha256"`
	TarballSHA256  string `json:"tarball_sha256"`
	// KeyFingerprint and Signature are the export's signature when it has
	// one, so the anchor binds the digest, the key and the time together.
	// Empty for an unsigned export: an anchor still fixes the digest in time.
	KeyFingerprint string `json:"key_fingerprint,omitempty"`
	Signature      string `json:"signature,omitempty"`
}

// NewAnchorPayload builds the payload for an export from its parsed archive
// and, when it was signed, its signature. The manifest digest is recomputed
// from the tarball rather than taken from the index, so an anchor can only
// ever name bytes the anchoring machine actually holds.
func NewAnchorPayload(name string, a *Archive, sig *Signature) (AnchorPayload, error) {
	if a == nil {
		return AnchorPayload{}, errors.New("no archive")
	}
	p := AnchorPayload{Type: AnchorPayloadType, Vantage: a.Manifest.Vantage, Day: a.Manifest.Day, Export: name,
		ManifestSHA256: a.ManifestSHA256, TarballSHA256: a.SHA256}
	if sig != nil {
		if sig.ManifestSHA256 != a.ManifestSHA256 {
			return AnchorPayload{}, errors.New("the signature is over a different manifest than the tarball holds")
		}
		p.KeyFingerprint, p.Signature = sig.KeyFingerprint, sig.Signature
	}
	return p, nil
}

// Bytes is the canonical encoding: encoding/json writes struct fields in
// declaration order with no whitespace, so the same payload is always the
// same bytes.
func (p AnchorPayload) Bytes() ([]byte, error) { return json.Marshal(p) }
