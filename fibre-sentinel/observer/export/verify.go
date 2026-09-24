package export

// Verifying an export from the outside: what cmd/sentinel-verify-export runs,
// written here so the builder's tests can hold the two to each other. Every
// check reads only the bytes a downloader has — the tarball, its .sha256
// sidecar, its .sig, and a public key obtained separately — and nothing from
// the observer's data directory.

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// maxMember bounds one member read from an untrusted tarball, so a
// decompression bomb cannot take the verifier's memory. A day's
// measurements file is tens of megabytes; this is far above that.
const maxMember = 4 << 30

// Archive is a parsed export tarball.
type Archive struct {
	SHA256         string            // of the tarball bytes
	Members        map[string][]byte // by name, manifest.json included
	ManifestRaw    []byte
	Manifest       Manifest
	ManifestSHA256 string
}

// ReadArchive parses an export tarball and digests its manifest.
func ReadArchive(tarball []byte) (*Archive, error) {
	sum := sha256.Sum256(tarball)
	a := &Archive{SHA256: hex.EncodeToString(sum[:]), Members: map[string][]byte{}}
	gz, err := gzip.NewReader(bytes.NewReader(tarball))
	if err != nil {
		return nil, fmt.Errorf("not gzip: %w", err)
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("tar: %w", err)
		}
		// '\x00' is the pre-POSIX spelling of a regular file; the reader
		// normalises it, but an old writer's tarball is still a tarball.
		if h.Typeflag != tar.TypeReg && h.Typeflag != '\x00' {
			return nil, fmt.Errorf("tar member %q is not a regular file", h.Name)
		}
		if _, dup := a.Members[h.Name]; dup {
			// Two members under one name would let a verifier and a tool
			// that extracts to disk read different bytes for the same file.
			return nil, fmt.Errorf("tar member %q appears twice", h.Name)
		}
		b, err := io.ReadAll(io.LimitReader(tr, maxMember+1))
		if err != nil {
			return nil, fmt.Errorf("tar member %q: %w", h.Name, err)
		}
		if len(b) > maxMember {
			return nil, fmt.Errorf("tar member %q is larger than %d bytes", h.Name, maxMember)
		}
		a.Members[h.Name] = b
	}
	raw, ok := a.Members["manifest.json"]
	if !ok {
		return nil, errors.New("no manifest.json in the tarball")
	}
	a.ManifestRaw = raw
	ms := sha256.Sum256(raw)
	a.ManifestSHA256 = hex.EncodeToString(ms[:])
	if err := json.Unmarshal(raw, &a.Manifest); err != nil {
		return nil, fmt.Errorf("manifest.json: %w", err)
	}
	return a, nil
}

// CheckMembers holds every member to the manifest: present, the right
// digest, the right byte count, the right number of lines, and nothing in
// the tarball the manifest does not name. It returns every problem, not the
// first.
func (a *Archive) CheckMembers() []string {
	var problems []string
	named := map[string]bool{"manifest.json": true}
	check := func(m Member) {
		named[m.Name] = true
		b, ok := a.Members[m.Name]
		if !ok {
			problems = append(problems, fmt.Sprintf("%s: in the manifest, not in the tarball", m.Name))
			return
		}
		sum := sha256.Sum256(b)
		if got := hex.EncodeToString(sum[:]); got != m.SHA256 {
			problems = append(problems, fmt.Sprintf("%s: sha256 %s, manifest says %s", m.Name, got, m.SHA256))
		}
		if int64(len(b)) != m.Bytes {
			problems = append(problems, fmt.Sprintf("%s: %d bytes, manifest says %d", m.Name, len(b), m.Bytes))
		}
		if m.Name != StateFile {
			if n := countLines(b); n != m.Lines {
				problems = append(problems, fmt.Sprintf("%s: %d lines, manifest says %d", m.Name, n, m.Lines))
			}
		}
	}
	for _, m := range a.Manifest.Files {
		check(m)
	}
	if a.Manifest.State != nil {
		check(*a.Manifest.State)
	}
	for name := range a.Members {
		if !named[name] {
			problems = append(problems, fmt.Sprintf("%s: in the tarball, not in the manifest", name))
		}
	}
	return problems
}

// countLines counts the non-blank lines the builder counts: collect skips
// whitespace-only lines without counting them but keeps their bytes.
func countLines(b []byte) int64 {
	var n int64
	for _, l := range bytes.Split(b, []byte{'\n'}) {
		if len(bytes.TrimSpace(l)) > 0 {
			n++
		}
	}
	return n
}

// CheckSidecar compares the tarball digest with a .sha256 sidecar
// ("<hex>  <name>\n", the sha256sum format).
func (a *Archive) CheckSidecar(sidecar []byte, name string) error {
	f := strings.Fields(string(sidecar))
	if len(f) == 0 {
		return errors.New("empty sidecar")
	}
	if !strings.EqualFold(f[0], a.SHA256) {
		return fmt.Errorf("sidecar says %s, the tarball is %s", f[0], a.SHA256)
	}
	if name != "" && len(f) > 1 && f[1] != name {
		return fmt.Errorf("sidecar names %s, not %s", f[1], name)
	}
	return nil
}

// CheckSignature verifies a .sig file's contents against the archive's
// manifest and a public key the caller obtained independently.
func (a *Archive) CheckSignature(sigJSON []byte, pub ed25519.PublicKey) (*Signature, error) {
	var sig Signature
	if err := json.Unmarshal(sigJSON, &sig); err != nil {
		return nil, fmt.Errorf(".sig: %w", err)
	}
	return &sig, VerifySignature(&sig, a.ManifestSHA256, pub)
}
