// Command sentinel-pub publishes N fibre blobs against a running
// multi-node-fibre devnet, so the scanner has something to find. It is a test
// helper, not part of the Sentinel.
//
// Publish path: fund a fresh key from the devnet 'uploader' account, deposit
// escrow, run fibre.Client.Upload to collect validator signatures, then
// broadcast the settling MsgPayForFibre — the same sequence fibre.Put performs
// internally (Client.Upload alone does not settle).
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
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
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func main() {
	var (
		appd      = flag.String("appd", "celestia-appd", "celestia-appd binary")
		rpc       = flag.String("rpc", "http://127.0.0.1:26657", "node 0 RPC")
		grpcAddr  = flag.String("grpc", "127.0.0.1:9090", "node 0 app gRPC")
		home      = flag.String("app-home", "", "node 0 app home (for the 'uploader' key); default $FIBRE_DEVNET_HOME/app0")
		chainID   = flag.String("chain-id", "fibre-devnet", "chain id")
		count     = flag.Int("count", 3, "number of blobs to publish")
		blobBytes = flag.Int("blob", 96*1024, "blob payload size")
		gap       = flag.Duration("gap", 6*time.Second, "delay between publishes")
		nsHex     = flag.String("ns", "", "namespace id suffix bytes (hex, <=10); default random-ish per run")
	)
	flag.Parse()

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

	kr := keyring.NewInMemory(ecfg.Codec)
	rec, _, err := kr.NewMnemonic("pub", keyring.English, sdk.GetConfig().GetFullBIP44Path(), keyring.DefaultBIP39Passphrase, hd.Secp256k1)
	must(err, "new key")
	addr, err := rec.GetAddress()
	must(err, "key addr")
	fmt.Printf("PUB| uploader-derived key: %s\n", addr)

	// fund from the devnet 'uploader' account.
	sh(*appd, "tx", "bank", "send", "uploader", addr.String(), "9000000000utia",
		"--from", "uploader", "--keyring-backend", "test", "--home", *home,
		"--chain-id", *chainID, "--fees", "6000utia", "--node", tcp(*rpc), "--yes")
	time.Sleep(5 * time.Second)

	txc, err := user.SetupTxClient(ctx, kr, conn, ecfg, user.WithDefaultAccount("pub"))
	must(err, "tx client")

	dep := &fibretypes.MsgDepositToEscrow{Signer: addr.String(), Amount: sdk.NewInt64Coin("utia", 6_000_000_000)}
	resp, err := txc.SubmitTx(ctx, []sdk.Msg{dep}, user.SetGasLimitAndGasPrice(200_000, 0.02))
	must(err, "deposit escrow")
	if resp.Code != 0 {
		fatal("deposit escrow code=%d codespace=%s", resp.Code, resp.Codespace)
	}
	fmt.Printf("PUB| escrow funded (tx %s @ h%d)\n", resp.TxHash, resp.Height)

	fcfg := celfibre.DefaultClientConfig()
	fcfg.DefaultKeyName = "pub"
	fcfg.StateAddress = *grpcAddr
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
		_, _ = rand.Read(data)
		blob, err := celfibre.NewBlob(data, celfibre.DefaultBlobConfigV0())
		must(err, "new blob")
		id := append(append([]byte(nil), nsSuffix...), byte(i))
		ns := share.MustNewV0Namespace(leftPad(id, 10))

		t0 := time.Now()
		// Upload collects validator signatures; then broadcast the settling
		// MsgPayForFibre ourselves (this is what fibre.Put does internally).
		sp, err := fc.Upload(ctx, ns, blob, celfibre.WithKeyName("pub"), celfibre.WithAwaitAllSignatures())
		must(err, fmt.Sprintf("upload %d", i))

		promiseProto, err := sp.ToProto()
		must(err, "promise to proto")
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

		fmt.Printf("PUB| #%d PUBLISHED promise_height=%d commitment=%s blob_v%d size=%d sigs=%d creation=%s settle_height=%d settle_tx=%s dur=%s\n",
			i, sp.Height, hex.EncodeToString(sp.Commitment[:]), sp.BlobVersion, sp.UploadSize, len(sp.ValidatorSignatures),
			sp.CreationTimestamp.UTC().Format(time.RFC3339Nano), tr.Height, br.TxHash, time.Since(t0).Round(time.Millisecond))

		if i < *count-1 {
			time.Sleep(*gap)
		}
	}
	fmt.Printf("PUB| done: %d blobs published\n", *count)
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
