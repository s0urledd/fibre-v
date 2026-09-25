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
	pass := newHostingPass(st, dir, "test", logf)
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

// The city file is added to a host that already has the ASN file: the pass
// must start using it without a restart.
func TestHostingPassPicksUpCityFileLater(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOSTING_ASN_DB", "")
	t.Setenv("HOSTING_COUNTRY_DB", "")
	t.Setenv("HOSTING_CITY_DB", "")
	st, err := store.Open(filepath.Join(dir, "observer.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	gz := func(s string) []byte {
		var b bytes.Buffer
		w := gzip.NewWriter(&b)
		_, _ = w.Write([]byte(s))
		_ = w.Close()
		return b.Bytes()
	}
	hd := filepath.Join(dir, "hosting")
	if err := os.MkdirAll(hd, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hd, hosting.DefaultASNFile), gz("1.0.0.0\t1.0.0.255\t13335\tUS\tCLOUDFLARENET\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var logs []string
	logf := func(f string, a ...any) { mu.Lock(); logs = append(logs, fmt.Sprintf(f, a...)); mu.Unlock() }
	pass := newHostingPass(st, dir, "test", logf)
	pass(context.Background(), time.Now())
	if err := os.WriteFile(filepath.Join(hd, hosting.DefaultCityFile), gz("1.0.0.0,1.0.0.255,OC,AU,Queensland,\"South Brisbane\",-27.4767,153.017\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	pass(context.Background(), time.Now().Add(time.Minute))
	mu.Lock()
	defer mu.Unlock()
	joined := strings.Join(logs, "\n")
	if !strings.Contains(joined, "city \""+filepath.Join(hd, hosting.DefaultCityFile)+"\"") {
		t.Fatalf("the pass never picked up the city file; logs:\n%s", joined)
	}
	if src, err := hosting.ReadSources(context.Background(), st.DB()); err != nil || src.City == nil {
		t.Fatalf("city source after the file appeared: %+v %v", src, err)
	}
}
