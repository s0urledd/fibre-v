package policy

import (
	"bufio"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

// SecretsFile is the reveal record's file name under the data directory.
const SecretsFile = "sampling-secrets.jsonl"

// DefaultRevealAfter is how long after a UTC day ends its sampling secret
// is published. The draw must stay unpredictable while a publication
// settled on that day can still be probed, which is its retention window
// plus the grace and post points; seven days clears any window this
// observer schedules. The collector ingests the file and /v1/sampling
// serves the secret beside the day's commitment.
const DefaultRevealAfter = 7 * 24 * time.Hour

// revealCatchUp bounds how far back a reveal pass looks for days it has not
// revealed yet, so a prober that was down for a while reveals what it
// missed rather than leaving those days committed forever.
const revealCatchUp = 14 * 24 * time.Hour

// Reveal is one revealed per-day secret, as appended to sampling-secrets.jsonl.
type Reveal struct {
	Day        string    `json:"day"`
	Commitment string    `json:"commitment"`
	Secret     string    `json:"secret"`
	RevealedAt time.Time `json:"revealed_at"`
}

// RevealDue appends to path the secrets of every UTC day that ended at
// least `after` ago, within the catch-up bound, and is not in the file
// yet. It returns the days revealed this call. A day is revealed once: the
// file is the record, and it is read before anything is appended.
func (p *Policy) RevealDue(path string, now time.Time, after time.Duration) ([]string, error) {
	if after <= 0 {
		return nil, nil
	}
	done, err := revealedDays(path)
	if err != nil {
		return nil, err
	}
	// The newest day whose end is at least `after` behind now.
	edge := now.UTC().Add(-after).Add(-24 * time.Hour)
	first := edge.Add(-revealCatchUp)
	var out []string
	var f *os.File
	for d := dayOf(first); !d.After(dayOf(edge)); d = d.Add(24 * time.Hour) {
		day := d.Format("2006-01-02")
		if done[day] {
			continue
		}
		if f == nil {
			f, err = os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
			if err != nil {
				return out, err
			}
			defer f.Close()
		}
		r := Reveal{Day: day, Commitment: p.DayCommitment(d), Secret: hex.EncodeToString(p.DaySecret(d)), RevealedAt: now.UTC()}
		b, err := json.Marshal(r)
		if err != nil {
			return out, err
		}
		if _, err := f.Write(append(b, '\n')); err != nil {
			return out, fmt.Errorf("append %s: %w", path, err)
		}
		out = append(out, day)
	}
	if f != nil {
		_ = f.Sync()
	}
	return out, nil
}

func dayOf(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// revealedDays reads the days already in the file. A partial trailing line
// or a malformed one is ignored: a day it named would be revealed again,
// which is harmless (the collector keeps the first record).
func revealedDays(path string) (map[string]bool, error) {
	out := map[string]bool{}
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	r := bufio.NewReader(f)
	for {
		line, err := r.ReadBytes('\n')
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, err
		}
		var rv Reveal
		if json.Unmarshal(line, &rv) == nil && rv.Day != "" {
			out[rv.Day] = true
		}
	}
	return out, nil
}

// RevealLoop runs RevealDue now and then once an hour until ctx ends. It
// is what the prober starts beside its probe loop.
func (p *Policy) RevealLoop(ctx context.Context, path string, after time.Duration, logf func(string, ...any)) {
	if after <= 0 {
		return
	}
	run := func() {
		days, err := p.RevealDue(path, time.Now(), after)
		if err != nil {
			if logf != nil {
				logf("sampling reveal: %v", err)
			}
			return
		}
		if len(days) > 0 && logf != nil {
			logf("sampling reveal: published the day secret for %s", strings.Join(days, ", "))
		}
	}
	run()
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			run()
		}
	}
}
