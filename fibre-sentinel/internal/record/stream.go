package record

import (
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
)

// Stream is one open record file: its archived segments and the live file,
// read as the one logical file they were written as.
type Stream struct {
	path    string
	live    *os.File
	base    int64
	size    int64
	idx     *Index
	closers []io.Closer
}

// Open opens path for reading. The live file is opened first and its base
// found from its own first line, so a rotation between the two steps
// cannot pair one generation's bytes with another's base. A missing live
// file is os.ErrNotExist, as os.Open would say.
func Open(path string) (*Stream, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	s := &Stream{path: path, live: f, size: info.Size()}
	idx, err := LoadIndex(path)
	if err != nil {
		f.Close()
		return nil, err
	}
	s.idx = idx
	if len(idx.Generations) > 0 {
		head, err := headOf(f, 0)
		if err != nil {
			f.Close()
			return nil, err
		}
		s.base, _ = idx.base(head)
	}
	return s, nil
}

// Base is the logical offset of the live file's first byte.
func (s *Stream) Base() int64 { return s.base }

// End is the logical offset of the live file's end when it was opened.
func (s *Stream) End() int64 { return s.base + s.size }

// Index is the file's archive index (empty when it was never archived).
func (s *Stream) Index() *Index { return s.idx }

// ReaderFrom reads the logical file from off to the live file's end
// (including whatever is appended while it reads). Bytes before the live
// file's base come from the segments, which must cover [off, base) without
// a gap.
func (s *Stream) ReaderFrom(off int64) (io.Reader, error) {
	if off < 0 {
		return nil, fmt.Errorf("%s: negative offset %d", s.path, off)
	}
	if off >= s.base {
		if _, err := s.live.Seek(off-s.base, io.SeekStart); err != nil {
			return nil, err
		}
		return s.live, nil
	}
	segs := append([]Segment(nil), s.idx.Segments...)
	sort.Slice(segs, func(i, j int) bool { return segs[i].From < segs[j].From })
	var rs []io.Reader
	at := off
	for _, sg := range segs {
		if sg.To <= at || sg.From >= s.base {
			continue
		}
		if sg.From > at {
			return nil, fmt.Errorf("%s: archive has no segment for logical bytes [%d, %d)", s.path, at, sg.From)
		}
		r, err := s.openSegment(sg)
		if err != nil {
			return nil, err
		}
		if skip := at - sg.From; skip > 0 {
			if n, err := io.CopyN(io.Discard, r, skip); err != nil {
				return nil, fmt.Errorf("%s: segment %s ends after %d of the %d bytes to skip: %w", s.path, sg.Name, n, skip, err)
			}
		}
		rs = append(rs, io.LimitReader(r, sg.To-at))
		at = sg.To
	}
	if at != s.base {
		return nil, fmt.Errorf("%s: archive ends at logical byte %d, the live file starts at %d", s.path, at, s.base)
	}
	if _, err := s.live.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	rs = append(rs, s.live)
	return io.MultiReader(rs...), nil
}

func (s *Stream) openSegment(sg Segment) (io.Reader, error) {
	f, err := os.Open(filepath.Join(ArchiveDir(s.path), sg.Name))
	if err != nil {
		return nil, err
	}
	s.closers = append(s.closers, f)
	z, err := gzip.NewReader(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", sg.Name, err)
	}
	s.closers = append(s.closers, z)
	return z, nil
}

// Close closes the live file and every segment opened.
func (s *Stream) Close() error {
	for _, c := range s.closers {
		_ = c.Close()
	}
	s.closers = nil
	return s.live.Close()
}

// OpenAll is a reader over the whole record, segments then live file, for
// the tools that read every line (recompute, measure-check). A file never
// archived reads exactly as os.Open would.
func OpenAll(path string) (io.ReadCloser, error) {
	s, err := Open(path)
	if err != nil {
		return nil, err
	}
	r, err := s.ReaderFrom(0)
	if err != nil {
		s.Close()
		return nil, err
	}
	return struct {
		io.Reader
		io.Closer
	}{r, s}, nil
}
