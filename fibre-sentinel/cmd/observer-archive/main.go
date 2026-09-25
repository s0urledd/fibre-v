// Command observer-archive keeps the observer's biggest JSONL files bounded:
// once a day, after the daily export, the lines older than -keep move from
// each live file into a gzip segment under <data-dir>/archive/<file>/, and
// the live file keeps the rest. Nothing leaves the record. Every reader
// reads the segments and the live file as the one file they were written as
// (internal/record), at the same byte offsets.
//
// Archived: measurements.jsonl, sampling_decisions.jsonl (sentinel-probe)
// and reachability.jsonl (observer-heartbeat), whose writers follow a
// rotation through record.Appender. The other record files are small and
// their writers hold them open without that protocol; they are left alone.
//
// The run is idempotent (a second run the same day finds nothing older than
// the cutoff) and crash-safe (record.Archive): the source bytes stay in the
// live file until the segment holding them is fsynced and has read back to
// the same digest, and a run that stopped half way is undone by the next.
//
//	observer-archive -data-dir /var/lib/fibre-observer/mocha            archive
//	observer-archive -data-dir ... -dry-run                             say what would move
//	observer-archive -data-dir ... -verify                              check every segment
//	observer-archive -data-dir ... -status                              one line per file
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/plsgiveup/fibre/fibre-sentinel/internal/probe"
	"github.com/plsgiveup/fibre/fibre-sentinel/internal/record"
	"github.com/plsgiveup/fibre/fibre-sentinel/internal/scan"
)

// FileSpec is one archived file and the field that dates its lines.
type FileSpec struct {
	Name      string
	TimeField string
}

// Files are the files this command archives. The time field of the
// prober's two files is the one its restart horizon reasons about
// (probe.archivedFrom): a measurement's scheduled_at, a decision's
// decided_at.
var Files = []FileSpec{
	{"measurements.jsonl", "scheduled_at"},
	{probe.SampledOutFile, "decided_at"},
	{"reachability.jsonl", "scheduled_at"},
}

// DefaultKeep is how much of each file stays live.
const DefaultKeep = 7 * 24 * time.Hour

// minMargin is added to the longest retention window the chain has had to
// make the shortest -keep accepted: a publication's schedule runs from its
// settlement to must_serve_until plus a few minutes, and the prober plans
// it again after a restart only while every row it could have written is
// still live.
const minMargin = 24 * time.Hour

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, time.Now()))
}

func run(args []string, stdout, stderr io.Writer, now time.Time) int {
	fs := flag.NewFlagSet("observer-archive", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var (
		dataDir   = fs.String("data-dir", "./sentinel-data", "the observer's data directory")
		keep      = fs.Duration("keep", DefaultKeep, "keep lines dated within this long in the live files (the cutoff is the start of that UTC day); at least the longest retention window in state.json plus 24h")
		only      = fs.String("files", "", "comma-separated subset of "+names()+" (default all)")
		expDir    = fs.String("exports-dir", "", "the daily exports' dir (default <data-dir>/exports); nothing the export has not read yet is archived")
		noExpCap  = fs.Bool("ignore-exports", false, "archive whether or not the daily export has read the lines (it reads the archive either way)")
		dryRun    = fs.Bool("dry-run", false, "report what would move; write nothing")
		verify    = fs.Bool("verify", false, "check every segment against its index (digest, length, lines, no gap) and exit")
		statusOut = fs.Bool("status", false, "print each file's base, live size and segments and exit")
	)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	specs, err := selectFiles(*only)
	if err != nil {
		fmt.Fprintln(stderr, "observer-archive:", err)
		return 2
	}
	if *verify {
		return verifyAll(*dataDir, specs, stdout, stderr)
	}
	if *statusOut {
		return statusAll(*dataDir, specs, stdout, stderr)
	}
	minKeep, err := MinKeep(filepath.Join(*dataDir, "state.json"))
	if err != nil {
		fmt.Fprintln(stderr, "observer-archive:", err)
		return 2
	}
	if *keep < minKeep {
		fmt.Fprintf(stderr, "observer-archive: -keep %s is shorter than %s (the longest retention window in state.json plus %s); a restarted prober could not see rows it still needs\n", *keep, minKeep, minMargin)
		return 2
	}
	cutoff := Cutoff(now, *keep)
	if *expDir == "" {
		*expDir = filepath.Join(*dataDir, "exports")
	}
	var exported map[string]int64
	if !*noExpCap {
		if exported, err = exportOffsets(*expDir); err != nil {
			fmt.Fprintln(stderr, "observer-archive:", err)
			return 2
		}
	}
	verb := "archived"
	if *dryRun {
		verb = "would archive"
	}
	failed := false
	for _, f := range specs {
		limit := int64(-1)
		if exported != nil {
			limit = exported[f.Name] // 0 when the export has not read the file: nothing moves
		}
		res, err := record.Archive(filepath.Join(*dataDir, f.Name), record.Options{
			Cutoff: cutoff, TimeField: f.TimeField, Limit: limit, DryRun: *dryRun, Now: now,
		})
		if err != nil {
			fmt.Fprintf(stderr, "%s: FAILED: %v\n", f.Name, err)
			failed = true
			continue
		}
		if res.Skipped != "" {
			fmt.Fprintf(stdout, "%s: %s; live %s\n", f.Name, res.Skipped, mb(res.Live))
			continue
		}
		gz := ""
		if res.GzBytes > 0 {
			gz = fmt.Sprintf(" into %s (%s gzip)", res.Segment, mb(res.GzBytes))
		}
		fmt.Fprintf(stdout, "%s: %s %d line(s), %s, dated before %s%s; live %s -> %s, base %d -> %d\n",
			f.Name, verb, res.Lines, mb(res.Cut), cutoff.Format("2006-01-02"), gz, mb(res.Live), mb(res.LiveKept), res.Base, res.Base+res.Cut)
	}
	if failed {
		return 1
	}
	return 0
}

// Cutoff is the start of the UTC day keep before now: every line dated on
// or after it stays live.
func Cutoff(now time.Time, keep time.Duration) time.Time {
	t := now.UTC().Add(-keep)
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// MinKeep is the shortest -keep accepted: the longest window any params
// value in state.json has given a publication (the later of the payment
// promise timeout and the shard retention), plus minMargin. A data dir
// without state.json has no publications yet, so the margin alone.
func MinKeep(statePath string) (time.Duration, error) {
	raw, err := os.ReadFile(statePath)
	if errors.Is(err, os.ErrNotExist) {
		return minMargin, nil
	}
	if err != nil {
		return 0, err
	}
	var st scan.PersistState
	if err := json.Unmarshal(raw, &st); err != nil {
		return 0, fmt.Errorf("%s: %w", statePath, err)
	}
	var window time.Duration
	for _, e := range st.ParamHistory {
		for _, s := range []int64{e.ParamsJSON.PaymentPromiseTimeoutSeconds, e.ParamsJSON.ShardRetentionSeconds} {
			if d := time.Duration(s) * time.Second; d > window {
				window = d
			}
		}
	}
	return window + minMargin, nil
}

// exportOffsets reads how far the daily export has read each file
// (exports/state.json); nil when exports were never built, which caps
// nothing.
func exportOffsets(dir string) (map[string]int64, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "state.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var st struct {
		Offsets map[string]int64 `json:"offsets"`
	}
	if err := json.Unmarshal(raw, &st); err != nil {
		return nil, fmt.Errorf("exports state: %w", err)
	}
	if st.Offsets == nil {
		st.Offsets = map[string]int64{}
	}
	return st.Offsets, nil
}

func selectFiles(only string) ([]FileSpec, error) {
	if strings.TrimSpace(only) == "" {
		return Files, nil
	}
	var out []FileSpec
	for _, n := range strings.Split(only, ",") {
		n = strings.TrimSpace(n)
		found := false
		for _, f := range Files {
			if f.Name == n {
				out = append(out, f)
				found = true
			}
		}
		if !found {
			return nil, fmt.Errorf("%q is not an archived file (%s)", n, names())
		}
	}
	return out, nil
}

func names() string {
	var n []string
	for _, f := range Files {
		n = append(n, f.Name)
	}
	return strings.Join(n, ",")
}

func verifyAll(dataDir string, specs []FileSpec, stdout, stderr io.Writer) int {
	failed := false
	for _, f := range specs {
		n, err := record.Verify(filepath.Join(dataDir, f.Name))
		switch {
		case errors.Is(err, os.ErrNotExist):
			fmt.Fprintf(stdout, "%s: no such file\n", f.Name)
		case err != nil:
			fmt.Fprintf(stderr, "%s: FAILED: %v\n", f.Name, err)
			failed = true
		default:
			fmt.Fprintf(stdout, "%s: %d segment(s) verified\n", f.Name, n)
		}
	}
	if failed {
		return 1
	}
	return 0
}

func statusAll(dataDir string, specs []FileSpec, stdout, stderr io.Writer) int {
	for _, f := range specs {
		s, err := record.Open(filepath.Join(dataDir, f.Name))
		if errors.Is(err, os.ErrNotExist) {
			fmt.Fprintf(stdout, "%s: no such file\n", f.Name)
			continue
		}
		if err != nil {
			fmt.Fprintf(stderr, "%s: %v\n", f.Name, err)
			return 1
		}
		idx := s.Index()
		var gz, lines int64
		for _, sg := range idx.Segments {
			if sg.To <= s.Base() {
				gz += sg.GzBytes
				lines += sg.Lines
			}
		}
		since := "never archived"
		if !idx.LiveSince.IsZero() {
			since = "live since " + idx.LiveSince.Format("2006-01-02")
		}
		fmt.Fprintf(stdout, "%s: base %d, live %s, end %d; %d segment(s), %d line(s), %s gzip; %s\n",
			f.Name, s.Base(), mb(s.End()-s.Base()), s.End(), len(idx.Segments), lines, mb(gz), since)
		s.Close()
	}
	return 0
}

func mb(n int64) string { return fmt.Sprintf("%.1f MB", float64(n)/1e6) }
