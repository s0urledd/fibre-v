package verdict

import "time"

// FaultSettling is how long a FAULT stays provisional after the probe that
// found it started.
//
// A FAULT can still be taken back automatically for a short while after it
// is written, by evidence that has not reached the store yet. Every such path
// is bounded, and the settling period is the longest of them with margin:
//
//   - The correlated-failure guard (vantage_health.suspect) drops every row
//     at a schedule point once enough of the validators probed there failed
//     together. It is judged over the rows at that point, and the prober may
//     run a slot late by up to MaxLatenessFraction (5%) of the publication's
//     window — twelve minutes on mocha's four-hour shard_retention — so the
//     rest of a point's cohort can land up to ~12 minutes after the fault
//     that opened it. Until then a fault that is one of many at a
//     network-side failure reads as this validator's.
//   - A silent x/fibre params change is noticed at the scanner's next
//     reconcile, at most paramReconcileEvery (60) blocks later — about six
//     minutes at mocha's block time — and the range it opens withholds the
//     rows it covers (RETENTION_UNVERIFIED) in the same transaction that
//     records it. A fault drawn against the stale deadline in between is
//     withdrawn then.
//   - Ingest itself: the collector tails the files every 10 s, and a scanner
//     that fell behind the chain catches up in minutes on a healthy node.
//
// The longest of those is the ~12 minute cohort; twice that, rounded up to
// the half hour, leaves room for a late prober and a slow scan together.
// Longer would hold a fault away from its final word for no evidence that
// can still arrive: the deferred shadow verdicts (amendments) never touch a
// FAULT row — they re-judge PROBE_ERROR rows — and a dispute is a human
// process with no bound, which is what the amendment record is for, not a
// settling period.
//
// Provisional is a label, not a hold. A provisional fault is counted in
// every rate exactly as a final one is, and flagged so a reader knows the
// figure can still move; docs/verdicts.md ("Provisional faults") gives the
// reasons. The figures themselves do not depend on it, which is why
// changing this constant does not change MethodologyVersion.
const FaultSettling = 30 * time.Minute
