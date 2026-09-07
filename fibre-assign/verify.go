package assign

import (
	"errors"
	"fmt"
)

// Verify checks that rowIndices is exactly the set of rows assigned to the
// validator at addr: the validator is in the map, the count matches, every
// index is one of its assigned rows, and there are no duplicates.
//
// It reproduces celestia-app fibre/validator.ShardMap.Verify. A Fibre Sentinel
// uses it on the row indices a probed validator actually returned from
// DownloadShard, to classify "served its assigned rows" vs "under-delivered"
// vs "served rows it was not assigned".
func (m ShardMap) Verify(addr Address, rowIndices []uint32) error {
	rows, ok := m[addr]
	if !ok {
		return errors.New("validator not in shard map")
	}
	if len(rowIndices) != len(rows) {
		return fmt.Errorf("expected %d rows, got %d", len(rows), len(rowIndices))
	}
	assigned := make(map[uint32]bool, len(rows))
	for _, idx := range rows {
		assigned[uint32(idx)] = false
	}
	for _, idx := range rowIndices {
		seen, ok := assigned[idx]
		if !ok {
			return fmt.Errorf("row %d not assigned to validator", idx)
		}
		if seen {
			return fmt.Errorf("duplicate row %d", idx)
		}
		assigned[idx] = true
	}
	return nil
}

// IsAssigned reports whether the validator at addr was assigned any rows for
// this commitment. A false here means a NotFound from that validator's Fibre
// endpoint is expected, not a fault.
func (m ShardMap) IsAssigned(addr Address) bool {
	return len(m[addr]) > 0
}
