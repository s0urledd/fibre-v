// Package api is the observer's read-only JSON API, versioned under /v1/.
//
// Every aggregate carries the probe count it was computed from, the time
// window it covers, and the vantage. Rates follow docs/verdicts.md: serve
// rate is HEALTHY / (HEALTHY + FAULT) over assigned probes in the in-window
// and grace phases; NOT_PROBED and PROBE_ERROR are reported as gaps, never
// folded into a rate.
package api

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/cosmos/cosmos-sdk/types/bech32"

	"github.com/plsgiveup/fibre/fibre-sentinel/internal/scan"
	"github.com/plsgiveup/fibre/fibre-sentinel/observer/store"
)

// Version is reported in /v1/meta.
const Version = "0.1.0"

// VantageInfo describes where this observer watches from.
//
// Every reachability observation on the site is a statement about a network
// path, and half that path is ours. A reader cannot judge an UNREACHABLE
// without knowing where it was measured from, and a validator operator cannot
// check our traffic against their own logs without knowing which addresses to
// look for.
//
// These fields are not equally trustworthy, and the response says so rather
// than presenting them as one thing:
//
//   - EgressAddresses is the anchor. An operator who sees connections from
//     these addresses on their Fibre port can match them against this record,
//     and one who sees connections from anywhere else knows they are not this
//     observer.
//   - ASN is checkable from those addresses by anyone, through public routing
//     data (RIPEstat, whois, bgp.tools). It identifies the network our
//     traffic is routed through, not a place: one provider can hold several
//     autonomous systems, and one autonomous system can span countries.
//   - Provider usually follows from the ASN, so it is checkable in the same
//     way, just less precisely.
//   - Location is the only genuinely unverifiable field. Geolocating an
//     address is a guess, so this is the operator's word and nothing more.
type VantageInfo struct {
	// Name is the short label every response already carries.
	Name string `json:"name"`
	// Location is where the machine physically sits, e.g. "Helsinki,
	// Finland". Operator's word; an address cannot prove it.
	Location string `json:"location,omitempty"`
	// Provider is the hosting company, e.g. "Hetzner".
	Provider string `json:"provider,omitempty"`
	// ASN is the autonomous system our traffic is routed through, e.g.
	// "AS24940". Anyone can check it against EgressAddresses.
	ASN string `json:"asn,omitempty"`
	// EgressAddresses are the source addresses probes leave from, and the
	// thing everything else here is checked against.
	EgressAddresses []string `json:"egress_addresses,omitempty"`
	// Verifiability says, per field, what a reader can check and how, so the
	// page rendering these cannot present a guess as a fact.
	Verifiability map[string]string `json:"verifiability"`
	// Complete is false while the operator has not filled this in, which is
	// what the dashboard checks before claiming the vantage is described.
	Complete bool `json:"complete"`
}

// Server serves the API over a store.
type Server struct {
	st      *store.Store
	vantage string
	info    VantageInfo
	mux     *http.ServeMux
	log     *scan.Logger // may be nil (tests)
}

// New builds a Server. vantage is the label rendered on every response.
func New(st *store.Store, vantage string) *Server { return NewWithLogger(st, vantage, nil) }

// NewWithLogger is New with somewhere to put the detail of an internal error
// that the response deliberately withholds.
func NewWithLogger(st *store.Store, vantage string, log *scan.Logger) *Server {
	return NewWithVantage(st, VantageInfo{Name: vantage}, log)
}

// NewWithVantage is NewWithLogger with the vantage described rather than only
// named.
func NewWithVantage(st *store.Store, info VantageInfo, log *scan.Logger) *Server {
	info.Verifiability = map[string]string{
		"egress_addresses": "the anchor: match these against the source addresses hitting your Fibre port",
		"asn":              "check it against egress_addresses through public routing data (whois, RIPEstat, bgp.tools); it names the network, not a place",
		"provider":         "usually follows from the asn, so checkable the same way",
		"location":         "the operator's word: geolocating an address is a guess, so nothing here proves it",
	}
	info.Complete = info.Location != "" && info.Provider != "" && info.ASN != "" && len(info.EgressAddresses) > 0
	s := &Server{st: st, vantage: info.Name, info: info, mux: http.NewServeMux(), log: log}
	s.mux.HandleFunc("GET /v1/meta", s.handleMeta)
	s.mux.HandleFunc("GET /v1/network", s.handleNetwork)
	s.mux.HandleFunc("GET /v1/validators", s.handleValidators)
	s.mux.HandleFunc("GET /v1/validators/{addr}", s.handleValidator)
	s.mux.HandleFunc("GET /v1/blobs", s.handleBlobs)
	s.mux.HandleFunc("GET /v1/blobs/{hash}", s.handleBlob)
	s.mux.HandleFunc("GET /v1/probes", s.handleProbes)
	s.mux.HandleFunc("GET /v1/runs", s.handleRuns)
	s.mux.HandleFunc("GET /v1/sampling", s.handleSampling)
	return s
}

// ServeHTTP implements http.Handler with the headers every response shares.
// Only successful responses are cacheable: a 400 or a 404 held for 15 seconds
// by a proxy outlives the mistake that caused it.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	rec := &statusWriter{ResponseWriter: w}
	s.mux.ServeHTTP(rec, r)
}

// statusWriter sets Cache-Control from the status code as the handler writes
// its header, and answers an unmatched route in the JSON shape the rest of
// the API uses.
type statusWriter struct {
	http.ResponseWriter
	wrote bool
	// swallow is set when this writer supplied the body itself (the JSON 404
	// in place of ServeMux's text/plain one), so the handler's own bytes are
	// dropped instead of being appended to it.
	swallow bool
}

func (w *statusWriter) WriteHeader(status int) {
	if w.wrote {
		return
	}
	w.wrote = true
	if status >= 200 && status < 300 {
		w.Header().Set("Cache-Control", "public, max-age=15")
	} else {
		w.Header().Set("Cache-Control", "no-store")
	}
	if status == http.StatusNotFound && !strings.HasPrefix(w.Header().Get("Content-Type"), "application/json") {
		w.swallow = true
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.ResponseWriter.WriteHeader(status)
		_, _ = w.ResponseWriter.Write([]byte(`{"error":"no such endpoint; see /v1/meta"}` + "\n"))
		return
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if !w.wrote {
		w.WriteHeader(http.StatusOK)
	}
	if w.swallow {
		return len(b), nil
	}
	return w.ResponseWriter.Write(b)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{"error": msg})
}

// writeInternal logs the real error and tells the client only that something
// went wrong: SQLite errors carry the database path and schema details that a
// public endpoint has no business publishing.
func (s *Server) writeInternal(w http.ResponseWriter, where string, err error) {
	if s.log != nil {
		s.log.Printf("api: %s: %v", where, err)
	}
	writeJSON(w, 500, map[string]any{"error": "internal error"})
}

// Window is a fixed lookback the dashboard offers.
type Window struct {
	Name  string        `json:"name"`
	Span  time.Duration `json:"-"`
	Start time.Time     `json:"start"`
	End   time.Time     `json:"end"`
}

var windows = map[string]time.Duration{"24h": 24 * time.Hour, "7d": 7 * 24 * time.Hour, "30d": 30 * 24 * time.Hour, "all": 0}

func parseWindow(r *http.Request, now time.Time) (Window, error) {
	name := r.URL.Query().Get("window")
	if name == "" {
		name = "24h"
	}
	span, ok := windows[name]
	if !ok {
		return Window{}, fmt.Errorf("window must be one of 24h, 7d, 30d, all")
	}
	w := Window{Name: name, Span: span, End: now}
	if span > 0 {
		w.Start = now.Add(-span)
	}
	return w, nil
}

func (w Window) startArg() string {
	if w.Span == 0 {
		return "0000"
	}
	return store.TS(w.Start)
}

// Rate is a numerator, denominator and their ratio, never a bare percentage.
type Rate struct {
	Num   int64    `json:"num"`
	Den   int64    `json:"den"`
	Value *float64 `json:"value"` // null when den == 0
}

func rate(num, den int64) Rate {
	r := Rate{Num: num, Den: den}
	if den > 0 {
		v := float64(num) / float64(den)
		r.Value = &v
	}
	return r
}

// ---- meta ----

type metaResponse struct {
	APIVersion             string       `json:"api_version"`
	Vantage                string       `json:"vantage"`
	VantageInfo            VantageInfo  `json:"vantage_info"`
	VantageCount           int          `json:"vantage_count"`
	ObservedFromOneVantage bool         `json:"observed_from_one_location"`
	ChainID                string       `json:"chain_id"`
	LastScannedHeight      string       `json:"last_scanned_height"`
	EndpointsHeight        string       `json:"endpoints_height"`
	ProtocolParamsFinger   string       `json:"protocol_params_fingerprint"`
	PinnedCelestiaApp      string       `json:"pinned_celestia_app_commit"`
	Counts                 store.Counts `json:"counts"`
	Collector              *runStatus   `json:"collector"`
	Prober                 *runStatus   `json:"prober"`
	// LastProbeAt is the newest measurement's start time. The prober writes
	// JSONL only (it never touches this database), so this is the only live
	// signal of it; a quiet chain makes it old without anything being wrong.
	LastProbeAt *string           `json:"last_probe_at"`
	Meta        map[string]string `json:"meta"`
	ServerTime  time.Time         `json:"server_time"`
}

type runStatus struct {
	RunID         int64   `json:"run_id"`
	StartedAt     string  `json:"started_at"`
	LastHeartbeat string  `json:"last_heartbeat_at"`
	StoppedAt     *string `json:"stopped_at"`
	Alive         bool    `json:"alive"` // heartbeat within the last 2 minutes
}

// vantageCount counts the distinct vantages that ever wrote a probe or a
// heartbeat (a second location that only runs the heartbeat still counts).
func (s *Server) vantageCount(ctx context.Context) int {
	var n int
	_ = s.st.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM (SELECT vantage FROM probes UNION SELECT vantage FROM reachability)`).Scan(&n)
	if n == 0 {
		n = 1
	}
	return n
}

func (s *Server) latestRun(ctx context.Context, component string, now time.Time) (*runStatus, error) {
	var rs runStatus
	err := s.st.DB().QueryRowContext(ctx, `SELECT id, started_at, last_heartbeat_at, stopped_at FROM observer_runs
		WHERE component = ? ORDER BY started_at DESC LIMIT 1`, component).Scan(&rs.RunID, &rs.StartedAt, &rs.LastHeartbeat, &rs.StoppedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if t, err := time.Parse(time.RFC3339Nano, rs.LastHeartbeat); err == nil && rs.StoppedAt == nil {
		rs.Alive = now.Sub(t) < 2*time.Minute
	}
	return &rs, nil
}

func (s *Server) handleMeta(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	now := time.Now()
	counts, err := s.st.Count(ctx)
	if err != nil {
		s.writeInternal(w, r.URL.Path, err)
		return
	}
	meta := map[string]string{}
	rows, err := s.st.DB().QueryContext(ctx, `SELECT key, value FROM meta`)
	if err != nil {
		s.writeInternal(w, r.URL.Path, err)
		return
	}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err == nil {
			meta[k] = v
		}
	}
	rows.Close()
	vantages := s.vantageCount(ctx)
	col, _ := s.latestRun(ctx, "collector", now)
	pr, _ := s.latestRun(ctx, "prober", now)
	var lastProbe *string
	var lp sql.NullString
	if err := s.st.DB().QueryRowContext(ctx, `SELECT MAX(started_at) FROM probes`).Scan(&lp); err == nil && lp.Valid {
		lastProbe = &lp.String
	}
	var pinned string
	_ = s.st.DB().QueryRowContext(ctx, `SELECT pinned_celestia_app FROM publications ORDER BY settlement_height DESC LIMIT 1`).Scan(&pinned)
	writeJSON(w, 200, metaResponse{
		APIVersion: Version, Vantage: s.vantage, VantageInfo: s.info,
		VantageCount: vantages, ObservedFromOneVantage: vantages == 1,
		ChainID: meta["chain_id"], LastScannedHeight: meta["last_scanned_height"], EndpointsHeight: meta["endpoints_height"],
		ProtocolParamsFinger: meta["protocol_params_fingerprint"], PinnedCelestiaApp: pinned,
		Counts: counts, Collector: col, Prober: pr, LastProbeAt: lastProbe, Meta: meta, ServerTime: now.UTC(),
	})
}

// ---- runs (gaps) ----

type runRow struct {
	ID            int64   `json:"id"`
	Component     string  `json:"component"`
	Vantage       string  `json:"vantage"`
	Version       string  `json:"version"`
	StartedAt     string  `json:"started_at"`
	LastHeartbeat string  `json:"last_heartbeat_at"`
	StoppedAt     *string `json:"stopped_at"`
	StopReason    *string `json:"stop_reason"`
}

func (s *Server) handleRuns(w http.ResponseWriter, r *http.Request) {
	win, err := parseWindow(r, time.Now())
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	rows, err := s.st.DB().QueryContext(r.Context(), `SELECT id, component, vantage, version, started_at, last_heartbeat_at, stopped_at, stop_reason
		FROM observer_runs WHERE last_heartbeat_at >= ? ORDER BY started_at`, win.startArg())
	if err != nil {
		s.writeInternal(w, r.URL.Path, err)
		return
	}
	defer rows.Close()
	out := []runRow{}
	for rows.Next() {
		var rr runRow
		if err := rows.Scan(&rr.ID, &rr.Component, &rr.Vantage, &rr.Version, &rr.StartedAt, &rr.LastHeartbeat, &rr.StoppedAt, &rr.StopReason); err != nil {
			s.writeInternal(w, r.URL.Path, err)
			return
		}
		out = append(out, rr)
	}
	writeJSON(w, 200, map[string]any{"window": win, "runs": out})
}

// ---- network ----

type classCounts map[string]int64

type networkResponse struct {
	Window                 Window `json:"window"`
	Vantage                string `json:"vantage"`
	ObservedFromOneVantage bool   `json:"observed_from_one_location"`
	RegisteredEndpoints    int64  `json:"registered_endpoints"`
	ValidatorsProbed       int64  `json:"validators_probed"`
	Reachability           Rate   `json:"reachability"` // endpoints whose latest heartbeat or probe reached TLS
	// ServeRate is HEALTHY / (HEALTHY + FAULT) over assigned probes in the
	// in-window and grace phases. Probes of validators whose storage the
	// settled promise does not prove are classified UNATTESTED and fall out
	// of both sides of this fraction: see Attestation for how many.
	// ServeRate is HEALTHY / (HEALTHY + FAULT) over probes of an assigned
	// shard while the validator was under obligation. FAULT means the
	// observer reached the validator and it failed to hand over a shard the
	// chain proves it stored. Everything the rate does not speak for is in
	// HeldOut, and ExcludedClasses says why each class is out.
	ServeRate Rate `json:"serve_rate"`
	// Coverage is how much of the rate's own population produced a verdict:
	// (HEALTHY + FAULT) over every probe in that population. A high rate over
	// low coverage is a statement about a handful of probes.
	Coverage Rate `json:"serve_rate_coverage"`
	// ByObligation counts one observation per (validator, blob) instead of
	// one per probe. The four in-window probes of one obligation are near
	// copies of each other, so this is the number a confidence interval may
	// honestly be drawn around.
	ByObligation     Rate               `json:"serve_rate_by_obligation"`
	HeldOut          map[string]int64   `json:"serve_rate_held_out"`
	ExcludedClasses  []excludedClass    `json:"serve_rate_excluded_classes"`
	Attestation      attestationStats   `json:"attestation"`
	ProbeCount       int64              `json:"probe_count"` // all probe rows in window
	Classes          classCounts        `json:"classes"`
	Publications     int64              `json:"publications"`
	PublicationBytes int64              `json:"publication_bytes"`
	Reconstructable  reconstructSummary `json:"reconstructable"`
	Gaps             int64              `json:"probe_gaps"` // NOT_PROBED + PROBE_ERROR rows in window
	// GapsByOutcome breaks the gaps down by what actually happened, because
	// they are not all the same thing. RPC_DEADLINE in particular is "the
	// download did not finish in time", and the observer's deadline scales
	// with shard size: a validator that is alive but slow lands there rather
	// than in the rate, and that population is concentrated among exactly the
	// validators most likely to be struggling. Publishing the breakdown is
	// what lets a reader see how big it is.
	GapsByOutcome map[string]int64 `json:"probe_gaps_by_outcome"`
	// VantageHealth is the worst single schedule point in the window: how
	// many distinct validators were unreachable there out of how many were
	// probed. Validators fail independently; this observer's own network does
	// not. A point where nearly every validator was unreachable at once is
	// far more likely to be a route, resolver or peering problem here than
	// twenty operators going down together, and a reader has to be able to
	// see that rather than infer it.
	VantageHealth vantageHealth `json:"vantage_health"`
	// ByPoint is the serve rate per schedule point. The points sit at
	// different fractions of the retention window, so a rate that is fine
	// early and poor late is a different finding from one that is uniformly
	// poor, and the pooled number cannot tell them apart.
	ByPoint []stratum `json:"serve_rate_by_point"`
}

func (s *Server) classCountsWhere(ctx context.Context, where string, args ...any) (classCounts, int64, error) {
	rows, err := s.st.DB().QueryContext(ctx, `SELECT classification, COUNT(*) FROM probes WHERE `+where+` GROUP BY classification`, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := classCounts{}
	var total int64
	for rows.Next() {
		var c string
		var n int64
		if err := rows.Scan(&c, &n); err != nil {
			return nil, 0, err
		}
		out[c] = n
		total += n
	}
	return out, total, rows.Err()
}

// attestationStats discloses what the serve rate left out. A validator is
// only obliged to serve a blob it stored, and the only on-chain proof it
// stored one is a signature on the settled promise that this observer
// verified against the validator's consensus key. Where that proof is
// missing the probe is classified UNATTESTED and excluded from the serve
// rate — in both directions, so neither a success nor a failure can move a
// rate the validator was never proven to owe.
type attestationStats struct {
	// Attested and Unattested are probes of assigned validators in the
	// in-window and grace phases: proven obliged, and not proven obliged.
	Attested   int64 `json:"attested_probes"`
	Unattested int64 `json:"unattested_probes"`
	// Coverage is Attested / (Attested + Unattested). Well below 1 means the
	// publisher stopped collecting signatures once it had a safe quorum, so
	// most of the set is unproven and the serve rate speaks for a minority.
	Coverage Rate `json:"coverage"`
	// Unknown counts probes from records written before the observer verified
	// signatures. Their attestation is absent, not negative, so they stay in
	// the serve rate under the older taxonomy and out of Coverage.
	Unknown int64 `json:"unknown_probes"`
}

// excludedFromRate names the classes published beside the serve rate rather
// than inside it, with the reason each one is out. It lives in one place so a
// page cannot describe the exclusions differently from the API.
//
// The grace phase is outside the rate's population for the same kind of
// reason: a grace probe can only ever add HEALTHY, since NOT_FOUND and
// unreachability there are TOLERATED by design. Including it gave a validator
// that prunes promptly a lower rate than one that over-retains, with
// identical in-window behaviour, and the "worst first" table sorts on exactly
// that axis. Grace probes are still recorded and still shown; they just do
// not move a retention rate.
var excludedFromRate = []excludedClass{
	{"UNATTESTED", "the settled promise carries no verified signature from this validator, so nothing proves it ever stored the shard"},
	{"UNREACHABLE", "the observer could not complete a conversation with the endpoint; from one vantage that is not distinguishable from a problem on the observer's own path"},
	{"NOT_REGISTERED", "the validator had no Fibre host in x/valaddr at the time of the probe; jailing and unbonding remove a provider from the bonded list while the chain keeps the entry"},
	{"SHADOWED_SHARD", "the rows returned verify against the blob commitment but are not this promise's assignment; DownloadShard is addressed by commitment alone, so another promise over the same blob answers in its place"},
	{"IDENTITY_EXPIRED", "the certificate is endorsed by the right consensus key but its signed validity window has lapsed; endpoint hygiene, not a retention failure"},
	{"NOT_PROBED", "the slot elapsed unprobed or the policy sampled it out; a gap in observation, never a zero"},
	{"PROBE_ERROR", "the observer's own probe failed"},
}

type excludedClass struct {
	Class  string `json:"class"`
	Reason string `json:"reason"`
}

// serveRate is HEALTHY over HEALTHY + FAULT, where FAULT means the observer
// reached the validator and it failed to hand over a shard it was proven to
// hold. Every other class is published under its own name beside the rate.
func serveRate(c classCounts) Rate {
	return rate(c["HEALTHY"], c["HEALTHY"]+c["FAULT"])
}

// obligationRate counts one observation per (validator, blob) rather than one
// per probe.
//
// The schedule visits the same validator and blob four times in window, and
// every bonded validator is assigned every blob because of the minimum-rows
// floor, so the probes inside one obligation are near-perfectly correlated: a
// certificate that lapsed, or a disk that lost a shard, produces four FAULT
// rows for one event. Counting those as four independent trials makes any
// confidence interval far narrower than the evidence supports, which is the
// wrong error to make under a public accusation. An obligation is kept when
// no probe of it faulted.
func (s *Server) obligationRate(ctx context.Context, where string, args ...any) (Rate, error) {
	var kept, broken int64
	err := s.st.DB().QueryRowContext(ctx, `SELECT
			COALESCE(SUM(CASE WHEN f = 0 THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN f > 0 THEN 1 ELSE 0 END), 0)
		FROM (
			SELECT SUM(CASE WHEN classification = 'FAULT' THEN 1 ELSE 0 END) AS f,
			       SUM(CASE WHEN classification IN ('HEALTHY','FAULT') THEN 1 ELSE 0 END) AS rated
			FROM probes WHERE `+where+`
			GROUP BY validator_address, promise_hash
		) WHERE rated > 0`, args...).Scan(&kept, &broken)
	if err != nil {
		return Rate{}, err
	}
	return rate(kept, kept+broken), nil
}

// vantageHealth reports the most correlated failure the window contains.
type vantageHealth struct {
	// WorstPoint is the fraction unreachable at the worst schedule point.
	WorstPoint Rate `json:"worst_point"`
	// At and Label identify that point, so it can be looked up in /v1/probes.
	At    string `json:"at,omitempty"`
	Label string `json:"label,omitempty"`
	// Correlated is true when that fraction is at or above the threshold
	// below, which is the observer saying it does not trust its own reading
	// at that point.
	Correlated bool `json:"correlated"`
	// Threshold is published so the judgement is not a hidden constant.
	Threshold float64 `json:"threshold"`
}

// correlatedUnreachableThreshold: above this share of the validators probed at
// one schedule point being unreachable, the likeliest explanation is this
// observer's own network rather than that many independent operators.
const correlatedUnreachableThreshold = 0.5

// worstCorrelatedPoint finds the schedule point in the window with the highest
// share of distinct validators unreachable at once.
func (s *Server) worstCorrelatedPoint(ctx context.Context, win Window) (vantageHealth, error) {
	out := vantageHealth{Threshold: correlatedUnreachableThreshold}
	rows, err := s.st.DB().QueryContext(ctx, `SELECT scheduled_at, schedule_label,
			COUNT(DISTINCT CASE WHEN classification = 'UNREACHABLE' THEN validator_address END),
			COUNT(DISTINCT validator_address)
		FROM probes
		WHERE started_at >= ? AND assigned = 1 AND phase = 'in_window'
		GROUP BY scheduled_at HAVING COUNT(DISTINCT validator_address) > 1`, win.startArg())
	if err != nil {
		return out, err
	}
	defer rows.Close()
	var best float64
	for rows.Next() {
		var at, label string
		var bad, all int64
		if err := rows.Scan(&at, &label, &bad, &all); err != nil {
			return out, err
		}
		if all == 0 {
			continue
		}
		if f := float64(bad) / float64(all); f > best || out.At == "" {
			best = f
			out.WorstPoint = rate(bad, all)
			out.At, out.Label = at, label
		}
	}
	out.Correlated = out.WorstPoint.Den > 0 && best >= correlatedUnreachableThreshold
	return out, rows.Err()
}

// byPoint breaks a rate down by schedule point. The four in-window points are
// deliberately packed toward the deadline (0.12, 0.45, 0.72, 0.92 of the
// window), so they are not interchangeable: a validator that prunes early
// fails late points and passes early ones, and a validator with a broken disk
// fails all four. Pooling them hides which of those two a low rate is, and
// two validators probed over different mixes of blob sizes and points can
// have their pooled rates reverse relative to their per-stratum ones. The
// breakdown is published so a reader can look rather than assume.
func (s *Server) rateByPoint(ctx context.Context, where string, args ...any) ([]stratum, error) {
	rows, err := s.st.DB().QueryContext(ctx, `SELECT schedule_label,
			COALESCE(SUM(CASE WHEN classification = 'HEALTHY' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN classification = 'FAULT' THEN 1 ELSE 0 END), 0)
		FROM probes WHERE `+where+` GROUP BY schedule_label ORDER BY schedule_label`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []stratum{}
	for rows.Next() {
		var st stratum
		var ok, bad int64
		if err := rows.Scan(&st.Key, &ok, &bad); err != nil {
			return nil, err
		}
		st.Rate = rate(ok, ok+bad)
		out = append(out, st)
	}
	return out, rows.Err()
}

// stratum is one slice of a rate's population, with the rate over that slice.
type stratum struct {
	Key  string `json:"key"`
	Rate Rate   `json:"serve_rate"`
}

// coverage is how much of the rate's own population produced a verdict.
// Without it a reader cannot tell a rate resting on twelve probes from one
// resting on four hundred scheduled slots, and the probe and gap counts
// published next to it are over a different population entirely.
func coverage(c classCounts) Rate {
	var rated, all int64
	for cls, n := range c {
		all += n
		if cls == "HEALTHY" || cls == "FAULT" {
			rated += n
		}
	}
	return rate(rated, all)
}

// heldOut counts, per excluded class, how many probes of this population the
// rate does not speak for.
func heldOut(c classCounts) map[string]int64 {
	out := map[string]int64{}
	for _, e := range excludedFromRate {
		if n := c[e.Class]; n > 0 {
			out[e.Class] = n
		}
	}
	return out
}

// attestationWhere counts proven, unproven and unknown obligations over the
// probe rows matching where.
func (s *Server) attestationWhere(ctx context.Context, where string, args ...any) (attestationStats, error) {
	var st attestationStats
	err := s.st.DB().QueryRowContext(ctx, `SELECT
			COALESCE(SUM(CASE WHEN attested = 1 THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN attested = 0 THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN attested IS NULL THEN 1 ELSE 0 END), 0)
		FROM probes WHERE `+where, args...).Scan(&st.Attested, &st.Unattested, &st.Unknown)
	if err != nil {
		return attestationStats{}, err
	}
	st.Coverage = rate(st.Attested, st.Attested+st.Unattested)
	return st, nil
}

func (s *Server) handleNetwork(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	now := time.Now()
	win, err := parseWindow(r, now)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	db := s.st.DB()
	var resp networkResponse
	resp.Window, resp.Vantage = win, s.vantage
	resp.ObservedFromOneVantage = s.vantageCount(ctx) == 1

	_ = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM endpoints WHERE closed_at IS NULL`).Scan(&resp.RegisteredEndpoints)
	_ = db.QueryRowContext(ctx, `SELECT COUNT(DISTINCT validator_address) FROM probes WHERE started_at >= ?`, win.startArg()).Scan(&resp.ValidatorsProbed)

	classes, total, err := s.classCountsWhere(ctx, `started_at >= ? AND assigned = 1 AND phase = 'in_window'`, win.startArg())
	if err != nil {
		s.writeInternal(w, r.URL.Path, err)
		return
	}
	resp.Classes = classes
	resp.ServeRate = serveRate(classes)
	resp.Coverage = coverage(classes)
	resp.HeldOut = heldOut(classes)
	resp.ExcludedClasses = excludedFromRate
	if resp.ByObligation, err = s.obligationRate(ctx,
		`started_at >= ? AND assigned = 1 AND phase = 'in_window'`, win.startArg()); err != nil {
		s.writeInternal(w, r.URL.Path, err)
		return
	}
	_ = total
	if resp.Attestation, err = s.attestationWhere(ctx,
		`started_at >= ? AND assigned = 1 AND phase = 'in_window'`, win.startArg()); err != nil {
		s.writeInternal(w, r.URL.Path, err)
		return
	}
	_ = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM probes WHERE started_at >= ?`, win.startArg()).Scan(&resp.ProbeCount)
	_ = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM probes WHERE started_at >= ? AND classification IN ('NOT_PROBED','PROBE_ERROR')`, win.startArg()).Scan(&resp.Gaps)
	resp.GapsByOutcome = map[string]int64{}
	if grows, gerr := db.QueryContext(ctx,
		`SELECT outcome, COUNT(*) FROM probes WHERE started_at >= ? AND classification IN ('NOT_PROBED','PROBE_ERROR') GROUP BY outcome`,
		win.startArg()); gerr == nil {
		for grows.Next() {
			var o string
			var n int64
			if err := grows.Scan(&o, &n); err == nil {
				resp.GapsByOutcome[o] = n
			}
		}
		grows.Close()
	}

	if resp.VantageHealth, err = s.worstCorrelatedPoint(ctx, win); err != nil {
		s.writeInternal(w, r.URL.Path, err)
		return
	}
	if resp.ByPoint, err = s.rateByPoint(ctx,
		`started_at >= ? AND assigned = 1 AND phase = 'in_window'`, win.startArg()); err != nil {
		s.writeInternal(w, r.URL.Path, err)
		return
	}

	reach, err := s.reachabilityNow(ctx)
	if err != nil {
		s.writeInternal(w, r.URL.Path, err)
		return
	}
	var reachable int64
	for _, v := range reach {
		if v.reachable {
			reachable++
		}
	}
	resp.Reachability = rate(reachable, int64(len(reach)))

	_ = db.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(blob_size),0) FROM publications WHERE settlement_time >= ?`, win.startArg()).Scan(&resp.Publications, &resp.PublicationBytes)

	recon, err := s.reconstructableCount(ctx, win)
	if err != nil {
		s.writeInternal(w, r.URL.Path, err)
		return
	}
	resp.Reconstructable = recon
	writeJSON(w, 200, resp)
}

// reachState is the latest reachability evidence for one validator: the most
// recent heartbeat or probe row, whichever is newer.
type reachState struct {
	at             string
	host           string
	reachable      bool // TCP and TLS ok
	identityOK     bool
	identityReason string
	source         string // heartbeat | probe
}

func (s *Server) reachabilityNow(ctx context.Context) (map[string]reachState, error) {
	out := map[string]reachState{}
	// The newest row per validator is the highest rowid: both files are
	// ingested in write order. MAX(rowid) GROUP BY uses the validator index
	// instead of a correlated MAX(started_at) per row over the whole table.
	q := `SELECT validator_address, validator_host, started_at, tcp_ok, tls_ok, identity_ok, identity_reason, 'heartbeat' FROM reachability
	      WHERE rowid IN (SELECT MAX(rowid) FROM reachability GROUP BY validator_address)
	      UNION ALL
	      SELECT validator_address, validator_host, started_at, tcp_ok, tls_ok, identity_ok, identity_reason, 'probe' FROM probes
	      WHERE rowid IN (SELECT MAX(rowid) FROM probes WHERE outcome NOT IN ('MISSED','PROBE_ERROR') GROUP BY validator_address)`
	rows, err := s.st.DB().QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var addr string
		var st reachState
		var tcp, tls, id int
		if err := rows.Scan(&addr, &st.host, &st.at, &tcp, &tls, &id, &st.identityReason, &st.source); err != nil {
			return nil, err
		}
		st.reachable = tcp == 1 && tls == 1
		st.identityOK = id == 1
		if cur, ok := out[addr]; !ok || st.at > cur.at {
			out[addr] = st
		}
	}
	return out, rows.Err()
}

// ---- validators ----

type validatorRow struct {
	Address        string  `json:"address"`      // 20-byte consensus address, hex
	ConsAddress    string  `json:"cons_address"` // celestiavalcons1... when known from the registry
	Host           string  `json:"host"`
	EndpointSince  *string `json:"endpoint_since"`
	VotingPower    int64   `json:"voting_power"` // from the latest assignment seen
	LastSeenAt     *string `json:"last_seen_at"`
	Reachable      *bool   `json:"reachable"`       // latest heartbeat or probe; null if never probed
	IdentityStatus string  `json:"identity_status"` // verified | mismatch | no_tls | unreachable | unknown
	IdentityReason string  `json:"identity_reason,omitempty"`
	// ServeRate is HEALTHY / (HEALTHY + FAULT) over this validator's assigned
	// probes in the in-window and grace phases. Probes where the settled
	// promise does not prove this validator stored the blob are UNATTESTED
	// and sit outside the fraction, so an unproven obligation can neither
	// reward nor punish it. Attestation says how many those were.
	ServeRate Rate `json:"serve_rate"`
	// Coverage and HeldOut carry the same meaning as on /v1/network: how much
	// of this validator's own obligation population produced a verdict, and
	// what the rate does not speak for.
	Coverage Rate `json:"serve_rate_coverage"`
	// ByObligation counts one observation per (validator, blob) instead of
	// one per probe. The four in-window probes of one obligation are near
	// copies of each other, so this is the number a confidence interval may
	// honestly be drawn around.
	ByObligation     Rate             `json:"serve_rate_by_obligation"`
	HeldOut          map[string]int64 `json:"serve_rate_held_out"`
	Attestation      attestationStats `json:"attestation"`
	ProbeCount       int64            `json:"probe_count"`
	Classes          classCounts      `json:"classes"`
	AssignedRowsLast int              `json:"assigned_rows_last"`
	ExpectedLoadBand string           `json:"expected_load_band"` // floor | low | mid | high, by assigned rows
	// AttestedLast reports whether the newest publication this validator
	// appears in proves it stored that blob: true, false (assigned but
	// unproven) or null (recorded before the observer verified signatures).
	AttestedLast *bool `json:"attested_last"`
	// AssignmentHeight is the settlement height the voting power, row count
	// and attestation above were read at. It is the newest publication this
	// validator appears in, which is not necessarily the newest publication:
	// a validator that has left the set keeps the figures from when it was
	// last assigned, and this says when that was.
	AssignmentHeight int64 `json:"assignment_height"`
}

func loadBand(rows int) string {
	switch {
	case rows <= 0:
		return ""
	case rows <= 148:
		return "floor"
	case rows < 1024:
		return "low"
	case rows < 2731:
		return "mid"
	default:
		return "high"
	}
}

func identityStatus(st *reachState) string {
	if st == nil {
		return "unknown"
	}
	if !st.reachable {
		return "unreachable"
	}
	if st.identityOK {
		return "verified"
	}
	if st.identityReason != "" {
		return "mismatch"
	}
	// TCP and TLS both succeeded, but no identity verdict was recorded (an
	// older row, or a probe that stopped before the identity step).
	return "unverified"
}

func (s *Server) validatorRows(ctx context.Context, win Window, only string) ([]validatorRow, error) {
	db := s.st.DB()
	byAddr := map[string]*validatorRow{}
	get := func(addr string) *validatorRow {
		v, ok := byAddr[addr]
		if !ok {
			v = &validatorRow{Address: addr, Classes: classCounts{}, IdentityStatus: "unknown"}
			byAddr[addr] = v
		}
		return v
	}
	// registry: every open endpoint (bech32 -> hex)
	eps, err := s.st.CurrentEndpoints(ctx)
	if err != nil {
		return nil, err
	}
	for _, e := range eps {
		hexAddr, err := consHex(e.ValidatorConsAddress)
		if err != nil {
			continue
		}
		v := get(hexAddr)
		v.ConsAddress, v.Host = e.ValidatorConsAddress, e.Host
		since := e.FirstSeenAt
		v.EndpointSince = &since
	}
	// Voting power and row count come from the newest publication each
	// validator actually appears in, not from the newest publication overall.
	// Reading them from the latest publication alone rendered a validator
	// that had left the set as zero power with zero rows beside its fault
	// count, and the table sorts faults to the top: the row that looked worst
	// was the one we had the least current information about. The height the
	// figures come from is published with them.
	rows, err := db.QueryContext(ctx, `SELECT a.validator_address, a.voting_power, a.row_count, a.attested, p.settlement_height
		FROM assignments a
		JOIN publications p ON p.promise_hash = a.promise_hash
		JOIN (
			SELECT a2.validator_address AS va, MAX(p2.settlement_height) AS h
			FROM assignments a2 JOIN publications p2 ON p2.promise_hash = a2.promise_hash
			GROUP BY a2.validator_address
		) m ON m.va = a.validator_address AND m.h = p.settlement_height`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var addr string
		var vp int64
		var rc int
		var att sql.NullInt64
		var h int64
		if err := rows.Scan(&addr, &vp, &rc, &att, &h); err != nil {
			rows.Close()
			return nil, err
		}
		v := get(addr)
		// A block can carry several publications; keep whichever row we see
		// with the highest height, and break the tie deterministically.
		if v.AssignmentHeight > h {
			continue
		}
		v.AssignmentHeight = h
		v.VotingPower, v.AssignedRowsLast, v.ExpectedLoadBand = vp, rc, loadBand(rc)
		v.AttestedLast = nil
		if att.Valid {
			b := att.Int64 == 1
			v.AttestedLast = &b
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// classes per validator in window
	rows, err = db.QueryContext(ctx, `SELECT validator_address, classification, COUNT(*) FROM probes
		WHERE started_at >= ? AND assigned = 1 AND phase = 'in_window' GROUP BY validator_address, classification`, win.startArg())
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var addr, c string
		var n int64
		if err := rows.Scan(&addr, &c, &n); err != nil {
			rows.Close()
			return nil, err
		}
		v := get(addr)
		v.Classes[c] = n
	}
	rows.Close()
	// proven / unproven / unknown obligations per validator, same scope as
	// the classes above so the serve rate and its exclusions line up.
	rows, err = db.QueryContext(ctx, `SELECT validator_address,
			COALESCE(SUM(CASE WHEN attested = 1 THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN attested = 0 THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN attested IS NULL THEN 1 ELSE 0 END), 0)
		FROM probes WHERE started_at >= ? AND assigned = 1 AND phase = 'in_window'
		GROUP BY validator_address`, win.startArg())
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var addr string
		var at, un, unk int64
		if err := rows.Scan(&addr, &at, &un, &unk); err != nil {
			rows.Close()
			return nil, err
		}
		v := get(addr)
		v.Attestation = attestationStats{Attested: at, Unattested: un, Unknown: unk, Coverage: rate(at, at+un)}
	}
	rows.Close()
	rows, err = db.QueryContext(ctx, `SELECT validator_address, COUNT(*), MAX(started_at) FROM probes WHERE started_at >= ? GROUP BY validator_address`, win.startArg())
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var addr, last string
		var n int64
		if err := rows.Scan(&addr, &n, &last); err != nil {
			rows.Close()
			return nil, err
		}
		v := get(addr)
		v.ProbeCount = n
		l := last
		v.LastSeenAt = &l
	}
	rows.Close()
	reach, err := s.reachabilityNow(ctx)
	if err != nil {
		return nil, err
	}
	for addr, st := range reach {
		v := get(addr)
		r := st.reachable
		v.Reachable = &r
		stc := st
		v.IdentityStatus = identityStatus(&stc)
		v.IdentityReason = st.identityReason
		if v.Host == "" {
			v.Host = st.host
		}
		if v.LastSeenAt == nil || st.at > *v.LastSeenAt {
			at := st.at
			v.LastSeenAt = &at
		}
	}
	// one observation per (validator, blob): see obligationRate.
	byObligation := map[string]Rate{}
	orows, err := db.QueryContext(ctx, `SELECT validator_address,
			COALESCE(SUM(CASE WHEN f = 0 THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN f > 0 THEN 1 ELSE 0 END), 0)
		FROM (
			SELECT validator_address, promise_hash,
			       SUM(CASE WHEN classification = 'FAULT' THEN 1 ELSE 0 END) AS f,
			       SUM(CASE WHEN classification IN ('HEALTHY','FAULT') THEN 1 ELSE 0 END) AS rated
			FROM probes WHERE started_at >= ? AND assigned = 1 AND phase = 'in_window'
			GROUP BY validator_address, promise_hash
		) WHERE rated > 0 GROUP BY validator_address`, win.startArg())
	if err != nil {
		return nil, err
	}
	for orows.Next() {
		var addr string
		var kept, broken int64
		if err := orows.Scan(&addr, &kept, &broken); err != nil {
			orows.Close()
			return nil, err
		}
		byObligation[addr] = rate(kept, kept+broken)
	}
	orows.Close()
	if err := orows.Err(); err != nil {
		return nil, err
	}

	out := make([]validatorRow, 0, len(byAddr))
	for addr, v := range byAddr {
		if only != "" && addr != only {
			continue
		}
		v.ServeRate = serveRate(v.Classes)
		v.Coverage = coverage(v.Classes)
		v.HeldOut = heldOut(v.Classes)
		v.ByObligation = byObligation[addr]
		out = append(out, *v)
	}
	// voting power desc, then address
	sortRows(out)
	return out, nil
}

func sortRows(v []validatorRow) {
	for i := 1; i < len(v); i++ {
		for j := i; j > 0; j-- {
			a, b := v[j-1], v[j]
			if a.VotingPower > b.VotingPower || (a.VotingPower == b.VotingPower && a.Address <= b.Address) {
				break
			}
			v[j-1], v[j] = v[j], v[j-1]
		}
	}
}

func consHex(bech string) (string, error) {
	hrp, raw, err := bech32.DecodeAndConvert(bech)
	if err != nil {
		return "", err
	}
	// An operator address (…valoper1…) and an account address decode to 20
	// bytes just as well, and would silently be looked up as a consensus
	// address that can never match.
	if !strings.HasSuffix(hrp, "valcons") {
		return "", fmt.Errorf("address prefix %q is not a consensus address (…valcons1…)", hrp)
	}
	if len(raw) != 20 {
		return "", fmt.Errorf("consensus address %d bytes, want 20", len(raw))
	}
	return hex.EncodeToString(raw), nil
}

// parseAddr accepts a 40-hex consensus address or a bech32 celestiavalcons address.
func parseAddr(s string) (string, error) {
	s = strings.TrimSpace(strings.ToLower(s))
	if len(s) == 40 {
		if _, err := hex.DecodeString(s); err == nil {
			return s, nil
		}
	}
	return consHex(s)
}

func (s *Server) handleValidators(w http.ResponseWriter, r *http.Request) {
	win, err := parseWindow(r, time.Now())
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	rows, err := s.validatorRows(r.Context(), win, "")
	if err != nil {
		s.writeInternal(w, r.URL.Path, err)
		return
	}
	writeJSON(w, 200, map[string]any{"window": win, "vantage": s.vantage, "validators": rows})
}

func (s *Server) handleValidator(w http.ResponseWriter, r *http.Request) {
	addr, err := parseAddr(r.PathValue("addr"))
	if err != nil {
		writeErr(w, 400, "address must be 40 hex chars or celestiavalcons1...")
		return
	}
	now := time.Now()
	ctx := r.Context()
	// The embedded validator object is built over a window like every other
	// response, and the window it was built over is echoed at the top level.
	// It used to be pinned to 24h with nothing saying so, so a caller reading
	// the raw JSON had a rate with no window attached to it.
	win, err := parseWindow(r, now)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	type span struct {
		Window Window `json:"window"`
		Rate   Rate   `json:"serve_rate"`
		// Count is the assigned in-window probes this window's rate is built
		// from, not every row for the validator (validator.probe_count).
		Count        int64            `json:"rated_probe_count"`
		Coverage     Rate             `json:"serve_rate_coverage"`
		ByObligation Rate             `json:"serve_rate_by_obligation"`
		HeldOut      map[string]int64 `json:"serve_rate_held_out"`
		Classes      classCounts      `json:"classes"`
	}
	var spans []span
	for _, name := range []string{"24h", "7d", "30d", "all"} {
		sw := Window{Name: name, Span: windows[name], End: now}
		if sw.Span > 0 {
			sw.Start = now.Add(-sw.Span)
		}
		classes, total, err := s.classCountsWhere(ctx, `validator_address = ? AND started_at >= ? AND assigned = 1 AND phase = 'in_window'`, addr, sw.startArg())
		if err != nil {
			s.writeInternal(w, r.URL.Path, err)
			return
		}
		obl, err := s.obligationRate(ctx, `validator_address = ? AND started_at >= ? AND assigned = 1 AND phase = 'in_window'`, addr, sw.startArg())
		if err != nil {
			s.writeInternal(w, r.URL.Path, err)
			return
		}
		spans = append(spans, span{
			Window: sw, Rate: serveRate(classes), Count: total,
			Coverage: coverage(classes), ByObligation: obl, HeldOut: heldOut(classes), Classes: classes,
		})
	}
	rows, err := s.validatorRows(ctx, win, addr)
	if err != nil {
		s.writeInternal(w, r.URL.Path, err)
		return
	}
	if len(rows) == 0 {
		writeErr(w, 404, "validator not seen in the registry or in any probe")
		return
	}
	probes, err := s.probeRows(ctx, `validator_address = ?`, 50, addr)
	if err != nil {
		s.writeInternal(w, r.URL.Path, err)
		return
	}
	writeJSON(w, 200, map[string]any{
		"window":                      win,
		"validator":                   rows[0],
		"windows":                     spans,
		"recent_probes":               probes,
		"serve_rate_excluded_classes": excludedFromRate,
		"vantage":                     s.vantage,
	})
}

// ---- blobs ----

type blobRow struct {
	PromiseHash        string       `json:"promise_hash"`
	Commitment         string       `json:"commitment"`
	Namespace          string       `json:"namespace"`
	BlobSize           int64        `json:"blob_size"`
	Signer             string       `json:"signer"`
	SettlementHeight   int64        `json:"settlement_height"`
	SettlementTime     string       `json:"settlement_time"`
	CreationTimestamp  string       `json:"creation_timestamp"`
	MustServeUntil     string       `json:"must_serve_until"`
	ValidatorsWithRows int          `json:"validators_with_rows"`
	SigmaRows          int          `json:"sigma_rows"`
	DistinctRows       int          `json:"distinct_rows"`
	AssignmentError    string       `json:"assignment_error,omitempty"`
	ProbeCount         int64        `json:"probe_count"`
	Classes            classCounts  `json:"classes"`
	Reconstructable    *reconstruct `json:"reconstructable"`
}

// reconstruct is the per-blob reconstructability verdict at the latest
// in-window schedule point that has probes: distinct rows held by
// validators that served correctly versus the rows needed (OriginalRows).
// Grace and post points are never used: "not found" is tolerated or
// expected there, so a quiet grace point says nothing about the promise.
// WindowOver says whether the obligation has ended since.
type reconstruct struct {
	Status        string `json:"status"` // yes | degraded | no | pending | unknown
	Point         string `json:"point"`  // schedule label the verdict is taken at
	PointAt       string `json:"point_at"`
	WindowOver    bool   `json:"window_over"`
	ServedRows    int    `json:"served_distinct_rows"`
	NeededRows    int    `json:"needed_rows"`
	ServedBy      int    `json:"served_by_validators"`
	AssignedTotal int    `json:"assigned_validators"`
	// TotalRows is the blob's encoded row count (16384 for blob v0): the
	// denominator a reader should draw the served rows against.
	TotalRows int `json:"total_rows"`
	// ProbedValidators is how many assigned validators have a real result
	// (not a gap) at the point. Status is "pending" while it is short of
	// assigned_validators: the sweep is still running or was skipped by the
	// policy, and an absent row is a gap, never a zero.
	ProbedValidators int `json:"probed_validators"`
	// AttestedValidators is how many of the assigned validators the settled
	// promise proves stored the blob. It is the denominator for "yes": a
	// validator with no proof of storage cannot demote the verdict by not
	// serving. AttestationKnown is false for publications recorded before the
	// observer verified signatures, where the whole assigned set is used
	// instead.
	AttestedValidators int  `json:"attested_validators"`
	AttestationKnown   bool `json:"attestation_known"`
	// ServedByAttested is how many proven-obliged validators served correctly
	// at the point.
	ServedByAttested int `json:"served_by_attested"`
}

func (s *Server) blobRows(ctx context.Context, where string, limit int, args ...any) ([]blobRow, error) {
	q := `SELECT promise_hash, commitment, namespace, blob_size, signer, settlement_height, settlement_time, creation_timestamp,
		must_serve_until, validators_with_rows, sigma_rows, distinct_rows, assignment_error FROM publications`
	if where != "" {
		q += " WHERE " + where
	}
	q += " ORDER BY settlement_height DESC, settlement_tx_index DESC LIMIT " + strconv.Itoa(limit)
	rows, err := s.st.DB().QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []blobRow
	for rows.Next() {
		var b blobRow
		if err := rows.Scan(&b.PromiseHash, &b.Commitment, &b.Namespace, &b.BlobSize, &b.Signer, &b.SettlementHeight, &b.SettlementTime,
			&b.CreationTimestamp, &b.MustServeUntil, &b.ValidatorsWithRows, &b.SigmaRows, &b.DistinctRows, &b.AssignmentError); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		classes, total, err := s.classCountsWhere(ctx, `promise_hash = ?`, out[i].PromiseHash)
		if err != nil {
			return nil, err
		}
		out[i].Classes, out[i].ProbeCount = classes, total
		rc, err := s.reconstructable(ctx, out[i].PromiseHash)
		if err != nil {
			return nil, err
		}
		out[i].Reconstructable = rc
	}
	return out, nil
}

// reconstructable computes the verdict at the latest COMPLETE in-window
// schedule point: the newest point at which every assigned validator has a
// real result (HEALTHY, FAULT, ... but not NOT_PROBED or PROBE_ERROR). The
// distinct row indices of validators that served correctly there are
// compared to OriginalRows. If no point is complete yet, the newest point in
// progress is reported with status "pending": a validator without a row is
// a gap in observation, not a validator that failed to serve.
func (s *Server) reconstructable(ctx context.Context, hash string) (*reconstruct, error) {
	db := s.st.DB()
	var assigned int
	var needed, total sql.NullInt64
	err := db.QueryRowContext(ctx, `SELECT validators_with_rows,
			json_extract(raw_json, '$.assignment.protocol_params.original_rows'),
			json_extract(raw_json, '$.assignment.protocol_params.total_rows')
		FROM publications WHERE promise_hash = ?`, hash).Scan(&assigned, &needed, &total)
	if errors.Is(err, sql.ErrNoRows) {
		return &reconstruct{Status: "unknown"}, nil
	}
	if err != nil {
		return nil, err
	}

	// How many assigned validators the promise proves stored the blob.
	// COUNT(attested) skips NULLs, so knownAtt = 0 means this publication
	// predates signature verification and attestation says nothing here.
	var knownAtt, attested int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(attested), COALESCE(SUM(attested), 0) FROM assignments WHERE promise_hash = ? AND row_count > 0`,
		hash).Scan(&knownAtt, &attested); err != nil {
		return nil, err
	}
	attestationKnown := knownAtt > 0

	// in-window points with real results, newest first, with how many
	// distinct assigned validators answered at each.
	rows, err := db.QueryContext(ctx, `SELECT scheduled_at, schedule_label, must_serve_until, COUNT(DISTINCT validator_address) FROM probes
		WHERE promise_hash = ? AND phase = 'in_window' AND assigned = 1
		  AND classification NOT IN ('NOT_PROBED','PROBE_ERROR')
		GROUP BY scheduled_at ORDER BY scheduled_at DESC`, hash)
	if err != nil {
		return nil, err
	}
	var pointAt, label, msu string
	var probed int
	complete := false
	first := true
	for rows.Next() {
		var at, lb, m string
		var n int
		if err := rows.Scan(&at, &lb, &m, &n); err != nil {
			rows.Close()
			return nil, err
		}
		if first {
			pointAt, label, msu, probed = at, lb, m, n
			first = false
		}
		if n >= assigned && assigned > 0 {
			pointAt, label, msu, probed = at, lb, m, n
			complete = true
			break
		}
	}
	rows.Close()
	if first {
		return &reconstruct{Status: "unknown"}, nil
	}
	windowOver := false
	if t, err := time.Parse(store.TimeLayout, msu); err == nil {
		windowOver = time.Now().After(t)
	} else if t, err := time.Parse(time.RFC3339Nano, msu); err == nil {
		windowOver = time.Now().After(t)
	}

	// distinct validators that served correctly at the point (any vantage
	// counts once) and the union of their assigned rows.
	srows, err := db.QueryContext(ctx, `SELECT DISTINCT p.validator_address, a.rows_json, a.attested FROM probes p
		JOIN assignments a ON a.promise_hash = p.promise_hash AND a.validator_address = p.validator_address
		WHERE p.promise_hash = ? AND p.scheduled_at = ? AND p.assigned = 1 AND p.outcome = 'SERVED_OK'`, hash, pointAt)
	if err != nil {
		return nil, err
	}
	defer srows.Close()
	served := map[int]struct{}{}
	servedBy, servedAtt := 0, 0
	rowsKnown := true
	for srows.Next() {
		var addr string
		var rj sql.NullString
		var att sql.NullInt64
		if err := srows.Scan(&addr, &rj, &att); err != nil {
			return nil, err
		}
		servedBy++
		if att.Valid && att.Int64 == 1 {
			servedAtt++
		}
		// A shard this validator served counts toward reconstruction whether
		// or not its storage was proven: the rows came back, so the data was
		// there. Attestation decides blame, never availability.
		if !rj.Valid {
			rowsKnown = false
			continue
		}
		var idx []int
		if err := json.Unmarshal([]byte(rj.String), &idx); err != nil {
			rowsKnown = false
			continue
		}
		for _, i := range idx {
			served[i] = struct{}{}
		}
	}
	rc := &reconstruct{Point: label, PointAt: pointAt, WindowOver: windowOver, NeededRows: int(needed.Int64),
		TotalRows: int(total.Int64), ServedBy: servedBy, AssignedTotal: assigned, ServedRows: len(served),
		ProbedValidators: probed, AttestedValidators: attested, AttestationKnown: attestationKnown,
		ServedByAttested: servedAtt}

	// "yes" means nobody who was proven to owe this blob failed to serve it.
	// Without proof of storage, a validator that stayed quiet is not a fault,
	// so it must not demote a blob whose rows all came back.
	whole := servedBy == assigned
	if attestationKnown {
		whole = servedAtt == attested
	}
	switch {
	case !rowsKnown || !needed.Valid || needed.Int64 == 0:
		rc.Status = "unknown"
	case !complete:
		rc.Status = "pending"
	case len(served) >= rc.NeededRows && whole:
		rc.Status = "yes"
	case len(served) >= rc.NeededRows:
		rc.Status = "degraded"
	default:
		rc.Status = "no"
	}
	return rc, nil
}

// reconstructSample bounds how many of the newest publications the network
// reconstructability rate is computed over per request. The bound is real and
// is published: reconstructSummary carries how many publications the window
// holds and how many were examined, so a rate over the newest 2000 of 50000
// cannot be read as a rate over the window.
const reconstructSample = 2000

// reconstructSummary is the network reconstructability figure with everything
// a reader needs to know what it covers. "Degraded" is reported on its own
// rather than folded into the numerator: it means the rows were all there but
// a validator proven to owe the blob did not answer, which is not the same
// statement as "the blob could be rebuilt with everyone serving".
type reconstructSummary struct {
	// Rate is fully-served publications over those with a verdict.
	Rate Rate `json:"rate"`
	// Recoverable counts publications where enough distinct rows came back to
	// rebuild the blob, whether or not every obliged validator answered. This
	// is the availability question; Rate is the compliance one.
	Recoverable Rate  `json:"recoverable"`
	Yes         int64 `json:"yes"`
	Degraded    int64 `json:"degraded"`
	No          int64 `json:"no"`
	// Pending and Unknown are publications with no verdict yet: a sweep still
	// running, or row lists that were not recorded.
	Pending int64 `json:"pending"`
	Unknown int64 `json:"unknown"`
	// PublicationsInWindow is every publication the window holds; Examined is
	// how many this request actually looked at. They differ when the window
	// holds more than the sample bound, and the dashboard says so when they do.
	PublicationsInWindow int64 `json:"publications_in_window"`
	Examined             int64 `json:"publications_examined"`
	SampleLimit          int   `json:"sample_limit"`
}

func (s *Server) reconstructableCount(ctx context.Context, win Window) (reconstructSummary, error) {
	out := reconstructSummary{SampleLimit: reconstructSample}
	if err := s.st.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM publications WHERE settlement_time >= ?`, win.startArg()).Scan(&out.PublicationsInWindow); err != nil {
		return out, err
	}
	blobs, err := s.blobRows(ctx, `settlement_time >= ?`, reconstructSample, win.startArg())
	if err != nil {
		return out, err
	}
	out.Examined = int64(len(blobs))
	for _, b := range blobs {
		if b.Reconstructable == nil {
			out.Unknown++
			continue
		}
		switch b.Reconstructable.Status {
		case "unknown":
			out.Unknown++
		case "pending":
			out.Pending++
		case "yes":
			out.Yes++
		case "degraded":
			out.Degraded++
		default:
			out.No++
		}
	}
	den := out.Yes + out.Degraded + out.No
	out.Rate = rate(out.Yes, den)
	out.Recoverable = rate(out.Yes+out.Degraded, den)
	return out, nil
}

func (s *Server) handleBlobs(w http.ResponseWriter, r *http.Request) {
	limit, err := parseLimit(r, 50, 500)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	var where string
	var args []any
	if ns := r.URL.Query().Get("namespace"); ns != "" {
		where, args = `namespace = ?`, []any{strings.ToLower(ns)}
	}
	if before := r.URL.Query().Get("before_height"); before != "" {
		// The cursor is (height, tx_index) because a block can carry several
		// publications: "< height" alone drops the rest of the block the page
		// boundary fell inside. before_tx_index defaults to 0, which with
		// the tuple comparison means "everything before this height".
		h, err := strconv.ParseInt(before, 10, 64)
		if err != nil {
			writeErr(w, 400, "before_height must be an integer")
			return
		}
		idx := int64(0)
		if raw := r.URL.Query().Get("before_tx_index"); raw != "" {
			idx, err = strconv.ParseInt(raw, 10, 64)
			if err != nil || idx < 0 {
				writeErr(w, 400, "before_tx_index must be a non-negative integer")
				return
			}
		}
		if where != "" {
			where += " AND "
		}
		where += `(settlement_height < ? OR (settlement_height = ? AND settlement_tx_index < ?))`
		args = append(args, h, h, idx)
	}
	blobs, err := s.blobRows(r.Context(), where, limit, args...)
	if err != nil {
		s.writeInternal(w, r.URL.Path, err)
		return
	}
	if blobs == nil {
		blobs = []blobRow{}
	}
	writeJSON(w, 200, map[string]any{"vantage": s.vantage, "blobs": blobs})
}

type assignmentRow struct {
	ValidatorAddress string `json:"validator_address"`
	VotingPower      int64  `json:"voting_power"`
	RowCount         int    `json:"row_count"`
	// Attested: the settled promise carries a signature from this validator
	// that verified against its consensus key, which is proof it stored the
	// shard. false means unproven, null means the record predates
	// verification. Never "did not store".
	Attested *bool `json:"attested"`
}

func (s *Server) handleBlob(w http.ResponseWriter, r *http.Request) {
	hash := strings.ToLower(r.PathValue("hash"))
	ctx := r.Context()
	blobs, err := s.blobRows(ctx, `promise_hash = ?`, 1, hash)
	if err != nil {
		s.writeInternal(w, r.URL.Path, err)
		return
	}
	if len(blobs) == 0 {
		writeErr(w, 404, "no publication with this promise hash")
		return
	}
	rows, err := s.st.DB().QueryContext(ctx, `SELECT validator_address, voting_power, row_count, attested FROM assignments WHERE promise_hash = ? ORDER BY voting_power DESC, validator_address`, hash)
	if err != nil {
		s.writeInternal(w, r.URL.Path, err)
		return
	}
	var assigns []assignmentRow
	for rows.Next() {
		var a assignmentRow
		var att sql.NullInt64
		if err := rows.Scan(&a.ValidatorAddress, &a.VotingPower, &a.RowCount, &att); err != nil {
			rows.Close()
			s.writeInternal(w, r.URL.Path, err)
			return
		}
		if att.Valid {
			b := att.Int64 == 1
			a.Attested = &b
		}
		assigns = append(assigns, a)
	}
	rows.Close()
	probes, err := s.probeRows(ctx, `promise_hash = ?`, 1000, hash)
	if err != nil {
		s.writeInternal(w, r.URL.Path, err)
		return
	}
	var params struct {
		ShardRetentionS        int64 `json:"shard_retention_s"`
		PaymentPromiseTimeoutS int64 `json:"payment_promise_timeout_s"`
	}
	_ = s.st.DB().QueryRowContext(ctx, `SELECT shard_retention_s, payment_promise_timeout_s FROM publications WHERE promise_hash = ?`, hash).Scan(&params.ShardRetentionS, &params.PaymentPromiseTimeoutS)
	writeJSON(w, 200, map[string]any{"blob": blobs[0], "params": params, "assignments": assigns, "probes": probes, "vantage": s.vantage})
}

// ---- sampling ----

// handleSampling publishes the load policy's admission decisions so the
// commit-and-reveal audit the methodology page describes can actually be
// carried out. Each row is one day's commitment to the secret the draws used,
// with the publications decided under it and the probability each was drawn
// at. Once the day's secret is revealed, anyone can recompute
// H(promise_hash || secret) < p * 2^64 for every promise hash of that day and
// check this observer's sample against their own.
func (s *Server) handleSampling(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	win, err := parseWindow(r, time.Now())
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	type day struct {
		DayCommitment string  `json:"day_commitment"`
		Binding       string  `json:"binding"`
		P             float64 `json:"p"`
		Publications  int64   `json:"publications"`
		Probed        int64   `json:"publications_probed"`
		SampledOut    int64   `json:"publications_sampled_out"`
	}
	rows, err := s.st.DB().QueryContext(ctx, `SELECT
			COALESCE(json_extract(raw_json, '$.sampling.day_commitment'), '') AS c,
			COALESCE(json_extract(raw_json, '$.sampling.binding'), '') AS b,
			COALESCE(json_extract(raw_json, '$.sampling.p'), 1.0) AS p,
			COUNT(DISTINCT promise_hash),
			COUNT(DISTINCT CASE WHEN classification != 'NOT_PROBED' THEN promise_hash END),
			COUNT(DISTINCT CASE WHEN classification = 'NOT_PROBED' AND classification_reason LIKE 'budget:%' THEN promise_hash END)
		FROM probes WHERE started_at >= ?
		GROUP BY c, b, p ORDER BY c, p`, win.startArg())
	if err != nil {
		s.writeInternal(w, r.URL.Path, err)
		return
	}
	defer rows.Close()
	days := []day{}
	for rows.Next() {
		var d day
		if err := rows.Scan(&d.DayCommitment, &d.Binding, &d.P, &d.Publications, &d.Probed, &d.SampledOut); err != nil {
			s.writeInternal(w, r.URL.Path, err)
			return
		}
		days = append(days, d)
	}
	if err := rows.Err(); err != nil {
		s.writeInternal(w, r.URL.Path, err)
		return
	}
	writeJSON(w, 200, map[string]any{
		"window":    win,
		"vantage":   s.vantage,
		"decisions": days,
		"how_to_audit": "Each row commits to that day's secret as SHA256(secret). " +
			"Once the secret is published, recompute H(promise_hash || secret) < p * 2^64 " +
			"for every MsgPayForFibre settled that day: the promise hashes that pass are the ones " +
			"this observer should have probed, and /v1/probes says which ones it did.",
		"secret_published": false,
	})
}

// ---- probes ----

type probeRow struct {
	Vantage          string `json:"vantage"`
	PromiseHash      string `json:"promise_hash"`
	ValidatorAddress string `json:"validator_address"`
	ValidatorHost    string `json:"validator_host"`
	Assigned         bool   `json:"assigned"`
	// Attested: the settled promise proves this validator stored the blob.
	// false means unproven (so this probe is UNATTESTED and outside the serve
	// rate), null means the measurement predates signature verification.
	Attested         *bool  `json:"attested"`
	AssignedRowCount int    `json:"assigned_row_count"`
	ScheduleLabel    string `json:"schedule_label"`
	ScheduledAt      string `json:"scheduled_at"`
	StartedAt        string `json:"started_at"`
	Phase            string `json:"phase"`
	Outcome          string `json:"outcome"`
	Classification   string `json:"classification"`
	Reason           string `json:"classification_reason"`
	RowsReturned     int    `json:"rows_returned"`
	RowsExpected     int    `json:"rows_expected"`
	TotalDurationMS  int64  `json:"total_duration_ms"`
	TLSOK            bool   `json:"tls_ok"`
	IdentityOK       bool   `json:"identity_ok"`
	RawError         string `json:"raw_error,omitempty"`
	// RetryFirstOutcome is set when this probe was the second attempt after a
	// transport timeout; it is the first attempt's outcome, so a reader can
	// see "the first try timed out" rather than only the final verdict.
	RetryFirstOutcome string `json:"retry_first_outcome,omitempty"`
	ClockOffsetMS     int64  `json:"clock_offset_ms,omitempty"`
}

func (s *Server) probeRows(ctx context.Context, where string, limit int, args ...any) ([]probeRow, error) {
	q := `SELECT vantage, promise_hash, validator_address, validator_host, assigned, attested, assigned_row_count, schedule_label, scheduled_at,
		started_at, phase, outcome, classification, classification_reason, rows_returned, rows_expected, total_duration_ms, tls_ok, identity_ok, raw_error,
		COALESCE(json_extract(raw_json, '$.retry.first_outcome'), ''), COALESCE(json_extract(raw_json, '$.clock_offset_ms'), 0)
		FROM probes`
	if where != "" {
		q += " WHERE " + where
	}
	q += " ORDER BY started_at DESC LIMIT " + strconv.Itoa(limit)
	rows, err := s.st.DB().QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []probeRow{}
	for rows.Next() {
		var p probeRow
		var assigned, tls, id int
		var att sql.NullInt64
		if err := rows.Scan(&p.Vantage, &p.PromiseHash, &p.ValidatorAddress, &p.ValidatorHost, &assigned, &att, &p.AssignedRowCount, &p.ScheduleLabel,
			&p.ScheduledAt, &p.StartedAt, &p.Phase, &p.Outcome, &p.Classification, &p.Reason, &p.RowsReturned, &p.RowsExpected,
			&p.TotalDurationMS, &tls, &id, &p.RawError, &p.RetryFirstOutcome, &p.ClockOffsetMS); err != nil {
			return nil, err
		}
		p.Assigned, p.TLSOK, p.IdentityOK = assigned == 1, tls == 1, id == 1
		if att.Valid {
			b := att.Int64 == 1
			p.Attested = &b
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Server) handleProbes(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, err := parseLimit(r, 100, 1000)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	var conds []string
	var args []any
	if v := q.Get("validator"); v != "" {
		addr, err := parseAddr(v)
		if err != nil {
			writeErr(w, 400, "validator must be 40 hex chars or celestiavalcons1...")
			return
		}
		conds, args = append(conds, `validator_address = ?`), append(args, addr)
	}
	if b := q.Get("blob"); b != "" {
		conds, args = append(conds, `promise_hash = ?`), append(args, strings.ToLower(b))
	}
	if since := q.Get("since"); since != "" {
		t, err := time.Parse(time.RFC3339, since)
		if err != nil {
			writeErr(w, 400, "since must be RFC 3339")
			return
		}
		conds, args = append(conds, `started_at >= ?`), append(args, store.TS(t))
	}
	if c := q.Get("class"); c != "" {
		conds, args = append(conds, `classification = ?`), append(args, strings.ToUpper(c))
	}
	rows, err := s.probeRows(r.Context(), strings.Join(conds, " AND "), limit, args...)
	if err != nil {
		s.writeInternal(w, r.URL.Path, err)
		return
	}
	writeJSON(w, 200, map[string]any{"vantage": s.vantage, "probes": rows})
}

// parseLimit reads ?limit= with a default and a maximum; anything that is
// not an integer in [1, max] is a 400, never a silent fallback.
func parseLimit(r *http.Request, def, max int) (int, error) {
	raw := r.URL.Query().Get("limit")
	if raw == "" {
		return def, nil
	}
	l, err := strconv.Atoi(raw)
	if err != nil || l < 1 || l > max {
		return 0, fmt.Errorf("limit must be an integer between 1 and %d", max)
	}
	return l, nil
}
