package scan

import (
	"encoding/hex"
	"time"

	fibretypes "github.com/celestiaorg/celestia-app/v10/x/fibre/types"
	assign "github.com/plsgiveup/fibre/fibre-assign"
)

// SchemaVersion is bumped whenever the Publication JSON shape changes.
//
// 1: initial shape.
// 2: verified attestation. AssignmentTable gained the signature-verification
//
//	counters and ValidatorAssignment gained Attested.
const SchemaVersion = 2

// AttestationSchemaVersion is the first record version whose attestation
// fields mean anything. Below it the record was written by a scanner that did
// not verify signatures, so Attested is false for every validator because the
// field did not exist — not because the validator failed to attest. Consumers
// must treat that as unknown.
const AttestationSchemaVersion = 2

// HasAttestation reports whether this record's attestation fields carry
// evidence. When false, Attested and the signature counters are absent, not
// negative.
func (p Publication) HasAttestation() bool { return p.SchemaVersion >= AttestationSchemaVersion }

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
	// MustServeUntilAmbiguous is set when the fibre params changed between the
	// promise height and the settlement tx; the server's own window depends on
	// the upload instant, so the earlier candidate is recorded. Additive field.
	MustServeUntilAmbiguous bool `json:"must_serve_until_ambiguous,omitempty"`

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
	// HostsAtSettlementKnown is whether the registry at the settlement
	// height could be read, so that an empty Host on a validator means "no
	// host registered" and not "not looked up".
	HostsAtSettlementKnown bool `json:"hosts_at_settlement_known,omitempty"`
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

	// AttestedWithRows is how many of ValidatorsWithRows carry a verified
	// signature over this promise, i.e. how many are PROVEN to have stored
	// their shard. Only these can be held to the retention obligation; see
	// Attestation in attest.go for why the rest are unproven rather than
	// absent.
	AttestedWithRows int `json:"attested_with_rows"`
	// SignatureEntries, SignaturesVerified, SignaturesUnmatched and
	// SignaturesOutOfPosition describe the verification itself, so a reader
	// can tell a promise whose signatures all checked out from one whose
	// trailing entries were never verified by the chain.
	SignatureEntries        int `json:"signature_entries"`
	SignaturesVerified      int `json:"signatures_verified"`
	SignaturesUnmatched     int `json:"signatures_unmatched,omitempty"`
	SignaturesOutOfPosition int `json:"signatures_out_of_position,omitempty"`
	// AttestedVotingPower is the voting power that attested, out of
	// TotalVotingPower.
	AttestedVotingPower int64 `json:"attested_voting_power"`
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
	// Attested is true when this validator's signature over the promise
	// verified against its consensus key, which proves it stored the shard
	// (the server writes before it signs). False means unproven, not absent:
	// the publisher stops collecting signatures at the safety threshold.
	Attested bool `json:"attested"`
	// Host is the Fibre host this validator had registered (x/valaddr) at
	// the settlement height: where the upload went and where the shard was
	// stored. Empty with AssignmentTable.HostsAtSettlementKnown true means
	// no host was registered; empty with it false means the lookup failed.
	Host string `json:"host_at_settlement,omitempty"`
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
