package export

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAudit_FutureDatedLineStallsTheFile(t *testing.T) {
	data := t.TempDir()
	dir := filepath.Join(data, "exports")
	meas := filepath.Join(data, "measurements.jsonl")
	body := line("started_at", "2026-09-10T10:00:00Z", "a") +
		line("started_at", "2099-01-01T00:00:00Z", "clock-jump") +
		line("started_at", "2026-09-10T10:00:01Z", "b") +
		line("started_at", "2026-09-11T10:00:00Z", "c")
	if err := os.WriteFile(meas, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	b := &Builder{DataDir: data, Dir: dir, Vantage: "v", Build: "x", Hour: 3}
	for _, now := range []time.Time{
		time.Date(2026, 9, 11, 3, 0, 0, 0, time.UTC),
		time.Date(2026, 9, 12, 3, 0, 0, 0, time.UTC),
		time.Date(2026, 9, 13, 3, 0, 0, 0, time.UTC),
		time.Date(2026, 9, 20, 3, 0, 0, 0, time.UTC),
	} {
		built, err := b.Run(now)
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range built {
			m := readTar(t, filepath.Join(dir, name))
			t.Logf("%s measurements member = %q", name, string(m["measurements.jsonl"]))
		}
	}
	st, _ := b.loadState()
	t.Logf("offsets after four builds: %+v (file is %d bytes)", st.Offsets, len(body))
}
