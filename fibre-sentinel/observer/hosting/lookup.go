package hosting

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"net/netip"
	"os"
	"strconv"
	"strings"
)

// ASNRecord is what the iptoasn file says about one address: the origin AS
// of the range it falls in, that AS's description, and the country the AS is
// registered in (the registry's word about the organisation, not a
// geolocation of the address).
type ASNRecord struct {
	ASN       uint32
	Org       string
	ASCountry string
}

// openMaybeGzip opens path, transparently gunzipping it when the file starts
// with the gzip magic bytes, so an operator can keep the download as it came
// or unpack it; the name does not matter. The returned closer closes both
// layers.
func openMaybeGzip(path string) (io.Reader, func() error, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	br := bufio.NewReaderSize(f, 1<<16)
	magic, _ := br.Peek(2)
	if len(magic) == 2 && magic[0] == 0x1f && magic[1] == 0x8b {
		zr, err := gzip.NewReader(br)
		if err != nil {
			f.Close()
			return nil, nil, fmt.Errorf("%s: %w", path, err)
		}
		return zr, func() error { zr.Close(); return f.Close() }, nil
	}
	return br, f.Close, nil
}

// scanRanges streams a range file (an empty target set is answered without
// opening it) and calls match for every line whose
// [start, end] contains at least one target. split turns a line into its
// start, end and the rest of the fields; a line it cannot read is counted
// and skipped (a truncated download should not make the pass fail on its
// last line, but a file with no readable line at all is an error: that is a
// wrong file, not a damaged one).
//
// The cost is one pass over the file per call, O(lines × targets) address
// comparisons: about 600,000 lines against a hundred addresses, a second or
// two of CPU once per lookup pass, and no table held in memory afterwards.
func scanRanges(path string, targets []netip.Addr, split func(line []byte) (start, end netip.Addr, rest [][]byte, ok bool), match func(t netip.Addr, rest [][]byte)) (lines, bad int, err error) {
	if len(targets) == 0 {
		return 0, 0, nil
	}
	r, closeFn, err := openMaybeGzip(path)
	if err != nil {
		return 0, 0, err
	}
	defer closeFn()
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 || line[0] == '#' {
			continue
		}
		start, end, rest, ok := split(line)
		if !ok {
			bad++
			continue
		}
		lines++
		for _, t := range targets {
			// Family first: an IPv4 target never sits inside an IPv6 range,
			// and netip orders every IPv4 address before every IPv6 one, so
			// without this check a v4 target would "fall" between the bounds
			// of a range spanning families. No real file has one; this makes
			// that a non-event rather than a wrong answer.
			if t.Is4() != start.Is4() {
				continue
			}
			if start.Compare(t) <= 0 && t.Compare(end) <= 0 {
				match(t, rest)
			}
		}
	}
	if err := sc.Err(); err != nil {
		return lines, bad, fmt.Errorf("%s: %w", path, err)
	}
	if lines == 0 {
		return 0, bad, fmt.Errorf("%s: no readable range line (%d unreadable): not the expected file format", path, bad)
	}
	return lines, bad, nil
}

// LookupASN finds the origin AS of every target in an iptoasn.com TSV file
// (ip2asn-combined.tsv, ip2asn-v4.tsv or ip2asn-v6.tsv; gzipped or not):
//
//	range_start \t range_end \t AS_number \t country_code \t AS_description
//
// A target in a "Not routed" range (AS 0) or in no range at all is absent
// from the result. When ranges overlap (they do not in the published files)
// the first one wins.
func LookupASN(path string, targets []netip.Addr) (map[netip.Addr]ASNRecord, error) {
	out := map[netip.Addr]ASNRecord{}
	_, _, err := scanRanges(path, dedupe(targets), splitTSV, func(t netip.Addr, rest [][]byte) {
		if _, seen := out[t]; seen || len(rest) < 3 {
			return
		}
		n, err := strconv.ParseUint(string(rest[0]), 10, 32)
		if err != nil || n == 0 {
			return
		}
		cc := strings.ToUpper(strings.TrimSpace(string(rest[1])))
		if len(cc) != 2 {
			cc = "" // "None", "Unknown": the file's words for no country
		}
		out[t] = ASNRecord{ASN: uint32(n), Org: strings.TrimSpace(string(rest[2])), ASCountry: cc}
	})
	return out, err
}

// LookupCountry finds the country of every target in a DB-IP "IP to Country
// Lite" CSV file (gzipped or not):
//
//	start_ip,end_ip,country_code
//
// ZZ (DB-IP's "unknown / reserved") and anything that is not a two-letter
// code leave the target out of the result.
func LookupCountry(path string, targets []netip.Addr) (map[netip.Addr]string, error) {
	out := map[netip.Addr]string{}
	_, _, err := scanRanges(path, dedupe(targets), splitCSV, func(t netip.Addr, rest [][]byte) {
		if _, seen := out[t]; seen || len(rest) < 1 {
			return
		}
		cc := strings.ToUpper(strings.Trim(strings.TrimSpace(string(rest[0])), `"`))
		if len(cc) != 2 || cc == "ZZ" {
			return
		}
		out[t] = cc
	})
	return out, err
}

func splitTSV(line []byte) (netip.Addr, netip.Addr, [][]byte, bool) {
	return splitRange(bytes.Split(line, []byte{'\t'}))
}

func splitCSV(line []byte) (netip.Addr, netip.Addr, [][]byte, bool) {
	return splitRange(bytes.Split(line, []byte{','}))
}

func splitRange(f [][]byte) (netip.Addr, netip.Addr, [][]byte, bool) {
	if len(f) < 3 {
		return netip.Addr{}, netip.Addr{}, nil, false
	}
	a, err1 := netip.ParseAddr(string(bytes.Trim(bytes.TrimSpace(f[0]), `"`)))
	b, err2 := netip.ParseAddr(string(bytes.Trim(bytes.TrimSpace(f[1]), `"`)))
	if err1 != nil || err2 != nil || a.Is4() != b.Is4() {
		return netip.Addr{}, netip.Addr{}, nil, false
	}
	return a.Unmap(), b.Unmap(), f[2:], true
}

func dedupe(in []netip.Addr) []netip.Addr {
	seen := map[netip.Addr]bool{}
	out := make([]netip.Addr, 0, len(in))
	for _, a := range in {
		a = a.Unmap()
		if !a.IsValid() || seen[a] {
			continue
		}
		seen[a] = true
		out = append(out, a)
	}
	return out
}
