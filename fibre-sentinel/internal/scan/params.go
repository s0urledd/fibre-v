package scan

import (
	"strconv"
	"time"

	fibretypes "github.com/celestiaorg/celestia-app/v10/x/fibre/types"
)

// ParamEntry is one fibre-params value together with the exact point in chain
// history it becomes effective. Ordering is lexicographic over
// (FromHeight, FromTxIndex).
//
//   - The seed entry (read once via an ABCI query at the scan's start height)
//     uses FromTxIndex = -1, so it is in effect for everything at or after that
//     height.
//   - An EventUpdateFibreParams emitted by the tx at index i in block H takes
//     effect for promises settled strictly after it, so its entry is
//     {FromHeight: H, FromTxIndex: i + 1}.
//   - The same event in FinalizeBlock (a gov-executed change, end of block) is
//     effective from the next block: {FromHeight: H + 1, FromTxIndex: -1}.
type ParamEntry struct {
	FromHeight  int64             `json:"from_height"`
	FromTxIndex int               `json:"from_tx_index"`
	Source      string            `json:"source"` // seed | event | finalize
	Params      fibretypes.Params `json:"-"`
	ParamsJSON  ParamsSnapshot    `json:"params"`
}

func lessKey(h1 int64, i1 int, h2 int64, i2 int) bool {
	if h1 != h2 {
		return h1 < h2
	}
	return i1 < i2
}

// ParamHistory is the ordered list of fibre-params values the scanner has seen,
// oldest first. must_serve_until for a publication is computed from the entry
// in effect at that publication's (settlement height, tx index).
type ParamHistory struct {
	entries []ParamEntry
}

// NewParamHistory seeds the history with params read at startHeight.
func NewParamHistory(startHeight int64, seed fibretypes.Params) *ParamHistory {
	return &ParamHistory{entries: []ParamEntry{{
		FromHeight:  startHeight,
		FromTxIndex: -1,
		Source:      "seed",
		Params:      seed,
		ParamsJSON:  snapshotParams(seed, startHeight, -1, "seed"),
	}}}
}

// Load rebuilds a history from persisted entries (each already carrying its
// ParamsSnapshot; Params is re-derived from the snapshot for computation).
func LoadParamHistory(entries []ParamEntry) *ParamHistory {
	h := &ParamHistory{entries: make([]ParamEntry, 0, len(entries))}
	for _, e := range entries {
		e.Params = e.ParamsJSON.toParams()
		h.entries = append(h.entries, e)
	}
	h.sort()
	return h
}

// Entries returns the entries oldest first (for persistence).
func (h *ParamHistory) Entries() []ParamEntry { return append([]ParamEntry(nil), h.entries...) }

func (h *ParamHistory) sort() {
	// insertion sort: the list is tiny and almost always already ordered.
	for i := 1; i < len(h.entries); i++ {
		for j := i; j > 0 && lessKey(h.entries[j].FromHeight, h.entries[j].FromTxIndex, h.entries[j-1].FromHeight, h.entries[j-1].FromTxIndex); j-- {
			h.entries[j], h.entries[j-1] = h.entries[j-1], h.entries[j]
		}
	}
}

// Add records a params change observed at (height, txIndex). txIndex is the
// index of the update tx for a tx event, or a sentinel past the last tx for a
// FinalizeBlock event (the caller passes the already-adjusted key). Returns
// true if it changed the effective value (a no-op repeat is dropped).
func (h *ParamHistory) add(fromHeight int64, fromTxIndex int, source string, p fibretypes.Params) bool {
	cur := h.at(fromHeight, fromTxIndex)
	if cur != nil && paramsEqual(cur.Params, p) {
		return false
	}
	h.entries = append(h.entries, ParamEntry{
		FromHeight:  fromHeight,
		FromTxIndex: fromTxIndex,
		Source:      source,
		Params:      p,
		ParamsJSON:  snapshotParams(p, fromHeight, fromTxIndex, source),
	})
	h.sort()
	return true
}

// AddTxEvent records an EventUpdateFibreParams from the tx at txIndex in block
// height. It becomes effective for promises settled after that tx.
func (h *ParamHistory) AddTxEvent(height int64, txIndex int, p fibretypes.Params) bool {
	return h.add(height, txIndex+1, "event", p)
}

// AddFinalizeEvent records an EventUpdateFibreParams from FinalizeBlock at
// block height; effective from the next block.
func (h *ParamHistory) AddFinalizeEvent(height int64, p fibretypes.Params) bool {
	return h.add(height+1, -1, "finalize", p)
}

// at returns the entry in effect at (height, txIndex), or nil if the history
// starts after that point.
func (h *ParamHistory) at(height int64, txIndex int) *ParamEntry {
	var found *ParamEntry
	for i := range h.entries {
		e := &h.entries[i]
		if !lessKey(height, txIndex, e.FromHeight, e.FromTxIndex) {
			// e.key <= (height, txIndex)
			found = e
		} else {
			break
		}
	}
	return found
}

// EffectiveAt returns the params snapshot in effect for a publication settled
// by the tx at txIndex in block height, and whether one was found.
func (h *ParamHistory) EffectiveAt(height int64, txIndex int) (ParamsSnapshot, bool) {
	e := h.at(height, txIndex)
	if e == nil {
		return ParamsSnapshot{}, false
	}
	return e.ParamsJSON, true
}

// MustServeUntil = creation + max(PaymentPromiseTimeout, ShardRetention), using
// the params in effect at (settlementHeight, settlementTxIndex).
func (h *ParamHistory) MustServeUntil(creation time.Time, settlementHeight int64, settlementTxIndex int) (time.Time, ParamsSnapshot, string, bool) {
	e := h.at(settlementHeight, settlementTxIndex)
	if e == nil {
		return time.Time{}, ParamsSnapshot{}, "", false
	}
	msu, basis := windowFrom(e.Params, creation)
	return msu, e.ParamsJSON, basis, true
}

// MustServeUntilForPromise is MustServeUntil with the upload-time ambiguity
// resolved conservatively. The Fibre server computes its prune time from the
// params it reads when the shard is UPLOADED (fibre/server_upload.go: the
// ValidatePaymentPromise query at latest state), which happens somewhere
// between the promise height and the settlement tx. The observer only sees
// the chain, so it takes the EARLIEST must_serve_until any params value in
// force anywhere in that interval could produce. Then it never calls a fault
// past a window the server may legitimately have used, whichever instant
// inside the interval the upload actually landed on.
//
// Every history entry in the interval is considered, not only its two ends.
// Comparing the endpoints alone missed a change that reverted before
// settlement: the two ends agreed, the record was marked unambiguous, and the
// deadline recorded was later than the one the server would have used if the
// upload fell in the middle. Every probe between the two deadlines would then
// have been published as a retention failure.
//
// If no history entry covers the promise height (the scan started after it),
// only the settlement params are used.
func (h *ParamHistory) MustServeUntilForPromise(creation time.Time, promiseHeight, settlementHeight int64, settlementTxIndex int) (msu time.Time, snap ParamsSnapshot, basis string, ambiguous bool, ok bool) {
	at := h.at(settlementHeight, settlementTxIndex)
	if at == nil {
		return time.Time{}, ParamsSnapshot{}, "", false, false
	}
	msu, basis = windowFrom(at.Params, creation)
	snap = at.ParamsJSON

	// Params in force at the end of the block before the promise height,
	// at the end of the promise-height block, plus every change that
	// landed between there and the settlement tx. The block before is a
	// candidate because the chain accepts a promise whose height is one
	// above the validating node's latest (x/fibre keeper: "allow up to 1
	// block ahead"), so a server whose node had not yet committed the
	// promise block validated the upload against the state before it.
	candidates := []*ParamEntry{}
	if before := h.at(promiseHeight-1, int(^uint(0)>>1)); before != nil {
		candidates = append(candidates, before)
	}
	if atPromise := h.at(promiseHeight, int(^uint(0)>>1)); atPromise != nil {
		candidates = append(candidates, atPromise)
	}
	for i := range h.entries {
		e := &h.entries[i]
		if lessKey(e.FromHeight, e.FromTxIndex, promiseHeight, int(^uint(0)>>1)) {
			continue // before the interval
		}
		if lessKey(settlementHeight, settlementTxIndex, e.FromHeight, e.FromTxIndex) {
			break // after the interval; entries are ordered
		}
		candidates = append(candidates, e)
	}

	differs := false
	for _, c := range candidates {
		if c == at {
			continue
		}
		if !paramsEqual(c.Params, at.Params) {
			differs = true
		}
		early, earlyBasis := windowFrom(c.Params, creation)
		if early.Before(msu) {
			msu, basis, snap = early, earlyBasis, c.ParamsJSON
		}
	}
	if !differs {
		return msu, snap, basis, false, true
	}
	basis += "; AMBIGUOUS: fibre params changed between promise height " + itoa64(promiseHeight) +
		" and settlement height " + itoa64(settlementHeight) +
		"; the server uses the params at upload time, which lies in that interval; the earliest bound over every params value in force there is recorded"
	return msu, snap, basis, true, true
}

func windowFrom(p fibretypes.Params, creation time.Time) (time.Time, string) {
	timeout := p.PaymentPromiseTimeout
	retention := p.ShardRetention
	window := timeout
	if retention > window {
		window = retention
	}
	basis := "creation_timestamp + max(payment_promise_timeout=" + timeout.String() +
		", shard_retention=" + retention.String() + ") = creation + " + window.String()
	return creation.Add(window).UTC(), basis
}

func itoa64(v int64) string { return strconv.FormatInt(v, 10) }

func paramsEqual(a, b fibretypes.Params) bool {
	return a.WithdrawalDelay == b.WithdrawalDelay &&
		a.PaymentPromiseTimeout == b.PaymentPromiseTimeout &&
		a.PaymentPromiseHeightWindow == b.PaymentPromiseHeightWindow &&
		a.ShardRetention == b.ShardRetention &&
		a.FullStakeStorageBudget == b.FullStakeStorageBudget
}
