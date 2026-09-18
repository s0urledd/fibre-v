// Package verdict derives the published figures from rows alone, with no
// database: the correlated-failure guard (suspect points) and the
// obligation buckets, over probe rows and publications as they sit in the
// JSONL record or a daily export. It is the second implementation of the
// rules the API evaluates in SQL, kept deliberately apart from it, so a
// third party holding the raw rows can reproduce every figure the site
// prints and the two can be checked against each other
// (sentinel-recompute; the API's tests run both over the same rows).
package verdict

import (
	"sort"
	"time"

	"github.com/plsgiveup/fibre/fibre-sentinel/internal/probe"
)

// The correlated-failure guard. At or above UnreachableThreshold of the
// validators probed at one schedule point being unreachable, or
// FaultThreshold of them faulting, the likeliest explanation is the
// observer's own side (its network, a stale pin, a broken coder) rather
// than that many independent operators at the same minute; such a point
// is suspect and every rate leaves its rows out. MinValidators is the
// floor under which a share is not a signal.
const (
	UnreachableThreshold = 0.5
	FaultThreshold       = 0.5
	MinValidators        = 3
)

// Row is what the rules need from one probe row.
type Row struct {
	PromiseHash    string
	Validator      string
	ScheduleLabel  string
	ScheduledAt    time.Time
	StartedAt      time.Time
	MustServeUntil time.Time
	Assigned       bool
	// Attested is the row's own attestation flag; false when unknown, as
	// the store's NULL is not 1.
	Attested       bool
	Phase          probe.Phase
	Classification probe.Classification
	TLSOK          bool
}

// FromMeasurement reduces a measurement to a Row.
func FromMeasurement(m probe.Measurement) Row {
	return Row{
		PromiseHash: m.PromiseHash, Validator: m.ValidatorAddress, ScheduleLabel: m.ScheduleLabel,
		ScheduledAt: m.ScheduledAt, StartedAt: m.StartedAt, MustServeUntil: m.MustServeUntil,
		Assigned: m.Assigned, Attested: m.Attested && m.HasAttestation(),
		Phase: m.Phase, Classification: m.Classification, TLSOK: m.TLS.OK,
	}
}

// Window bounds a computation: rows started in [Start, End] and
// publications settled in it; End is also the as-of moment that decides
// pending. Start is ignored when All is set.
type Window struct {
	Start time.Time
	End   time.Time
	All   bool
}

func (w Window) holds(t time.Time) bool {
	if t.After(w.End) {
		return false
	}
	return w.All || !t.Before(w.Start)
}

// SuspectPoint is one schedule point the rates leave out.
type SuspectPoint struct {
	At          time.Time
	Label       string
	Validators  int
	Unreachable int
	Faulted     int
	Rows        int
	Reason      string
}

// SuspectPoints applies the correlated-failure guard: over assigned
// in-window rows started in the window, grouped by scheduled time, with
// more than one validator probed at the point.
func SuspectPoints(rows []Row, w Window) []SuspectPoint {
	type acc struct {
		label                  string
		vals, unreach, faulted map[string]bool
		n                      int
	}
	groups := map[time.Time]*acc{}
	for _, r := range rows {
		if !r.Assigned || r.Phase != probe.PhaseInWindow || !w.holds(r.StartedAt) {
			continue
		}
		k := r.ScheduledAt.UTC()
		g, ok := groups[k]
		if !ok {
			g = &acc{label: r.ScheduleLabel, vals: map[string]bool{}, unreach: map[string]bool{}, faulted: map[string]bool{}}
			groups[k] = g
		}
		g.n++
		g.vals[r.Validator] = true
		switch r.Classification {
		case probe.ClassUnreachable:
			g.unreach[r.Validator] = true
		case probe.ClassFault:
			g.faulted[r.Validator] = true
		}
	}
	var out []SuspectPoint
	for at, g := range groups {
		all := len(g.vals)
		if all <= 1 {
			continue
		}
		bad, faulted := len(g.unreach), len(g.faulted)
		reason := ""
		if float64(bad)/float64(all) >= UnreachableThreshold && bad >= MinValidators {
			reason = "unreachable"
		}
		if float64(faulted)/float64(all) >= FaultThreshold && faulted >= MinValidators {
			if reason != "" {
				reason += ","
			}
			reason += "fault"
		}
		if reason == "" {
			continue
		}
		out = append(out, SuspectPoint{At: at, Label: g.label, Validators: all, Unreachable: bad, Faulted: faulted, Rows: g.n, Reason: reason})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	return out
}

// Obligations are the buckets one validator's (or the network's) proven
// obligations in a window fall into. See docs/verdicts.md, "Obligations".
type Obligations struct {
	Total                 int64 `json:"total"`
	Served                int64 `json:"served"`
	Broken                int64 `json:"broken"`
	EndUnobserved         int64 `json:"end_unobserved"`
	Unobserved            int64 `json:"unobserved"`
	UnobservedReachable   int64 `json:"unobserved_reachable"`
	UnobservedUnreachable int64 `json:"unobserved_unreachable"`
	UnobservedNotProbed   int64 `json:"unobserved_not_probed"`
	Pending               int64 `json:"pending"`
}

func (o *Obligations) add(x Obligations) {
	o.Total += x.Total
	o.Served += x.Served
	o.Broken += x.Broken
	o.EndUnobserved += x.EndUnobserved
	o.Unobserved += x.Unobserved
	o.UnobservedReachable += x.UnobservedReachable
	o.UnobservedUnreachable += x.UnobservedUnreachable
	o.UnobservedNotProbed += x.UnobservedNotProbed
	o.Pending += x.Pending
}

// Rate is served over served plus broken; ok is false with nothing rated.
func (o Obligations) Rate() (v float64, ok bool) {
	if o.Served+o.Broken == 0 {
		return 0, false
	}
	return float64(o.Served) / float64(o.Served+o.Broken), true
}

func isGap(c probe.Classification) bool {
	return c == probe.ClassNotProbed || c == probe.ClassProbeError
}

// ComputeObligations buckets every proven obligation: an assigned,
// attested (validator, promise) pair whose promise settled in the window,
// judged over its in-window rows started by the window's end, at schedule
// points that are not suspect. settled maps promise hash to settlement
// time; a row whose promise is not in it is left out, as the SQL join
// leaves it out. The network total is the sum over validators.
func ComputeObligations(rows []Row, settled map[string]time.Time, w Window, suspect []SuspectPoint) (Obligations, map[string]Obligations) {
	sus := map[time.Time]bool{}
	for _, p := range suspect {
		sus[p.At.UTC()] = true
	}
	type key struct{ validator, promise string }
	type obl struct {
		faults, healthy, attempted, reached int64
		pending                             bool
		last                                *Row
	}
	obls := map[key]*obl{}
	for i := range rows {
		r := &rows[i]
		st, ok := settled[r.PromiseHash]
		if !ok || !w.holds(st) || r.StartedAt.After(w.End) {
			continue
		}
		if !r.Assigned || !r.Attested || r.Phase != probe.PhaseInWindow || sus[r.ScheduledAt.UTC()] {
			continue
		}
		k := key{r.Validator, r.PromiseHash}
		o, ok := obls[k]
		if !ok {
			o = &obl{}
			obls[k] = o
		}
		switch r.Classification {
		case probe.ClassFault:
			o.faults++
		case probe.ClassHealthy:
			o.healthy++
		}
		if !isGap(r.Classification) {
			o.attempted++
			if r.TLSOK {
				o.reached++
			}
		}
		if r.MustServeUntil.After(w.End) {
			o.pending = true
		}
		// the newest row: a verdict row before a gap row, then the latest
		// schedule point, then the latest start
		if o.last == nil || newer(r, o.last) {
			o.last = r
		}
	}
	byVal := map[string]Obligations{}
	var net Obligations
	for k, o := range obls {
		var b Obligations
		b.Total = 1
		last := o.last.Classification
		switch {
		case o.pending:
			b.Pending = 1
		case o.faults > 0:
			b.Broken = 1
		case last == probe.ClassHealthy:
			b.Served = 1
		case o.healthy > 0:
			b.EndUnobserved = 1
		case o.reached > 0:
			b.UnobservedReachable = 1
		case o.attempted > 0:
			b.UnobservedUnreachable = 1
		default:
			b.UnobservedNotProbed = 1
		}
		b.Unobserved = b.UnobservedReachable + b.UnobservedUnreachable + b.UnobservedNotProbed
		v := byVal[k.validator]
		v.add(b)
		byVal[k.validator] = v
		net.add(b)
	}
	return net, byVal
}

// newer orders rows as the API's ROW_NUMBER does: a gap row (NOT_PROBED,
// PROBE_ERROR) never outranks a verdict row; then the later schedule
// point; then the later start.
func newer(a, b *Row) bool {
	ga, gb := isGap(a.Classification), isGap(b.Classification)
	if ga != gb {
		return !ga
	}
	if !a.ScheduledAt.Equal(b.ScheduledAt) {
		return a.ScheduledAt.After(b.ScheduledAt)
	}
	return a.StartedAt.After(b.StartedAt)
}
