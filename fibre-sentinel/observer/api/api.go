// Package api is the observer's read-only JSON API, versioned under /v1/.
//
// Every aggregate carries the probe count it was computed from, the time
// window it covers, and the vantage. Rates follow docs/verdicts.md: serve
// rate is HEALTHY / (HEALTHY + FAULT) over assigned, attested probes in the
// in-window phase only; grace is recorded and shown but never rated;
// NOT_PROBED and PROBE_ERROR are reported as gaps, never folded into a rate.
package api

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cosmos/cosmos-sdk/types/bech32"

	"github.com/plsgiveup/fibre/fibre-sentinel/internal/scan"
	"github.com/plsgiveup/fibre/fibre-sentinel/observer/export"
	"github.com/plsgiveup/fibre/fibre-sentinel/observer/rollup"
	"github.com/plsgiveup/fibre/fibre-sentinel/observer/store"
	"github.com/plsgiveup/fibre/fibre-sentinel/observer/verdict"
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
	// The two window aggregates the dashboard waits for, held as snapshots per
	// window. See snapshot.go: both are aggregates over the whole window and
	// are computed on a schedule rather than per request, so a reader never
	// waits for one and never sees one without its age.
	net  *snapshotCache[*networkResponse]
	vals *snapshotCache[[]validatorRow]
	// The publisher-side summary, same treatment: see market.go.
	market *snapshotCache[*marketResponse]
	// labels is the operator-maintained publisher name registry.
	labels map[string]PublisherLabel
	// dataDir holds the status files the processes write (internal/status);
	// empty means liveness is not reported.
	dataDir string

	// How many vantages the store holds, and the table watermarks it was
	// counted at: see vantageCount.
	vantageMu   sync.Mutex
	vantageN    int
	vantageMark [2]int64

	// Per-publication verdicts, keyed by what the publication's probes look
	// like right now. See blobcache.go.
	blobs *blobCache
	// asOf rations pinned-window requests (see asOfLimiter).
	asOf asOfLimiter
}

// Option configures a Server before it warms its caches.
type Option func(*Server)

// WithPublisherLabels installs the publisher name registry (see market.go).
func WithPublisherLabels(m map[string]PublisherLabel) Option {
	return func(s *Server) {
		if m != nil {
			s.labels = m
		}
	}
}

// WithDataDir tells the server where the processes' status files live.
func WithDataDir(dir string) Option { return func(s *Server) { s.dataDir = dir } }

// New builds a Server. vantage is the label rendered on every response.
func New(st *store.Store, vantage string) *Server { return NewWithLogger(st, vantage, nil) }

// NewWithLogger is New with somewhere to put the detail of an internal error
// that the response deliberately withholds.
func NewWithLogger(st *store.Store, vantage string, log *scan.Logger) *Server {
	return NewWithVantage(st, VantageInfo{Name: vantage}, log)
}

// NewWithVantage is NewWithLogger with the vantage described rather than only
// named.
func NewWithVantage(st *store.Store, info VantageInfo, log *scan.Logger, opts ...Option) *Server {
	info.Verifiability = map[string]string{
		"egress_addresses": "the anchor: match these against the source addresses hitting your Fibre port",
		"asn":              "check it against egress_addresses through public routing data (whois, RIPEstat, bgp.tools); it names the network, not a place",
		"provider":         "usually follows from the asn, so checkable the same way",
		"location":         "the operator's word: geolocating an address is a guess, so nothing here proves it",
	}
	info.Complete = info.Location != "" && info.Provider != "" && info.ASN != "" && len(info.EgressAddresses) > 0
	s := &Server{st: st, vantage: info.Name, info: info, mux: http.NewServeMux(), log: log, blobs: newBlobCache(), labels: map[string]PublisherLabel{}}
	for _, o := range opts {
		o(s)
	}
	s.net = newSnapshotCache("network", s.computeNetwork)
	s.market = newSnapshotCache("market", s.computeMarket)
	s.vals = newSnapshotCache("validators", func(ctx context.Context, win Window) ([]validatorRow, error) {
		return s.validatorRows(ctx, win, "")
	})
	// Serve the previous process's snapshots at once, then warm every window
	// so the first visitor is not the one who waits.
	if s.dataDir != "" {
		dir := filepath.Join(s.dataDir, "snapshots")
		s.net.persistTo(dir, s.logf())
		s.vals.persistTo(dir, s.logf())
		s.market.persistTo(dir, s.logf())
	}
	s.net.warm(s.logf(), time.Now())
	s.vals.warm(s.logf(), time.Now())
	s.market.warm(s.logf(), time.Now())
	// And the first page of blobs, for the same reason: with the verdict cache
	// empty that page costs six queries per row, which is the one cold path
	// left on the site. It is a single read of what /v1/blobs answers by
	// default, discarded — the point is the cache it leaves behind.
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), snapshotTimeout)
		defer cancel()
		if _, err := s.blobRows(ctx, "", blobPageDefault); err != nil && log != nil {
			log.Printf("warming the blob page: %v", err)
		}
	}()
	s.mux.HandleFunc("GET /v1/meta", s.handleMeta)
	s.mux.HandleFunc("GET /v1/network", s.handleNetwork)
	s.mux.HandleFunc("GET /v1/validators", s.handleValidators)
	s.mux.HandleFunc("GET /v1/validators/{addr}", s.handleValidator)
	s.mux.HandleFunc("GET /v1/blobs", s.handleBlobs)
	s.mux.HandleFunc("GET /v1/blobs/{hash}", s.handleBlob)
	s.mux.HandleFunc("GET /v1/probes", s.handleProbes)
	s.mux.HandleFunc("GET /v1/runs", s.handleRuns)
	s.mux.HandleFunc("GET /v1/sampling", s.handleSampling)
	s.mux.HandleFunc("GET /v1/exports", s.handleExports)
	s.mux.HandleFunc("GET /v1/exports/{name}", s.handleExportFile)
	s.mux.HandleFunc("GET /v1/health", s.handleHealth)
	s.mux.HandleFunc("GET /v1/market", s.handleMarket)
	s.mux.HandleFunc("GET /v1/publishers", s.handlePublishers)
	s.mux.HandleFunc("GET /v1/publishers/{addr}", s.handlePublisher)
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
	switch {
	case status < 200 || status >= 300:
		w.Header().Set("Cache-Control", "no-store")
	case w.Header().Get("Cache-Control") == "":
		// A handler that set its own policy (an immutable export, an
		// uncached pinned window) keeps it.
		w.Header().Set("Cache-Control", "public, max-age=15")
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
	// AsOf: End was pinned by the caller (?as_of=), so every figure is what
	// the observer would have published at End from the rows it had by
	// then: rows started after End are left out. What the chain says now
	// (jailed, bonded, the current registry) is not rewound; see
	// AsOfNote.
	AsOf bool `json:"as_of,omitempty"`
}

// rolledUp is the label beside figures that rest on the daily rollup: past
// the raw retention the "all" window is the rollup for every day before
// RawFrom plus the raw rows from RawFrom on. See observer/rollup.
type rolledUp struct {
	RawFrom string `json:"raw_from"`
	Days    int64  `json:"days"`
	Note    string `json:"note"`
}

// rolledFor returns the rollups the window folds in, and their label: only
// the "all" window, only once a day has been pruned. A pinned end before
// RawFrom takes whole rolled days up to and including its own.
func (s *Server) rolledFor(ctx context.Context, win Window, only string) (*rollup.Rolled, *rolledUp, error) {
	if win.Span != 0 {
		return nil, nil, nil
	}
	from, ok := rollup.RawFrom(s.st)
	if !ok {
		return nil, nil, nil
	}
	before := from
	if end := win.End.UTC().Truncate(24 * time.Hour).Add(24 * time.Hour); end.Before(before) {
		before = end
	}
	r, err := rollup.Load(ctx, s.st.DB(), before, only)
	if err != nil {
		return nil, nil, err
	}
	label := &rolledUp{RawFrom: from.Format("2006-01-02"), Days: r.Days,
		Note: "rolled up after " + rolledNote + ": obligations, classes, faults, probe counts, gaps and heartbeats for days before raw_from come from the daily rollup; latency, by-point, attestation and throughput figures cover the raw record from raw_from on"}
	return r, label, nil
}

// rolledNote names the raw retention in the label; the API does not read
// the collector's flag, so it states the decision (deploy/README.md).
const rolledNote = "90 days"

func addRolledObligations(o *obligationStats, r rollup.Obligations) {
	o.Total += r.Total
	o.Served += r.Served
	o.Broken += r.Broken
	o.EndUnobserved += r.EndUnobserved
	o.UnobservedReachable += r.UnobservedReachable
	o.UnobservedUnreachable += r.UnobservedUnreachable
	o.UnobservedNotProbed += r.UnobservedNotProbed
	o.Pending += r.Pending
	o.finish()
}

// AsOfNote goes beside a pinned window's figures.
const AsOfNote = "rows started after as_of are left out; chain state (jailed, bond_status, current host and endpoint counts) is as of now, not as_of"

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
	if v := r.URL.Query().Get("as_of"); v != "" {
		at, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return Window{}, fmt.Errorf("as_of must be RFC 3339, e.g. 2026-09-18T12:00:00Z")
		}
		if at.After(now.Add(time.Minute)) {
			return Window{}, fmt.Errorf("as_of is in the future")
		}
		now = at
	}
	w := Window{Name: name, Span: span, End: now, AsOf: r.URL.Query().Get("as_of") != ""}
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

// endArg bounds a query at the window's end. With an unpinned window End
// is the moment the query was planned, so the bound admits every row the
// store holds; with ?as_of= it is what makes the answer reproducible.
func (w Window) endArg() string { return store.TS(w.End) }

// asOfArg is the end bound for queries that only pinned windows need.
func (w Window) asOfArg() string {
	if !w.AsOf {
		return ""
	}
	return w.endArg()
}

// asOfLimiter rations pinned-window requests, which bypass the snapshot
// cache and cost a full aggregate each: a small burst, then one every two
// seconds.
type asOfLimiter struct {
	mu     sync.Mutex
	tokens float64
	last   time.Time
}

const (
	asOfBurst    = 4.0
	asOfInterval = 2 * time.Second
)

func (l *asOfLimiter) allow(now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.last.IsZero() {
		l.tokens = asOfBurst
	} else {
		l.tokens = min(asOfBurst, l.tokens+now.Sub(l.last).Seconds()/asOfInterval.Seconds())
	}
	l.last = now
	if l.tokens < 1 {
		return false
	}
	l.tokens--
	return true
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
	APIVersion             string      `json:"api_version"`
	Vantage                string      `json:"vantage"`
	VantageInfo            VantageInfo `json:"vantage_info"`
	VantageCount           int         `json:"vantage_count"`
	ObservedFromOneVantage bool        `json:"observed_from_one_location"`
	ChainID                string      `json:"chain_id"`
	// AppVersion is the chain's current application version, and FibreActive
	// is whether that is high enough for x/fibre and x/valaddr to exist. Below
	// FibreAppVersion the modules are not there, so an empty registry and an
	// empty publication list say nothing about any validator — and a site that
	// cannot tell "the module is absent" from "the module is empty" will imply
	// the second while the first is true. Empty when the collector has not
	// reached a node yet, which is itself worth showing.
	AppVersion      string `json:"app_version,omitempty"`
	FibreAppVersion string `json:"fibre_app_version,omitempty"`
	FibreActive     bool   `json:"fibre_active"`
	// ChainHeight is the chain's tip as the collector last saw it, which is not
	// LastScannedHeight: that is how far the SCANNER has read, and before Fibre
	// activates there is nothing for it to read, so it stays empty while the
	// chain is plainly making blocks. Reporting the chain's progress as our own,
	// or ours as the chain's, would be wrong in opposite directions.
	ChainHeight          string       `json:"chain_height,omitempty"`
	LastScannedHeight    string       `json:"last_scanned_height"`
	EndpointsHeight      string       `json:"endpoints_height"`
	ProtocolParamsFinger string       `json:"protocol_params_fingerprint"`
	PinnedCelestiaApp    string       `json:"pinned_celestia_app_commit"`
	Counts               store.Counts `json:"counts"`
	Collector            *runStatus   `json:"collector"`
	Prober               *runStatus   `json:"prober"`
	// LastProbeAt is the newest measurement's start time. The prober writes
	// JSONL only (it never touches this database), so this is the only live
	// signal of it; a quiet chain makes it old without anything being wrong.
	LastProbeAt *string           `json:"last_probe_at"`
	Meta        map[string]string `json:"meta"`
	ServerTime  time.Time         `json:"server_time"`
	// Components is every observer process with its liveness, from the
	// status files in the data directory (see /v1/health). Health is the
	// same verdict /v1/health returns: ok, degraded or down.
	Components []componentStatus `json:"components"`
	Health     string            `json:"health"`
	// ScanGaps are height ranges the scanner could not read from its node.
	// A publication in one of them is unknown to this observer.
	ScanGaps []scan.ScanGap `json:"scan_gaps,omitempty"`
	// PinStatus says whether the chain's app version matches the celestia-app
	// major this build's assignment constants are pinned to: matches,
	// chain_ahead, chain_behind or unknown.
	PinStatus string `json:"pin_status"`
	// UnassignablePublications is how many publications have no row
	// assignment (a blob version this build does not know), and so are never
	// probed.
	UnassignablePublications int64 `json:"unassignable_publications"`
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
// vantageCount is how many distinct places the stored observations were made
// from. It decides one sentence on every page — whether this is a single
// vantage or several — and it is the most expensive query /v1/meta runs: no
// index covers `vantage`, so it scans both probe tables in full and unions them
// through a temp B-tree. Measured on an 85,000-probe store it was 49ms of the
// endpoint's 58ms of SQL, on the endpoint every page polls.
//
// It is also a number that essentially never changes: a second vantage appears
// once, when a second observer's file is first ingested. So it is computed once
// and reused until the tables it reads have actually grown. Both are
// append-only, so the highest rowid in each is an exact watermark — a vantage
// cannot appear without a row, and a row cannot arrive without raising it — and
// MAX(rowid) is a single seek to the end of the b-tree rather than a scan.
//
// Exact rather than a timer on purpose. The claim this drives is the one-vantage
// caveat printed above every page, and a cached count is a claim about how much
// the site's own evidence is worth.
func (s *Server) vantageCount(ctx context.Context) int {
	db := s.st.DB()
	var pr, re sql.NullInt64
	_ = db.QueryRowContext(ctx, `SELECT MAX(rowid) FROM probes`).Scan(&pr)
	_ = db.QueryRowContext(ctx, `SELECT MAX(rowid) FROM reachability`).Scan(&re)
	mark := [2]int64{pr.Int64, re.Int64}

	s.vantageMu.Lock()
	if s.vantageN > 0 && s.vantageMark == mark {
		n := s.vantageN
		s.vantageMu.Unlock()
		return n
	}
	s.vantageMu.Unlock()

	var n int
	_ = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM (SELECT vantage FROM probes UNION SELECT vantage FROM reachability)`).Scan(&n)
	if n == 0 {
		n = 1
	}
	s.vantageMu.Lock()
	s.vantageN, s.vantageMark = n, mark
	s.vantageMu.Unlock()
	return n
}

// latestRun is the newest run row for a component, with whether its heartbeat
// is recent enough to call it alive.
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
	h := s.health(ctx, now)
	writeJSON(w, 200, metaResponse{
		Components: h.Components, Health: h.Status, ScanGaps: h.ScanGaps, PinStatus: h.PinStatus,
		UnassignablePublications: s.unassignablePublications(ctx),
		APIVersion:               Version, Vantage: s.vantage, VantageInfo: s.info,
		VantageCount: vantages, ObservedFromOneVantage: vantages == 1,
		ChainID: meta["chain_id"], LastScannedHeight: meta["last_scanned_height"], EndpointsHeight: meta["endpoints_height"],
		AppVersion: meta["app_version"], FibreAppVersion: meta["fibre_app_version"], FibreActive: meta["fibre_active"] == "yes",
		ChainHeight:          meta["chain_height"],
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
	PID           *int64  `json:"pid"`
	Hostname      *string `json:"hostname"`
	// Config is what the run was started with: every flag by name, as the
	// component recorded it in runs.jsonl (status.RunEvent). It is what a
	// verifier needs to re-derive this run's rows: the prune tolerance
	// behind a phase, the schedule points, the timeouts, the policy file.
	// Null for a run recorded before the file existed, and for the
	// collector's own row.
	Config json.RawMessage `json:"config"`
}

func (s *Server) handleRuns(w http.ResponseWriter, r *http.Request) {
	win, err := parseWindow(r, time.Now())
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	rows, err := s.st.DB().QueryContext(r.Context(), `SELECT id, component, vantage, version, started_at, last_heartbeat_at, stopped_at, stop_reason, pid, hostname, config_json
		FROM observer_runs WHERE last_heartbeat_at >= ? AND started_at <= ? ORDER BY started_at`, win.startArg(), win.endArg())
	if err != nil {
		s.writeInternal(w, r.URL.Path, err)
		return
	}
	defer rows.Close()
	out := []runRow{}
	for rows.Next() {
		var rr runRow
		var cfg *string
		if err := rows.Scan(&rr.ID, &rr.Component, &rr.Vantage, &rr.Version, &rr.StartedAt, &rr.LastHeartbeat, &rr.StoppedAt, &rr.StopReason, &rr.PID, &rr.Hostname, &cfg); err != nil {
			s.writeInternal(w, r.URL.Path, err)
			return
		}
		if cfg != nil && json.Valid([]byte(*cfg)) {
			rr.Config = json.RawMessage(*cfg)
		} else {
			rr.Config = json.RawMessage("null")
		}
		out = append(out, rr)
	}
	writeJSON(w, 200, map[string]any{"window": win, "runs": out,
		"note": "a run without stopped_at whose component's status file is stale is a crash; config is the component's flags at start, from runs.jsonl"})
}

// ---- network ----

type classCounts map[string]int64

type networkResponse struct {
	// AsOfNote is set on a pinned window (see Window.AsOf).
	AsOfNote string `json:"as_of_note,omitempty"`
	// RolledUp is set when figures rest partly on the daily rollup.
	RolledUp *rolledUp `json:"rolled_up,omitempty"`
	Window   Window    `json:"window"`
	Vantage  string    `json:"vantage"`
	// ComputedAt and ComputeMs say when this summary was taken and how long it
	// took. It is a snapshot refreshed on a schedule, not a live query, so its
	// age is published rather than left for a reader to assume.
	ComputedAt             string `json:"computed_at,omitempty"`
	ComputeMs              int64  `json:"compute_ms,omitempty"`
	ObservedFromOneVantage bool   `json:"observed_from_one_location"`
	RegisteredEndpoints    int64  `json:"registered_endpoints"`
	ValidatorsProbed       int64  `json:"validators_probed"`
	Reachability           Rate   `json:"reachability"` // endpoints whose latest heartbeat or probe reached TLS
	// ReachabilityWindow is every reachability heartbeat in the window that
	// completed TLS, over every heartbeat sent. Reachability above is a census
	// of the endpoints right now; this is how the whole window went, which is
	// the difference between "two are down" and "two have been down all week".
	// Heartbeats are pooled, so a validator that registered mid-window
	// contributes fewer samples than one that was there throughout.
	ReachabilityWindow Rate `json:"reachability_window"`
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
	// Obligations counts one observation per (validator, blob) instead of
	// one per probe, judged by the newest probe: see obligationStats. This is
	// the headline figure and the number a confidence interval may honestly
	// be drawn around; ByObligation repeats its rate.
	Obligations     obligationStats  `json:"obligations"`
	ByObligation    Rate             `json:"serve_rate_by_obligation"`
	HeldOut         map[string]int64 `json:"serve_rate_held_out"`
	ExcludedClasses []excludedClass  `json:"serve_rate_excluded_classes"`
	Attestation     attestationStats `json:"attestation"`
	ProbeCount      int64            `json:"probe_count"` // all probe rows in window
	Classes         classCounts      `json:"classes"`
	// Faults is every FAULT of an assigned shard in the window, in any
	// phase. The serve rate's population is in-window only; corrupt bytes
	// returned in grace are still corrupt bytes, and docs/verdicts.md counts
	// INVALID_ROWS in any phase, so the count is wider than the rate.
	Faults           int64              `json:"faults"`
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
	// LatencyP50 and LatencyP95 are the network's own service times: the
	// median and 95th percentile of a whole probe, dial to verified rows, over
	// every probe that came back HEALTHY in this window. See the per-validator
	// fields for why this is published without a threshold.
	LatencyP50    *int64 `json:"serve_latency_p50_ms"`
	LatencyP95    *int64 `json:"serve_latency_p95_ms"`
	LatencySample int64  `json:"serve_latency_sample"`
}

// latencyWhere returns the median and 95th percentile of a whole probe over the
// rows matching where, and how many rows that is.
func (s *Server) latencyWhere(ctx context.Context, where string, args ...any) (p50, p95 *int64, n int64, err error) {
	var a, b sql.NullInt64
	err = s.st.DB().QueryRowContext(ctx, `SELECT
			MAX(CASE WHEN rn = (c + 1) / 2         THEN ms END),
			MAX(CASE WHEN rn = (c * 95 + 99) / 100 THEN ms END),
			COALESCE(MAX(c), 0)
		FROM (
			SELECT total_duration_ms AS ms,
			       ROW_NUMBER() OVER (ORDER BY total_duration_ms) AS rn,
			       COUNT(*)     OVER ()                           AS c
			FROM probes WHERE `+where+`
			  AND classification = 'HEALTHY' AND total_duration_ms > 0
		)`, args...).Scan(&a, &b, &n)
	if err != nil {
		return nil, nil, 0, err
	}
	if a.Valid {
		x := a.Int64
		p50 = &x
	}
	if b.Valid {
		x := b.Int64
		p95 = &x
	}
	return p50, p95, n, nil
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

	// The same three counts per (validator, blob) obligation rather than per
	// probe, which is the unit an operator reads them in.
	//
	// Each obligation is probed at four schedule points, so the probe counts
	// above run about four times these. A page that told an operator "812
	// unattested" when the true statement is "203 blobs carried no signature
	// from you" would have multiplied its own evidence by the size of a
	// schedule the reader cannot see, and the figure it multiplied is the one
	// most likely to be misread as an accusation. An obligation is counted
	// attested if any probe of it carries verified proof, so a mix of NULL and
	// 1 is proven rather than unknown.
	AttestedBlobs   int64 `json:"attested_blobs"`
	UnattestedBlobs int64 `json:"unattested_blobs"`
	UnknownBlobs    int64 `json:"unknown_blobs"`
	// BlobCoverage is AttestedBlobs / (AttestedBlobs + UnattestedBlobs). It is
	// not the same number as Coverage: obligations differ in how many times
	// they were probed, so the probe-counted ratio silently weights an
	// obligation by its probe count.
	BlobCoverage Rate `json:"blob_coverage"`
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
	{"IDENTITY_MISMATCH", "the certificate is not endorsed by this validator's consensus key, so no client can download from the endpoint; a statement about the endpoint, shown as its status, not about any shard"},
	{"SERVER_ERROR", "the endpoint was reached and answered with an application error instead of the shard; from one probe that is not distinguishable from a transient fault, so it is shown beside the rate"},
	{"THROTTLED", "the endpoint was reached and refused the download with a rate limit; that says nothing about the shard, and the prober backs off from a validator that says so"},
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

// obligationStats counts one observation per (validator, blob) rather than
// one per probe, and says what became of each.
//
// The schedule visits the same validator and blob four times in window, and
// every bonded validator is assigned every blob because of the minimum-rows
// floor, so the probes inside one obligation are near-perfectly correlated: a
// certificate that lapsed, or a disk that lost a shard, produces four FAULT
// rows for one event. Counting those as four independent trials makes any
// confidence interval far narrower than the evidence supports, which is the
// wrong error to make under a public accusation.
//
// The verdict on an obligation is the newest probe of it, the same rule the
// per-blob reconstructability verdict uses. "Kept when no probe faulted" was
// the old rule, and it let a validator that served at the first point and
// answered 500 at the next three count as fully kept: that is the profile of
// a server that pruned early, and the one this observer exists to notice.
//
//	served         newest probe HEALTHY, no fault anywhere
//	broken         any probe FAULT
//	end_unobserved served earlier, but the newest probe produced no verdict
//	unobserved     never seen serving, and never faulted, split by what the
//	               probes did see: the endpoint completed TLS and still
//	               handed nothing over; it never completed TLS; or this
//	               observer never attempted the download (backoff, budget,
//	               a slot that elapsed)
//	pending        the retention window has not ended, so the newest probe
//	               is not the last one; no verdict yet
//
// Only served and broken enter the rate. The rest is published beside it so a
// reader can see how many obligations the rate does not speak for.
//
// The population is obligations the settled promise proves (attested = 1):
// an unattested one is nothing to keep or break, and a record from before
// signatures were verified (attested NULL) is not evidence either way, so it
// is outside this count and reported under attestation.unknown. An
// obligation belongs to a window by its publication's settlement time, not
// by each probe's time, so an obligation is judged whole or not at all: a
// window cut through the middle of one would decide it on half its probes.
// The window's end is the moment the verdict is drawn (as_of); an obligation
// whose must_serve_until is later than that is pending.
type obligationStats struct {
	Total                 int64 `json:"total"`
	Served                int64 `json:"served"`
	Broken                int64 `json:"broken"`
	EndUnobserved         int64 `json:"end_unobserved"`
	Unobserved            int64 `json:"unobserved"`
	UnobservedReachable   int64 `json:"unobserved_reachable"`
	UnobservedUnreachable int64 `json:"unobserved_unreachable"`
	UnobservedNotProbed   int64 `json:"unobserved_not_probed"`
	Pending               int64 `json:"pending"`
	// Rate is served / (served + broken).
	Rate Rate `json:"rate"`
}

// obligationBuckets is the per-obligation reduction the two obligation
// queries share: one row per (validator, blob), with the newest probe's class
// and what the other probes saw. A gap row (NOT_PROBED, PROBE_ERROR) never
// becomes the newest probe while a real one exists, so a slot this observer
// missed does not turn a served obligation into an unobserved one.
//
// Arguments, in order: as_of (pending cut), window start (settlement_time),
// then whatever the caller appends (suspect points, a validator filter).
// The obligation SQL lives in observer/rollup, which computes the daily
// rollups with the same statements; see rollup.ObligationBuckets.
const (
	obligationBuckets = rollup.ObligationBuckets
	obligationSums    = rollup.ObligationSums
)

func (o *obligationStats) finish() {
	o.Unobserved = o.UnobservedReachable + o.UnobservedUnreachable + o.UnobservedNotProbed
	o.Rate = rate(o.Served, o.Served+o.Broken)
}

// obligationArgs is the argument list obligationBuckets expects: as_of (the
// pending cut), the window's settlement bounds, the row bound, the suspect
// points, then the caller's own.
func obligationArgs(win Window, ss suspectSet, extra ...any) []any {
	args := []any{store.TS(win.End), win.startArg(), win.endArg(), win.endArg()}
	args = append(args, ss.args...)
	return append(args, extra...)
}

// obligationsWhere reduces the window's proven obligations to buckets. extra
// is appended to the WHERE clause (a validator filter), its arguments last.
func (s *Server) obligationsWhere(ctx context.Context, win Window, ss suspectSet, extra string, extraArgs ...any) (obligationStats, error) {
	var o obligationStats
	err := s.st.DB().QueryRowContext(ctx, `SELECT `+obligationSums+` FROM (`+obligationBuckets+ss.clause("pr.scheduled_at")+extra+`)
			GROUP BY validator_address, promise_hash)`, obligationArgs(win, ss, extraArgs...)...).
		Scan(&o.Total, &o.Broken, &o.Served, &o.EndUnobserved, &o.UnobservedReachable, &o.UnobservedUnreachable, &o.UnobservedNotProbed, &o.Pending)
	if err != nil {
		return obligationStats{}, err
	}
	o.finish()
	return o, nil
}

// obligationsByValidator is obligationsWhere grouped by validator.
func (s *Server) obligationsByValidator(ctx context.Context, win Window, ss suspectSet, extra string, extraArgs ...any) (map[string]obligationStats, error) {
	rows, err := s.st.DB().QueryContext(ctx, `SELECT validator_address, `+obligationSums+` FROM (`+obligationBuckets+ss.clause("pr.scheduled_at")+extra+`)
			GROUP BY validator_address, promise_hash) GROUP BY validator_address`, obligationArgs(win, ss, extraArgs...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]obligationStats{}
	for rows.Next() {
		var addr string
		var o obligationStats
		if err := rows.Scan(&addr, &o.Total, &o.Broken, &o.Served, &o.EndUnobserved, &o.UnobservedReachable, &o.UnobservedUnreachable, &o.UnobservedNotProbed, &o.Pending); err != nil {
			return nil, err
		}
		o.finish()
		out[addr] = o
	}
	return out, rows.Err()
}

// vantageHealth reports the correlated failures the window contains, and
// which schedule points every rate leaves out because of them.
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
	// Threshold and FaultThreshold are published so the judgement is not a
	// hidden constant.
	Threshold      float64 `json:"threshold"`
	FaultThreshold float64 `json:"fault_threshold"`
	// MinValidators is the floor on how many validators must share the
	// failure before a share means anything: one of two is half.
	MinValidators int64 `json:"min_validators"`
	// Suspect lists every schedule point in the window at which the share
	// of validators unreachable, or the share faulting, reached its
	// threshold. Every probe row at those points is left out of the serve
	// rate, the obligation buckets, the per-point breakdown and the fault
	// count: validators fail independently and one observer's network, or
	// one observer's stale assignment, does not. SuspectRows is how many
	// rows that removed.
	Suspect     []suspectPoint `json:"suspect"`
	SuspectRows int64          `json:"suspect_rows"`
}

// suspectPoint is one schedule point the observer does not trust itself at.
type suspectPoint struct {
	At          string `json:"at"`
	Label       string `json:"label"`
	Validators  int64  `json:"validators"`
	Unreachable Rate   `json:"unreachable"`
	Fault       Rate   `json:"fault"`
	// Reason is "unreachable", "fault" or "unreachable,fault".
	Reason string `json:"reason"`
}

// suspectSet is the SQL side of vantageHealth.Suspect: the clause that drops
// those points from a population query, and its arguments.
type suspectSet struct {
	points []suspectPoint
	args   []any
}

// clause is " AND <col> NOT IN (?, ...)" or "" when nothing is suspect.
func (ss suspectSet) clause(col string) string {
	if len(ss.args) == 0 {
		return ""
	}
	return " AND " + col + " NOT IN (?" + strings.Repeat(", ?", len(ss.args)-1) + ")"
}

// correlatedUnreachableThreshold: at or above this share of the validators
// probed at one schedule point being unreachable, the likeliest explanation
// is this observer's own network rather than that many independent
// operators. correlatedFaultThreshold is the same judgement for faults: half
// the set losing data at the same minute is not a finding about the set, it
// is a finding about the observer (a stale assignment pin, a broken coder).
//
// correlatedMinValidators is the floor under which a share is not a signal:
// one validator of two failing is half the set and an ordinary Tuesday.
const (
	correlatedUnreachableThreshold = verdict.UnreachableThreshold
	correlatedFaultThreshold       = verdict.FaultThreshold
	correlatedMinValidators        = verdict.MinValidators
)

// suspectPoints finds every schedule point in the window at which the share
// of distinct validators unreachable, or faulting, reached its threshold,
// among points where more than one validator was probed. The points are
// judged over the whole set at that minute, so the same point is suspect on
// every page and for every validator.
func (s *Server) suspectPoints(ctx context.Context, win Window) (vantageHealth, suspectSet, error) {
	out := vantageHealth{Threshold: correlatedUnreachableThreshold, FaultThreshold: correlatedFaultThreshold,
		MinValidators: correlatedMinValidators, Suspect: []suspectPoint{}}
	var ss suspectSet
	pts, err := rollup.SuspectPoints(ctx, s.st.DB(), `started_at >= ? AND started_at <= ?`, win.startArg(), win.endArg())
	if err != nil {
		return out, ss, err
	}
	var best float64
	for _, p := range pts {
		if p.Validators == 0 {
			continue
		}
		fu := float64(p.Unreachable) / float64(p.Validators)
		if fu > best || out.At == "" {
			best = fu
			out.WorstPoint = rate(p.Unreachable, p.Validators)
			out.At, out.Label = p.At, p.Label
		}
		reason := p.Reason()
		if reason == "" {
			continue
		}
		out.Suspect = append(out.Suspect, suspectPoint{At: p.At, Label: p.Label, Validators: p.Validators,
			Unreachable: rate(p.Unreachable, p.Validators), Fault: rate(p.Faulted, p.Validators), Reason: reason})
		out.SuspectRows += p.Rows
		ss.args = append(ss.args, p.At)
	}
	out.Correlated = out.WorstPoint.Den > 0 && best >= correlatedUnreachableThreshold && out.WorstPoint.Num >= correlatedMinValidators
	ss.points = out.Suspect
	return out, ss, nil
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

	// The same over obligations. MAX ignores NULLs in SQLite and in Postgres,
	// so an obligation with any verified evidence resolves to that evidence
	// and only one with no evidence at all stays unknown.
	err = s.st.DB().QueryRowContext(ctx, `SELECT
			COALESCE(SUM(CASE WHEN a = 1 THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN a = 0 THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN a IS NULL THEN 1 ELSE 0 END), 0)
		FROM (
			SELECT MAX(attested) AS a FROM probes WHERE `+where+`
			GROUP BY validator_address, promise_hash
		)`, args...).Scan(&st.AttestedBlobs, &st.UnattestedBlobs, &st.UnknownBlobs)
	if err != nil {
		return attestationStats{}, err
	}
	st.BlobCoverage = rate(st.AttestedBlobs, st.AttestedBlobs+st.UnattestedBlobs)
	return st, nil
}

func (s *Server) handleNetwork(w http.ResponseWriter, r *http.Request) {
	win, err := parseWindow(r, time.Now())
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if win.AsOf {
		// A pinned window is computed on demand, uncached and rationed.
		if !s.asOf.allow(time.Now()) {
			w.Header().Set("Retry-After", "2")
			writeErr(w, 429, "as_of requests are limited to one every two seconds")
			return
		}
		t0 := time.Now()
		resp, err := s.computeNetwork(r.Context(), win)
		if err != nil {
			s.writeInternal(w, r.URL.Path, err)
			return
		}
		resp.ComputedAt, resp.ComputeMs = t0.UTC().Format(time.RFC3339Nano), time.Since(t0).Milliseconds()
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, 200, resp)
		return
	}
	resp, at, ms, err := s.net.get(r.Context(), s.logf(), win)
	if err != nil {
		s.writeInternal(w, r.URL.Path, err)
		return
	}
	// A copy, so a reader cannot mutate the cached snapshot and two concurrent
	// readers cannot race on it.
	out := *resp
	out.ComputedAt, out.ComputeMs = at.UTC().Format(time.RFC3339Nano), ms
	writeJSON(w, 200, &out)
}

// logf adapts the server's logger, which may be absent in tests, to what the
// snapshot cache needs.
func (s *Server) logf() logf {
	if s.log == nil {
		return nil
	}
	return func(format string, args ...any) { s.log.Printf(format, args...) }
}

// computeNetwork does the work handleNetwork used to do inline. It is called
// from the snapshot cache rather than from the request, so its context outlives
// the reader who triggered it.
func (s *Server) computeNetwork(ctx context.Context, win Window) (*networkResponse, error) {
	db := s.st.DB()
	var resp networkResponse
	resp.Window, resp.Vantage = win, s.vantage
	resp.ObservedFromOneVantage = s.vantageCount(ctx) == 1

	if win.AsOf {
		resp.AsOfNote = AsOfNote
		_ = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM endpoints WHERE first_seen_at <= ? AND (closed_at IS NULL OR closed_at > ?)`, win.endArg(), win.endArg()).Scan(&resp.RegisteredEndpoints)
	} else {
		_ = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM endpoints WHERE closed_at IS NULL`).Scan(&resp.RegisteredEndpoints)
	}
	_ = db.QueryRowContext(ctx, `SELECT COUNT(DISTINCT validator_address) FROM probes WHERE started_at >= ? AND started_at <= ?`, win.startArg(), win.endArg()).Scan(&resp.ValidatorsProbed)

	// The points this observer does not trust itself at come first: every
	// population below leaves them out.
	vh, ss, err := s.suspectPoints(ctx, win)
	if err != nil {
		return nil, err
	}
	resp.VantageHealth = vh
	pop := `started_at >= ? AND started_at <= ? AND assigned = 1 AND phase = 'in_window'` + ss.clause("scheduled_at")
	popArgs := append([]any{win.startArg(), win.endArg()}, ss.args...)

	classes, total, err := s.classCountsWhere(ctx, pop, popArgs...)
	if err != nil {
		return nil, err
	}
	resp.Classes = classes
	_ = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM probes WHERE started_at >= ? AND started_at <= ? AND assigned = 1 AND classification = 'FAULT'`+ss.clause("scheduled_at"), popArgs...).Scan(&resp.Faults)
	resp.ServeRate = serveRate(classes)
	resp.Coverage = coverage(classes)
	resp.HeldOut = heldOut(classes)
	resp.ExcludedClasses = excludedFromRate
	if resp.Obligations, err = s.obligationsWhere(ctx, win, ss, ""); err != nil {
		return nil, err
	}
	resp.ByObligation = resp.Obligations.Rate
	_ = total
	if resp.Attestation, err = s.attestationWhere(ctx,
		`started_at >= ? AND started_at <= ? AND assigned = 1 AND phase = 'in_window'`, win.startArg(), win.endArg()); err != nil {
		return nil, err
	}
	_ = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM probes WHERE started_at >= ? AND started_at <= ?`, win.startArg(), win.endArg()).Scan(&resp.ProbeCount)
	_ = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM probes WHERE started_at >= ? AND started_at <= ? AND classification IN ('NOT_PROBED','PROBE_ERROR')`, win.startArg(), win.endArg()).Scan(&resp.Gaps)
	resp.GapsByOutcome = map[string]int64{}
	if grows, gerr := db.QueryContext(ctx,
		`SELECT outcome, COUNT(*) FROM probes WHERE started_at >= ? AND started_at <= ? AND classification IN ('NOT_PROBED','PROBE_ERROR') GROUP BY outcome`,
		win.startArg(), win.endArg()); gerr == nil {
		for grows.Next() {
			var o string
			var n int64
			if err := grows.Scan(&o, &n); err == nil {
				resp.GapsByOutcome[o] = n
			}
		}
		grows.Close()
	}

	if resp.ByPoint, err = s.rateByPoint(ctx, pop, popArgs...); err != nil {
		return nil, err
	}
	if resp.LatencyP50, resp.LatencyP95, resp.LatencySample, err = s.latencyWhere(ctx,
		`started_at >= ? AND started_at <= ? AND assigned = 1 AND phase = 'in_window'`, win.startArg(), win.endArg()); err != nil {
		return nil, err
	}

	reach, err := s.reachabilityNow(ctx, "", win.asOfArg())
	if err != nil {
		return nil, err
	}
	var reachable int64
	for _, v := range reach {
		if v.reachable {
			reachable++
		}
	}
	resp.Reachability = rate(reachable, int64(len(reach)))

	var beats, beatsUp int64
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*),
			COALESCE(SUM(CASE WHEN tcp_ok = 1 AND tls_ok = 1 THEN 1 ELSE 0 END), 0)
		FROM reachability WHERE started_at >= ? AND started_at <= ? AND outcome <> 'PROBE_ERROR'`, win.startArg(), win.endArg()).Scan(&beats, &beatsUp); err != nil {
		return nil, err
	}
	resp.ReachabilityWindow = rate(beatsUp, beats)

	_ = db.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(blob_size),0) FROM publications WHERE settlement_time >= ? AND settlement_time <= ?`, win.startArg(), win.endArg()).Scan(&resp.Publications, &resp.PublicationBytes)

	recon, err := s.reconstructableCount(ctx, win)
	if err != nil {
		return nil, err
	}
	resp.Reconstructable = recon

	// Past the raw retention the "all" window rests on the daily rollup
	// for the pruned days: fold it in and say so.
	rolled, label, err := s.rolledFor(ctx, win, "")
	if err != nil {
		return nil, err
	}
	if rolled != nil {
		resp.RolledUp = label
		for c, n := range rolled.Classes {
			resp.Classes[c] += n
		}
		resp.ServeRate = serveRate(resp.Classes)
		resp.Coverage = coverage(resp.Classes)
		resp.HeldOut = heldOut(resp.Classes)
		resp.Faults += rolled.Faults
		resp.ProbeCount += rolled.Probes
		resp.Gaps += rolled.Gaps
		addRolledObligations(&resp.Obligations, rolled.Obligations)
		resp.ByObligation = resp.Obligations.Rate
		resp.ReachabilityWindow = rate(beatsUp+rolled.BeatsUp, beats+rolled.Beats)
		// validators probed: the raw set and the rolled set together
		seen := map[string]bool{}
		for a := range rolled.ProbesByVal {
			seen[a] = true
		}
		if vrows, err := db.QueryContext(ctx, `SELECT DISTINCT validator_address FROM probes WHERE started_at >= ? AND started_at <= ?`, win.startArg(), win.endArg()); err == nil {
			for vrows.Next() {
				var a string
				if vrows.Scan(&a) == nil {
					seen[a] = true
				}
			}
			vrows.Close()
		}
		resp.ValidatorsProbed = int64(len(seen))
	}
	return &resp, nil
}

// reachState is the latest reachability evidence for one validator: the most
// recent heartbeat or probe row, whichever is newer.
type reachState struct {
	at             string
	host           string
	reachable      bool // TCP and TLS ok
	tcpOK          bool
	tlsOK          bool
	identityOK     bool
	identityReason string
	source         string // heartbeat | probe
}

// reachabilityNow returns the latest evidence per validator, or for just one
// when only is set: a request about a single validator has no reason to walk
// the whole set, and the detail page is the caller that asks for one.
func (s *Server) reachabilityNow(ctx context.Context, only, asOf string) (map[string]reachState, error) {
	out := map[string]reachState{}
	// The newest row per validator is the highest rowid: both files are
	// ingested in write order. MAX(rowid) GROUP BY uses the validator index
	// instead of a correlated MAX(started_at) per row over the whole table.
	// A pinned window (asOf) asks for the newest row started by then.
	rf, pf, args := "", "", []any{}
	if only != "" {
		rf, pf = " AND validator_address = ?", " AND validator_address = ?"
		args = []any{only, only}
	}
	if asOf != "" {
		rf += " AND started_at <= ?"
		pf += " AND started_at <= ?"
		if only != "" {
			args = []any{only, asOf, only, asOf}
		} else {
			args = []any{asOf, asOf}
		}
	}
	q := `SELECT validator_address, validator_host, started_at, tcp_ok, tls_ok, identity_ok, identity_reason, 'heartbeat' FROM reachability
	      WHERE rowid IN (SELECT MAX(rowid) FROM reachability WHERE outcome <> 'PROBE_ERROR'` + rf + ` GROUP BY validator_address)
	      UNION ALL
	      SELECT validator_address, validator_host, started_at, tcp_ok, tls_ok, identity_ok, identity_reason, 'probe' FROM probes
	      WHERE rowid IN (SELECT MAX(rowid) FROM probes WHERE outcome NOT IN ('MISSED','PROBE_ERROR')` + pf + ` GROUP BY validator_address)`
	rows, err := s.st.DB().QueryContext(ctx, q, args...)
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
		st.tcpOK, st.tlsOK = tcp == 1, tls == 1
		st.reachable = st.tcpOK && st.tlsOK
		st.identityOK = id == 1
		if cur, ok := out[addr]; !ok || st.at > cur.at {
			out[addr] = st
		}
	}
	return out, rows.Err()
}

// ---- validators ----

type validatorRow struct {
	Address     string `json:"address"`      // 20-byte consensus address, hex
	ConsAddress string `json:"cons_address"` // celestiavalcons1... when known from the registry
	// Moniker is the name the operator set in the staking module, read from
	// the chain itself. Empty when the chain has no validator at this
	// consensus address, or before identities have been polled once. A reader
	// recognises a validator by this, not by twenty hex characters.
	Moniker string `json:"moniker,omitempty"`
	// Operator is the celestiavaloper... address, for linking out.
	Operator string `json:"operator_address,omitempty"`
	// KeybaseIdentity is the operator's Keybase key suffix when it set one,
	// which is how an avatar could be resolved later. Deliberately NOT called
	// "identity": on this row that word already means the TLS consensus-key
	// binding this observer checks, and the two are unrelated.
	KeybaseIdentity string `json:"keybase_identity,omitempty"`
	Website         string `json:"website,omitempty"`
	// Jailed and BondStatus are the chain's own words about the validator,
	// unlike everything else on this row, which this observer measured. A
	// jailed validator still owes the shards it signed for, so these are
	// shown rather than used to drop anyone from the table.
	Jailed        bool    `json:"jailed"`
	BondStatus    string  `json:"bond_status,omitempty"`
	Host          string  `json:"host"`
	EndpointSince *string `json:"endpoint_since"`
	// LastHost and EndpointClosedAt describe the newest endpoint row that
	// has closed, for a validator with no open one: the host this observer
	// last saw registered and when it stopped appearing in the bonded
	// provider list. Host stays empty for such a validator; it used to be
	// back-filled from the newest probe row, which made the table's word for
	// a jailed validator depend on whether the prober had restarted since.
	LastHost         string  `json:"last_host,omitempty"`
	EndpointClosedAt *string `json:"endpoint_closed_at,omitempty"`
	VotingPower      int64   `json:"voting_power"` // from the latest assignment seen
	LastSeenAt       *string `json:"last_seen_at"`
	Reachable        *bool   `json:"reachable"`       // latest heartbeat or probe; null if never probed
	IdentityStatus   string  `json:"identity_status"` // verified | expired | mismatch | unverified | no_tls | unreachable | unknown
	IdentityReason   string  `json:"identity_reason,omitempty"`
	// Reachability is how often this observer completed a TLS conversation with the
	// endpoint over the window, from the reachability heartbeat: every
	// registered validator, every five minutes, whether or not it was assigned
	// anything. It is the closest thing here to "is the Fibre service
	// running", and unlike the serve rate its coverage does not depend on
	// attestation — an operator the publisher never collected a signature
	// from still gets 288 samples a day.
	//
	// It is not an accusation. Half of every path measured here is this
	// observer's own, so a dip is a statement about a route as much as about
	// a server, which is why it is published beside the serve rate rather
	// than folded into it. It is not signing uptime either: a validator
	// can sign every block with its Fibre endpoint down, and the reverse.
	Reachability Rate `json:"reachability_window"`
	// IdentityValid is how often the certificate presented was endorsed by
	// this validator's consensus key, over the heartbeats that got far enough
	// to see a certificate. A validator whose endpoint is up but whose
	// endorsement has lapsed is serving nothing a client will accept, and
	// nothing in the serve rate says so.
	IdentityValid Rate `json:"identity_rate_window"`
	// LastUnreachableAt is the most recent heartbeat in the window that could
	// not complete TLS, and LastReachableAt the most recent that did, so a
	// reader can tell a single outage from a service that is flapping, and
	// the table can say how long the current state has held.
	LastUnreachableAt *string `json:"last_unreachable_at"`
	LastReachableAt   *string `json:"last_reachable_at"`
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
	// Obligations counts one observation per (validator, blob) instead of
	// one per probe, judged by the newest probe: see obligationStats. This
	// is the headline figure; ByObligation repeats its rate.
	Obligations  obligationStats  `json:"obligations"`
	ByObligation Rate             `json:"serve_rate_by_obligation"`
	HeldOut      map[string]int64 `json:"serve_rate_held_out"`
	Attestation  attestationStats `json:"attestation"`
	ProbeCount   int64            `json:"probe_count"`
	Classes      classCounts      `json:"classes"`
	// Faults is every FAULT of an assigned shard in the window, in any
	// phase; see networkResponse.Faults.
	Faults           int64  `json:"faults"`
	AssignedRowsLast int    `json:"assigned_rows_last"`
	ExpectedLoadBand string `json:"expected_load_band"` // floor | low | mid | high, by assigned rows
	// Latency is how long this observer waited for a shard it did get: the
	// median and 95th percentile of the whole probe, dial to verified rows,
	// over the probes that came back HEALTHY in this window.
	//
	// Nothing else on this site measures performance, and Fibre exists to be
	// fast — a validator that serves everything in twenty seconds is not doing
	// its job, and every other figure here would call it perfect. The numbers
	// are published without a threshold and without a word attached: this
	// observer sits in one place, so part of every millisecond is its own
	// path, and naming a validator "slow" from one vantage would be the same
	// mistake as calling one unreachable from one vantage.
	//
	// BytesPerSecond is the size-normalised companion: the median transfer
	// rate over the download step alone, bytes handed over divided by the
	// time DownloadShard took. Assignments run from 148 rows to 4,096, so raw
	// duration is not comparable between validators, and neither was rows
	// per second over the whole probe: the dial, handshake and identity
	// check cost the same for a small shard as for a large one, so the old
	// figure rose with stake by construction. ThroughputSample is how many
	// healthy probes carried a byte count; records from before the count
	// existed are left out.
	LatencyP50       *int64 `json:"serve_latency_p50_ms"`
	LatencyP95       *int64 `json:"serve_latency_p95_ms"`
	LatencySample    int64  `json:"serve_latency_sample"`
	BytesPerSecond   *int64 `json:"serve_bytes_per_second"`
	ThroughputSample int64  `json:"serve_throughput_sample"`
	// ByPoint is this validator's serve rate per schedule point, the same
	// breakdown /v1/network publishes for the whole set. The points sit at
	// different fractions of the retention window, so a validator that serves
	// early and not late has pruned before it was allowed to, and a validator
	// that is uniformly poor has a different problem. The pooled rate cannot
	// tell those apart, and pruning early is the specific failure this
	// observer exists to catch.
	ByPoint []stratum `json:"serve_rate_by_point"`
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
	// TimeoutsEnforced is how many MsgPaymentPromiseTimeout this validator's
	// operator account submitted in the window: promises it held that the
	// publisher abandoned, reported to the chain so the escrow was charged.
	// The chain pays nothing for it; a count above zero says the operator
	// runs the enforcement path at all. Matched on address bytes, so an
	// operator that submits from another account is not counted.
	TimeoutsEnforced int64 `json:"timeouts_enforced"`
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
		if st.tcpOK && !st.tlsOK {
			return "no_tls"
		}
		return "unreachable"
	}
	if st.identityOK {
		return "verified"
	}
	switch st.identityReason {
	case "":
		// TCP and TLS both succeeded, but no identity verdict was recorded
		// (an older row, or a probe that stopped before the identity step).
		return "unverified"
	case "cert_expired", "cert_not_yet_valid", "window_empty", "window_too_long":
		// The right key endorsed it and the signed window has lapsed: a
		// renewal running late, which the prober files as IDENTITY_EXPIRED.
		// Calling it a mismatch accused the operator of the wrong thing.
		return "expired"
	}
	return "mismatch"
}

func (s *Server) validatorRows(ctx context.Context, win Window, only string) ([]validatorRow, error) {
	db := s.st.DB()
	// Every aggregate below groups by validator, and a request for one
	// validator used to compute all of them and throw the rest away at the
	// end. On a store with 60 validators that made the detail page 1.4s, and
	// the cost is in the population, not the window: the assignment join alone
	// walks every assignment of every publication. With `only` pushed into the
	// SQL each of these becomes an index seek on validator_address.
	//
	// `vfilter` is appended last in every query it appears in, so its argument
	// is appended last too.
	vfilter := func(col string) string {
		if only == "" {
			return ""
		}
		return " AND " + col + " = ?"
	}
	vargs := func(base ...any) []any {
		if only == "" {
			return base
		}
		return append(base, only)
	}
	// The points the observer does not trust itself at, left out of every
	// per-validator population below exactly as they are network-wide.
	_, ss, err := s.suspectPoints(ctx, win)
	if err != nil {
		return nil, err
	}
	sus := ss.clause("scheduled_at")
	winArgs := append([]any{win.startArg(), win.endArg()}, ss.args...)
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
	// The newest closed endpoint row, for validators with no open one: what
	// was registered and when it left the bonded list. Ordered so the newest
	// closure per validator is the one that lands.
	crows, err := db.QueryContext(ctx, `SELECT validator_cons_address, host, closed_at FROM endpoints
		WHERE closed_at IS NOT NULL ORDER BY closed_at`)
	if err != nil {
		return nil, err
	}
	for crows.Next() {
		var cons, host, closed string
		if err := crows.Scan(&cons, &host, &closed); err != nil {
			crows.Close()
			return nil, err
		}
		hexAddr, err := consHex(cons)
		if err != nil {
			continue
		}
		v := get(hexAddr)
		if v.ConsAddress == "" {
			v.ConsAddress = cons
		}
		if v.Host == "" {
			v.LastHost = host
			at := closed
			v.EndpointClosedAt = &at
		}
	}
	crows.Close()
	if err := crows.Err(); err != nil {
		return nil, err
	}
	// Voting power and row count come from the newest publication each
	// validator actually appears in, not from the newest publication overall.
	// Reading them from the latest publication alone rendered a validator
	// that had left the set as zero power with zero rows beside its fault
	// count, and the table sorts faults to the top: the row that looked worst
	// was the one we had the least current information about. The height the
	// figures come from is published with them.
	innerWhere, outerWhere := "", ""
	assignArgs := []any{}
	if only != "" {
		innerWhere, outerWhere = " WHERE a2.validator_address = ?", " WHERE a.validator_address = ?"
		assignArgs = []any{only, only}
	}
	tieHash := map[string]string{}
	rows, err := db.QueryContext(ctx, `SELECT a.validator_address, a.voting_power, a.row_count, a.attested, p.settlement_height, a.promise_hash
		FROM assignments a
		JOIN publications p ON p.promise_hash = a.promise_hash
		JOIN (
			SELECT a2.validator_address AS va, MAX(p2.settlement_height) AS h
			FROM assignments a2 JOIN publications p2 ON p2.promise_hash = a2.promise_hash`+innerWhere+`
			GROUP BY a2.validator_address
		) m ON m.va = a.validator_address AND m.h = p.settlement_height`+outerWhere, assignArgs...)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var addr string
		var vp int64
		var rc int
		var att sql.NullInt64
		var h int64
		var ph string
		if err := rows.Scan(&addr, &vp, &rc, &att, &h, &ph); err != nil {
			rows.Close()
			return nil, err
		}
		v := get(addr)
		// A block can carry several publications; keep the row with the
		// highest height and, within one block, the greatest promise hash,
		// so two snapshots of the same data agree whatever order SQLite
		// hands the rows back in.
		if v.AssignmentHeight > h || (v.AssignmentHeight == h && tieHash[addr] >= ph) {
			continue
		}
		v.AssignmentHeight, tieHash[addr] = h, ph
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
	frows, err := db.QueryContext(ctx, `SELECT validator_address, COUNT(*) FROM probes
		WHERE started_at >= ? AND started_at <= ? AND assigned = 1 AND classification = 'FAULT'`+sus+vfilter("validator_address")+`
		GROUP BY validator_address`, vargs(winArgs...)...)
	if err != nil {
		return nil, err
	}
	for frows.Next() {
		var addr string
		var n int64
		if err := frows.Scan(&addr, &n); err != nil {
			frows.Close()
			return nil, err
		}
		get(addr).Faults = n
	}
	frows.Close()
	if err := frows.Err(); err != nil {
		return nil, err
	}
	// classes per validator in window
	rows, err = db.QueryContext(ctx, `SELECT validator_address, classification, COUNT(*) FROM probes
		WHERE started_at >= ? AND started_at <= ? AND assigned = 1 AND phase = 'in_window'`+sus+vfilter("validator_address")+`
		GROUP BY validator_address, classification`, vargs(winArgs...)...)
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
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// The retention profile per validator: the same population as the classes
	// above, sliced by schedule point.
	rows, err = db.QueryContext(ctx, `SELECT validator_address, schedule_label,
			COALESCE(SUM(CASE WHEN classification = 'HEALTHY' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN classification = 'FAULT' THEN 1 ELSE 0 END), 0)
		FROM probes WHERE started_at >= ? AND started_at <= ? AND assigned = 1 AND phase = 'in_window'`+sus+vfilter("validator_address")+`
		GROUP BY validator_address, schedule_label
		ORDER BY validator_address, schedule_label`, vargs(winArgs...)...)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var addr string
		var st stratum
		var ok, bad int64
		if err := rows.Scan(&addr, &st.Key, &ok, &bad); err != nil {
			rows.Close()
			return nil, err
		}
		st.Rate = rate(ok, ok+bad)
		v := get(addr)
		v.ByPoint = append(v.ByPoint, st)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// How long a served shard took, per validator. Percentiles rather than a
	// mean: a mean over a few hundred probes is moved by one timeout, and the
	// figure a reader wants is "what does it usually take" beside "what does
	// it take when it is bad".
	//
	// HEALTHY only. A probe that failed has a duration too, and it measures
	// how long a failure took, which is not a service figure. total_duration_ms
	// is the whole probe — dial, TLS, DownloadShard, row verification against
	// the commitment — because that is what a client actually waits for.
	//
	// Throughput is over the download step alone, bytes over download_ms,
	// and ranked on its own (rb): the probe with the median duration can
	// carry the highest transfer rate of the set. Records without a byte
	// count sort last and are outside the throughput sample (cb).
	rows, err = db.QueryContext(ctx, `SELECT validator_address,
			MAX(CASE WHEN rn = (c + 1) / 2          THEN ms END),
			MAX(CASE WHEN rn = (c * 95 + 99) / 100  THEN ms END),
			MAX(c),
			MAX(CASE WHEN rb = (cb + 1) / 2         THEN bps END),
			MAX(cb)
		FROM (
			SELECT validator_address AS validator_address,
			       total_duration_ms AS ms,
			       CASE WHEN bytes_returned > 0 AND download_ms > 0 THEN bytes_returned * 1000 / download_ms END AS bps,
			       ROW_NUMBER() OVER (PARTITION BY validator_address ORDER BY total_duration_ms) AS rn,
			       COUNT(*)     OVER (PARTITION BY validator_address)                            AS c,
			       ROW_NUMBER() OVER (PARTITION BY validator_address
			                          ORDER BY (bytes_returned IS NULL OR download_ms <= 0), bytes_returned * 1000.0 / NULLIF(download_ms, 0)) AS rb,
			       SUM(CASE WHEN bytes_returned > 0 AND download_ms > 0 THEN 1 ELSE 0 END)
			                    OVER (PARTITION BY validator_address)                            AS cb
			FROM probes
			WHERE started_at >= ? AND started_at <= ? AND assigned = 1 AND phase = 'in_window'
			  AND classification = 'HEALTHY' AND total_duration_ms > 0`+vfilter("validator_address")+`
		) GROUP BY validator_address`, vargs(win.startArg(), win.endArg())...)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var addr string
		var p50, p95, bps sql.NullInt64
		var n, nb int64
		if err := rows.Scan(&addr, &p50, &p95, &n, &bps, &nb); err != nil {
			rows.Close()
			return nil, err
		}
		v := get(addr)
		v.LatencySample = n
		if p50.Valid {
			x := p50.Int64
			v.LatencyP50 = &x
		}
		if p95.Valid {
			x := p95.Int64
			v.LatencyP95 = &x
		}
		v.ThroughputSample = nb
		if bps.Valid && nb > 0 {
			x := bps.Int64
			v.BytesPerSecond = &x
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// proven / unproven / unknown per validator, same scope as the classes
	// above so the serve rate and its exclusions line up. Counted twice: once
	// per probe, which is what the rate's population is, and once per
	// (validator, blob) obligation, which is what an operator reads. The two
	// differ by the size of the probe schedule, so publishing only the first
	// would inflate every disclosure about a named validator fourfold.
	rows, err = db.QueryContext(ctx, `SELECT validator_address,
			COALESCE(SUM(CASE WHEN attested = 1 THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN attested = 0 THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN attested IS NULL THEN 1 ELSE 0 END), 0)
		FROM probes WHERE started_at >= ? AND started_at <= ? AND assigned = 1 AND phase = 'in_window'`+vfilter("validator_address")+`
		GROUP BY validator_address`, vargs(win.startArg(), win.endArg())...)
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
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows, err = db.QueryContext(ctx, `SELECT validator_address,
			COALESCE(SUM(CASE WHEN a = 1 THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN a = 0 THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN a IS NULL THEN 1 ELSE 0 END), 0)
		FROM (
			SELECT validator_address AS validator_address, MAX(attested) AS a
			FROM probes WHERE started_at >= ? AND started_at <= ? AND assigned = 1 AND phase = 'in_window'`+vfilter("validator_address")+`
			GROUP BY validator_address, promise_hash
		) GROUP BY validator_address`, vargs(win.startArg(), win.endArg())...)
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
		v.Attestation.AttestedBlobs, v.Attestation.UnattestedBlobs, v.Attestation.UnknownBlobs = at, un, unk
		v.Attestation.BlobCoverage = rate(at, at+un)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows, err = db.QueryContext(ctx, `SELECT validator_address, COUNT(*), MAX(started_at) FROM probes
		WHERE started_at >= ? AND started_at <= ?`+vfilter("validator_address")+` GROUP BY validator_address`, vargs(win.startArg(), win.endArg())...)
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
	// The heartbeat history, which until now was written every five minutes for
	// every registered validator and read only for its newest row. It is the
	// one stability signal here whose coverage does not depend on being
	// assigned or attested anything, which is exactly what an operator asking
	// "is my Fibre server up" needs.
	// A PROBE_ERROR heartbeat is the observer's own failure (a host it could
	// not parse, an identity check it starved of CPU) and is left out of
	// every count, as its probe-side twin is.
	hrows, err := db.QueryContext(ctx, `SELECT validator_address, COUNT(*),
			COALESCE(SUM(CASE WHEN tcp_ok = 1 AND tls_ok = 1 THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN tcp_ok = 1 AND tls_ok = 1 AND identity_ok = 1 THEN 1 ELSE 0 END), 0),
			MAX(CASE WHEN tcp_ok = 1 AND tls_ok = 1 THEN NULL ELSE started_at END),
			MAX(CASE WHEN tcp_ok = 1 AND tls_ok = 1 THEN started_at END)
		FROM reachability WHERE started_at >= ? AND started_at <= ? AND outcome <> 'PROBE_ERROR'`+vfilter("validator_address")+`
		GROUP BY validator_address`, vargs(win.startArg(), win.endArg())...)
	if err != nil {
		return nil, err
	}
	for hrows.Next() {
		var addr string
		var seen, up, ident int64
		var lastDown, lastUp sql.NullString
		if err := hrows.Scan(&addr, &seen, &up, &ident, &lastDown, &lastUp); err != nil {
			hrows.Close()
			return nil, err
		}
		v := get(addr)
		v.Reachability = rate(up, seen)
		// Denominator is the heartbeats that reached TLS, not all of them: an
		// unreachable endpoint presented no certificate, and counting that as
		// an identity failure would report the same outage twice.
		v.IdentityValid = rate(ident, up)
		if lastDown.Valid {
			at := lastDown.String
			v.LastUnreachableAt = &at
		}
		if lastUp.Valid {
			at := lastUp.String
			v.LastReachableAt = &at
		}
	}
	hrows.Close()
	if err := hrows.Err(); err != nil {
		return nil, err
	}
	reach, err := s.reachabilityNow(ctx, only, win.asOfArg())
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
		if v.LastSeenAt == nil || st.at > *v.LastSeenAt {
			at := st.at
			v.LastSeenAt = &at
		}
	}
	// Names from the staking module, and a row for every bonded validator
	// whether or not anything has been measured about it yet.
	//
	// This used to name only validators the observer already had a reason to
	// show, on the grounds that a name is not evidence and listing the whole
	// chain would bury the ones with a Fibre endpoint. That reasoning holds
	// after Fibre activates — and before it, it leaves the page empty. On a
	// chain below app version 10 there is no x/valaddr to register in and no
	// publication to be assigned, so nothing produces a row, and the site that
	// exists to watch these validators cannot say which ones it is watching.
	//
	// Seeding from the bonded set fixes that without becoming a second mode:
	// every bonded validator is assigned every blob, so after activation these
	// rows are a subset of what the assignment table produces anyway. Before
	// it, they are the whole answer to "am I in your list", with every measured
	// column honestly empty.
	irows, err := db.QueryContext(ctx, `SELECT cons_address, operator_address, moniker, identity, website, jailed, status, tokens
		FROM validator_identities`)
	if err != nil {
		return nil, err
	}
	for irows.Next() {
		var addr, op, moniker, identity, website, status, tokens string
		var jailed int
		if err := irows.Scan(&addr, &op, &moniker, &identity, &website, &jailed, &status, &tokens); err != nil {
			irows.Close()
			return nil, err
		}
		hexAddr := strings.ToLower(addr)
		v, known := byAddr[hexAddr]
		if !known {
			// Only the active set gets a row of its own: an unbonded validator
			// with nothing measured has no Fibre obligation to report on, and
			// one WITH something measured is already in byAddr from its probes.
			if status != "BOND_STATUS_BONDED" || (only != "" && hexAddr != only) {
				continue
			}
			v = get(hexAddr)
		}
		v.Moniker, v.Operator, v.KeybaseIdentity, v.Website = moniker, op, identity, website
		v.Jailed, v.BondStatus = jailed == 1, status
		// Voting power from the staking module, only where no assignment has
		// given one. After activation the assignment's figure wins: it is the
		// power the shard split was actually computed from, at a height this
		// row publishes, rather than the power right now.
		if v.VotingPower == 0 {
			if n, err := strconv.ParseInt(tokens, 10, 64); err == nil {
				v.VotingPower = n / 1_000_000
			}
		}
	}
	irows.Close()
	if err := irows.Err(); err != nil {
		return nil, err
	}

	// one observation per (validator, blob): see obligationStats.
	byObligation, err := s.obligationsByValidator(ctx, win, ss, vfilter("pr.validator_address"), vargs()...)
	if err != nil {
		return nil, err
	}

	timeouts, err := s.timeoutsByAccount(ctx, win)
	if err != nil {
		return nil, err
	}
	// Past the raw retention the "all" window folds the daily rollup in
	// for the pruned days (see rolledFor).
	rolled, _, err := s.rolledFor(ctx, win, only)
	if err != nil {
		return nil, err
	}
	if rolled != nil {
		for addr, rp := range rolled.ProbesByVal {
			v := get(addr)
			for c, n := range rp.Classes {
				v.Classes[c] += n
			}
			v.Faults += rp.Faults
			v.ProbeCount += rp.Probes
			v.Reachability = rate(v.Reachability.Num+rp.BeatsUp, v.Reachability.Den+rp.Beats)
		}
		for addr, ro := range rolled.ObligationsByVal {
			get(addr)
			o := byObligation[addr]
			addRolledObligations(&o, ro)
			byObligation[addr] = o
		}
	}
	out := make([]validatorRow, 0, len(byAddr))
	for addr, v := range byAddr {
		if only != "" && addr != only {
			continue
		}
		v.ServeRate = serveRate(v.Classes)
		v.Coverage = coverage(v.Classes)
		v.HeldOut = heldOut(v.Classes)
		v.Obligations = byObligation[addr]
		v.ByObligation = v.Obligations.Rate
		if v.Operator != "" {
			v.TimeoutsEnforced = timeouts[accountKey(v.Operator)]
		}
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
	if win.AsOf {
		if !s.asOf.allow(time.Now()) {
			w.Header().Set("Retry-After", "2")
			writeErr(w, 429, "as_of requests are limited to one every two seconds")
			return
		}
		t0 := time.Now()
		rows, err := s.validatorRows(r.Context(), win, "")
		if err != nil {
			s.writeInternal(w, r.URL.Path, err)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, 200, map[string]any{
			"window": win, "vantage": s.vantage, "validators": rows, "as_of_note": AsOfNote,
			"computed_at": t0.UTC().Format(time.RFC3339Nano), "compute_ms": time.Since(t0).Milliseconds(),
		})
		return
	}
	rows, at, ms, err := s.vals.get(r.Context(), s.logf(), win)
	if err != nil {
		s.writeInternal(w, r.URL.Path, err)
		return
	}
	out := map[string]any{
		"window": win, "vantage": s.vantage, "validators": rows,
		"computed_at": at.UTC().Format(time.RFC3339Nano), "compute_ms": ms,
	}
	if _, label, err := s.rolledFor(r.Context(), win, ""); err == nil && label != nil {
		out["rolled_up"] = label
	}
	writeJSON(w, 200, out)
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
		// Count is every assigned in-window probe in this window, rated or
		// held out, not every row for the validator (validator.probe_count).
		Count        int64            `json:"probe_count"`
		Coverage     Rate             `json:"serve_rate_coverage"`
		Obligations  obligationStats  `json:"obligations"`
		ByObligation Rate             `json:"serve_rate_by_obligation"`
		HeldOut      map[string]int64 `json:"serve_rate_held_out"`
		Classes      classCounts      `json:"classes"`
		RolledUp     *rolledUp        `json:"rolled_up,omitempty"`
	}
	var spans []span
	// The suspect points over all time, so the recent-probes list can mark
	// rows that no rate counts.
	suspectAll := []suspectPoint{}
	for _, name := range []string{"24h", "7d", "30d", "all"} {
		sw := Window{Name: name, Span: windows[name], End: now}
		if sw.Span > 0 {
			sw.Start = now.Add(-sw.Span)
		}
		_, ss, err := s.suspectPoints(ctx, sw)
		if err != nil {
			s.writeInternal(w, r.URL.Path, err)
			return
		}
		if sw.Name == "all" {
			suspectAll = ss.points
		}
		classes, total, err := s.classCountsWhere(ctx, `validator_address = ? AND started_at >= ? AND started_at <= ? AND assigned = 1 AND phase = 'in_window'`+ss.clause("scheduled_at"),
			append([]any{addr, sw.startArg(), sw.endArg()}, ss.args...)...)
		if err != nil {
			s.writeInternal(w, r.URL.Path, err)
			return
		}
		obl, err := s.obligationsWhere(ctx, sw, ss, ` AND pr.validator_address = ?`, addr)
		if err != nil {
			s.writeInternal(w, r.URL.Path, err)
			return
		}
		rolled, label, err := s.rolledFor(ctx, sw, addr)
		if err != nil {
			s.writeInternal(w, r.URL.Path, err)
			return
		}
		if rolled != nil {
			if rp, ok := rolled.ProbesByVal[addr]; ok {
				for c, n := range rp.Classes {
					classes[c] += n
					total += n
				}
			}
			addRolledObligations(&obl, rolled.ObligationsByVal[addr])
		}
		spans = append(spans, span{
			Window: sw, Rate: serveRate(classes), Count: total,
			Coverage: coverage(classes), Obligations: obl, ByObligation: obl.Rate, HeldOut: heldOut(classes), Classes: classes,
			RolledUp: label,
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
	out := map[string]any{
		"window":                      win,
		"validator":                   rows[0],
		"windows":                     spans,
		"recent_probes":               probes,
		"suspect_points":              suspectAll,
		"serve_rate_excluded_classes": excludedFromRate,
		"vantage":                     s.vantage,
	}
	if _, label, err := s.rolledFor(ctx, win, addr); err == nil && label != nil {
		out["rolled_up"] = label
	}
	writeJSON(w, 200, out)
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
	// Charge is the fee side of this promise from the payments table: what
	// the module charged, and whether the promise settled or timed out. Null
	// for a publication whose payment was not recorded (ingested before the
	// scanner wrote payments).
	Charge *blobCharge `json:"charge"`
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
	hashes := make([]string, len(out))
	for i := range out {
		hashes[i] = out[i].PromiseHash
	}
	charges, err := s.chargesFor(ctx, hashes)
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Charge = charges[out[i].PromiseHash]
	}
	// Per blob, deliberately. Batching both of these was tried and measured on
	// a store with 2,200 publications: the class tally got about 8% slower,
	// because fifty seeks on probes_promise beat one CTE-joined GROUP BY, and
	// batching the row lists was far worse, taking /v1/blobs?limit=200 from
	// 1.35s to 3.58s by pulling every in-window point's JSON when only the
	// chosen point is wanted. A page is a few hundred rows at most. The batch
	// is kept for the network summary, which examines two thousand and
	// publishes no row count.
	//
	// What made the page fast was not batching but not recomputing: both of
	// these are functions of the publication's probes, which are append-only,
	// so a settled verdict is settled for good. One query fetches a
	// fingerprint of every row's probes and the rest is a map lookup. See
	// blobcache.go.
	fps, err := s.probeFingerprints(ctx, where, limit, args...)
	if err != nil {
		return nil, err
	}
	for i := range out {
		hash := out[i].PromiseHash
		fp := fps[hash]
		if v, ok := s.blobs.get(hash, fp); ok {
			out[i].Classes, out[i].ProbeCount, out[i].Reconstructable = v.classes, v.total, v.rc
			continue
		}
		classes, total, err := s.classCountsWhere(ctx, `promise_hash = ?`, hash)
		if err != nil {
			return nil, err
		}
		out[i].Classes, out[i].ProbeCount = classes, total
		rc, err := s.reconstructable(ctx, hash)
		if err != nil {
			return nil, err
		}
		out[i].Reconstructable = rc
		// Only once the obligation has ended. window_over is the one part of a
		// verdict that depends on the clock rather than on the store, and
		// caching it before it flips would freeze "still under obligation" onto
		// a blob whose deadline has since passed.
		if rc != nil && rc.WindowOver {
			s.blobs.put(hash, blobVerdict{fp: fp, classes: classes, total: total, rc: rc})
		}
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
		WHERE p.promise_hash = ? AND p.scheduled_at = ? AND p.assigned = 1 AND p.phase = 'in_window' AND p.outcome = 'SERVED_OK'`, hash, pointAt)
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
		`SELECT COUNT(*) FROM publications WHERE settlement_time >= ? AND settlement_time <= ?`, win.startArg(), win.endArg()).Scan(&out.PublicationsInWindow); err != nil {
		return out, err
	}
	// Statuses only: the summary publishes no row count, so the bounds settle
	// every verdict and not one row list is parsed.
	verdicts, err := s.reconstructBatch(ctx, `settlement_time >= ? AND settlement_time <= ?`, reconstructSample, win.startArg(), win.endArg())
	if err != nil {
		return out, err
	}
	out.Examined = int64(len(verdicts))
	for _, rc := range verdicts {
		if rc == nil {
			out.Unknown++
			continue
		}
		switch rc.Status {
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

// blobPageDefault is the page size /v1/blobs answers with when the caller does
// not ask for one, and the size the startup warm-up fills the verdict cache to.
const blobPageDefault = 50

func (s *Server) handleBlobs(w http.ResponseWriter, r *http.Request) {
	limit, err := parseLimit(r, blobPageDefault, 500)
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
	// Moniker is the name from the staking module, so this table reads like
	// a list of validators rather than a list of hashes. Empty when the chain
	// has no validator at this consensus address.
	Moniker     string `json:"moniker,omitempty"`
	VotingPower int64  `json:"voting_power"`
	RowCount    int    `json:"row_count"`
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
	rows, err := s.st.DB().QueryContext(ctx, `SELECT a.validator_address, a.voting_power, a.row_count, a.attested,
			COALESCE(i.moniker, '')
		FROM assignments a
		LEFT JOIN validator_identities i ON i.cons_address = a.validator_address
		WHERE a.promise_hash = ? ORDER BY a.voting_power DESC, a.validator_address`, hash)
	if err != nil {
		s.writeInternal(w, r.URL.Path, err)
		return
	}
	var assigns []assignmentRow
	for rows.Next() {
		var a assignmentRow
		var att sql.NullInt64
		if err := rows.Scan(&a.ValidatorAddress, &a.VotingPower, &a.RowCount, &att, &a.Moniker); err != nil {
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
		// Day, Secret and RevealedAt are filled once the prober has
		// published the day's secret (sampling-secrets.jsonl): from then
		// on H(promise_hash || secret) < p * 2^64 can be recomputed by
		// anyone for every promise settled that day.
		Day        *string `json:"day"`
		Secret     *string `json:"secret"`
		RevealedAt *string `json:"revealed_at"`
	}
	secrets := map[string]struct{ day, secret, at string }{}
	if srows, err := s.st.DB().QueryContext(ctx, `SELECT commitment, day, secret, revealed_at FROM sampling_secrets`); err == nil {
		for srows.Next() {
			var c, d, sec, at string
			if srows.Scan(&c, &d, &sec, &at) == nil {
				secrets[c] = struct{ day, secret, at string }{d, sec, at}
			}
		}
		srows.Close()
	}
	rows, err := s.st.DB().QueryContext(ctx, `SELECT
			COALESCE(sampling_commitment, '') AS c,
			COALESCE(sampling_binding, '') AS b,
			COALESCE(sampling_p, 1.0) AS p,
			COUNT(DISTINCT promise_hash),
			COUNT(DISTINCT CASE WHEN classification != 'NOT_PROBED' THEN promise_hash END),
			COUNT(DISTINCT CASE WHEN classification = 'NOT_PROBED' AND classification_reason LIKE 'budget:%' THEN promise_hash END)
		FROM probes WHERE started_at >= ? AND started_at <= ?
		GROUP BY c, b, p ORDER BY c, p`, win.startArg(), win.endArg())
	if err != nil {
		s.writeInternal(w, r.URL.Path, err)
		return
	}
	defer rows.Close()
	days := []day{}
	revealed := 0
	for rows.Next() {
		var d day
		if err := rows.Scan(&d.DayCommitment, &d.Binding, &d.P, &d.Publications, &d.Probed, &d.SampledOut); err != nil {
			s.writeInternal(w, r.URL.Path, err)
			return
		}
		if sec, ok := secrets[d.DayCommitment]; ok {
			dd, ss, at := sec.day, sec.secret, sec.at
			d.Day, d.Secret, d.RevealedAt = &dd, &ss, &at
			revealed++
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
			"The prober publishes a day's secret " + policyRevealNote + " after the day ends (the row's secret field; " +
			"the record is sampling-secrets.jsonl, also in the daily export). With it, recompute " +
			"H(promise_hash || secret) < p * 2^64 for every MsgPayForFibre settled that day: the promise hashes that " +
			"pass are the ones this observer should have probed, and /v1/probes says which ones it did. " +
			"sentinel-recompute -sampling does this from the export.",
		"days_listed":      len(days),
		"secrets_revealed": revealed,
		"secret_published": revealed > 0,
	})
}

// policyRevealNote is the reveal delay as the prober's default (policy.
// DefaultRevealAfter); the API does not import the policy package.
const policyRevealNote = "seven days"

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
	// The evidence behind the verdict, when the row carries it (rows from
	// before schema 9 do not): the row indices returned, a digest of the
	// returned payload, the gRPC status code, the promise whose shard
	// answered instead, and the observer build and chain app version the
	// classification was made under.
	RowIndices    []uint32 `json:"row_indices,omitempty"`
	RowsSHA256    string   `json:"rows_sha256,omitempty"`
	RPCCode       string   `json:"rpc_code,omitempty"`
	ShadowedBy    string   `json:"shadowed_by,omitempty"`
	ObserverBuild string   `json:"observer_build,omitempty"`
	AppVersion    int64    `json:"app_version,omitempty"`
}

func (s *Server) probeRows(ctx context.Context, where string, limit int, args ...any) ([]probeRow, error) {
	q := `SELECT vantage, promise_hash, validator_address, validator_host, assigned, attested, assigned_row_count, schedule_label, scheduled_at,
		started_at, phase, outcome, classification, classification_reason, rows_returned, rows_expected, total_duration_ms, tls_ok, identity_ok, raw_error,
		COALESCE(retry_first_outcome, ''), COALESCE(clock_offset_ms, 0),
		COALESCE(row_indices, ''), COALESCE(rows_sha256, ''), COALESCE(rpc_code, ''), COALESCE(shadowed_by, ''), COALESCE(observer_build, ''), COALESCE(app_version, 0)
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
		var idxJSON string
		if err := rows.Scan(&p.Vantage, &p.PromiseHash, &p.ValidatorAddress, &p.ValidatorHost, &assigned, &att, &p.AssignedRowCount, &p.ScheduleLabel,
			&p.ScheduledAt, &p.StartedAt, &p.Phase, &p.Outcome, &p.Classification, &p.Reason, &p.RowsReturned, &p.RowsExpected,
			&p.TotalDurationMS, &tls, &id, &p.RawError, &p.RetryFirstOutcome, &p.ClockOffsetMS,
			&idxJSON, &p.RowsSHA256, &p.RPCCode, &p.ShadowedBy, &p.ObserverBuild, &p.AppVersion); err != nil {
			return nil, err
		}
		if idxJSON != "" {
			_ = json.Unmarshal([]byte(idxJSON), &p.RowIndices)
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
	// at: one schedule point, exactly as vantage_health.suspect lists it, so
	// the rows behind an incident are one link away.
	if at := q.Get("at"); at != "" {
		conds, args = append(conds, `scheduled_at = ?`), append(args, at)
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

// ---- exports ----

// exportsDir is where the collector builds the daily exports.
func (s *Server) exportsDir() string {
	if s.dataDir == "" {
		return ""
	}
	return filepath.Join(s.dataDir, "exports")
}

// handleExports lists the daily exports: one tarball per UTC day holding
// every record file's lines for that day, with a manifest of digests. It
// is what a verifier downloads; sentinel-recompute re-derives every verdict
// and every published figure from it.
func (s *Server) handleExports(w http.ResponseWriter, r *http.Request) {
	entries := []export.Entry{}
	if dir := s.exportsDir(); dir != "" {
		var err error
		if entries, err = export.ReadIndex(dir); err != nil {
			s.writeInternal(w, r.URL.Path, err)
			return
		}
	}
	writeJSON(w, 200, map[string]any{
		"vantage": s.vantage,
		"exports": entries,
		"how_to_verify": "download /v1/exports/<name>, check its sha256 against the entry (and the .sha256 sidecar), " +
			"untar, check each member against manifest.json, then run sentinel-recompute on the directory: it re-derives " +
			"every row's phase and classification from the row's own fields and the run's recorded configuration, and every " +
			"obligation figure from the rows, and prints what differs from this API's /v1/validators?as_of=<day end>.",
		"rule": "records are assigned to a day by their own timestamp; a record that reached the file after its day's export was built is in the next export, counted as late",
	})
}

// handleExportFile serves one export or its digest sidecar. Names are
// checked against the export name pattern, so nothing else under the
// directory is reachable.
func (s *Server) handleExportFile(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	dir := s.exportsDir()
	if dir == "" || !export.NamePattern.MatchString(name) {
		writeErr(w, 404, "no such export")
		return
	}
	path := filepath.Join(dir, name)
	f, err := os.Open(path)
	if err != nil {
		writeErr(w, 404, "no such export")
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.IsDir() {
		writeErr(w, 404, "no such export")
		return
	}
	if strings.HasSuffix(name, ".sha256") {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	} else {
		w.Header().Set("Content-Type", "application/gzip")
		w.Header().Set("Content-Disposition", "attachment; filename=\""+name+"\"")
	}
	// An export is written once and never changes; its name carries the day.
	w.Header().Set("Cache-Control", "public, max-age=86400, immutable")
	http.ServeContent(w, r, name, info.ModTime(), f)
}
