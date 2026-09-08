// Package policy implements the probe load policy from
// docs/research/R4-probe-etiquette.md: deterministic, unpredictable sampling
// of publications when the byte or request budget would be exceeded,
// per-validator and global caps, and a backoff that never adds requests.
//
// The policy plugs into the prober through probe.Policy. It keeps only
// in-memory counters; the durable record of every decision is the
// NOT_PROBED measurement the prober writes with the policy's reason.
package policy

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"os"
	"sort"
	"sync"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/plsgiveup/fibre/fibre-sentinel/internal/probe"
	"github.com/plsgiveup/fibre/fibre-sentinel/internal/scan"
)

// Config is the on-disk policy (YAML). Zero values take the defaults from
// R4 section 3.5.
type Config struct {
	Capacity struct {
		FloorRows         int   `yaml:"floor_rows"`          // rows of the smallest validator the capacity model is stated for (148)
		FloorValidatorBps int64 `yaml:"floor_validator_bps"` // assumed serving capacity of a floor validator, bits per second (0.65 Gbps, UNVERIFIED)
		ScaleWithRows     bool  `yaml:"scale_with_rows"`     // cap(rows) = cap(floor) * rows / floor_rows
	} `yaml:"capacity_model"`
	Caps struct {
		PerValidator struct {
			RequestsPerMinute    int           `yaml:"requests_per_minute"`
			MinRequestSpacing    time.Duration `yaml:"min_request_spacing"`
			BytesPerHourFraction float64       `yaml:"bytes_per_hour_fraction"` // of the capacity model
			BytesPerDayFraction  float64       `yaml:"bytes_per_day_fraction"`
		} `yaml:"per_validator"`
		Global struct {
			BytesPerHour int64 `yaml:"bytes_per_hour"`
			BytesPerDay  int64 `yaml:"bytes_per_day"`
		} `yaml:"global"`
	} `yaml:"caps"`
	Sampling struct {
		MasterSecretFile   string        `yaml:"master_secret_file"`
		ProjectionLookback time.Duration `yaml:"projection_lookback"`
		AlwaysProbe        []string      `yaml:"always_probe"` // promise hashes exempt from sampling
	} `yaml:"sampling"`
	Backoff struct {
		SkipDownloadAfter int           `yaml:"skip_download_after"` // consecutive transport failures before L4 is skipped
		SkipWindow        time.Duration `yaml:"skip_window"`         // how long after the last failure the skip holds
	} `yaml:"backoff"`
}

// Default returns the R4 defaults.
func Default() Config {
	var c Config
	c.Capacity.FloorRows = 148
	c.Capacity.FloorValidatorBps = 650_000_000
	c.Capacity.ScaleWithRows = true
	c.Caps.PerValidator.RequestsPerMinute = 30
	c.Caps.PerValidator.MinRequestSpacing = 2 * time.Second
	c.Caps.PerValidator.BytesPerHourFraction = 0.01
	c.Caps.PerValidator.BytesPerDayFraction = 0.0075
	c.Caps.Global.BytesPerHour = 50 << 30 // 50 GiB
	c.Caps.Global.BytesPerDay = 600 << 30 // 600 GiB
	c.Sampling.ProjectionLookback = time.Hour
	c.Backoff.SkipDownloadAfter = 3
	c.Backoff.SkipWindow = 20 * time.Minute
	return c
}

// Load reads a YAML file over the defaults. An empty path returns Default().
func Load(path string) (Config, error) {
	c := Default()
	if path == "" {
		return c, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return c, err
	}
	if err := yaml.Unmarshal(b, &c); err != nil {
		return c, fmt.Errorf("parse %s: %w", path, err)
	}
	return c, c.validate()
}

func (c Config) validate() error {
	if c.Capacity.FloorRows <= 0 || c.Capacity.FloorValidatorBps <= 0 {
		return errors.New("capacity_model: floor_rows and floor_validator_bps must be positive")
	}
	if c.Caps.PerValidator.BytesPerHourFraction <= 0 || c.Caps.Global.BytesPerHour <= 0 {
		return errors.New("caps: bytes_per_hour_fraction and global.bytes_per_hour must be positive")
	}
	return nil
}

// bytesPerHourCap is the per-validator hourly byte cap for a validator with
// rows assigned rows.
func (c Config) bytesPerHourCap(rows int) int64 {
	capBytes := float64(c.Capacity.FloorValidatorBps) / 8 * 3600 * c.Caps.PerValidator.BytesPerHourFraction
	if c.Capacity.ScaleWithRows && rows > c.Capacity.FloorRows {
		capBytes *= float64(rows) / float64(c.Capacity.FloorRows)
	}
	return int64(capBytes)
}

func (c Config) bytesPerDayCap(rows int) int64 {
	capBytes := float64(c.Capacity.FloorValidatorBps) / 8 * 86400 * c.Caps.PerValidator.BytesPerDayFraction
	if c.Capacity.ScaleWithRows && rows > c.Capacity.FloorRows {
		capBytes *= float64(rows) / float64(c.Capacity.FloorRows)
	}
	return int64(capBytes)
}

// ShardBytes estimates the wire size of one validator's shard for a blob:
// rows × (row + 14 proof hashes + protobuf framing) + the fixed RLC vector.
// Inputs are the padded upload size (promise.blob_size) and the protocol
// params. Framing is 36 bytes per row: 14 proof entries × 2 (tag, length),
// the row data tag and 4-byte length, and the index field. The numbers match
// the table in docs/research/R4-probe-etiquette.md section 1.3.
func ShardBytes(blobSize uint32, originalRows, rows int) int64 {
	if originalRows <= 0 {
		return 0
	}
	rowSize := int64(blobSize) / int64(originalRows)
	const proofBytes = 14 * 32
	const framing = 36
	return int64(rows)*(rowSize+proofBytes+framing) + int64(originalRows)*16 + 4
}

// downloadsPerBlob is how many schedule points transfer bytes (in-window
// points plus the grace point; the post point expects NOT_FOUND).
const downloadsPerBlob = 5

// event is one accounted probe.
type event struct {
	at    time.Time
	bytes int64
}

type validatorState struct {
	events       []event // trailing 24h
	lastRequest  time.Time
	consecFail   int
	lastFailAt   time.Time
	rowsLastSeen int
}

// Policy is a probe.Policy backed by Config.
type Policy struct {
	cfg    Config
	master []byte

	mu         sync.Mutex
	validators map[string]*validatorState
	global     []event
	// publications seen in the projection lookback, keyed by promise hash,
	// with the bytes a full schedule over all assigned validators would cost.
	recentPubs map[string]pubLoad
	decisions  map[string]bool // promise hash -> admitted (sticky per process)
	lastP      float64
	lastCap    string
}

type pubLoad struct {
	settled  time.Time
	global   int64
	perVal   map[string]int64 // validator hex -> bytes for the whole schedule
	perValRs map[string]int
}

// New builds a Policy. The master secret is read from cfg.Sampling.
// MasterSecretFile, or generated (and written, 0600) if the file is absent
// and the path is set; with no path a process-local random secret is used.
func New(cfg Config) (*Policy, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	master, err := loadOrCreateSecret(cfg.Sampling.MasterSecretFile)
	if err != nil {
		return nil, err
	}
	return &Policy{
		cfg:        cfg,
		master:     master,
		validators: map[string]*validatorState{},
		recentPubs: map[string]pubLoad{},
		decisions:  map[string]bool{},
		lastP:      1,
	}, nil
}

func loadOrCreateSecret(path string) ([]byte, error) {
	if path == "" {
		b := make([]byte, 32)
		if _, err := rand.Read(b); err != nil {
			return nil, err
		}
		return b, nil
	}
	b, err := os.ReadFile(path)
	if err == nil {
		if len(b) < 16 {
			return nil, fmt.Errorf("%s: master secret shorter than 16 bytes", path)
		}
		return b, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	b = make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		return nil, fmt.Errorf("write master secret: %w", err)
	}
	return b, nil
}

// DaySecret derives the per-day secret for the UTC day containing t.
func (p *Policy) DaySecret(t time.Time) []byte {
	mac := hmac.New(sha256.New, p.master)
	mac.Write([]byte(t.UTC().Format("2006-01-02")))
	return mac.Sum(nil)
}

// DayCommitment is SHA256(day secret), safe to publish before the day so the
// sample can be audited after the secret is revealed.
func (p *Policy) DayCommitment(t time.Time) string {
	s := sha256.Sum256(p.DaySecret(t))
	return hex.EncodeToString(s[:])
}

// Sampled reports whether promiseHash is in the sample at probability prob,
// using the day secret of the publication's settlement day. Deterministic
// and, without the secret, unpredictable.
func (p *Policy) Sampled(promiseHash string, settled time.Time, prob float64) bool {
	if prob >= 1 {
		return true
	}
	if prob <= 0 {
		return false
	}
	h := sha256.New()
	hb, err := hex.DecodeString(promiseHash)
	if err != nil {
		hb = []byte(promiseHash)
	}
	h.Write(hb)
	h.Write(p.DaySecret(settled))
	v := binary.BigEndian.Uint64(h.Sum(nil)[:8])
	return float64(v) < prob*math.Exp2(64)
}

// Observe registers a publication's projected load. The prober calls it for
// every publication it plans, every cycle; only publications settled within
// the lookback count.
func (p *Policy) observe(pub scan.Publication, now time.Time) {
	if _, ok := p.recentPubs[pub.PromiseHash]; ok {
		return
	}
	if now.Sub(pub.SettlementTime) > p.cfg.Sampling.ProjectionLookback {
		return
	}
	pl := pubLoad{settled: pub.SettlementTime, perVal: map[string]int64{}, perValRs: map[string]int{}}
	orig := pub.Assignment.ProtocolParams.OriginalRows
	for _, v := range pub.Assignment.Validators {
		b := ShardBytes(pub.Promise.BlobSize, orig, v.RowCount) * downloadsPerBlob
		pl.perVal[v.Address] = b
		pl.perValRs[v.Address] = v.RowCount
		pl.global += b
	}
	p.recentPubs[pub.PromiseHash] = pl
}

// projectedP computes the admission probability from the trailing lookback:
// p = min(1, cap/projected) over the global hourly cap and the most
// constrained validator's hourly cap.
func (p *Policy) projectedP(now time.Time) (float64, string) {
	lookback := p.cfg.Sampling.ProjectionLookback
	var global int64
	perVal := map[string]int64{}
	perValRows := map[string]int{}
	for h, pl := range p.recentPubs {
		if now.Sub(pl.settled) > lookback {
			delete(p.recentPubs, h)
			continue
		}
		global += pl.global
		for a, b := range pl.perVal {
			perVal[a] += b
			perValRows[a] = pl.perValRs[a]
		}
	}
	// scale the lookback window to one hour.
	scale := float64(time.Hour) / float64(lookback)
	prob, binding := 1.0, "none"
	if g := float64(global) * scale; g > float64(p.cfg.Caps.Global.BytesPerHour) {
		prob = float64(p.cfg.Caps.Global.BytesPerHour) / g
		binding = "global_bytes_per_hour"
	}
	// deterministic iteration for stable logs.
	addrs := make([]string, 0, len(perVal))
	for a := range perVal {
		addrs = append(addrs, a)
	}
	sort.Strings(addrs)
	for _, a := range addrs {
		capB := float64(p.cfg.bytesPerHourCap(perValRows[a]))
		if v := float64(perVal[a]) * scale; v > capB {
			if q := capB / v; q < prob {
				prob, binding = q, "validator_bytes_per_hour"
			}
		}
	}
	return prob, binding
}

// Admit implements probe.Policy.
func (p *Policy) Admit(pub scan.Publication, alreadyStarted bool) (bool, string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if alreadyStarted {
		p.decisions[pub.PromiseHash] = true
		return true, ""
	}
	if d, ok := p.decisions[pub.PromiseHash]; ok {
		if d {
			return true, ""
		}
		return false, p.lastReason(pub)
	}
	for _, h := range p.cfg.Sampling.AlwaysProbe {
		if h == pub.PromiseHash {
			p.decisions[pub.PromiseHash] = true
			return true, ""
		}
	}
	now := time.Now()
	p.observe(pub, now)
	prob, binding := p.projectedP(now)
	p.lastP, p.lastCap = prob, binding
	in := p.Sampled(pub.PromiseHash, pub.SettlementTime, prob)
	p.decisions[pub.PromiseHash] = in
	if in {
		return true, ""
	}
	return false, p.lastReason(pub)
}

func (p *Policy) lastReason(pub scan.Publication) string {
	return fmt.Sprintf("budget:p=%.3f:%s:day_commitment=%s", p.lastP, p.lastCap, p.DayCommitment(pub.SettlementTime))
}

// State returns the last computed admission probability and binding cap,
// for logs and the API.
func (p *Policy) State() (prob float64, binding string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.lastP, p.lastCap
}

func (p *Policy) state(addr string) *validatorState {
	vs, ok := p.validators[addr]
	if !ok {
		vs = &validatorState{}
		p.validators[addr] = vs
	}
	return vs
}

func sumSince(events []event, since time.Time) (n int, bytes int64) {
	for _, e := range events {
		if !e.at.Before(since) {
			n++
			bytes += e.bytes
		}
	}
	return n, bytes
}

func trim(events []event, since time.Time) []event {
	i := 0
	for i < len(events) && events[i].at.Before(since) {
		i++
	}
	return events[i:]
}

// BeforeProbe implements probe.Policy.
func (p *Policy) BeforeProbe(pub scan.Publication, t probe.Target, now time.Time) (allow, skipDownload bool, reason string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	vs := p.state(t.AddressHex)
	vs.events = trim(vs.events, now.Add(-24*time.Hour))
	p.global = trim(p.global, now.Add(-24*time.Hour))
	pv := p.cfg.Caps.PerValidator

	if !vs.lastRequest.IsZero() && now.Sub(vs.lastRequest) < pv.MinRequestSpacing {
		// Not a budget denial: the caller's schedule is coarse (seconds),
		// so we simply wait out the spacing rather than drop the probe.
		time.Sleep(pv.MinRequestSpacing - now.Sub(vs.lastRequest))
		now = time.Now()
	}
	if reqs, _ := sumSince(vs.events, now.Add(-time.Minute)); pv.RequestsPerMinute > 0 && reqs >= pv.RequestsPerMinute {
		return false, false, fmt.Sprintf("budget:validator_requests_per_minute=%d", pv.RequestsPerMinute)
	}
	rows := t.RowCount
	if rows == 0 {
		rows = vs.rowsLastSeen
	}
	est := ShardBytes(pub.Promise.BlobSize, pub.Assignment.ProtocolParams.OriginalRows, rows)
	if _, hb := sumSince(vs.events, now.Add(-time.Hour)); hb+est > p.cfg.bytesPerHourCap(rows) {
		return false, false, "budget:validator_bytes_per_hour"
	}
	if _, db := sumSince(vs.events, now.Add(-24*time.Hour)); db+est > p.cfg.bytesPerDayCap(rows) {
		return false, false, "budget:validator_bytes_per_day"
	}
	if _, gh := sumSince(p.global, now.Add(-time.Hour)); gh+est > p.cfg.Caps.Global.BytesPerHour {
		return false, false, "budget:global_bytes_per_hour"
	}
	if _, gd := sumSince(p.global, now.Add(-24*time.Hour)); p.cfg.Caps.Global.BytesPerDay > 0 && gd+est > p.cfg.Caps.Global.BytesPerDay {
		return false, false, "budget:global_bytes_per_day"
	}
	if p.cfg.Backoff.SkipDownloadAfter > 0 && vs.consecFail >= p.cfg.Backoff.SkipDownloadAfter &&
		now.Sub(vs.lastFailAt) < p.cfg.Backoff.SkipWindow {
		return true, true, fmt.Sprintf("backoff:transport:k=%d", vs.consecFail)
	}
	return true, false, ""
}

// AfterProbe implements probe.Policy.
func (p *Policy) AfterProbe(pub scan.Publication, m probe.Measurement) {
	p.mu.Lock()
	defer p.mu.Unlock()
	vs := p.state(m.ValidatorAddress)
	vs.lastRequest = m.StartedAt
	if m.AssignedRowCount > 0 {
		vs.rowsLastSeen = m.AssignedRowCount
	}
	var bytes int64
	if m.Download.Attempted && m.Download.RowsReturned > 0 {
		bytes = ShardBytes(pub.Promise.BlobSize, pub.Assignment.ProtocolParams.OriginalRows, m.Download.RowsReturned)
	}
	ev := event{at: m.StartedAt, bytes: bytes}
	vs.events = append(vs.events, ev)
	p.global = append(p.global, ev)

	switch m.Outcome {
	case probe.OutcomeDNSFail, probe.OutcomeTCPRefused, probe.OutcomeTCPTimeout, probe.OutcomeTCPUnreachable,
		probe.OutcomeTLSFail, probe.OutcomeRPCUnavailable:
		vs.consecFail++
		vs.lastFailAt = m.StartedAt
	case probe.OutcomeServedOK, probe.OutcomeNotFound, probe.OutcomeReachable, probe.OutcomePartial,
		probe.OutcomeWrongRows, probe.OutcomeInvalidRows:
		vs.consecFail = 0
	}
}

var _ probe.Policy = (*Policy)(nil)
