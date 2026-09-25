package ingest_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/plsgiveup/fibre/fibre-sentinel/observer/ingest"
	"github.com/plsgiveup/fibre/fibre-sentinel/observer/store"
)

// confirmLine is one row as a confirming vantage's sentinel-probe writes it.
func confirmLine(vantage, addr string, minute int) string {
	at := time.Date(2026, 9, 25, 10, minute, 0, 0, time.UTC).Format(time.RFC3339)
	return fmt.Sprintf(`{"vantage":%q,"promise_hash":"ph","validator_address":%q,"validator_host":"h:7980","assigned":true,"attested":true,"schedule_label":"w2","scheduled_at":%q,"started_at":%q,"phase":"in_window","outcome":"NOT_FOUND","classification":"FAULT","download":{"rows_expected":2}}`,
		vantage, addr, at, at)
}

func countConfirmations(t *testing.T, st *store.Store) (confirmations, probes int) {
	t.Helper()
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM probe_confirmations`).Scan(&confirmations); err != nil {
		t.Fatal(err)
	}
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM probes`).Scan(&probes); err != nil {
		t.Fatal(err)
	}
	return confirmations, probes
}

func ingestConfirmations(t *testing.T, st *store.Store, dir string) (inserted, skipped int64) {
	t.Helper()
	files, err := ingest.VantageMeasurementFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		r, err := ingest.VantageMeasurements(st, f, "ut-1", time.Now())
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		inserted += r.Inserted
		skipped += r.Skipped
	}
	return inserted, skipped
}

// A second vantage's measurements.jsonl is tailed like every copied file: a
// half-copied last line waits, a copy that grew reads only what is new, a
// re-copy from scratch inserts nothing twice, and a row carrying this
// observer's own vantage is stepped over. The rows land in
// probe_confirmations, keyed to the fault they answer, and never in probes.
func TestVantageMeasurementsAreTailedIntoConfirmations(t *testing.T) {
	st := openStore(t)
	vdir := filepath.Join(t.TempDir(), ingest.VantagesDir)
	if files, err := ingest.VantageMeasurementFiles(vdir); err != nil || len(files) != 0 {
		t.Fatalf("missing dir: %v %v", files, err)
	}
	f := filepath.Join(vdir, "de-1", "measurements.jsonl")
	os.MkdirAll(filepath.Dir(f), 0o755)
	third := confirmLine("de-1", "aa", 10)
	os.WriteFile(f, []byte(confirmLine("de-1", "aa", 0)+"\n"+confirmLine("de-1", "bb", 0)+"\n"+third[:50]), 0o644)
	if ins, _ := ingestConfirmations(t, st, vdir); ins != 2 {
		t.Fatalf("first copy inserted %d, want 2 (the partial line waits)", ins)
	}
	// the copy completes and grows by a row claiming this observer's name
	os.WriteFile(f, []byte(confirmLine("de-1", "aa", 0)+"\n"+confirmLine("de-1", "bb", 0)+"\n"+third+"\n"+confirmLine("ut-1", "cc", 0)+"\n"), 0o644)
	ins, skipped := ingestConfirmations(t, st, vdir)
	if ins != 1 || skipped != 1 {
		t.Fatalf("second pass inserted %d, skipped %d; want 1 and 1 (own vantage's name)", ins, skipped)
	}
	// replaced by a fresh, shorter copy: read again, nothing twice
	os.WriteFile(f, []byte(confirmLine("de-1", "aa", 0)+"\n"), 0o644)
	if ins, _ := ingestConfirmations(t, st, vdir); ins != 0 {
		t.Fatalf("a re-copy inserted %d rows again", ins)
	}
	c, p := countConfirmations(t, st)
	if c != 3 || p != 0 {
		t.Fatalf("confirmations %d, probes %d; want 3 and 0", c, p)
	}
	var key string
	if err := st.DB().QueryRow(`SELECT probe_key FROM probe_confirmations WHERE validator_address = 'aa' AND scheduled_at LIKE '2026-09-25T10:00%'`).Scan(&key); err != nil {
		t.Fatal(err)
	}
	if want := "ut-1|ph|aa|2026-09-25T10:00:00Z"; key != want {
		t.Errorf("probe_key = %q, want this observer's own key for the slot %q", key, want)
	}
}
