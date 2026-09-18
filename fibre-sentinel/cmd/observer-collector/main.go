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
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/plsgiveup/fibre/fibre-sentinel/internal/scan"
	"github.com/plsgiveup/fibre/fibre-sentinel/internal/status"
	"github.com/plsgiveup/fibre/fibre-sentinel/observer/export"
	"github.com/plsgiveup/fibre/fibre-sentinel/observer/ingest"
	"github.com/plsgiveup/fibre/fibre-sentinel/observer/rollup"
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
		escEvery  = flag.Duration("escrow-every", 5*time.Minute, "how often to read every known publisher's escrow balance (one state query each; 0 = never)")
		rpcTO     = flag.Duration("rpc-timeout", 15*time.Second, "per-RPC-call timeout")
		once      = flag.Bool("once", false, "ingest everything currently on disk, poll endpoints once, then exit")
		logLines  = flag.Int("log-ring", 300, "log lines kept in memory for the crash dump")
		pubsPath  = flag.String("publications", "", "path to publications.jsonl (default <data-dir>/publications.jsonl)")
		statePath = flag.String("state", "", "path to state.json (default <data-dir>/state.json)")
		measPath  = flag.String("measurements", "", "path to measurements.jsonl (default <data-dir>/measurements.jsonl)")
		reachPath = flag.String("reachability", "", "path to reachability.jsonl (default <data-dir>/reachability.jsonl)")
		payPath   = flag.String("payments", "", "path to payments.jsonl (default <data-dir>/payments.jsonl)")
		regPath   = flag.String("registry", "", "path to registry.jsonl, this collector's own endpoint-history log (default <data-dir>/registry.jsonl)")
		runsPath  = flag.String("runs", "", "path to runs.jsonl, every component's record of its starts, stops and configuration (default <data-dir>/runs.jsonl)")
		secPath   = flag.String("sampling-secrets", "", "path to sampling-secrets.jsonl, the prober's revealed day secrets (default <data-dir>/sampling-secrets.jsonl)")
		amendPath = flag.String("amendments", "", "path to amendments.jsonl, this collector's own log of late shadow verdicts (default <data-dir>/amendments.jsonl)")
		pruneTol  = flag.Duration("prune-tolerance", 5*time.Minute, "how long past must_serve_until a promise's shard is still taken to be on disk when judging a deferred shadow verdict")
		expDir    = flag.String("exports-dir", "", "where the daily export tarballs are built (default <data-dir>/exports)")
		expHour   = flag.Int("export-hour", 3, "UTC hour after which a day's export is built, the grace for late rows (-1 = never build exports)")
		retainRaw = flag.Duration("retain-raw", rollup.Default().RetainRaw, "keep probe and heartbeat rows this long; older rolled days are pruned, whole days at a time (0 = keep forever)")
		retainRJ  = flag.Duration("retain-raw-json", rollup.Default().RetainRawJSON, "keep a row's raw_json (the bulk of it) this long; every typed column stays (0 = keep forever)")
		rollAfter = flag.Duration("rollup-after", rollup.Default().RollupAfter, "compute a day's obligation and probe rollups this long after the day ends; must clear every retention window (0 = never roll up, so never prune)")
		retEvery  = flag.Duration("retention-every", time.Hour, "how often the retention pass runs")
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
	if *reachPath == "" {
		*reachPath = filepath.Join(*dataDir, "reachability.jsonl")
	}
	if *payPath == "" {
		*payPath = filepath.Join(*dataDir, "payments.jsonl")
	}
	if *regPath == "" {
		*regPath = filepath.Join(*dataDir, "registry.jsonl")
	}
	if *runsPath == "" {
		*runsPath = filepath.Join(*dataDir, status.RunsFile)
	}
	if *secPath == "" {
		*secPath = filepath.Join(*dataDir, "sampling-secrets.jsonl")
	}
	if *expDir == "" {
		*expDir = filepath.Join(*dataDir, "exports")
	}
	if *amendPath == "" {
		*amendPath = filepath.Join(*dataDir, "amendments.jsonl")
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
	live := status.New(*dataDir, "collector", *vantage, version)
	live.Start()
	defer live.Stop("exit")

	// The endpoint history has no source but the live polls, so every
	// opening and closing is appended here as well as written to the
	// database; on a rebuild the file is replayed before the first poll.
	regFile, err := os.OpenFile(*regPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		log.Fatalf("open %s: %v", *regPath, err)
	}
	defer regFile.Close()
	appendRegistry := func(evs []store.EndpointEvent) {
		for _, e := range evs {
			b, err := json.Marshal(e)
			if err != nil {
				continue
			}
			if _, err := regFile.Write(append(b, '\n')); err != nil {
				log.Printf("registry: write: %v", err)
				live.Error(fmt.Sprintf("registry write: %v", err))
				return
			}
		}
		if len(evs) > 0 {
			_ = regFile.Sync()
		}
	}
	var exporter *export.Builder
	if *expHour >= 0 {
		exporter = &export.Builder{DataDir: *dataDir, Dir: *expDir, Vantage: *vantage, Build: status.BuildRevision(), Hour: *expHour, Logf: log.Printf}
	}
	// Late shadow verdicts are this collector's own judgement and, like the
	// endpoint history, have no source but this process: every one is
	// appended here as well as written to the database, and replayed on a
	// rebuild before anything is re-judged.
	amendFile, err := os.OpenFile(*amendPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		log.Fatalf("open %s: %v", *amendPath, err)
	}
	defer amendFile.Close()
	frontierMissing := false
	judgeLate := func(now time.Time) {
		v, err := st.Meta("last_scanned_time")
		if err != nil || v == "" {
			if !frontierMissing {
				log.Printf("late verdicts: the scanner's frontier time is not on record yet (state.json last_scanned_time); deferred shadow verdicts wait")
				frontierMissing = true
			}
			return
		}
		frontier, err := time.Parse(store.TimeLayout, v)
		if err != nil {
			log.Printf("late verdicts: bad last_scanned_time %q", v)
			return
		}
		ams, err := st.LateShadowVerdicts(ctx, frontier, now, *pruneTol)
		if err != nil {
			log.Printf("late verdicts: %v", err)
			live.Error(fmt.Sprintf("late verdicts: %v", err))
			return
		}
		applied := 0
		for _, a := range ams {
			ok, err := st.ApplyAmendment(a)
			if err != nil {
				log.Printf("late verdicts: apply %s: %v", a.DedupeKey, err)
				continue
			}
			if !ok {
				continue
			}
			applied++
			if b, err := json.Marshal(a); err == nil {
				if _, err := amendFile.Write(append(b, '\n')); err != nil {
					log.Printf("amendments: write: %v", err)
					live.Error(fmt.Sprintf("amendments write: %v", err))
				}
			}
			log.Printf("late verdict: %s %s %s: %s -> %s", a.PromiseHash[:min(12, len(a.PromiseHash))], a.ValidatorAddress, a.ScheduledAt.UTC().Format(time.RFC3339), a.From, a.To)
		}
		if applied > 0 {
			_ = amendFile.Sync()
			live.Set("late_verdicts", applied)
		}
	}
	log.Printf("collector up: run=%d vantage=%s db=%s data=%s exports=%s", runID, *vantage, *dbPath, *dataDir, *expDir)

	retention := rollup.Config{RetainRaw: *retainRaw, RetainRawJSON: *retainRJ, RollupAfter: *rollAfter}
	var lastRetention time.Time
	var lastEscrow time.Time
	pass := func(pollEndpoints bool) {
		now := time.Now()
		if err := ingest.State(st, *statePath, now); err != nil {
			log.Printf("state: %v", err)
		}
		if r, err := ingest.Publications(st, *pubsPath, now); err != nil {
			log.Printf("publications: %v", err)
		} else {
			if r.Inserted > 0 {
				log.Printf("publications: +%d (read %d, line %d)", r.Inserted, r.Read, r.Line)
			}
			if r.Skipped > 0 {
				log.Printf("publications: WARNING skipped %d undecodable line(s); last: %s", r.Skipped, r.LastSkipped)
			}
		}
		if r, err := ingest.Measurements(st, *measPath, now); err != nil {
			log.Printf("measurements: %v", err)
		} else {
			if r.Inserted > 0 {
				log.Printf("measurements: +%d (read %d, line %d)", r.Inserted, r.Read, r.Line)
			}
			if r.Skipped > 0 {
				log.Printf("measurements: WARNING skipped %d undecodable line(s); last: %s", r.Skipped, r.LastSkipped)
			}
		}
		if r, err := ingest.Reachability(st, *reachPath, now); err != nil {
			log.Printf("reachability: %v", err)
		} else {
			if r.Inserted > 0 {
				log.Printf("reachability: +%d (read %d, line %d)", r.Inserted, r.Read, r.Line)
			}
			if r.Skipped > 0 {
				log.Printf("reachability: WARNING skipped %d undecodable line(s); last: %s", r.Skipped, r.LastSkipped)
			}
		}
		if r, err := ingest.Registry(st, *regPath, now); err != nil {
			log.Printf("registry: %v", err)
		} else if r.Inserted > 0 {
			log.Printf("registry: +%d endpoint event(s) replayed (read %d, line %d)", r.Inserted, r.Read, r.Line)
		}
		if r, err := ingest.Payments(st, *payPath, now); err != nil {
			log.Printf("payments: %v", err)
		} else {
			if r.Inserted > 0 {
				log.Printf("payments: +%d (read %d, line %d)", r.Inserted, r.Read, r.Line)
			}
			if r.Skipped > 0 {
				log.Printf("payments: WARNING skipped %d undecodable line(s); last: %s", r.Skipped, r.LastSkipped)
			}
		}
		if r, err := ingest.Runs(st, *runsPath, now); err != nil {
			log.Printf("runs: %v", err)
		} else if r.Inserted > 0 {
			log.Printf("runs: +%d run event(s) replayed (read %d, line %d)", r.Inserted, r.Read, r.Line)
		}
		if r, err := ingest.SamplingSecrets(st, *secPath, now); err != nil {
			log.Printf("sampling secrets: %v", err)
		} else if r.Inserted > 0 {
			log.Printf("sampling secrets: +%d day(s) revealed (read %d, line %d)", r.Inserted, r.Read, r.Line)
		}
		if r, err := ingest.Amendments(st, *amendPath, now); err != nil {
			log.Printf("amendments: %v", err)
		} else if r.Inserted > 0 {
			log.Printf("amendments: +%d late verdict(s) replayed (read %d, line %d)", r.Inserted, r.Read, r.Line)
		}
		judgeLate(now)
		if *retEvery > 0 && time.Since(lastRetention) >= *retEvery {
			lastRetention = now
			if rep, err := rollup.Run(ctx, st, now, retention); err != nil {
				log.Printf("retention: %v", err)
				live.Error(fmt.Sprintf("retention: %v", err))
			} else {
				if len(rep.RolledDays) > 0 {
					log.Printf("retention: rolled up %d day(s) through %s (%d obligations still pending at roll)", len(rep.RolledDays), rep.RolledDays[len(rep.RolledDays)-1], rep.PendingAtRoll)
					live.Set("rollup_through", rep.RolledDays[len(rep.RolledDays)-1])
				}
				if rep.PendingAtRoll > 0 {
					log.Printf("retention: WARNING %d obligation(s) were still pending when their day was rolled; -rollup-after is shorter than a retention window", rep.PendingAtRoll)
				}
				if rep.RawJSONDropped > 0 {
					log.Printf("retention: dropped raw_json from %d row(s)", rep.RawJSONDropped)
				}
				if len(rep.PrunedDays) > 0 {
					log.Printf("retention: pruned %d row(s) of %d day(s) through %s", rep.PrunedRows, len(rep.PrunedDays), rep.PrunedDays[len(rep.PrunedDays)-1])
					live.Set("raw_from", rep.PrunedDays[len(rep.PrunedDays)-1])
				}
			}
		}
		if exporter != nil {
			if built, err := exporter.Run(now); err != nil {
				log.Printf("export: %v", err)
				live.Error(fmt.Sprintf("export: %v", err))
			} else if len(built) > 0 {
				live.Set("last_export", built[len(built)-1])
			}
		}
		if chain != nil && *escEvery > 0 && time.Since(lastEscrow) >= *escEvery {
			// Escrow balances, one state query per publisher the payments
			// table has seen. There is no list-all-escrow query, so an account
			// that deposited but never appeared in a payment we ingested is
			// not polled; it also has nothing to show. On its own, slower
			// clock: a balance moves when a payment lands, and the payments
			// themselves arrive through the file, not this poll.
			lastEscrow = now
			if pubs, err := st.Publishers(); err != nil {
				log.Printf("escrow: publishers: %v", err)
			} else {
				polled := 0
				for _, pub := range pubs {
					if ctx.Err() != nil {
						break
					}
					e, err := chain.EscrowAccount(ctx, pub, 0)
					if err != nil {
						log.Printf("escrow: %s: %v", pub, err)
						continue
					}
					if err := st.UpsertEscrowAccount(e, now); err != nil {
						log.Printf("escrow: store: %v", err)
						continue
					}
					polled++
				}
				if polled > 0 {
					_ = st.SetMeta("escrow_accounts", itoa(int64(polled)), now)
				}
			}
		}
		if pollEndpoints && chain != nil {
			// Whether Fibre exists on this chain at all, recorded rather than
			// inferred. x/fibre and x/valaddr are introduced in app version
			// 10, so below that every Fibre query fails for a reason that has
			// nothing to do with any validator — and a site that cannot tell
			// "the module is not there" from "the module is there and nobody
			// registered" will show the second while the first is true. Both
			// the version and the verdict are stored, so the page can say
			// which chain it is watching and what state that chain is in.
			if av, err := chain.AppVersion(ctx); err != nil {
				log.Printf("app version: %v", err)
			} else {
				_ = st.SetMeta("app_version", itoa(int64(av)), now)
				active := "no"
				if av >= scan.FibreAppVersion {
					active = "yes"
				}
				_ = st.SetMeta("fibre_active", active, now)
				_ = st.SetMeta("fibre_app_version", itoa(scan.FibreAppVersion), now)
			}
			chainID, height, err := chain.Status(ctx)
			if err != nil {
				log.Printf("endpoints: status: %v", err)
				live.Error(fmt.Sprintf("chain status: %v", err))
			} else {
				live.OK()
				live.Progress(height)
				// The chain's own identity and tip, recorded here rather than
				// only by the scanner: before Fibre activates there are no
				// publications to carry them, and "which chain is this, and how
				// far along is it" is the whole content of the site until then.
				// Kept separate from last_scanned_height, which is how far the
				// SCANNER has read; conflating the two would report the chain's
				// progress as our own.
				_ = st.SetMeta("chain_id", chainID, now)
				_ = st.SetMeta("chain_height", itoa(height), now)

				if provs, err := chain.BondedFibreProviders(ctx); err != nil {
					// Before v10 the module does not exist; that is a normal
					// state, logged but not fatal. fibre_active above says
					// which of the two this is.
					log.Printf("endpoints: %v", err)
				} else if evs, err := st.ObserveEndpointEvents(ctx, provs, height, now); err != nil {
					log.Printf("endpoints: store: %v", err)
				} else {
					if len(evs) > 0 {
						opened, closed := 0, 0
						for _, e := range evs {
							if e.Kind == store.EndpointOpened {
								opened++
							} else {
								closed++
							}
						}
						log.Printf("endpoints: h=%d registered=%d opened=%d closed=%d", height, len(provs), opened, closed)
						appendRegistry(evs)
					}
					_ = st.SetMeta("endpoints_height", itoa(height), now)
					_ = st.SetMeta("endpoints_registered", itoa(int64(len(provs))), now)
				}
			}
			// Validator names, from the chain's own staking module rather
			// than from an explorer's API. A reader recognises a validator by
			// the name its operator chose, not by twenty hex characters, and
			// taking that name from a third-party index would make this
			// observer depend on somebody else's coverage and terms.
			if ids, err := chain.ValidatorIdentities(ctx); err != nil {
				log.Printf("validator identities: %v", err)
			} else if n, err := st.UpsertValidatorIdentities(ids, now); err != nil {
				log.Printf("validator identities: store: %v", err)
			} else if n > 0 {
				log.Printf("validator identities: %d of %d stored", n, len(ids))
				_ = st.SetMeta("validator_identities", itoa(int64(n)), now)
			}
		}
		if err := st.Heartbeat(runID, time.Now()); err != nil {
			log.Printf("heartbeat: %v", err)
			live.Error(fmt.Sprintf("store heartbeat: %v", err))
		}
		if c, err := st.Count(ctx); err == nil {
			live.Set("publications", c.Publications)
			live.Set("probes", c.Probes)
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
			live.Stop("signal")
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
