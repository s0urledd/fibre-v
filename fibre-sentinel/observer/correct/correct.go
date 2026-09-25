package correct

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strconv"
	"time"

	"github.com/plsgiveup/fibre/fibre-sentinel/internal/probe"
	"github.com/plsgiveup/fibre/fibre-sentinel/internal/scan"
	"github.com/plsgiveup/fibre/fibre-sentinel/observer/store"
)

// Package correct applies what a verified params range proves: it moves the
// deadlines the range covers and re-grades the rows drawn against them.
//
// It is a package rather than a file in the collector because the whole
// point of the mechanism is the path from a record to a published figure,
// and a test that reaches into the store and marks a range corrected by
// hand exercises none of it. The first cut of this code had a dead
// corrector — HoldingRanges filtered out exactly the ranges Run looks for,
// so the two never met — and the test passed, because it called
// MarkRangeCorrected itself.

// Corrector turns a verified params range into moved deadlines and
// re-graded verdicts, writing every move to corrections.jsonl before it
// touches the database.
//
// The order is deliberate and the same one judgeLate now uses: the line is
// appended and fsynced, then applied. A line whose correction did not apply
// replays as a no-op, because both Apply*Correction are idempotent on their
// (target, range) key and a rebuild replays this file. A correction applied
// with no line is permanent and silent: the store would carry a deadline
// this observer moved with nothing on record saying why, and the export
// would no longer reproduce it.
type Corrector struct {
	st       *store.Store
	file     *os.File
	pruneTol time.Duration
}

// New builds a Corrector writing its lines to file.
func New(st *store.Store, file *os.File, pruneTol time.Duration) *Corrector {
	return &Corrector{st: st, file: file, pruneTol: pruneTol}
}

func (c *Corrector) append(x store.Correction) error {
	b, err := json.Marshal(x)
	if err != nil {
		return err
	}
	if _, err := c.file.Write(append(b, '\n')); err != nil {
		return err
	}
	return c.file.Sync()
}

// Run applies every verified range that is still withholding. Verifying a
// range says what the deadline should have been; this is what moves it, and
// until it has the range keeps withholding — which is why HoldingRanges
// returns verified ranges at all.
//
// A range is closed only when every publication it covers and every row of
// those publications has been re-derived. Anything the corrector could not
// reach — a publication whose own timestamps will not parse, a row whose
// raw_json the retention pass has stripped — leaves the range open and its
// verdicts withheld. That is the conservative end: a row this observer
// cannot re-derive is one it must not publish a verdict for, and the
// alternative is releasing it under a deadline nothing checked.
func (c *Corrector) Run(ctx context.Context, now time.Time) (int, error) {
	ranges, err := c.st.HoldingRanges(ctx)
	if err != nil {
		return 0, err
	}
	base, err := c.st.ParamHistory(ctx)
	if err != nil {
		return 0, err
	}
	moved := 0
	for _, u := range ranges {
		if u.Resolution != scan.ResolutionVerified {
			continue // still open, or unresolvable: nothing to correct against
		}
		n, unreached, err := c.correctRange(ctx, u, base, now)
		moved += n
		if err != nil {
			return moved, fmt.Errorf("range %s: %w", u.ID, err)
		}
		if unreached > 0 {
			log.Printf("params range %s verified over %d height(s): %d correction(s) applied, but %d target(s) could not be re-derived; "+
				"the range stays open and its obligations stay withheld", u.ID, u.HeightsRead, n, unreached)
			continue
		}
		// The completion line goes to the record before the latch, under
		// the same rule as every other correction: a line whose latch did
		// not land replays as a no-op, a latch with no line would leave a
		// rebuild re-holding rows the live store has released.
		if err := c.append(store.Correction{
			SchemaVersion: store.CorrectionSchemaVersion, Kind: store.CorrectionRangeComplete,
			UncertaintyID: u.ID, JudgedAt: now.UTC(),
			Reason: "every deadline and verdict this range covers has been re-derived against the params read at all " +
				strconv.FormatInt(u.HeightsRead, 10) + " of its heights",
		}); err != nil {
			return moved, err
		}
		if err := c.st.MarkRangeCorrected(ctx, u.ID, now); err != nil {
			return moved, err
		}
		log.Printf("params range %s verified over %d height(s): %d deadline/verdict correction(s); the hold on its publications is lifted",
			u.ID, u.HeightsRead, n)
	}
	n, err := c.sweepStaleRows(ctx, now)
	return moved + n, err
}

// maxSweep bounds one pass's standing work. A publication's rows arrive
// four at a time per validator over the length of its window, so the
// backlog after a correction is bounded by the set still being probed; the
// cap only bites on a first pass over a store that was corrected while the
// collector was down, and the rows it does not reach stay held until the
// next pass.
const maxSweep = 20000

// sweepStaleRows re-grades every row whose deadline disagrees with its
// publication's, whatever range corrected that publication and whenever
// the row arrived.
//
// This is the standing half of the mechanism, and it is what the
// range-keyed pass above cannot do. The prober schedules from
// publications.jsonl, which is append-only and still carries the deadline
// the scanner stamped, so it keeps producing rows against a withdrawn
// deadline for as long as the old window runs — hours after the range was
// closed. Nothing keyed on a range can find those rows, because the range
// is finished with. Nothing keyed on the disagreement can miss them.
//
// They cannot be published in the meantime either: InsertProbe stamps the
// hold in the same statement that writes the row, so a stale row is born
// withheld and this pass is what releases it.
func (c *Corrector) sweepStaleRows(ctx context.Context, now time.Time) (int, error) {
	rows, err := c.st.StaleDeadlineRows(ctx, maxSweep)
	if err != nil {
		return 0, err
	}
	moved, unreached := 0, 0
	for _, r := range rows {
		var m probe.Measurement
		if r.RawJSON == "" || json.Unmarshal([]byte(r.RawJSON), &m) != nil {
			unreached++
			continue
		}
		got := m.RecomputeWith(c.pruneTol, r.Deadline)
		pc := store.Correction{
			SchemaVersion: store.CorrectionSchemaVersion, Kind: store.CorrectionProbeVerdict,
			UncertaintyID: r.UncertaintyID, PromiseHash: r.PromiseHash, DedupeKey: r.DedupeKey,
			ValidatorAddress: r.ValidatorAddress, ScheduledAt: r.ScheduledAt,
			FromPhase: r.Phase, ToPhase: string(got.Phase),
			FromClassification: r.Classification, ToClassification: string(got.Classification),
			FromMustServeUntil: r.MustServeUntil, ToMustServeUntil: r.Deadline,
			PruneToleranceS: int64(c.pruneTol / time.Second),
			Reason:          got.Reason + " (graded against the deadline this publication carries after its params range was verified; the prober still schedules from the deadline on the append-only record)",
			JudgedAt:        now.UTC(),
		}
		if err := c.append(pc); err != nil {
			return moved, err
		}
		if _, err := c.st.ApplyProbeCorrection(pc); err != nil {
			return moved, err
		}
		moved++
	}
	// Decisions recorded against the withdrawn deadline, the same way.
	pts, err := c.st.StaleSampledOutPoints(ctx, maxSweep)
	if err != nil {
		return moved, err
	}
	n, err := c.correctSampledOut(pts, "", time.Time{}, now,
		" (graded against the deadline this publication carries after its params range was verified; the prober still schedules from the deadline on the append-only record)")
	moved += n
	if err != nil {
		return moved, err
	}
	if unreached > 0 {
		log.Printf("params corrections: %d row(s) arriving against a withdrawn deadline could not be re-derived and stay withheld", unreached)
	}
	if moved > 0 {
		log.Printf("params corrections: %d row(s) that arrived against a withdrawn deadline re-graded", moved)
	}
	return moved, nil
}

// correctRange returns how many corrections landed and how many targets it
// could not re-derive. A non-zero second return keeps the range open.
func (c *Corrector) correctRange(ctx context.Context, u scan.ParamUncertainty, base []scan.ParamEntry, now time.Time) (int, int, error) {
	hist := scan.ResolvedHistory(base, []scan.ParamUncertainty{u})
	pubs, err := c.st.PublicationsCoveredBy(ctx, u)
	if err != nil {
		return 0, 0, err
	}
	moved, unreached := 0, 0
	for _, p := range pubs {
		if p.Unreadable {
			unreached++
			continue
		}
		recomputed, _, basis, _, ok := hist.MustServeUntilForPromise(
			p.CreationTimestamp, p.PromiseHeight, p.SettlementHeight, p.SettlementTxIndex)
		if !ok {
			// The history does not reach this promise, so nothing about its
			// deadline is proven and the hold must stand.
			unreached++
			continue
		}
		// Clamped against what the scanner stamped, not against the current
		// value: on a second pass the current value is already the
		// corrected one, and comparing it with itself would report no
		// change and skip the rows below.
		corrected, changed := scan.CorrectedDeadline(p.AsStamped, recomputed)
		if changed && !corrected.Equal(p.MustServeUntil) {
			if err := c.correctPublication(p, u, corrected, basis, now); err != nil {
				return moved, unreached, err
			}
			moved++
		}
		n, missed, err := c.correctRows(ctx, p, u, corrected, now)
		moved += n
		unreached += missed
		if err != nil {
			return moved, unreached, err
		}
	}
	return moved, unreached, nil
}

func (c *Corrector) correctPublication(p store.PublicationInRange, u scan.ParamUncertainty, corrected time.Time, basis string, now time.Time) error {
	dc := store.Correction{
		SchemaVersion: store.CorrectionSchemaVersion, Kind: store.CorrectionPublicationDeadline,
		UncertaintyID: u.ID, PromiseHash: p.PromiseHash,
		FromMustServeUntil: p.MustServeUntil, ToMustServeUntil: corrected,
		FromBasis: p.Basis, ToBasis: basis + "; CORRECTED: recomputed against the params read at every height of " + u.ID,
		Reason:   "x/fibre params were read at every height of " + u.ID + ", and the values in force there give an earlier deadline than the one this publication was recorded with",
		JudgedAt: now.UTC(),
	}
	if err := c.append(dc); err != nil {
		return err
	}
	_, err := c.st.ApplyPublicationCorrection(dc)
	return err
}

// correctRows re-grades every row of one publication that has not already
// been corrected for this range. A row whose own record is gone is counted
// as unreached rather than skipped, so the range stays open and the row
// stays withheld.
func (c *Corrector) correctRows(ctx context.Context, p store.PublicationInRange, u scan.ParamUncertainty, corrected time.Time, now time.Time) (int, int, error) {
	rows, err := c.st.ProbeRowsOf(ctx, p.PromiseHash, u.ID)
	if err != nil {
		return 0, 0, err
	}
	moved, unreached := 0, 0
	for _, r := range rows {
		var m probe.Measurement
		if r.RawJSON == "" || json.Unmarshal([]byte(r.RawJSON), &m) != nil {
			// The row's own record has been stripped by the retention
			// pass, so it cannot be re-graded the way it was graded.
			// A row this observer cannot re-derive is one it must not
			// publish a verdict for, so the range stays open and the
			// row stays withheld.
			unreached++
			continue
		}
		got := m.RecomputeWith(c.pruneTol, corrected)
		// The deadline moving is itself a reason to correct, even when the
		// phase and the classification come out the same. probes
		// .must_serve_until is not decoration: ObligationBuckets cuts the
		// end segment at must_serve_until minus a quarter of the window,
		// and decides pending on must_serve_until against as_of. A row
		// left with a withdrawn deadline puts the end-segment cut in the
		// wrong place, so a HEALTHY reading lands in served or in
		// end_unobserved on the strength of a window this observer no
		// longer stands behind.
		if string(got.Phase) == r.Phase && string(got.Classification) == r.Classification &&
			r.MustServeUntil.Equal(corrected) {
			continue
		}
		pc := store.Correction{
			SchemaVersion: store.CorrectionSchemaVersion, Kind: store.CorrectionProbeVerdict,
			UncertaintyID: u.ID, PromiseHash: p.PromiseHash, DedupeKey: r.DedupeKey,
			ValidatorAddress: r.ValidatorAddress, ScheduledAt: r.ScheduledAt,
			FromPhase: r.Phase, ToPhase: string(got.Phase),
			FromClassification: r.Classification, ToClassification: string(got.Classification),
			FromMustServeUntil: r.MustServeUntil, ToMustServeUntil: corrected,
			PruneToleranceS: int64(c.pruneTol / time.Second),
			Reason:          got.Reason + " (re-graded against the deadline the params read at every height of " + u.ID + " support)",
			JudgedAt:        now.UTC(),
		}
		if err := c.append(pc); err != nil {
			return moved, unreached, err
		}
		if _, err := c.st.ApplyProbeCorrection(pc); err != nil {
			return moved, unreached, err
		}
		moved++
	}
	pts, err := c.st.SampledOutPointsOf(ctx, p.PromiseHash)
	if err != nil {
		return moved, unreached, err
	}
	n, err := c.correctSampledOut(pts, u.ID, corrected, now,
		" (re-graded against the deadline the params read at every height of "+u.ID+" support)")
	return moved + n, unreached, err
}

// correctSampledOut re-grades the points of sampled-out decisions against
// deadline, as correctRows re-grades stored rows: a decision stands for a
// NOT_PROBED row per assigned validator at each point, and those rows' phase
// and deadline move the way a stored row's would. A point already carrying
// what the deadline gives is left alone, so a pass that finds nothing to do
// writes nothing.
func (c *Corrector) correctSampledOut(pts []store.SampledOutPoint, uncertaintyID string, deadline, now time.Time, why string) (int, error) {
	moved := 0
	for _, pt := range pts {
		deadline := deadline
		id := uncertaintyID
		if !pt.Deadline.IsZero() {
			deadline, id = pt.Deadline, pt.UncertaintyID
		}
		row := probe.Measurement{SchemaVersion: probe.MeasurementSchemaVersion, Assigned: true,
			ScheduledAt: pt.ScheduledAt, StartedAt: pt.ScheduledAt, Outcome: probe.OutcomeMissed, Classification: probe.ClassNotProbed}
		got := row.RecomputeWith(c.pruneTol, deadline)
		if string(got.Phase) == pt.Phase && pt.MustServeUntil.Equal(deadline) {
			continue
		}
		sc := store.Correction{
			SchemaVersion: store.CorrectionSchemaVersion, Kind: store.CorrectionSampledOutPoint,
			UncertaintyID: id, PromiseHash: pt.PromiseHash, Vantage: pt.Vantage, DedupeKey: pt.Key(),
			ScheduledAt: pt.ScheduledAt, FromPhase: pt.Phase, ToPhase: string(got.Phase),
			FromClassification: string(probe.ClassNotProbed), ToClassification: string(probe.ClassNotProbed),
			FromMustServeUntil: pt.MustServeUntil, ToMustServeUntil: deadline,
			PruneToleranceS: int64(c.pruneTol / time.Second),
			Reason:          "sampled out: every assigned validator's row at this point" + why,
			JudgedAt:        now.UTC(),
		}
		if err := c.append(sc); err != nil {
			return moved, err
		}
		if _, err := c.st.ApplySampledOutCorrection(sc); err != nil {
			return moved, err
		}
		moved++
	}
	return moved, nil
}
