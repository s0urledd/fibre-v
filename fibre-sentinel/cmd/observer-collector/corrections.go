package main

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

// corrector turns a verified params range into moved deadlines and
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
type corrector struct {
	st       *store.Store
	file     *os.File
	pruneTol time.Duration
}

func (c *corrector) append(x store.Correction) error {
	b, err := json.Marshal(x)
	if err != nil {
		return err
	}
	if _, err := c.file.Write(append(b, '\n')); err != nil {
		return err
	}
	return c.file.Sync()
}

// run applies every verified range that still holds. A range that has been
// read at every height is no longer a reason to withhold anything: the
// values it proved are in the history the scanner persisted, so the
// deadline each covered publication should have carried can be recomputed
// and the rows re-graded against it.
func (c *corrector) run(ctx context.Context, now time.Time) (int, error) {
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
			continue // still open, or unresolvable: the hold stands
		}
		n, err := c.correctRange(ctx, u, base, now)
		if err != nil {
			return moved, fmt.Errorf("range %s: %w", u.ID, err)
		}
		moved += n
		if err := c.st.MarkRangeCorrected(ctx, u.ID); err != nil {
			return moved, err
		}
		log.Printf("params range %s verified over %d height(s): %d deadline/verdict correction(s); the hold on its publications is lifted",
			u.ID, u.HeightsRead, n)
	}
	return moved, nil
}

func (c *corrector) correctRange(ctx context.Context, u scan.ParamUncertainty, base []scan.ParamEntry, now time.Time) (int, error) {
	hist := scan.ResolvedHistory(base, []scan.ParamUncertainty{u})
	pubs, err := c.st.PublicationsCoveredBy(ctx, u)
	if err != nil {
		return 0, err
	}
	moved := 0
	for _, p := range pubs {
		recomputed, _, basis, _, ok := hist.MustServeUntilForPromise(
			p.CreationTimestamp, p.PromiseHeight, p.SettlementHeight, p.SettlementTxIndex)
		if !ok {
			continue // the history does not reach this promise; nothing proven
		}
		// Clamped: a verification can withdraw an accusation, never make
		// one. See scan.CorrectedDeadline.
		corrected, changed := scan.CorrectedDeadline(p.MustServeUntil, recomputed)
		if !changed {
			continue
		}
		dc := store.Correction{
			SchemaVersion: store.CorrectionSchemaVersion, Kind: store.CorrectionPublicationDeadline,
			UncertaintyID: u.ID, PromiseHash: p.PromiseHash,
			FromMustServeUntil: p.MustServeUntil, ToMustServeUntil: corrected,
			FromBasis: p.Basis, ToBasis: basis + "; CORRECTED: recomputed against the params read at every height of " + u.ID,
			Reason:   "x/fibre params were read at every height of " + u.ID + ", and the values in force there give an earlier deadline than the one this publication was recorded with",
			JudgedAt: now.UTC(),
		}
		if err := c.append(dc); err != nil {
			return moved, err
		}
		if _, err := c.st.ApplyPublicationCorrection(dc); err != nil {
			return moved, err
		}
		moved++

		rows, err := c.st.ProbeRowsOf(ctx, p.PromiseHash, u.ID)
		if err != nil {
			return moved, err
		}
		for _, r := range rows {
			var m probe.Measurement
			if r.RawJSON == "" || json.Unmarshal([]byte(r.RawJSON), &m) != nil {
				// The row's own record has been stripped by the retention
				// pass, so it cannot be re-graded the way it was graded.
				// Left alone and left held: a row this observer cannot
				// re-derive is one it must not publish a verdict for.
				continue
			}
			got := m.RecomputeWith(c.pruneTol, corrected)
			if string(got.Phase) == r.Phase && string(got.Classification) == r.Classification {
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
				return moved, err
			}
			if _, err := c.st.ApplyProbeCorrection(pc); err != nil {
				return moved, err
			}
			moved++
		}
	}
	return moved, nil
}

// bumpHoldsRevision tells the API that a hold was raised or lifted, or a
// verdict moved. Its cached aggregates run to a thirty-minute TTL, and a
// withheld fault republished for half an hour after the hold landed is the
// accusation the hold exists to stop.
func bumpHoldsRevision(st *store.Store, now time.Time) {
	if err := st.SetMeta(store.MetaParamHoldsRev, strconv.FormatInt(now.UTC().UnixNano(), 10), now); err != nil {
		log.Printf("param holds revision: %v", err)
	}
}
