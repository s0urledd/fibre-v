package export

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func line(field, ts, extra string) string {
	return `{"` + field + `":"` + ts + `","x":"` + extra + `"}` + "\n"
}

func readTar(t *testing.T, path string) map[string][]byte {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	out := map[string][]byte{}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(tr)
		out[h.Name] = b
	}
	return out
}

// Every line of every source file lands in exactly one export, the one
// of its own day or, when it arrived after that export was built, the
// next one, marked late; the manifest's digests match the members.
func TestBuilder_EveryLineInExactlyOneExport(t *testing.T) {
	data := t.TempDir()
	dir := filepath.Join(data, "exports")
	meas := filepath.Join(data, "measurements.jsonl")
	d1, d2, d3 := "2026-09-10", "2026-09-11", "2026-09-12"
	first := line("started_at", d1+"T10:00:00Z", "a") + line("started_at", d1+"T23:59:59Z", "b") +
		line("started_at", d2+"T00:00:01Z", "c") + line("started_at", d2+"T05:00:00Z", "d")
	if err := os.WriteFile(meas, []byte(first), 0o644); err != nil {
		t.Fatal(err)
	}
	// publications: one on d1, and a torn tail that must wait
	pubs := filepath.Join(data, "publications.jsonl")
	if err := os.WriteFile(pubs, []byte(line("settlement_time", d1+"T12:00:00Z", "p1")+`{"settlement_time":"`+d2), 0o644); err != nil {
		t.Fatal(err)
	}
	b := &Builder{DataDir: data, Dir: dir, Vantage: "eu/west 1", Build: "abc", Hour: 3}

	// d2 at 02:00: d1's grace has passed, d2's has not
	built, err := b.Run(time.Date(2026, 9, 12, 2, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if len(built) != 1 || built[0] != "fibrescope-eu-west-1-2026-09-10.tar.gz" {
		t.Fatalf("built %v", built)
	}
	members := readTar(t, filepath.Join(dir, built[0]))
	var man Manifest
	if err := json.Unmarshal(members["manifest.json"], &man); err != nil {
		t.Fatal(err)
	}
	if man.Day != d1 || man.Vantage != "eu/west 1" || man.Build != "abc" {
		t.Errorf("manifest = %+v", man)
	}
	byName := map[string]Member{}
	for _, m := range man.Files {
		byName[m.Name] = m
	}
	if got := string(members["measurements.jsonl"]); got != line("started_at", d1+"T10:00:00Z", "a")+line("started_at", d1+"T23:59:59Z", "b") {
		t.Errorf("d1 measurements member:\n%s", got)
	}
	if m := byName["measurements.jsonl"]; m.Lines != 2 || m.LateLines != 0 || m.From != 0 || m.To != int64(len(members["measurements.jsonl"])) {
		t.Errorf("measurements member = %+v", m)
	}
	if m := byName["publications.jsonl"]; m.Lines != 1 || string(members["publications.jsonl"]) != line("settlement_time", d1+"T12:00:00Z", "p1") {
		t.Errorf("publications member = %+v (torn tail must wait)", m)
	}
	if m := byName["payments.jsonl"]; m.Lines != 0 || m.SHA256 != emptySHA {
		t.Errorf("missing file must be an empty member: %+v", m)
	}
	for _, m := range man.Files {
		sum := sha256.Sum256(members[m.Name])
		if hex.EncodeToString(sum[:]) != m.SHA256 {
			t.Errorf("%s: manifest digest does not match the member", m.Name)
		}
	}
	// the sidecar digest is the tarball's
	raw, _ := os.ReadFile(filepath.Join(dir, built[0]))
	side, _ := os.ReadFile(filepath.Join(dir, built[0]+".sha256"))
	sum := sha256.Sum256(raw)
	if !strings.HasPrefix(string(side), hex.EncodeToString(sum[:])+"  "+built[0]) {
		t.Errorf("sidecar = %q", side)
	}

	// a late d1 row arrives after d1's export; d2 rows continue; then the
	// publications tail completes
	late := line("started_at", d1+"T22:00:00Z", "late") + line("started_at", d2+"T23:00:00Z", "e") + line("started_at", d3+"T00:30:00Z", "f")
	f, _ := os.OpenFile(meas, os.O_APPEND|os.O_WRONLY, 0o644)
	f.WriteString(late)
	f.Close()
	f, _ = os.OpenFile(pubs, os.O_APPEND|os.O_WRONLY, 0o644)
	f.WriteString(`T09:00:00Z","x":"p2"}` + "\n")
	f.Close()

	// still d2 at 02:00: nothing new is due
	if built, err := b.Run(time.Date(2026, 9, 12, 2, 30, 0, 0, time.UTC)); err != nil || len(built) != 0 {
		t.Fatalf("built %v err %v before the grace hour", built, err)
	}
	built, err = b.Run(time.Date(2026, 9, 12, 3, 0, 0, 0, time.UTC))
	if err != nil || len(built) != 1 {
		t.Fatalf("built %v err %v", built, err)
	}
	members = readTar(t, filepath.Join(dir, built[0]))
	if err := json.Unmarshal(members["manifest.json"], &man); err != nil {
		t.Fatal(err)
	}
	for _, m := range man.Files {
		byName[m.Name] = m
	}
	want := line("started_at", d2+"T00:00:01Z", "c") + line("started_at", d2+"T05:00:00Z", "d") +
		line("started_at", d1+"T22:00:00Z", "late") + line("started_at", d2+"T23:00:00Z", "e")
	if got := string(members["measurements.jsonl"]); got != want {
		t.Errorf("d2 measurements member:\n%s\nwant:\n%s", got, want)
	}
	if m := byName["measurements.jsonl"]; m.Lines != 4 || m.LateLines != 1 {
		t.Errorf("d2 measurements member = %+v, want 4 lines with 1 late", m)
	}
	if got := string(members["publications.jsonl"]); got != `{"settlement_time":"`+d2+`T09:00:00Z","x":"p2"}`+"\n" {
		t.Errorf("d2 publications member: %q", got)
	}
	// the d3 row waits for d3's export, from the offset the state recorded
	idx, err := ReadIndex(dir)
	if err != nil || len(idx) != 2 || idx[0].Day != d2 || idx[1].Day != d1 {
		t.Fatalf("index = %+v err %v", idx, err)
	}
	built, err = b.Run(time.Date(2026, 9, 13, 3, 0, 0, 0, time.UTC))
	if err != nil || len(built) != 1 {
		t.Fatalf("d3: built %v err %v", built, err)
	}
	members = readTar(t, filepath.Join(dir, built[0]))
	if got := string(members["measurements.jsonl"]); got != line("started_at", d3+"T00:30:00Z", "f") {
		t.Errorf("d3 measurements member: %q", got)
	}
}

func TestNamePattern(t *testing.T) {
	for _, ok := range []string{"fibrescope-local-2026-09-10.tar.gz", "fibrescope-eu-west.1-2026-09-10.tar.gz.sha256"} {
		if !NamePattern.MatchString(ok) {
			t.Errorf("%q must match", ok)
		}
	}
	for _, bad := range []string{"../x", "fibrescope-a-2026-09-10.tar", "index.json", "state.json", "fibrescope-a/b-2026-09-10.tar.gz"} {
		if NamePattern.MatchString(bad) {
			t.Errorf("%q must not match", bad)
		}
	}
}
