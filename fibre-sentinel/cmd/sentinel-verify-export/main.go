// Command sentinel-verify-export checks a downloaded daily export offline:
//
//  1. the tarball's sha256 against its .sha256 sidecar (when present);
//  2. every member against manifest.json: digest, byte count, line count,
//     and nothing in the tarball the manifest does not name;
//  3. the .sig beside it against manifest.json's digest and a public key the
//     caller supplies (-pubkey), which must match the fingerprint the
//     signature names. The key the .sig carries is never trusted on its own.
//
// It never touches the network. Get the key once, from the observer's
// /v1/exports/pubkey or from its operator, keep it, and pin it with
// -pubkey (and, if you like, -fingerprint).
//
//	sentinel-verify-export -pubkey tensile-exports.pem tensile-<vantage>-2026-09-10.tar.gz
//
// Exit status: 0 every check passed; 1 a check failed; 2 bad usage or an
// unreadable file; 3 the export is intact but unsigned and -require-signature
// was given.
//
// -keygen PATH writes a fresh signing key for an observer operator (PKCS#8
// PEM at PATH, mode 0600, and the public key at PATH.pub) and prints its
// fingerprint. `openssl genpkey -algorithm ed25519 -out PATH` makes the same
// kind of key.
//
// The same checks can be made with sha256sum, tar, jq and OpenSSL 3 alone;
// docs/exports-signing.md spells them out.
package main

import (
	"crypto/ed25519"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/plsgiveup/fibre/fibre-sentinel/observer/export"
)

func main() {
	var (
		pubPath = flag.String("pubkey", "", "the observer's export public key: PKIX PEM, hex or base64 of the 32-byte key, or the JSON /v1/exports/pubkey answers with")
		pin     = flag.String("fingerprint", "", "optional: require the key to have this fingerprint (sha256:<hex>)")
		sigPath = flag.String("sig", "", "signature file (default <tarball>.sig)")
		reqSig  = flag.Bool("require-signature", false, "fail (exit 3) when the export has no signature")
		keygen  = flag.String("keygen", "", "write a new ed25519 signing key to this path (and its public key to PATH.pub), print the fingerprint, and exit")
	)
	flag.Parse()

	if *keygen != "" {
		fp, err := export.GenerateKey(*keygen)
		if err != nil {
			fatal(2, "keygen: %v", err)
		}
		fmt.Printf("wrote %s (private, keep it off the web) and %s.pub\nfingerprint %s\n", *keygen, *keygen, fp)
		return
	}
	if flag.NArg() == 0 {
		fatal(2, "usage: sentinel-verify-export [-pubkey KEY] [-require-signature] EXPORT.tar.gz ...")
	}
	var pub ed25519.PublicKey
	if *pubPath != "" {
		raw, err := os.ReadFile(*pubPath)
		if err != nil {
			fatal(2, "read %s: %v", *pubPath, err)
		}
		if pub, err = export.ParsePublicKey(raw); err != nil {
			fatal(2, "%s: %v", *pubPath, err)
		}
		if *pin != "" && export.Fingerprint(pub) != *pin {
			fatal(1, "FAIL key %s is %s, not the pinned %s", *pubPath, export.Fingerprint(pub), *pin)
		}
	}
	worst := 0
	for _, path := range flag.Args() {
		sp := *sigPath
		if sp == "" {
			sp = path + ".sig"
		}
		if code := verify(path, sp, pub, *reqSig); code > worst {
			worst = code
		}
	}
	os.Exit(worst)
}

// verify checks one export and returns its exit status.
func verify(path, sigPath string, pub ed25519.PublicKey, requireSig bool) int {
	name := filepath.Base(path)
	tarball, err := os.ReadFile(path)
	if err != nil {
		fmt.Printf("FAIL %s: %v\n", name, err)
		return 2
	}
	a, err := export.ReadArchive(tarball)
	if err != nil {
		fmt.Printf("FAIL %s: %v\n", name, err)
		return 1
	}
	fmt.Printf("export   %s  day=%s vantage=%s build=%s\n", name, a.Manifest.Day, a.Manifest.Vantage, a.Manifest.Build)
	fmt.Printf("tarball  sha256 %s\n", a.SHA256)
	fmt.Printf("manifest sha256 %s\n", a.ManifestSHA256)
	failed := false
	if side, err := os.ReadFile(path + ".sha256"); err == nil {
		if err := a.CheckSidecar(side, name); err != nil {
			fmt.Printf("FAIL sidecar: %v\n", err)
			failed = true
		} else {
			fmt.Printf("ok   sidecar matches the tarball\n")
		}
	} else if errors.Is(err, os.ErrNotExist) {
		fmt.Printf("--   no .sha256 sidecar beside the tarball; compare the tarball digest with the index entry by hand\n")
	}
	if problems := a.CheckMembers(); len(problems) > 0 {
		for _, p := range problems {
			fmt.Printf("FAIL member %s\n", p)
		}
		failed = true
	} else {
		fmt.Printf("ok   %d members match manifest.json\n", len(a.Members)-1)
	}
	sigJSON, err := os.ReadFile(sigPath)
	switch {
	case errors.Is(err, os.ErrNotExist):
		fmt.Printf("--   unsigned: no %s\n", filepath.Base(sigPath))
		if requireSig && !failed {
			return 3
		}
	case err != nil:
		fmt.Printf("FAIL %s: %v\n", sigPath, err)
		failed = true
	case pub == nil:
		fmt.Printf("FAIL signed, but no -pubkey given: a signature checked against the key it carries proves nothing\n")
		failed = true
	default:
		sig, err := a.CheckSignature(sigJSON, pub)
		if err != nil {
			fmt.Printf("FAIL signature: %v\n", err)
			failed = true
		} else {
			fmt.Printf("ok   signature by %s over %q\n", sig.KeyFingerprint, sig.Message)
		}
	}
	if failed {
		fmt.Printf("FAIL %s\n", name)
		return 1
	}
	fmt.Printf("PASS %s\n", name)
	return 0
}

func fatal(code int, f string, a ...any) {
	fmt.Fprintf(os.Stderr, f+"\n", a...)
	os.Exit(code)
}
