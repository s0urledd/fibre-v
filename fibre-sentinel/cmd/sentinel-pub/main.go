// Command sentinel-pub publishes N fibre blobs against a running
// multi-node-fibre devnet, so the scanner has something to find. It is a test
// helper, not part of the Sentinel.
//
// Publish path: fund a fresh key from the devnet 'uploader' account, deposit
// escrow, run fibre.Client.Upload to collect validator signatures, then
// broadcast the settling MsgPayForFibre — the same sequence fibre.Put performs
// internally (Client.Upload alone does not settle).
//
// Three more modes exercise the escrow side of x/fibre, which a publisher that
// always pays never produces:
//
//	-abandon           upload and collect signatures, then do NOT settle; the
//	                   signed promise is written to -promise-out so its timeout
//	                   can be submitted later. Validators hold the shards for
//	                   nothing until then.
//	-timeout FILE      broadcast MsgPaymentPromiseTimeout for a promise written
//	                   by -abandon. Anyone may submit it once creation +
//	                   payment_promise_timeout has passed; the escrow of the
//	                   promise's signer is charged, the submitter gets nothing.
//	-withdraw UTIA     broadcast MsgRequestWithdrawal; the payout lands in a
//	                   begin-block after withdrawal_delay.
//	-show FILE         print the promise hash and fields of a file written by
//	                   -abandon, without touching the chain. The hash is the
//	                   store key a validator serves the commitment under, so
//	                   it says which of two uploads of one commitment answers.
//
// -key-file keeps the run's key across invocations, so the account that
// abandoned a promise can be the one that withdraws, and a different one can
// be the one that reports the timeout.
package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/celestiaorg/celestia-app/v10/app"
	"github.com/celestiaorg/celestia-app/v10/app/encoding"
	celfibre "github.com/celestiaorg/celestia-app/v10/fibre"
	"github.com/celestiaorg/celestia-app/v10/pkg/user"
	fibretypes "github.com/celestiaorg/celestia-app/v10/x/fibre/types"
	"github.com/celestiaorg/go-square/v4/share"
	"github.com/cosmos/cosmos-sdk/crypto/hd"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"
	sdk "github.com/cosmos/cosmos-sdk/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/plsgiveup/fibre/fibre-sentinel/internal/uploadprobe"
)

// abandonedPromise is what -abandon writes and -timeout reads: the promise
// proto bytes, plus the fields a human wants to see before submitting.
type abandonedPromise struct {
	Publisher     string    `json:"publisher"`
	PromiseHash   string    `json:"promise_hash"`
	Commitment    string    `json:"commitment"`
	Namespace     string    `json:"namespace"`
	BlobSize      uint32    `json:"blob_size"`
	CreationTime  time.Time `json:"creation_timestamp"`
	PromiseHeight int64     `json:"promise_height"`
	Signatures    int       `json:"validator_signatures"`
	PromiseProto  string    `json:"promise_proto_hex"`
}

func main() {
	var (
		appd      = flag.String("appd", "celestia-appd", "celestia-appd binary")
		rpc       = flag.String("rpc", "http://127.0.0.1:26657", "node 0 RPC")
		grpcAddr  = flag.String("grpc", "127.0.0.1:9090", "node 0 app gRPC")
		home      = flag.String("app-home", "", "node 0 app home (for the 'uploader' key); default $FIBRE_DEVNET_HOME/app0")
		chainID   = flag.String("chain-id", "fibre-devnet", "chain id")
		count     = flag.Int("count", 3, "number of blobs to publish")
		blobBytes = flag.Int("blob", 96*1024, "blob payload size")
		seed      = flag.Int64("seed", 0, "deterministic payloads: blob i is derived from seed+i, so two runs with the same seed, size and namespace publish the same blobs (two promises over one commitment, the shadowing case); 0 = random")
		gap       = flag.Duration("gap", 6*time.Second, "delay between publishes")
		nsHex     = flag.String("ns", "", "namespace id suffix bytes (hex, <=10); default random-ish per run")
		// Off by default, which is what the protocol does: the publisher stops
		// at two thirds of voting power and the rest of the set is left with no
		// signature on chain. This used to be hardcoded ON, so every devnet blob
		// came back with a full signature set — and UNATTESTED, which is about a
		// third of every real assignment, was never produced by a devnet run at
		// all. It also makes a publish fail outright when any validator is down,
		// since "all" cannot be reached, which is not how a real publisher
		// behaves and is not a failure the observer should have to model.
		awaitAll = flag.Bool("await-all", false, "wait for EVERY validator's signature rather than stopping at the protocol's safety threshold")

		keyFile    = flag.String("key-file", "", "persist the run's mnemonic here and reuse it on later runs (default: a fresh key every run)")
		deposit    = flag.Int64("deposit", 6_000_000_000, "utia deposited to escrow before publishing (0 = skip; a reused key is usually still funded)")
		abandon    = flag.Bool("abandon", false, "collect signatures but never settle; write each promise to -promise-out")
		promiseOut = flag.String("promise-out", "", "where -abandon writes the promise (default abandoned-<commitment8>.json in the current directory)")
		timeoutF   = flag.String("timeout", "", "broadcast MsgPaymentPromiseTimeout for the promise file(s) written by -abandon (comma-separated)")
		withdraw   = flag.Int64("withdraw", 0, "broadcast MsgRequestWithdrawal for this many utia and exit")
		showF      = flag.String("show", "", "print the promise hash of the promise file(s) written by -abandon (comma-separated) and exit")
		// Upload-side measurement groundwork, off by default: when set, every validator's handling of every shard
		// this run uploads is recorded from the fibre client's own trace spans
		// (internal/uploadprobe) and written here as JSONL once the client has
		// finished its background deliveries.
		upResults = flag.String("upload-results", "", "write per-validator upload results (accepted, budget_exceeded, deadline, ...) as JSONL to this file; empty = off")
		upGrace   = flag.Duration("upload-results-grace", 30*time.Second, "with -upload-results: how long to let deliveries past quorum finish before stopping the client")
	)
	flag.Parse()

	if *showF != "" {
		for _, f := range strings.Split(*showF, ",") {
			showPromise(strings.TrimSpace(f))
		}
		return
	}

	if *home == "" {
		dn := os.Getenv("FIBRE_DEVNET_HOME")
		if dn == "" {
			fatal("set -app-home or FIBRE_DEVNET_HOME")
		}
		*home = dn + "/app0"
	}
	ctx := context.Background()
	ecfg := encoding.MakeConfig(app.ModuleEncodingRegisters...)

	conn, err := grpc.NewClient(*grpcAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	must(err, "dial app grpc")
	defer conn.Close()

	kr, addr, fresh := openKey(ecfg, *keyFile)
	fmt.Printf("PUB| key: %s (%s)\n", addr, map[bool]string{true: "new", false: "reused from " + *keyFile}[fresh])

	// Fund from the devnet 'uploader' account: always for a fresh key, and
	// for a reused one whose account the chain does not know or that has
	// run dry. A key file written by a run that died before funding used to
	// come back as "reused" and skip this, then fail at the first tx.
	if fresh || funded(ctx, conn, addr) < 1_000_000 {
		sh(*appd, "tx", "bank", "send", "uploader", addr.String(), "9000000000utia",
			"--from", "uploader", "--keyring-backend", "test", "--home", *home,
			"--chain-id", *chainID, "--fees", "6000utia", "--node", tcp(*rpc), "--yes")
		time.Sleep(5 * time.Second)
		fmt.Printf("PUB| funded %s from uploader\n", addr)
	}

	txc, err := user.SetupTxClient(ctx, kr, conn, ecfg, user.WithDefaultAccount("pub"))
	must(err, "tx client")

	switch {
	case *timeoutF != "":
		for _, f := range strings.Split(*timeoutF, ",") {
			submitTimeout(ctx, txc, addr, strings.TrimSpace(f))
		}
		return
	case *withdraw > 0:
		msg := &fibretypes.MsgRequestWithdrawal{Signer: addr.String(), Amount: sdk.NewInt64Coin("utia", *withdraw)}
		resp, err := txc.SubmitTx(ctx, []sdk.Msg{msg}, user.SetGasLimitAndGasPrice(200_000, 0.02))
		must(err, "request withdrawal")
		if resp.Code != 0 {
			fatal("request withdrawal code=%d codespace=%s", resp.Code, resp.Codespace)
		}
		fmt.Printf("PUB| WITHDRAWAL REQUESTED %d utia (tx %s @ h%d); paid out in a begin-block after withdrawal_delay\n", *withdraw, resp.TxHash, resp.Height)
		return
	}

	if *deposit > 0 {
		dep := &fibretypes.MsgDepositToEscrow{Signer: addr.String(), Amount: sdk.NewInt64Coin("utia", *deposit)}
		resp, err := txc.SubmitTx(ctx, []sdk.Msg{dep}, user.SetGasLimitAndGasPrice(200_000, 0.02))
		must(err, "deposit escrow")
		if resp.Code != 0 {
			fatal("deposit escrow code=%d codespace=%s", resp.Code, resp.Codespace)
		}
		fmt.Printf("PUB| escrow funded %d utia (tx %s @ h%d)\n", *deposit, resp.TxHash, resp.Height)
	}

	fcfg := celfibre.DefaultClientConfig()
	fcfg.DefaultKeyName = "pub"
	fcfg.StateAddress = *grpcAddr
	// With -upload-results the client traces into a recorder instead of the
	// global (no-op) tracer; nothing else about the upload changes.
	var upRec *uploadprobe.Recorder
	var upTracer trace.Tracer
	if *upResults != "" {
		upRec = uploadprobe.NewRecorder("sentinel-pub")
		tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(upRec))
		defer tp.Shutdown(context.Background())
		upTracer = tp.Tracer("fibre-client")
		fcfg.Tracer = upTracer
	}
	fc, err := celfibre.NewClient(kr, fcfg)
	must(err, "fibre client")
	must(fc.Start(ctx), "fibre client start")
	defer fc.Stop(context.Background())
	fmt.Printf("PUB| fibre client started; chain_id=%s\n", fc.ChainID())

	nsSuffix := []byte{0x53, 0x4e, 0x54} // "SNT"
	if *nsHex != "" {
		b := mustHex(*nsHex)
		nsSuffix = b
	}

	for i := 0; i < *count; i++ {
		data := make([]byte, *blobBytes)
		if *seed != 0 {
			fillDeterministic(data, *seed+int64(i))
		} else {
			_, _ = rand.Read(data)
		}
		blob, err := celfibre.NewBlob(data, celfibre.DefaultBlobConfigV0())
		must(err, "new blob")
		id := append(append([]byte(nil), nsSuffix...), byte(i))
		ns := share.MustNewV0Namespace(leftPad(id, 10))

		t0 := time.Now()
		// Upload collects validator signatures; then broadcast the settling
		// MsgPayForFibre ourselves (this is what fibre.Put does internally).
		opts := []celfibre.UploadOption{celfibre.WithKeyName("pub")}
		if *awaitAll {
			opts = append(opts, celfibre.WithAwaitAllSignatures())
		}
		upCtx := ctx
		var upSpan trace.Span
		if upTracer != nil {
			upCtx, upSpan = upTracer.Start(ctx, "sentinel-pub.upload")
		}
		sp, err := fc.Upload(upCtx, ns, blob, opts...)
		if upSpan != nil {
			// Every upload_to span of this upload shares the trace, including
			// the deliveries that end after Upload returns.
			labels := map[string]string{"blob_index": fmt.Sprint(i), "blob_size": fmt.Sprint(*blobBytes)}
			if err == nil {
				labels["commitment"] = hex.EncodeToString(sp.Commitment[:])
				if h, herr := sp.Hash(); herr == nil {
					labels["promise_hash"] = hex.EncodeToString(h)
				}
			}
			upRec.Label(upSpan.SpanContext().TraceID(), labels)
			upSpan.End()
		}
		must(err, fmt.Sprintf("upload %d", i))

		promiseProto, err := sp.ToProto()
		must(err, "promise to proto")

		// ValidatorSignatures is positional over the validator set: a slot the
		// publisher never filled is an empty entry, so len() is the set size,
		// not the signature count. Report both so a run at the two-thirds
		// threshold reads as what it is.
		signed := 0
		for _, sig := range sp.ValidatorSignatures {
			if len(sig) > 0 {
				signed++
			}
		}

		promiseHash, err := sp.Hash()
		must(err, "promise hash")

		if *abandon {
			raw, err := promiseProto.Marshal()
			must(err, "marshal promise")
			rec := abandonedPromise{
				Publisher: addr.String(), PromiseHash: hex.EncodeToString(promiseHash), Commitment: hex.EncodeToString(sp.Commitment[:]), Namespace: hex.EncodeToString(ns.Bytes()),
				BlobSize: sp.UploadSize, CreationTime: sp.CreationTimestamp.UTC(), PromiseHeight: int64(sp.Height),
				Signatures: signed, PromiseProto: hex.EncodeToString(raw),
			}
			out := *promiseOut
			if out == "" || *count > 1 {
				out = fmt.Sprintf("abandoned-%s.json", rec.Commitment[:8])
				if *promiseOut != "" {
					out = strings.TrimSuffix(*promiseOut, ".json") + "-" + rec.Commitment[:8] + ".json"
				}
			}
			b, _ := json.MarshalIndent(rec, "", "  ")
			must(os.WriteFile(out, append(b, '\n'), 0o644), "write promise")
			fmt.Printf("PUB| #%d ABANDONED promise_height=%d promise_hash=%s commitment=%s size=%d sigs=%d/%d creation=%s -> %s (timeout submittable after creation + payment_promise_timeout)\n",
				i, sp.Height, rec.PromiseHash, rec.Commitment, sp.UploadSize, signed, len(sp.ValidatorSignatures), rec.CreationTime.Format(time.RFC3339), out)
		} else {
			msg := &fibretypes.MsgPayForFibre{
				Signer:              addr.String(),
				PaymentPromise:      *promiseProto,
				ValidatorSignatures: sp.ValidatorSignatures,
			}
			br, err := txc.BroadcastTx(ctx, []sdk.Msg{msg}, user.SetGasLimitAndGasPrice(400_000, 0.02))
			must(err, fmt.Sprintf("broadcast PayForFibre %d", i))
			if br.Code != 0 {
				fatal("PayForFibre #%d failed: code=%d codespace=%s raw_log=%s", i, br.Code, br.Codespace, br.RawLog)
			}
			tr, err := txc.ConfirmTx(ctx, br.TxHash)
			must(err, fmt.Sprintf("confirm PayForFibre %d", i))
			fmt.Printf("PUB| #%d PUBLISHED promise_height=%d promise_hash=%s commitment=%s blob_v%d size=%d sigs=%d/%d creation=%s settle_height=%d settle_tx=%s fee=%dutia dur=%s\n",
				i, sp.Height, hex.EncodeToString(promiseHash), hex.EncodeToString(sp.Commitment[:]), sp.BlobVersion, sp.UploadSize, signed, len(sp.ValidatorSignatures),
				sp.CreationTimestamp.UTC().Format(time.RFC3339Nano), tr.Height, br.TxHash, fibretypes.EstimateGasForPayForFibre(sp.UploadSize), time.Since(t0).Round(time.Millisecond))
		}

		if i < *count-1 {
			time.Sleep(*gap)
		}
	}
	fmt.Printf("PUB| done: %d blobs %s\n", *count, map[bool]string{true: "abandoned", false: "published"}[*abandon])
	if upRec != nil {
		// Let the deliveries past quorum finish, then stop the client (which
		// waits for them) before writing: a delivery cut short by Stop reads
		// as "canceled", the client's own decision, never the validator's.
		time.Sleep(*upGrace)
		stopCtx, cancel := context.WithTimeout(context.Background(), time.Minute)
		_ = fc.Stop(stopCtx)
		cancel()
		f, err := os.Create(*upResults)
		must(err, "create "+*upResults)
		must(upRec.WriteJSONL(f), "write "+*upResults)
		must(f.Close(), "close "+*upResults)
		fmt.Printf("PUB| upload results: %d per-validator deliveries -> %s\n", len(upRec.Results()), *upResults)
	}
}

// funded is the account's spendable utia, or 0 when the chain has never
// seen it.
func funded(ctx context.Context, conn *grpc.ClientConn, addr sdk.AccAddress) int64 {
	resp, err := banktypes.NewQueryClient(conn).Balance(ctx, &banktypes.QueryBalanceRequest{Address: addr.String(), Denom: "utia"})
	if err != nil || resp.Balance == nil || !resp.Balance.Amount.IsInt64() {
		return 0
	}
	return resp.Balance.Amount.Int64()
}

// openKey returns an in-memory keyring holding "pub": a fresh key, or the one
// whose mnemonic is in keyFile, written there on first use.
func openKey(ecfg encoding.Config, keyFile string) (keyring.Keyring, sdk.AccAddress, bool) {
	kr := keyring.NewInMemory(ecfg.Codec)
	path := sdk.GetConfig().GetFullBIP44Path()
	if keyFile != "" {
		if b, err := os.ReadFile(keyFile); err == nil {
			rec, err := kr.NewAccount("pub", strings.TrimSpace(string(b)), keyring.DefaultBIP39Passphrase, path, hd.Secp256k1)
			must(err, "load key from "+keyFile)
			addr, err := rec.GetAddress()
			must(err, "key addr")
			return kr, addr, false
		} else if !os.IsNotExist(err) {
			fatal("read %s: %v", keyFile, err)
		}
	}
	rec, mnemonic, err := kr.NewMnemonic("pub", keyring.English, path, keyring.DefaultBIP39Passphrase, hd.Secp256k1)
	must(err, "new key")
	addr, err := rec.GetAddress()
	must(err, "key addr")
	if keyFile != "" {
		must(os.WriteFile(keyFile, []byte(mnemonic+"\n"), 0o600), "write "+keyFile)
	}
	return kr, addr, true
}

// submitTimeout charges the promise's signer for a promise that was never
// settled. The chain accepts it from anyone once the promise has expired.
func submitTimeout(ctx context.Context, txc *user.TxClient, signer sdk.AccAddress, file string) {
	b, err := os.ReadFile(file)
	must(err, "read "+file)
	var rec abandonedPromise
	must(json.Unmarshal(b, &rec), "parse "+file)
	raw, err := hex.DecodeString(rec.PromiseProto)
	must(err, "promise hex")
	var pp fibretypes.PaymentPromise
	must(pp.Unmarshal(raw), "promise proto")
	msg := &fibretypes.MsgPaymentPromiseTimeout{Signer: signer.String(), PaymentPromise: pp}
	resp, err := txc.SubmitTx(ctx, []sdk.Msg{msg}, user.SetGasLimitAndGasPrice(300_000, 0.02))
	if err != nil {
		fatal("timeout for %s: %v (the chain refuses it before creation + payment_promise_timeout; creation was %s)", rec.Commitment[:8], err, rec.CreationTime.Format(time.RFC3339))
	}
	if resp.Code != 0 {
		fatal("timeout for %s: code=%d codespace=%s", rec.Commitment[:8], resp.Code, resp.Codespace)
	}
	fmt.Printf("PUB| TIMED OUT commitment=%s publisher=%s charged=%dutia processor=%s tx=%s @ h%d\n",
		rec.Commitment, rec.Publisher, fibretypes.EstimateGasForPayForFibre(rec.BlobSize), signer, resp.TxHash, resp.Height)
}

// showPromise prints what -abandon wrote, with the promise hash recomputed
// from the proto bytes so a file from a build that did not record it still
// answers. Validators key the shard store by (commitment, promise hash) and
// serve a commitment from the lowest hash that has a readable shard, so the
// hash decides which of two uploads of one commitment a probe sees.
func showPromise(file string) {
	b, err := os.ReadFile(file)
	must(err, "read "+file)
	var rec abandonedPromise
	must(json.Unmarshal(b, &rec), "parse "+file)
	raw, err := hex.DecodeString(rec.PromiseProto)
	must(err, "promise hex")
	var pp fibretypes.PaymentPromise
	must(pp.Unmarshal(raw), "promise proto")
	var internal celfibre.PaymentPromise
	must(internal.FromProto(&pp), "promise from proto")
	h, err := internal.Hash()
	must(err, "promise hash")
	fmt.Printf("PUB| %s promise_hash=%s commitment=%s promise_height=%d creation=%s publisher=%s sigs=%d\n",
		file, hex.EncodeToString(h), rec.Commitment, rec.PromiseHeight, rec.CreationTime.Format(time.RFC3339), rec.Publisher, rec.Signatures)
}

func leftPad(b []byte, n int) []byte {
	if len(b) >= n {
		return b[len(b)-n:]
	}
	out := make([]byte, n)
	copy(out[n-len(b):], b)
	return out
}

func mustHex(s string) []byte {
	b, err := hex.DecodeString(strings.TrimPrefix(s, "0x"))
	must(err, "parse hex")
	return b
}

func sh(name string, args ...string) string {
	c := exec.Command(name, args...)
	c.Stderr = os.Stderr
	b, err := c.Output()
	if err != nil {
		fatal("cmd %s %s: %v", name, strings.Join(args, " "), err)
	}
	return string(b)
}

func tcp(u string) string {
	return "tcp://" + strings.TrimPrefix(strings.TrimPrefix(u, "http://"), "tcp://")
}

func must(err error, ctx string) {
	if err != nil {
		fatal("%s: %v", ctx, err)
	}
}

func fatal(f string, a ...any) {
	fmt.Fprintf(os.Stderr, "PUB-FATAL: "+f+"\n", a...)
	os.Exit(1)
}

// fillDeterministic fills data from SHA-256 of the seed in counter mode: the
// same seed and size give the same payload on any machine, which is what a
// second promise over the same commitment needs.
func fillDeterministic(data []byte, seed int64) {
	var counter [16]byte
	binary.BigEndian.PutUint64(counter[:8], uint64(seed))
	for off := 0; off < len(data); off += sha256.Size {
		binary.BigEndian.PutUint64(counter[8:], uint64(off))
		sum := sha256.Sum256(counter[:])
		copy(data[off:], sum[:])
	}
}
