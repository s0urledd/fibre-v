package export

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/plsgiveup/fibre/fibre-sentinel/internal/record"
)

// The daily exports read the same bytes whether or not the files were
// archived in between: an export offset past the live file's base reads the
// live file at the right place, and one behind it reads the archive. The
// tarballs' members and manifests (source_from, source_to, digests) are the
// ones built from a data dir that was never archived.
func TestExportsAreUnchangedByArchiving(t *testing.T) {
	day := func(d int, h int) string {
		return time.Date(2026, 9, 10+d, h, 0, 0, 0, time.UTC).Format(time.RFC3339)
	}
	meas := func(d, h int, x string) string {
		return `{"scheduled_at":"` + day(d, h) + `","started_at":"` + day(d, h) + `","x":"` + x + `"}` + "\n"
	}
	var lines []string
	for d := 0; d < 6; d++ {
		for h := 0; h < 24; h += 6 {
			lines = append(lines, meas(d, h, string(rune('a'+d))))
		}
	}
	write := func(dir string, ls []string) {
		f, err := os.OpenFile(filepath.Join(dir, "measurements.jsonl"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			t.Fatal(err)
		}
		for _, l := range ls {
			f.WriteString(l)
		}
		f.Close()
	}
	plain, rotated := t.TempDir(), t.TempDir()
	for _, dir := range []string{plain, rotated} {
		write(dir, lines[:12]) // days 0-2
	}
	build := func(dir string, now time.Time) []string {
		b := &Builder{DataDir: dir, Dir: filepath.Join(dir, "exports"), Vantage: "v", Build: "x", Hour: 3}
		built, err := b.Run(now)
		if err != nil {
			t.Fatal(err)
		}
		return built
	}
	// day 0 and day 1 exported
	for _, dir := range []string{plain, rotated} {
		build(dir, time.Date(2026, 9, 12, 4, 0, 0, 0, time.UTC))
	}
	// archive past the export's offset (day 2 has not been exported): the
	// next export's source_from is behind the live file's base
	if _, err := record.Archive(filepath.Join(rotated, "measurements.jsonl"), record.Options{
		Cutoff: time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC), TimeField: "scheduled_at", Limit: -1}); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{plain, rotated} {
		write(dir, lines[12:])
		build(dir, time.Date(2026, 9, 14, 4, 0, 0, 0, time.UTC))
	}
	// and once more with the offset past the base
	if _, err := record.Archive(filepath.Join(rotated, "measurements.jsonl"), record.Options{
		Cutoff: time.Date(2026, 9, 14, 0, 0, 0, 0, time.UTC), TimeField: "scheduled_at", Limit: -1}); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{plain, rotated} {
		build(dir, time.Date(2026, 9, 16, 4, 0, 0, 0, time.UTC))
	}
	if s, err := record.Open(filepath.Join(rotated, "measurements.jsonl")); err != nil || s.Base() == 0 {
		t.Fatalf("the rotated dir was not archived: %v", err)
	} else {
		s.Close()
	}
	ents, _ := os.ReadDir(filepath.Join(plain, "exports"))
	n := 0
	for _, e := range ents {
		if filepath.Ext(e.Name()) != ".gz" {
			continue
		}
		n++
		a := readTar(t, filepath.Join(plain, "exports", e.Name()))
		b := readTar(t, filepath.Join(rotated, "exports", e.Name()))
		if !bytes.Equal(a["measurements.jsonl"], b["measurements.jsonl"]) {
			t.Fatalf("%s: measurements differ:\n%s\nvs\n%s", e.Name(), a["measurements.jsonl"], b["measurements.jsonl"])
		}
		var ma, mb Manifest
		json.Unmarshal(a["manifest.json"], &ma)
		json.Unmarshal(b["manifest.json"], &mb)
		for i := range ma.Files {
			if ma.Files[i] != mb.Files[i] {
				t.Fatalf("%s: manifest member %+v vs %+v", e.Name(), ma.Files[i], mb.Files[i])
			}
		}
	}
	if n != 5 {
		t.Fatalf("%d exports compared, want 5", n)
	}
}
