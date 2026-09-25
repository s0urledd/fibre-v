package rollup_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/plsgiveup/fibre/fibre-sentinel/internal/probe"
	"github.com/plsgiveup/fibre/fibre-sentinel/observer/rollup"
)

// The heartbeat counts a day is rolled into are this observer's own, as the
// live reachability figures are: another vantage's copied rows for the same
// day, all failing here, leave probe_daily exactly as they found it.
func TestRollupCountsOnlyTheOwnVantagesHeartbeats(t *testing.T) {
	st := openStore(t)
	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	day := time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC)
	for i := range 4 {
		at := day.Add(time.Duration(i) * 5 * time.Minute)
		for _, v := range []string{"ut-1", "de-1"} {
			up := v == "ut-1"
			m := probe.Measurement{Vantage: v, ValidatorAddress: "aa", ValidatorHost: "h:7980", ScheduledAt: at, StartedAt: at, Outcome: probe.OutcomeReachable}
			m.DNS.OK, m.TCP.OK, m.TLS.OK, m.Identity.OK = true, up, up, up
			if !up {
				m.Outcome = probe.OutcomeTCPRefused
			}
			raw, _ := json.Marshal(m)
			if _, err := st.InsertReachability(m, raw); err != nil {
				t.Fatal(err)
			}
		}
	}
	cfg := rollup.Config{RollupAfter: 14 * 24 * time.Hour, Vantage: "ut-1"}
	if _, err := rollup.Run(context.Background(), st, now, cfg); err != nil {
		t.Fatal(err)
	}
	var beats, up, ident int64
	if err := st.DB().QueryRow(`SELECT beats, beats_up, identity_up FROM probe_daily WHERE day = ? AND validator_address = 'aa'`,
		day.Format("2006-01-02")).Scan(&beats, &up, &ident); err != nil {
		t.Fatal(err)
	}
	if beats != 4 || up != 4 || ident != 4 {
		t.Fatalf("rolled beats %d up %d identity %d, want 4/4/4: another vantage's rows were counted", beats, up, ident)
	}
}
