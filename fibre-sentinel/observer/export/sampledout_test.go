package export

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A sampled-out publication's one decision is in the export of the day it
// was decided, like the rows it stands for would have been.
func TestBuilder_CarriesSampledOutDecisions(t *testing.T) {
	data := t.TempDir()
	dir := filepath.Join(data, "exports")
	d1, d2 := "2026-09-10", "2026-09-11"
	so := line("decided_at", d1+"T10:00:00Z", "a") + line("decided_at", d2+"T01:00:00Z", "b")
	if err := os.WriteFile(filepath.Join(data, "sampling_decisions.jsonl"), []byte(so), 0o644); err != nil {
		t.Fatal(err)
	}
	b := &Builder{DataDir: data, Dir: dir, Vantage: "v", Build: "abc", Hour: 3}
	built, err := b.Run(time.Date(2026, 9, 11, 4, 0, 0, 0, time.UTC))
	if err != nil || len(built) != 1 {
		t.Fatalf("built %v err %v", built, err)
	}
	members := readTar(t, filepath.Join(dir, built[0]))
	if got := string(members["sampling_decisions.jsonl"]); got != line("decided_at", d1+"T10:00:00Z", "a") {
		t.Fatalf("decisions member: %q", got)
	}
	var man Manifest
	if err := json.Unmarshal(members["manifest.json"], &man); err != nil {
		t.Fatal(err)
	}
	for _, m := range man.Files {
		if m.Name == "sampling_decisions.jsonl" {
			if m.Lines != 1 || m.TimeField != "decided_at" {
				t.Fatalf("manifest member %+v", m)
			}
			return
		}
	}
	t.Fatal("the manifest does not list sampling_decisions.jsonl")
}
