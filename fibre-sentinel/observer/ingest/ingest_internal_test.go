package ingest

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/plsgiveup/fibre/fibre-sentinel/observer/store"
)

// A line the store refused must be read again on the next pass.
//
// tail advances its byte offset before it runs the handler, and flushes the
// cursor on the way out of a store error whenever any earlier line in the
// pass is still unflushed. That combination — one line stored, the next
// refused, the cursor write itself succeeding — persisted a position past a
// record that never reached SQL. The record stayed in the JSONL, the next
// pass began after it, and nothing short of re-ingesting the file from zero
// would ever have found it again. Idempotent inserts do not help: they stop
// duplicates, they do not resurrect a line nobody reads.
func TestCursorDoesNotAdvancePastALineTheStoreRefused(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "o.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	path := filepath.Join(t.TempDir(), "records.jsonl")
	if err := os.WriteFile(path, []byte("one\ntwo\nthree\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()

	// The second line fails once, the way a store error arrives: not an
	// ErrBadRecord, so the pass stops rather than stepping over it.
	boom := errors.New("insert: disk I/O error")
	var seen []string
	failing := true
	fn := func(raw []byte) (bool, error) {
		seen = append(seen, string(raw))
		if failing && string(raw) == "two" {
			return false, boom
		}
		return true, nil
	}

	res, err := tail(st, path, fn, now)
	if !errors.Is(err, boom) {
		t.Fatalf("first pass error = %v, want the store error", err)
	}
	if got := []string{"one", "two"}; len(seen) != 2 || seen[0] != got[0] || seen[1] != got[1] {
		t.Fatalf("first pass read %v, want one then two", seen)
	}

	// The cursor must sit at the end of "one", not the end of "two".
	off, line, err := st.Cursor(path)
	if err != nil {
		t.Fatal(err)
	}
	if want := int64(len("one\n")); off != want || line != 1 {
		t.Errorf("cursor after the failure = offset %d line %d, want %d and 1 (the end of the line that was stored)", off, line, want)
	}
	if res.Offset != int64(len("one\n")) || res.Line != 1 {
		t.Errorf("result position = offset %d line %d, want it rewound to before the refused line", res.Offset, res.Line)
	}

	// Second pass, with the store healthy: the refused line comes back.
	failing = false
	seen = nil
	if _, err := tail(st, path, fn, now); err != nil {
		t.Fatalf("second pass: %v", err)
	}
	if len(seen) != 2 || seen[0] != "two" || seen[1] != "three" {
		t.Fatalf("second pass read %v, want the refused line and the one after it", seen)
	}
}

// A cursor write that fails on the error path is reported, not swallowed:
// the position on disk is then ahead of what was stored, which is the state
// the rewind exists to prevent.
func TestAFailedCursorWriteOnTheErrorPathIsReported(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "o.db"))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "records.jsonl")
	if err := os.WriteFile(path, []byte("one\ntwo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	boom := errors.New("insert refused")
	fn := func(raw []byte) (bool, error) {
		if string(raw) == "two" {
			// Close the store underneath, so the flush that follows cannot
			// write either.
			st.Close()
			return false, boom
		}
		return true, nil
	}
	_, err = tail(st, path, fn, time.Now().UTC())
	if err == nil {
		t.Fatal("no error from a pass whose insert and cursor write both failed")
	}
	if !errors.Is(err, boom) {
		t.Errorf("error = %v, want it to carry the store error", err)
	}
	if !contains(err.Error(), "cursor could not be written") {
		t.Errorf("error = %v, want it to name the failed cursor write too", err)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
