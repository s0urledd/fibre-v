package scan

import (
	"context"
	"errors"
	"fmt"
	"time"

	celfibre "github.com/celestiaorg/celestia-app/v10/fibre"
	fibretypes "github.com/celestiaorg/celestia-app/v10/x/fibre/types"
	cmttypes "github.com/cometbft/cometbft/types"
	assign "github.com/plsgiveup/fibre/fibre-assign"
)

// Config controls a scan run. Zero values fall back to the defaults in Run.
type Config struct {
	RPCURL  string
	DataDir string

	// StartHeight is where a FRESH scan begins (ignored on resume). 0 or
	// negative means "latest height at startup".
	StartHeight int64
	// MaxHeight, if > 0, stops the scan after this height instead of the tip.
	MaxHeight int64

	// Follow keeps polling for new blocks after the tip is reached.
	Follow        bool
	FollowTimeout time.Duration // give up (fatal) if no new block within this
	PollInterval  time.Duration // gap between tip polls

	RPCTimeout time.Duration // per-RPC-call timeout
	Deadline   time.Duration // whole-run wall-clock cap (0 = none)

	IncludeFailed bool // also record MsgPayForFibre txs that failed (code != 0)
	StoreRows     bool // include full per-validator row index lists in records

	CheckpointEvery int // Sync+SaveState every N processed heights (default 20)
}

// Scanner is the chain scanner: discovery + recording only, no probing.
type Scanner struct {
	cfg   Config
	log   *Logger
	chain *Chain
	store *Store

	params      *ParamHistory
	chainID     string
	startHeight int64 // resolved fresh-scan start (persisted across restarts)

	valSetCache map[int64][]assign.Validator
}

// New builds a Scanner. It opens the store and dials the RPC lazily in Run.
func New(cfg Config, log *Logger) (*Scanner, error) {
	if cfg.RPCTimeout <= 0 {
		cfg.RPCTimeout = 15 * time.Second
	}
	if cfg.FollowTimeout <= 0 {
		cfg.FollowTimeout = 120 * time.Second
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 2 * time.Second
	}
	if cfg.CheckpointEvery <= 0 {
		cfg.CheckpointEvery = 20
	}
	ch, err := NewChain(cfg.RPCURL, cfg.RPCTimeout, log)
	if err != nil {
		return nil, err
	}
	st, err := OpenStore(cfg.DataDir)
	if err != nil {
		return nil, err
	}
	return &Scanner{
		cfg:         cfg,
		log:         log,
		chain:       ch,
		store:       st,
		valSetCache: map[int64][]assign.Validator{},
	}, nil
}

// Run executes the scan. It returns nil on a clean finish (tip or MaxHeight
// reached in non-follow mode). Any timeout or unrecoverable error calls
// log.Fatalf, which dumps the log ring and exits the process.
func (s *Scanner) Run(parent context.Context) error {
	ctx := parent
	if s.cfg.Deadline > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(parent, s.cfg.Deadline)
		defer cancel()
	}
	defer s.store.Close()

	chainID, tip, err := s.chain.Status(ctx)
	if err != nil {
		s.log.Fatalf("initial status: %v", err)
	}
	s.chainID = chainID
	s.log.Printf("connected: chain_id=%s tip=%d rpc=%s", chainID, tip, s.cfg.RPCURL)

	next, err := s.resume(ctx, tip)
	if err != nil {
		s.log.Fatalf("resume: %v", err)
	}

	target := tip
	if s.cfg.MaxHeight > 0 && s.cfg.MaxHeight < target {
		target = s.cfg.MaxHeight
	}
	s.log.Printf("scanning from height %d to %d (follow=%v)", next, target, s.cfg.Follow)

	sinceCheckpoint := 0
	totalPubs := 0
	for {
		for h := next; h <= target; h++ {
			if err := ctx.Err(); err != nil {
				if errors.Is(err, context.Canceled) {
					return s.stopClean(next-1, "signal", totalPubs)
				}
				s.log.Fatalf("run deadline hit mid-scan at height %d: %v", h, err)
			}
			n := s.processBlock(ctx, h)
			totalPubs += n
			next = h + 1
			sinceCheckpoint++
			if sinceCheckpoint >= s.cfg.CheckpointEvery || h == target {
				s.checkpoint(next - 1)
				sinceCheckpoint = 0
			}
		}

		if !s.cfg.Follow {
			s.checkpoint(next - 1)
			s.log.Printf("done: scanned through height %d, %d publications recorded this run", next-1, totalPubs)
			return nil
		}

		// follow mode: wait (bounded) for a higher tip.
		newTarget, err := s.waitForHeight(ctx, target+1)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return s.stopClean(next-1, "signal", totalPubs)
			}
			s.log.Fatalf("follow wait: %v", err)
		}
		target = newTarget
		if s.cfg.MaxHeight > 0 && s.cfg.MaxHeight < target {
			target = s.cfg.MaxHeight
			if next > target {
				s.checkpoint(next - 1)
				s.log.Printf("done: reached MaxHeight %d in follow mode", s.cfg.MaxHeight)
				return nil
			}
		}
	}
}

// resume decides the first height to scan and restores param history.
func (s *Scanner) resume(ctx context.Context, tip int64) (int64, error) {
	st, err := s.store.LoadState()
	if err != nil {
		return 0, err
	}
	if st != nil {
		if st.ChainID != "" && st.ChainID != s.chainID {
			return 0, fmt.Errorf("data dir belongs to chain %q but RPC is chain %q", st.ChainID, s.chainID)
		}
		if len(st.ParamHistory) == 0 {
			return 0, errors.New("state.json has no param history")
		}
		s.params = LoadParamHistory(st.ParamHistory)
		s.startHeight = st.StartHeight
		resumeAt := st.LastScannedHeight + 1
		s.log.Printf("resuming: last_scanned=%d, %d param-history entries", st.LastScannedHeight, len(st.ParamHistory))
		return resumeAt, nil
	}

	// fresh scan.
	start := s.cfg.StartHeight
	if start <= 0 {
		start = tip
	}
	if start < 1 {
		start = 1
	}
	seed, err := s.chain.FibreParamsAt(ctx, start)
	if err != nil {
		return 0, fmt.Errorf("seed params at height %d: %w", start, err)
	}
	s.params = NewParamHistory(start, seed)
	s.startHeight = start
	s.log.Printf("fresh scan: start=%d seed params: promise_timeout=%s shard_retention=%s withdrawal_delay=%s",
		start, seed.PaymentPromiseTimeout, seed.ShardRetention, seed.WithdrawalDelay)

	// persist the seed immediately so a crash before the first block still
	// resumes with the right history.
	if err := s.store.SaveState(PersistState{
		ChainID:           s.chainID,
		StartHeight:       start,
		LastScannedHeight: start - 1,
		ParamFingerprint:  assign.ParamsV10BlobV0.Fingerprint(),
		ParamHistory:      s.params.Entries(),
	}); err != nil {
		return 0, err
	}
	return start, nil
}

// stopClean persists progress and returns nil — used when the operator stops
// the scanner (SIGINT/SIGTERM). Resuming later picks up from here.
func (s *Scanner) stopClean(lastScanned int64, reason string, totalPubs int) error {
	s.checkpoint(lastScanned)
	s.log.Printf("stopped (%s): scanned through height %d, %d publications recorded this run", reason, lastScanned, totalPubs)
	return nil
}

func (s *Scanner) checkpoint(lastScanned int64) {
	if err := s.store.Sync(); err != nil {
		s.log.Fatalf("sync publications: %v", err)
	}
	if err := s.store.SaveState(PersistState{
		ChainID:           s.chainID,
		StartHeight:       s.startHeight,
		LastScannedHeight: lastScanned,
		ParamFingerprint:  assign.ParamsV10BlobV0.Fingerprint(),
		ParamHistory:      s.params.Entries(),
	}); err != nil {
		s.log.Fatalf("save state: %v", err)
	}
}

// waitForHeight polls Status until the tip reaches want, or FollowTimeout
// elapses (fatal). Returns the observed tip (>= want).
func (s *Scanner) waitForHeight(ctx context.Context, want int64) (int64, error) {
	deadline := time.Now().Add(s.cfg.FollowTimeout)
	for {
		_, tip, err := s.chain.Status(ctx)
		if err != nil {
			return 0, fmt.Errorf("status while following: %w", err)
		}
		if tip >= want {
			return tip, nil
		}
		if time.Now().After(deadline) {
			return 0, fmt.Errorf("no new block: tip stuck at %d, waited %s for height %d", tip, s.cfg.FollowTimeout, want)
		}
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-time.After(s.cfg.PollInterval):
		}
	}
}

// processBlock scans one height: first apply any fibre-param updates, then
// record every single-message MsgPayForFibre tx. Returns publications recorded.
func (s *Scanner) processBlock(ctx context.Context, h int64) int {
	blk, err := s.chain.Block(ctx, h)
	if err != nil {
		s.log.Fatalf("fetch block %d: %v", h, err)
	}
	res, err := s.chain.BlockResults(ctx, h)
	if err != nil {
		s.log.Fatalf("fetch block_results %d: %v", h, err)
	}
	if len(res.TxCodes) != len(blk.Txs) {
		s.log.Fatalf("block %d: %d txs but %d results", h, len(blk.Txs), len(res.TxCodes))
	}

	// 1) param updates first, so must_serve_until for a PayForFibre later in
	//    the same block sees the new value.
	for i, evs := range res.TxEvents {
		for _, ev := range evs {
			p, isUpdate, perr := parseUpdateFibreParams(ev)
			if !isUpdate {
				continue
			}
			if perr != nil {
				s.log.Fatalf("block %d tx %d: %v", h, i, perr)
			}
			if s.params.AddTxEvent(h, i, p) {
				s.log.Printf("param update @ h=%d tx=%d: promise_timeout=%s shard_retention=%s withdrawal_delay=%s",
					h, i, p.PaymentPromiseTimeout, p.ShardRetention, p.WithdrawalDelay)
			}
		}
	}
	for _, ev := range res.FinalizeEvts {
		p, isUpdate, perr := parseUpdateFibreParams(ev)
		if !isUpdate {
			continue
		}
		if perr != nil {
			s.log.Fatalf("block %d finalize events: %v", h, perr)
		}
		if s.params.AddFinalizeEvent(h, p) {
			s.log.Printf("param update @ h=%d (finalize, effective h=%d): promise_timeout=%s shard_retention=%s",
				h, h+1, p.PaymentPromiseTimeout, p.ShardRetention)
		}
	}

	// 2) MsgPayForFibre txs.
	recorded := 0
	for i, raw := range blk.Txs {
		_, isFibre, perr := fibretypes.TryParseFibreTx(raw)
		if !isFibre {
			continue
		}
		txHash := hexstr(cmttypes.Tx(raw).Hash())
		code := res.TxCodes[i]
		if perr != nil {
			s.log.Printf("h=%d tx=%d (%s): malformed fibre tx, skipped: %v", h, i, txHash[:12], perr)
			continue
		}
		if code != 0 && !s.cfg.IncludeFailed {
			s.log.Printf("h=%d tx=%d (%s): MsgPayForFibre failed code=%d, skipped (use -include-failed to record)", h, i, txHash[:12], code)
			continue
		}
		if s.store.Seen(txHash) {
			continue
		}
		msg, derr := decodePayForFibre(raw)
		if derr != nil || msg == nil {
			s.log.Printf("h=%d tx=%d (%s): could not decode MsgPayForFibre: %v", h, i, txHash[:12], derr)
			continue
		}
		pub, berr := s.buildPublication(ctx, msg, blk, i, txHash, code)
		if berr != nil {
			s.log.Fatalf("h=%d tx=%d: build publication: %v", h, i, berr)
		}
		if err := s.store.AppendPublication(pub); err != nil {
			s.log.Fatalf("h=%d tx=%d: append publication: %v", h, i, err)
		}
		recorded++
		a := pub.Assignment
		s.log.Printf("RECORDED h=%d tx=%d promise=%s commit=%s blob_v%d size=%d valset_h=%d assign[with_rows=%d sigma=%d distinct=%d overlaps=%d] must_serve_until=%s",
			h, i, pub.PromiseHash[:12], pub.Promise.Commitment[:12], pub.Promise.BlobVersion, pub.Promise.BlobSize,
			pub.Promise.Height, a.ValidatorsWithRows, a.Sigma, a.Distinct, a.WrapOverlaps, pub.MustServeUntil.Format(time.RFC3339))
		if a.Error != "" {
			s.log.Printf("  assignment note: %s", a.Error)
		}
	}
	return recorded
}

func (s *Scanner) buildPublication(ctx context.Context, msg *fibretypes.MsgPayForFibre, blk *Block, txIndex int, txHash string, code uint32) (Publication, error) {
	pp := msg.PaymentPromise

	// promise hash (on-chain identity).
	var internal celfibre.PaymentPromise
	if err := internal.FromProto(&pp); err != nil {
		return Publication{}, fmt.Errorf("promise FromProto: %w", err)
	}
	hash, err := internal.Hash()
	if err != nil {
		return Publication{}, fmt.Errorf("promise hash: %w", err)
	}

	nsVersion := uint8(0)
	nsID := ""
	if len(pp.Namespace) > 0 {
		nsVersion = pp.Namespace[0]
		nsID = hexstr(pp.Namespace[1:])
	}

	fields := PromiseFields{
		ChainID:           pp.ChainId,
		Height:            pp.Height,
		Namespace:         hexstr(pp.Namespace),
		NamespaceVersion:  nsVersion,
		NamespaceID:       nsID,
		BlobSize:          pp.BlobSize,
		BlobVersion:       pp.BlobVersion,
		Commitment:        hexstr(pp.Commitment),
		CreationTimestamp: pp.CreationTimestamp.UTC(),
		SignerPublicKey:   hexstr(pp.SignerPublicKey.Key),
		Signature:         hexstr(pp.Signature),
	}

	mustServe, paramsSnap, basis, ok := s.params.MustServeUntil(pp.CreationTimestamp, blk.Height, txIndex)
	if !ok {
		return Publication{}, fmt.Errorf("no param history entry in effect at height %d tx %d", blk.Height, txIndex)
	}

	// assignment table over the validator set at the PROMISE height.
	var table AssignmentTable
	vals, verr := s.validatorSet(ctx, pp.Height)
	if verr != nil {
		table = AssignmentTable{Error: "validator set at height " + fmt.Sprint(pp.Height) + ": " + verr.Error(), ValidatorSetHeight: pp.Height}
	} else {
		var commitment [32]byte
		copy(commitment[:], pp.Commitment)
		table = buildAssignmentTable(commitment, pp.BlobVersion, pp.Height, vals, s.cfg.StoreRows)
	}

	return Publication{
		SchemaVersion:           SchemaVersion,
		PromiseHash:             hexstr(hash),
		SettlementHeight:        blk.Height,
		SettlementTime:          blk.Time.UTC(),
		SettlementTxHash:        txHash,
		SettlementTxIndex:       txIndex,
		SettlementTxCode:        code,
		Signer:                  msg.Signer,
		ValidatorSignatureCount: len(msg.ValidatorSignatures),
		Promise:                 fields,
		ParamsAtPublication:     paramsSnap,
		MustServeUntil:          mustServe,
		MustServeUntilBasis:     basis,
		Assignment:              table,
		RecordedAt:              time.Now().UTC(),
	}, nil
}

// validatorSet fetches (and caches) the consensus validator set at height as
// fibre-assign validators, verifying each address derives from its ed25519 key.
func (s *Scanner) validatorSet(ctx context.Context, height int64) ([]assign.Validator, error) {
	if v, ok := s.valSetCache[height]; ok {
		return v, nil
	}
	members, err := s.chain.ValidatorSet(ctx, height)
	if err != nil {
		return nil, err
	}
	if len(members) == 0 {
		return nil, fmt.Errorf("empty validator set")
	}
	out := make([]assign.Validator, 0, len(members))
	for _, m := range members {
		var a assign.Address
		if len(m.Address) != len(a) {
			return nil, fmt.Errorf("consensus address is %d bytes, want %d", len(m.Address), len(a))
		}
		copy(a[:], m.Address)
		if len(m.PubKey) == 32 {
			if derived, derr := assign.AddressFromEd25519PubKey(m.PubKey); derr == nil && derived != a {
				s.log.Printf("WARNING: validator address %s does not match sha256(pubkey)[:20]=%s at height %d", a, derived, height)
			}
		}
		out = append(out, assign.Validator{Address: a, VotingPower: m.VotingPower})
	}
	s.valSetCache[height] = out
	return out, nil
}
