package main

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/celestiaorg/celestia-app/v10/app"
	"github.com/celestiaorg/celestia-app/v10/app/encoding"
	blobtypes "github.com/celestiaorg/celestia-app/v10/x/blob/types"
	blobtx "github.com/celestiaorg/go-square/v4/tx"
	"github.com/cosmos/cosmos-sdk/crypto/hd"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/x/auth/signing"

	"github.com/plsgiveup/fibre/fibre-sentinel/observer/export"
)

// The anchor is built and signed entirely offline, and what it prints
// decodes back into exactly one MsgPayForBlobs from the keyring's key,
// carrying the payload under the anchor namespace, with the fee and the
// account sequence it claims. Nothing here dials anything.
func TestBuildAnchorTxOffline(t *testing.T) {
	ecfg := encoding.MakeConfig(app.ModuleEncodingRegisters...)
	kr := keyring.NewInMemory(ecfg.Codec)
	rec, _, err := kr.NewMnemonic("tensile-ops", keyring.English, sdk.GetConfig().GetFullBIP44Path(), keyring.DefaultBIP39Passphrase, hd.Secp256k1)
	if err != nil {
		t.Fatal(err)
	}
	addr, _ := rec.GetAddress()
	payload := export.AnchorPayload{Type: export.AnchorPayloadType, Vantage: "v", Day: "2026-09-10", Export: "tensile-v-2026-09-10.tar.gz",
		ManifestSHA256: strings.Repeat("ab", 32), TarballSHA256: strings.Repeat("cd", 32)}
	out, err := buildAnchorTx(kr, anchorParams{KeyName: "tensile-ops", ChainID: "mocha-4", AccountNumber: 42, Sequence: 7,
		NamespaceID: []byte(export.AnchorNamespaceID), Payload: payload, GasPrice: 0.004})
	if err != nil {
		t.Fatal(err)
	}
	if !out.DryRun || out.Signer != addr.String() || out.AccountNumber != 42 || out.Sequence != 7 || out.ChainID != "mocha-4" {
		t.Errorf("header = %+v", out)
	}
	if !strings.HasSuffix(out.Namespace, hex.EncodeToString([]byte(export.AnchorNamespaceID))) || len(out.Namespace) != 58 {
		t.Errorf("namespace = %s", out.Namespace)
	}
	raw, err := base64.StdEncoding.DecodeString(out.BlobTx)
	if err != nil {
		t.Fatal(err)
	}
	btx, ok, err := blobtx.UnmarshalBlobTx(raw)
	if err != nil || !ok || len(btx.Blobs) != 1 {
		t.Fatalf("not a one-blob BlobTx: ok=%v err=%v", ok, err)
	}
	want, _ := payload.Bytes()
	if !bytes.Equal(btx.Blobs[0].Data(), want) {
		t.Errorf("blob data = %s, want the payload", btx.Blobs[0].Data())
	}
	tx, err := ecfg.TxConfig.TxDecoder()(btx.Tx)
	if err != nil {
		t.Fatal(err)
	}
	msgs := tx.GetMsgs()
	if len(msgs) != 1 {
		t.Fatalf("%d messages", len(msgs))
	}
	pfb, ok := msgs[0].(*blobtypes.MsgPayForBlobs)
	if !ok || pfb.Signer != addr.String() || len(pfb.BlobSizes) != 1 || int(pfb.BlobSizes[0]) != len(want) {
		t.Fatalf("message = %#v", msgs[0])
	}
	if base64.StdEncoding.EncodeToString(pfb.ShareCommitments[0]) != out.ShareCommitment {
		t.Error("printed share commitment is not the message's")
	}
	fee := tx.(sdk.FeeTx)
	if fee.GetGas() != out.GasLimit || fee.GetGas() != blobtypes.DefaultEstimateGas(pfb) || fee.GetFee().AmountOf("utia").Uint64() != out.FeeUtia || out.FeeUtia == 0 {
		t.Errorf("gas %d fee %s, printed %d / %d", fee.GetGas(), fee.GetFee(), out.GasLimit, out.FeeUtia)
	}
	t.Logf("anchor: payload %d bytes, gas %d, fee %d utia at 0.004", out.PayloadBytes, out.GasLimit, out.FeeUtia)
	sigs, err := tx.(signing.SigVerifiableTx).GetSignaturesV2()
	if err != nil || len(sigs) != 1 || sigs[0].Sequence != 7 {
		t.Errorf("signatures %+v err %v", sigs, err)
	}
	// A reserved namespace is refused before anything is signed.
	if _, err := buildAnchorTx(kr, anchorParams{KeyName: "tensile-ops", ChainID: "mocha-4", NamespaceID: []byte{0}, Payload: payload, GasPrice: 0.004}); err == nil {
		t.Error("the all-zero (reserved) namespace was accepted")
	}
}

// The chain id is never a default: it comes from the node or the flag, the
// two must agree when both are given, and neither is an error.
func TestTheChainIDIsReadOrRequired(t *testing.T) {
	for _, c := range []struct {
		flagged, node, want string
		ok                  bool
	}{
		{"", "mocha-5", "mocha-5", true},
		{"mocha-5", "mocha-5", "mocha-5", true},
		{"mocha-5", "", "mocha-5", true},
		{"mocha-4", "mocha-5", "", false},
		{"", "", "", false},
	} {
		got, err := resolveChainID(c.flagged, c.node)
		if (err == nil) != c.ok || got != c.want {
			t.Errorf("resolveChainID(%q, %q) = %q, %v", c.flagged, c.node, got, err)
		}
	}
}
