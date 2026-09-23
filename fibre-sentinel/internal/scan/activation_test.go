package scan

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	fibretypes "github.com/celestiaorg/celestia-app/v10/x/fibre/types"
	valaddrtypes "github.com/celestiaorg/celestia-app/v10/x/valaddr/types"
	abci "github.com/cometbft/cometbft/abci/types"
	cmtjson "github.com/cometbft/cometbft/libs/json"
	cmtversion "github.com/cometbft/cometbft/proto/tendermint/version"
	coretypes "github.com/cometbft/cometbft/rpc/core/types"
	cmttypes "github.com/cometbft/cometbft/types"
)

// upgradeNode is a node already on the Fibre binary, on a chain whose
// upgrade is at height upgrade: blocks below it ran under app v9, blocks
// from it on under v10. It answers the way such a node does, which is the
// answer the scanner had never been tested against: x/fibre and x/valaddr
// asked about a height from before the upgrade come back as a recovered
// panic (code 111222, the module's store did not exist at that version),
// not as "unknown query path".
type upgradeNode struct {
	srv     *httptest.Server
	upgrade int64
	tip     int64

	mu        sync.Mutex
	calls     map[string]int
	params    func(h int64) fibretypes.Params
	provs     []byte
	panicFrom int64 // > 0: x/fibre also panics at every height from here on
}

func newUpgradeNode(t *testing.T, upgrade, tip int64, provs []byte) *upgradeNode {
	t.Helper()
	n := &upgradeNode{upgrade: upgrade, tip: tip, calls: map[string]int{}, provs: provs,
		params: func(int64) fibretypes.Params { return fibretypes.DefaultParams() }}
	blockTime := time.Date(2026, 9, 24, 18, 0, 0, 0, time.UTC)
	header := func(h int64) *cmttypes.Header {
		app := uint64(9)
		if h >= n.upgrade {
			app = FibreAppVersion
		}
		return &cmttypes.Header{Version: cmtversion.Consensus{Block: 11, App: app}, ChainID: "test-1", Height: h,
			Time: blockTime.Add(time.Duration(h) * 6 * time.Second)}
	}
	n.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params map[string]any  `json:"params"`
		}
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &req); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		var height int64
		if h, ok := req.Params["height"].(string); ok {
			fmt.Sscan(h, &height)
		}
		path, _ := req.Params["path"].(string)
		n.mu.Lock()
		n.calls[req.Method+" "+path]++
		panicFrom, params := n.panicFrom, n.params
		n.mu.Unlock()

		var result any
		switch req.Method {
		case "status":
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":{"node_info":{"network":"test-1","protocol_version":{"p2p":"8","block":"11","app":"10"},"id":"","listen_addr":"","version":"","channels":"","moniker":"fake","other":{"tx_index":"on","rpc_address":""}},"sync_info":{"latest_block_hash":"","latest_app_hash":"","latest_block_height":"%d","latest_block_time":"2026-09-24T18:00:00Z","earliest_block_hash":"","earliest_app_hash":"","earliest_block_height":"1","earliest_block_time":"2026-09-21T00:00:00Z","catching_up":false},"validator_info":{"address":"","pub_key":null,"voting_power":"0"}}}`, req.ID, n.tip)
			return
		case "header":
			result = coretypes.ResultHeader{Header: header(height)}
		case "block":
			result = coretypes.ResultBlock{Block: &cmttypes.Block{Header: *header(height)}}
		case "block_results":
			result = coretypes.ResultBlockResults{Height: height}
		case "abci_query":
			resp := abci.ResponseQuery{Height: height}
			switch {
			case height > 0 && height < n.upgrade,
				path == pathFibreParams && panicFrom > 0 && height >= panicFrom:
				resp.Code, resp.Codespace = abciPanicCode, "undefined"
				resp.Log = "panic: runtime error: invalid memory address or nil pointer dereference"
			case path == pathFibreParams:
				resp.Value, _ = (&fibretypes.QueryParamsResponse{Params: params(height)}).Marshal()
			case path == pathProviders:
				resp.Value = n.provs
			default:
				resp.Code, resp.Codespace, resp.Log = 6, "sdk", "unknown query path: unknown request"
			}
			result = coretypes.ResultABCIQuery{Response: resp}
		default:
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"error":{"code":-32601,"message":"Method not found"}}`, req.ID)
			return
		}
		raw, err := cmtjson.Marshal(result)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":%s}`, req.ID, raw)
	}))
	t.Cleanup(n.srv.Close)
	return n
}

func (n *upgradeNode) count(key string) int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.calls[key]
}

// The answer itself: a v10 node's panic about a pre-upgrade height is the
// module not existing yet, proven from that block's header, and a panic at a
// height where the module exists is not — it is asked a few times, quickly,
// and handed back rather than retried for the life of the process.
func TestAV10NodesPanicAboutAPreUpgradeHeightMeansTheModuleWasNotThereYet(t *testing.T) {
	node := newUpgradeNode(t, 1003, 1030, nil)
	c, err := NewChain(node.srv.URL, 2*time.Second, NewLogger(20))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	_, err = c.FibreParamsAt(ctx, 1002)
	var ae *ABCIError
	if !errors.As(err, &ae) || ae.Code != abciPanicCode || !ae.BeforeModule || ae.AppVersion != 9 {
		t.Fatalf("x/fibre at the last v9 height: %v (%+v)", err, ae)
	}
	if !IsModuleInactive(err) {
		t.Fatalf("a pre-upgrade panic not read as inactive: %v", err)
	}
	if _, err := c.BondedFibreProvidersAt(ctx, 1002); !IsModuleInactive(err) {
		t.Fatalf("x/valaddr at the last v9 height: %v", err)
	}
	if _, _, err := c.FibreProviderInfoAt(ctx, "celestiavalcons1x", 1002); !IsModuleInactive(err) {
		t.Fatalf("x/valaddr provider info at the last v9 height: %v", err)
	}
	if _, err := c.FibreParamsAt(ctx, 1003); err != nil {
		t.Fatalf("x/fibre at the first v10 height: %v", err)
	}

	// A panic where the module exists is the app's own trouble, not "not
	// there yet".
	node.mu.Lock()
	node.panicFrom = 1005
	node.mu.Unlock()
	_, err = c.FibreParamsAt(ctx, 1006)
	if err == nil || IsModuleInactive(err) || IsHeightUnavailable(err) {
		t.Fatalf("a panic at a v10 height: %v", err)
	}

	old := appPanicWait
	appPanicWait = time.Millisecond
	t.Cleanup(func() { appPanicWait = old })
	s := &Scanner{log: NewLogger(20), chain: c}
	tctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	before := node.count("abci_query " + pathFibreParams)
	start := time.Now()
	err = s.retryRPC(tctx, "params at height 1006", func() error {
		_, e := c.FibreParamsAt(tctx, 1006)
		return e
	})
	if err == nil || tctx.Err() != nil {
		t.Fatalf("a repeated panic: err=%v ctx=%v", err, tctx.Err())
	}
	if n := node.count("abci_query "+pathFibreParams) - before; n != appPanicTries {
		t.Fatalf("asked %d times, want %d", n, appPanicTries)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("a repeated panic held the caller for %s", d)
	}
}

// The failure this guards against, as it would have happened on mocha: a
// scanner that followed the chain on v9 as "x/fibre inactive", a node
// restarted on the Fibre binary, and the scanner still a few blocks short of
// the upgrade height. Every scheduled seed try at a pre-upgrade height was a
// panic the scanner took for an outage and retried forever: the scan stopped
// at the last v9 hundred and never reached activation.
//
// With the fix it passes through, seeds params and hosts at the first v10
// block (not at the next hundred), starts the first reconcile's interval at
// that seed, and a silent change inside that interval is verified by reading
// every height from the seed on, not declared unresolvable because the scan
// started before Fibre existed.
func TestAScannerCrossingTheUpgradeOnAV10NodeSeedsAtTheFirstV10Block(t *testing.T) {
	bechA, addrA := consAddr(t, 0xaa)
	provs, err := (&valaddrtypes.QueryAllBondedFibreProvidersResponse{Providers: []valaddrtypes.FibreProvider{
		{ValidatorConsensusAddress: bechA, Info: valaddrtypes.FibreProviderInfo{Host: "a.example:9090"}},
	}}).Marshal()
	if err != nil {
		t.Fatal(err)
	}
	node := newUpgradeNode(t, 1003, 1030, provs)
	changed := fibretypes.DefaultParams()
	changed.ShardRetention += time.Hour
	node.params = func(h int64) fibretypes.Params {
		if h == 0 || h >= 1010 {
			return changed // set in block 1010 without an event
		}
		return fibretypes.DefaultParams()
	}
	dir := t.TempDir()
	s, err := New(Config{RPCURL: node.srv.URL, DataDir: dir, StartHeight: 995, RPCTimeout: 2 * time.Second}, NewLogger(400))
	if err != nil {
		t.Fatal(err)
	}
	s.chainID = "test-1"
	defer s.store.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	start, err := s.resume(ctx, 1030)
	if err != nil || start != 995 || !s.fibreInactive {
		t.Fatalf("resume at a pre-upgrade height: start=%d inactive=%v err=%v", start, s.fibreInactive, err)
	}
	for h := int64(995); h <= 1004; h++ {
		s.processBlock(ctx, h)
		if ctx.Err() != nil {
			t.Fatalf("the scan stalled at height %d", h)
		}
		if h < 1003 && !s.fibreInactive {
			t.Fatalf("x/fibre called active at the v9 height %d", h)
		}
	}
	if s.fibreInactive {
		t.Fatal("x/fibre still inactive past the upgrade")
	}
	if e := s.params.Entries(); len(e) != 1 || e[0].FromHeight != 1003 {
		t.Fatalf("params history: %+v, want one seed at the first v10 block", e)
	}
	if seeded, at := s.hosts.Seeded(); !seeded || at != 1003 {
		t.Fatalf("host history seeded=%v at=%d, want the first v10 block", seeded, at)
	}
	if host, _ := s.hosts.HostAt(addrA, 1004, 0, nil); host != "a.example:9090" {
		t.Fatalf("host of A at 1004: %q", host)
	}
	if s.lastReconcile != 1002 {
		t.Fatalf("lastReconcile=%d, want the height before the seed", s.lastReconcile)
	}
	// Each pre-upgrade seed try was answered once, not retried.
	if n := node.count("abci_query " + pathFibreParams); n > 10 {
		t.Fatalf("x/fibre params asked %d times across ten blocks", n)
	}

	s.reconcileParams(ctx, 1020)
	raw, err := os.ReadFile(filepath.Join(dir, "param_uncertainty.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 1 {
		t.Fatalf("param_uncertainty.jsonl: %q", raw)
	}
	var u ParamUncertainty
	if err := json.Unmarshal([]byte(lines[0]), &u); err != nil {
		t.Fatal(err)
	}
	if u.Kind != UncertaintySilentChange || u.FromHeight != 1003 || u.ToHeight != 1020 || !u.IntervalStartKnown {
		t.Fatalf("interval: %+v, want 1003-1020 from the seed", u)
	}
	if u.Resolution != ResolutionVerified || u.HeightsRead != 18 || len(u.Values) != 2 || u.Values[1].FromHeight != 1010 {
		t.Fatalf("resolution: %s read=%d values=%+v err=%s", u.Resolution, u.HeightsRead, u.Values, u.ResolveError)
	}
	if got, ok := s.params.EffectiveAt(1015, 0); !ok || got.ShardRetentionSeconds != int64(changed.ShardRetention/time.Second) {
		t.Fatalf("params at 1015 after the verified read: %+v", got)
	}
	if s.lastReconcile != 1020 {
		t.Fatalf("lastReconcile=%d after the check", s.lastReconcile)
	}
}

// CometBFT's wording for a node that discards ABCI responses, and the SDK's
// for pruned state: both are heights this node will never serve.
func TestTheNodesOwnWordingForAHeightItWillNeverServe(t *testing.T) {
	for _, msg := range []string{
		"block_results 5: node is not persisting finalize block responses",
		"abci query /celestia.fibre.v1.Query/Params h=5: code=18 codespace=sdk log=failed to load state at height 5; version does not exist (latest height: 900): invalid request",
	} {
		if !IsHeightUnavailable(errors.New(msg)) {
			t.Errorf("not read as unavailable: %s", msg)
		}
	}
	if !IsResultsNotPersisted(errors.New("node is not persisting finalize block responses")) {
		t.Error("discard_abci_responses wording not recognised")
	}
}
