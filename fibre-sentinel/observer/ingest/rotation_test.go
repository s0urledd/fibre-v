package ingest_test

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/plsgiveup/fibre/fibre-sentinel/internal/record"
	"github.com/plsgiveup/fibre/fibre-sentinel/observer/ingest"
	"github.com/plsgiveup/fibre/fibre-sentinel/observer/store"
)

var day0 = time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)

// measLine is one measurement dated d days and n minutes after day0.
func measLine(d, n int) string {
	at := day0.Add(time.Duration(d)*24*time.Hour + time.Duration(n)*time.Minute).Format(time.RFC3339)
	return fmt.Sprintf(`{"vantage":"t","promise_hash":"p%d","validator_address":"v%d","scheduled_at":%q,"started_at":%q,"phase":"in_window","outcome":"SERVED_OK","classification":"HEALTHY"}`+"\n", d, n, at, at)
}

func writeDays(t *testing.T, path string, from, to int) {
	t.Helper()
	a, err := record.OpenAppender(path)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	for d := from; d < to; d++ {
		for n := 0; n < 3; n++ {
			if _, err := a.Write([]byte(measLine(d, n))); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func rotate(t *testing.T, path string, beforeDay int) record.Result {
	t.Helper()
	res, err := record.Archive(path, record.Options{Cutoff: day0.Add(time.Duration(beforeDay) * 24 * time.Hour), TimeField: "scheduled_at", Limit: -1})
	if err != nil {
		t.Fatal(err)
	}
	if res.Skipped != "" {
		t.Fatalf("nothing archived: %s", res.Skipped)
	}
	return res
}

func probeCount(t *testing.T, st *store.Store) int {
	t.Helper()
	var n int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM probes`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// A collector at the end of the file when it is rotated reads only what is
// appended after: the replaced live file is neither ingested again nor
// skipped, and its cursor keeps counting in the record's own offsets.
func TestIngestAcrossRotation(t *testing.T) {
	st := openStore(t)
	path := filepath.Join(t.TempDir(), "measurements.jsonl")
	writeDays(t, path, 0, 5)
	if r, err := ingest.Measurements(st, path, time.Now()); err != nil || r.Inserted != 15 {
		t.Fatalf("first pass: %+v %v", r, err)
	}
	off, line, _ := st.Cursor(path)
	rotate(t, path, 3)
	if r, err := ingest.Measurements(st, path, time.Now()); err != nil || r.Read != 0 {
		t.Fatalf("the rotated file was read again: %+v %v", r, err)
	}
	if o, l, _ := st.Cursor(path); o != off || l != line {
		t.Fatalf("cursor moved on a rotation: %d/%d -> %d/%d", off, line, o, l)
	}
	writeDays(t, path, 5, 6)
	r, err := ingest.Measurements(st, path, time.Now())
	if err != nil || r.Read != 3 || r.Inserted != 3 || r.Line != 18 {
		t.Fatalf("after rotation: %+v %v", r, err)
	}
	if n := probeCount(t, st); n != 18 {
		t.Fatalf("%d probes, want 18", n)
	}
}

// A collector behind the rotation (stopped over it, or its cursor behind
// for any reason) reads the archived lines it has not seen from the
// archive, then the live file: nothing is skipped.
func TestIngestBehindRotationReadsTheArchive(t *testing.T) {
	st := openStore(t)
	path := filepath.Join(t.TempDir(), "measurements.jsonl")
	writeDays(t, path, 0, 1)
	if r, err := ingest.Measurements(st, path, time.Now()); err != nil || r.Inserted != 3 {
		t.Fatalf("first pass: %+v %v", r, err)
	}
	writeDays(t, path, 1, 6)
	rotate(t, path, 2)
	rotate(t, path, 4)
	r, err := ingest.Measurements(st, path, time.Now())
	if err != nil || r.Read != 15 || r.Inserted != 15 || r.Skipped != 0 || r.Line != 18 {
		t.Fatalf("behind the rotation: %+v %v", r, err)
	}
	if n := probeCount(t, st); n != 18 {
		t.Fatalf("%d probes, want 18", n)
	}
}

// A store rebuilt from scratch out of the archive and the live file holds
// exactly what one rebuilt from the file as it was before any rotation
// holds, row for row, cursor for cursor.
func TestRebuildFromArchiveEqualsRebuildFromOriginal(t *testing.T) {
	orig := filepath.Join(t.TempDir(), "measurements.jsonl")
	rot := filepath.Join(t.TempDir(), "measurements.jsonl")
	writeDays(t, orig, 0, 4)
	writeDays(t, rot, 0, 4)
	rotate(t, rot, 2)
	writeDays(t, orig, 4, 8)
	writeDays(t, rot, 4, 8)
	rotate(t, rot, 6)
	now := time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC)

	dump := func(path string) ([][]any, [2]int64) {
		st := openStore(t)
		if _, err := ingest.Measurements(st, path, now); err != nil {
			t.Fatal(err)
		}
		rows, err := st.DB().Query(`SELECT * FROM probes ORDER BY vantage, promise_hash, validator_address, scheduled_at`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		cols, _ := rows.Columns()
		var out [][]any
		for rows.Next() {
			v := make([]any, len(cols))
			p := make([]any, len(cols))
			for i := range v {
				p[i] = &v[i]
			}
			if err := rows.Scan(p...); err != nil {
				t.Fatal(err)
			}
			out = append(out, v)
		}
		off, line, _ := st.Cursor(path)
		return out, [2]int64{off, line}
	}
	a, ca := dump(orig)
	b, cb := dump(rot)
	if len(a) != 24 || !reflect.DeepEqual(a, b) {
		t.Fatalf("rebuilt stores differ: %d rows vs %d", len(a), len(b))
	}
	if ca != cb {
		t.Fatalf("cursors differ: %v vs %v", ca, cb)
	}
	live, _ := os.ReadFile(rot)
	if int64(len(live)) >= cb[0] {
		t.Fatal("the rotated file was not shorter than the record; the test proves nothing")
	}
}
