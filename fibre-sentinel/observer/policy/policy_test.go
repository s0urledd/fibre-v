package policy

import (
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
	if binding != "global_bytes_per_hour" {
		t.Fatalf("binding cap = %s", binding)
	}
	if prob > 0.6 || prob < 0.3 {
		t.Fatalf("p = %.3f, want roughly 0.4", prob)
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
