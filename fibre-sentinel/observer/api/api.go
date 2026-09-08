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

	"github.com/plsgiveup/fibre/fibre-sentinel/observer/store"
)

// Version is reported in /v1/meta.
const Version = "0.1.0"

// Server serves the API over a store.
type Server struct {
	st      *store.Store
	vantage string
	mux     *http.ServeMux
}

// New builds a Server. vantage is the label rendered on every response.
func New(st *store.Store, vantage string) *Server {
	s := &Server{st: st, vantage: vantage, mux: http.NewServeMux()}
	s.mux.HandleFunc("GET /v1/meta", s.handleMeta)
	s.mux.HandleFunc("GET /v1/network", s.handleNetwork)
	s.mux.HandleFunc("GET /v1/validators", s.handleValidators)
	s.mux.HandleFunc("GET /v1/validators/{addr}", s.handleValidator)
	s.mux.HandleFunc("GET /v1/blobs", s.handleBlobs)
	s.mux.HandleFunc("GET /v1/blobs/{hash}", s.handleBlob)
	s.mux.HandleFunc("GET /v1/probes", s.handleProbes)
	s.mux.HandleFunc("GET /v1/runs", s.handleRuns)
	return s
}

// ServeHTTP implements http.Handler with the headers every response shares.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Cache-Control", "public, max-age=15")
	s.mux.ServeHTTP(w, r)
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
	return w.Start.UTC().Format(time.RFC3339Nano)
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
	APIVersion             string            `json:"api_version"`
	Vantage                string            `json:"vantage"`
	VantageCount           int               `json:"vantage_count"`
	ObservedFromOneVantage bool              `json:"observed_from_one_location"`
	ChainID                string            `json:"chain_id"`
	LastScannedHeight      string            `json:"last_scanned_height"`
	EndpointsHeight        string            `json:"endpoints_height"`
	ProtocolParamsFinger   string            `json:"protocol_params_fingerprint"`
	PinnedCelestiaApp      string            `json:"pinned_celestia_app_commit"`
	Counts                 store.Counts      `json:"counts"`
	Collector              *runStatus        `json:"collector"`
	Prober                 *runStatus        `json:"prober"`
	Meta                   map[string]string `json:"meta"`
	ServerTime             time.Time         `json:"server_time"`
}

type runStatus struct {
	RunID         int64   `json:"run_id"`
	StartedAt     string  `json:"started_at"`
	LastHeartbeat string  `json:"last_heartbeat_at"`
	StoppedAt     *string `json:"stopped_at"`
	Alive         bool    `json:"alive"` // heartbeat within the last 2 minutes
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
		writeErr(w, 500, err.Error())
		return
	}
	meta := map[string]string{}
	rows, err := s.st.DB().QueryContext(ctx, `SELECT key, value FROM meta`)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err == nil {
			meta[k] = v
		}
	}
	rows.Close()
	var vantages int
	_ = s.st.DB().QueryRowContext(ctx, `SELECT COUNT(DISTINCT vantage) FROM probes`).Scan(&vantages)
	if vantages == 0 {
		vantages = 1
	}
	col, _ := s.latestRun(ctx, "collector", now)
	pr, _ := s.latestRun(ctx, "prober", now)
	var pinned string
	_ = s.st.DB().QueryRowContext(ctx, `SELECT pinned_celestia_app FROM publications ORDER BY settlement_height DESC LIMIT 1`).Scan(&pinned)
	writeJSON(w, 200, metaResponse{
		APIVersion: Version, Vantage: s.vantage, VantageCount: vantages, ObservedFromOneVantage: vantages == 1,
		ChainID: meta["chain_id"], LastScannedHeight: meta["last_scanned_height"], EndpointsHeight: meta["endpoints_height"],
		ProtocolParamsFinger: meta["protocol_params_fingerprint"], PinnedCelestiaApp: pinned,
		Counts: counts, Collector: col, Prober: pr, Meta: meta, ServerTime: now.UTC(),
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
		writeErr(w, 500, err.Error())
		return
	}
	defer rows.Close()
	out := []runRow{}
	for rows.Next() {
		var rr runRow
		if err := rows.Scan(&rr.ID, &rr.Component, &rr.Vantage, &rr.Version, &rr.StartedAt, &rr.LastHeartbeat, &rr.StoppedAt, &rr.StopReason); err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		out = append(out, rr)
	}
	writeJSON(w, 200, map[string]any{"window": win, "runs": out})
}

// ---- network ----

type classCounts map[string]int64

type networkResponse struct {
	Window                 Window      `json:"window"`
	Vantage                string      `json:"vantage"`
	ObservedFromOneVantage bool        `json:"observed_from_one_location"`
	RegisteredEndpoints    int64       `json:"registered_endpoints"`
	ValidatorsProbed       int64       `json:"validators_probed"`
	Reachability           Rate        `json:"reachability"` // endpoints whose latest heartbeat or probe reached TLS
	ServeRate              Rate        `json:"serve_rate"`   // HEALTHY / (HEALTHY + FAULT), assigned, in-window + grace
	ProbeCount             int64       `json:"probe_count"`  // all probe rows in window
	Classes                classCounts `json:"classes"`
	Publications           int64       `json:"publications"`
	PublicationBytes       int64       `json:"publication_bytes"`
	Reconstructable        Rate        `json:"reconstructable"` // publications whose latest probed point held >= OriginalRows distinct served rows
	Gaps                   int64       `json:"probe_gaps"`      // NOT_PROBED + PROBE_ERROR rows in window
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

func serveRate(c classCounts) Rate {
	return rate(c["HEALTHY"], c["HEALTHY"]+c["FAULT"])
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
	var vantages int
	_ = db.QueryRowContext(ctx, `SELECT COUNT(DISTINCT vantage) FROM probes`).Scan(&vantages)
	resp.ObservedFromOneVantage = vantages <= 1

	_ = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM endpoints WHERE closed_at IS NULL`).Scan(&resp.RegisteredEndpoints)
	_ = db.QueryRowContext(ctx, `SELECT COUNT(DISTINCT validator_address) FROM probes WHERE started_at >= ?`, win.startArg()).Scan(&resp.ValidatorsProbed)

	classes, total, err := s.classCountsWhere(ctx, `started_at >= ? AND assigned = 1 AND phase IN ('in_window','grace')`, win.startArg())
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	resp.Classes = classes
	resp.ServeRate = serveRate(classes)
	_ = total
	_ = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM probes WHERE started_at >= ?`, win.startArg()).Scan(&resp.ProbeCount)
	_ = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM probes WHERE started_at >= ? AND classification IN ('NOT_PROBED','PROBE_ERROR')`, win.startArg()).Scan(&resp.Gaps)

	reach, err := s.reachabilityNow(ctx)
	if err != nil {
		writeErr(w, 500, err.Error())
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
		writeErr(w, 500, err.Error())
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
	q := `SELECT validator_address, validator_host, started_at, tcp_ok, tls_ok, identity_ok, identity_reason, 'heartbeat' FROM reachability r
	      WHERE started_at = (SELECT MAX(started_at) FROM reachability WHERE validator_address = r.validator_address)
	      UNION ALL
	      SELECT validator_address, validator_host, started_at, tcp_ok, tls_ok, identity_ok, identity_reason, 'probe' FROM probes p
	      WHERE outcome NOT IN ('MISSED','PROBE_ERROR')
	        AND started_at = (SELECT MAX(started_at) FROM probes WHERE validator_address = p.validator_address AND outcome NOT IN ('MISSED','PROBE_ERROR'))`
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
	Address          string      `json:"address"`      // 20-byte consensus address, hex
	ConsAddress      string      `json:"cons_address"` // celestiavalcons1... when known from the registry
	Host             string      `json:"host"`
	EndpointSince    *string     `json:"endpoint_since"`
	VotingPower      int64       `json:"voting_power"` // from the latest assignment seen
	LastSeenAt       *string     `json:"last_seen_at"`
	Reachable        *bool       `json:"reachable"`       // latest heartbeat or probe; null if never probed
	IdentityStatus   string      `json:"identity_status"` // verified | mismatch | no_tls | unreachable | unknown
	IdentityReason   string      `json:"identity_reason,omitempty"`
	ServeRate        Rate        `json:"serve_rate"`
	ProbeCount       int64       `json:"probe_count"`
	Classes          classCounts `json:"classes"`
	AssignedRowsLast int         `json:"assigned_rows_last"`
	ExpectedLoadBand string      `json:"expected_load_band"` // floor | low | mid | high, by assigned rows
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
	return "no_tls"
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
	// validators with assignments (voting power, rows)
	rows, err := db.QueryContext(ctx, `SELECT a.validator_address, a.voting_power, a.row_count FROM assignments a
		JOIN publications p ON p.promise_hash = a.promise_hash
		WHERE p.settlement_height = (SELECT MAX(p2.settlement_height) FROM publications p2 JOIN assignments a2 ON a2.promise_hash = p2.promise_hash WHERE a2.validator_address = a.validator_address)`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var addr string
		var vp int64
		var rc int
		if err := rows.Scan(&addr, &vp, &rc); err != nil {
			rows.Close()
			return nil, err
		}
		v := get(addr)
		v.VotingPower, v.AssignedRowsLast, v.ExpectedLoadBand = vp, rc, loadBand(rc)
	}
	rows.Close()
	// classes per validator in window
	rows, err = db.QueryContext(ctx, `SELECT validator_address, classification, COUNT(*) FROM probes
		WHERE started_at >= ? AND assigned = 1 AND phase IN ('in_window','grace') GROUP BY validator_address, classification`, win.startArg())
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
	out := make([]validatorRow, 0, len(byAddr))
	for addr, v := range byAddr {
		if only != "" && addr != only {
			continue
		}
		v.ServeRate = serveRate(v.Classes)
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
	_, raw, err := bech32.DecodeAndConvert(bech)
	if err != nil {
		return "", err
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
		writeErr(w, 500, err.Error())
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
	type span struct {
		Window  Window      `json:"window"`
		Rate    Rate        `json:"serve_rate"`
		Count   int64       `json:"probe_count"`
		Classes classCounts `json:"classes"`
	}
	var spans []span
	for _, name := range []string{"24h", "7d", "30d"} {
		win := Window{Name: name, Span: windows[name], Start: now.Add(-windows[name]), End: now}
		classes, total, err := s.classCountsWhere(ctx, `validator_address = ? AND started_at >= ? AND assigned = 1 AND phase IN ('in_window','grace')`, addr, win.startArg())
		if err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		spans = append(spans, span{Window: win, Rate: serveRate(classes), Count: total, Classes: classes})
	}
	rows, err := s.validatorRows(ctx, Window{Name: "24h", Span: 24 * time.Hour, Start: now.Add(-24 * time.Hour), End: now}, addr)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if len(rows) == 0 {
		writeErr(w, 404, "validator not seen in the registry or in any probe")
		return
	}
	probes, err := s.probeRows(ctx, `validator_address = ?`, 50, addr)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"validator": rows[0], "windows": spans, "recent_probes": probes, "vantage": s.vantage})
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
	Status        string `json:"status"` // yes | degraded | no | unknown
	Point         string `json:"point"`  // schedule label the verdict is taken at
	PointAt       string `json:"point_at"`
	WindowOver    bool   `json:"window_over"`
	ServedRows    int    `json:"served_distinct_rows"`
	NeededRows    int    `json:"needed_rows"`
	ServedBy      int    `json:"served_by_validators"`
	AssignedTotal int    `json:"assigned_validators"`
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

// reconstructable computes the verdict at the latest schedule point with any
// probe: union the assigned row indices of validators whose probe at that
// point was HEALTHY (or served in post phase), compare to OriginalRows.
func (s *Server) reconstructable(ctx context.Context, hash string) (*reconstruct, error) {
	db := s.st.DB()
	var label, pointAt, msu string
	err := db.QueryRowContext(ctx, `SELECT schedule_label, scheduled_at, must_serve_until FROM probes
		WHERE promise_hash = ? AND outcome NOT IN ('MISSED','PROBE_ERROR') AND phase = 'in_window'
		ORDER BY scheduled_at DESC LIMIT 1`, hash).Scan(&label, &pointAt, &msu)
	if errors.Is(err, sql.ErrNoRows) {
		return &reconstruct{Status: "unknown"}, nil
	}
	if err != nil {
		return nil, err
	}
	windowOver := false
	if t, err := time.Parse(time.RFC3339Nano, msu); err == nil {
		windowOver = time.Now().After(t)
	}
	var needed int
	var assigned int
	if err := db.QueryRowContext(ctx, `SELECT validators_with_rows FROM publications WHERE promise_hash = ?`, hash).Scan(&assigned); err != nil {
		return nil, err
	}
	// OriginalRows is not stored per publication; it is the fingerprinted
	// protocol param. Blob v0: 4096. Read it from the raw record to stay
	// honest if a future blob version changes it.
	var raw string
	if err := db.QueryRowContext(ctx, `SELECT raw_json FROM publications WHERE promise_hash = ?`, hash).Scan(&raw); err != nil {
		return nil, err
	}
	var rec struct {
		Assignment struct {
			ProtocolParams struct {
				OriginalRows int `json:"original_rows"`
			} `json:"protocol_params"`
		} `json:"assignment"`
	}
	_ = json.Unmarshal([]byte(raw), &rec)
	needed = rec.Assignment.ProtocolParams.OriginalRows

	rows, err := db.QueryContext(ctx, `SELECT p.validator_address, a.rows_json FROM probes p
		JOIN assignments a ON a.promise_hash = p.promise_hash AND a.validator_address = p.validator_address
		WHERE p.promise_hash = ? AND p.scheduled_at = ? AND p.outcome = 'SERVED_OK'`, hash, pointAt)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	served := map[int]struct{}{}
	servedBy := 0
	rowsKnown := true
	for rows.Next() {
		var addr string
		var rj sql.NullString
		if err := rows.Scan(&addr, &rj); err != nil {
			return nil, err
		}
		servedBy++
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
	rc := &reconstruct{Point: label, PointAt: pointAt, WindowOver: windowOver, NeededRows: needed, ServedBy: servedBy, AssignedTotal: assigned, ServedRows: len(served)}
	switch {
	case !rowsKnown || needed == 0:
		rc.Status = "unknown"
	case len(served) >= needed && servedBy == assigned:
		rc.Status = "yes"
	case len(served) >= needed:
		rc.Status = "degraded"
	default:
		rc.Status = "no"
	}
	return rc, nil
}

func (s *Server) reconstructableCount(ctx context.Context, win Window) (Rate, error) {
	blobs, err := s.blobRows(ctx, `settlement_time >= ?`, 500, win.startArg())
	if err != nil {
		return Rate{}, err
	}
	var yes, den int64
	for _, b := range blobs {
		if b.Reconstructable == nil || b.Reconstructable.Status == "unknown" {
			continue
		}
		den++
		if b.Reconstructable.Status != "no" {
			yes++
		}
	}
	return rate(yes, den), nil
}

func (s *Server) handleBlobs(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if l, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && l > 0 && l <= 500 {
		limit = l
	}
	var where string
	var args []any
	if ns := r.URL.Query().Get("namespace"); ns != "" {
		where, args = `namespace = ?`, []any{strings.ToLower(ns)}
	}
	if before := r.URL.Query().Get("before_height"); before != "" {
		h, err := strconv.ParseInt(before, 10, 64)
		if err != nil {
			writeErr(w, 400, "before_height must be an integer")
			return
		}
		if where != "" {
			where += " AND "
		}
		where += `settlement_height < ?`
		args = append(args, h)
	}
	blobs, err := s.blobRows(r.Context(), where, limit, args...)
	if err != nil {
		writeErr(w, 500, err.Error())
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
}

func (s *Server) handleBlob(w http.ResponseWriter, r *http.Request) {
	hash := strings.ToLower(r.PathValue("hash"))
	ctx := r.Context()
	blobs, err := s.blobRows(ctx, `promise_hash = ?`, 1, hash)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if len(blobs) == 0 {
		writeErr(w, 404, "no publication with this promise hash")
		return
	}
	rows, err := s.st.DB().QueryContext(ctx, `SELECT validator_address, voting_power, row_count FROM assignments WHERE promise_hash = ? ORDER BY voting_power DESC, validator_address`, hash)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	var assigns []assignmentRow
	for rows.Next() {
		var a assignmentRow
		if err := rows.Scan(&a.ValidatorAddress, &a.VotingPower, &a.RowCount); err != nil {
			rows.Close()
			writeErr(w, 500, err.Error())
			return
		}
		assigns = append(assigns, a)
	}
	rows.Close()
	probes, err := s.probeRows(ctx, `promise_hash = ?`, 1000, hash)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	var params struct {
		ShardRetentionS        int64 `json:"shard_retention_s"`
		PaymentPromiseTimeoutS int64 `json:"payment_promise_timeout_s"`
	}
	_ = s.st.DB().QueryRowContext(ctx, `SELECT shard_retention_s, payment_promise_timeout_s FROM publications WHERE promise_hash = ?`, hash).Scan(&params.ShardRetentionS, &params.PaymentPromiseTimeoutS)
	writeJSON(w, 200, map[string]any{"blob": blobs[0], "params": params, "assignments": assigns, "probes": probes, "vantage": s.vantage})
}

// ---- probes ----

type probeRow struct {
	Vantage          string `json:"vantage"`
	PromiseHash      string `json:"promise_hash"`
	ValidatorAddress string `json:"validator_address"`
	ValidatorHost    string `json:"validator_host"`
	Assigned         bool   `json:"assigned"`
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
}

func (s *Server) probeRows(ctx context.Context, where string, limit int, args ...any) ([]probeRow, error) {
	q := `SELECT vantage, promise_hash, validator_address, validator_host, assigned, assigned_row_count, schedule_label, scheduled_at,
		started_at, phase, outcome, classification, classification_reason, rows_returned, rows_expected, total_duration_ms, tls_ok, identity_ok, raw_error
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
		if err := rows.Scan(&p.Vantage, &p.PromiseHash, &p.ValidatorAddress, &p.ValidatorHost, &assigned, &p.AssignedRowCount, &p.ScheduleLabel,
			&p.ScheduledAt, &p.StartedAt, &p.Phase, &p.Outcome, &p.Classification, &p.Reason, &p.RowsReturned, &p.RowsExpected,
			&p.TotalDurationMS, &tls, &id, &p.RawError); err != nil {
			return nil, err
		}
		p.Assigned, p.TLSOK, p.IdentityOK = assigned == 1, tls == 1, id == 1
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Server) handleProbes(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit := 100
	if l, err := strconv.Atoi(q.Get("limit")); err == nil && l > 0 && l <= 1000 {
		limit = l
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
		conds, args = append(conds, `started_at >= ?`), append(args, t.UTC().Format(time.RFC3339Nano))
	}
	if c := q.Get("class"); c != "" {
		conds, args = append(conds, `classification = ?`), append(args, strings.ToUpper(c))
	}
	rows, err := s.probeRows(r.Context(), strings.Join(conds, " AND "), limit, args...)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"vantage": s.vantage, "probes": rows})
}
