package scan

import (
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
	timeout := e.Params.PaymentPromiseTimeout
	retention := e.Params.ShardRetention
	window := timeout
	if retention > window {
		window = retention
	}
	basis := "creation_timestamp + max(payment_promise_timeout=" + timeout.String() +
		", shard_retention=" + retention.String() + ") = creation + " + window.String()
	return creation.Add(window).UTC(), e.ParamsJSON, basis, true
}

func paramsEqual(a, b fibretypes.Params) bool {
	return a.WithdrawalDelay == b.WithdrawalDelay &&
		a.PaymentPromiseTimeout == b.PaymentPromiseTimeout &&
		a.PaymentPromiseHeightWindow == b.PaymentPromiseHeightWindow &&
		a.ShardRetention == b.ShardRetention &&
		a.FullStakeStorageBudget == b.FullStakeStorageBudget
}
