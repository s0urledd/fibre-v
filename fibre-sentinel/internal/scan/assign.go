package scan

import (
	"fmt"
	"sort"

	assign "github.com/plsgiveup/fibre/fibre-assign"
)

// protocolParamsForBlobVersion returns the pinned, off-chain assignment
// constants for a blob version. Only v0 has a pin; anything else must be
// recorded with an explicit error rather than a wrong assignment.
func protocolParamsForBlobVersion(v uint32) (assign.ProtocolParams, error) {
	switch v {
	case 0:
		return assign.ParamsV10BlobV0, nil
	default:
		return assign.ProtocolParams{}, fmt.Errorf("no pinned ProtocolParams for blob version %d (only v0)", v)
	}
}

// buildAssignmentTable computes the fibre-assign shard assignment for commitment
// over vals (the validator set at the promise height) and summarises it.
func buildAssignmentTable(commitment [32]byte, blobVersion uint32, valSetHeight int64, vals []assign.Validator, storeRows bool) AssignmentTable {
	pp, err := protocolParamsForBlobVersion(blobVersion)
	if err != nil {
		return AssignmentTable{Error: err.Error(), ValidatorSetHeight: valSetHeight}
	}
	if err := pp.Validate(); err != nil {
		return AssignmentTable{Error: "protocol params invalid: " + err.Error(), ProtocolParams: protoParamsSnapshot(pp), ValidatorSetHeight: valSetHeight}
	}

	sm, err := assign.Assign(commitment, vals, pp)
	if err != nil {
		return AssignmentTable{Error: "assign: " + err.Error(), ProtocolParams: protoParamsSnapshot(pp), ValidatorSetHeight: valSetHeight}
	}

	var total int64
	for _, v := range vals {
		total += v.VotingPower
	}

	// deterministic output order: voting power desc, then address asc — the
	// canonical validator order the reference walks.
	ordered := append([]assign.Validator(nil), vals...)
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].VotingPower != ordered[j].VotingPower {
			return ordered[i].VotingPower > ordered[j].VotingPower
		}
		return ordered[i].Address.String() < ordered[j].Address.String()
	})

	seen := map[int]int{}
	sigma := 0
	withRows := 0
	out := make([]ValidatorAssignment, 0, len(ordered))
	for _, v := range ordered {
		rows := sm[v.Address]
		if len(rows) > 0 {
			withRows++
		}
		sigma += len(rows)
		for _, r := range rows {
			seen[r]++
		}
		va := ValidatorAssignment{
			Address:     v.Address.String(),
			VotingPower: v.VotingPower,
			RowCount:    len(rows),
		}
		if storeRows {
			va.Rows = append([]int(nil), rows...)
		}
		out = append(out, va)
	}
	overlaps := 0
	for _, c := range seen {
		if c > 1 {
			overlaps++
		}
	}

	return AssignmentTable{
		ProtocolParams:     protoParamsSnapshot(pp),
		ValidatorSetHeight: valSetHeight,
		TotalVotingPower:   total,
		Validators:         out,
		Sigma:              sigma,
		Distinct:           len(seen),
		WrapOverlaps:       overlaps,
		ValidatorsWithRows: withRows,
	}
}
