package probe

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/plsgiveup/fibre/fibre-sentinel/internal/scan"
)

// A chain RPC that answers everything with 503 "server busy" (celestia-core
// v0.42 under max_concurrent_heavy_requests; block is a heavy method, and
// the rest is answered the same way here to be safe). None of the prober's
// chain reads may move on it: the app version and the pin verdict keep
// their previous value, the clock offset keeps its previous value, the
// scanner-frontier mark keeps its previous value. A validator's verdict
// rests on the Fibre handshake and the shard it serves, never on whether
// this observer's own node had a slot free.
func TestAChainThatIsBusyKeepsEveryPreviousReading(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = body
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		fmt.Fprint(w, `{"jsonrpc":"2.0","id":-1,"error":{"code":-32603,"message":"Internal error","data":"server busy: too many concurrent heavy RPC requests, retry later"}}`)
	}))
	defer srv.Close()
	c, err := scan.NewChain(srv.URL, 2*time.Second, scan.NewLogger(20))
	if err != nil {
		t.Fatal(err)
	}
	p := testProber(t)
	p.chain = c
	ctx := context.Background()

	p.observer.AppVersion, p.observer.PinStale = 10, false
	p.pollAppVersion(ctx)
	if p.observer.AppVersion != 10 || p.observer.PinStale {
		t.Fatalf("a busy node moved the app version or the pin verdict: %+v", p.observer)
	}

	p.clockOffset = 3 * time.Second
	p.measureClock(ctx)
	if p.clockOffset != 3*time.Second {
		t.Fatalf("a busy node moved the clock offset: %s", p.clockOffset)
	}

	p.scanned = scannedMark{height: 7, timedFor: 0}
	p.pollScanned(ctx)
	if p.scanned.timedFor != 0 || !p.scanned.at.IsZero() {
		t.Fatalf("a busy node moved the scanner-frontier mark: %+v", p.scanned)
	}
}
