package scan

import (
	"context"
	"os"
	"testing"
	"time"
)

// An opt-in check against a real endpoint, skipped unless FIBRE_LIVE_RPC is
// set, because CI has no business depending on somebody else's node:
//
//	FIBRE_LIVE_RPC=https://rpc-mocha.pops.one go test ./internal/scan/ -run Live
//
// It exists because the failure it catches is invisible to every offline test.
// CometBFT's default HTTP client builds its own Transport with no Proxy
// function, so it ignores HTTPS_PROXY and dials straight out; on a host whose
// only egress is a proxy, every call came back "Forbidden" — which reads as the
// chain refusing us rather than as a proxy nobody told the client about. Run
// this from wherever the observer will actually live before trusting it there.
func TestLiveChainReach(t *testing.T) {
	url := os.Getenv("FIBRE_LIVE_RPC")
	if url == "" {
		t.Skip("set FIBRE_LIVE_RPC to run")
	}
	c, err := NewChain(url, 20*time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	av, err := c.AppVersion(ctx)
	t.Logf("app_version=%d err=%v", av, err)
	id, h, err := c.Status(ctx)
	t.Logf("chain=%s height=%d err=%v", id, h, err)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
}
