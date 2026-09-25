// Command sentinel-anchor builds the transaction that anchors one day's
// export on Celestia, and prints it. It does NOT broadcast: this build has
// no broadcast path at all, and that is deliberate (see below).
//
// What an anchor is: the export's manifest digest (and its signature, when
// the export was signed) as a regular Celestia blob, a MsgPayForBlobs under a
// dedicated namespace (export.AnchorNamespaceID, "tensileexp", unless -ns
// says otherwise). A block including it is a public, timestamped record
// that the digest existed then, which a signature alone cannot give: the
// holder of a signing key can sign a replacement for a past day at any time,
// but cannot back-date a block. The payload shape is export.AnchorPayload.
//
// How it builds the transaction, and why it looks like sentinel-pub:
// sentinel-pub signs with celestia-app's pkg/user (a TxClient over gRPC)
// because it also broadcasts. This command uses the same library's offline
// half — user.NewSigner + Signer.CreatePayForBlobs, the exact code path
// TxClient.SubmitPayForBlob takes before it hands the bytes to the node — so
// the bytes printed here are the bytes a broadcast would send. Gas is
// blobtypes.DefaultEstimateGas for the message (the chain's own linear
// model) and the fee is gas x -gas-price, rounded up.
//
// The chain id, account number and sequence a signature commits to come
// from -chain-id, -account-number and -sequence, or, with -grpc, from two
// read-only queries against that node (its node info and the x/auth
// account). There is no default chain id: one baked in goes stale at the
// next hardspoon (mocha-4 became mocha-5), and a transaction signed for the
// wrong chain is refused by every node. Given both, -chain-id must match
// the node's. Nothing else is sent anywhere.
//
// Why dry-run only: anchoring spends real (testnet) funds from an account
// the operator controls, and every anchor is permanent. The code path that
// matters — the payload, the namespace, the signed PFB — is here and tested;
// sending it is one more call the owner can add once the account is funded
// and the decision is made, or they can broadcast the printed bytes
// themselves (docs/exports-signing.md, "Anchoring").
//
//	sentinel-anchor -exports-dir <data-dir>/exports -day 2026-09-10 \
//	  -keyring-backend test -keyring-dir /etc/fibre-observer/keyring-mocha -key-name tensile-ops \
//	  -grpc <node:9090>    # or: -chain-id <id> -account-number N -sequence S
//
// -keyring-dir has celestia-appd's --keyring-dir meaning: the test backend's
// keys live in <dir>/keyring-test.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/celestiaorg/celestia-app/v10/app"
	"github.com/celestiaorg/celestia-app/v10/app/encoding"
	"github.com/celestiaorg/celestia-app/v10/pkg/appconsts"
	"github.com/celestiaorg/celestia-app/v10/pkg/user"
	blobtypes "github.com/celestiaorg/celestia-app/v10/x/blob/types"
	"github.com/celestiaorg/go-square/v4/share"
	blobtx "github.com/celestiaorg/go-square/v4/tx"
	"github.com/cosmos/cosmos-sdk/client/grpc/cmtservice"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/plsgiveup/fibre/fibre-sentinel/observer/export"
)

func main() {
	var (
		expDir   = flag.String("exports-dir", "./sentinel-data/exports", "the collector's exports directory (index.json, the tarballs, their .sig)")
		day      = flag.String("day", "", "UTC day to anchor, YYYY-MM-DD (default: the newest export in the index)")
		krDir    = flag.String("keyring-dir", "", "keyring root, as celestia-appd --keyring-dir (the test backend reads <dir>/keyring-test)")
		krBack   = flag.String("keyring-backend", "test", "keyring backend: test | file | os")
		keyName  = flag.String("key-name", "tensile-ops", "key in the keyring that signs the anchor")
		chainID  = flag.String("chain-id", "", "chain id the transaction is signed for (required unless -grpc, which reads it from the node; given both, they must agree)")
		nsHex    = flag.String("ns", hex.EncodeToString([]byte(export.AnchorNamespaceID)), "version-0 namespace ID for anchors, hex, at most 10 bytes")
		accNum   = flag.Uint64("account-number", math.MaxUint64, "account number the signature commits to (required unless -grpc)")
		seq      = flag.Uint64("sequence", math.MaxUint64, "account sequence the signature commits to (required unless -grpc)")
		grpcAddr = flag.String("grpc", "", "optional: app gRPC address to read the chain id, account number and sequence from (read-only queries)")
		gasPrice = flag.Float64("gas-price", appconsts.DefaultMinGasPrice, "utia per gas")
	)
	flag.Parse()

	if *krDir == "" {
		fatal("-keyring-dir is required")
	}
	name, err := pickExport(*expDir, *day)
	if err != nil {
		fatal("%v", err)
	}
	tarball, err := os.ReadFile(filepath.Join(*expDir, name))
	if err != nil {
		fatal("%v", err)
	}
	a, err := export.ReadArchive(tarball)
	if err != nil {
		fatal("%s: %v", name, err)
	}
	if p := a.CheckMembers(); len(p) > 0 {
		fatal("%s does not match its own manifest, refusing to anchor it: %s", name, strings.Join(p, "; "))
	}
	var sig *export.Signature
	if raw, err := os.ReadFile(filepath.Join(*expDir, name+".sig")); err == nil {
		sig = &export.Signature{}
		if err := json.Unmarshal(raw, sig); err != nil {
			fatal("%s.sig: %v", name, err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		fatal("%v", err)
	}
	payload, err := export.NewAnchorPayload(name, a, sig)
	if err != nil {
		fatal("%v", err)
	}
	nsID, err := hex.DecodeString(strings.TrimPrefix(*nsHex, "0x"))
	if err != nil || len(nsID) == 0 || len(nsID) > share.NamespaceVersionZeroIDSize {
		fatal("-ns must be 1..%d bytes of hex", share.NamespaceVersionZeroIDSize)
	}

	ecfg := encoding.MakeConfig(app.ModuleEncodingRegisters...)
	kr, err := keyring.New("celestia-app", *krBack, *krDir, os.Stdin, ecfg.Codec)
	if err != nil {
		fatal("open keyring: %v", err)
	}
	if *grpcAddr != "" {
		rec, err := kr.Key(*keyName)
		if err != nil {
			fatal("key %s: %v", *keyName, err)
		}
		addr, err := rec.GetAddress()
		if err != nil {
			fatal("key %s: %v", *keyName, err)
		}
		conn, err := grpc.NewClient(*grpcAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			fatal("dial %s: %v", *grpcAddr, err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		info, ierr := cmtservice.NewServiceClient(conn).GetNodeInfo(ctx, &cmtservice.GetNodeInfoRequest{})
		n, s, err := user.QueryAccount(ctx, conn, ecfg.InterfaceRegistry, addr)
		cancel()
		conn.Close()
		if ierr != nil {
			fatal("node info from %s: %v", *grpcAddr, ierr)
		}
		if err != nil {
			fatal("account %s: %v (an account the chain has never seen has not been funded yet)", addr, err)
		}
		if *chainID, err = resolveChainID(*chainID, info.GetDefaultNodeInfo().GetNetwork()); err != nil {
			fatal("%v", err)
		}
		*accNum, *seq = n, s
	} else if *chainID, err = resolveChainID(*chainID, ""); err != nil {
		fatal("%v", err)
	}
	if *accNum == math.MaxUint64 || *seq == math.MaxUint64 {
		fatal("give -account-number and -sequence, or -grpc to read them")
	}
	out, err := buildAnchorTx(kr, anchorParams{
		KeyName: *keyName, ChainID: *chainID, AccountNumber: *accNum, Sequence: *seq,
		NamespaceID: nsID, Payload: payload, GasPrice: *gasPrice,
	})
	if err != nil {
		fatal("%v", err)
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(out)
	fmt.Fprintln(os.Stderr, "DRY RUN: nothing was broadcast. This build has no broadcast path; see docs/exports-signing.md.")
}

// resolveChainID is the chain id to sign for: the one -chain-id names, the
// one the node reports (node, empty without -grpc), and the two must agree
// when both are given. Neither is an error, never a default.
func resolveChainID(flagged, node string) (string, error) {
	switch {
	case node != "" && flagged != "" && node != flagged:
		return "", fmt.Errorf("-chain-id is %s but the node at -grpc is on %s; a transaction signed for one is refused by the other", flagged, node)
	case node != "":
		return node, nil
	case flagged == "":
		return "", errors.New("-chain-id is required without -grpc: the chain id is part of what the signature commits to, and there is no default to go stale")
	}
	return flagged, nil
}

// pickExport returns the export name for day, or the newest in the index.
func pickExport(dir, day string) (string, error) {
	idx, err := export.ReadIndex(dir)
	if err != nil {
		return "", err
	}
	if len(idx) == 0 {
		return "", fmt.Errorf("%s: no exports in the index", dir)
	}
	sort.Slice(idx, func(i, j int) bool { return idx[i].Day > idx[j].Day })
	if day == "" {
		return idx[0].Name, nil
	}
	for _, e := range idx {
		if e.Day == day {
			return e.Name, nil
		}
	}
	return "", fmt.Errorf("%s: no export for %s", dir, day)
}

type anchorParams struct {
	KeyName       string
	ChainID       string
	AccountNumber uint64
	Sequence      uint64
	NamespaceID   []byte
	Payload       export.AnchorPayload
	GasPrice      float64
}

// anchorTx is what the command prints: enough to check the transaction by
// eye, and the signed bytes themselves.
type anchorTx struct {
	DryRun        bool                 `json:"dry_run"`
	ChainID       string               `json:"chain_id"`
	Signer        string               `json:"signer"`
	AccountNumber uint64               `json:"account_number"`
	Sequence      uint64               `json:"sequence"`
	Namespace     string               `json:"namespace"` // full 29-byte namespace, hex
	Payload       export.AnchorPayload `json:"payload"`
	PayloadBytes  int                  `json:"payload_bytes"`
	// ShareCommitment is the blob's commitment as the MsgPayForBlobs
	// carries it: what a light node or explorer shows for the blob.
	ShareCommitment string  `json:"share_commitment"`
	GasLimit        uint64  `json:"gas_limit"`
	GasPrice        float64 `json:"gas_price_utia"`
	FeeUtia         uint64  `json:"fee_utia"`
	// TxHash is sha256 of the inner signed transaction, the hash the chain
	// will index it under.
	TxHash string `json:"tx_hash"`
	// BlobTx is the complete BlobTx (signed tx + blob), standard base64:
	// the bytes a broadcast would send.
	BlobTx string `json:"blob_tx_base64"`
	Note   string `json:"note"`
}

func buildAnchorTx(kr keyring.Keyring, p anchorParams) (*anchorTx, error) {
	ecfg := encoding.MakeConfig(app.ModuleEncodingRegisters...)
	rec, err := kr.Key(p.KeyName)
	if err != nil {
		return nil, fmt.Errorf("key %s: %w", p.KeyName, err)
	}
	addr, err := rec.GetAddress()
	if err != nil {
		return nil, err
	}
	ns, err := share.NewV0Namespace(p.NamespaceID)
	if err != nil {
		return nil, fmt.Errorf("namespace: %w", err)
	}
	if err := blobtypes.ValidateBlobNamespace(ns); err != nil {
		return nil, fmt.Errorf("namespace: %w", err)
	}
	data, err := p.Payload.Bytes()
	if err != nil {
		return nil, err
	}
	blob, err := share.NewV0Blob(ns, data)
	if err != nil {
		return nil, err
	}
	msg, err := blobtypes.NewMsgPayForBlobs(addr.String(), appconsts.Version, blob)
	if err != nil {
		return nil, err
	}
	gas := blobtypes.DefaultEstimateGas(msg)
	fee := uint64(math.Ceil(float64(gas) * p.GasPrice))
	signer, err := user.NewSigner(kr, ecfg.TxConfig, p.ChainID, user.NewAccount(p.KeyName, p.AccountNumber, p.Sequence))
	if err != nil {
		return nil, err
	}
	blobTx, _, err := signer.CreatePayForBlobs(p.KeyName, []*share.Blob{blob}, user.SetGasLimit(gas), user.SetFee(fee))
	if err != nil {
		return nil, err
	}
	inner, err := innerTx(blobTx)
	if err != nil {
		return nil, err
	}
	txHash := sha256.Sum256(inner)
	return &anchorTx{
		DryRun: true, ChainID: p.ChainID, Signer: addr.String(), AccountNumber: p.AccountNumber, Sequence: p.Sequence,
		Namespace: hex.EncodeToString(ns.Bytes()), Payload: p.Payload, PayloadBytes: len(data),
		ShareCommitment: base64.StdEncoding.EncodeToString(msg.ShareCommitments[0]),
		GasLimit:        gas, GasPrice: p.GasPrice, FeeUtia: fee,
		TxHash: strings.ToUpper(hex.EncodeToString(txHash[:])),
		BlobTx: base64.StdEncoding.EncodeToString(blobTx),
		Note:   "dry run: built and signed, not broadcast",
	}, nil
}

// innerTx is the signed transaction inside a BlobTx: the part the chain
// hashes and indexes.
func innerTx(raw []byte) ([]byte, error) {
	btx, ok, err := blobtx.UnmarshalBlobTx(raw)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errors.New("CreatePayForBlobs did not return a BlobTx")
	}
	return btx.Tx, nil
}

func fatal(f string, a ...any) {
	fmt.Fprintf(os.Stderr, "ANCHOR-FATAL: "+f+"\n", a...)
	os.Exit(1)
}
