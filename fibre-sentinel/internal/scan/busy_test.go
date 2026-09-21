package scan

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// busyRPC is a node saturated on heavy requests, as celestia-core v0.42
// answers one: the first `busy` calls of every heavy method get HTTP 503
// with the JSON-RPC error the server writes for it, and the calls after
// that get a real answer. Exactly what max_concurrent_heavy_requests does
// under load, so the observer's reading of it is tested against the wire
// shape and not a paraphrase.
const busyText = "server busy: too many concurrent heavy RPC requests, retry later"

func busyRPC(t *testing.T, busy int) (*httptest.Server, func() int) {
	t.Helper()
	var mu sync.Mutex
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
		mu.Lock()
		defer mu.Unlock()
		calls++
		w.Header().Set("Content-Type", "application/json")
		if calls <= busy {
			// rpc/jsonrpc/server/http_uri_handler.go: WriteRPCResponseHTTPError(w,
			// http.StatusServiceUnavailable, RPCInternalError(id, errHeavyRequestLimit))
			w.WriteHeader(http.StatusServiceUnavailable)
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"error":{"code":-32603,"message":"Internal error","data":%q}}`, req.ID, busyText)
			return
		}
		switch req.Method {
		case "block_results":
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":{"height":"%v","txs_results":[],"finalize_block_events":[],"validator_updates":[],"consensus_param_updates":null,"app_hash":""}}`, req.ID, req.Params["height"])
		case "status":
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":{"node_info":{"network":"test-1","moniker":"fake","protocol_version":{"p2p":"8","block":"11","app":"9"},"id":"","listen_addr":"","version":"","channels":"","other":{"tx_index":"on","rpc_address":""}},"sync_info":{"latest_block_hash":"","latest_app_hash":"","latest_block_height":"5","latest_block_time":"2026-09-21T00:00:00Z","earliest_block_hash":"","earliest_app_hash":"","earliest_block_height":"1","earliest_block_time":"2026-09-21T00:00:00Z","catching_up":false},"validator_info":{"address":"","pub_key":null,"voting_power":"0"}}}`, req.ID)
		default:
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"error":{"code":-32601,"message":"Method not found"}}`, req.ID)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, func() int { mu.Lock(); defer mu.Unlock(); return calls }
}

// A node that is saturated on heavy requests (celestia-core v0.42,
// max_concurrent_heavy_requests) answers block_results with 503 and "server
// busy". That is neither a height the node lacks nor a module that is not
// there: the scanner must read it as transient, retry it with backoff until
// a slot frees, and never turn it into a gap — a gap says a block is
// unknown to this observer for good, and these blocks are not.
func TestHeavyRequestBusyIsRetriedWithBackoffAndIsNeverAGap(t *testing.T) {
	srv, calls := busyRPC(t, 2)
	c, err := NewChain(srv.URL, 5*time.Second, NewLogger(20))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	s := &Scanner{log: NewLogger(20), chain: c}

	// the raw answer, as the client surfaces it, and how the scanner reads it
	_, err = c.BlockResults(ctx, 5)
	if err == nil || !strings.Contains(err.Error(), busyText) || !strings.Contains(err.Error(), "503") {
		t.Fatalf("want the 503 busy error through the client, got %v", err)
	}
	if IsHeightUnavailable(err) {
		t.Fatalf("busy read as a height the node does not have: %v", err)
	}
	if IsModuleInactive(err) {
		t.Fatalf("busy read as an absent module: %v", err)
	}
	if s.recordGap(5, err, time.Time{}) || len(s.gaps) != 0 {
		t.Fatalf("busy became a gap: %+v", s.gaps)
	}

	// retried with backoff until the node has a slot; the block is read in
	// full and nothing was skipped
	start := time.Now()
	var res *BlockResults
	err = s.retryRPCAt(ctx, "fetch block_results 5", 5, func() error {
		var e error
		res, e = c.BlockResults(ctx, 5)
		return e
	})
	if err != nil || res == nil || res.Height != 5 {
		t.Fatalf("after the node freed a slot: res=%+v err=%v", res, err)
	}
	if n := calls(); n != 3 {
		t.Fatalf("want 3 calls (one busy taken above, one busy retried, one served), got %d", n)
	}
	if waited := time.Since(start); waited < rpcBackoff(0) {
		t.Fatalf("retried without backoff: %s", waited)
	}
	if len(s.gaps) != 0 {
		t.Fatalf("a gap was recorded across a busy answer: %+v", s.gaps)
	}
	if s.unavailableRun != 0 {
		t.Fatalf("busy counted toward the unavailable run: %d", s.unavailableRun)
	}
}
