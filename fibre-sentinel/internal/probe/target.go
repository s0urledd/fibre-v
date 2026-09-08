package probe

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/cosmos/cosmos-sdk/types/bech32"
	assign "github.com/plsgiveup/fibre/fibre-assign"
	"github.com/plsgiveup/fibre/fibre-sentinel/internal/scan"
)

// Target is one validator to probe for a given publication.
type Target struct {
	Address      assign.Address // 20-byte consensus address
	AddressHex   string
	PubKey       ed25519.PublicKey // consensus key, for the TLS identity check
	Host         string            // host:port registered in x/valaddr (may be "")
	VotingPower  int64
	Assigned     bool
	AssignedRows []int // recomputed by fibre-assign; empty if unassigned
	RowCount     int
}

// Resolver turns a publication into probe targets: it fetches the validator set
// at the promise height (for consensus keys), the current host registry, and
// recomputes the assignment with fibre-assign. Host registry and validator sets
// are cached briefly so a burst of probes does not hammer the node.
type Resolver struct {
	chain *scan.Chain

	mu            sync.Mutex
	hostCacheAt   time.Time
	hostCacheTTL  time.Duration
	hostByConsHex map[string]string // 20-byte hex -> host:port

	valSetCache map[int64][]scan.ValSetMember
}

// NewResolver builds a Resolver over an RPC chain client.
func NewResolver(chain *scan.Chain, hostCacheTTL time.Duration) *Resolver {
	if hostCacheTTL <= 0 {
		hostCacheTTL = 60 * time.Second
	}
	return &Resolver{
		chain:        chain,
		hostCacheTTL: hostCacheTTL,
		valSetCache:  map[int64][]scan.ValSetMember{},
	}
}

// hostMap returns consHex(20-byte) -> host:port, refreshed at most every TTL.
func (r *Resolver) hostMap(ctx context.Context) (map[string]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.hostByConsHex != nil && time.Since(r.hostCacheAt) < r.hostCacheTTL {
		return r.hostByConsHex, nil
	}
	providers, err := r.chain.BondedFibreProviders(ctx)
	if err != nil {
		if r.hostByConsHex != nil {
			return r.hostByConsHex, nil // serve stale rather than fail a probe
		}
		return nil, err
	}
	m := make(map[string]string, len(providers))
	for _, p := range providers {
		_, raw, err := bech32.DecodeAndConvert(p.ConsAddressBech32)
		if err != nil {
			continue
		}
		m[strings.ToLower(hex.EncodeToString(raw))] = p.Host
	}
	r.hostByConsHex = m
	r.hostCacheAt = time.Now()
	return m, nil
}

func (r *Resolver) validatorSet(ctx context.Context, height int64) ([]scan.ValSetMember, error) {
	r.mu.Lock()
	if v, ok := r.valSetCache[height]; ok {
		r.mu.Unlock()
		return v, nil
	}
	r.mu.Unlock()

	v, err := r.chain.ValidatorSet(ctx, height)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	r.valSetCache[height] = v
	r.mu.Unlock()
	return v, nil
}

// TargetsFor resolves every assigned validator (and, if includeUnassigned, the
// rest of the set) for a publication into probe Targets.
func (r *Resolver) TargetsFor(ctx context.Context, p scan.Publication, includeUnassigned bool) ([]Target, error) {
	members, err := r.validatorSet(ctx, p.Assignment.ValidatorSetHeight)
	if err != nil {
		return nil, fmt.Errorf("validator set at height %d: %w", p.Assignment.ValidatorSetHeight, err)
	}
	hosts, err := r.hostMap(ctx)
	if err != nil {
		return nil, fmt.Errorf("host registry: %w", err)
	}

	// recompute the assignment independently from the record's own inputs.
	pp := assign.ProtocolParams{
		OriginalRows:        p.Assignment.ProtocolParams.OriginalRows,
		TotalRows:           p.Assignment.ProtocolParams.TotalRows,
		MinRowsPerValidator: p.Assignment.ProtocolParams.MinRowsPerValidator,
		LivenessThreshold: assign.Fraction{
			Numerator:   p.Assignment.ProtocolParams.LivenessThresholdNum,
			Denominator: p.Assignment.ProtocolParams.LivenessThresholdDen,
		},
	}
	var commitment [32]byte
	cb, err := hex.DecodeString(p.Promise.Commitment)
	if err != nil || len(cb) != 32 {
		return nil, fmt.Errorf("bad commitment hex %q", p.Promise.Commitment)
	}
	copy(commitment[:], cb)

	vals := make([]assign.Validator, 0, len(members))
	pubByAddr := map[assign.Address]ed25519.PublicKey{}
	powerByAddr := map[assign.Address]int64{}
	for _, m := range members {
		var a assign.Address
		if len(m.Address) != len(a) {
			return nil, fmt.Errorf("consensus address %d bytes, want 20", len(m.Address))
		}
		copy(a[:], m.Address)
		vals = append(vals, assign.Validator{Address: a, VotingPower: m.VotingPower})
		if len(m.PubKey) == ed25519.PublicKeySize {
			pubByAddr[a] = ed25519.PublicKey(append([]byte(nil), m.PubKey...))
		}
		powerByAddr[a] = m.VotingPower
	}

	sm, err := assign.Assign(commitment, vals, pp)
	if err != nil {
		return nil, fmt.Errorf("recompute assignment: %w", err)
	}

	// cross-check recomputed counts against the record.
	recByAddr := map[string]int{}
	for a, rows := range sm {
		recByAddr[a.String()] = len(rows)
	}
	for _, v := range p.Assignment.Validators {
		if recByAddr[v.Address] != v.RowCount {
			return nil, fmt.Errorf("assignment mismatch for %s: record says %d rows, recompute says %d — record and chain disagree",
				v.Address, v.RowCount, recByAddr[v.Address])
		}
	}

	var out []Target
	for _, v := range vals {
		rows := sm[v.Address]
		assigned := len(rows) > 0
		if !assigned && !includeUnassigned {
			continue
		}
		addrHex := v.Address.String()
		out = append(out, Target{
			Address:      v.Address,
			AddressHex:   addrHex,
			PubKey:       pubByAddr[v.Address],
			Host:         hosts[addrHex],
			VotingPower:  powerByAddr[v.Address],
			Assigned:     assigned,
			AssignedRows: append([]int(nil), rows...),
			RowCount:     len(rows),
		})
	}
	return out, nil
}
