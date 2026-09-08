package probe

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/plsgiveup/fibre/fibre-sentinel/internal/scan"
)

// Config controls a prober run.
type Config struct {
	RPCURL           string
	PublicationsPath string // publications.jsonl written by the scanner
	DataDir          string // where measurements.jsonl lives
	Vantage          string // this prober's vantage-point name

	Schedule ScheduleConfig
	Timeouts StepTimeouts

	IncludeUnassigned bool

	// Policy, if set, decides which publications are probed (sampling), which
	// probes fit the byte and request budgets, and when to back off. nil
	// means probe everything, the pre-policy behaviour.
	Policy Policy

	// run mode
	Once     bool          // probe everything due right now, then exit
	Drain    bool          // run until every known publication's schedule is fully in the past
	Deadline time.Duration // whole-run wall-clock cap (0 = none)

	// pacing (every wait is bounded by MaxSleep)
	MaxSleep     time.Duration // longest sleep between cycles
	MinSleep     time.Duration // shortest, to avoid a busy loop
	MaxLateness  time.Duration // a slot older than this is recorded MISSED, not probed
	RPCTimeout   time.Duration
	HostCacheTTL time.Duration
}

func (c Config) withDefaults() Config {
	if c.MaxSleep <= 0 {
		c.MaxSleep = 30 * time.Second
	}
	if c.MinSleep <= 0 {
		c.MinSleep = time.Second
	}
	if c.MaxLateness <= 0 {
		c.MaxLateness = 90 * time.Second
	}
	if c.RPCTimeout <= 0 {
		c.RPCTimeout = 15 * time.Second
	}
	if c.HostCacheTTL <= 0 {
		c.HostCacheTTL = 60 * time.Second
	}
	if c.Vantage == "" {
		c.Vantage = "local"
	}
	return c
}

// Prober turns the scanner's publications into scheduled probes and raw
// measurements. The pending-probe queue is never persisted — it is re-derived
// from publications.jsonl + measurements.jsonl every cycle, so a restart
// resumes exactly.
type Prober struct {
	cfg      Config
	log      *scan.Logger
	chain    *scan.Chain
	resolver *Resolver
	store    *MeasurementStore
	chainID  string

	coders map[[2]int]*Coder // keyed by (originalRows, totalRows)
}

// New builds a Prober.
func New(cfg Config, log *scan.Logger) (*Prober, error) {
	cfg = cfg.withDefaults()
	ch, err := scan.NewChain(cfg.RPCURL, cfg.RPCTimeout, log)
	if err != nil {
		return nil, err
	}
	st, err := OpenMeasurementStore(cfg.DataDir)
	if err != nil {
		return nil, err
	}
	return &Prober{
		cfg:      cfg,
		log:      log,
		chain:    ch,
		resolver: NewResolver(ch, cfg.HostCacheTTL),
		store:    st,
		coders:   map[[2]int]*Coder{},
	}, nil
}

func (p *Prober) schedCfg() ScheduleConfig { return p.cfg.Schedule.withDefaults() }

func (p *Prober) coderFor(originalRows, totalRows int) (*Coder, error) {
	k := [2]int{originalRows, totalRows}
	if c, ok := p.coders[k]; ok {
		return c, nil
	}
	c, err := NewCoder(originalRows, totalRows)
	if err != nil {
		return nil, err
	}
	p.coders[k] = c
	return c, nil
}

// Run executes the prober.
func (p *Prober) Run(parent context.Context) error {
	ctx := parent
	if p.cfg.Deadline > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(parent, p.cfg.Deadline)
		defer cancel()
	}
	defer p.store.Close()

	id, tip, err := p.chain.Status(ctx)
	if err != nil {
		p.log.Fatalf("initial status: %v", err)
	}
	p.chainID = id
	p.log.Printf("prober up: vantage=%s chain_id=%s tip=%d rpc=%s pubs=%s data=%s",
		p.cfg.Vantage, id, tip, p.cfg.RPCURL, p.cfg.PublicationsPath, p.store.Path())

	probed := 0
	for {
		if err := ctx.Err(); err != nil {
			if errors.Is(err, context.Canceled) {
				p.log.Printf("stopped (signal): %d probes this run", probed)
				return nil
			}
			p.log.Fatalf("run deadline hit: %v", err)
		}

		pubs, err := scan.LoadPublications(p.cfg.PublicationsPath)
		if err != nil {
			p.log.Fatalf("load publications: %v", err)
		}

		now := time.Now()
		due, future, missed, dropped := p.plan(pubs, now)

		for _, mj := range missed {
			p.recordNotProbed(mj, "scheduled point elapsed before the prober ran it")
		}
		for _, d := range dropped {
			p.recordNotProbed(d.job, d.reason)
		}

		if len(due) > 0 {
			probed += p.runDue(ctx, due)
		}

		if p.cfg.Once {
			p.log.Printf("done (--once): %d probes, %d missed slots", probed, len(missed))
			return nil
		}

		if len(future) == 0 {
			if p.cfg.Drain {
				p.log.Printf("done (--drain): every known schedule is in the past; %d probes this run", probed)
				return nil
			}
			// follow mode: nothing to do; poll for new publications.
			if !p.sleep(ctx, p.cfg.MaxSleep) {
				p.log.Printf("stopped (signal): %d probes this run", probed)
				return nil
			}
			continue
		}

		wait := time.Until(future[0].point.At)
		if wait > p.cfg.MaxSleep {
			wait = p.cfg.MaxSleep
		}
		if wait < p.cfg.MinSleep {
			wait = p.cfg.MinSleep
		}
		if !p.sleep(ctx, wait) {
			p.log.Printf("stopped (signal): %d probes this run", probed)
			return nil
		}
	}
}

func (p *Prober) sleep(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}

// job is one (publication, schedule point, validator) probe slot.
type job struct {
	pub   scan.Publication
	point SchedulePoint
}

// Policy is the prober's load policy hook (see observer/policy).
type Policy interface {
	// Admit decides whether a publication is probed at all. alreadyStarted
	// is true when any of its points was already handled, in which case the
	// decision must stay "yes" so a schedule is never half-recorded.
	Admit(pub scan.Publication, alreadyStarted bool) (ok bool, reason string)
	// BeforeProbe is asked right before one probe. It may deny it (recorded
	// as NOT_PROBED with reason) or ask for L1-L3 only.
	BeforeProbe(pub scan.Publication, t Target, now time.Time) (allow, skipDownload bool, reason string)
	// AfterProbe accounts the bytes and requests a probe consumed.
	AfterProbe(pub scan.Publication, m Measurement)
}

// skipped is a slot the policy decided not to probe.
type skipped struct {
	job    job
	reason string
}

// plan splits every not-yet-recorded schedule point into due (probe now),
// future (sleep until), and missed (record a MISSED marker). "Recorded" is
// per-validator, so plan works at publication+point granularity and runDue
// expands to validators.
func (p *Prober) plan(pubs []scan.Publication, now time.Time) (due, future, missed []job, dropped []skipped) {
	for _, pub := range pubs {
		if pub.Assignment.Error != "" {
			continue // no assignment table -> nothing to probe
		}
		points := ScheduleFor(pub, p.cfg.Schedule)
		var pending []SchedulePoint
		started := false
		for _, pt := range points {
			if p.store.HandledPoint(p.cfg.Vantage, pub.PromiseHash, pt.At) {
				started = true
				continue // already probed / marked for every target
			}
			pending = append(pending, pt)
		}
		if len(pending) == 0 {
			continue
		}
		if p.cfg.Policy != nil {
			if ok, reason := p.cfg.Policy.Admit(pub, started); !ok {
				for _, pt := range pending {
					dropped = append(dropped, skipped{job{pub, pt}, reason})
				}
				continue
			}
		}
		for _, pt := range pending {
			switch {
			case now.Before(pt.At):
				future = append(future, job{pub, pt})
			case now.Sub(pt.At) <= p.cfg.MaxLateness:
				due = append(due, job{pub, pt})
			default:
				missed = append(missed, job{pub, pt})
			}
		}
	}
	sort.Slice(future, func(i, j int) bool { return future[i].point.At.Before(future[j].point.At) })
	sort.Slice(due, func(i, j int) bool { return due[i].point.At.Before(due[j].point.At) })
	return due, future, missed, dropped
}

// runDue probes every due slot. Slots are grouped by publication so the
// validator set / host registry is resolved once per group. A group whose
// targets cannot be resolved is left for the next cycle (not marked, so it
// retries until it either succeeds or ages into MISSED).
func (p *Prober) runDue(ctx context.Context, due []job) int {
	byPub := map[string][]job{}
	order := []string{}
	for _, j := range due {
		if _, ok := byPub[j.pub.PromiseHash]; !ok {
			order = append(order, j.pub.PromiseHash)
		}
		byPub[j.pub.PromiseHash] = append(byPub[j.pub.PromiseHash], j)
	}

	n := 0
	for _, ph := range order {
		jobs := byPub[ph]
		pub := jobs[0].pub

		targets, err := p.resolver.TargetsFor(ctx, pub, p.cfg.IncludeUnassigned)
		if err != nil {
			p.log.Printf("resolve targets for %s: %v (retry next cycle)", short(ph), err)
			continue
		}
		coder, cerr := p.coderFor(pub.Assignment.ProtocolParams.OriginalRows, pub.Assignment.ProtocolParams.TotalRows)
		if cerr != nil {
			p.log.Printf("coder for %s: %v (retry next cycle)", short(ph), cerr)
			continue
		}
		var commitment [32]byte
		cb, _ := hex.DecodeString(pub.Promise.Commitment)
		copy(commitment[:], cb)

		for _, j := range jobs {
			for _, t := range targets {
				if ctx.Err() != nil {
					return n // stopping; leave the rest for the next run
				}
				if p.store.Has(p.cfg.Vantage, ph, t.AddressHex, j.point.At) {
					continue
				}
				skipDL := false
				if p.cfg.Policy != nil {
					allow, skip, reason := p.cfg.Policy.BeforeProbe(pub, t, time.Now())
					if !allow {
						p.recordNotProbedTarget(pub, j.point, t, reason)
						continue
					}
					skipDL = skip
				}
				in := Input{
					Vantage:            p.cfg.Vantage,
					ChainID:            p.chainID,
					PromiseHash:        ph,
					Commitment:         commitment,
					CommitmentHex:      pub.Promise.Commitment,
					BlobVersion:        pub.Promise.BlobVersion,
					MustServeUntil:     pub.MustServeUntil,
					ValidatorSetHeight: pub.Assignment.ValidatorSetHeight,
					Target:             t,
					SchedulePoint:      j.point,
					PruneTolerance:     p.schedCfg().PruneTolerance,
					SkipDownload:       skipDL,
				}
				m := Run(ctx, in, coder, p.cfg.Timeouts)
				if err := p.store.Append(m); err != nil {
					p.log.Fatalf("append measurement: %v", err)
				}
				if p.cfg.Policy != nil {
					p.cfg.Policy.AfterProbe(pub, m)
				}
				n++
				p.logMeasurement(m)
			}
		}
	}
	return n
}

// recordNotProbed marks one (publication, point) slot NOT_PROBED for every
// target, with the given reason (elapsed, or a policy decision).
func (p *Prober) recordNotProbed(j job, reason string) {
	pub := j.pub
	targets, err := p.resolver.TargetsFor(context.Background(), pub, p.cfg.IncludeUnassigned)
	if err != nil {
		// cannot resolve targets for the slot: record one bare marker
		// against the publication so the slot is not retried forever.
		m := Measurement{
			SchemaVersion: MeasurementSchemaVersion, Vantage: p.cfg.Vantage,
			PromiseHash: pub.PromiseHash, Commitment: pub.Promise.Commitment,
			MustServeUntil: pub.MustServeUntil, ScheduleLabel: j.point.Label,
			ScheduledAt: j.point.At.UTC(), StartedAt: time.Now().UTC(), FinishedAt: time.Now().UTC(),
			Phase: PhaseAt(j.point.At, pub, p.cfg.Schedule), Outcome: OutcomeMissed,
			Classification: ClassNotProbed, ClassificationReason: reason + "; targets unresolved: " + err.Error(),
		}
		_ = p.store.Append(m)
		return
	}
	for _, t := range targets {
		p.recordNotProbedTarget(pub, j.point, t, reason)
	}
}

// recordNotProbedTarget writes one NOT_PROBED measurement for a single target.
func (p *Prober) recordNotProbedTarget(pub scan.Publication, pt SchedulePoint, t Target, reason string) {
	if p.store.Has(p.cfg.Vantage, pub.PromiseHash, t.AddressHex, pt.At) {
		return
	}
	m := Measurement{
		SchemaVersion: MeasurementSchemaVersion, Vantage: p.cfg.Vantage,
		PromiseHash: pub.PromiseHash, Commitment: pub.Promise.Commitment,
		BlobVersion: pub.Promise.BlobVersion, MustServeUntil: pub.MustServeUntil,
		ValidatorSetHeight: pub.Assignment.ValidatorSetHeight,
		ValidatorAddress:   t.AddressHex, ValidatorHost: t.Host,
		Assigned: t.Assigned, AssignedRowCount: t.RowCount,
		ScheduleLabel: pt.Label, ScheduledAt: pt.At.UTC(),
		StartedAt: time.Now().UTC(), FinishedAt: time.Now().UTC(),
		Phase: PhaseAt(pt.At, pub, p.cfg.Schedule), Outcome: OutcomeMissed,
		Classification: ClassNotProbed, ClassificationReason: reason,
	}
	if err := p.store.Append(m); err != nil {
		p.log.Fatalf("append not-probed measurement: %v", err)
	}
	p.logMeasurement(m)
}

func (p *Prober) logMeasurement(m Measurement) {
	tail := ""
	if m.Download.Attempted {
		tail = fmt.Sprintf(" rows=%d/%d commit=%v assign=%v", m.Download.RowsReturned, m.Download.RowsExpected, m.Download.CommitmentVerified, m.Download.AssignmentVerified)
	}
	p.log.Printf("PROBE %s val=%s %s[%s] assigned=%v phase=%s -> %s / %s (%dms lat=%dms)%s",
		short(m.PromiseHash), short(m.ValidatorAddress), m.ScheduleLabel, m.Phase, m.Assigned,
		m.Phase, m.Outcome, m.Classification, m.TotalDurationMS, m.LatenessMS, tail)
	if m.Classification == ClassFault {
		p.log.Printf("  FAULT reason: %s | raw: %s", m.ClassificationReason, truncate(m.RawError, 160))
	}
}

func short(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
