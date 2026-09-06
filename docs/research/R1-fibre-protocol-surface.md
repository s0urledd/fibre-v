# R1 — Celestia Fibre protocol surface (from source)

Scope: what an independent observer needs to know about Fibre's on-chain modules, the Fibre server's client-facing API, TLS identity, config, params, prune timing, and what changed between the observer's pinned celestia-app commit and current main.

Method: everything below is read from the checkouts named here. Nothing is from memory. Where a fact could not be established from source it is marked **unknown** with what would resolve it.

| Checkout | Commit | `git describe --tags` |
|---|---|---|
| pin | `0b69316466c3ba02f708c0e2a101f834d5d1827f` ("fix!: count MsgModuleQuerySafe queries against the block message limit (#7762)") | `v9.0.0-arabica-333-g0b693164` |
| main | `5735e05013780b513b190f8253554f9e71781f4d` ("docs: clarify Fibre prune integrity handling in ADR 030 (#7779)") | `v10.0.1-corto-5-g5735e050` |
| observer repo (plsgiveup/fibre) | `ac4bae425dc586f3d4f1fa2392c0f3de05d6e827` | — |

Citation convention: `path:line @pin`. Between pin and main, `git diff 0b6931646..HEAD -- fibre x/fibre x/valaddr proto specs` touches only `fibre/cmd/README.md` and three files under `fibre/internal/e2e/` (see §8). Every Go/proto/spec line cited below is therefore byte-identical at main unless a note says otherwise; where a main-only file is cited it is marked `@main`.

---

## 1. Message types

### x/fibre (`proto/celestia/fibre/v1/tx.proto @pin`)

| Msg | Signer field | Fields | REST (gateway) | Source |
|---|---|---|---|---|
| `MsgDepositToEscrow` | `signer` | `signer` (AddressString), `amount` (Coin) | `POST /fibre/v1/deposit-to-escrow` | tx.proto:19-24, 55-61 |
| `MsgRequestWithdrawal` | `signer` | `signer`, `amount` (Coin) | `POST /fibre/v1/request-withdrawal` | tx.proto:27-32, 67-73 |
| `MsgPayForFibre` | `signer` | see below | `POST /fibre/v1/pay-for-fibre` | tx.proto:35-40, 79-87 |
| `MsgPaymentPromiseTimeout` | `signer` ("can be anyone") | `signer`, `payment_promise` (PaymentPromise) | `POST /fibre/v1/payment-promise-timeout` | tx.proto:43-48, 93-99 |
| `MsgUpdateFibreParams` | `authority` | `authority`, `params` (Params, all fields required) | none (no http annotation) | tx.proto:51, 106-114 |

There is no "withdraw" message that pays out directly: `MsgRequestWithdrawal` schedules, and BeginBlocker pays out after `withdrawal_delay` (`x/fibre/keeper/abci.go:50-125 @pin`).

**`MsgPayForFibre` exact proto fields** (tx.proto:79-87 @pin):

```
string        signer               = 1  [(cosmos_proto.scalar) = "cosmos.AddressString"]  // option (cosmos.msg.v1.signer) = "signer"
PaymentPromise payment_promise     = 2  [(gogoproto.nullable) = false]
repeated bytes validator_signatures = 3
```

`validator_signatures` is positional over the validator set at `payment_promise.height`; empty entries are skipped; more entries than validators is rejected; the only threshold is voting power ≥ `TotalVotingPower * 2 / 3` (integer division) (`x/fibre/keeper/msg_server.go:380-410 @pin`, `fibre/validator/signature_set.go:30-35,58,84-88 @pin`; spec `specs/src/fibre_module.md:258 @pin`).

**`PaymentPromise` exact proto fields** (`proto/celestia/fibre/v1/fibre.proto:29-51 @pin`):

```
string                          chain_id           = 1   // e.g. corto-1, mocha-4, celestia
int64                           height             = 2   // determines the validator set used
bytes                           namespace          = 3
uint32                          blob_size          = 4   // padded upload size in bytes (fibre_server.md:64)
uint32                          blob_version       = 5
bytes                           commitment         = 6   // hash of row root and RLC root
google.protobuf.Timestamp       creation_timestamp = 7   [stdtime, non-nullable]
cosmos.crypto.secp256k1.PubKey  signer_public_key  = 8   [non-nullable]  // escrow owner
bytes                           signature          = 9   // escrow owner signature over sign bytes
```

Sign bytes: `RawBytesMessageSignBytes(chain_id, "fibre/pp:v0", stripped)` where `stripped = signer_pubkey(33) || namespace(29) || blob_size u32be || commitment(32) || blob_version u32be || height u64be || creation_timestamp Go-binary(15)` (`fibre/payment_promise.go:182-200 @pin`; `specs/src/fibre_server.md:66-83 @pin`). Stateless validation rules: `fibre/payment_promise.go:127-175 @pin` (33-byte key, chain ID ≤ 20 bytes, upload size > 0, non-zero timestamp, 64-byte signature, height > 0, signature verifies).

Other state objects: `EscrowAccount{signer, balance, available_balance}` (fibre.proto:15-25), `Withdrawal{signer, amount, requested_timestamp, available_timestamp}` (fibre.proto:56-66), `ProcessedPayment{payment_promise_hash, processed_at}` (`genesis.proto:24-27`).

### x/valaddr (`proto/celestia/valaddr/v1/tx.proto @pin`)

Exactly one message:

```
message MsgSetFibreProviderInfo {         // option (cosmos.msg.v1.signer) = "signer"
  string signer = 1 [(cosmos_proto.scalar) = "cosmos.ValidatorAddressString"];  // celestiavaloper...
  string host   = 2;                       // "host:port"
}
```
tx.proto:14-30. REST: `POST /valaddr/v1/set-fibre-provider-info` (tx.proto:16-19). CLI: `celestia-appd tx valaddr set-host [host]` (`x/valaddr/client/cli/tx.go:32 @pin`).

There is **no remove/unregister message**. Removal happens only by EndBlock garbage collection: validator not found in staking, or jailed **and** unbonded for longer than `JailedGracePeriod = 7 * 24h` after `UnbondingTime` (`x/valaddr/keeper/keeper.go:83-132 @pin`, `x/valaddr/types/keys.go:20-26 @pin`, `x/valaddr/module.go:122-125 @pin`). Update = send `set-host` again; the keeper overwrites (`keeper.go:47-56`).

---

## 2. Events

### x/fibre — typed events

All x/fibre events are emitted with `EmitTypedEvent`, so the event **type string is the proto full name** (`x/fibre/types/events.go:10-18 @pin` uses `proto.MessageName`), e.g. `celestia.fibre.v1.EventPayForFibre`. Attributes are the proto fields (JSON-encoded values, as the SDK does for typed events).

| Event (proto name, `event.proto @pin`) | Attributes | Emitted by |
|---|---|---|
| `EventDepositToEscrow` | `signer`, `amount` (Coin) | `msg_server.go:64-65` |
| `EventWithdrawFromEscrowRequest` | `signer`, `amount`, `requested_at`, `available_at` | `msg_server.go:115-116` |
| `EventWithdrawFromEscrowExecuted` | `signer`, `amount` | BeginBlocker `abci.go:117-118` |
| `EventPayForFibre` | `signer`, `namespace` (bytes), `commitment` (bytes), `validator_count` (uint32 = length of the submitted signature slice) | `msg_server.go:174-175` |
| `EventPaymentPromiseTimeout` | `processor`, `escrow_signer`, `payment_promise_hash` | `msg_server.go:259-260` |
| `EventUpdateFibreParams` | `signer` (authority), `params` (full Params) | `msg_server.go:285-286` |
| `EventProcessedPaymentPruned` | `payment_promise_hash`, `processed_at` | BeginBlocker `abci.go:162-163` |

Note for an observer: `EventPayForFibre` carries namespace, commitment and a count, but **not** the PaymentPromise itself (no `creation_timestamp`, `height`, `blob_size`). Those must be read from the tx body (`MsgPayForFibre.payment_promise`).

### x/valaddr — plain SDK event

```
type: set_fibre_provider_info
  validator_consensus_address = <celestiavalcons...>
  host                        = <host:port>
```
`x/valaddr/types/msg.go:11-18 @pin`, `x/valaddr/keeper/msg_server.go:58-64 @pin`. The proto `EventSetFibreProviderInfo` (`event.proto:7-12`) exists but the handler emits the plain event, as the spec also states (`specs/src/fibre_registry_module.md:91-98 @pin`). No event is emitted on EndBlock garbage collection (`keeper.go:126-131` only deletes).

---

## 3. Query endpoints

### x/fibre `Query` (`proto/celestia/fibre/v1/query.proto:15-44 @pin`)

| RPC | REST | Notes |
|---|---|---|
| `Params` | `GET /fibre/v1/params` | returns `Params` |
| `EscrowAccount{signer}` | `GET /fibre/v1/escrow-account/{signer}` | `escrow_account`, `found` |
| `Withdrawals{signer, pagination}` | `GET /fibre/v1/withdrawals/{signer}` | handler ignores pagination (`x/fibre/keeper/grpc_query.go:47-60`; spec fibre_module.md:418) |
| `IsPaymentProcessed{promise_hash}` | `GET /fibre/v1/is-payment-processed/{promise_hash}` | `processed_at`, `found` |
| `ValidatePaymentPromise{promise}` | `POST /fibre/v1/validate-payment-promise` | returns `is_valid`, `expiration_time`, `shard_retention`; invalid promise is a gRPC error not `is_valid=false` (grpc_query.go:102-105); may reserve against a validator-local budget and return `ResourceExhausted` (grpc_query.go:107-123) |

Gateway routes are registered (`x/fibre/module.go:80-84 @pin`). CLI: `celestia-appd query fibre params|escrow-account|withdrawals|is-payment-processed` (`specs/src/fibre_module.md:477-480 @pin`).

### x/valaddr `Query` (`proto/celestia/valaddr/v1/query.proto:10-22 @pin`)

| RPC | REST | Returns |
|---|---|---|
| `FibreProviderInfo{validator_consensus_address}` | `GET /valaddr/v1/fibre-provider-info/{validator_consensus_address}` | `info {host}`, `found` |
| `AllBondedFibreProviders{}` | `GET /valaddr/v1/all-bonded-fibre-providers` | `providers[] {validator_consensus_address, info{host}}` — **filtered to currently bonded validators** (`x/valaddr/keeper/grpc_query.go:32-61 @pin`), no pagination |

**How a client lists validators' Fibre endpoints:** call `AllBondedFibreProviders` (this is what the reference client does at start: `fibre/internal/grpc/host_registry.go:181-196 @pin`), then per-validator `FibreProviderInfo` keyed by consensus address, re-queried at most once per `DefaultRefreshInterval = DelayedPrecommitTimeout + TimeoutCommit` (host_registry.go:20-23, 119-175). Gateway routes are registered (`x/valaddr/module.go:65-69 @pin`). CLI: `celestia-appd query valaddr provider <consaddr>` and `celestia-appd query valaddr providers` (`x/valaddr/client/cli/query.go:34,67 @pin`).

**Endpoint record contents:** exactly one field, `host` (string) (`query.proto:56-59 @pin`; state key `0x01 | consAddr -> FibreProviderInfo`, `x/valaddr/types/keys.go:28-38 @pin`). No port field separate from host, no pubkey, no TLS fingerprint, no timestamp, no version. Validation: non-empty, ≤ 100 chars, `net.SplitHostPort` succeeds, non-empty host part, numeric port in `[1, 65535]`; schemes and paths rejected (`x/valaddr/types/msg.go:20-27, 52-79 @pin`). The registry re-applies `ValidateHost` on read (host_registry.go:113-129).

**Registration path:** `MsgSetFibreProviderInfo` signed by the operator address; the handler looks up the staking validator, derives the consensus pubkey → consensus address and stores under that (`x/valaddr/keeper/msg_server.go:26-56 @pin`). Update = re-send. Removal = EndBlock GC only (§1).

**Spec drift to be aware of:** `specs/src/fibre_registry_module.md:28,129-154 @pin` still documents `AllFibreProviders` / `GET /valaddr/v1/all-fibre-providers` (unfiltered). The proto, keeper, and `x/valaddr/README.md:79 @pin` all say `AllBondedFibreProviders` / `/valaddr/v1/all-bonded-fibre-providers`. Trust the proto.

---

## 4. Fibre server client-facing gRPC service and TLS

### Service definition (`proto/celestia/fibre/v1/service.proto @pin`)

```
service celestia.fibre.v1.Fibre {
  rpc UploadShard  (UploadShardRequest)   returns (UploadShardResponse);     // unary
  rpc DownloadShard(DownloadShardRequest) returns (DownloadShardResponse);   // unary
}

message BlobRow   { uint32 index = 1; bytes data = 2; repeated bytes proof = 3; }
message BlobShard { repeated BlobRow rows = 1; bytes rlcs = 2; }   // rlcs: flattened, 16 bytes per ORIGINAL row (4096*16 = 65536 bytes for v0)

message UploadShardRequest   { PaymentPromise promise = 1; BlobShard shard = 2; }
message UploadShardResponse  { bytes validator_signature = 1; }   // comment says "const len == 32"; code requires ed25519 signature length (spec fibre_server.md:83)
message DownloadShardRequest { bytes blob_id = 1; }               // 33 bytes = blob_version(1) || commitment(32)
message DownloadShardResponse{ BlobShard shard = 1; }
```
service.proto:8-50. **No streaming** — both RPCs are unary. `BlobIDSize = 33`, `CommitmentSize = 32` (`fibre/blob_id.go:13,76 @pin`).

**DownloadShard behaviour** (`fibre/server_download.go:19-84 @pin`): parses the 33-byte blob ID (InvalidArgument on failure), checks `BlobConfigForVersion(id.Version())` (only version 0 exists: `fibre/blob.go:64-71`), then `store.Get(commitment)` — returns the first stored shard for that commitment (`NotFound` if none, `Internal` on read error). The response contains **only the rows this validator was assigned** plus the full RLC vector; the server does no assignment check on download and the client verifies (`fibre/client_download.go:177-204`, `fibre/download.go:126-145`).

**Auth:** none at the application layer. No client certificate / mTLS; `DownloadShard` is public to any peer that completes the server-authenticated TLS handshake (`specs/src/fibre_server.md:131 @pin`; `fibre/cmd/README.md` at main, lines 139-155 @main). Uploads are gated only by the payment-promise checks (`fibre/server_upload.go:35-41, 66-80`).

**Codec:** the server installs a custom CodecV2 named `fibre-proto` via `grpclib.ForceServerCodecV2` (`fibre/server.go:137-140 @pin`; `fibre/internal/grpc/codec.go:12,45-56`). Its wire format is ordinary protobuf (Unmarshal delegates to gogoproto `Unmarshal`, codec.go:85-101); the wrapper only adds buffer pooling and pre-decode row/proof count limits for `UploadShardRequest`. `ForceServerCodec` "will override any lookups by content-subtype" (grpc-go v1.83.2 `server.go:372-373` in the module cache), so a stock gRPC client with the default `proto` subtype works — the observer's prober uses a stock `grpc.NewClient` with no `CallContentSubtype` (`fibre-sentinel/internal/probe/probe.go:267-281`) and its committed devnet run shows 27 `SERVED_OK` measurements (`fibre-sentinel/sample/probe/measurements.jsonl`). The reference client sets `CallContentSubtype("fibre-proto")` (`fibre/internal/grpc/fibre_client.go:94-99`), which is not required for correctness on the wire.

### Which listeners have TLS

| Link | TLS | Source |
|---|---|---|
| Client-facing Fibre gRPC (`server_listen_address`, default `0.0.0.0:7980`) | **TLS 1.3 only, always on, no plaintext fallback** | `fibre/server.go:125-142 @pin` (`credentials.NewTLS`, `MinVersion: tls.VersionTLS13`); `specs/src/fibre_server.md:131`; `fibre/cmd/README.md:139-155 @main` |
| Fibre server → app node gRPC (`app_grpc_address`, default `127.0.0.1:9090`) | **no TLS** | `specs/src/fibre_server.md:121 @pin`; `fibre/cmd/README.md:9 @pin` and `:152 @main` |
| Fibre server → privval signer gRPC (`signer_grpc_address`, default `127.0.0.1:26669`) | **no TLS** | same |

### Self-signed cert + consensus-key-signed extension

Normative spec: **`specs/src/fibre_tls_identity.md @pin`** (unchanged at main). Implementation: `fibre/internal/tlsid/tlsid.go @pin`.

- Ephemeral Ed25519 TLS key generated on every server start; self-signed X.509 v3; CN `celestia-fibre`; KeyUsage digitalSignature; EKU serverAuth + clientAuth; random 128-bit serial (`tlsid.go:145-226`).
- Validity: `notBefore = now − 5min`, `notAfter = now + 365d`, both truncated to seconds (`tlsid.go:102-112, 182-183`; spec :11). No in-process refresh; re-minted on restart (`tlsid.go:95-102`).
- Extension OID **`1.3.6.1.4.1.66463.1.1`** (IANA PEN 66463 = Celestia; `.1` = Fibre; `.1.1` = TLS signed identity), non-critical (`tlsid.go:114-122`; `specs/src/fibre_server.md:135-144`).
- Extension value = DER `SignedIdentity{ payload OCTET STRING, signature OCTET STRING }` where `payload` = DER `BindingPayload{ version INTEGER (=1), notBefore INTEGER (unix s), notAfter INTEGER (unix s), tlsPubKey OCTET STRING (SPKI DER of the TLS key, 44 bytes) }` (`tlsid.go:124-139, 185-203`; spec :14-31).
- **What is signed:** `signedBytes = "COMET::RAW_BYTES::SIGN" || uvarint(len(P)) || P`, `P = protobuf(SignRawBytesRequest{chain_id, raw_bytes = "celestia-fibre-tls:" || DER(BindingPayload), unique_id = "celestia-fibre-tls-v1"})`, signed Ed25519 by the validator consensus key via privval `SignRawBytes` (`tlsid.go:74-81, 195, 323-328, 368-373`; spec :33-49). The chain ID is in the envelope but **not** in `BindingPayload`; the consensus pubkey is **not** embedded — the verifier supplies the expected key from the validator set.
- Verification (14 ordered checks, spec :51-68; code `tlsid.go:267-366`): extension present; ≤ 8192 bytes; parses with no trailing bytes; payload non-empty and ≤ 4096; signature non-empty; payload parses with no trailing bytes; version == 1; signature verifies under expected consensus key; cert SPKI == `tlsPubKey`; `notAfter > notBefore`; window ≤ 365d + 10min; `notBefore − 5min ≤ now ≤ notAfter + 5min`; cert NotBefore/NotAfter (unix s) equal signed window; serverAuth EKU present.
- Client side: `InsecureSkipVerify: true` + `VerifyConnection` callback (runs on resumed sessions too), TLS 1.3 min (`fibre/internal/grpc/fibre_client.go:72-89`).
- Golden vectors: `fibre/internal/tlsid/testdata/identity_vectors.json` (spec :86-113).

---

## 5. Fibre server config keys

### TOML file `$FIBRE_HOME/config/server_config.toml` (`fibre/server_config.go:22-27 @pin`; note `fibre/cmd/README.md:88 @pin` says `$FIBRE_HOME/server_config.toml` — the code path is `<home>/config/server_config.toml`, `server_config.go:25-26`)

| TOML key | Flag | Default | Source |
|---|---|---|---|
| `app_grpc_address` | `--app-grpc-address` | `127.0.0.1:9090` | server_config.go:31-32, 90; start_cmd.go:69 |
| `server_listen_address` | `--server-listen-address` | `0.0.0.0:7980` | server_config.go:33-34, 91; start_cmd.go:70 |
| `signer_grpc_address` | `--signer-grpc-address` | `127.0.0.1:26669` (PrivValidatorAPI gRPC) | server_config.go:35-36, 92; start_cmd.go:71 |
| `upload_verify_workers` | (no flag) | `runtime.GOMAXPROCS(0)`; must be ≥ 1 | server_config.go:37-38, 99, 148-150 |
| `unlimited_budget` | `--unlimited-budget` | `false` | server_config.go:66-69; start_cmd.go:72 |

Persistent flags: `--home` / `FIBRE_HOME` (default `~/.celestia-fibre`) (`fibre/cmd/root_cmd.go:14-26, 80-86 @pin`); `--log-level`, `--log-format` (`fibre/cmd/log.go:55-56`); `--otel-endpoint` (tracing + metrics, `fibre/cmd/tracing.go:28`, `metrics.go:23`); `--pprof[=addr]`, `--pyroscope-endpoint`, `--pyroscope-basic-auth-user/-pass` (`fibre/cmd/profiling.go:33-39`). Precedence flag > file > default (start_cmd.go:38-53, 66-68).

Not TOML-visible (`toml:"-"`), compiled in: `LivenessThreshold`, `MinRowsPerValidator`, `OriginalRows`, `MaxShardSize`, `MaxMessageSize`, `StoreConfig.Path` (set from `--home`, start_cmd.go:61) (server_config.go:40-53; `fibre/store.go` `StoreConfig{Path, Log}`).

**Storage budget:** derived, not configured: `budget = FullStakeStorageBudget(on-chain) * assignedRows / OriginalRows`, recomputed at startup and on every prune tick; a validator outside the active set derives 0 (`fibre/server.go:212-285 @pin`). `--unlimited-budget` disables the limiter (server.go:219-222). Upload rejected with `ResourceExhausted` + `RetryInfo{pruneInterval + jitter ≤ 30s}` when over budget (`fibre/server_upload.go:100-110, 163-169`).

**ShardRetention:** no local key. The server takes `shard_retention` from the chain on every upload via `ValidatePaymentPromise` (`fibre/cmd/README.md:97-101 @pin`; `fibre/internal/grpc/app_client.go:98-113`).

**Connection / concurrency limits (compile-time constants, `fibre/internal/grpc/server.go:19-40, 53-86 @pin`):**

| Knob | Value |
|---|---|
| `maxConnections` (netutil.LimitListener) | 16 |
| `maxConcurrentStreams` (per connection) | 13 |
| `connectionTimeout` (TCP+TLS+HTTP/2 setup) | 15 s |
| keepalive enforcement `MinTime` | 10 s |
| keepalive `MaxConnectionIdle` / `Time` / `Timeout` | 5 min / 2 min / 20 s |
| `MaxRecvMsgSize` = `MaxSendMsgSize` = `MaxMessageSize` | 138,857,562 bytes (≈132.4 MiB; derivation in §6) — `fibre/server.go:134-135` |
| codec pre-decode caps (upload only) | rows ≤ `MaxRowsPerValidator()` = 4096, proof segments ≤ `MerkleProofDepth()` = 14 — `fibre/server.go:137-140` |
| `UploadVerifyWorkers` (verifier pool) | GOMAXPROCS — `fibre/server_upload.go:319-358` |

**Download rate limiting: none, at either commit.** `fibre/server_download.go @pin` has no limiter, token bucket, per-client cap, bandwidth cap or concurrency cap specific to downloads; the only bounds on downloads are the global 16-connection / 13-streams-per-connection caps and keepalive above. The spec says so explicitly: "The server does not implement per-peer token buckets, throughput caps, request backoff hints, or explicit upload/download RPC concurrency limits" (`specs/src/fibre_server.md:226 @pin`). (That spec line is slightly stale on uploads: the code now does return `ResourceExhausted` with a `RetryInfo` backoff hint on budget exhaustion, `server_upload.go:104-109`, contradicting `fibre_server.md:222`.) The comment in `internal/grpc/server.go:19-24` calls tying caps to staking power or adding a per-peer policy "possible follow-ups".

---

## 6. Parameters

### On-chain `x/fibre` Params (`proto/celestia/fibre/v1/params.proto:10-29 @pin`; `x/fibre/types/params.go @pin`)

| Param | Type | Default | Min | Max | Validation | Source |
|---|---|---|---|---|---|---|
| `withdrawal_delay` | Duration | 24h | `MaxPaymentPromiseTimeout + 10min` = **12h10m** | 7×24h = **168h** | non-nil, in range | params.go:21, 47-61, 146-165 |
| `payment_promise_timeout` | Duration | 1h | 10min | 12h | non-nil, in range | params.go:23, 63-70, 168-187 |
| `payment_promise_height_window` | uint64 | 1000 | 1 (≠ 0) | none | non-zero | params.go:25, 190-201 |
| `shard_retention` | Duration | 4h | 10min | 7×24h = 168h | non-nil, in range | params.go:28, 72-75, 204-223 |
| `full_stake_storage_budget` | uint64 (bytes) | `2 << 40` = 2 TiB | 1 (≠ 0) | none ("any positive budget is valid") | non-zero | params.go:31, 225-238 |

Derived constants (not params): `MaxPromiseClockSkew = 10min` (promise may be timestamped up to 10 min ahead of block time; params.go:35-38, keeper.go:400-405); `MinTimeoutSettlementWindow = 10min` (params.go:40-47); `PaymentPromiseRetentionWindow() = withdrawal_delay + 10min` for processed-payment replay records (params.go:99-105). Promise freshness: `creation_timestamp` must be after `max(block_time − withdrawal_delay, freshness floor)` (keeper.go:376-397).

### Compiled-in protocol params (`fibre/protocol_params.go:57-70 @pin`)

| Constant | Value | Source |
|---|---|---|
| `Rows` (= `OriginalRows`, K) | `1 << 12` = **4096** | :59 |
| `EncodingRatio` | 0.25 | :60 |
| `TotalRows()` = K+N | **16384** (`ParityRows` = 12288) | :80-88 |
| `MaxValidatorCount` | 100 | :62 |
| `UniqueDecodingSecurityBits` | 100 | :64 |
| `SafetyThreshold` | 2/3 | :65 |
| `LivenessThreshold` | **1/3** ("the minimum percentage of stake needed to cause a liveness failure") | :45-47, 66 |
| `MaxBlobSize` | `1 << 27` = **128 MiB** (including the 5-byte header) | :68 |
| `MinRowSize` | `field.LeopardChunkSize` = 2 × 32 = **64 bytes** | :69; `pkg/rsema1d/field/leopard.go:3-6` |
| `MinRowsPerValidator()` | max(ceil(100 / (1 − log2 1.25)) = 148, ceil(4096 / ceil(100·1/3)=34) = 121) = **148** | :136-159 |
| `MaxRowsPerValidator()` | ceil(4096 · (3−2)/3 · 3/1) = **4096** | :113-127 |
| `ValidatorsForReconstruction()` | ceil(100 · 1/3) = **34** (uses MaxValidatorCount, not the live set) | :161-168 |
| `MerkleProofDepth()` | bits.Len(16383) = **14** | :212-216 |
| `MaxRowSize(0)` | ceil(128 MiB / 4096) = 32768 (multiple of 64) | :170-195 |
| `MaxShardSize()` | 4096·16 + 4096·(4 + 32768 + 14·32) = **136,134,656** | :197-210 |
| `MaxPaymentPromiseSize` | 125 + 64 + 20 = 209 | `fibre/payment_promise.go:182-200` |
| `MaxMessageSize()` | (136,134,656 + 209) + 2% = **138,857,562** | :218-223 |

(Numeric values above were recomputed from the formulas; they match the observer's `ParamsV10BlobV0 = {4096, 16384, 148, 1/3}` in `fibre-assign/params.go:64-69`.)

Blob (`fibre/blob.go @pin`): only **blob version 0** is supported (`:64-71, :76-78`); header = 1 byte version + 4 bytes data size (`:290-296`); `MaxDataSize = MaxBlobSize − 5` (`:106`); `NewBlob` rejects empty data and data > `MaxDataSize` (`:158-163`); `UploadSize = RowSize(dataLen+5) * 4096` where `RowSize` rounds up to a multiple of 64 (`:120-124`, protocol_params.go:173-190). So the smallest possible `blob_size` (promise field 4) is 64 × 4096 = **262,144 bytes = 256 KiB**, and the largest is 128 MiB; the smallest *user payload* is 1 byte. An `init()` guard panics if `LivenessThreshold < EncodingRatio` (protocol_params.go:72-78).

### Reconstruction threshold, from source

- **Rows:** reconstruction needs **K = 4096 unique verified rows out of 16384**, i.e. 1/4 of the extended rows. `download.reconstruct()` calls `Reconstructor.Reconstruct`; `rsema1d.ErrNotEnoughRows` with some rows → `ErrNotEnoughShards`, with zero rows → `ErrNotFound` (`fibre/download.go:215-232 @pin`); dispatch stops when `reconstructor.Want() == 0` (`download.go:87-118`). Spec: "any 4096 rows out of 16384 are enough to reconstruct the blob, so the row recovery threshold is 1/4 of the extended data" (`specs/src/fibre_encoding.md:51 @pin`).
- **How LivenessThreshold is used in assignment:** `AssignedRows = min(max(ceil(OriginalRows · VotingPower · 3 / (TotalVotingPower · 1)), 148), 4096)` (`fibre/validator/set.go:107-113 @pin`). The relation `originalRows / rows = livenessThreshold / stake%` means a validator (or group) holding **1/3 of voting power is assigned all 4096 original-row-equivalents** (`set.go:48-53, 70-72`; spec `fibre_server.md:172-179`). Row indices `0..16383` are Fisher-Yates shuffled with ChaCha8 seeded by the commitment, then handed out in validator-set order; when the sum of assignments exceeds 16384 (because of the 148-row floor) indices wrap modulo `totalRows`, so rows may be duplicated across validators (`set.go:65-105`). Download also uses it in `Select` to find the split point where assignments start overlapping (`set.go:120-155`), and the server uses it for the storage budget (`server.go:256`).
- Consequence, stated as the source states it: liveness is a **voting-power** condition ("fraction of stake needed for reconstruction (typically 1/3)", `server_config.go:42-43`; "1/4 row threshold gives room for assignment rounding, duplicate rows, slow or missing validators, and bad rows discarded by RLC verification", `fibre_encoding.md:51`). The code does not encode "1/3 of validators by count" anywhere except `ValidatorsForReconstruction()` (34 of a hypothetical 100), which only feeds the `MinRowsPerValidator` floor.
- Safety/signing threshold is separate: `MsgPayForFibre` needs ≥ 2/3 voting power in signatures (§1).

---

## 7. `must_serve_until` / prune timing

Observer formula (`fibre-sentinel/README.md:72,118`; `fibre-sentinel/internal/scan/params.go:136-149`; `fibre-sentinel/internal/probe/doc.go:9`):
`must_serve_until = creation_timestamp + max(payment_promise_timeout, shard_retention)`.

Source, identical at pin and main:

1. `ValidatePaymentPromise` returns `expiration_time = creation_timestamp + params.PaymentPromiseTimeout` (`x/fibre/keeper/keeper.go:407 @pin`, returned at `grpc_query.go:125-129`) and `shard_retention = params.ShardRetention` (`grpc_query.go:128`), both read from the params **in effect at upload time**.
2. The server computes `pruneAt = shardPruneAt(creation_timestamp, expiresAt, retention)` = `expiresAt` if it is after `creation_timestamp + retention`, else `creation_timestamp + retention` (`fibre/server_upload.go:216, 220-230 @pin`) — i.e. exactly `creation_timestamp + max(PaymentPromiseTimeout, ShardRetention)`. Anchored on `creation_timestamp`, not `time.Now()`, deliberately (`server_upload.go:220-223`). Also `proto/celestia/fibre/v1/query.proto:98-99` comment and `specs/src/fibre_server.md:158, 206`.
3. `Store.Put(ctx, promise, shard, pruneAt)` (`server_upload.go:114`); the prune loop runs every `pruneInterval = time.Minute` and calls `store.PruneBefore(ctx, time.Now())` (`fibre/server_prune.go:8-49 @pin`). The prune index key is minute-granular: `/prune/<YYYYMMDDHHmm>/<commitment>/<hash>` (`specs/src/fibre_server.md:199 @pin`).

**Confirmed.** The formula the observer uses matches both commits. Two practical notes for the tolerance window: deletion happens on the next minute tick after `pruneAt` and the index is bucketed by minute, so a shard can outlive `must_serve_until` by up to roughly two minutes; and there is no guarantee a shard is served *after* `must_serve_until` (the observer's "NOT_FOUND tolerated after must_serve_until" rule is consistent with this). **Unknown:** exact bucket rounding in `fibre/store.go` `PruneBefore` (not read); resolving it would pin the upper bound of the prune lag precisely.

---

## 8. Diff pin → main

`git -C celestia-app log --stat 0b6931646..HEAD` — 9 commits:

| Commit | Date | Subject | Files | Affects observer-relevant surface? |
|---|---|---|---|---|
| e9ffc40f | 2026-09-02 | fix!: count ICA messages in MsgRecvPacket against the block message limit (#7761) | `app/*` msg counting, tests | No (block message accounting, not Fibre) |
| ca74ffaa | 2026-09-02 | chore(deps): bump celestia-core v0.41.0, nmt v0.24.4, rsmt2d v0.15.3, cosmos-sdk api v0.7.7 (#7764) | go.mod/go.sum, Dockerfiles, testnode | Not Fibre code. Indirectly: celestia-core v0.41.0 adds a heavy-RPC concurrency limit (default 20, gRPC `ResourceExhausted` on excess) covering the gRPC validator-set endpoint (`docs/release-notes/release-notes.md:30-32 @main`) — the observer's validator-set queries against a public RPC can be throttled |
| 69c2d936 | 2026-09-02 | docs: add v10 node operator release notes (#7765) | release notes | No (docs). This commit is tag `v10.0.0-corto` |
| 6e5c64c0 | 2026-09-02 | fix: force inter-block cache on for embedded v3 app (#7771) | multiplexer | No. This commit is tag `v10.0.1-corto` |
| 5a2bb4e7 | 2026-09-03 | docs: simplify fibre server TLS section for node operators (#7766) | `fibre/cmd/README.md` | Docs only; rewrites the TLS section (same facts: TLS always on, app+signer links not TLS, downloads public) |
| fc71c89c | 2026-09-03 | docs: add Fibre elastic shard storage ADR (#7763) | `docs/architecture/adr-030-…md` | **Proposed** (Status: Proposed, ADR-030:14-16 @main). Moves shard payloads to S3/R2 object storage; explicitly does **not** change protobufs, consensus state, governance params, the upload service, or client egress (ADR-030:44-52). Not implemented in this range |
| db30be0a | 2026-09-03 | test(fibre): wait for settlement state visibility before asserting (#7773) | `fibre/internal/e2e/*` | Tests only |
| 670185b7 | 2026-09-04 | feat: report effective tx latency alongside actual latency (#7772) | latency-monitor tool, docker-e2e | No |
| 5735e050 | 2026-09-04 | docs: clarify Fibre prune integrity handling in ADR 030 (#7779) | ADR-030 one line | No |

`git diff --stat 0b6931646..HEAD -- fibre x/fibre x/valaddr proto specs`: **4 files** — `fibre/cmd/README.md` (−61/+13), `fibre/internal/e2e/fibre_e2e_test.go`, `fibre_stack_test.go`, `fibre_timeout_e2e_test.go`. **Zero** changes to assignment (`fibre/validator/`), retention/prune (`server_upload.go`, `server_prune.go`, `store.go`), the serving API (`proto/celestia/fibre/v1/service.proto`, `server_download.go`), TLS identity (`fibre/internal/tlsid/`), params (`x/fibre/types/params.go`, `fibre/protocol_params.go`, `fibre/blob.go`), or `x/valaddr`.

**fibre-assign `ParamsV10BlobV0` fingerprint:** the four inputs (`OriginalRows=4096, TotalRows=16384, MinRowsPerValidator=148, LivenessThreshold=1/3`) are unchanged at main — `fibre/protocol_params.go` and `fibre/blob.go` are byte-identical between pin and HEAD, so `Fingerprint()` (`fibre-assign/params.go:102-111`, sha256 over the five integers) is unchanged. Note `protocol_params.go` did change between the vector commit `dba1550` and the pin (commit 9a9a6504, 2026-08-28, "bound UploadShard row and proof counts before decoding (#7754)"): a refactor that extracted `MerkleProofDepth()`; no constant values changed (diff shows only the tree-depth computation moved into a method).

**fibre-tlsverify golden vectors:** `dba155084505a8f6c5d37260a94f70f939fb96de` (2026-08-13, "docs(fibre): specify endorsed TLS identity and add golden vectors (#7662)") is reachable and is an ancestor of the pin. `sha256(fibre/internal/tlsid/testdata/identity_vectors.json)` is `22e22a790264f6cdfc03263b564c9033c3d7b00146814a98a4686f337b008d9d` at dba1550, at the pin, at HEAD, **and** for the observer's copy `fibre-tlsverify/testdata/identity_vectors.json`. `fibre/internal/tlsid/` has no diff between dba1550 and HEAD. Unchanged.

---

## 9. Recommendation on bumping the pin; release tagging

**Bump at activation (to the deployed tag), not now.** Reasons tied to the diff:

- Nothing the observer depends on changed between the pin and main (§8): no assignment, retention, API, TLS or param change; fingerprint and vectors are identical. A bump now buys nothing functionally.
- The pin is already inside the v10 release line: pin → v10.0.0-corto (69c2d936) is 2 commits, pin → v10.0.1-corto (6e5c64c0) is 4 commits, none in Fibre dirs. The pin's own `git describe` reads `v9.0.0-arabica-333-g0b693164` only because no v10 tag is an ancestor of it (v10.0.0-corto is 2 commits *after* the pin).
- The only pending change with any Fibre footprint is ADR-030 (storage backend), which is "Proposed", declares the wire/protobuf/params surface out of scope, and would still need a code PR before it matters. Watch for its implementation PR, and for any change to `fibre/protocol_params.go`, `fibre/blob.go`, `fibre/validator/set.go`, `fibre/internal/tlsid/`, `x/fibre/types/params.go`, `proto/celestia/{fibre,valaddr}`.
- What *does* argue for a later re-pin: the `-mocha` tag will be a stable, auditable reference (`PinnedCelestiaAppCommit` is meant to be diffed "at its release tag", `fibre-assign/params.go:9-14`), and the deps bump (celestia-core v0.41.0 heavy-RPC limit) is operationally relevant to the observer's RPC usage even though it is not in Fibre code. When the mocha tag is cut: re-run `git diff <pin>..<tag> -- fibre x/fibre x/valaddr proto specs`, re-hash `identity_vectors.json`, recompute the fingerprint, and update `PinnedCelestiaAppCommit`.

**Tag/branch a mocha release would come from** (`docs/maintainers/release-guide.md @pin`): release branches are created from the version branch and named like `v4.1.0-release` (`:15-19`); RC tags carry `-rc<N>` (`:26-27`); testnet tags drop `-rc` and append **`-corto`** (internal `corto-1`) or **`-mocha`** (public Mocha) "depending on the target network" (`:74`); the mainnet tag has no suffix (`:88`). Existing v10 tags: `v10.0.0-corto`, `v10.0.1-corto` only; no `v10.*-mocha` tag and no `v10.x-release` branch exists yet in the fetched refs (`git tag -l 'v10*'`, `git branch -r`). Prior pattern in the tag list: `v9.0.0-arabica`, `v9.0.0-mocha`, `v9.0.1-mocha`, …, `v9.0.4`. So expect something like `v10.0.x-mocha`, cut from a `v10.0.x-release` branch off main; **unknown** which commit until it is tagged.

---

## 10. What the observer must expect on the wire (probe sequence)

1. **Discover endpoints.** gRPC `celestia.valaddr.v1.Query/AllBondedFibreProviders` (or `GET /valaddr/v1/all-bonded-fibre-providers`) → `providers[]{validator_consensus_address (celestiavalcons…), info.host ("host:port")}`; only bonded validators are returned (`x/valaddr/keeper/grpc_query.go:37-61`). Per-validator refresh via `FibreProviderInfo`. The observer already does this over ABCI query path `/celestia.valaddr.v1.Query/AllBondedFibreProviders` (`fibre-sentinel/internal/scan/chain.go:174-193`).
2. **Get the validator set at the promise height** (consensus pubkeys + voting power; the reference uses the CometBFT gRPC Block API `ValidatorSet`, `specs/src/fibre_server.md:148`, `fibre/internal/grpc/set_getter.go`). Map `validator_consensus_address` ↔ consensus pubkey (`sdk.ConsAddress(val.Address)`, `host_registry.go:136`). Expect the heavy-RPC limiter on public nodes running core v0.41.0 (§8).
3. **Compute the assignment** with `Assign(commitment, TotalRows=16384, OriginalRows=4096, MinRows=148, Liveness=1/3)` (`fibre/validator/set.go:65-105`) to know which row indices each validator must return.
4. **Dial `host:port` with TLS 1.3**, `InsecureSkipVerify` + custom `VerifyConnection`: parse leaf cert, find extension OID `1.3.6.1.4.1.66463.1.1`, run the 14 checks of §4 against the expected consensus pubkey and chain ID (`fibre/internal/tlsid/tlsid.go:267-366`; `fibre-tlsverify`). No SNI/SAN/hostname binding. Expect connection setup to complete within 15 s or be dropped (`internal/grpc/server.go:33`), and the server to accept at most 16 concurrent connections in total (`:28`).
5. **`celestia.fibre.v1.Fibre/DownloadShard`** with `DownloadShardRequest{blob_id = blob_version(1 byte, =0) || commitment(32)}` (33 bytes). Standard protobuf encoding; the default `proto` content-subtype is accepted (§4). Responses: `OK` with `shard{rows[]{index, data, proof[14 × 32-byte hashes]}, rlcs (65,536 bytes)}`; `NOT_FOUND` "no blob shard found for commitment …" if pruned/never stored; `INVALID_ARGUMENT` for a bad ID or version ≠ 0; `INTERNAL` on store read failure; `RESOURCE_EXHAUSTED` is *not* a download code (`server_download.go:27-58`).
6. **Verify rows.** (a) all rows same non-zero size, `len(rlcs) == 4096·16` (`fibre/client_download.go:247-282`); (b) feed `RowProof{Index, Row, Proof}` + RLC vector to `rsema1d.Reconstructor.Add` for the commitment — this checks Merkle row proofs and the commitment/RLC relationship (`fibre/download.go:126-145`; `specs/src/fibre_client.md:410,415`); (c) check the returned index set equals this validator's assignment: same count, all assigned, no duplicates (`ShardMap.Verify`, `set.go:187-215`). The observer's prober already does (a)-(c) (`fibre-sentinel/internal/probe/probe.go:264-330`). Row size ≤ 32,768 (`download.go:137-139`).
7. **Timing.** Rows must be available from settlement until `creation_timestamp + max(payment_promise_timeout, shard_retention)` (§7); after that, `NOT_FOUND` within roughly two prune minutes is normal.

---

## 11. Briefing claims checked against source

| Claim | Verdict | Source |
|---|---|---|
| "Missing TLS is only on the signer link (`priv_validator_grpc_laddr`); client-facing upload/download gRPC supports TLS" | **Partly wrong.** Two links lack TLS, not one: the signer link **and** the app-node gRPC link (`--app-grpc-address`, default `127.0.0.1:9090`). And the client-facing link does not merely "support" TLS — it is **TLS-only** (TLS 1.3 minimum, no plaintext fallback, no negotiation). | `specs/src/fibre_server.md:121,131 @pin`; `fibre/cmd/README.md:9 @pin`, `:139-155 @main`; `fibre/server.go:129-132 @pin` |
| "Blob size on Fibre: 256 KiB min, 128 MiB max" | **Right only for the promise's `blob_size` (padded upload size); wrong as a limit on input data.** Min padded upload = MinRowSize 64 × 4096 rows = 262,144 B = 256 KiB; max `MaxBlobSize` = 128 MiB *including* the 5-byte header, so max user payload = 128 MiB − 5 B; min user payload = 1 byte. No code path rejects a 1-byte blob. | `fibre/protocol_params.go:68-69,173-190 @pin`; `fibre/blob.go:106,120-124,158-163 @pin`; `pkg/rsema1d/field/leopard.go:3-6 @pin` |
| "Retrieval needs 1/3 of the validator set honest" | **Imprecise.** The parameter is `LivenessThreshold = 1/3` of **voting power** (stake), not 1/3 of validators by count; assignment is sized so that any 1/3 of stake holds ≥ 4096 original-row-equivalents, and reconstruction needs 4096 unique verified rows (1/4 of 16384). Overlap/wrap-around and the 148-row floor make the stake condition approximate, which is why the spec chose a 1/4 row threshold under a 1/3 stake target. `fibre.md:3` separately says "honest majority assumptions". The 2/3 figure is the signing (safety) threshold, not retrieval. | `fibre/protocol_params.go:45-47,66 @pin`; `fibre/validator/set.go:48-53,107-113 @pin`; `specs/src/fibre_encoding.md:51 @pin`; `fibre/download.go:215-232 @pin`; `specs/src/fibre.md:3` |
| "Fibre server runs on the same machine as the validator in phase 1" | **Roughly supported, not literally.** No "phase 1" language exists in the repo. The operator doc requires a `celestia-appd` node "on the same host (or a trusted host-local network)" because the app and signer links are plaintext, and goreleaser calls fibre "a separate binary that validators run alongside celestia-appd"; main's README explicitly allows "a separate server" over "its private IP or a closed network connection". The binary is separate (`fibre`, built from `./fibre/cmd`), not embedded in `celestia-appd`. | `fibre/cmd/README.md:9 @pin`, `:152-153 @main`; `.goreleaser.yaml:252-258 @pin`; `Makefile:429-434 @pin` |

Additional contradictions found in the source itself (spec vs code), useful because the observer cites specs:
- `specs/src/fibre_registry_module.md` documents `AllFibreProviders` / `/valaddr/v1/all-fibre-providers` (unfiltered); the proto/keeper/README implement `AllBondedFibreProviders` / `/valaddr/v1/all-bonded-fibre-providers` (bonded-only) (§3).
- `specs/src/fibre_server.md:222` says the server never returns `ResourceExhausted` or backoff hints; `fibre/server_upload.go:104-109` does, on storage-budget exhaustion (upload path only).
- `specs/src/fibre_server.md:90-110` lists a `ServerConfig` without `MaxShardSize`, `OriginalRows`, `UnlimitedBudget`; the code has them (`fibre/server_config.go:30-79`).
- `fibre/cmd/README.md:88 @pin` says the config file is `$FIBRE_HOME/server_config.toml`; code writes `$FIBRE_HOME/config/server_config.toml` (`fibre/server_config.go:25-26`).

---

## 12. Packaging of the `fibre` binary (for completeness)

`.goreleaser.yaml @pin`: builds `fibre-{darwin,linux}-{amd64,arm64}` from `main: ./fibre/cmd`, `binary: fibre`, `CGO_ENABLED=1` "to match celestia-appd" (`:252-327`); archived as `fibre_{Os}_{x86_64|arm64}.tar.gz` (`:364-377`) with `checksums.txt` (`:378-379`); version stamped from the release tag and full commit (`:270-273`, `Makefile:34-46`). Every celestia-app release therefore ships a matching `fibre` archive (`fibre/cmd/README.md:20-24 @pin`); Linux archives need glibc ≥ 2.34 (`:39-42`).

---

## 13. Unknowns

| Item | What would resolve it |
|---|---|
| Exact prune-index bucketing / maximum lag between `pruneAt` and deletion | Read `fibre/store.go` `PruneBefore` and `pruneKey` at the pin |
| Whether the server rejects an uploaded row size that is not a multiple of 64 (affects the true minimum `blob_size` a *non-reference* uploader could get signed) | Read `pkg/rsema1d` `Verifier.Verify` row-size checks |
| The commit/tag a mocha release will be cut from | Wait for a `v10.*-mocha` tag or `v10.*-release` branch; re-run the §8 diff against it |
| Whether ADR-030 (object storage) lands before mocha and whether its implementation touches `Store.Get` semantics observable via `DownloadShard` latency | Watch PRs referencing ADR 030 / `fibre/store.go` |
| Behaviour of a stock gRPC client against the forced `fibre-proto` server codec on a non-devnet build | Already evidenced on devnet by the observer's 27 `SERVED_OK` samples; a mocha probe run would confirm on the real network |
