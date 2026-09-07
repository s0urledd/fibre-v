// Command sentinel-probe turns the scanner's publications into scheduled probes
// and raw measurements. It reads <data-dir>/publications.jsonl (written by
// sentinel-scan), derives a per-publication probe schedule from each record's
// must_serve_until, probes the assigned validators at those times, and appends
// one raw Measurement per probe to <data-dir>/measurements.jsonl.
//
// The pending-probe queue is never persisted: it is re-derived from the
// publications and the existing measurements every cycle, so a restart resumes
// exactly. Every wait is bounded — the loop never sleeps more than -max-sleep,
// and every probe layer (DNS/TCP/TLS/identity/download) has its own timeout.
package main

import (
	"context"
	"flag"
	"math"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/plsgiveup/fibre/fibre-sentinel/internal/probe"
	"github.com/plsgiveup/fibre/fibre-sentinel/internal/scan"
)

func main() {
	var (
		rpc      = flag.String("rpc", "http://127.0.0.1:26657", "CometBFT RPC endpoint")
		dataDir  = flag.String("data-dir", "./sentinel-data", "dir holding publications.jsonl; measurements.jsonl is written here")
		pubsPath = flag.String("publications", "", "path to publications.jsonl (default <data-dir>/publications.jsonl)")
		vantage  = flag.String("vantage", "local", "name of this vantage point (recorded on every measurement)")

		once     = flag.Bool("once", false, "probe everything currently due, then exit")
		drain    = flag.Bool("drain", false, "run until every known publication's schedule is in the past, then exit")
		deadline = flag.Duration("deadline", 0, "whole-run wall-clock cap (0 = none)")

		inWindow = flag.Int("in-window-probes", 4, "number of probes inside [settlement, must_serve_until]")
		graceOff = flag.Duration("grace-offset", 30*time.Second, "grace probe: must_serve_until + this")
		pruneTol = flag.Duration("prune-tolerance", 150*time.Second, "NOT_FOUND is normal until must_serve_until + this (devnet prune lag ~1m45s)")
		postMrg  = flag.Duration("post-margin", 60*time.Second, "post probe: must_serve_until + prune-tolerance + this")

		unassigned  = flag.Bool("probe-unassigned", false, "also probe validators not assigned the shard (their NOT_FOUND is the baseline)")
		maxSleep    = flag.Duration("max-sleep", 30*time.Second, "longest sleep between cycles")
		maxLateness = flag.Duration("max-lateness", 90*time.Second, "a schedule point older than this is recorded MISSED instead of probed")
		rpcTO       = flag.Duration("rpc-timeout", 15*time.Second, "per-RPC-call timeout")
		dnsTO       = flag.Duration("dns-timeout", 5*time.Second, "")
		tcpTO       = flag.Duration("tcp-timeout", 5*time.Second, "")
		tlsTO       = flag.Duration("tls-timeout", 10*time.Second, "")
		dlTO        = flag.Duration("download-timeout", 25*time.Second, "")
		logLines    = flag.Int("log-ring", 400, "log lines kept in memory for the crash dump")
	)
	flag.Parse()

	if *pubsPath == "" {
		*pubsPath = filepath.Join(*dataDir, "publications.jsonl")
	}

	log := scan.NewLogger(*logLines)

	// spread the in-window probes across (0,1), clustered toward the deadline
	// (x^0.7), where a retention breach is most likely to show.
	var fracs []float64
	for i := 0; i < *inWindow; i++ {
		if *inWindow == 1 {
			fracs = append(fracs, 0.6)
			break
		}
		x := float64(i+1) / float64(*inWindow+1)
		fracs = append(fracs, math.Pow(x, 0.7))
	}

	pr, err := probe.New(probe.Config{
		RPCURL:           *rpc,
		PublicationsPath: *pubsPath,
		DataDir:          *dataDir,
		Vantage:          *vantage,
		Schedule: probe.ScheduleConfig{
			InWindowFractions: fracs,
			GraceOffset:       *graceOff,
			PruneTolerance:    *pruneTol,
			PostMargin:        *postMrg,
		},
		Timeouts: probe.StepTimeouts{
			DNS: *dnsTO, TCP: *tcpTO, TLS: *tlsTO, Download: *dlTO,
		},
		IncludeUnassigned: *unassigned,
		Once:              *once,
		Drain:             *drain,
		Deadline:          *deadline,
		MaxSleep:          *maxSleep,
		MaxLateness:       *maxLateness,
		RPCTimeout:        *rpcTO,
	}, log)
	if err != nil {
		log.Fatalf("init: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := pr.Run(ctx); err != nil {
		log.Fatalf("run: %v", err)
	}
}
