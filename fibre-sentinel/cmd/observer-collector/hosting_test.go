package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/plsgiveup/fibre/fibre-sentinel/observer/hosting"
	"github.com/plsgiveup/fibre/fibre-sentinel/observer/store"
)

// The deploy downloads the hosting databases after the new collector has
// started. The pass must notice them on a later poll, not only at start:
// on the first deploy it stayed off until someone restarted the collector.
func TestHostingPassPicksUpDatabasesThatArriveAfterStart(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "observer.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	var mu sync.Mutex
	var logs []string
	logf := func(f string, a ...any) { mu.Lock(); logs = append(logs, fmt.Sprintf(f, a...)); mu.Unlock() }
	pass := newHostingPass(st, dir, logf)
	pass(context.Background(), time.Now())

	var b bytes.Buffer
	w := gzip.NewWriter(&b)
	_, _ = w.Write([]byte("1.0.0.0\t1.0.0.255\t13335\tUS\tCLOUDFLARENET\n"))
	_ = w.Close()
	if err := os.MkdirAll(filepath.Join(dir, "hosting"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "hosting", hosting.DefaultASNFile), b.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	pass(context.Background(), time.Now().Add(time.Minute))

	mu.Lock()
	defer mu.Unlock()
	joined := strings.Join(logs, "\n")
	if !strings.Contains(joined, "appeared") {
		t.Fatalf("the pass never noticed the database written after start; logs:\n%s", joined)
	}
}
