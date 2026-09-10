# talon-sdk-go

Go bindings for the Talon embedded database engine.

## Remote Server client without cgo

Server-only applications should import the dedicated pure-Go package. It does
not compile or link the embedded database or native Talon library, and is
verified in CI with `CGO_ENABLED=0`:

```go
import (
    "time"

    server "github.com/darkmice/talon-sdk-go/server"
)

client, err := server.NewServerClient(server.ServerClientConfig{
    BaseURL: "https://talon.internal.example",
    Token:   token,
    Timeout: 10 * time.Second,
})
if err != nil {
    return err
}
defer client.Close()

health, err := client.Health(ctx)
```

The package owns the typed HTTP contracts for health, KV set/get/delete/
exists/setnx, conditional transaction v2, exact receipt lookup, conditional
point-read v1, bounded same-snapshot read v1, and leased conditional prefix-scan
v1. The root package keeps the existing `talon.NewServerClient` API as a
compatibility wrapper, but importing the root package still includes the
embedded DB/cgo surface.

Prefix scans use a stable request ID and an opaque, node-local continuation
cursor. Continue only through the sealed request; `cursor_unavailable` means
the caller must abandon that scan and begin a replacement scan with a new
request ID. The client never silently falls back to a fresh snapshot:

```go
scan, err := server.NewConditionalPrefixScanRequest(
    "outbox-worker-scan-1",
    "tenant-outbox",
    []byte("pending/"),
    128,
    &requiredRevision,
)
if err != nil {
    return err
}
page, err := client.ConditionalPrefixScan(ctx, scan)
if err != nil {
    return err
}
next, ok, err := page.Continuation(scan)
```

Revision-stream operations are not part of the Server HTTP client. They remain
embedded-DB APIs until Talon defines and releases a separately versioned HTTP
transport contract; the Server package does not emulate them with repeated
point or snapshot reads.

## Security boundary

Production native code is not selected from a tag, `latest` URL, linker search
path, or the historical headers/libraries under `include/` and `lib/`. Those
files are retained only for source compatibility and are not trust roots or
runtime fallbacks. `Open` requires an out-of-band trust policy and a
`talon-native-manifest-v1` offline bundle. It verifies, in order:

- strict manifest JSON (duplicate and unknown fields are rejected);
- release channel/tag and full `talon-bin` commit;
- Ed25519 signature, pinned key ID, and SHA-256 of the DER public key;
- full Core repository/tag/commit/Cargo semver;
- ABI profile/version/header SHA-256 and the exact SDK symbol set;
- platform, target triple, runner, archive/member names, sizes, and SHA-256;
- SBOM, license inventory, Core license, NOTICE, and signed feature gates.
- the loaded Core's bounded self-manifest, clean source identity, build binding,
  features, capabilities, and every required runtime symbol.

Only after all checks pass is the verified dynamic library copied to a private
directory and loaded with `dlopen`. Missing policy or missing artifacts fail
closed. The public key shipped beside an artifact is not a trust root.

The native bundle contract covers `darwin/amd64`, `darwin/arm64`,
`linux/amd64`, and `linux/arm64`. Each maps to one fixed manifest platform,
target triple, runner label, archive shape, and dynamic-library name; other
platforms fail closed before bundle access.

## Open

Applications may pass a `NativePolicy` directly with `OpenWithOptions`, or use
`Open`, which reads the following required environment variables:

```text
TALON_NATIVE_BUNDLE_DIR
TALON_NATIVE_PUBLIC_KEY_FILE
TALON_NATIVE_EXPECTED_KEY_ID
TALON_NATIVE_EXPECTED_KEY_SHA256
TALON_NATIVE_EXPECTED_RELEASE_TAG
TALON_NATIVE_EXPECTED_TALON_BIN_COMMIT
TALON_NATIVE_EXPECTED_CORE_REPOSITORY
TALON_NATIVE_EXPECTED_CORE_TAG
TALON_NATIVE_EXPECTED_CORE_COMMIT
TALON_NATIVE_EXPECTED_CORE_VERSION
TALON_NATIVE_EXPECTED_ABI_PROFILE
TALON_NATIVE_EXPECTED_ABI_VERSION
TALON_NATIVE_EXPECTED_HEADER_SHA256
```

`TALON_NATIVE_REQUIRED_CAPABILITIES` is an optional comma-separated list. A
required signed feature that is gated, or not implemented by this SDK version,
prevents startup. Recognized values are `storage_conditional_batch_v1`,
`storage_conditional_point_read`, `storage_conditional_snapshot_read`, and
`revision_stream`. The point-read requires Core's
`storage_conditional_point_read_v1`; same-snapshot multi-key reads require
`storage_conditional_snapshot_read_v1`; revision streams require both
`revision_stream_v1` and `revision_stream_v2_mmr_proof` from the artifact-bound
runtime self-manifest.

```go
db, err := talon.Open("path/to/db")
if err != nil {
    return err
}
defer db.Close()
```

## Parameterized SQL and exact values

`Query` and `Exec` use Core's binary parameter ABI. SQL is never assembled by
interpolation and callers must construct an exact Talon `Value`:

```go
name, err := talon.TextValue(userInput)
if err != nil {
    return err
}

if err := db.Exec(
    "INSERT INTO users (id, name) VALUES (?, ?)",
    talon.IntegerValue(42),
    name,
); err != nil {
    return err
}

rows, err := db.Query("SELECT id, name FROM users WHERE id = ?", talon.IntegerValue(42))
```

Rows retain their wire kinds. Integer and float accessors do not coerce each
other. Unknown tags, non-canonical booleans, invalid UTF-8/JSON, non-finite
numbers, oversized vectors, truncated payloads, impossible shapes, and trailing
bytes are rejected as protocol violations.

Decimal values use an exact signed i128 coefficient plus retained scale. Both
precision and scale are limited to 38:

```go
price, err := talon.DecimalValue(big.NewInt(12345), 2) // 123.45
```

## Capability and error gates

SDK-originated errors have a stable `ErrorCode`, available through
`ErrorCodeOf`. A released bundle must self-attest `native_error_codes_v1` before
the SDK will load it. Known Core codes are mapped to stable SDK codes; unknown
but well-formed codes remain `native_unclassified` and are available through
`NativeCodeOf`. Diagnostic text is never parsed to infer conflict, missing data,
or retryability.

The SDK exposes a strong `ConditionalTransaction` v2 API with typed conditions,
closed put/delete/increment mutations, durable replay receipts, and
`ConditionalTransactionReceipt` recovery after an indeterminate
acknowledgement. Request-ID rebinding is a `CodeNativeConflict`; a failed
predicate/counter operation is a non-error result with `Rejected == true`; and
a possibly-applied acknowledgement is `CodeResultIndeterminate`. Revisions,
quorum index/term, and i64 observations are accepted only as canonical decimal
strings and parsed losslessly—never through `float64` or `map[string]any`.
The loaded Core must also attest `conditional_transaction_command_digest_v1`;
transaction-v2 alone is not treated as proof of digest compatibility.

```go
request, err := talon.NewConditionalTransactionRequest(
    "billing",
    "quota-charge:account-42:attempt-1", // stable for this exact payload
    []talon.ConditionalTransactionCondition{{
        Key:      []byte("account-42/status"),
        Expected: []byte("active"),
        Operator: talon.CompareEqual,
    }},
    []talon.ConditionalTransactionMutation{
        talon.ConditionalIncrement([]byte("account-42/usage"), 1),
    },
)
if err != nil {
    return err
}
result, err := db.ExecuteConditionalTransaction(request)
var receipt *talon.ConditionalTransactionReceipt
if err != nil && talon.ErrorCodeOf(err) == talon.CodeResultIndeterminate {
    // Recovery requires the same sealed namespace, conditions, and mutations;
    // a request ID by itself is not accepted as transaction identity.
    lookup, lookupErr := db.ConditionalTransactionReceiptFor(request)
    if lookupErr != nil {
        return lookupErr
    }
    if lookup.Status == talon.ConditionalReceiptIndeterminate {
        return fmt.Errorf("transaction outcome is still indeterminate")
    }
    receipt = lookup.Receipt
} else if err != nil {
    return err
} else {
    if result.Rejected {
        // A false condition or invalid/overflowing counter is a durable result.
        return fmt.Errorf("transaction rejected: %s", result.Receipt.Conflict.Kind)
    }
    receipt = &result.Receipt
}
if receipt.Outcome == talon.ConditionalOutcomeConflict {
    // The receipt lookup recovered the same durable rejection.
    return fmt.Errorf("transaction rejected: %s", receipt.Conflict.Kind)
}
```

Receipt lookup returns the durable receipt rather than reconstructing every
outer convenience field; applications should branch on `Receipt.Outcome` after
recovery. A found receipt is accepted only when its command hash, namespace,
ordered conditions/operators/expected values, ordered mutation kinds/keys/
values/deltas, observations, and quorum result all match the sealed request.
Reusing the request ID with the identical payload is safe and returns
`Duplicate == true`. Reusing it with different conditions or mutations is a
conflict and is never an automatic retry strategy.

`CodeNativeNotFound` from receipt lookup is not proof that the transaction was
never executed, so it does not authorize rebinding the request ID or retrying a
different payload. `ConditionalIncrement` is an exact signed i64 counter
operation; monetary values that require decimal precision must remain canonical
bytes and must not be converted to i64 or `float64` for this API.

### Conditional point reads

`ConditionalGet` is the typed read surface for conditional namespaces.
It verifies the echoed namespace, key, nullable required revision, observed
revision, local snapshot revision, exact absent/empty/value state, and Core's
response digest. A required revision is a lower bound: Core returns
`CodeSnapshotNotAvailable` if the local snapshot has not applied it. It is not
an exact historical read, and `SnapshotRevision` is not a portable fencing
token. Passing no required revision may return stale follower state.

```go
requiredRevision := receipt.Revision
readRequest, err := talon.NewConditionalPointReadRequest(
    "billing",
    []byte("account-42/usage"),
    &requiredRevision,
)
if err != nil {
    return err
}
point, err := db.ConditionalGet(readRequest)
if err != nil {
    return err
}
if !point.Found {
    return fmt.Errorf("counter is absent at revision %d", point.ObservedRevision)
}
// point.Found with len(point.Value) == 0 is an existing empty value.
```

The capability is versioned as `storage_conditional_point_read` v1 and also
requires the `storage_conditional_point_read_v1` feature. A gated or incomplete
Core identity fails before native execution; the SDK never falls back to legacy
`storage/get`.

Each call verifies one key from one snapshot. Neither `ObservedRevision` nor
`SnapshotRevision` can be fed into later calls to claim a multi-key exact
snapshot. Consumers must not loop over latest point reads and present the
combined values as atomic. This API also does not prove range absence. Exact
historical multi-key reads, range-absence predicates, or transactionally
discovered read-sets require separate versioned Core wire contracts; the SDK
does not simulate them with read-before-write, process locks, or repeated
`setnx` calls.

### Conditional same-snapshot multi-key reads

`ConditionalSnapshotGet` reads 1–128 unique keys from one local MVCC snapshot.
It preserves request order, distinguishes absent keys from existing empty
values, verifies every echoed key and observation index, and authenticates the
complete response with `response_sha256`. It is still a bounded snapshot
observation: the revision is a lower bound and `SnapshotRevision` is local
metadata, not a portable fence or historical-read selector.

```go
snapshotRequest, err := talon.NewConditionalSnapshotReadRequest(
    "billing",
    [][]byte{[]byte("receipt"), []byte("association"), []byte("outbox")},
    &requiredRevision,
)
if err != nil {
    return err
}
snapshot, err := db.ConditionalSnapshotGet(snapshotRequest)
```

The capability is versioned as `storage_conditional_snapshot_read` v1 and the
SDK fails closed while Core reports it as gated. The contract does not provide
arbitrary historical reads, range absence, or a transactional dynamic read
set; callers must derive any later key set and validate the returned receipt
identity inside the verified snapshot.

### Authenticated immutable revision stream v2

The SDK also exposes Core's immutable revision-stream contract as closed,
typed requests. `NewRevisionStreamAppendRequest` derives the next revision from
the caller-supplied expected head; it never reads state, sorts events locally,
or emulates CAS. An append and its side facts share one Core conditional
transaction and one durable receipt.

Every v2 head carries a canonical MMR checkpoint. The SDK deterministically
rebuilds the content, leaf, peak, proof-root, head-root, and every internal
Merkle-node mutation before it accepts the transaction receipt. A proofless v1
head cannot be extended through the typed API.

```go
appendRequest, err := talon.NewRevisionStreamAppendRequest(
    "event:tenant-42:1", // stable for this exact append
    "tenant-facts",
    []byte("tenant-42/project-7"),
    previousHead, // nil only for revision 1
    occurredAtUnixNanos,
    eventPayload,
    []talon.ConditionalTransactionCondition{{
        Key:      []byte("project-7/status"),
        Expected: []byte("active"),
        Operator: talon.CompareEqual,
    }},
    []talon.ConditionalTransactionMutation{
        talon.ConditionalIncrement([]byte("project-7/event-count"), 1),
    },
)
if err != nil {
    return err
}

appendResult, err := db.RevisionStreamAppend(appendRequest)
if err != nil && talon.ErrorCodeOf(err) == talon.CodeResultIndeterminate {
    lookup, lookupErr := db.RevisionStreamAppendReceipt(appendRequest)
    if lookupErr != nil {
        return lookupErr
    }
    if lookup.Status == talon.ConditionalReceiptIndeterminate {
        return fmt.Errorf("append outcome is still indeterminate")
    }
    if lookup.TransactionReceipt.Outcome == talon.ConditionalOutcomeConflict {
        return fmt.Errorf("append was rejected")
    }
    if lookup.StreamReceipt == nil {
        return fmt.Errorf("applied append omitted its authenticated stream receipt")
    }
} else if err != nil {
    return err
} else if appendResult.Rejected {
    return fmt.Errorf("append rejected: %s", appendResult.TransactionReceipt.Conflict.Kind)
}
```

Time-range queries return both the immutable pinned head and the currently
observed live head. Continue only with the opaque continuation emitted by the
verified page; it preserves the original stream, time window, pinned head,
cursor, previous root, and the minimum observed live head across pages.

The pinned proof authenticates revision-stream entries only. It does not add
fixed-snapshot observations of side namespaces; consumers must wait for a Core
wire contract that explicitly returns and authenticates those observations.

```go
request, err := talon.NewRevisionStreamQueryRequest(
    []byte("tenant-42/project-7"), fromUnixNanos, untilUnixNanos, 100,
)
if err != nil {
    return err
}
for {
    page, err := db.RevisionStreamQuery(request)
    if err != nil {
        return err
    }
    consume(page.Entries)
    if page.Continuation == nil {
        break
    }
    request, err = page.Continuation.NextRequest(100)
    if err != nil {
        return err
    }
}
```

All 64-bit wire integers are canonical decimal strings. The SDK rejects
unknown/duplicate/trailing JSON, invalid UTF-8, scope/order/count drift,
non-contiguous pages, moving continuations, live-head rollback, non-canonical
MMR peaks/inclusion paths, window/page/head substitution, and inconsistent
command, receipt, quorum-result, payload, content, proof-root, or head-root
digests. `snapshot_not_available` and
`corrupt_stream` map to `CodeSnapshotNotAvailable` and `CodeCorruptStream`.

The API is intentionally present before production admission so consumers can
compile against the contract. It still calls `RequireCapability("revision_stream")`
first. The current Core self-manifest reports that capability as `gated`, so
both append and query return `CodeCapabilityUnavailable` and do not execute
native operations until Core reports version 2 as `available` and attests both
revision-stream features. The external signed manifest deliberately retains its
v1 `gates` shape; proof admission comes from the artifact-bound Core
self-manifest rather than an invented external gate.

The Core worktree now advertises `native_conditional_transaction_v2`. The
`talon-bin` packaging generator requires binary self-attestation and covers all
four platform targets, but its current release lock is still `UNRELEASED`, pins
an obsolete ABI declaration, keeps runtime attestation gated, and has no
production signing identity.
Production startup therefore remains fail-closed until the lock is advanced
atomically and a clean signed bundle exists. The SDK does not emulate this
surface with process locks, read-then-write, multiple `setnx` calls, or SQL
interpolation.

## License

See `LICENSE`.

## Parameterized SQL

Use `ExecParams` and `QueryParams` for positional `?` parameters. Values are
encoded through the native `talon_execute` JSON `bind` path; SQL text is never
constructed by interpolation. `Query` and `QueryParams` reject unknown or
malformed Talon Value tags, while the original `SQL` method remains available
for callers that need the legacy raw result shape.

The exact bundled native artifacts, platforms, ABI header hash and provenance
boundary are recorded in [native-manifest.json](native-manifest.json).

General conditional KV transactions are intentionally not emulated in this
SDK. `KvSetNX` retains its existing single-key semantics; it is not a substitute
for multi-key compare-and-swap. Consumers that need conditional transactions
must wait for a clean pinned Core revision and complete native matrix.
