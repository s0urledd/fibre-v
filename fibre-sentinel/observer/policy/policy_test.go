package policy

import (
	"fmt"
	"testing"
	"time"

	"github.com/plsgiveup/fibre/fibre-sentinel/internal/probe"
	"github.com/plsgiveup/fibre/fibre-sentinel/internal/scan"
)

func newTest(t *testing.T, cfg Config) *Policy {
	t.Helper()
	p, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	p.master = []byte("fixed-test-master-secret-32-bytes!!")
	return p
}

func pubOf(hash string, settled time.Time, blobSize uint32, rows ...int) scan.Publication {
	var pub scan.Publication
	pub.PromiseHash = hash
	pub.SettlementTime = settled
	pub.Promise.BlobSize = blobSize
	pub.Assignment.ProtocolParams.OriginalRows = 4096
	pub.Assignment.ProtocolParams.TotalRows = 16384
	for i, r := range rows {
		pub.Assignment.Validators = append(pub.Assignment.Validators, scan.ValidatorAssignment{
			Address: string(rune('a'+i)) + "0000000000000000000000000000000000000000"[1:], RowCount: r,
		})
	}
	return pub
}

func TestShardBytesMatchesR4(t *testing.T) {
	// R4 section 1.3: 128 MiB blob, 148 rows -> 4,986,836 B; 4096 rows -> 136,265,732 B.
	if got := ShardBytes(128<<20, 4096, 148); got != 4_986_836 {
		t.Fatalf("floor shard bytes = %d", got)
	}
	if got := ShardBytes(128<<20, 4096, 4096); got != 136_265_732 {
		t.Fatalf("max shard bytes = %d", got)
	}
	if got := ShardBytes(256<<10, 4096, 148); got != 146_644 {
		t.Fatalf("256 KiB floor shard bytes = %d", got)
	}
}

func TestSamplingIsDeterministicAndUnbiased(t *testing.T) {
	p := newTest(t, Default())
	day := time.Date(2026, 9, 8, 10, 0, 0, 0, time.UTC)
	in := 0
	const n = 4000
	for i := 0; i < n; i++ {
		h := hexHash(i)
		a := p.Sampled(h, day, 0.4)
		b := p.Sampled(h, day, 0.4)
		if a != b {
			t.Fatalf("non-deterministic decision for %s", h)
		}
		if a {
			in++
		}
	}
	frac := float64(in) / n
	if frac < 0.36 || frac > 0.44 {
		t.Fatalf("sampled fraction %.3f, want about 0.40", frac)
	}
	if !p.Sampled(hexHash(1), day, 1) || p.Sampled(hexHash(1), day, 0) {
		t.Fatal("p=1 must admit, p=0 must reject")
	}
	// a different day secret gives a different sample.
	other := day.Add(24 * time.Hour)
	diff := 0
	for i := 0; i < 200; i++ {
		if p.Sampled(hexHash(i), day, 0.5) != p.Sampled(hexHash(i), other, 0.5) {
			diff++
		}
	}
	if diff == 0 {
		t.Fatal("day secret has no effect on the sample")
	}
	if p.DayCommitment(day) == p.DayCommitment(other) {
		t.Fatal("day commitments must differ")
	}
}

func hexHash(i int) string {
	const hexdigits = "0123456789abcdef"
	b := make([]byte, 64)
	for j := range b {
		b[j] = hexdigits[(i>>(uint(j%8)*4)+j)&15]
	}
	return string(b)
}

func TestAdmitEverythingUnderBudget(t *testing.T) {
	p := newTest(t, Default())
	now := time.Now()
	for i := 0; i < 50; i++ {
		pub := pubOf(hexHash(i), now.Add(-time.Duration(i)*time.Minute), 1<<20, 148, 3035, 3186)
		if ok, reason := p.Admit(pub, false); !ok {
			t.Fatalf("1 MiB blob %d rejected: %s", i, reason)
		}
	}
	if prob, _ := p.State(); prob != 1 {
		t.Fatalf("p = %.3f, want 1", prob)
	}
}

func TestAdmitSamplesWhenGlobalCapBinds(t *testing.T) {
	cfg := Default()
	cfg.Caps.Global.BytesPerHour = 50 << 30
	p := newTest(t, cfg)
	now := time.Now()
	// Scenario A from R4: 60 × 128 MiB blobs in the last hour over 100
	// validators (Σ rows ≈ 12,288) needs ~125 GB/h; the 50 GB/h cap binds
	// at p ≈ 0.4.
	rows := make([]int, 0, 100)
	for i := 0; i < 100; i++ {
		rows = append(rows, 123)
	}
	admitted := 0
	for i := 0; i < 60; i++ {
		pub := pubOf(hexHash(1000+i), now.Add(-time.Duration(i)*time.Minute), 128<<20, rows...)
		if ok, _ := p.Admit(pub, false); ok {
			admitted++
		}
	}
	prob, binding := p.State()
	// The global DAILY cap binds first, not the hourly one. 600 GiB/day over
	// 50 GiB/h is twelve hours of headroom, so a load sustained for a full
	// day runs out of daily budget at half the hourly rate. The sampler has
	// to see that when it picks p, or the day's later probes get denied one
	// by one after their publications were already admitted — and because
	// the schedule is packed toward the deadline, the points lost are the
	// late in-window and grace ones, which is where a breach shows.
	if binding != "global_bytes_per_day" {
		t.Fatalf("binding cap = %s, want the daily cap: it is tighter than the hourly one at sustained load", binding)
	}
	if prob > 0.4 || prob <= 0 {
		t.Fatalf("p = %.3f, want tighter than the hourly cap's ~0.4", prob)
	}
	if admitted == 0 || admitted == 60 {
		t.Fatalf("admitted %d of 60", admitted)
	}
	// sticky: asking again returns the same answers.
	for i := 0; i < 60; i++ {
		pub := pubOf(hexHash(1000+i), now.Add(-time.Duration(i)*time.Minute), 128<<20, rows...)
		ok1, _ := p.Admit(pub, false)
		ok2, _ := p.Admit(pub, false)
		if ok1 != ok2 {
			t.Fatal("decision changed between calls")
		}
	}
	// a started publication is always admitted.
	pub := pubOf(hexHash(9999), now, 128<<20, rows...)
	if ok, _ := p.Admit(pub, true); !ok {
		t.Fatal("already-started publication must be admitted")
	}
}

func TestBudgetAndBackoff(t *testing.T) {
	cfg := Default()
	cfg.Caps.PerValidator.MinRequestSpacing = 0
	cfg.Caps.PerValidator.RequestsPerMinute = 3
	cfg.Backoff.SkipDownloadAfter = 2
	p := newTest(t, cfg)
	now := time.Now()
	pub := pubOf(hexHash(7), now, 1<<20, 148)
	tgt := probe.Target{AddressHex: pub.Assignment.Validators[0].Address, RowCount: 148, Assigned: true}

	m := probe.Measurement{ValidatorAddress: tgt.AddressHex, AssignedRowCount: 148, StartedAt: now, Outcome: probe.OutcomeTCPRefused}
	for i := 0; i < 3; i++ {
		allow, skip, reason := p.BeforeProbe(pub, tgt, now)
		if !allow {
			t.Fatalf("probe %d denied: %s", i, reason)
		}
		if i >= 2 && !skip {
			t.Fatalf("probe %d: expected download skip after 2 transport failures", i)
		}
		p.AfterProbe(pub, m)
	}
	if allow, _, reason := p.BeforeProbe(pub, tgt, now); allow || reason != "budget:validator_requests_per_minute=3" {
		t.Fatalf("4th probe within a minute: allow=%v reason=%s", allow, reason)
	}
	// a success clears the backoff.
	ok := probe.Measurement{ValidatorAddress: tgt.AddressHex, StartedAt: now.Add(2 * time.Minute), Outcome: probe.OutcomeServedOK,
		Download: probe.DownloadResult{Attempted: true, RowsReturned: 148}}
	p.AfterProbe(pub, ok)
	if _, skip, _ := p.BeforeProbe(pub, tgt, now.Add(3*time.Minute)); skip {
		t.Fatal("backoff should clear after a success")
	}
	// byte cap: a 128 MiB blob's floor shard is ~5 MB; cap is 2.9 GB/h, so
	// ~585 downloads fit; force the cap low instead.
	cfg2 := Default()
	cfg2.Caps.PerValidator.MinRequestSpacing = 0
	cfg2.Caps.PerValidator.RequestsPerMinute = 0
	cfg2.Capacity.FloorValidatorBps = 8 * 1024 // 1 KiB/s -> 36 KB/h at 1%
	p2 := newTest(t, cfg2)
	big := pubOf(hexHash(8), now, 128<<20, 148)
	if allow, _, reason := p2.BeforeProbe(big, tgt, now); allow {
		t.Fatalf("5 MB shard should exceed a 36 KB/h cap; reason=%s", reason)
	}
}

// The shipped YAML files must load, and the caps they compute must be the
// numbers their own comments (and R4) promise validators.
func TestShippedPoliciesLoad(t *testing.T) {
	cases := []struct {
		file    string
		perHour int64
		perDay  int64
	}{
		{"policy.example.yaml", 2_925_000_000, 52_650_000_000},
		{"policy.mocha.yaml", 47_700_000, 858_600_000},
	}
	for _, c := range cases {
		cfg, err := Load(c.file)
		if err != nil {
			t.Fatalf("%s: %v", c.file, err)
		}
		if err := cfg.validate(); err != nil {
			t.Fatalf("%s: %v", c.file, err)
		}
		if got := cfg.bytesPerHourCap(cfg.Capacity.FloorRows); got != c.perHour {
			t.Errorf("%s: per-hour cap at floor rows = %d, want %d", c.file, got, c.perHour)
		}
		if got := cfg.bytesPerDayCap(cfg.Capacity.FloorRows); got != c.perDay {
			t.Errorf("%s: per-day cap at floor rows = %d, want %d", c.file, got, c.perDay)
		}
	}
}

func TestValidateRejectsBadConfig(t *testing.T) {
	bad := []struct {
		name  string
		mutfn func(*Config)
	}{
		{"zero day fraction", func(c *Config) { c.Caps.PerValidator.BytesPerDayFraction = 0 }},
		{"zero global day", func(c *Config) { c.Caps.Global.BytesPerDay = 0 }},
		{"negative requests", func(c *Config) { c.Caps.PerValidator.RequestsPerMinute = -1 }},
		{"negative spacing", func(c *Config) { c.Caps.PerValidator.MinRequestSpacing = -time.Second }},
		{"zero lookback", func(c *Config) { c.Sampling.ProjectionLookback = 0 }},
		{"negative skip window", func(c *Config) { c.Backoff.SkipWindow = -time.Minute }},
		{"zero floor rows", func(c *Config) { c.Capacity.FloorRows = 0 }},
	}
	for _, b := range bad {
		cfg := Default()
		b.mutfn(&cfg)
		if err := cfg.validate(); err == nil {
			t.Errorf("%s: validate accepted it", b.name)
		}
	}
	if err := Default().validate(); err != nil {
		t.Fatalf("the defaults must validate: %v", err)
	}
}

// The sticky admit/deny map must stay bounded, and eviction must drop the
// publications whose windows closed longest ago. It used to empty the whole
// map, which re-decided every publication still in flight at whatever
// admission probability the load happened to give — admitting some points of
// a schedule whose earlier points were denied.
func TestDecisionsAreBoundedAndEvictOldestFirst(t *testing.T) {
	p := newTest(t, Default())
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	hash := func(i int) string { return fmt.Sprintf("%08x", i) }
	for i := 0; i < maxDecisions+1000; i++ {
		p.remember(scan.Publication{PromiseHash: hash(i), SettlementTime: base.Add(time.Duration(i) * time.Second)}, i%2 == 0)
	}
	if len(p.decisions) >= maxDecisions {
		t.Fatalf("decisions grew to %d, bound is %d", len(p.decisions), maxDecisions)
	}
	// the newest publication is always still known
	newest := hash(maxDecisions + 999)
	if _, ok := p.decisions[newest]; !ok {
		t.Fatalf("the newest publication was evicted")
	}
	// and the ones that survived are newer than the ones that did not
	oldestKept := base.Add(time.Duration(maxDecisions+1000) * time.Second)
	for h, d := range p.decisions {
		if d.at.Before(oldestKept) {
			oldestKept = d.at
		}
		if d.at.IsZero() {
			t.Fatalf("%s kept with no settlement time", h)
		}
	}
	if _, ok := p.decisions[hash(0)]; ok {
		t.Fatalf("the oldest publication survived eviction while newer ones were dropped")
	}
	// a decision, once made, never changes while it is remembered
	pub := scan.Publication{PromiseHash: newest, SettlementTime: base}
	first, _ := p.Admit(pub, false)
	for i := 0; i < 5; i++ {
		if again, _ := p.Admit(pub, false); again != first {
			t.Fatalf("Admit flipped from %v to %v for a publication still in the map", first, again)
		}
	}
	// Forget releases it
	p.Forget(newest)
	if _, ok := p.decisions[newest]; ok {
		t.Fatalf("Forget did not drop the decision")
	}
}
