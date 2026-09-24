package scan

import (
	"context"
	"fmt"

	cmtbytes "github.com/cometbft/cometbft/libs/bytes"
	rpcclient "github.com/cometbft/cometbft/rpc/client"
	"github.com/cosmos/cosmos-sdk/types/bech32"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
)

// FibreModuleName is x/fibre's module account. Every deposit is sent to it
// (MsgDepositToEscrow), every settlement and timeout leaves it for the fee
// collector, and every executed withdrawal leaves it for the depositor, so
// its bank balance is the escrow held across all accounts — including
// accounts this observer never saw publish, which a sum over the known
// publishers' EscrowAccount answers misses.
const FibreModuleName = "fibre"

// ModuleAddress is a module account's bech32 address on Celestia.
func ModuleAddress(module string) (string, error) {
	return bech32.ConvertAndEncode(accountHRP, authtypes.NewModuleAddress(module))
}

// ModuleBalance reads a module account's balance of denom at the latest height.
func (c *Chain) ModuleBalance(parent context.Context, module, denom string) (int64, error) {
	ctx, cancel := c.ctx(parent)
	defer cancel()
	addr, err := ModuleAddress(module)
	if err != nil {
		return 0, err
	}
	req := banktypes.QueryBalanceRequest{Address: addr, Denom: denom}
	data, err := req.Marshal()
	if err != nil {
		return 0, fmt.Errorf("marshal balance request: %w", err)
	}
	const path = "/cosmos.bank.v1beta1.Query/Balance"
	res, err := c.rpc.ABCIQueryWithOptions(ctx, path, cmtbytes.HexBytes(data), rpcclient.ABCIQueryOptions{})
	if err != nil {
		return 0, fmt.Errorf("abci query balance: %w", err)
	}
	if res.Response.Code != 0 {
		return 0, c.queryError(parent, path, 0, res.Response)
	}
	var resp banktypes.QueryBalanceResponse
	if err := resp.Unmarshal(res.Response.Value); err != nil {
		return 0, fmt.Errorf("unmarshal balance response: %w", err)
	}
	if resp.Balance == nil {
		return 0, nil
	}
	if !resp.Balance.Amount.IsInt64() {
		return 0, fmt.Errorf("balance %s does not fit int64", resp.Balance.Amount)
	}
	return resp.Balance.Amount.Int64(), nil
}
