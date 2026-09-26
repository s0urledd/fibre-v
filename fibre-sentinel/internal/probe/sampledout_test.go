package probe

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/plsgiveup/fibre/fibre-sentinel/internal/scan"
)

// denyPolicy refuses every publication with a fixed reason, as the load
// policy does when a publication is drawn out of the sample.
type denyPolicy struct {
	reason string
}

func (d denyPolicy) Admit(scan.Publication, bool) (bool, string) { return false, d.reason }
func (denyPolicy) BeforeProbe(scan.Publication, Target, time.Time) (bool, string) {
	return true, ""
}
func (denyPolicy) AfterProbe(scan.Publication, Measurement) {}
func (denyPolicy) Release(scan.Publication, Target)         {}
func (denyPolicy) SamplingFor(scan.Publication) (float64, string, string) {
	return 0.286, "validator_bytes_per_day", "c0ffee"
}
func (denyPolicy) Forget(string) {}

const drawnOut = "budget:p=0.286:validator_bytes_per_day:day_commitment=c0ffee"

func sampledPub(now time.Time) scan.Publication {
	p := pub(now.Add(-2*time.Minute), now.Add(30*time.Minute))
	p.SchemaVersion = scan.AttestationSchemaVersion
	p.PromiseHash = "5a5a5a5a5a5a5a5a5a5a5a5a5a5a5a5a5a5a5a5a5a5a5a5a5a5a5a5a5a5a5a5a"
	p.Promise.ChainID = "chain-1"
	p.Assignment.Validators = []scan.ValidatorAssignment{
		{Address: "aa01", RowCount: 12, Attested: true},
		{Address: "aa02", RowCount: 7, Attested: false},
		{Address: "aa03", RowCount: 3, Attested: true},
	}
	return p
}

func sampledProber(t *testing.T, reason string) *Prober {
	t.Helper()
	p := testProber(t)
	p.cfg.Policy = denyPolicy{reason: reason}
	// Nothing in these tests reaches a chain: a sampled-out publication is
	// recorded from its publication record, and the per-row path below
	// falls back to the record when the resolver cannot be asked.
	ch, err := scan.NewChain("http://127.0.0.1:1", 200*time.Millisecond, p.log)
	if err != nil {
		t.Fatal(err)
	}
	p.resolver = NewResolver(ch, time.Minute)
	return p
}

// A publication the sampler draws out is recorded as one decision, not as a
// NOT_PROBED row per validator per point; a restarted prober reads it back
// and does not put the publication to the policy again.
func TestSampledOutIsRecordedOnce(t *testing.T) {
	p := sampledProber(t, drawnOut)
	now := time.Now().UTC()
	pb := sampledPub(now)
	_, _, _, dropped, _ := p.plan([]scan.Publication{pb}, now)
	if len(dropped) == 0 {
		t.Fatal("the denied publication planned nothing")
	}
	p.recordDropped(context.Background(), dropped)
	if err := p.store.Sync(); err != nil {
		t.Fatal(err)
	}
	ms, err := LoadMeasurements(p.store.Path())
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) != 0 {
		t.Fatalf("a sampled-out publication wrote %d measurement rows", len(ms))
	}
	ds, err := LoadSampledOut(p.sampled.Path())
	if err != nil {
		t.Fatal(err)
	}
	if len(ds) != 1 {
		t.Fatalf("want one decision, got %d", len(ds))
	}
	d := ds[0]
	if d.PromiseHash != pb.PromiseHash || d.Vantage != "v1" || d.Reason != drawnOut || d.Kind != SampledOutKind ||
		d.Sampling.P != 0.286 || d.Sampling.Binding != "validator_bytes_per_day" || d.Sampling.DayCommitment != "c0ffee" {
		t.Fatalf("decision: %+v", d)
	}
	if d.Validators != 3 || len(d.Points) != len(dropped) || len(d.Points) != len(ScheduleFor(pb, p.cfg.Schedule)) {
		t.Fatalf("decision covers %d points x %d validators, dropped %d slots", len(d.Points), d.Validators, len(dropped))
	}

	// The next cycle has nothing left to write for it.
	_, _, _, dropped, finished := p.plan([]scan.Publication{pb}, now)
	if len(dropped) != 0 || len(finished) != 1 {
		t.Fatalf("recorded publication planned again: dropped=%d finished=%v", len(dropped), finished)
	}
	// Nor does a restarted prober, which knows it only from the file.
	so, err := OpenSampledOutStore(p.cfg.DataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer so.Close()
	if !so.Has("v1", pb.PromiseHash) || so.Has("v2", pb.PromiseHash) {
		t.Fatal("reopened store does not know the decision")
	}
	// Appending it again is a no-op: one line per publication.
	if err := so.Append(d); err != nil {
		t.Fatal(err)
	}
	if ds, _ := LoadSampledOut(so.Path()); len(ds) != 1 {
		t.Fatalf("a second append wrote a second decision: %d", len(ds))
	}
}

// The decision expands to exactly the rows the prober used to write for it,
// field for field: that is what lets every figure read the decision where it
// read the rows.
func TestSampledOutExpandsToTheRowsItReplaces(t *testing.T) {
	now := time.Now().UTC()
	pb := sampledPub(now)

	// The rows, the old way: no decision store.
	old := sampledProber(t, drawnOut)
	old.sampled = nil
	_, _, _, dropped, _ := old.plan([]scan.Publication{pb}, now)
	old.recordDropped(context.Background(), dropped)
	if err := old.store.Sync(); err != nil {
		t.Fatal(err)
	}
	rows, err := LoadMeasurements(old.store.Path())
	if err != nil {
		t.Fatal(err)
	}

	// The decision, the new way, expanded.
	nw := sampledProber(t, drawnOut)
	_, _, _, dropped, _ = nw.plan([]scan.Publication{pb}, now)
	nw.recordDropped(context.Background(), dropped)
	ds, err := LoadSampledOut(nw.sampled.Path())
	if err != nil || len(ds) != 1 {
		t.Fatalf("decisions: %v %d", err, len(ds))
	}
	exp := ds[0].Expand(pb)

	if len(rows) != len(exp) || len(rows) != 3*len(ScheduleFor(pb, nw.cfg.Schedule)) {
		t.Fatalf("old path wrote %d rows, the decision expands to %d", len(rows), len(exp))
	}
	key := func(ms []Measurement) {
		sort.Slice(ms, func(i, j int) bool { return ms[i].DedupeKey() < ms[j].DedupeKey() })
	}
	key(rows)
	key(exp)
	for i := range rows {
		a, b := rows[i], exp[i]
		// The old rows were stamped one by one and, with the chain out of
		// reach here, said so in their reason; neither is part of what the
		// decision records.
		a.StartedAt, a.FinishedAt, b.StartedAt, b.FinishedAt = time.Time{}, time.Time{}, time.Time{}, time.Time{}
		a.ClassificationReason, b.ClassificationReason = "", ""
		a.MustServeUntil, b.MustServeUntil = a.MustServeUntil.UTC(), b.MustServeUntil.UTC()
		if a.DedupeKey() != b.DedupeKey() || a.Phase != b.Phase || a.ScheduleLabel != b.ScheduleLabel ||
			a.Assigned != b.Assigned || a.Attested != b.Attested || a.HasAttestation() != b.HasAttestation() ||
			a.AssignedRowCount != b.AssignedRowCount || a.Outcome != b.Outcome || a.Classification != b.Classification ||
			!a.MustServeUntil.Equal(b.MustServeUntil) || a.ValidatorSetHeight != b.ValidatorSetHeight ||
			*a.Sampling != *b.Sampling || a.Commitment != b.Commitment {
			t.Fatalf("row %d differs:\n old %+v\n new %+v", i, a, b)
		}
	}
	if exp[0].ClassificationReason != drawnOut {
		t.Fatalf("expanded reason %q", exp[0].ClassificationReason)
	}

	// A validator the record lists with no rows is not assigned, and the
	// prober never had it as a target.
	pb.Assignment.Validators = append(pb.Assignment.Validators, scan.ValidatorAssignment{Address: "aa04"})
	if n := len(ds[0].Expand(pb)); n != len(exp) {
		t.Fatalf("a validator with no rows was expanded: %d rows", n)
	}
}

// Only a whole publication drawn out of the sample is collapsed. A refusal
// with any other reason, or a prober that also probes unassigned validators
// (targets the publication record does not list), keeps its rows.
func TestSampledOutOnlyForAWholePublicationDraw(t *testing.T) {
	now := time.Now().UTC()
	for _, c := range []struct {
		name       string
		reason     string
		unassigned bool
	}{
		{"not a draw", "budget:state_unknown_cooldown", false},
		{"unassigned targets", drawnOut, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			p := sampledProber(t, c.reason)
			p.cfg.IncludeUnassigned = c.unassigned
			pb := sampledPub(now)
			_, _, _, dropped, _ := p.plan([]scan.Publication{pb}, now)
			p.recordDropped(context.Background(), dropped)
			if err := p.store.Sync(); err != nil {
				t.Fatal(err)
			}
			ms, _ := LoadMeasurements(p.store.Path())
			if len(ms) == 0 {
				t.Fatal("no rows written")
			}
			if b, err := os.ReadFile(filepath.Join(p.cfg.DataDir, SampledOutFile)); err != nil || len(b) != 0 {
				t.Fatalf("a decision was recorded: %q %v", b, err)
			}
		})
	}
}

func TestIsSampledOutRow(t *testing.T) {
	for _, c := range []struct {
		cls    Classification
		reason string
		want   bool
	}{
		{ClassNotProbed, drawnOut, true},
		{ClassNotProbed, "budget:validator_bytes_per_day", false},
		{ClassNotProbed, "scheduled point elapsed before the prober ran it", false},
		{ClassHealthy, drawnOut, false},
	} {
		if got := IsSampledOutRow(Measurement{Classification: c.cls, ClassificationReason: c.reason}); got != c.want {
			t.Errorf("%s %q: got %v", c.cls, c.reason, got)
		}
	}
}
