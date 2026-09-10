# Conditional transaction v3 / compact_receipt_v1 draft

Status: SDK compile-ahead draft. The Core contract is not released or remotely
versioned as of 2026-09-10. This document records the exact assumptions isolated
by this SDK branch; it is not a production compatibility claim.

## Fixed by this SDK draft

- Transaction request `version` is the JSON number `3`.
- Capability admission requires all of:
  - capability `native_conditional_transaction_v3`, version 3, status
    `available`;
  - feature `native_conditional_transaction_v3`;
  - feature `compact_receipt_v1`;
  - feature `conditional_transaction_command_digest_v1`.
  - capability limits exactly `max_value_bytes=8388608`,
    `max_aggregate_command_bytes=33554432`, and
    `max_compact_receipt_bytes=16777216`, with unrelated limit fields absent.
- `command_sha256` is SHA-256 over the existing `TLNCTX2` binary domain with
  the embedded little-endian request version set to 3. It continues to bind the
  namespace, ordered conditions, operators, expected bytes, mutations, put
  bytes, and increment deltas, while excluding `request_id`.
- Each compact value observation is:

  ```json
  {"present":true,"byte_length":8388608,"sha256":"<64 lowercase hex>"}
  ```

  An absent value is exactly `{"present":false,"byte_length":0}` with
  `sha256` omitted. A present empty value has `byte_length: 0` and the SHA-256
  of empty bytes, so it remains distinct from absence.
- Every v3 observation retains `before:null` and `after:null`, and requires
  `before_compact` and `after_compact`. Conditions require `matched`; mutations
  must omit it. Every increment observation, including an unexecuted mutation
  after an earlier conflict, must carry paired canonical decimal-string
  `before_i64` and `after_i64` evidence when its initial value is absent or an
  eight-byte counter. Both fields are omitted only for a present non-eight-byte
  value, which is deterministic `counter_invalid` evidence. This mirrors the
  local Core working draft's receipt constructor and remains a pre-merge
  contract item.
- `receipt_sha256` is SHA-256 of the canonical JSON receipt after replacing its
  own value with the empty string. A quorum receipt's `result_sha256` binds the
  canonical compact receipt including the real `receipt_sha256`.
- The transaction receipt's `command_sha256` and the quorum receipt's
  `command_sha256` are deliberately different hash domains. The former is the
  `TLNCTX2` semantic digest above. The latter is SHA-256 of the exact Core
  `ConsensusCommand` v1 bytes (`TLNQ` envelope, request ID, and sole
  `StorageConditionalBatch` operation). The SDK recomputes both from the same
  sealed request.
- Receipt lookup remains exact-request recovery: the returned version, request
  ID, command digest, revision, item identity/order, compact observations,
  outcome/conflict, receipt digest, and optional quorum digest are all checked.

## Fail-closed validation

The SDK does not accept compact receipts as weaker v2 receipts. It verifies:

- version 3 and the complete capability/feature tuple;
- lowercase SHA-256 and absent/empty/present invariants;
- top-level `result`, `original_result`, `applied`, and `duplicate` consistency;
- request ID and full command-digest binding on execution and response-loss
  lookup;
- condition/mutation count, order, index, keyspace, and key identity;
- put/delete/increment after-state summaries and counter delta/overflow evidence;
- the first deterministic conflict kind/index, in condition order followed by
  increment-mutation order, and no-state-change summaries;
- canonical `receipt_sha256` and quorum `result_sha256`.

`eq` and `ne` are the only admitted condition operators. A digest can prove
equality to known expected bytes, but it cannot prove `lt`, `le`, `gt`, or `ge`
ordering. Those operators remain rejected until Core publishes independently
verifiable order evidence or narrows its own v3 admission contract.

## Bounds

- Per expected/put value: 8 MiB.
- Aggregate binary conditional command: 32 MiB.
- Compact receipt: 16 MiB.
- v3 JSON request: 40 MiB. This is enough for one 8 MiB all-`0xff` octet array
  plus the native command envelope. The general SDK request bound remains
  32 MiB.
- Native JSON response: unchanged at 16 MiB. Compact observations solve the
  terminal-value expansion; the SDK does not conceal the issue by increasing
  this limit.

The three capability limits cover conditional-value, binary-command, and
compact-receipt semantics. They do not attest the C JSON entry point's maximum
request size. The 40 MiB SDK request allowance therefore remains a draft
transport assumption that requires native E2E evidence before merge.

## Core dependency before release

Core must still freeze and publish:

1. version/feature/capability names, exact limits, limits build-binding
   serialization, and coexistence with v2;
2. the compact field names, absent encoding, canonical command/receipt hashes,
   and lookup replay shape;
3. v3 admission and persisted-receipt construction in the same native
   transaction as user state;
4. cross-language golden vectors for semantic-command, consensus-command,
   receipt, and limits build-binding hashes, plus standalone/quorum conformance
   for applied, conflict, duplicate, response loss, 8 MiB high-byte values, and
   one request ID rebound to a different command;
5. clean signed native artifacts and a complete release matrix.

If Core changes any item above, update the constants and wire adapters in
`conditional_compact.go`; do not silently reinterpret the v2 public types or
relax their existing verification.
