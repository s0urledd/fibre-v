package probe

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cosmos/cosmos-sdk/types/bech32"
)

func consBech(t *testing.T, hexAddr string) string {
	t.Helper()
	raw, err := hex.DecodeString(hexAddr)
	if err != nil {
		t.Fatal(err)
	}
	s, err := bech32.ConvertAndEncode("celestiavalcons", raw)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// A prober restart used to forget every host it had seen a now-jailed
// validator register. The collector's registry.jsonl is the durable copy;
// the prober replays it, so the fallback survives the restart.
func TestHostRegistry_SeedsLastKnownHostsAcrossARestart(t *testing.T) {
	aa := "aa" + "00000000000000000000000000000000000000"[:38]
	bb := "bb" + "00000000000000000000000000000000000000"[:38]
	t1 := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	t2 := t1.Add(time.Hour)
	t3 := t2.Add(time.Hour)
	path := filepath.Join(t.TempDir(), "registry.jsonl")
	lines := "" +
		`{"kind":"endpoint_opened","validator_cons_address":"` + consBech(t, aa) + `","host":"a.example:9090","height":10,"at":"` + t1.Format(time.RFC3339) + `"}` + "\n" +
		`{"kind":"endpoint_opened","validator_cons_address":"` + consBech(t, bb) + `","host":"b-old.example:9090","height":10,"at":"` + t1.Format(time.RFC3339) + `"}` + "\n" +
		`{"kind":"endpoint_closed","validator_cons_address":"` + consBech(t, bb) + `","host":"b-old.example:9090","height":20,"at":"` + t2.Format(time.RFC3339) + `","reason":"left_bonded_provider_list"}` + "\n" +
		`{"kind":"endpoint_opened","validator_cons_address":"` + consBech(t, bb) + `","host":"b-new.example:9090","height":21,"at":"` + t2.Format(time.RFC3339) + `"}` + "\n" +
		`this line is not json` + "\n" +
		`{"kind":"endpoint_closed","validator_cons_address":"` + consBech(t, bb) + `","host":"b-new.example:9090","height":30,"at":"` + t3.Format(time.RFC3339) + `"}` + "\n" +
		`{"kind":"endpoint_opened","validator_cons_address":"` + consBech(t, aa) + `","host":"partial` // no newline: mid-write
	if err := os.WriteFile(path, []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}

	// a fresh process: nothing in memory
	r := NewResolver(nil, time.Minute)
	reg := newHostRegistry(path)
	applied, skipped, err := reg.refresh(r)
	if err != nil {
		t.Fatal(err)
	}
	if applied != 5 || skipped != 1 {
		t.Fatalf("applied=%d skipped=%d, want 5 and 1", applied, skipped)
	}
	bonded := map[string]string{} // both validators jailed: neither is bonded now
	host, source, at := r.hostFor(bonded, aa)
	if host != "a.example:9090" || source != "last_known" || !at.Equal(t1) {
		t.Fatalf("aa resolved to %q via %q at %s", host, source, at)
	}
	// bb moved hosts and was then jailed: the newest record names the host
	// it was last seen with, confirmed at the time it left the bonded list
	host, source, at = r.hostFor(bonded, bb)
	if host != "b-new.example:9090" || source != "last_known" || !at.Equal(t3) {
		t.Fatalf("bb resolved to %q via %q at %s", host, source, at)
	}

	// the partial line is read once the collector finishes it
	if err := os.WriteFile(path, []byte(lines+`.example:9090","height":40,"at":"`+t3.Add(time.Hour).Format(time.RFC3339)+`"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	applied, skipped, err = reg.refresh(r)
	if err != nil || applied != 1 || skipped != 0 {
		t.Fatalf("second refresh: applied=%d skipped=%d err=%v", applied, skipped, err)
	}
	if host, _, _ := r.hostFor(bonded, aa); host != "partial.example:9090" {
		t.Fatalf("aa after the completed line: %q", host)
	}

	// a live confirmation newer than the registry wins, and an older
	// registry record cannot roll it back
	now := t3.Add(2 * time.Hour)
	r.mu.Lock()
	r.lastKnown[bb] = knownHost{host: "b-live.example:9090", at: now}
	r.mu.Unlock()
	r.seedLastKnown(bb, "b-new.example:9090", t3)
	if host, _, at := r.hostFor(bonded, bb); host != "b-live.example:9090" || !at.Equal(now) {
		t.Fatalf("an older registry record overrode a newer live confirmation: %q at %s", host, at)
	}
}

func TestHostRegistry_MissingFileIsNotAnError(t *testing.T) {
	r := NewResolver(nil, time.Minute)
	reg := newHostRegistry(filepath.Join(t.TempDir(), "absent.jsonl"))
	if applied, skipped, err := reg.refresh(r); err != nil || applied != 0 || skipped != 0 {
		t.Fatalf("applied=%d skipped=%d err=%v", applied, skipped, err)
	}
}
