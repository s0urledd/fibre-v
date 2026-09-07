// Package scan is the Fibre Sentinel chain scanner: discovery and recording,
// not probing.
//
// The scanner walks the chain height by height over a single CometBFT RPC
// endpoint. At each height it does two things:
//
//  1. Applies fibre-module parameter changes. EventUpdateFibreParams carries a
//     full Params snapshot; the scanner keeps an ordered ParamHistory keyed by
//     (height, tx index) so must_serve_until for any publication uses the
//     parameters that were in effect at the exact point it settled — including
//     a param update earlier in the same block. The history is seeded once by
//     an ABCI query of Query/Params at the scan's start height; nothing is
//     hard-coded.
//
//  2. Records publications. A publication is a transaction whose sole message is
//     MsgPayForFibre (the same single-message shape consensus enforces, checked
//     via x/fibre/types.TryParseFibreTx). For each one the scanner persists:
//     every PaymentPromise field, the settlement height/time/tx, the params in
//     effect, must_serve_until = creation_timestamp +
//     max(PaymentPromiseTimeout, ShardRetention), and the fibre-assign shard
//     assignment table over the validator set at the promise height.
//
// State lives in <data-dir>/state.json (scan cursor + param history) and
// <data-dir>/publications.jsonl (one record per line, append-only). Publications
// are fsynced before the cursor advances, and every record carries the
// settlement tx hash as a dedupe key, so a crash mid-block is safe to re-scan.
//
// Every RPC call is timeout-bounded. Follow mode polls for new blocks with a
// hard FollowTimeout: if the chain stalls the scanner dumps its recent log ring
// and exits non-zero rather than hanging.
//
// Protocol assignment constants (OriginalRows, TotalRows, MinRowsPerValidator,
// LivenessThreshold) are NOT on chain; they come from fibre-assign's pinned
// ParamsV10BlobV0 and are only valid for blob version 0 on a celestia-app build
// matching assign.PinnedCelestiaAppCommit. Other blob versions are recorded with
// an explicit assignment error instead of a wrong table.
package scan
