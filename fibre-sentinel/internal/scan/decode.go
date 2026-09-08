package scan

import (
	"fmt"
	"strings"

	fibretypes "github.com/celestiaorg/celestia-app/v10/x/fibre/types"
	abci "github.com/cometbft/cometbft/abci/types"
	cosmostx "github.com/cosmos/cosmos-sdk/types/tx"
	gogojsonpb "github.com/cosmos/gogoproto/jsonpb"
)

// decodePayForFibre pulls a single-message MsgPayForFibre out of raw SDK tx
// bytes, decoding exactly the way the SDK's own decoder does (outer TxRaw with
// opaque body_bytes). Returns (nil, nil) when the tx is not a single-message
// MsgPayForFibre, and (nil, err) when it looks like one but is malformed.
func decodePayForFibre(txBytes []byte) (*fibretypes.MsgPayForFibre, error) {
	var raw cosmostx.TxRaw
	if err := raw.Unmarshal(txBytes); err != nil {
		return nil, nil
	}
	var body cosmostx.TxBody
	if err := body.Unmarshal(raw.BodyBytes); err != nil {
		return nil, nil
	}
	if len(body.Messages) != 1 {
		return nil, nil
	}
	if body.Messages[0].TypeUrl != fibretypes.MsgPayForFibreTypeURL {
		return nil, nil
	}
	var msg fibretypes.MsgPayForFibre
	if err := msg.Unmarshal(body.Messages[0].Value); err != nil {
		return nil, fmt.Errorf("unmarshal MsgPayForFibre: %w", err)
	}
	return &msg, nil
}

// eventUpdateFibreParamsType is the typed-event name emitted by
// keeper.UpdateFibreParams.
const eventUpdateFibreParamsType = "celestia.fibre.v1.EventUpdateFibreParams"

// parseUpdateFibreParams extracts the new Params from an EventUpdateFibreParams
// ABCI event. The SDK emits typed events as JSON attributes: the "params"
// attribute value is the gogoproto-jsonpb encoding of the Params message.
func parseUpdateFibreParams(ev abci.Event) (fibretypes.Params, bool, error) {
	if ev.Type != eventUpdateFibreParamsType {
		return fibretypes.Params{}, false, nil
	}
	for _, attr := range ev.Attributes {
		if attr.Key != "params" {
			continue
		}
		var p fibretypes.Params
		u := gogojsonpb.Unmarshaler{AllowUnknownFields: true}
		if err := u.Unmarshal(strings.NewReader(attr.Value), &p); err != nil {
			return fibretypes.Params{}, true, fmt.Errorf("parse EventUpdateFibreParams params: %w (raw=%s)", err, attr.Value)
		}
		return p, true, nil
	}
	return fibretypes.Params{}, true, fmt.Errorf("EventUpdateFibreParams without a params attribute")
}
