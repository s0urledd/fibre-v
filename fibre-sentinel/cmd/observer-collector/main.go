// observer-collector feeds the observer store from the sentinel tools'
// append-only files and from the chain's Fibre endpoint registry.
//
// It does not scan the chain itself: sentinel-scan keeps writing
// publications.jsonl and state.json, sentinel-probe keeps writing
// measurements.jsonl, and this process tails those files into SQLite with
// byte-offset cursors. On top of that it polls AllBondedFibreProviders and
// keeps the endpoint history, and it records its own run span so the
// dashboard can show observer downtime as a gap.
package main

import (
	"context"
	"flag"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/plsgiveup/fibre/fibre-sentinel/internal/scan"
	"github.com/plsgiveup/fibre/fibre-sentinel/observer/ingest"
	"github.com/plsgiveup/fibre/fibre-sentinel/observer/store"
)

const version = "0.1.0"

func main() {
	var (
		rpc       = flag.String("rpc", "http://127.0.0.1:26657", "CometBFT RPC endpoint (endpoint registry polls)")
		dataDir   = flag.String("data-dir", "./sentinel-data", "dir holding publications.jsonl, state.json and measurements.jsonl")
		dbPath    = flag.String("db", "", "SQLite database path (default <data-dir>/observer.db)")
		vantage   = flag.String("vantage", "local", "vantage name recorded on this run")
		interval  = flag.Duration("interval", 10*time.Second, "how often to tail the files")
		epEvery   = flag.Duration("endpoints-every", 60*time.Second, "how often to poll AllBondedFibreProviders (0 = never)")
		rpcTO     = flag.Duration("rpc-timeout", 15*time.Second, "per-RPC-call timeout")
		once      = flag.Bool("once", false, "ingest everything currently on disk, poll endpoints once, then exit")
		logLines  = flag.Int("log-ring", 300, "log lines kept in memory for the crash dump")
		pubsPath  = flag.String("publications", "", "path to publications.jsonl (default <data-dir>/publications.jsonl)")
		statePath = flag.String("state", "", "path to state.json (default <data-dir>/state.json)")
		measPath  = flag.String("measurements", "", "path to measurements.jsonl (default <data-dir>/measurements.jsonl)")
	)
	flag.Parse()

	if *dbPath == "" {
		*dbPath = filepath.Join(*dataDir, "observer.db")
	}
	if *pubsPath == "" {
		*pubsPath = filepath.Join(*dataDir, "publications.jsonl")
	}
	if *statePath == "" {
		*statePath = filepath.Join(*dataDir, "state.json")
	}
	if *measPath == "" {
		*measPath = filepath.Join(*dataDir, "measurements.jsonl")
	}

	log := scan.NewLogger(*logLines)

	st, err := store.Open(*dbPath)
	if err != nil {
		log.Fatalf("open store: %v", err)
	}
	defer st.Close()

	var chain *scan.Chain
	if *epEvery > 0 {
		chain, err = scan.NewChain(*rpc, *rpcTO, log)
		if err != nil {
			log.Fatalf("rpc client: %v", err)
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	runID, err := st.StartRun("collector", *vantage, version, time.Now())
	if err != nil {
		log.Fatalf("start run: %v", err)
	}
	log.Printf("collector up: run=%d vantage=%s db=%s data=%s", runID, *vantage, *dbPath, *dataDir)

	pass := func(pollEndpoints bool) {
		now := time.Now()
		if err := ingest.State(st, *statePath, now); err != nil {
			log.Printf("state: %v", err)
		}
		if r, err := ingest.Publications(st, *pubsPath, now); err != nil {
			log.Printf("publications: %v", err)
		} else if r.Inserted > 0 {
			log.Printf("publications: +%d (read %d, line %d)", r.Inserted, r.Read, r.Line)
		}
		if r, err := ingest.Measurements(st, *measPath, now); err != nil {
			log.Printf("measurements: %v", err)
		} else if r.Inserted > 0 {
			log.Printf("measurements: +%d (read %d, line %d)", r.Inserted, r.Read, r.Line)
		}
		if pollEndpoints && chain != nil {
			_, height, err := chain.Status(ctx)
			if err != nil {
				log.Printf("endpoints: status: %v", err)
			} else if provs, err := chain.BondedFibreProviders(ctx); err != nil {
				// Before v10 the module does not exist; that is a normal
				// state, logged but not fatal.
				log.Printf("endpoints: %v", err)
			} else if opened, closed, err := st.ObserveEndpoints(ctx, provs, height, now); err != nil {
				log.Printf("endpoints: store: %v", err)
			} else {
				if opened > 0 || closed > 0 {
					log.Printf("endpoints: h=%d registered=%d opened=%d closed=%d", height, len(provs), opened, closed)
				}
				_ = st.SetMeta("endpoints_height", itoa(height), now)
				_ = st.SetMeta("endpoints_registered", itoa(int64(len(provs))), now)
			}
		}
		if err := st.Heartbeat(runID, time.Now()); err != nil {
			log.Printf("heartbeat: %v", err)
		}
	}

	pass(true)
	if *once {
		c, _ := st.Count(ctx)
		log.Printf("done (--once): publications=%d assignments=%d probes=%d open_endpoints=%d", c.Publications, c.Assignments, c.Probes, c.OpenEndpoints)
		_ = st.StopRun(runID, time.Now(), "once")
		return
	}

	tick := time.NewTicker(*interval)
	defer tick.Stop()
	lastEP := time.Now()
	for {
		select {
		case <-ctx.Done():
			log.Printf("stopped (signal)")
			_ = st.StopRun(runID, time.Now(), "signal")
			return
		case <-tick.C:
			poll := *epEvery > 0 && time.Since(lastEP) >= *epEvery
			pass(poll)
			if poll {
				lastEP = time.Now()
			}
		}
	}
}

func itoa(i int64) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var b [20]byte
	n := len(b)
	for i > 0 {
		n--
		b[n] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		n--
		b[n] = '-'
	}
	return string(b[n:])
}
