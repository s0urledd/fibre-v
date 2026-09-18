package scan

import (
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"

	abci "github.com/cometbft/cometbft/abci/types"
	"github.com/cosmos/cosmos-sdk/types/bech32"
)

// The x/valaddr event a validator's Fibre host registration emits
// (celestia-app x/valaddr/types/msg.go). The keeper emits it as a legacy
// event on every MsgSetFibreProviderInfo, set or update; there is no
// removal message, and an empty host is refused by the message itself.
const (
	eventSetFibreProviderInfo = "set_fibre_provider_info"
	attrValidatorConsAddress  = "validator_consensus_address"
	attrHost                  = "host"
)

// Host sources on an assignment's host_at_settlement.
const (
	// HostFromEvent: a set_fibre_provider_info event at or before the
	// settlement tx, the newest one for the validator.
	HostFromEvent = "event"
	// HostFromSeed: no event on record; the host is the one the bonded
	// registry showed at the scan's start height (a registration older than
	// the scan).
	HostFromSeed = "seed"
	// HostFromSeedLazy: no event on record and the validator was not in the
	// bonded seed (jailed or unbonding when the scan started; registration
	// outlives bonding), so its registration was read once, the first time
	// it appeared in an assignment, with a single FibreProviderInfo query at
	// that settlement height. Every change since the scan's start would have
	// been an event on record, so the value holds from the seed height on.
	HostFromSeedLazy = "seed_lazy"
	// HostFromSeedCurrent: the state at the wanted height was pruned, so
	// the registry was read at the current height instead; the entry holds
	// from that height on, and earlier settlements without an event are
	// unknown (HostUnknownNoSeed), never this value.
	HostFromSeedCurrent = "seed_current"
	// HostNone: the chain was asked for this validator's registration and
	// answered that there is none (an explicit empty seed entry). A registry
	// state, as NOT_REGISTERED is; never inferred from absence.
	HostNone = "none"
	// HostUnknownGap: a scan gap lies between the validator's newest entry
	// (or the start of the scan) and the settlement, so an event in it may
	// have changed the host. Never the seed value.
	HostUnknownGap = "unknown_gap"
	// HostUnknownNoSeed: nothing on record covers the settlement: the seed
	// could not be read, the validator was never asked about, or the only
	// entry was read at a later height (HostFromSeedCurrent).
	HostUnknownNoSeed = "unknown_no_seed"
)

// HostEntry is one registration on record: a set_fibre_provider_info event
// (effective for promises settled after its tx) or a seed entry at the
// scan's start height.
type HostEntry struct {
	FromHeight  int64  `json:"from_height"`
	FromTxIndex int    `json:"from_tx_index"`
	ConsAddress string `json:"cons_address"` // 20-byte hex, lower case
	Host        string `json:"host"`
	Source      string `json:"source"` // seed | event
}

// HostEvent is the host_history.jsonl record: a HostEntry with the block
// time it was read at, appended by the scanner as it goes.
type HostEvent struct {
	HostEntry
	Time time.Time `json:"time"`
}

// HostHistory is every registration the scanner has on record, per
// validator, oldest first. host_at_settlement for a publication is the
// entry in effect at its (settlement height, tx index), the same rule the
// params history applies.
type HostHistory struct {
	seeded  bool
	seedAt  int64
	byAddr  map[string][]HostEntry
	entries []HostEntry
}

// NewHostHistory starts an empty history; Seed or Load fills it.
func NewHostHistory() *HostHistory {
	return &HostHistory{byAddr: map[string][]HostEntry{}}
}

// Seed records the bonded registry as read at height, as entries with the
// given source (HostFromSeed at the scan's start height, HostFromSeedCurrent
// when the start was pruned and the registry was read at the tip). A
// registration older than the scan is known only from here, or from a lazy
// seed (SeedOne).
func (h *HostHistory) Seed(height int64, providers []FibreProvider, source string) {
	h.seeded, h.seedAt = true, height
	for _, p := range providers {
		addr, err := consHexOf(p.ConsAddressBech32)
		if err != nil {
			continue
		}
		h.add(HostEntry{FromHeight: height, FromTxIndex: -1, ConsAddress: addr, Host: p.Host, Source: source})
	}
}

// SeedOne records one validator's registration read from the chain, as an
// entry from fromHeight on: HostFromSeedLazy at the seed height when it was
// read at (or before) the settlement it was needed for, HostFromSeedCurrent
// at the tip when only the current state could be read. An empty host is
// the chain's explicit "none" and is recorded as such.
func (h *HostHistory) SeedOne(consAddrHex, host, source string, fromHeight int64) HostEntry {
	e := HostEntry{FromHeight: fromHeight, FromTxIndex: -1, ConsAddress: strings.ToLower(consAddrHex), Host: host, Source: source}
	h.add(e)
	return e
}

// Known reports whether anything at all is on record for the validator: an
// event, or a seed entry including an explicit "none".
func (h *HostHistory) Known(consAddrHex string) bool {
	return len(h.byAddr[strings.ToLower(consAddrHex)]) > 0
}

// LoadHostHistory rebuilds a history from persisted entries.
func LoadHostHistory(entries []HostEntry, seeded bool, seedAt int64) *HostHistory {
	h := NewHostHistory()
	h.seeded, h.seedAt = seeded, seedAt
	for _, e := range entries {
		h.add(e)
	}
	return h
}

// Entries returns every entry, oldest first (for persistence).
func (h *HostHistory) Entries() []HostEntry { return append([]HostEntry(nil), h.entries...) }

// Seeded reports whether the registry was read at the scan's start, and
// at which height.
func (h *HostHistory) Seeded() (bool, int64) { return h.seeded, h.seedAt }

func (h *HostHistory) add(e HostEntry) {
	e.ConsAddress = strings.ToLower(e.ConsAddress)
	list := h.byAddr[e.ConsAddress]
	for _, x := range list {
		if x.FromHeight == e.FromHeight && x.FromTxIndex == e.FromTxIndex {
			return // already on record (a replay)
		}
	}
	list = append(list, e)
	sort.SliceStable(list, func(i, j int) bool {
		return lessKey(list[i].FromHeight, list[i].FromTxIndex, list[j].FromHeight, list[j].FromTxIndex)
	})
	h.byAddr[e.ConsAddress] = list
	h.entries = append(h.entries, e)
	sort.SliceStable(h.entries, func(i, j int) bool {
		return lessKey(h.entries[i].FromHeight, h.entries[i].FromTxIndex, h.entries[j].FromHeight, h.entries[j].FromTxIndex)
	})
}

// AddTxEvent records a set_fibre_provider_info event from the tx at txIndex
// in block height. It is in effect for promises settled after that tx, so
// two registrations in one block are ordered by tx index. Returns the
// entry, and false when it was already on record.
func (h *HostHistory) AddTxEvent(height int64, txIndex int, consAddrHex, host string) (HostEntry, bool) {
	e := HostEntry{FromHeight: height, FromTxIndex: txIndex + 1, ConsAddress: strings.ToLower(consAddrHex), Host: host, Source: HostFromEvent}
	before := len(h.entries)
	h.add(e)
	return e, len(h.entries) > before
}

// HostAt returns the host a validator had registered when a promise settled
// by the tx at txIndex in block height, and where that came from (a Host*
// constant). gaps are the scanner's unread ranges: an event inside a gap
// between the newest entry on record and the settlement may have changed
// the host, and then nothing on record is trusted.
func (h *HostHistory) HostAt(consAddrHex string, height int64, txIndex int, gaps []ScanGap) (string, string) {
	var found *HostEntry
	for i := range h.byAddr[strings.ToLower(consAddrHex)] {
		e := &h.byAddr[strings.ToLower(consAddrHex)][i]
		if lessKey(height, txIndex, e.FromHeight, e.FromTxIndex) {
			break
		}
		found = e
	}
	// the interval a missed event would fall in: after the newest entry
	// (or after the seed, or the whole scan when nothing is on record)
	var after int64
	switch {
	case found != nil:
		after = found.FromHeight
	case h.seeded:
		after = h.seedAt
	}
	if gapBetween(gaps, after, height) {
		return "", HostUnknownGap
	}
	if found == nil {
		// nothing covers this settlement: no seed, a seed read later than
		// it (seed_current), or a validator nobody has asked about yet
		return "", HostUnknownNoSeed
	}
	if found.Host == "" {
		return "", HostNone // the chain's explicit answer: not registered
	}
	return found.Host, found.Source
}

// gapBetween reports whether any gap touches (after, upTo]: heights the
// scanner did not read where a registration could have happened.
func gapBetween(gaps []ScanGap, after, upTo int64) bool {
	for _, g := range gaps {
		if g.To > after && g.From <= upTo {
			return true
		}
	}
	return false
}

// parseSetFibreProviderInfo reads a set_fibre_provider_info event. ok is
// false for any other event type; an event of the type with a malformed
// address or no host is an error, since a registration was made and cannot
// be placed.
func parseSetFibreProviderInfo(ev abci.Event) (consAddrHex, host string, ok bool, err error) {
	if ev.Type != eventSetFibreProviderInfo {
		return "", "", false, nil
	}
	var addr string
	hasHost := false
	for _, a := range ev.Attributes {
		switch a.Key {
		case attrValidatorConsAddress:
			addr = a.Value
		case attrHost:
			host, hasHost = a.Value, true
		}
	}
	if addr == "" || !hasHost {
		return "", "", true, fmt.Errorf("set_fibre_provider_info without %s or %s", attrValidatorConsAddress, attrHost)
	}
	hexAddr, err := consHexOf(addr)
	if err != nil {
		return "", "", true, fmt.Errorf("set_fibre_provider_info address %q: %w", addr, err)
	}
	return hexAddr, host, true, nil
}

func consHexOf(bech string) (string, error) {
	_, raw, err := bech32.DecodeAndConvert(bech)
	if err != nil {
		return "", err
	}
	if len(raw) != 20 {
		return "", fmt.Errorf("consensus address %d bytes, want 20", len(raw))
	}
	return strings.ToLower(hex.EncodeToString(raw)), nil
}
