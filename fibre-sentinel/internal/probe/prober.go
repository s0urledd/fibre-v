package probe

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
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

	// Concurrency is how many probes run at once across all validators (R4
	// section 3.5: global 8). A single validator never sees more than one
	// connection from this vantage at a time, whatever this value is.
	Concurrency int

	// BackfillMissed bounds how far back a (re)started prober writes
	// NOT_PROBED markers for slots it never ran. Older slots are simply left
	// without a row: the gap is just as visible, and a fresh prober pointed at
	// a data directory with days of history does not spend hours writing
	// markers before its first live probe.
	BackfillMissed time.Duration

	// RetryTransportTimeout re-runs a probe once when the first attempt fails
	// with a transport timeout (TCP connect timeout, TLS handshake timeout, or
	// a gRPC Unavailable whose cause is a timeout). The server's default
	// connection cap is filled by a 16-signer upload, so a probe arriving
	// during an upload waits for a slot and can time out without saying
	// anything about retention. The retry costs one extra request and no
	// bytes, waits RetryDelay, and is skipped when the retry would land in a
	// different schedule phase than the first attempt. Both attempts are
	// recorded in the final measurement's Retry field.
	RetryTransportTimeout bool
	RetryDelay            time.Duration // default 20s
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
	if c.RetryDelay <= 0 {
		c.RetryDelay = 20 * time.Second
	}
	if c.Concurrency <= 0 {
		c.Concurrency = 8
	}
	if c.BackfillMissed <= 0 {
		c.BackfillMissed = time.Hour
	}
	if c.Vantage == "" {
		c.Vantage = "local"
	}
	return c
}

// Prober turns the scanner's publications into scheduled probes and raw
// measurements. The pending-probe queue is never persisted: it is re-derived
// from publications.jsonl + measurements.jsonl every cycle, so a restart
// resumes exactly (per target, so a point interrupted half-way is finished
// for the remaining validators).
type Prober struct {
	cfg      Config
	log      *scan.Logger
	chain    *scan.Chain
	resolver *Resolver
	store    *MeasurementStore
	feed     *pubFeed
	chainID  string

	clockMu     sync.Mutex
	clockOffset time.Duration // observer clock - latest block time

	coders map[[2]int]*Coder // keyed by (originalRows, totalRows)

	// complete marks (vantage, promise, point) slots every target of which
	// has a row; plan skips them without resolving targets again.
	complete map[string]bool
	// skippedPubs are publications logged once as not probeable (wrong chain,
	// failed settlement tx).
	skippedPubs map[string]bool

	valMu    sync.Mutex
	valLocks map[string]*sync.Mutex // one connection per validator at a time
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
		cfg:         cfg,
		log:         log,
		chain:       ch,
		resolver:    NewResolver(ch, cfg.HostCacheTTL),
		store:       st,
		feed:        newPubFeed(cfg.PublicationsPath),
		coders:      map[[2]int]*Coder{},
		complete:    map[string]bool{},
		skippedPubs: map[string]bool{},
		valLocks:    map[string]*sync.Mutex{},
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

func (p *Prober) validatorLock(addr string) *sync.Mutex {
	p.valMu.Lock()
	defer p.valMu.Unlock()
	l, ok := p.valLocks[addr]
	if !ok {
		l = &sync.Mutex{}
		p.valLocks[addr] = l
	}
	return l
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
	p.measureClock(ctx)
	p.log.Printf("prober up: vantage=%s chain_id=%s tip=%d rpc=%s pubs=%s data=%s concurrency=%d",
		p.cfg.Vantage, id, tip, p.cfg.RPCURL, p.cfg.PublicationsPath, p.store.Path(), p.cfg.Concurrency)

	probed := 0
	for {
		if err := ctx.Err(); err != nil {
			if errors.Is(err, context.Canceled) {
				p.log.Printf("stopped (signal): %d probes this run", probed)
				return nil
			}
			p.log.Fatalf("run deadline hit: %v", err)
		}

		p.measureClock(ctx)

		if added, err := p.feed.refresh(); err != nil {
			p.log.Fatalf("load publications: %v", err)
		} else if added > 0 {
			p.log.Printf("publications: +%d (%d live)", added, len(p.feed.pubs))
		}

		now := time.Now()
		due, future, missed, dropped, finished := p.plan(p.feed.all(), now)

		for _, mj := range missed {
			p.recordNotProbed(ctx, mj, "scheduled point elapsed before the prober ran it")
		}
		for _, d := range dropped {
			p.recordNotProbed(ctx, d.job, d.reason)
		}
		if len(missed)+len(dropped) > 0 {
			if err := p.store.Sync(); err != nil {
				p.log.Fatalf("sync measurements: %v", err)
			}
		}
		for _, h := range finished {
			p.feed.forget(h)
			p.store.Forget(h)
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

// clockSkewWarn is the offset from chain time past which every verdict this
// vantage produces is suspect: the phase boundaries are only 30 s (grace
// offset) and 150 s (prune tolerance) wide.
const clockSkewWarn = 30 * time.Second

// measureClock records the observer's clock offset against the chain's latest
// block time. Every phase decision uses the local clock, so a drifted vantage
// would silently mislabel probes; the offset is stamped on every measurement
// and a large one is logged.
func (p *Prober) measureClock(ctx context.Context) {
	blockTime, err := p.chain.LatestBlockTime(ctx)
	if err != nil {
		p.log.Printf("clock check: %v (keeping previous offset)", err)
		return
	}
	offset := time.Since(blockTime)
	p.clockMu.Lock()
	prev := p.clockOffset
	p.clockOffset = offset
	p.clockMu.Unlock()
	if abs(offset) > clockSkewWarn && abs(prev) <= clockSkewWarn {
		p.log.Printf("WARNING: observer clock is %s from the chain's latest block time; phase boundaries are seconds wide, check NTP", offset.Round(time.Second))
	}
}

func (p *Prober) clockOffsetMS() int64 {
	p.clockMu.Lock()
	defer p.clockMu.Unlock()
	return p.clockOffset.Milliseconds()
}

func abs(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}

func (p *Prober) sleep(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}

// job is one (publication, schedule point) slot; runDue expands it to
// validators.
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

// probeable reports whether a publication belongs to this prober at all, and
// logs once when it does not.
func (p *Prober) probeable(pub scan.Publication) bool {
	reason := ""
	switch {
	case pub.Assignment.Error != "":
		return false // no assignment table -> nothing to probe (logged by the scanner)
	case pub.SettlementTxCode != 0:
		reason = fmt.Sprintf("settlement tx failed (code %d); no obligation", pub.SettlementTxCode)
	case pub.Promise.ChainID != "" && p.chainID != "" && pub.Promise.ChainID != p.chainID:
		reason = fmt.Sprintf("promise chain_id %q is not this RPC's chain %q", pub.Promise.ChainID, p.chainID)
	}
	if reason == "" {
		return true
	}
	if !p.skippedPubs[pub.PromiseHash] {
		p.skippedPubs[pub.PromiseHash] = true
		p.log.Printf("skip %s: %s", short(pub.PromiseHash), reason)
	}
	return false
}

// plan splits every not-yet-complete schedule point into due (probe now),
// future (sleep until), missed (record a MISSED marker) and dropped (policy
// refused the publication). finished lists publications whose whole schedule
// is behind the backfill horizon: nothing will ever be recorded for them
// again, so they can be forgotten.
func (p *Prober) plan(pubs []scan.Publication, now time.Time) (due, future, missed []job, dropped []skipped, finished []string) {
	horizon := now.Add(-p.cfg.BackfillMissed)
	for _, pub := range pubs {
		if !p.probeable(pub) {
			continue
		}
		points := ScheduleFor(pub, p.cfg.Schedule)
		var pending []SchedulePoint
		started := false
		allPast := true
		for _, pt := range points {
			key := pointKey(p.cfg.Vantage, pub.PromiseHash, pt.At)
			if p.complete[key] {
				started = true
				continue
			}
			if p.store.HandledPoint(p.cfg.Vantage, pub.PromiseHash, pt.At) {
				started = true // at least one target has a row; finish the rest
			}
			if pt.At.After(horizon) {
				allPast = false
			}
			pending = append(pending, pt)
		}
		if len(pending) == 0 || allPast {
			// every point is complete, or so old that nothing will be
			// written for it: forget the publication.
			finished = append(finished, pub.PromiseHash)
			continue
		}
		if p.cfg.Policy != nil {
			if ok, reason := p.cfg.Policy.Admit(pub, started); !ok {
				for _, pt := range pending {
					if pt.At.After(horizon) {
						dropped = append(dropped, skipped{job{pub, pt}, reason})
					}
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
			case pt.At.After(horizon):
				missed = append(missed, job{pub, pt})
			default:
				// behind the backfill horizon: left without a row
				p.complete[pointKey(p.cfg.Vantage, pub.PromiseHash, pt.At)] = true
			}
		}
	}
	sort.SliceStable(future, func(i, j int) bool { return future[i].point.At.Before(future[j].point.At) })
	sort.SliceStable(due, func(i, j int) bool { return due[i].point.At.Before(due[j].point.At) })
	return due, future, missed, dropped, finished
}

// work is one (publication, point, validator) probe ready to run.
type work struct {
	job        job
	target     Target
	coder      *Coder
	commitment [32]byte
	key        string // point key, for completion tracking
}

// runDue probes every due slot. Slots are grouped by publication so the
// validator set / host registry is resolved once per group; the resulting
// (point, validator) probes then run on a pool of Concurrency workers, with
// at most one in-flight probe per validator. A group whose targets cannot be
// resolved is left for the next cycle (not marked, so it retries until it
// either succeeds or ages into MISSED). A point is marked complete only when
// every one of its targets has a row.
func (p *Prober) runDue(ctx context.Context, due []job) int {
	byPub := map[string][]job{}
	order := []string{}
	for _, j := range due {
		if _, ok := byPub[j.pub.PromiseHash]; !ok {
			order = append(order, j.pub.PromiseHash)
		}
		byPub[j.pub.PromiseHash] = append(byPub[j.pub.PromiseHash], j)
	}

	var items []work
	pointItems := map[string]int{}
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
			key := pointKey(p.cfg.Vantage, ph, j.point.At)
			for _, t := range targets {
				if p.store.Has(p.cfg.Vantage, ph, t.AddressHex, j.point.At) {
					continue
				}
				items = append(items, work{job: j, target: t, coder: coder, commitment: commitment, key: key})
				pointItems[key]++
			}
			if pointItems[key] == 0 {
				p.complete[key] = true // every target already recorded
			}
		}
	}
	if len(items) == 0 {
		return 0
	}

	var (
		mu       sync.Mutex
		n        int
		done     = map[string]int{}
		sem      = make(chan struct{}, p.cfg.Concurrency)
		wg       sync.WaitGroup
		canceled bool
	)
	for _, it := range items {
		if ctx.Err() != nil {
			canceled = true
			break
		}
		sem <- struct{}{}
		wg.Add(1)
		go func(it work) {
			defer wg.Done()
			defer func() { <-sem }()
			probedOne := p.runOne(ctx, it)
			mu.Lock()
			if probedOne {
				n++
			}
			done[it.key]++
			mu.Unlock()
		}(it)
	}
	wg.Wait()
	if !canceled && ctx.Err() == nil {
		for key, want := range pointItems {
			if done[key] == want {
				p.complete[key] = true
			}
		}
	}
	return n
}

// runOne runs a single probe end to end: lateness re-check, policy gate,
// per-validator serialisation, the probe itself, the optional retry, and the
// record. It reports whether a network probe was carried out.
func (p *Prober) runOne(ctx context.Context, it work) bool {
	j, t, pub := it.job, it.target, it.job.pub
	ph := pub.PromiseHash

	// The slot was due when planned; a long cycle must not silently probe it
	// in a later phase. Past MaxLateness it is a gap, not a verdict.
	if late := time.Since(j.point.At); late > p.cfg.MaxLateness {
		p.recordNotProbedTarget(pub, j.point, t, fmt.Sprintf("elapsed while the cycle ran (%s late)", late.Round(time.Second)))
		return false
	}
	skipDL := false
	if p.cfg.Policy != nil {
		allow, skip, reason := p.cfg.Policy.BeforeProbe(pub, t, time.Now())
		if !allow {
			p.recordNotProbedTarget(pub, j.point, t, reason)
			return false
		}
		skipDL = skip
	}
	in := Input{
		Vantage:            p.cfg.Vantage,
		ChainID:            p.chainID,
		PromiseHash:        ph,
		Commitment:         it.commitment,
		CommitmentHex:      pub.Promise.Commitment,
		BlobVersion:        pub.Promise.BlobVersion,
		MustServeUntil:     pub.MustServeUntil,
		ValidatorSetHeight: pub.Assignment.ValidatorSetHeight,
		Target:             t,
		SchedulePoint:      j.point,
		PruneTolerance:     p.schedCfg().PruneTolerance,
		SkipDownload:       skipDL,
		ExpectedShardBytes: ShardBytes(pub.Promise.BlobSize, pub.Assignment.ProtocolParams.OriginalRows, t.RowCount),
		ClockOffsetMS:      p.clockOffsetMS(),
	}

	lock := p.validatorLock(t.AddressHex)
	lock.Lock()
	m := Run(ctx, in, it.coder, p.cfg.Timeouts)
	if p.cfg.RetryTransportTimeout && shouldRetryTransport(m, pub, p.cfg.Schedule, p.cfg.RetryDelay, time.Now()) {
		p.log.Printf("probe %s %s: %s (%s); retrying once in %s", short(ph), t.Host, m.Outcome, m.RawError, p.cfg.RetryDelay)
		if sleepCtx(ctx, p.cfg.RetryDelay) {
			m = retryOnce(ctx, in, it.coder, p.cfg.Timeouts, m, p.cfg.RetryDelay)
		}
	}
	lock.Unlock()

	if err := p.store.Append(m); err != nil {
		p.log.Fatalf("append measurement: %v", err)
	}
	if p.cfg.Policy != nil {
		p.cfg.Policy.AfterProbe(pub, m)
	}
	p.logMeasurement(m)
	return true
}

// recordNotProbed marks one (publication, point) slot NOT_PROBED for every
// target, with the given reason (elapsed, or a policy decision). Rows are
// appended without fsync; the caller syncs once per batch.
func (p *Prober) recordNotProbed(ctx context.Context, j job, reason string) {
	pub := j.pub
	key := pointKey(p.cfg.Vantage, pub.PromiseHash, j.point.At)
	targets, err := p.resolver.TargetsFor(ctx, pub, p.cfg.IncludeUnassigned)
	if err != nil {
		// No targets, so no per-validator row can be written. The point is
		// marked handled in memory and ages past the backfill horizon on a
		// restart; a row with no validator address would only be a record
		// nothing downstream can attribute.
		p.log.Printf("not-probed %s %s: targets unresolved: %v (%s)", short(pub.PromiseHash), j.point.Label, err, reason)
		p.complete[key] = true
		return
	}
	for _, t := range targets {
		p.recordNotProbedTarget(pub, j.point, t, reason)
	}
	p.complete[key] = true
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
		Assigned: t.Assigned, Attested: t.Attested, AssignedRowCount: t.RowCount,
		ScheduleLabel: pt.Label, ScheduledAt: pt.At.UTC(),
		StartedAt: time.Now().UTC(), FinishedAt: time.Now().UTC(),
		Phase: PhaseAt(pt.At, pub, p.cfg.Schedule), Outcome: OutcomeMissed,
		Classification: ClassNotProbed, ClassificationReason: reason,
	}
	if err := p.store.AppendDeferred(m); err != nil {
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

// shouldRetryTransport decides whether a first attempt deserves the one
// transport-timeout retry: the outcome must be a transport timeout, and the
// retry, started after delay, must still fall in the same schedule phase as
// the first attempt (a retry that crossed from in_window into grace would
// change the verdict, not just the evidence).
func shouldRetryTransport(m Measurement, pub scan.Publication, sc ScheduleConfig, delay time.Duration, now time.Time) bool {
	if m.Retry != nil {
		return false // already retried
	}
	if !m.transportTimeout() {
		return false
	}
	return PhaseAt(now.Add(delay), pub, sc) == m.Phase
}

// transportTimeout reports whether the measurement failed because a
// connection could not be established in time: TCP connect timeout, TLS
// handshake timeout, or gRPC Unavailable caused by a timeout. A download that
// started and then ran out of time is not a transport timeout.
func (m Measurement) transportTimeout() bool {
	switch m.Outcome {
	case OutcomeTCPTimeout:
		return true
	case OutcomeTLSFail:
		return m.TLS.Attempted && isTimeoutText(m.TLS.Error)
	case OutcomeRPCUnavailable:
		return m.Download.Attempted && isTimeoutText(m.Download.Error)
	}
	return false
}

func isTimeoutText(s string) bool {
	ls := strings.ToLower(s)
	return strings.Contains(ls, "timeout") || strings.Contains(ls, "deadline exceeded")
}

// retryOnce runs the probe a second time and returns the second measurement
// with the first attempt attached. The second attempt's timings and verdict
// stand on their own; the first is evidence.
func retryOnce(ctx context.Context, in Input, coder *Coder, to StepTimeouts, first Measurement, delay time.Duration) Measurement {
	m := Run(ctx, in, coder, to)
	m.Retry = &RetryInfo{
		Attempts:        2,
		DelayMS:         delay.Milliseconds(),
		FirstStartedAt:  first.StartedAt,
		FirstOutcome:    first.Outcome,
		FirstError:      first.RawError,
		FirstDurationMS: first.TotalDurationMS,
	}
	if m.Outcome == first.Outcome {
		m.ClassificationReason += "; persisted across a retry after " + delay.String()
	} else {
		m.ClassificationReason += "; first attempt " + string(first.Outcome) + ", retried after " + delay.String()
	}
	return m
}

// sleepCtx waits d or until ctx is done; it reports whether the full wait
// completed.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
