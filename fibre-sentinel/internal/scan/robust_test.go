package scan

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadPublications_TornTailIgnored(t *testing.T) {
	path := filepath.Join(t.TempDir(), "publications.jsonl")
	os.WriteFile(path, []byte(`{"promise_hash":"aa"}`+"\n"+`{"promise_hash":"bb","settle`), 0o644)
	pubs, err := LoadPublications(path)
	if err != nil || len(pubs) != 1 || pubs[0].PromiseHash != "aa" {
		t.Fatalf("pubs=%+v err=%v", pubs, err)
	}
	// a malformed COMPLETE line is still a hard error
	os.WriteFile(path, []byte(`{"promise_hash":"aa"}`+"\n"+`{bad}`+"\n"), 0o644)
	if _, err := LoadPublications(path); err == nil {
		t.Fatal("malformed complete line must error")
	}
}

func TestOpenStore_RepairsTornTail(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "publications.jsonl")
	os.WriteFile(path, []byte(`{"promise_hash":"aa","settlement_tx_hash":"t1"}`+"\n"+`{"promise_hash":"bb","settlem`), 0o644)
	st, err := OpenStore(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()
	if !st.Seen("t1") {
		t.Fatal("intact record not loaded")
	}
	b, _ := os.ReadFile(path)
	if string(b) != `{"promise_hash":"aa","settlement_tx_hash":"t1"}`+"\n" {
		t.Fatalf("torn tail not truncated: %q", b)
	}
	if err := st.AppendPublication(Publication{PromiseHash: "cc", SettlementTxHash: "t2"}); err != nil {
		t.Fatal(err)
	}
	if err := st.Sync(); err != nil {
		t.Fatal(err)
	}
	pubs, err := LoadPublications(path)
	if err != nil || len(pubs) != 2 {
		t.Fatalf("after append: %d pubs, err %v", len(pubs), err)
	}
	if err := st.SaveState(PersistState{ChainID: "x", LastScannedHeight: 7}); err != nil {
		t.Fatal(err)
	}
	got, err := st.LoadState()
	if err != nil || got == nil || got.LastScannedHeight != 7 {
		t.Fatalf("state: %+v err=%v", got, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "state.json.tmp")); !os.IsNotExist(err) {
		t.Fatal("temp state file left behind")
	}
}

func TestIsModuleInactive_ByCode(t *testing.T) {
	if !IsModuleInactive(&ABCIError{Code: 6, Codespace: "sdk", Log: "unknown request: ..."}) {
		t.Fatal("sdk/6 should be inactive")
	}
	if IsModuleInactive(&ABCIError{Code: 3, Codespace: "sdk", Log: "invalid request"}) {
		t.Fatal("sdk/3 is not inactive")
	}
	if !IsModuleInactive(errors.New("rpc error: unknown query path")) {
		t.Fatal("text match lost")
	}
	if !IsResultsNotPersisted(errors.New("block_results 5: rpc: finalize block responses not persisted")) {
		t.Fatal("not-persisted detection")
	}
}

func TestRetryRPC(t *testing.T) {
	s := &Scanner{log: NewLogger(10)}
	calls := 0
	err := s.retryRPC(context.Background(), "test", func() error {
		calls++
		if calls < 2 {
			return errors.New("block_results 9: not found")
		}
		return nil
	})
	if err != nil || calls != 2 {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
	// a height the node does not have is retried for the grace period and
	// then reported as unavailable, typed, so the scanner can record a gap
	// and move on instead of exiting.
	prev := unavailableGrace
	unavailableGrace = 0
	defer func() { unavailableGrace = prev }()
	calls = 0
	err = s.retryRPCAt(context.Background(), "test", 9, func() error {
		calls++
		return errors.New("finalize block responses not persisted")
	})
	var ue *ErrHeightUnavailable
	if !errors.As(err, &ue) || ue.Height != 9 || calls != 1 {
		t.Fatalf("not persisted: err=%v calls=%d", err, calls)
	}
	if !s.recordGap(9, err) || !s.recordGap(10, err) || len(s.gaps) != 1 || s.gaps[0].From != 9 || s.gaps[0].To != 10 {
		t.Fatalf("gaps not merged: %+v", s.gaps)
	}
	if s.recordGap(11, errors.New("boom")) {
		t.Fatal("a transient error must never become a gap")
	}
	err = s.retryRPCAt(context.Background(), "test", 12, func() error {
		return errors.New("height 12 is not available, lowest height is 500")
	})
	if !errors.As(err, &ue) {
		t.Fatalf("pruned: %v", err)
	}
	// a cancelled context stops at once
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	calls = 0
	start := time.Now()
	err = s.retryRPC(ctx, "test", func() error { calls++; return errors.New("boom") })
	if err == nil || calls != 1 || time.Since(start) > time.Second {
		t.Fatalf("cancelled: err=%v calls=%d", err, calls)
	}
}
