// observer-heartbeat runs the reachability probe: every interval it dials
// every registered Fibre endpoint (DNS, TCP, TLS 1.3, consensus-key identity)
// and stops there; no DownloadShard, about 3 KB per validator. It is what the
// network overview's "reachable now" and "TLS identity" columns are built
// from on days when a validator has no assignment to probe.
//
// Output: <data-dir>/reachability.jsonl, one probe.Measurement per line with
// an empty promise_hash and schedule_label "heartbeat". observer-collector
// ingests it into the reachability table.
package main

import (
	"bufio"
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"syscall"
	"time"

	"github.com/cosmos/cosmos-sdk/types/bech32"

	"github.com/plsgiveup/fibre/fibre-sentinel/internal/probe"
	"github.com/plsgiveup/fibre/fibre-sentinel/internal/scan"
)

func main() {
	var (
		rpc      = flag.String("rpc", "http://127.0.0.1:26657", "CometBFT RPC endpoint")
		dataDir  = flag.String("data-dir", "./sentinel-data", "reachability.jsonl is written here")
		vantage  = flag.String("vantage", "local", "vantage name recorded on every measurement")
		interval = flag.Duration("interval", 10*time.Minute, "how often every registered endpoint is dialled")
		once     = flag.Bool("once", false, "one round, then exit")
		rpcTO    = flag.Duration("rpc-timeout", 15*time.Second, "per-RPC-call timeout")
		dnsTO    = flag.Duration("dns-timeout", 5*time.Second, "")
		tcpTO    = flag.Duration("tcp-timeout", 5*time.Second, "")
		tlsTO    = flag.Duration("tls-timeout", 10*time.Second, "")
		logLines = flag.Int("log-ring", 300, "log lines kept in memory for the crash dump")
	)
	flag.Parse()

	log := scan.NewLogger(*logLines)
	chain, err := scan.NewChain(*rpc, *rpcTO, log)
	if err != nil {
		log.Fatalf("rpc client: %v", err)
	}
	if err := os.MkdirAll(*dataDir, 0o755); err != nil {
		log.Fatalf("mkdir: %v", err)
	}
	out, err := os.OpenFile(filepath.Join(*dataDir, "reachability.jsonl"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		log.Fatalf("open output: %v", err)
	}
	defer out.Close()
	w := bufio.NewWriter(out)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	timeouts := probe.StepTimeouts{DNS: *dnsTO, TCP: *tcpTO, TLS: *tlsTO}

	round := func() {
		chainID, tip, err := chain.Status(ctx)
		if err != nil {
			log.Printf("status: %v", err)
			return
		}
		provs, err := chain.BondedFibreProviders(ctx)
		if err != nil {
			log.Printf("providers: %v (x/valaddr not available before v10?)", err)
			return
		}
		members, err := chain.ValidatorSet(ctx, tip)
		if err != nil {
			log.Printf("validator set h=%d: %v", tip, err)
			return
		}
		keyByHex := map[string]ed25519.PublicKey{}
		for _, m := range members {
			if len(m.PubKey) == ed25519.PublicKeySize {
				keyByHex[hex.EncodeToString(m.Address)] = ed25519.PublicKey(m.PubKey)
			}
		}
		sort.Slice(provs, func(i, j int) bool { return provs[i].ConsAddressBech32 < provs[j].ConsAddressBech32 })
		scheduled := time.Now().UTC().Truncate(time.Second)
		n, ok := 0, 0
		for _, pr := range provs {
			if ctx.Err() != nil {
				return
			}
			_, raw, err := bech32.DecodeAndConvert(pr.ConsAddressBech32)
			if err != nil || len(raw) != 20 {
				log.Printf("bad consensus address %q: %v", pr.ConsAddressBech32, err)
				continue
			}
			addrHex := hex.EncodeToString(raw)
			var a [20]byte
			copy(a[:], raw)
			in := probe.Input{
				Vantage: *vantage, ChainID: chainID,
				Target:        probe.Target{Address: a, AddressHex: addrHex, PubKey: keyByHex[addrHex], Host: pr.Host},
				SchedulePoint: probe.SchedulePoint{At: scheduled, Label: "heartbeat", Phase: probe.PhasePost},
				SkipDownload:  true,
			}
			m := probe.Run(ctx, in, nil, timeouts)
			m.ValidatorSetHeight = tip
			b, err := json.Marshal(m)
			if err != nil {
				log.Fatalf("marshal: %v", err)
			}
			w.Write(b)
			w.WriteByte('\n')
			n++
			if m.Outcome == probe.OutcomeReachable {
				ok++
			}
			log.Printf("%s %s -> %s (%d ms)", pr.ConsAddressBech32[:20], pr.Host, m.Outcome, m.TotalDurationMS)
		}
		if err := w.Flush(); err != nil {
			log.Fatalf("flush: %v", err)
		}
		if err := out.Sync(); err != nil {
			log.Fatalf("sync: %v", err)
		}
		log.Printf("round done: h=%d registered=%d probed=%d reachable=%d", tip, len(provs), n, ok)
	}

	round()
	if *once {
		return
	}
	tick := time.NewTicker(*interval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			log.Printf("stopped (signal)")
			return
		case <-tick.C:
			round()
		}
	}
}

var _ = fmt.Sprintf
