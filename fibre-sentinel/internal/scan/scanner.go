package scan

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	celfibre "github.com/celestiaorg/celestia-app/v10/fibre"
	fibretypes "github.com/celestiaorg/celestia-app/v10/x/fibre/types"
	cmttypes "github.com/cometbft/cometbft/types"
	assign "github.com/plsgiveup/fibre/fibre-assign"
	"github.com/plsgiveup/fibre/fibre-sentinel/internal/status"
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
	FollowTimeout time.Duration // give up (fatal) if no new block within this; 0 = never, warn instead
	PollInterval  time.Duration // gap between tip polls

	RPCTimeout time.Duration // per-RPC-call timeout
	Deadline   time.Duration // whole-run wall-clock cap (0 = none)

	IncludeFailed bool // also record MsgPayForFibre txs that failed (code != 0)
	StoreRows     bool // include full per-validator row index lists in records

	CheckpointEvery int // Sync+SaveState every N processed heights (default 20)
}

// Scanner is the chain scanner: discovery + recording only, no probing.
type Scanner struct {
	cfg    Config
	log    *Logger
	chain  *Chain
	store  *Store
	status *status.Writer
	gaps   []ScanGap

	params      *ParamHistory
	chainID     string
	startHeight int64 // resolved fresh-scan start (persisted across restarts)

	// fibreInactive is set when the x/fibre module does not answer queries
	// (the chain is on an app version before Fibre). The scanner keeps
	// following blocks so it is already in place at activation, retries
	// the params seed every inactiveRetryEvery heights, and seeds at once if
	// a MsgPayForFibre shows up.
	fibreInactive bool

	// valSets caches the validator set per promise height: the fibre-assign
	// view used for row assignment and the raw members (with consensus keys)
	// used to verify signatures, kept in one entry so the two can never
	// disagree about which heights are cached. valSetOrder is insertion
	// order, which is what eviction walks.
	valSets     map[int64]valSetEntry
	valSetOrder []int64
}

// New builds a Scanner. It opens the store and dials the RPC lazily in Run.
func New(cfg Config, log *Logger) (*Scanner, error) {
	if cfg.RPCTimeout <= 0 {
		cfg.RPCTimeout = 15 * time.Second
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
		status:  status.New(cfg.DataDir, "scanner", "", ""),
		cfg:     cfg,
		log:     log,
		chain:   ch,
		store:   st,
		valSets: map[int64]valSetEntry{},
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
	s.status.Start()
	defer s.status.Stop("exit")

	var chainID string
	var tip int64
	if err := s.retryRPC(ctx, "initial status", func() error {
		var err error
		chainID, tip, err = s.chain.Status(ctx)
		return err
	}); err != nil {
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
			s.status.OK()
			if !s.fibreInactive && h%paramReconcileEvery == 0 {
				s.reconcileParams(ctx, h)
			}
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
		s.params = LoadParamHistory(st.ParamHistory)
		s.fibreInactive = len(st.ParamHistory) == 0
		s.startHeight = st.StartHeight
		s.gaps = st.Gaps
		resumeAt := st.LastScannedHeight + 1
		s.log.Printf("resuming: last_scanned=%d, %d param-history entries%s", st.LastScannedHeight, len(st.ParamHistory),
			map[bool]string{true: " (x/fibre not active yet)", false: ""}[s.fibreInactive])
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
	seed, err := s.seedParamsFor(ctx, start)
	switch {
	case err == nil:
		s.params = NewParamHistory(start, seed)
		s.log.Printf("fresh scan: start=%d seed params: promise_timeout=%s shard_retention=%s withdrawal_delay=%s",
			start, seed.PaymentPromiseTimeout, seed.ShardRetention, seed.WithdrawalDelay)
	case IsModuleInactive(err):
		s.params = LoadParamHistory(nil)
		s.fibreInactive = true
		s.log.Printf("fresh scan: start=%d, x/fibre is not active on this chain yet (%v); following blocks without params and retrying every %d heights",
			start, err, inactiveRetryEvery)
	default:
		return 0, fmt.Errorf("seed params at height %d: %w", start, err)
	}
	s.startHeight = start

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
	s.status.Stop(reason)
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
		Gaps:              s.gaps,
	}); err != nil {
		s.log.Fatalf("save state: %v", err)
	}
	s.status.Progress(lastScanned)
}

// recordGap notes a height the node could not serve and lets the scan move
// on. A MsgPayForFibre in that block is lost to this observer, and the gap is
// published rather than hidden: state.json carries the ranges, the API and
// the dashboard show them. Consecutive heights merge into one range. Returns
// false when err is not that kind of failure.
func (s *Scanner) recordGap(h int64, err error) bool {
	var ue *ErrHeightUnavailable
	if !errors.As(err, &ue) {
		return false
	}
	reason := "height unavailable from the RPC node (pruned, or storage.discard_abci_responses = true)"
	if n := len(s.gaps); n > 0 && s.gaps[n-1].To == h-1 {
		s.gaps[n-1].To = h
		s.gaps[n-1].LastError = ue.Err.Error()
	} else {
		s.gaps = append(s.gaps, ScanGap{From: h, To: h, Reason: reason, LastError: ue.Err.Error(), At: time.Now().UTC()})
	}
	s.log.Printf("WARNING: GAP h=%d not scanned: %v; recorded and moving on (%d gap ranges so far)", h, ue.Err, len(s.gaps))
	s.status.Error(fmt.Sprintf("gap at h=%d: %v", h, ue.Err))
	return true
}

// waitForHeight polls Status until the tip reaches want, or FollowTimeout
// elapses (fatal). With FollowTimeout 0 it waits forever and logs a warning
// every five minutes: a halted chain (upgrade, outage) is the node's problem,
// and the observer should be there when blocks resume. Returns the observed
// tip (>= want). Transient RPC errors are retried, not fatal.
func (s *Scanner) waitForHeight(ctx context.Context, want int64) (int64, error) {
	start := time.Now()
	var deadline time.Time
	if s.cfg.FollowTimeout > 0 {
		deadline = start.Add(s.cfg.FollowTimeout)
	}
	nextWarn := start.Add(5 * time.Minute)
	for {
		var tip int64
		err := s.retryRPC(ctx, "status while following", func() error {
			var err error
			_, tip, err = s.chain.Status(ctx)
			return err
		})
		if err != nil {
			return 0, fmt.Errorf("status while following: %w", err)
		}
		if tip >= want {
			return tip, nil
		}
		s.status.Set("chain_tip", tip)
		if !deadline.IsZero() && time.Now().After(deadline) {
			return 0, fmt.Errorf("no new block: tip stuck at %d, waited %s for height %d", tip, s.cfg.FollowTimeout, want)
		}
		if time.Now().After(nextWarn) {
			s.log.Printf("WARNING: no new block for %s (tip %d, waiting for %d); still following", time.Since(start).Round(time.Second), tip, want)
			nextWarn = time.Now().Add(5 * time.Minute)
		}
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-time.After(s.cfg.PollInterval):
		}
	}
}

// rpcBackoff is the wait before the next attempt at a transient RPC failure:
// 1, 2, 4, 8, 16, 30, 30, ... seconds. Public endpoints hiccup, and at the
// tip CometBFT stores the block before the FinalizeBlock response, so
// block_results for a height /status just reported can be "not found" for a
// moment.
func rpcBackoff(attempt int) time.Duration {
	if attempt >= 5 {
		return 30 * time.Second
	}
	return time.Duration(1<<uint(attempt)) * time.Second
}

// unavailableGrace is how long a height that the node says it does not have
// is retried before the scanner records a gap and moves on. A node that is
// still catching up, or restarting, answers "not available" for a while and
// then has the height; a node that pruned it, or runs with
// storage.discard_abci_responses = true, never will.
var unavailableGrace = 10 * time.Minute

// rpcWarnEvery is how often a still-failing retry is logged as a WARNING,
// so a long outage leaves a trail without a line every few seconds.
const rpcWarnEvery = 5 * time.Minute

// ErrHeightUnavailable wraps an RPC error that means the node cannot serve
// this height at all: pruned, or ABCI responses discarded. It is returned
// only after unavailableGrace of retries.
type ErrHeightUnavailable struct {
	Height int64
	Err    error
}

func (e *ErrHeightUnavailable) Error() string {
	return fmt.Sprintf("height %d unavailable from this node after %s: %v", e.Height, unavailableGrace, e.Err)
}

func (e *ErrHeightUnavailable) Unwrap() error { return e.Err }

// retryRPC runs fn until it succeeds. A transient failure (the node is down,
// a timeout, a tip race) is retried for as long as it takes, with capped
// backoff and a WARNING every few minutes: an RPC outage is the node's
// problem, and the scanner should be there when it comes back rather than
// exit and be restarted in a loop by the supervisor. It gives up at once on
// a context cancellation or on x/fibre being inactive, and after
// unavailableGrace on a height the node says it does not have.
func (s *Scanner) retryRPC(ctx context.Context, what string, fn func() error) error {
	return s.retryRPCAt(ctx, what, 0, fn)
}

func (s *Scanner) retryRPCAt(ctx context.Context, what string, height int64, fn func() error) error {
	start := time.Now()
	nextWarn := start.Add(rpcWarnEvery)
	for attempt := 0; ; attempt++ {
		err := fn()
		if err == nil {
			if attempt > 0 {
				s.log.Printf("%s: recovered after %d attempts, %s", what, attempt+1, time.Since(start).Round(time.Second))
			}
			return nil
		}
		if ctx.Err() != nil {
			return err
		}
		if IsModuleInactive(err) {
			return err
		}
		unavailable := IsHeightUnavailable(err)
		if unavailable && time.Since(start) >= unavailableGrace {
			return &ErrHeightUnavailable{Height: height, Err: err}
		}
		wait := rpcBackoff(attempt)
		if unavailable {
			wait = 30 * time.Second
		}
		if attempt < 3 || time.Now().After(nextWarn) {
			level := ""
			if attempt >= 3 {
				level = "WARNING: "
				nextWarn = time.Now().Add(rpcWarnEvery)
			}
			s.log.Printf("%s%s: %v (attempt %d, failing for %s, retry in %s)", level, what, err, attempt+1, time.Since(start).Round(time.Second), wait)
			s.status.Error(fmt.Sprintf("%s: %v", what, err))
		}
		select {
		case <-ctx.Done():
			return err
		case <-time.After(wait):
		}
	}
}

// processBlock scans one height: first apply any fibre-param updates, then
// record every single-message MsgPayForFibre tx. Returns publications recorded.
// inactiveRetryEvery is how often (in heights) the scanner re-asks for
// x/fibre params while the module is inactive.
const inactiveRetryEvery = 100

// IsModuleInactive reports whether an ABCI query error means the queried
// module does not exist on the chain (app version before Fibre).
func IsModuleInactive(err error) bool {
	if err == nil {
		return false
	}
	var ae *ABCIError
	if errors.As(err, &ae) && ae.Code == 6 && (ae.Codespace == "sdk" || ae.Codespace == "") {
		return true // cosmos-sdk ErrUnknownRequest
	}
	msg := err.Error()
	return strings.Contains(msg, "unknown query path") || strings.Contains(msg, "unknown request")
}

// paramReconcileEvery is how often, in blocks, the scanner re-reads x/fibre
// params from state and compares them with the history it built from events.
// A governance proposal announces its change with an event; an upgrade
// handler or a store migration that calls SetParams emits nothing, and
// without this check such a change would stay invisible for the life of the
// data dir, with every later window computed from the old retention. At six
// second blocks this is a check every half hour.
const paramReconcileEvery = 300

// reconcileParams compares the params in state after block h with the
// history's view of block h and, on a difference, records the live params as
// in force from the next block, which is the earliest point the scanner can
// vouch for. The height at which the silent change really landed is lost;
// the log line says so.
func (s *Scanner) reconcileParams(ctx context.Context, h int64) {
	var live fibretypes.Params
	if err := s.retryRPC(ctx, fmt.Sprintf("params reconcile at height %d", h), func() error {
		var err error
		live, err = s.chain.FibreParamsAt(ctx, h)
		return err
	}); err != nil {
		s.log.Printf("h=%d: params reconcile skipped: %v", h, err)
		return
	}
	cur := s.params.at(h, math.MaxInt)
	if cur != nil && paramsEqual(cur.Params, live) {
		return
	}
	if s.params.add(h+1, -1, "reconcile", live) {
		s.log.Printf("WARNING: h=%d: x/fibre params in state differ from the event history (promise_timeout=%s shard_retention=%s withdrawal_delay=%s in state); a change landed without an event, recorded as in force from height %d",
			h, live.PaymentPromiseTimeout, live.ShardRetention, live.WithdrawalDelay, h+1)
	}
}

// seedParamsFor returns the params in force at the first tx of block h, which
// is the state after block h-1. The history keys a seed as (h, -1), "from the
// first tx of block h"; querying at h itself answers with the state after
// block h, which is wrong for a publication that shares block h with a param
// change landing later in the same block. At h = 1, and when x/fibre only
// became active in block h, h-1 answers "inactive" and the state after block
// h is the best there is. Transient RPC errors are retried; "inactive" is
// returned at once.
func (s *Scanner) seedParamsFor(ctx context.Context, h int64) (fibretypes.Params, error) {
	var seed fibretypes.Params
	fetch := func(at int64) error {
		return s.retryRPC(ctx, fmt.Sprintf("params at height %d", at), func() error {
			var err error
			seed, err = s.chain.FibreParamsAt(ctx, at)
			return err
		})
	}
	if h > 1 {
		err := fetch(h - 1)
		if err == nil || !IsModuleInactive(err) {
			return seed, err
		}
	}
	return seed, fetch(h)
}

// trySeed asks for params at h and, on success, starts the history there.
func (s *Scanner) trySeed(ctx context.Context, h int64) bool {
	seed, err := s.seedParamsFor(ctx, h)
	if err != nil {
		if !IsModuleInactive(err) {
			s.log.Printf("params at h=%d: %v (still treating x/fibre as inactive)", h, err)
		}
		return false
	}
	s.params = NewParamHistory(h, seed)
	s.fibreInactive = false
	s.log.Printf("x/fibre ACTIVE at h=%d: promise_timeout=%s shard_retention=%s withdrawal_delay=%s",
		h, seed.PaymentPromiseTimeout, seed.ShardRetention, seed.WithdrawalDelay)
	return true
}

func (s *Scanner) processBlock(ctx context.Context, h int64) int {
	if s.fibreInactive && (h%inactiveRetryEvery == 0 || h == s.startHeight) {
		s.trySeed(ctx, h)
	}
	var blk *Block
	var res *BlockResults
	if err := s.retryRPCAt(ctx, fmt.Sprintf("fetch block %d", h), h, func() error {
		var err error
		blk, err = s.chain.Block(ctx, h)
		return err
	}); err != nil {
		if s.recordGap(h, err) {
			return 0
		}
		s.log.Fatalf("fetch block %d: %v", h, err)
	}
	if err := s.retryRPCAt(ctx, fmt.Sprintf("fetch block_results %d", h), h, func() error {
		var err error
		res, err = s.chain.BlockResults(ctx, h)
		return err
	}); err != nil {
		if s.recordGap(h, err) {
			return 0
		}
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
		if s.fibreInactive && !s.trySeed(ctx, h) {
			s.log.Fatalf("h=%d tx=%d: MsgPayForFibre seen but x/fibre params cannot be read", h, i)
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

	// 3) Escrow movements: deposits, withdrawals, and what each promise
	//    charged. Recorded whether or not x/fibre params could be read; they
	//    need only the tx bytes.
	recorded += s.recordEconomy(blk, res, h)
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

	mustServe, paramsSnap, basis, ambiguous, ok := s.params.MustServeUntilForPromise(pp.CreationTimestamp, pp.Height, blk.Height, txIndex)
	if !ok {
		return Publication{}, fmt.Errorf("no param history entry in effect at height %d tx %d", blk.Height, txIndex)
	}
	if ambiguous {
		s.log.Printf("publication %s: fibre params changed between promise height %d and settlement %d; earlier must_serve_until recorded", hexstr(pp.Commitment)[:8], pp.Height, blk.Height)
	}

	// assignment table over the validator set at the PROMISE height. A
	// failure to fetch the set is an RPC problem, retried and then fatal,
	// never frozen into the record: a record with an assignment error is
	// skipped by the prober for good, and there is no re-scan path.
	var table AssignmentTable
	var set valSetEntry
	if verr := s.retryRPC(ctx, fmt.Sprintf("validator set at height %d", pp.Height), func() error {
		var err error
		set, err = s.validatorSet(ctx, pp.Height)
		return err
	}); verr != nil {
		return Publication{}, fmt.Errorf("validator set at height %d: %w", pp.Height, verr)
	}
	vals := set.vals

	// Which validators does this promise PROVE stored their shard? The chain
	// does not answer that: its signature check runs in the ante handler and
	// is skipped in ExecModeFinalize, so the observer verifies the signatures
	// itself against the consensus keys at the promise height.
	signBytes, sberr := internal.SignBytes()
	if sberr != nil {
		return Publication{}, fmt.Errorf("promise sign bytes: %w", sberr)
	}
	// The members come back with the set, never from a second lookup: a
	// cache miss here once produced an empty member list, and an empty list
	// verifies nothing, which recorded every validator on the blob as
	// unattested while claiming to be evidence.
	if len(set.members) == 0 {
		return Publication{}, fmt.Errorf("validator set at height %d has no members", pp.Height)
	}
	att := verifyAttestations(signBytes, msg.ValidatorSignatures, set.members)
	if att.Unmatched > 0 || att.OutOfPosition > 0 {
		s.log.Printf("h=%d tx=%d promise %s: %d signature entries, %d verified, %d matched no validator, %d out of position",
			blk.Height, txIndex, hexstr(hash)[:12], att.Entries, att.Verified, att.Unmatched, att.OutOfPosition)
	}

	{
		var commitment [32]byte
		copy(commitment[:], pp.Commitment)
		table = buildAssignmentTable(commitment, pp.BlobVersion, pp.Height, vals, s.cfg.StoreRows, att)
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
		MustServeUntilAmbiguous: ambiguous,
		Assignment:              table,
		RecordedAt:              time.Now().UTC(),
	}, nil
}

// valSetEntry is one cached validator set at one height.
type valSetEntry struct {
	vals    []assign.Validator
	members []ValSetMember
}

// maxValSetHeights bounds the validator-set cache. Publications arrive at
// many distinct promise heights, so an unbounded map would grow for the life
// of the process.
const maxValSetHeights = 256

// validatorSet fetches (and caches) the consensus validator set at height, as
// fibre-assign validators plus the raw members, verifying each address
// derives from its ed25519 key.
//
// Eviction is by insertion order, not by height. A promise height is not the
// scan height: the chain accepts a promise up to PaymentPromiseHeightWindow
// blocks old, so a late-settling publication can ask for a height below
// everything already cached. Dropping the lowest heights evicted exactly that
// entry in the same call that inserted it.
func (s *Scanner) validatorSet(ctx context.Context, height int64) (valSetEntry, error) {
	if e, ok := s.valSets[height]; ok {
		return e, nil
	}
	members, err := s.chain.ValidatorSet(ctx, height)
	if err != nil {
		return valSetEntry{}, err
	}
	if len(members) == 0 {
		return valSetEntry{}, fmt.Errorf("empty validator set")
	}
	out := make([]assign.Validator, 0, len(members))
	for _, m := range members {
		var a assign.Address
		if len(m.Address) != len(a) {
			return valSetEntry{}, fmt.Errorf("consensus address is %d bytes, want %d", len(m.Address), len(a))
		}
		copy(a[:], m.Address)
		if len(m.PubKey) == 32 {
			if derived, derr := assign.AddressFromEd25519PubKey(m.PubKey); derr == nil && derived != a {
				s.log.Printf("WARNING: validator address %s does not match sha256(pubkey)[:20]=%s at height %d", a, derived, height)
			}
		}
		out = append(out, assign.Validator{Address: a, VotingPower: m.VotingPower})
	}
	e := valSetEntry{vals: out, members: members}
	s.valSets[height] = e
	s.valSetOrder = append(s.valSetOrder, height)
	for len(s.valSetOrder) > maxValSetHeights {
		delete(s.valSets, s.valSetOrder[0])
		s.valSetOrder = s.valSetOrder[1:]
	}
	return e, nil
}
