package record

import (
	"bytes"
	"fmt"
	"os"
)

// Appender appends whole lines to a record file that Archive may rotate.
//
// Every write takes a shared flock on the file the Appender holds and,
// under it, checks that the path still names that file. Archive swaps the
// live file only while it holds the exclusive lock on the old one, after
// copying every byte the old one holds; so a write that got the shared lock
// first is in the old file before the copy is taken, and a write that got
// it after finds the path moved on, releases, reopens the path and writes
// to the new file. No line lands in a file after it was copied, and none is
// written twice.
//
// An Appender is not safe for concurrent use; its owners already serialise
// their writes.
type Appender struct {
	path string
	f    *os.File
}

// OpenAppender opens (creating) path for append.
func OpenAppender(path string) (*Appender, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	return &Appender{path: path, f: f}, nil
}

// Write appends b, which must be whole lines, in one write(2).
func (a *Appender) Write(b []byte) (int, error) {
	for try := 0; ; try++ {
		if err := lockShared(a.f); err != nil {
			return 0, fmt.Errorf("lock %s: %w", a.path, err)
		}
		cur, err := a.current()
		if err != nil {
			_ = unlock(a.f)
			return 0, err
		}
		if cur {
			n, err := a.f.Write(b)
			_ = unlock(a.f)
			return n, err
		}
		_ = unlock(a.f)
		if try >= 8 {
			return 0, fmt.Errorf("%s: the file keeps being replaced under the writer", a.path)
		}
		f, err := os.OpenFile(a.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return 0, err
		}
		// The old file was fsynced by the archiver along with the copy of
		// it, so nothing written to it is lost by closing it here.
		_ = a.f.Close()
		a.f = f
	}
}

// current reports whether the path still names the file held.
func (a *Appender) current() (bool, error) {
	held, err := a.f.Stat()
	if err != nil {
		return false, err
	}
	now, err := os.Stat(a.path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return os.SameFile(held, now), nil
}

// Sync fsyncs the file held. A line written to a file that was rotated
// before this Sync is in the new file, which the archiver fsynced before
// the swap.
func (a *Appender) Sync() error { return a.f.Sync() }

// Close closes the file held.
func (a *Appender) Close() error { return a.f.Close() }

// Path is the path appended to.
func (a *Appender) Path() string { return a.path }

// RepairTail cuts a trailing partial line (no final newline) off path, the
// tail a crash mid-write leaves, and reports how many bytes were removed.
// It holds the exclusive lock while it does, on the file the path names
// once the lock is held, so an archiver is never copying the bytes it cuts.
// Files that end in a newline, are empty, or do not exist are left alone.
func RepairTail(path string) (int64, error) {
	for try := 0; ; try++ {
		f, err := os.OpenFile(path, os.O_RDWR, 0)
		if os.IsNotExist(err) {
			return 0, nil
		}
		if err != nil {
			return 0, err
		}
		if err := lockExclusive(f); err != nil {
			f.Close()
			return 0, err
		}
		a := &Appender{path: path, f: f}
		if cur, err := a.current(); err != nil || !cur {
			_ = unlock(f)
			f.Close()
			if err != nil {
				return 0, err
			}
			if try >= 8 {
				return 0, fmt.Errorf("%s: the file keeps being replaced", path)
			}
			continue // rotated while we waited: repair the new one
		}
		n, err := cutTornTail(f)
		_ = unlock(f)
		f.Close()
		return n, err
	}
}

func cutTornTail(f *os.File) (int64, error) {
	info, err := f.Stat()
	if err != nil || info.Size() == 0 {
		return 0, err
	}
	size := info.Size()
	end, err := lastLineEnd(f, size)
	if err != nil || end == size {
		return 0, err
	}
	if err := f.Truncate(end); err != nil {
		return 0, err
	}
	return size - end, f.Sync()
}

// lastLineEnd is the offset just past the last newline in f's first size
// bytes: the end of its last complete line, 0 when it has none.
func lastLineEnd(f *os.File, size int64) (int64, error) {
	const chunk = 1 << 16
	b := make([]byte, chunk)
	for end := size; end > 0; {
		start := max(end-chunk, 0)
		n, err := f.ReadAt(b[:end-start], start)
		if err != nil && int64(n) < end-start {
			return 0, err
		}
		if i := bytes.LastIndexByte(b[:n], '\n'); i >= 0 {
			return start + int64(i) + 1, nil
		}
		end = start
	}
	return 0, nil
}
