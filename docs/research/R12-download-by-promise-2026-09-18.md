# R12: DownloadShard cannot ask for a promise's shard

*2026-09-18. An observation from building an independent Fibre observer, written for the Celestia forum thread on Fibre (CIP-51). Draft; the observer's first concrete protocol feedback.*

## What the store does

`celestia-app/fibre/store.go` (v10.1.0-mocha): `Put` stores every (commitment, promise) shard side by side, "independently without deduplication", under `/shard/<commitment>/<promiseHash>`. `Get(commitment)` iterates those keys from the lowest and returns the first shard file it can read. `DownloadShard` takes a `BlobID` (version + commitment) and calls `Get`.

So when two or more promises exist over the same blob, which promise's shard a download returns is decided by **promise-hash order**, not by time, not by the caller.

## Why it matters beyond one observer

CIP-51's retention obligation is per promise: a validator that signed promise P is obliged to serve P's shard until P's `must_serve_until`. But no client can ask for P's shard. The reference client asks for the commitment and receives whichever shard sorts first; a client of promise P may receive the rows assigned under promise Q (a different row set for the same validator when the validator set or its power changed between the two), and cannot tell the server it wanted P.

Two consequences:

1. **Per-promise retention is not checkable inside the protocol.** A validator that dropped P's shard but still holds Q's answers every download of the commitment with Q's rows, correctly, and no request can expose P's absence. This is not a measurement limit of an outside observer; the reference client has the same view.
2. **A shard whose promise never settled can answer.** An upload that reached the server but whose `MsgPayForFibre` never landed stays on disk until its prune and never appears on chain. If its hash sorts first it is what every client gets. Nobody can name it, because it exists in no block.

For an observer this means genuine rows that match no settled promise's assignment cannot be called a fault: the validator may be answering honestly with a shard the chain never saw. This observer files them as their own class, held out of the rate (`UNMATCHED_GENUINE`), and reserves the fault verdict for "no shard of this blob at all" and "bytes that do not verify".

## Proposal

Add an optional `promise_hash` field to `DownloadShardRequest`. When set, the server returns the shard stored under `/shard/<commitment>/<promiseHash>` or `NotFound`; when unset, today's behaviour. The store already keys by (commitment, promiseHash), so the lookup exists; `Get` gains a variant taking the hash. Cost: one optional field on the wire and a few lines in `server_download.go`.

With it, a client of promise P can fetch P's shard, a retention obligation becomes checkable per promise by anyone, and an abandoned upload can no longer stand in for a settled one. Without it, "the validator served the blob" is the only download-side statement the protocol supports, and any per-promise claim rests on hash order.

A smaller alternative would be to serve the newest settled promise's shard first, but the server has no view of settlement, so hash order is the only order it can apply consistently.

## What we verified

- `store.go` `Put`/`Get` as above (commit pinned in `fibre-assign/params.go`).
- `server_download.go`: `s.store.Get(ctx, id.Commitment())`, no promise argument.
- `server_upload.go`: `Has(commitment, promiseHash)` before `Put`, so duplicates of one promise are deduplicated; different promises are not.
