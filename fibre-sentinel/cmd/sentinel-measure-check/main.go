// Command sentinel-measure-check asserts the Sentinel error-class taxonomy held
// over a measurements.jsonl, including a fault-injection run where one fibre
// server was killed mid-window. Test/CI helper.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/plsgiveup/fibre/fibre-sentinel/internal/probe"
)

func main() {
	var (
		dataDir    = flag.String("data-dir", "./sentinel-data", "dir holding measurements.jsonl")
		killedHost = flag.String("killed-host", "", "host:port of the fibre server that was killed mid-window")
		killAtStr  = flag.String("kill-at", "", "RFC3339 time the kill happened (measurements from the killed validator started after this, in-window, must be FAULT)")
		minProbes  = flag.Int("min-probes", 6, "minimum total measurements expected")
	)
	flag.Parse()

	ms, err := probe.LoadMeasurements(filepath.Join(*dataDir, "measurements.jsonl"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "measure-check FATAL: %v\n", err)
		os.Exit(2)
	}
	fmt.Printf("measure-check| loaded %d measurements\n", len(ms))
	if len(ms) < *minProbes {
		fmt.Fprintf(os.Stderr, "measure-check FATAL: only %d measurements, want >= %d\n", len(ms), *minProbes)
		os.Exit(1)
	}

	var killAt time.Time
	if *killAtStr != "" {
		killAt, err = time.Parse(time.RFC3339, *killAtStr)
		if err != nil {
			fmt.Fprintf(os.Stderr, "measure-check FATAL: bad -kill-at: %v\n", err)
			os.Exit(2)
		}
	}

	// which validator address sits behind the killed host?
	killedAddr := ""
	for _, m := range ms {
		if *killedHost != "" && m.ValidatorHost == *killedHost {
			killedAddr = m.ValidatorAddress
			break
		}
	}
	if *killedHost != "" && killedAddr == "" {
		fmt.Printf("measure-check| WARN: no measurement references killed host %s\n", *killedHost)
	}

	fails := 0
	fail := func(format string, a ...any) {
		fmt.Printf("FAIL: "+format+"\n", a...)
		fails++
	}

	// counters for a readable summary
	byClass := map[probe.Classification]int{}
	byOutcome := map[probe.Outcome]int{}

	sawKilledFault := false
	sawHealthyInWindow := false
	sawExpectedGone := false

	for _, m := range ms {
		byClass[m.Classification]++
		byOutcome[m.Outcome]++

		assignedInWindow := m.Assigned && m.Phase == probe.PhaseInWindow
		isKilled := killedAddr != "" && m.ValidatorAddress == killedAddr

		switch {
		case isKilled && assignedInWindow && (killAt.IsZero() || m.StartedAt.After(killAt)):
			// killed validator, under obligation, after the kill: must be FAULT
			if m.Classification != probe.ClassFault {
				fail("killed %s in_window after kill: class=%s outcome=%s (want FAULT)", short(m.ValidatorAddress), m.Classification, m.Outcome)
			} else {
				sawKilledFault = true
			}

		case !isKilled && assignedInWindow:
			// a validator that was never killed, under obligation: must be HEALTHY
			if m.Classification != probe.ClassHealthy {
				fail("live assigned %s in_window: class=%s outcome=%s (want HEALTHY)", short(m.ValidatorAddress), m.Classification, m.Outcome)
			} else {
				sawHealthyInWindow = true
			}

		case m.Assigned && m.Phase == probe.PhaseGrace:
			// grace: NOT_FOUND / unreachable are tolerated, never FAULT.
			reachOrNF := m.Outcome == probe.OutcomeNotFound ||
				strings.Contains(string(m.Outcome), "UNAVAILABLE") ||
				strings.HasPrefix(string(m.Outcome), "TCP_")
			if m.Classification == probe.ClassFault && reachOrNF {
				fail("assigned %s grace: %s classed FAULT (want TOLERATED)", short(m.ValidatorAddress), m.Outcome)
			}

		case m.Assigned && m.Phase == probe.PhasePost:
			// post: must never be FAULT; NOT_FOUND -> EXPECTED_GONE
			if m.Classification == probe.ClassFault {
				fail("assigned %s post: class=FAULT outcome=%s (obligation is over)", short(m.ValidatorAddress), m.Outcome)
			}
			if m.Outcome == probe.OutcomeNotFound {
				if m.Classification != probe.ClassExpectedGone {
					fail("assigned %s post NOT_FOUND: class=%s (want EXPECTED_GONE)", short(m.ValidatorAddress), m.Classification)
				} else {
					sawExpectedGone = true
				}
			}

		case !m.Assigned:
			// unassigned NOT_FOUND is always expected
			if m.Outcome == probe.OutcomeNotFound && m.Classification != probe.ClassExpectedUnassigned {
				fail("unassigned %s NOT_FOUND: class=%s (want EXPECTED_UNASSIGNED)", short(m.ValidatorAddress), m.Classification)
			}
		}
	}

	fmt.Println("measure-check| by classification:")
	for c, n := range byClass {
		fmt.Printf("measure-check|   %-24s %d\n", c, n)
	}
	fmt.Println("measure-check| by outcome:")
	for o, n := range byOutcome {
		fmt.Printf("measure-check|   %-24s %d\n", o, n)
	}

	if killedAddr != "" && !sawKilledFault {
		fail("no in_window FAULT recorded for the killed validator %s — fault injection not detected", short(killedAddr))
	}
	if !sawHealthyInWindow {
		fail("no HEALTHY in_window measurement for any live assigned validator")
	}
	if !sawExpectedGone {
		fmt.Printf("measure-check| WARN: no post-window NOT_FOUND -> EXPECTED_GONE seen (window may not have fully elapsed)\n")
	}

	fmt.Printf("measure-check| %d checks failed\n", fails)
	if fails > 0 {
		os.Exit(1)
	}
	fmt.Println("measure-check| PASS: taxonomy held (fault injection detected, live validators healthy, post-window expected)")
}

func short(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}
