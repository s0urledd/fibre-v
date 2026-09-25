package policy

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Budget state across restarts.
//
// The caps in BeforeProbe are sliding windows (a minute of requests, an
// hour and a day of bytes) plus the minimum spacing and the transport
// backoff, and all of it used to live in memory only. A prober restart
// started every validator on a fresh budget: a restart loop — a crash
// dump, a supervisor restarting it every few seconds, an operator
// redeploying through an afternoon — could spend the whole hourly byte cap
// once per restart, send a minute's requests again in the same minute, and
// forget that a validator's transport had failed three times in a row, on
// exactly the endpoints R4 promises to be gentlest with. The caps
// constrain this vantage over wall-clock windows, not per process.
//
// So the minimal state behind those checks is written to the data dir and
// read back on start: per validator, the trailing 24 hours of accounted and
// reserved probes in one-minute buckets, the last admission (spacing), the
// consecutive transport failures and when the last one was (backoff), and
// the rows last seen. The global windows are the sum of the validators'
// events, as they are in memory, so they need nothing of their own.
//
// Conservative in every direction a reload can be wrong:
//   - a bucket is stamped at the NEWEST probe in it, so a restored event is
//     never older than the one it stands for and leaves each window no
//     earlier than it would have;
//   - a reservation still pending at the save is written as a probe made
//     (the request may well have gone out);
//   - probes made after the last save and before the process died are not
//     on record. Saves are at most saveEvery apart, and the next admission
//     of any validator after a restart waits out the spacing from the save
//     time, which covers the one request per validator the spacing let
//     through in that interval;
//   - a missing, unreadable, corrupt or unknown-version file says nothing
//     about what was spent, so it is read as "the budget may be spent":
//     nothing is admitted for UnknownCooldown after the start, instead of
//     starting fresh. The file is written again at the first change, so a
//     cool-down is paid once per lost file, not once per restart, and a
//     process that exits without a change writes nothing over the file
//     it could not read;
//   - a save that fails closes admission until one succeeds: a prober that
//     cannot record what it spent cannot promise a restart will respect it.

// BudgetStateFile is the file name sentinel-probe gives the state in its
// data directory.
const BudgetStateFile = "probe-budget.json"

// budgetStateVersion is the file's schema version. A file with any other
// version is read as unknown state (cool-down), never half-understood.
const budgetStateVersion = 1

// saveEvery bounds how often the state is written: every change marks it
// dirty, and one save covers everything that changed in the interval.
var saveEvery = time.Second

type budgetState struct {
	Version    int                          `json:"version"`
	SavedAt    time.Time                    `json:"saved_at"`
	Validators map[string]validatorSnapshot `json:"validators"`
}

type validatorSnapshot struct {
	LastRequest  time.Time      `json:"last_request"`
	ConsecFail   int            `json:"consec_fail,omitempty"`
	LastFailAt   time.Time      `json:"last_fail_at"`
	RowsLastSeen int            `json:"rows_last_seen,omitempty"`
	Minutes      []minuteBucket `json:"minutes,omitempty"`
}

// minuteBucket is every probe of one validator in one wall-clock minute:
// how many requests and how many bytes, stamped at the newest of them.
type minuteBucket struct {
	At    time.Time `json:"at"`
	N     int       `json:"n"`
	Bytes int64     `json:"bytes"`
}

// snapshot builds the file's contents. The caller holds p.mu.
func (p *Policy) snapshot(now time.Time) budgetState {
	since := now.Add(-24 * time.Hour)
	st := budgetState{Version: budgetStateVersion, SavedAt: now.UTC(), Validators: map[string]validatorSnapshot{}}
	for addr, vs := range p.validators {
		evs := make([]event, 0, len(vs.events))
		for _, e := range vs.events {
			if !e.at.Before(since) {
				evs = append(evs, e)
			}
		}
		for _, r := range p.pending {
			if r.addr == addr && !r.at.Before(since) {
				evs = append(evs, event{at: r.at, bytes: r.bytes})
			}
		}
		snap := validatorSnapshot{LastRequest: vs.lastRequest.UTC(), ConsecFail: vs.consecFail, LastFailAt: vs.lastFailAt.UTC(),
			RowsLastSeen: vs.rowsLastSeen, Minutes: bucketByMinute(evs)}
		if len(snap.Minutes) == 0 && snap.ConsecFail == 0 && snap.LastRequest.Before(since) {
			continue // nothing that still constrains anything
		}
		st.Validators[addr] = snap
	}
	return st
}

func bucketByMinute(evs []event) []minuteBucket {
	byMin := map[int64]*minuteBucket{}
	for _, e := range evs {
		k := e.at.Unix() / 60
		b, ok := byMin[k]
		if !ok {
			b = &minuteBucket{At: e.at.UTC()}
			byMin[k] = b
		}
		b.N++
		b.Bytes += e.bytes
		if e.at.After(b.At) {
			b.At = e.at.UTC()
		}
	}
	out := make([]minuteBucket, 0, len(byMin))
	for _, b := range byMin {
		out = append(out, *b)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	return out
}

// restore loads the state file into a new Policy, or starts the cool-down
// when there is nothing trustworthy to load. Called from New, before the
// policy is shared.
func (p *Policy) restore(now time.Time) {
	st, err := readBudgetState(p.stateFile)
	if err != nil {
		p.cooldownUntil = now.Add(p.cfg.State.UnknownCooldown)
		p.logStartup("probe budget: %v; the budget already spent is unknown, so nothing is admitted until %s (cool-down %s) rather than starting on a fresh budget",
			err, p.cooldownUntil.UTC().Format(time.RFC3339), p.cfg.State.UnknownCooldown)
		return
	}
	since := now.Add(-24 * time.Hour)
	n := 0
	for addr, snap := range st.Validators {
		vs := p.state(addr)
		vs.lastRequest, vs.consecFail, vs.lastFailAt, vs.rowsLastSeen = snap.LastRequest, snap.ConsecFail, snap.LastFailAt, snap.RowsLastSeen
		for _, b := range snap.Minutes {
			if b.At.Before(since) || b.N <= 0 {
				continue
			}
			// N requests at the bucket's newest time; the bytes ride on the
			// first, the sums are the same either way.
			for i := 0; i < b.N; i++ {
				e := event{at: b.At}
				if i == 0 {
					e.bytes = b.Bytes
				}
				vs.events = append(vs.events, e)
				p.global = append(p.global, e)
				n++
			}
		}
		sort.SliceStable(vs.events, func(i, j int) bool { return vs.events[i].at.Before(vs.events[j].at) })
	}
	sort.SliceStable(p.global, func(i, j int) bool { return p.global[i].at.Before(p.global[j].at) })
	p.restoredAt = st.SavedAt
	p.logStartup("probe budget: restored %d validators, %d probes in the last 24h, saved at %s", len(st.Validators), n, st.SavedAt.UTC().Format(time.RFC3339))
}

func readBudgetState(path string) (budgetState, error) {
	var st budgetState
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return st, fmt.Errorf("no budget state at %s", path)
	}
	if err != nil {
		return st, fmt.Errorf("budget state %s unreadable: %w", path, err)
	}
	if err := json.Unmarshal(b, &st); err != nil {
		return st, fmt.Errorf("budget state %s corrupt: %w", path, err)
	}
	if st.Version != budgetStateVersion {
		return st, fmt.Errorf("budget state %s has version %d, this build reads %d", path, st.Version, budgetStateVersion)
	}
	if st.SavedAt.IsZero() {
		return st, fmt.Errorf("budget state %s has no saved_at", path)
	}
	return st, nil
}

// logStartup logs through the policy's logger if one is set, and keeps the
// line for SetLogger otherwise: New runs before the caller can install one.
func (p *Policy) logStartup(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	if p.logf != nil {
		p.logf("%s", msg)
		return
	}
	p.startupLog = append(p.startupLog, msg)
}

// markDirty records a change and schedules a save. The caller holds p.mu.
func (p *Policy) markDirty() {
	if p.stateFile == "" {
		return
	}
	p.dirty = true
	if p.saveTimer != nil {
		return
	}
	p.saveTimer = time.AfterFunc(saveEvery, func() { _ = p.Flush() })
}

// Flush writes the budget state now, if anything changed since the last
// save or the restore. sentinel-probe calls it on the way out; the timer
// calls it after changes. A failure is logged and closes admission until a
// save succeeds (see stateGuard).
//
// Nothing changed, nothing is written. A process that restored the file
// and exits before probing has nothing to add to it, and one that could not
// read it (the cool-down) must leave it alone: a valid empty state written
// over it lets the next start skip the cool-down on a budget that may be
// spent, and replaces a file that was unreadable for a moment, or written
// by a newer build, with nothing.
func (p *Policy) Flush() error {
	if p.stateFile == "" {
		return nil
	}
	p.saveMu.Lock()
	defer p.saveMu.Unlock()
	p.mu.Lock()
	if p.saveTimer != nil {
		p.saveTimer.Stop()
		p.saveTimer = nil
	}
	if !p.dirty {
		p.mu.Unlock()
		return nil
	}
	p.dirty = false
	b, err := json.Marshal(p.snapshot(time.Now()))
	p.mu.Unlock()
	if err == nil {
		err = writeAtomic(p.stateFile, b)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if err != nil {
		if !p.saveFailing && p.logf != nil {
			p.logf("WARNING: probe budget state could not be saved (%v): admission is closed until it can be, so a restart cannot start on a budget already spent", err)
		}
		p.saveFailing = true
		p.dirty = true
		return err
	}
	if p.saveFailing && p.logf != nil {
		p.logf("probe budget state saved again; admission reopened")
	}
	p.saveFailing = false
	return nil
}

// stateGuard is the denial, if any, the persisted-state rules impose before
// the caps are looked at. The caller holds p.mu.
func (p *Policy) stateGuard(now time.Time) string {
	if now.Before(p.cooldownUntil) {
		return "budget:state_unknown_cooldown"
	}
	if p.saveFailing {
		p.markDirty() // try again; the next save that works reopens
		return "budget:state_unsaved"
	}
	return ""
}

// writeAtomic replaces path with data: a temp file in the same directory,
// synced, renamed over it, and the directory synced, so a crash leaves
// either the old file or the new one, never half of one.
func writeAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, filepath.Base(path)+".tmp*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		d.Close()
	}
	return nil
}
