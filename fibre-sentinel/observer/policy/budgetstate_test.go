package policy

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/plsgiveup/fibre/fibre-sentinel/internal/probe"
)

// newPersisted is newTest with the budget state kept at file.
func newPersisted(t *testing.T, cfg Config, file string) *Policy {
	t.Helper()
	cfg.State.File = file
	p := newTest(t, cfg)
	t.Cleanup(func() {
		p.mu.Lock()
		if p.saveTimer != nil {
			p.saveTimer.Stop()
		}
		p.mu.Unlock()
	})
	return p
}

// A restart does not hand out a fresh budget: the request window, the byte
// window and the transport backoff all carry over, and the spacing counts
// from before the restart.
func TestBudgetAndBackoffSurviveARestart(t *testing.T) {
	file := filepath.Join(t.TempDir(), BudgetStateFile)
	if err := writeAtomic(file, []byte(`{"version":1,"saved_at":"2026-01-01T00:00:00Z","validators":{}}`)); err != nil {
		t.Fatal(err)
	}
	cfg := Default()
	cfg.Caps.PerValidator.MinRequestSpacing = 0
	cfg.Caps.PerValidator.RequestsPerMinute = 3
	cfg.Backoff.SkipDownloadAfter = 2
	p := newPersisted(t, cfg, file)
	now := time.Now()
	pub := pubOf(hexHash(31), now, 1<<20, 148)
	tgt := probe.Target{AddressHex: pub.Assignment.Validators[0].Address, RowCount: 148, Assigned: true}
	fail := probe.Measurement{ValidatorAddress: tgt.AddressHex, AssignedRowCount: 148, StartedAt: now, Outcome: probe.OutcomeTCPTimeout}
	for i := 0; i < 2; i++ {
		if allow, _, reason := p.BeforeProbe(pub, tgt, now); !allow {
			t.Fatalf("probe %d denied: %s", i, reason)
		}
		p.AfterProbe(pub, fail)
	}
	// a third admitted and still in flight when the process dies
	if allow, skip, reason := p.BeforeProbe(pub, tgt, now); !allow || !skip {
		t.Fatalf("third probe: allow=%v skip=%v %s", allow, skip, reason)
	}
	if err := p.Flush(); err != nil {
		t.Fatal(err)
	}

	// the next process
	q := newPersisted(t, cfg, file)
	if allow, _, reason := q.BeforeProbe(pub, tgt, now); allow || reason != "budget:validator_requests_per_minute=3" {
		t.Fatalf("after a restart, a 4th request in the same minute: allow=%v reason=%s (a fresh budget)", allow, reason)
	}
	// next minute: admitted, and still backed off (2 transport failures on record)
	if allow, skip, reason := q.BeforeProbe(pub, tgt, now.Add(61*time.Second)); !allow || !skip {
		t.Fatalf("after a restart: allow=%v skip=%v %s, want admitted without the download (backoff kept)", allow, skip, reason)
	}
	q.mu.Lock()
	vs := q.validators[tgt.AddressHex]
	n, _ := sumSince(vs.events, now.Add(-time.Hour))
	gn, _ := sumSince(q.global, now.Add(-time.Hour))
	q.mu.Unlock()
	if n != 3 || gn != 3 {
		t.Fatalf("restored %d validator and %d global events, want 3 (two accounted, one in flight)", n, gn)
	}

	// the spacing counts from the save, whatever the restored state says
	cfg.Caps.PerValidator.MinRequestSpacing = time.Hour
	r := newPersisted(t, cfg, file)
	r.mu.Lock()
	wait := r.spacingWait("ffff", time.Now())
	r.mu.Unlock()
	if wait < 59*time.Minute {
		t.Fatalf("spacing after a restart for a validator not on record = %s, want it counted from the save", wait)
	}
}

// Bytes carry over too: a restart loop cannot spend the hourly byte cap once
// per process.
func TestTheByteBudgetSurvivesARestart(t *testing.T) {
	file := filepath.Join(t.TempDir(), BudgetStateFile)
	cfg := Default()
	cfg.State.UnknownCooldown = 0 // first start of this data dir
	cfg.Caps.PerValidator.MinRequestSpacing = 0
	cfg.Caps.PerValidator.RequestsPerMinute = 0
	pub := pubOf(hexHash(32), time.Now(), 1<<20, 148)
	tgt := probe.Target{AddressHex: pub.Assignment.Validators[0].Address, RowCount: 148, Assigned: true}
	shard := ShardBytes(pub.Promise.BlobSize, pub.Assignment.ProtocolParams.OriginalRows, 148)
	cfg.Capacity.FloorValidatorBps = int64(float64(shard)*2.5*8/3600/cfg.Caps.PerValidator.BytesPerHourFraction) + 1
	cfg.Caps.PerValidator.BytesPerDayFraction = 1
	admitted := 0
	for restart := 0; restart < 4; restart++ {
		p := newPersisted(t, cfg, file)
		for i := 0; i < 3; i++ {
			now := time.Now()
			if allow, _, _ := p.BeforeProbe(pub, tgt, now); allow {
				admitted++
				p.AfterProbe(pub, probe.Measurement{ValidatorAddress: tgt.AddressHex, AssignedRowCount: 148, StartedAt: now,
					Outcome: probe.OutcomeServedOK, Download: probe.DownloadResult{Attempted: true, RowsExpected: 148}})
			}
		}
		if err := p.Flush(); err != nil {
			t.Fatal(err)
		}
	}
	if admitted != 2 {
		t.Fatalf("4 processes admitted %d shards against an hourly cap of 2.5", admitted)
	}
}

// No trustworthy file — missing, corrupt, another version — is read as "the
// budget may be spent": nothing is admitted for the cool-down, then the
// caps apply as usual, and the file is written again.
func TestAnUnknownBudgetStateStartsWithACoolDown(t *testing.T) {
	cases := map[string]string{
		"missing": "",
		"corrupt": `{"version":1,"saved_at":`,
		"version": `{"version":99,"saved_at":"2026-01-01T00:00:00Z"}`,
		"no time": `{"version":1}`,
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			file := filepath.Join(t.TempDir(), BudgetStateFile)
			if content != "" {
				if err := os.WriteFile(file, []byte(content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			cfg := Default()
			cfg.Caps.PerValidator.MinRequestSpacing = 0
			p := newPersisted(t, cfg, file)
			var logged []string
			p.SetLogger(func(f string, a ...any) { logged = append(logged, f) })
			pub := pubOf(hexHash(33), time.Now(), 1<<20, 148)
			tgt := probe.Target{AddressHex: pub.Assignment.Validators[0].Address, RowCount: 148, Assigned: true}
			if allow, _, reason := p.BeforeProbe(pub, tgt, time.Now()); allow || reason != "budget:state_unknown_cooldown" {
				t.Fatalf("allow=%v reason=%s, want the cool-down", allow, reason)
			}
			if len(logged) == 0 {
				t.Fatal("the cool-down was not logged")
			}
			if allow, _, reason := p.BeforeProbe(pub, tgt, time.Now().Add(11*time.Minute)); !allow {
				t.Fatalf("after the cool-down: %s", reason)
			}
			if err := p.Flush(); err != nil {
				t.Fatal(err)
			}
			if _, err := readBudgetState(file); err != nil {
				t.Fatalf("the state was not written back: %v", err)
			}
		})
	}
	// a cool-down of 0 is off
	cfg := Default()
	cfg.State.UnknownCooldown = 0
	cfg.Caps.PerValidator.MinRequestSpacing = 0
	p := newPersisted(t, cfg, filepath.Join(t.TempDir(), BudgetStateFile))
	pub := pubOf(hexHash(34), time.Now(), 1<<20, 148)
	if allow, _, reason := p.BeforeProbe(pub, probe.Target{AddressHex: pub.Assignment.Validators[0].Address, RowCount: 148}, time.Now()); !allow {
		t.Fatalf("cool-down 0: %s", reason)
	}
}

// A state that cannot be saved closes admission until a save works.
func TestAnUnsavableBudgetStateClosesAdmission(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "not-yet")
	cfg := Default()
	cfg.State.UnknownCooldown = 0
	cfg.Caps.PerValidator.MinRequestSpacing = 0
	p := newPersisted(t, cfg, filepath.Join(dir, BudgetStateFile))
	pub := pubOf(hexHash(35), time.Now(), 1<<20, 148)
	tgt := probe.Target{AddressHex: pub.Assignment.Validators[0].Address, RowCount: 148}
	if allow, _, _ := p.BeforeProbe(pub, tgt, time.Now()); !allow {
		t.Fatal("first probe denied")
	}
	if err := p.Flush(); err == nil {
		t.Fatal("save into a missing directory succeeded")
	}
	if allow, _, reason := p.BeforeProbe(pub, tgt, time.Now()); allow || reason != "budget:state_unsaved" {
		t.Fatalf("with the state unsaved: allow=%v reason=%s", allow, reason)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := p.Flush(); err != nil {
		t.Fatal(err)
	}
	if allow, _, reason := p.BeforeProbe(pub, tgt, time.Now()); !allow {
		t.Fatalf("after a save worked: %s", reason)
	}
}

// Changes are saved on their own, within saveEvery, without a Flush.
func TestTheBudgetStateIsSavedAfterChanges(t *testing.T) {
	old := saveEvery
	saveEvery = 20 * time.Millisecond
	defer func() { saveEvery = old }()
	file := filepath.Join(t.TempDir(), BudgetStateFile)
	cfg := Default()
	cfg.State.UnknownCooldown = 0
	cfg.Caps.PerValidator.MinRequestSpacing = 0
	p := newPersisted(t, cfg, file)
	pub := pubOf(hexHash(36), time.Now(), 1<<20, 148)
	tgt := probe.Target{AddressHex: pub.Assignment.Validators[0].Address, RowCount: 148}
	if allow, _, _ := p.BeforeProbe(pub, tgt, time.Now()); !allow {
		t.Fatal("denied")
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		st, err := readBudgetState(file)
		if err == nil && len(st.Validators[tgt.AddressHex].Minutes) == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("state not saved: %+v %v", st, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	// wait out the in-flight timer so the temp dir can be removed
	if err := p.Flush(); err != nil {
		t.Fatal(err)
	}
}

// Buckets are stamped at their newest probe, so a restored event leaves a
// window no earlier than the probe it stands for; old buckets are dropped.
func TestBudgetBucketsAreStampedAtTheirNewestProbe(t *testing.T) {
	base := time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC)
	got := bucketByMinute([]event{{base.Add(5 * time.Second), 10}, {base.Add(50 * time.Second), 20}, {base.Add(70 * time.Second), 1}})
	if len(got) != 2 || !got[0].At.Equal(base.Add(50*time.Second)) || got[0].N != 2 || got[0].Bytes != 30 || got[1].N != 1 {
		t.Fatalf("buckets: %+v", got)
	}
	b, _ := json.Marshal(budgetState{Version: 1, SavedAt: time.Now(), Validators: map[string]validatorSnapshot{
		"aa": {Minutes: []minuteBucket{{At: time.Now().Add(-25 * time.Hour), N: 5, Bytes: 1}, {At: time.Now().Add(-time.Minute), N: 2, Bytes: 7}}},
	}})
	file := filepath.Join(t.TempDir(), BudgetStateFile)
	if err := os.WriteFile(file, b, 0o600); err != nil {
		t.Fatal(err)
	}
	p := newPersisted(t, Default(), file)
	p.mu.Lock()
	defer p.mu.Unlock()
	n, bytes := sumSince(p.validators["aa"].events, time.Now().Add(-24*time.Hour))
	if n != 2 || bytes != 7 || len(p.global) != 2 {
		t.Fatalf("restored n=%d bytes=%d global=%d, want the day-old bucket dropped", n, bytes, len(p.global))
	}
	if !strings.Contains(strings.Join(p.startupLog, "\n"), "restored 1 validators") {
		t.Fatalf("startup log: %q", p.startupLog)
	}
}

// A process that changes nothing writes nothing: one that could not read the
// file (and sat out the cool-down) leaves it for the next start to be as
// wary of, instead of writing a valid empty state that starts it on a fresh
// budget; one that restored it leaves it exactly as it was.
func TestAnUnchangedBudgetStateIsNotWritten(t *testing.T) {
	cfg := Default()
	cfg.Caps.PerValidator.MinRequestSpacing = 0
	for name, content := range map[string]string{
		"unreadable": `{"version":99,"saved_at":"2026-01-01T00:00:00Z","validators":{"aa":{}}}`,
		"restored":   `{"version":1,"saved_at":"2026-01-01T00:00:00Z","validators":{}}`,
	} {
		t.Run(name, func(t *testing.T) {
			file := filepath.Join(t.TempDir(), BudgetStateFile)
			if err := os.WriteFile(file, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			p := newPersisted(t, cfg, file)
			pub := pubOf(hexHash(37), time.Now(), 1<<20, 148)
			if name == "unreadable" {
				// a probe denied by the cool-down is not a change either
				p.BeforeProbe(pub, probe.Target{AddressHex: pub.Assignment.Validators[0].Address, RowCount: 148}, time.Now())
			}
			if err := p.Flush(); err != nil {
				t.Fatal(err)
			}
			if b, err := os.ReadFile(file); err != nil || string(b) != content {
				t.Fatalf("the file was rewritten without a change: %s %v", b, err)
			}
			if name == "unreadable" {
				q := newPersisted(t, cfg, file)
				if allow, _, reason := q.BeforeProbe(pub, probe.Target{AddressHex: pub.Assignment.Validators[0].Address, RowCount: 148}, time.Now()); allow || reason != "budget:state_unknown_cooldown" {
					t.Fatalf("the next start: allow=%v reason=%s, want the cool-down again", allow, reason)
				}
			}
		})
	}
}
