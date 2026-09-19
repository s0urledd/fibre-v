package probe

import (
	"math/bits"
	"strings"
	"testing"

	"github.com/celestiaorg/celestia-app/v10/pkg/rsema1d/field"
	fibretypes "github.com/celestiaorg/celestia-app/v10/x/fibre/types"
)

// The verifier rejects a row whose index is outside [0, K+N) and a proof
// whose depth is not bits.Len(K+N)-1. Both bounds come from this observer's
// idea of the code parameters, not from the response, and downloadAndVerify
// reads any error from the verifier as INVALID_ROWS — a fault in every
// phase. So a single wrong K or N here would have recorded every honest
// validator on the network as faulting at the same minute. parseShard takes
// those two checks first and names them for what they are: a disagreement
// about the parameters, which is this observer's gap.
func TestParseShard_ParameterDisagreementIsNotAFault(t *testing.T) {
	const k, total = 16, 64
	depth := bits.Len(uint(total)) - 1
	rlcs := make([]byte, k*field.GF128Size)
	row := make([]byte, field.LeopardChunkSize)
	proof := func(d int) [][]byte {
		p := make([][]byte, d)
		for i := range p {
			p[i] = make([]byte, 32)
		}
		return p
	}
	shard := func(index uint32, d int) *fibretypes.BlobShard {
		return &fibretypes.BlobShard{Rows: []*fibretypes.BlobRow{{Index: index, Data: row, Proof: proof(d)}}, Rlcs: rlcs}
	}

	if _, _, err := parseShard(shard(0, depth), k, total); err != nil {
		t.Fatalf("a well-formed shard was rejected: %v", err)
	}
	for _, c := range []struct {
		name string
		sh   *fibretypes.BlobShard
		want string
	}{
		{"index at the boundary", shard(total, depth), "outside this observer's code parameters"},
		{"index far out", shard(total+5, depth), "outside this observer's code parameters"},
		{"proof too shallow", shard(0, depth-1), "this observer expects"},
		{"proof too deep", shard(0, depth+1), "this observer expects"},
	} {
		_, _, err := parseShard(c.sh, k, total)
		if err == nil {
			t.Errorf("%s: accepted", c.name)
			continue
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want a message containing %q", c.name, err, c.want)
		}
	}
}

// The wording matters as much as the routing: the reason lands on the row
// and is read by an operator asking why their validator is a gap. It must
// say the observer is unsure, never that the validator was wrong.
func TestParseShard_ReasonBlamesTheObserverNotTheValidator(t *testing.T) {
	const k, total = 16, 64
	sh := &fibretypes.BlobShard{
		Rows: []*fibretypes.BlobRow{{Index: total + 1, Data: make([]byte, field.LeopardChunkSize), Proof: make([][]byte, bits.Len(uint(total))-1)}},
		Rlcs: make([]byte, k*field.GF128Size),
	}
	_, _, err := parseShard(sh, k, total)
	if err == nil {
		t.Fatal("accepted")
	}
	msg := err.Error()
	if !strings.Contains(msg, "this observer") {
		t.Errorf("reason %q does not say whose limit it is", msg)
	}
	for _, bad := range []string{"invalid", "fault", "misbehav", "wrong rows"} {
		if strings.Contains(strings.ToLower(msg), bad) {
			t.Errorf("reason %q reads as an accusation (%q)", msg, bad)
		}
	}
}

// Nothing bounded how many rows could come back. Every returned index was
// written to the measurement before any verification ran, so a server could
// answer with the same legal index millions of times and that list would
// reach measurements.jsonl, the probes table, /v1/probes and the daily export
// at whatever size it chose. No shard of a blob can carry more rows than the
// code has, so that is the bound — and it is a shape error, this observer's
// gap, never a statement about the validator.
func TestParseShard_MoreRowsThanTheBlobHasIsRefused(t *testing.T) {
	const k, total = 16, 64
	depth := bits.Len(uint(total)) - 1
	proof := make([][]byte, depth)
	for i := range proof {
		proof[i] = make([]byte, 32)
	}
	build := func(n int) *fibretypes.BlobShard {
		rows := make([]*fibretypes.BlobRow, n)
		for i := range rows {
			rows[i] = &fibretypes.BlobRow{Index: uint32(i % total), Data: make([]byte, field.LeopardChunkSize), Proof: proof}
		}
		return &fibretypes.BlobShard{Rows: rows, Rlcs: make([]byte, k*field.GF128Size)}
	}
	if _, _, err := parseShard(build(total), k, total); err != nil {
		t.Fatalf("a shard with exactly the blob's rows was refused: %v", err)
	}
	for _, n := range []int{total + 1, total * 100} {
		_, _, err := parseShard(build(n), k, total)
		if err == nil {
			t.Errorf("%d rows accepted for a blob of %d", n, total)
			continue
		}
		if !strings.Contains(err.Error(), "more than") {
			t.Errorf("%d rows: %v, want a message naming the count", n, err)
		}
		for _, bad := range []string{"invalid", "fault", "misbehav"} {
			if strings.Contains(strings.ToLower(err.Error()), bad) {
				t.Errorf("%d rows: reason %q reads as an accusation", n, err)
			}
		}
	}
}
