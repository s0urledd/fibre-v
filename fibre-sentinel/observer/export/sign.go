package export

// Signed exports.
//
// A daily export already carries its own digests: the tarball's sha256 in
// the index and in the .sha256 sidecar, and every member's sha256 in
// manifest.json. Those let two verifiers agree they hold the same bytes, but
// not that the bytes came from this observer: anyone who can serve a
// tarball can serve a matching sidecar. A signature closes that gap. The
// observer's operator holds an ed25519 key, and every export built while the
// key is configured gets a signature over its manifest digest, published
// beside the tarball (<name>.sig) and in the index entry. The public key is
// served at /v1/exports/pubkey and recorded in the exports directory
// (signing-keys.json), so an export downloaded today can be checked years
// from now against the key that signed it, and a key rotation leaves the old
// exports verifiable.
//
// What is signed is the manifest digest, not the tarball digest, and that is
// deliberate. The manifest names every member with its sha256, its line
// count and the byte range of the source file it came from, so a signature
// over the manifest commits to every record in the export; the tarball digest
// adds only gzip framing and tar headers, which a verifier who re-packs the
// members would change without changing a single record. Signing the
// manifest is also what lets the same 32-byte digest be anchored on chain
// (cmd/sentinel-anchor) without the chain having to hold anything else.
//
// The message is the ASCII string SignatureDomain + hex(sha256(manifest.json)),
// with no trailing newline. The domain prefix keeps the signature from being
// replayable as a signature over some other 64-hex-character string this key
// might one day sign. The message is short and printable on purpose: anyone
// with OpenSSL 3 can rebuild it with printf and check it, with no tool from
// this repository (see docs/exports-signing.md for the exact commands).
//
// No key configured means no signature, and the export, the sidecar and the
// index entry are byte-for-byte what they were before signing existed: the
// Signature field is omitted, not written empty.

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// SignatureDomain prefixes the manifest digest in the signed message.
const SignatureDomain = "tensile-export-manifest/v1:"

// SigningKeysFile is the record, in the exports directory, of every public
// key that has signed an export here. The API serves it at
// /v1/exports/pubkey.
const SigningKeysFile = "signing-keys.json"

// SigningKeyEnv is the environment variable the collector reads the key path
// from when -export-signing-key is not given.
const SigningKeyEnv = "TENSILE_EXPORT_SIGNING_KEY"

// Signature is what <name>.sig holds and what the index entry carries.
type Signature struct {
	// Algorithm is always "ed25519".
	Algorithm string `json:"algorithm"`
	// ManifestSHA256 is hex(sha256(manifest.json)) as the tarball holds it.
	ManifestSHA256 string `json:"manifest_sha256"`
	// Message is the exact byte string that was signed, spelled out so a
	// verifier does not have to reconstruct it from this documentation.
	Message string `json:"message"`
	// Signature is the 64-byte ed25519 signature, standard base64.
	Signature string `json:"signature"`
	// KeyFingerprint is "sha256:" + hex(sha256(raw 32-byte public key)).
	KeyFingerprint string `json:"key_fingerprint"`
	// PublicKey is the raw 32-byte public key, standard base64. It is here
	// for convenience only: a signature verified against the key it carries
	// proves nothing, so a verifier checks KeyFingerprint against a key it
	// obtained independently (/v1/exports/pubkey, the operator's own
	// announcement) and only then uses this.
	PublicKey string `json:"public_key"`
}

// SigningMessage is the exact byte string signed for a manifest digest.
func SigningMessage(manifestSHA256 string) []byte {
	return []byte(SignatureDomain + manifestSHA256)
}

// Fingerprint is the fingerprint of an ed25519 public key.
func Fingerprint(pub ed25519.PublicKey) string {
	sum := sha256.Sum256(pub)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// Signer holds the observer's export signing key.
type Signer struct {
	priv ed25519.PrivateKey
	pub  ed25519.PublicKey
}

// NewSigner wraps an ed25519 private key.
func NewSigner(priv ed25519.PrivateKey) *Signer {
	return &Signer{priv: priv, pub: priv.Public().(ed25519.PublicKey)}
}

// PublicKey is the signer's public key.
func (s *Signer) PublicKey() ed25519.PublicKey { return s.pub }

// Sign signs a manifest digest.
func (s *Signer) Sign(manifestSHA256 string) *Signature {
	msg := SigningMessage(manifestSHA256)
	return &Signature{
		Algorithm:      "ed25519",
		ManifestSHA256: manifestSHA256,
		Message:        string(msg),
		Signature:      base64.StdEncoding.EncodeToString(ed25519.Sign(s.priv, msg)),
		KeyFingerprint: Fingerprint(s.pub),
		PublicKey:      base64.StdEncoding.EncodeToString(s.pub),
	}
}

// LoadSigner reads a PKCS#8 PEM ed25519 private key, the format
// `openssl genpkey -algorithm ed25519` writes and GenerateKey below writes.
// Any other key type is an error, never a silent fallback to unsigned: an
// operator who configured a key expects signatures.
func LoadSigner(path string) (*Signer, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		return nil, fmt.Errorf("%s: no PEM block", path)
	}
	if block.Type != "PRIVATE KEY" {
		return nil, fmt.Errorf("%s: PEM block is %q, want \"PRIVATE KEY\" (PKCS#8, as openssl genpkey -algorithm ed25519 writes)", path, block.Type)
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	priv, ok := key.(ed25519.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("%s: key is %T, want ed25519", path, key)
	}
	return NewSigner(priv), nil
}

// GenerateKey writes a fresh ed25519 key: the private key as PKCS#8 PEM to
// path (mode 0600, refusing to overwrite) and the public key as PKIX PEM to
// path + ".pub". It returns the fingerprint.
func GenerateKey(path string) (string, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", err
	}
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return "", err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", err
	}
	if err := pem.Encode(f, &pem.Block{Type: "PRIVATE KEY", Bytes: der}); err != nil {
		f.Close()
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	pubPEM, err := PublicKeyPEM(pub)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path+".pub", []byte(pubPEM), 0o644); err != nil {
		return "", err
	}
	return Fingerprint(pub), nil
}

// PublicKeyPEM is the PKIX PEM encoding of an ed25519 public key, the form
// `openssl pkeyutl -pubin -inkey` reads.
func PublicKeyPEM(pub ed25519.PublicKey) (string, error) {
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return "", err
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})), nil
}

// ParsePublicKeys is ParsePublicKey, except that the JSON /v1/exports/pubkey
// answers with gives every key it lists, the current one first: after a
// rotation the exports signed before it are checked against the key that
// signed them, which is no longer current.
func ParsePublicKeys(raw []byte) ([]ed25519.PublicKey, error) {
	s := strings.TrimSpace(string(raw))
	if !strings.HasPrefix(s, "{") {
		pub, err := ParsePublicKey(raw)
		if err != nil {
			return nil, err
		}
		return []ed25519.PublicKey{pub}, nil
	}
	var doc struct {
		Keys []SigningKey `json:"keys"`
	}
	if err := json.Unmarshal([]byte(s), &doc); err != nil {
		return nil, err
	}
	cur, err := ParsePublicKey(raw)
	if err != nil {
		return nil, err
	}
	out := []ed25519.PublicKey{cur}
	for _, k := range doc.Keys {
		pub, err := ParsePublicKey([]byte(k.PublicKey))
		if err != nil {
			return nil, fmt.Errorf("key %s: %w", k.KeyFingerprint, err)
		}
		if !pub.Equal(cur) {
			out = append(out, pub)
		}
	}
	return out, nil
}

// KeyFor picks the key among pubs whose fingerprint the signature names, or
// the first when none does (VerifySignature then reports the mismatch). The
// signature only chooses among keys the caller already trusts; it never
// supplies one.
func KeyFor(pubs []ed25519.PublicKey, sig *Signature) ed25519.PublicKey {
	if len(pubs) == 0 {
		return nil
	}
	if sig != nil {
		for _, p := range pubs {
			if Fingerprint(p) == sig.KeyFingerprint {
				return p
			}
		}
	}
	return pubs[0]
}

// ParsePublicKey accepts a PKIX PEM public key, a raw 32-byte key in
// standard base64 or hex, or the JSON /v1/exports/pubkey answers with (the
// current key is used).
func ParsePublicKey(raw []byte) (ed25519.PublicKey, error) {
	s := strings.TrimSpace(string(raw))
	if strings.HasPrefix(s, "{") {
		var doc struct {
			Current *SigningKey `json:"current"`
		}
		if err := json.Unmarshal([]byte(s), &doc); err != nil {
			return nil, err
		}
		if doc.Current == nil {
			return nil, errors.New("no current key in the JSON")
		}
		return ParsePublicKey([]byte(doc.Current.PublicKey))
	}
	if block, _ := pem.Decode([]byte(s)); block != nil {
		key, err := x509.ParsePKIXPublicKey(block.Bytes)
		if err != nil {
			return nil, err
		}
		pub, ok := key.(ed25519.PublicKey)
		if !ok {
			return nil, fmt.Errorf("public key is %T, want ed25519", key)
		}
		return pub, nil
	}
	if b, err := hex.DecodeString(s); err == nil && len(b) == ed25519.PublicKeySize {
		return ed25519.PublicKey(b), nil
	}
	if b, err := base64.StdEncoding.DecodeString(s); err == nil && len(b) == ed25519.PublicKeySize {
		return ed25519.PublicKey(b), nil
	}
	return nil, errors.New("not a PEM, hex or base64 ed25519 public key")
}

// VerifySignature checks sig against manifestSHA256 and a public key the
// caller obtained independently. It never trusts the key the signature
// carries: the fingerprint must match pub.
func VerifySignature(sig *Signature, manifestSHA256 string, pub ed25519.PublicKey) error {
	if sig == nil {
		return errors.New("no signature")
	}
	if sig.Algorithm != "ed25519" {
		return fmt.Errorf("algorithm %q, want ed25519", sig.Algorithm)
	}
	if !strings.EqualFold(sig.ManifestSHA256, manifestSHA256) {
		return fmt.Errorf("signature covers manifest %s, the export's manifest is %s", sig.ManifestSHA256, manifestSHA256)
	}
	if fp := Fingerprint(pub); sig.KeyFingerprint != fp {
		return fmt.Errorf("signed by key %s, verifying against %s", sig.KeyFingerprint, fp)
	}
	msg := SigningMessage(strings.ToLower(manifestSHA256))
	if sig.Message != "" && sig.Message != string(msg) {
		return fmt.Errorf("message %q is not the one this format signs (%q)", sig.Message, msg)
	}
	raw, err := base64.StdEncoding.DecodeString(sig.Signature)
	if err != nil {
		return fmt.Errorf("signature is not base64: %w", err)
	}
	if !ed25519.Verify(pub, msg, raw) {
		return errors.New("ed25519 signature does not verify")
	}
	return nil
}

// SigningKey is one entry of signing-keys.json.
type SigningKey struct {
	Algorithm      string `json:"algorithm"`
	KeyFingerprint string `json:"key_fingerprint"`
	// PublicKey is the raw 32-byte key, standard base64.
	PublicKey    string `json:"public_key"`
	PublicKeyPEM string `json:"public_key_pem"`
	// FirstDay and LastDay are the export days this key signed.
	FirstDay string `json:"first_day"`
	LastDay  string `json:"last_day"`
}

// ReadSigningKeys returns every key that signed an export in dir, newest
// first by the last day it signed. A missing file is an empty list: exports
// from an observer that never signed.
func ReadSigningKeys(dir string) ([]SigningKey, error) {
	raw, err := os.ReadFile(filepath.Join(dir, SigningKeysFile))
	if errors.Is(err, os.ErrNotExist) {
		return []SigningKey{}, nil
	}
	if err != nil {
		return nil, err
	}
	var out []SigningKey
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("%s: %w", SigningKeysFile, err)
	}
	if out == nil {
		out = []SigningKey{}
	}
	return out, nil
}

// recordSigningKey adds or widens pub's entry in signing-keys.json. It is
// written before the index names an export signed by the key, so an index
// entry never points at a key the directory does not publish.
func recordSigningKey(dir string, pub ed25519.PublicKey, day string) error {
	keys, err := ReadSigningKeys(dir)
	if err != nil {
		return err
	}
	fp := Fingerprint(pub)
	found := false
	for i := range keys {
		if keys[i].KeyFingerprint != fp {
			continue
		}
		found = true
		if day < keys[i].FirstDay || keys[i].FirstDay == "" {
			keys[i].FirstDay = day
		}
		if day > keys[i].LastDay {
			keys[i].LastDay = day
		}
	}
	if !found {
		pemStr, err := PublicKeyPEM(pub)
		if err != nil {
			return err
		}
		keys = append(keys, SigningKey{Algorithm: "ed25519", KeyFingerprint: fp, PublicKey: base64.StdEncoding.EncodeToString(pub),
			PublicKeyPEM: pemStr, FirstDay: day, LastDay: day})
	}
	// Newest last day first, and on a tie the key signing now: a rotation
	// that re-signs a day the old key had signed (a rebuild after a crash
	// before the state was saved) must make the new key current, since the
	// .sig and the index about to be written name it.
	sort.SliceStable(keys, func(i, j int) bool {
		if keys[i].LastDay != keys[j].LastDay {
			return keys[i].LastDay > keys[j].LastDay
		}
		return keys[i].KeyFingerprint == fp && keys[j].KeyFingerprint != fp
	})
	raw, err := json.MarshalIndent(keys, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(filepath.Join(dir, SigningKeysFile), raw)
}
