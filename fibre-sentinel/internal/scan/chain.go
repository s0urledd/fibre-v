package scan

import (
	"context"
	"fmt"
	"time"

	fibretypes "github.com/celestiaorg/celestia-app/v10/x/fibre/types"
	valaddrtypes "github.com/celestiaorg/celestia-app/v10/x/valaddr/types"
	abci "github.com/cometbft/cometbft/abci/types"
	cmtbytes "github.com/cometbft/cometbft/libs/bytes"
	rpcclient "github.com/cometbft/cometbft/rpc/client"
	rpchttp "github.com/cometbft/cometbft/rpc/client/http"
	cmttypes "github.com/cometbft/cometbft/types"
)

// Chain is a thin, timeout-bounded wrapper over a single CometBFT RPC endpoint.
// Every call takes a fresh child context with Timeout; nothing here can block
// forever. The scanner only reads — no subscriptions, no websockets.
type Chain struct {
	rpc     *rpchttp.HTTP
	timeout time.Duration
	log     *Logger
}

// NewChain dials rpcURL (e.g. http://127.0.0.1:26657). It does not verify
// connectivity; the first real call will surface a dead endpoint.
func NewChain(rpcURL string, timeout time.Duration, log *Logger) (*Chain, error) {
	c, err := rpchttp.New(rpcURL, "/websocket")
	if err != nil {
		return nil, fmt.Errorf("rpc client for %s: %w", rpcURL, err)
	}
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	return &Chain{rpc: c, timeout: timeout, log: log}, nil
}

func (c *Chain) ctx(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, c.timeout)
}

// Status returns (chainID, latestHeight).
func (c *Chain) Status(parent context.Context) (string, int64, error) {
	ctx, cancel := c.ctx(parent)
	defer cancel()
	s, err := c.rpc.Status(ctx)
	if err != nil {
		return "", 0, fmt.Errorf("status: %w", err)
	}
	return s.NodeInfo.Network, s.SyncInfo.LatestBlockHeight, nil
}

// Block holds only what the scanner needs from one block.
type Block struct {
	Height int64
	Time   time.Time
	Txs    []cmttypes.Tx
}

// Block fetches block header + raw txs at height.
func (c *Chain) Block(parent context.Context, height int64) (*Block, error) {
	ctx, cancel := c.ctx(parent)
	defer cancel()
	res, err := c.rpc.Block(ctx, &height)
	if err != nil {
		return nil, fmt.Errorf("block %d: %w", height, err)
	}
	return &Block{
		Height: res.Block.Height,
		Time:   res.Block.Time,
		Txs:    res.Block.Data.Txs,
	}, nil
}

// BlockResults holds the per-tx result codes and the events the scanner scans
// for EventUpdateFibreParams (both tx events and FinalizeBlock events).
type BlockResults struct {
	Height       int64
	TxCodes      []uint32
	TxEvents     [][]abci.Event
	FinalizeEvts []abci.Event
}

// BlockResults fetches execution results at height.
func (c *Chain) BlockResults(parent context.Context, height int64) (*BlockResults, error) {
	ctx, cancel := c.ctx(parent)
	defer cancel()
	res, err := c.rpc.BlockResults(ctx, &height)
	if err != nil {
		return nil, fmt.Errorf("block_results %d: %w", height, err)
	}
	out := &BlockResults{
		Height:       res.Height,
		TxCodes:      make([]uint32, len(res.TxsResults)),
		TxEvents:     make([][]abci.Event, len(res.TxsResults)),
		FinalizeEvts: res.FinalizeBlockEvents,
	}
	for i, r := range res.TxsResults {
		out.TxCodes[i] = r.Code
		out.TxEvents[i] = r.Events
	}
	return out, nil
}

// ValSetMember is one validator at a height.
type ValSetMember struct {
	Address     []byte // 20-byte consensus address
	PubKey      []byte // 32-byte ed25519 consensus key
	VotingPower int64
}

// ValidatorSet returns the full consensus validator set at height, paging until
// it has every member.
func (c *Chain) ValidatorSet(parent context.Context, height int64) ([]ValSetMember, error) {
	const perPage = 100
	var out []ValSetMember
	for page := 1; ; page++ {
		ctx, cancel := c.ctx(parent)
		p := page
		pp := perPage
		res, err := c.rpc.Validators(ctx, &height, &p, &pp)
		cancel()
		if err != nil {
			return nil, fmt.Errorf("validators h=%d page=%d: %w", height, page, err)
		}
		for _, v := range res.Validators {
			out = append(out, ValSetMember{
				Address:     append([]byte(nil), v.Address.Bytes()...),
				PubKey:      append([]byte(nil), v.PubKey.Bytes()...),
				VotingPower: v.VotingPower,
			})
		}
		if len(out) >= res.Total || len(res.Validators) == 0 {
			break
		}
	}
	return out, nil
}

// FibreParamsAt reads the on-chain fibre module params as of height, straight
// from the chain (ABCI query, no gRPC). height <= 0 means latest.
func (c *Chain) FibreParamsAt(parent context.Context, height int64) (fibretypes.Params, error) {
	ctx, cancel := c.ctx(parent)
	defer cancel()

	req := fibretypes.QueryParamsRequest{}
	data, err := req.Marshal()
	if err != nil {
		return fibretypes.Params{}, fmt.Errorf("marshal params request: %w", err)
	}
	opts := rpcclient.ABCIQueryOptions{Height: height, Prove: false}
	res, err := c.rpc.ABCIQueryWithOptions(ctx, "/celestia.fibre.v1.Query/Params", cmtbytes.HexBytes(data), opts)
	if err != nil {
		return fibretypes.Params{}, fmt.Errorf("abci query params h=%d: %w", height, err)
	}
	if res.Response.Code != 0 {
		return fibretypes.Params{}, fmt.Errorf("abci query params h=%d: code=%d log=%s", height, res.Response.Code, res.Response.Log)
	}
	var resp fibretypes.QueryParamsResponse
	if err := resp.Unmarshal(res.Response.Value); err != nil {
		return fibretypes.Params{}, fmt.Errorf("unmarshal params response: %w", err)
	}
	return resp.Params, nil
}

// FibreProvider is one bonded validator's registered fibre service host.
type FibreProvider struct {
	ConsAddressBech32 string // celestiavalcons1...
	Host              string // host:port the validator serves fibre from
}

// BondedFibreProviders lists the fibre service host every currently-bonded
// validator has registered (x/valaddr AllBondedFibreProviders, latest height,
// ABCI query — no gRPC). Providers whose validator left the active set are
// already excluded by the chain.
func (c *Chain) BondedFibreProviders(parent context.Context) ([]FibreProvider, error) {
	ctx, cancel := c.ctx(parent)
	defer cancel()

	req := valaddrtypes.QueryAllBondedFibreProvidersRequest{}
	data, err := req.Marshal()
	if err != nil {
		return nil, fmt.Errorf("marshal providers request: %w", err)
	}
	res, err := c.rpc.ABCIQueryWithOptions(ctx, "/celestia.valaddr.v1.Query/AllBondedFibreProviders", cmtbytes.HexBytes(data), rpcclient.ABCIQueryOptions{})
	if err != nil {
		return nil, fmt.Errorf("abci query bonded fibre providers: %w", err)
	}
	if res.Response.Code != 0 {
		return nil, fmt.Errorf("abci query bonded fibre providers: code=%d log=%s", res.Response.Code, res.Response.Log)
	}
	var resp valaddrtypes.QueryAllBondedFibreProvidersResponse
	if err := resp.Unmarshal(res.Response.Value); err != nil {
		return nil, fmt.Errorf("unmarshal providers response: %w", err)
	}
	out := make([]FibreProvider, 0, len(resp.Providers))
	for _, p := range resp.Providers {
		out = append(out, FibreProvider{ConsAddressBech32: p.ValidatorConsensusAddress, Host: p.Info.Host})
	}
	return out, nil
}

// ChainID returns the network id from /status.
func (c *Chain) ChainID(parent context.Context) (string, error) {
	id, _, err := c.Status(parent)
	return id, err
}
