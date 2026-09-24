package api_test

import (
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/plsgiveup/fibre/fibre-sentinel/internal/status"
	"github.com/plsgiveup/fibre/fibre-sentinel/observer/api"
	"github.com/plsgiveup/fibre/fibre-sentinel/observer/store"
)

// The prober's run row only ever carries its start: it writes JSONL, never
// the database. /v1/meta said "prober": {"alive": false} beside a components
// entry saying it was running. The status file decides both now.
func TestMetaProberAliveFollowsItsStatusFile(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "observer.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	old := store.TS(time.Now().Add(-3 * time.Hour))
	if _, err := st.DB().Exec(`INSERT INTO observer_runs (component, vantage, version, started_at, last_heartbeat_at) VALUES ('prober','test','t',?,?)`, old, old); err != nil {
		t.Fatal(err)
	}
	w := status.New(dir, "prober", "test", "t")
	w.Start()
	w.OK()
	defer w.Stop("test")
	time.Sleep(1200 * time.Millisecond) // the delayed status write

	srv := api.NewWithVantage(st, api.VantageInfo{Name: "test"}, nil, api.WithDataDir(dir))
	ts := httptest.NewServer(srv)
	defer func() { ts.Close(); srv.Close() }()
	var meta struct {
		Prober *struct {
			Alive bool `json:"alive"`
		} `json:"prober"`
	}
	if code := getAny(t, ts, "/v1/meta", &meta); code != 200 {
		t.Fatalf("meta: %d", code)
	}
	if meta.Prober == nil || !meta.Prober.Alive {
		t.Fatalf("a running prober reported %+v", meta.Prober)
	}
}
