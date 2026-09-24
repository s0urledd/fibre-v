package scan

import (
	"context"
	"fmt"
	"time"

	fibretypes "github.com/celestiaorg/celestia-app/v10/x/fibre/types"
	cmtbytes "github.com/cometbft/cometbft/libs/bytes"
	rpcclient "github.com/cometbft/cometbft/rpc/client"
)

// The withdrawal queue, read from state rather than rebuilt from events.
//
// Why events are not enough. x/fibre announces a withdrawal twice: when it
// is requested (EventWithdrawFromEscrowRequest, with requested_at and
// available_at) and when the begin-blocker pays it (EventWithdrawFromEscrowExecuted,
// with the signer and the amount only). Between the two, a settlement or a
// timeout that finds the account's available balance short takes the
// shortfall out of the queued withdrawals, oldest first, shrinking one or
// deleting it outright, and announces nothing
// (x/fibre/keeper/msg_server.go:313-320 → keeper.go:487-512 at the pinned
// commit). A queue replayed from the two events therefore overstates what
// is pending and cannot say which request a payout paid.
//
// The Withdrawals query answers from the same store the begin-blocker walks,
// so it is the queue as the chain holds it. Upstream ignores the request's
// pagination and returns every withdrawal of the signer in one answer
// (x/fibre/keeper/grpc_query.go:46-60); this reader refuses an answer that
// claims a further page rather than silently keep the first.

// PendingWithdrawal is one queued withdrawal as x/fibre stores it.
// RequestedAt is the block time of the MsgRequestWithdrawal; together with
// the signer it is the store key, and the module refuses a second request
// in the same block (msg_server.go:91-95), so (signer, RequestedAt) names
// one withdrawal for its whole life. AvailableAt is RequestedAt plus the
// withdrawal_delay in force when it was requested (msg_server.go:97): a
// later params change does not move it.
type PendingWithdrawal struct {
	Signer      string
	Denom       string
	AmountUtia  uint64
	RequestedAt time.Time
	AvailableAt time.Time
}

// WithdrawalsPath is the ABCI query path of x/fibre's Withdrawals query.
const WithdrawalsPath = "/celestia.fibre.v1.Query/Withdrawals"

// Withdrawals reads every queued withdrawal of signer at height (<= 0 means
// latest) and returns them with the height the node answered at. An
// account with nothing queued (or no escrow at all) is an empty list, not
// an error: upstream answers both with an empty list.
func (c *Chain) Withdrawals(parent context.Context, signer string, height int64) ([]PendingWithdrawal, int64, error) {
	ctx, cancel := c.ctx(parent)
	defer cancel()

	req := fibretypes.QueryWithdrawalsRequest{Signer: signer}
	data, err := req.Marshal()
	if err != nil {
		return nil, 0, fmt.Errorf("marshal withdrawals request: %w", err)
	}
	res, err := c.rpc.ABCIQueryWithOptions(ctx, WithdrawalsPath, cmtbytes.HexBytes(data), rpcclient.ABCIQueryOptions{Height: height})
	if err != nil {
		return nil, 0, fmt.Errorf("abci query withdrawals %s h=%d: %w", signer, height, err)
	}
	if res.Response.Code != 0 {
		return nil, 0, c.queryError(parent, WithdrawalsPath, height, res.Response)
	}
	out, err := decodeWithdrawals(signer, res.Response.Value)
	if err != nil {
		return nil, 0, err
	}
	return out, res.Response.Height, nil
}

// decodeWithdrawals turns a QueryWithdrawalsResponse into PendingWithdrawals.
// Split out so the decoding (and the refusal of a paginated answer, and of
// a row for someone else's account) is testable without a node.
func decodeWithdrawals(signer string, value []byte) ([]PendingWithdrawal, error) {
	var resp fibretypes.QueryWithdrawalsResponse
	if err := resp.Unmarshal(value); err != nil {
		return nil, fmt.Errorf("unmarshal withdrawals response: %w", err)
	}
	if resp.Pagination != nil && len(resp.Pagination.NextKey) > 0 {
		// The pinned module never pages this answer. One that does has
		// changed under us, and keeping page one would close every queued
		// withdrawal on page two as if it had left the queue.
		return nil, fmt.Errorf("withdrawals %s: the node returned a paginated answer (next_key set); this reader handles the unpaginated upstream answer only", signer)
	}
	out := make([]PendingWithdrawal, 0, len(resp.Withdrawals))
	for i, w := range resp.Withdrawals {
		if w.Signer != "" && w.Signer != signer {
			return nil, fmt.Errorf("withdrawals %s: entry %d belongs to %s", signer, i, w.Signer)
		}
		denom, amt := coinAmount(w.Amount)
		out = append(out, PendingWithdrawal{
			Signer:      signer,
			Denom:       denom,
			AmountUtia:  amt,
			RequestedAt: w.RequestedTimestamp.UTC(),
			AvailableAt: w.AvailableTimestamp.UTC(),
		})
	}
	return out, nil
}

// HeaderTime is the block time of height, from its header alone. The
// collector needs it twice: to place a withdrawal-queue read on the chain's
// clock (whether a withdrawal could have been paid by then is a question of
// block time, not of this machine's clock), and to date the height a params
// value took effect at.
func (c *Chain) HeaderTime(parent context.Context, height int64) (time.Time, error) {
	t, err := c.headerTime(parent, height)
	if err != nil {
		return time.Time{}, err
	}
	return t.UTC(), nil
}
