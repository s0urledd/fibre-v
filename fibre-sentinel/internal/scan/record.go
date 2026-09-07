package scan

import (
	"encoding/hex"
	"time"

	fibretypes "github.com/celestiaorg/celestia-app/v10/x/fibre/types"
	assign "github.com/plsgiveup/fibre/fibre-assign"
)

// SchemaVersion is bumped whenever the Publication JSON shape changes.
const SchemaVersion = 1

// Publication is the persisted record for one on-chain MsgPayForFibre. One JSON
// object per line in publications.jsonl.
type Publication struct {
	SchemaVersion int    `json:"schema_version"`
	PromiseHash   string `json:"promise_hash"` // hex sha256, the on-chain promise identity

	// settlement: the block that carried the MsgPayForFibre tx.
	SettlementHeight  int64     `json:"settlement_height"`
	SettlementTime    time.Time `json:"settlement_time"`
	SettlementTxHash  string    `json:"settlement_tx_hash"`
	SettlementTxIndex int       `json:"settlement_tx_index"`
	SettlementTxCode  uint32    `json:"settlement_tx_code"` // 0 = success

	Signer                  string `json:"signer"` // MsgPayForFibre.signer (bech32)
	ValidatorSignatureCount int    `json:"validator_signature_count"`

	Promise PromiseFields `json:"promise"`

	ParamsAtPublication ParamsSnapshot `json:"params_at_publication"`
	MustServeUntil      time.Time      `json:"must_serve_until"`
	MustServeUntilBasis string         `json:"must_serve_until_basis"`

	Assignment AssignmentTable `json:"assignment"`

	RecordedAt time.Time `json:"recorded_at"` // scanner wall clock, informational
}

// PromiseFields is every field of the decoded PaymentPromise.
type PromiseFields struct {
	ChainID           string    `json:"chain_id"`
	Height            int64     `json:"height"` // validator-set height
	Namespace         string    `json:"namespace"`
	NamespaceVersion  uint8     `json:"namespace_version"`
	NamespaceID       string    `json:"namespace_id"`
	BlobSize          uint32    `json:"blob_size"`
	BlobVersion       uint32    `json:"blob_version"`
	Commitment        string    `json:"commitment"`
	CreationTimestamp time.Time `json:"creation_timestamp"`
	SignerPublicKey   string    `json:"signer_public_key"` // hex, secp256k1 compressed (33B)
	Signature         string    `json:"signature"`         // hex, 64B
}

// ParamsSnapshot is a fibre-params value plus the point it became effective.
type ParamsSnapshot struct {
	WithdrawalDelay            string `json:"withdrawal_delay"`
	PaymentPromiseTimeout      string `json:"payment_promise_timeout"`
	PaymentPromiseHeightWindow uint64 `json:"payment_promise_height_window"`
	ShardRetention             string `json:"shard_retention"`
	FullStakeStorageBudget     uint64 `json:"full_stake_storage_budget"`

	WithdrawalDelaySeconds       int64 `json:"withdrawal_delay_seconds"`
	PaymentPromiseTimeoutSeconds int64 `json:"payment_promise_timeout_seconds"`
	ShardRetentionSeconds        int64 `json:"shard_retention_seconds"`

	EffectiveFromHeight  int64  `json:"effective_from_height"`
	EffectiveFromTxIndex int    `json:"effective_from_tx_index"`
	Source               string `json:"source"` // seed | event | finalize
}

func snapshotParams(p fibretypes.Params, fromHeight int64, fromTxIndex int, source string) ParamsSnapshot {
	return ParamsSnapshot{
		WithdrawalDelay:              p.WithdrawalDelay.String(),
		PaymentPromiseTimeout:        p.PaymentPromiseTimeout.String(),
		PaymentPromiseHeightWindow:   p.PaymentPromiseHeightWindow,
		ShardRetention:               p.ShardRetention.String(),
		FullStakeStorageBudget:       p.FullStakeStorageBudget,
		WithdrawalDelaySeconds:       int64(p.WithdrawalDelay / time.Second),
		PaymentPromiseTimeoutSeconds: int64(p.PaymentPromiseTimeout / time.Second),
		ShardRetentionSeconds:        int64(p.ShardRetention / time.Second),
		EffectiveFromHeight:          fromHeight,
		EffectiveFromTxIndex:         fromTxIndex,
		Source:                       source,
	}
}

func (s ParamsSnapshot) toParams() fibretypes.Params {
	parse := func(str string, secs int64) time.Duration {
		if d, err := time.ParseDuration(str); err == nil {
			return d
		}
		return time.Duration(secs) * time.Second
	}
	return fibretypes.Params{
		WithdrawalDelay:            parse(s.WithdrawalDelay, s.WithdrawalDelaySeconds),
		PaymentPromiseTimeout:      parse(s.PaymentPromiseTimeout, s.PaymentPromiseTimeoutSeconds),
		PaymentPromiseHeightWindow: s.PaymentPromiseHeightWindow,
		ShardRetention:             parse(s.ShardRetention, s.ShardRetentionSeconds),
		FullStakeStorageBudget:     s.FullStakeStorageBudget,
	}
}

// AssignmentTable is the fibre-assign shard assignment for the promise's
// commitment over the validator set at the promise height.
type AssignmentTable struct {
	// Error is set (and the rest left zero) when the assignment could not be
	// computed — e.g. an unknown blob version with no pinned ProtocolParams.
	Error string `json:"error,omitempty"`

	ProtocolParams     ProtocolParamsSnapshot `json:"protocol_params"`
	ValidatorSetHeight int64                  `json:"validator_set_height"`
	TotalVotingPower   int64                  `json:"total_voting_power"`
	Validators         []ValidatorAssignment  `json:"validators"`

	Sigma              int `json:"sigma"`         // total assigned rows across validators
	Distinct           int `json:"distinct"`      // distinct row indices
	WrapOverlaps       int `json:"wrap_overlaps"` // indices held by >1 validator
	ValidatorsWithRows int `json:"validators_with_rows"`
}

// ProtocolParamsSnapshot records the (off-chain, pinned) assignment constants
// used, so a record is self-describing.
type ProtocolParamsSnapshot struct {
	OriginalRows         int    `json:"original_rows"`
	TotalRows            int    `json:"total_rows"`
	MinRowsPerValidator  int    `json:"min_rows_per_validator"`
	LivenessThresholdNum uint64 `json:"liveness_threshold_numerator"`
	LivenessThresholdDen uint64 `json:"liveness_threshold_denominator"`
	Fingerprint          string `json:"fingerprint"`
	PinnedCelestiaApp    string `json:"pinned_celestia_app_commit"`
}

// ValidatorAssignment is one validator's row assignment.
type ValidatorAssignment struct {
	Address     string `json:"address"` // hex, 20-byte consensus address
	VotingPower int64  `json:"voting_power"`
	RowCount    int    `json:"row_count"`
	Rows        []int  `json:"rows,omitempty"` // omitted when -rows=false
}

func protoParamsSnapshot(p assign.ProtocolParams) ProtocolParamsSnapshot {
	return ProtocolParamsSnapshot{
		OriginalRows:         p.OriginalRows,
		TotalRows:            p.TotalRows,
		MinRowsPerValidator:  p.MinRowsPerValidator,
		LivenessThresholdNum: p.LivenessThreshold.Numerator,
		LivenessThresholdDen: p.LivenessThreshold.Denominator,
		Fingerprint:          p.Fingerprint(),
		PinnedCelestiaApp:    assign.PinnedCelestiaAppCommit,
	}
}

func hexstr(b []byte) string { return hex.EncodeToString(b) }
