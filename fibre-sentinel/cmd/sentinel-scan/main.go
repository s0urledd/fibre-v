// Command sentinel-scan is the Fibre Sentinel chain scanner: discovery and
// recording only.
//
// It walks the chain height by height, finds transactions whose sole message is
// MsgPayForFibre, decodes the full PaymentPromise, tracks the fibre module
// params from EventUpdateFibreParams, and writes one JSON record per publication
// to <data-dir>/publications.jsonl. It never probes a validator.
//
// Every RPC call is timeout-bounded. Follow mode gives up (non-zero exit, with a
// dump of the last log lines) if the chain stops producing blocks — it never
// hangs.
package main

import (
	"context"
	"flag"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/plsgiveup/fibre/fibre-sentinel/internal/scan"
)

func main() {
	var (
		rpc         = flag.String("rpc", "http://127.0.0.1:26657", "CometBFT RPC endpoint")
		dataDir     = flag.String("data-dir", "./sentinel-data", "directory for state.json + publications.jsonl")
		startHeight = flag.Int64("start-height", 0, "fresh-scan start height (0 = tip at startup); ignored on resume")
		maxHeight   = flag.Int64("max-height", 0, "stop after this height (0 = run to tip)")
		follow      = flag.Bool("follow", false, "keep scanning new blocks after reaching the tip")
		followTO    = flag.Duration("follow-timeout", 2*time.Minute, "in follow mode, fail if no new block within this")
		pollEvery   = flag.Duration("poll", 2*time.Second, "follow-mode tip poll interval")
		rpcTO       = flag.Duration("rpc-timeout", 15*time.Second, "per-RPC-call timeout")
		deadline    = flag.Duration("deadline", 0, "whole-run wall-clock cap (0 = none)")
		includeFail = flag.Bool("include-failed", false, "also record MsgPayForFibre txs that failed on chain")
		storeRows   = flag.Bool("rows", true, "include full per-validator row-index lists in each record")
		checkpoint  = flag.Int("checkpoint-every", 20, "fsync + persist cursor every N heights")
		logLines    = flag.Int("log-ring", 300, "log lines kept in memory for the crash dump")
	)
	flag.Parse()

	log := scan.NewLogger(*logLines)

	s, err := scan.New(scan.Config{
		RPCURL:          *rpc,
		DataDir:         *dataDir,
		StartHeight:     *startHeight,
		MaxHeight:       *maxHeight,
		Follow:          *follow,
		FollowTimeout:   *followTO,
		PollInterval:    *pollEvery,
		RPCTimeout:      *rpcTO,
		Deadline:        *deadline,
		IncludeFailed:   *includeFail,
		StoreRows:       *storeRows,
		CheckpointEvery: *checkpoint,
	}, log)
	if err != nil {
		log.Fatalf("init: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := s.Run(ctx); err != nil {
		log.Fatalf("run: %v", err)
	}
}
