package scan

import (
	"context"
	cryptoed25519 "crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	fibretypes "github.com/celestiaorg/celestia-app/v10/x/fibre/types"
	valaddrtypes "github.com/celestiaorg/celestia-app/v10/x/valaddr/types"
	abci "github.com/cometbft/cometbft/abci/types"
	cmtbytes "github.com/cometbft/cometbft/libs/bytes"
	rpcclient "github.com/cometbft/cometbft/rpc/client"
	rpchttp "github.com/cometbft/cometbft/rpc/client/http"
	cmttypes "github.com/cometbft/cometbft/types"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	"github.com/cosmos/cosmos-sdk/crypto/keys/ed25519"
	sdkquery "github.com/cosmos/cosmos-sdk/types/query"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
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
//
// The HTTP client is ours rather than CometBFT's default, for one reason: the
// default builds a Transport with a hand-rolled dialer and no Proxy function,
// so it ignores HTTPS_PROXY and connects straight out. On a host that only has
// egress through a proxy — a corporate network, a locked-down VPS, a CI
// sandbox — every call fails with something that looks like the chain refusing
// us rather than like a proxy we never asked. http.ProxyFromEnvironment is the
// standard library's own rule and is a no-op when no proxy is configured, so
// the common case is unchanged.
func NewChain(rpcURL string, timeout time.Duration, log *Logger) (*Chain, error) {
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	httpc := &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			DialContext:           (&net.Dialer{Timeout: timeout, KeepAlive: 30 * time.Second}).DialContext,
			TLSHandshakeTimeout:   timeout,
			ResponseHeaderTimeout: timeout,
			MaxIdleConns:          16,
			MaxIdleConnsPerHost:   16,
			IdleConnTimeout:       90 * time.Second,
		},
	}
	c, err := rpchttp.NewWithClient(rpcURL, "/websocket", httpc)
	if err != nil {
		return nil, fmt.Errorf("rpc client for %s: %w", rpcURL, err)
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

// AppVersion is the application version the chain is currently running, from
// ABCIInfo. It is the one number that says whether Fibre exists here at all:
// x/fibre and x/valaddr are introduced in app version 10, so on a chain below
// that every Fibre query fails for a reason that has nothing to do with any
// validator. An observer that cannot tell "the module is not there" from "the
// module is there and empty" will publish the second when the first is true.
func (c *Chain) AppVersion(parent context.Context) (uint64, error) {
	ctx, cancel := c.ctx(parent)
	defer cancel()
	info, err := c.rpc.ABCIInfo(ctx)
	if err != nil {
		return 0, fmt.Errorf("abci_info: %w", err)
	}
	return info.Response.AppVersion, nil
}

// FibreAppVersion is the app version x/fibre and x/valaddr first exist at.
const FibreAppVersion = 10

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
		return fibretypes.Params{}, &ABCIError{Path: "/celestia.fibre.v1.Query/Params", Height: height,
			Code: res.Response.Code, Codespace: res.Response.Codespace, Log: res.Response.Log}
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

// ValidatorIdentity is what the staking module says about one validator:
// the name its operator chose and the facts a reader needs to recognise it.
//
// It is read from the chain, not from an explorer's API. An observer whose
// validator names come from somebody else's index is that much less
// independent, and it inherits that index's rate limits, attribution terms
// and coverage. The staking module carries all of this already, on every
// Cosmos chain, including the networks a third-party indexer has not got
// round to.
type ValidatorIdentity struct {
	// ConsAddressHex is the 20-byte consensus address, lower-case hex. It is
	// derived here from the validator's consensus public key so that it joins
	// directly against the address every probe row and assignment already
	// uses, with no bech32 round trip.
	ConsAddressHex string
	// OperatorAddress is the celestiavaloper... form, for linking out.
	OperatorAddress string
	Moniker         string
	// Identity is the operator's Keybase key suffix, when it set one. It is
	// how an avatar could be looked up later; it is not needed for a name.
	Identity string
	Website  string
	// Tokens is the staked amount as the chain reports it, and Jailed says
	// whether the validator is currently jailed. Both are the chain's own
	// words about the validator, unlike anything this observer measures.
	Tokens string
	Jailed bool
	// Status is BOND_STATUS_BONDED, _UNBONDING or _UNBONDED. A validator that
	// is not bonded still owes the shards it signed for, so this is shown
	// rather than used to filter anyone out.
	Status string
}

// ValidatorIdentities returns every validator the staking module knows,
// bonded or not, paging until the set is complete.
//
// Unbonded and jailed validators are deliberately included. A validator's
// Fibre obligation comes from the promise it signed, which outlives its
// bonding, and dropping it here would hide exactly the validator whose row a
// reader is most likely to be looking for.
func (c *Chain) ValidatorIdentities(parent context.Context) ([]ValidatorIdentity, error) {
	var out []ValidatorIdentity
	var nextKey []byte
	for page := 0; ; page++ {
		if page > 64 {
			return nil, fmt.Errorf("validator identities: more than 64 pages; refusing to keep paging")
		}
		batch, key, err := c.validatorIdentityPage(parent, nextKey)
		if err != nil {
			return nil, err
		}
		out = append(out, batch...)
		if len(key) == 0 {
			return out, nil
		}
		nextKey = key
	}
}

func (c *Chain) validatorIdentityPage(parent context.Context, key []byte) ([]ValidatorIdentity, []byte, error) {
	ctx, cancel := c.ctx(parent)
	defer cancel()

	req := stakingtypes.QueryValidatorsRequest{
		Pagination: &sdkquery.PageRequest{Key: key, Limit: 200},
	}
	data, err := req.Marshal()
	if err != nil {
		return nil, nil, fmt.Errorf("marshal validators request: %w", err)
	}
	res, err := c.rpc.ABCIQueryWithOptions(ctx, "/cosmos.staking.v1beta1.Query/Validators", cmtbytes.HexBytes(data), rpcclient.ABCIQueryOptions{})
	if err != nil {
		return nil, nil, fmt.Errorf("abci query validators: %w", err)
	}
	if res.Response.Code != 0 {
		return nil, nil, fmt.Errorf("abci query validators: code=%d log=%s", res.Response.Code, res.Response.Log)
	}
	var resp stakingtypes.QueryValidatorsResponse
	if err := resp.Unmarshal(res.Response.Value); err != nil {
		return nil, nil, fmt.Errorf("unmarshal validators response: %w", err)
	}

	out := make([]ValidatorIdentity, 0, len(resp.Validators))
	for _, v := range resp.Validators {
		id := ValidatorIdentity{
			OperatorAddress: v.OperatorAddress,
			Moniker:         v.Description.Moniker,
			Identity:        v.Description.Identity,
			Website:         v.Description.Website,
			Tokens:          v.Tokens.String(),
			Jailed:          v.Jailed,
			Status:          v.Status.String(),
		}
		if addr, err := consAddressFromAny(v.ConsensusPubkey); err == nil {
			id.ConsAddressHex = addr
		} else if c.log != nil {
			// A validator whose key this build cannot parse still belongs in
			// the list; it simply cannot be joined to a probe row, and a
			// silent drop would look like the validator not existing.
			c.log.Printf("validator %s: consensus key: %v", v.OperatorAddress, err)
		}
		out = append(out, id)
	}
	var next []byte
	if resp.Pagination != nil {
		next = resp.Pagination.NextKey
	}
	return out, next, nil
}

// consAddressFromAny derives the 20-byte consensus address from a validator's
// consensus public key, the same way CometBFT does: the first 20 bytes of the
// SHA-256 of the raw ed25519 key. Deriving it here means the staking view and
// the probe rows share one identifier with no bech32 conversion in between.
func consAddressFromAny(pk *codectypes.Any) (string, error) {
	if pk == nil {
		return "", fmt.Errorf("no consensus public key")
	}
	var key ed25519.PubKey
	if err := key.Unmarshal(pk.Value); err != nil {
		return "", fmt.Errorf("unmarshal consensus key: %w", err)
	}
	if len(key.Key) != cryptoed25519.PublicKeySize {
		return "", fmt.Errorf("consensus key is %d bytes, want %d", len(key.Key), cryptoed25519.PublicKeySize)
	}
	sum := sha256.Sum256(key.Key)
	return strings.ToLower(hex.EncodeToString(sum[:20])), nil
}

// ChainID returns the network id from /status.
func (c *Chain) ChainID(parent context.Context) (string, error) {
	id, _, err := c.Status(parent)
	return id, err
}

// LatestBlockTime returns the timestamp of the chain's latest block, for
// comparing the observer's own clock against the chain's.
func (c *Chain) LatestBlockTime(parent context.Context) (time.Time, error) {
	ctx, cancel := c.ctx(parent)
	defer cancel()
	s, err := c.rpc.Status(ctx)
	if err != nil {
		return time.Time{}, fmt.Errorf("status: %w", err)
	}
	return s.SyncInfo.LatestBlockTime, nil
}

// ABCIError is a non-zero ABCI query response code.
type ABCIError struct {
	Path      string
	Height    int64
	Code      uint32
	Codespace string
	Log       string
}

func (e *ABCIError) Error() string {
	return fmt.Sprintf("abci query %s h=%d: code=%d codespace=%s log=%s", e.Path, e.Height, e.Code, e.Codespace, e.Log)
}

// IsResultsNotPersisted reports the block_results error of a node that runs
// with storage.discard_abci_responses = true. Retrying never helps.
func IsResultsNotPersisted(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "not persisted") || strings.Contains(s, "discard_abci_responses")
}
