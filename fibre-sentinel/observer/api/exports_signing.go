package api

// The export signing key, as the API publishes it.
//
// The collector signs each daily export with an ed25519 key and records the
// public half in <exports>/signing-keys.json (observer/export/sign.go). This
// process never holds the private key and never needs it: it serves the
// record the collector wrote, so the key a reader fetches here is exactly the
// one the index and the .sig files name. Every key that ever signed an
// export here stays in the list, with the days it covered, so a rotation
// leaves older exports verifiable.
//
// A reader should fetch this once and keep it. A key fetched from the same
// origin as the export it verifies proves only that both came from the same
// server; the value of a signature grows with how independently the key was
// obtained (the operator's own announcement, an earlier download, the on-chain
// anchor that names its fingerprint).

import (
	"net/http"

	"github.com/plsgiveup/fibre/fibre-sentinel/observer/export"
)

// exportVerifyCommand is the whole check with nothing but OpenSSL 3 and
// standard tools, quoted in both the pubkey answer and the exports list.
const exportVerifyCommand = `N=tensile-<vantage>-<day>.tar.gz; ` +
	`curl -sO $API/v1/exports/$N -O $API/v1/exports/$N.sig; ` +
	`curl -s $API/v1/exports/pubkey | jq -r .current.public_key_pem > tensile-exports.pem; ` +
	`printf '%s' "` + export.SignatureDomain + `$(tar -xzOf $N manifest.json | sha256sum | cut -c1-64)" > msg; ` +
	`jq -r .signature $N.sig | base64 -d > sig.bin; ` +
	`openssl pkeyutl -verify -pubin -inkey tensile-exports.pem -rawin -in msg -sigfile sig.bin`

// exportSigning is the "signing" block of /v1/exports and the body of
// /v1/exports/pubkey.
type exportSigning struct {
	Signed bool `json:"signed"`
	// Current is the key that signed the newest signed export.
	Current *export.SigningKey  `json:"current,omitempty"`
	Keys    []export.SigningKey `json:"keys"`
	// Message is how the signed bytes are formed.
	Message       string `json:"message"`
	HowToVerify   string `json:"how_to_verify"`
	VerifyCommand string `json:"verify_command"`
	Note          string `json:"note"`
}

func (s *Server) exportSigning() (*exportSigning, error) {
	out := &exportSigning{Keys: []export.SigningKey{},
		Message: `the ASCII string "` + export.SignatureDomain + `" followed by the lowercase hex sha256 of manifest.json as the tarball holds it, no newline; ed25519`,
		HowToVerify: "sentinel-verify-export -pubkey <this key> <export>.tar.gz checks the sidecar, every member against the manifest, and the signature, offline. " +
			"verify_command does the signature check with OpenSSL 3 alone.",
		VerifyCommand: exportVerifyCommand,
		Note: "Fetch the key once and keep it: a key fetched from the same server as the export proves only that both came from that server. " +
			"Every key that ever signed an export here stays listed with the days it signed. Exports built before signing was switched on have no signature.",
	}
	dir := s.exportsDir()
	if dir == "" {
		return out, nil
	}
	keys, err := export.ReadSigningKeys(dir)
	if err != nil {
		return nil, err
	}
	out.Keys = keys
	if len(keys) > 0 {
		cur := keys[0] // newest last_day first
		out.Current, out.Signed = &cur, true
	}
	return out, nil
}

// handleExportPubkey serves the export signing key(s). 404 when this
// observer has never signed an export, in the API's JSON error shape, so a
// script checking signatures fails loudly instead of verifying against
// nothing.
func (s *Server) handleExportPubkey(w http.ResponseWriter, r *http.Request) {
	sig, err := s.exportSigning()
	if err != nil {
		s.writeInternal(w, r.URL.Path, err)
		return
	}
	if !sig.Signed {
		writeErr(w, 404, "this observer's exports are not signed")
		return
	}
	writeJSON(w, 200, sig)
}
