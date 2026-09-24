package main

// The hosting lookup pass: which network and country each registered Fibre
// host resolves into, from local database files (see observer/hosting and
// deploy/hosting-db.sh). It lives in its own file so main.go carries only
// the two lines that wire it in.
//
// Configuration, first match wins:
//
//	-hosting-asn-db / -hosting-country-db             flags
//	HOSTING_ASN_DB / HOSTING_COUNTRY_DB               environment (the systemd
//	                                                  units load <network>.env)
//	<data-dir>/hosting/ip2asn-combined.tsv.gz         the files hosting-db.sh
//	<data-dir>/hosting/dbip-country-lite.csv.gz       writes, when present
//
// No ASN file means the feature is off and nothing is looked up. The pass
// never makes a network call: addresses come from the heartbeat's own
// reachability rows.

import (
	"context"
	"flag"
	"time"

	"github.com/plsgiveup/fibre/fibre-sentinel/observer/hosting"
	"github.com/plsgiveup/fibre/fibre-sentinel/observer/store"
)

// Package-level so the flag block in main() stays untouched; flag.Parse
// there picks them up like any other.
var (
	hostingASNFlag     = flag.String("hosting-asn-db", "", "iptoasn.com ip2asn TSV (gz ok) for the hosting lookup (default $HOSTING_ASN_DB, else <data-dir>/hosting/"+hosting.DefaultASNFile+" if present; none = feature off)")
	hostingCountryFlag = flag.String("hosting-country-db", "", "DB-IP IP-to-Country Lite CSV (gz ok), optional (default $HOSTING_COUNTRY_DB, else <data-dir>/hosting/"+hosting.DefaultCountryFile+" if present)")
)

// newHostingPass prepares the table and returns the function the collector
// calls on every endpoint-poll pass. Errors are logged, never fatal: this
// is an annotation on the record, not part of it.
func newHostingPass(st *store.Store, dataDir string, logf func(string, ...any)) func(ctx context.Context, now time.Time) {
	if err := hosting.EnsureSchema(st.DB()); err != nil {
		logf("hosting: create table: %v; lookups disabled", err)
		return func(context.Context, time.Time) {}
	}
	cfg := hosting.ResolveConfig(*hostingASNFlag, *hostingCountryFlag, dataDir)
	if cfg.ASNPath == "" {
		logf("hosting: no ASN database configured; provider/country lookup off (deploy/hosting-db.sh enables it)")
	} else {
		logf("hosting: asn db %s, country db %q", cfg.ASNPath, cfg.CountryPath)
	}
	r := &hosting.Refresher{DB: st.DB(), Cfg: cfg, Logf: logf}
	return func(ctx context.Context, now time.Time) {
		// The databases usually arrive after the collector has started
		// (deploy/hosting-db.sh runs once the new binaries are up), and the
		// paths were resolved at start. While no ASN file was found, look
		// again every pass: a stat per minute, and no restart needed.
		if r.Cfg.ASNPath == "" {
			if c := hosting.ResolveConfig(*hostingASNFlag, *hostingCountryFlag, dataDir); c.ASNPath != "" {
				logf("hosting: asn db %s appeared, country db %q; lookup on", c.ASNPath, c.CountryPath)
				r.Cfg = c
			}
		}
		res, err := r.Run(ctx, now)
		switch {
		case err != nil:
			logf("hosting: %v", err)
		case res.Enabled && !res.Skipped:
			logf("hosting: %d open endpoint(s), %d resolved, %d with an origin AS", res.Hosts, res.Resolved, res.WithASN)
		}
	}
}
