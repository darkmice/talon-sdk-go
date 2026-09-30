# GoFrame gredis native integration contract

GoFrame v2.8.3 `gredis.Adapter` requires eight command groups: Generic,
String, Hash, List, Set, SortedSet, PubSub, and Script. A native Talon adapter
must implement each group against Talon Core through the signed embedded ABI.
It must not connect to Talon's RESP server or use PgWire.

The Core KV engine stores byte values with a TTL header. Its legacy JSON
route may label non-UTF-8 bytes separately, but the versioned `talon_kv_read_v1`
ABI reports every present KV value as Redis `string`; it has no collection
type namespace. A separate Core worktree implements that native read subset
for GET, MGET, EXISTS, TYPE, and whole-second TTL. Its source self-manifest
marks `native_kv_read` v1 as available and `native_goframe_gredis` v1 as gated.
The SDK has matching binary-safe methods and preserves the previous signed
ABI symbol set, but a signed new ABI artifact has not been admitted.
Consequently, a `gredis`
implementation that merely forwards those commands would compile but would
not satisfy GoFrame's Redis semantics.

The locked GoFrame v2.8.3 source declares 111 group methods: Generic 23,
String 19, Hash 14, List 17, Set 15, SortedSet 14, PubSub 3, and Script 6.
`Do`, `Conn`, `Close`, and the connection's subscription/receive operations
are additional requirements.
The native read subset covers only five commands; it cannot satisfy any group
interface in full.

The Core Draft branch also has an internal, feature-gated List foundation:
LPUSH, LPUSHX, RPUSH, RPUSHX, LPOP, RPOP, LLEN, LINDEX, and LRANGE. It shares
typed key ownership and expiry with Hash, uses generation-scoped ordered item
keys, and has an explicit bounded stale-item reclaimer. These methods are Rust
internals only. No native command ABI or Go `gredis.Adapter` calls them, and
the reclaim sweep has no runtime scheduler. This does not complete GoFrame's
17 List methods or change the gated capability status.

The Core contract needed for the adapter is:

1. A single typed key namespace with atomic type checks, Redis-compatible
   missing-key and wrong-type responses, and millisecond expiry shared by all
   collection types. Collection operations must update data, TTL, and the
   replication oplog atomically.
2. Versioned native command requests and typed replies for all methods in the
   eight GoFrame groups. Command validation must happen before writes. The Go
   SDK must fail closed when the loaded signed Core does not attest the
   capability version.
3. Connection-scoped subscriptions with cancellation and bounded queues for
   PubSub, including pattern subscriptions. Blocking list pops must observe
   deadlines and wake on writes from other native handles.
4. Script loading, SHA lookup, evaluation, and cancellation with an explicit
   atomicity and resource-limit contract. `EVAL` cannot be mapped to an
   arbitrary sequence of unlocked KV calls.
5. Differential tests for return values, ordering, TTL, wrong-type errors,
   concurrent writers, reconnect, and restart, plus GoFrame API tests using
   each group and a signed native test artifact.

## Performance admission

Talon's embedded performance is an adapter requirement. Adding command
coverage alone does not admit `native_goframe_gredis`. Before enabling it,
benchmark the same pinned Core build through both the Rust engine and the Go
native ABI, then through representative GoFrame calls. Report throughput,
p50/p99 latency, allocations per operation, and memory use for hit/miss GET,
SET, Hash operations, expiry, and mixed read/write loads at 1, 4, and 8
threads. Include Primary replication and a large-key distribution; keep
setup, signing, loading, and GoFrame initialization outside the operation
timers. Record the hardware, build profile, dataset, concurrency, and raw
runs so regressions can be reproduced.

The experimental Core foundation has a measured extra metadata lookup on
string GET misses. Its standalone diagnostic is not evidence for Go SDK or
GoFrame performance, and aggregate throughput on one development machine
does not clear the latency gate. Set acceptance budgets from representative
consumer workloads before changing the storage layout or admitting the
capability. A new native command ABI should preserve binary arguments and
typed replies without per-command JSON serialization.

The `gdb` driver and its `native_sql_result` v2 protocol are independent of
this missing `gredis` capability. They must not be advertised as a complete
Redis adapter.
