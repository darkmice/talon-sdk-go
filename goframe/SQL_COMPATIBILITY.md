# GoFrame native SQL compatibility

The signed-artifact baseline below retains its original evidence. A separate
2026-10-04 dirty Core development candidate passes read-your-writes, restart,
exact-value and single-owner contracts through explicit local development
admission; see [the development handoff](../LOCAL_DEVELOPMENT_HANDOFF.zh-CN.md).
That candidate is not a released or production-admitted Core.

This matrix describes the GoFrame v2.8.3 `gdb` adapter against an embedded,
signed Talon Core. It is based on `TestLocalSignedCoreGoFrameInterop`, invoked
through `TestLocalSignedCoreKVInterop` with an ephemeral test signature and
clean Core commit `a968c4db18d1f14b51d9d4b2e4f55ec5a642e72c`. It is local
interoperability evidence, not a released Core or production performance claim.
The matrix covers the listed statement shapes only. The stricter consumer
contracts below currently fail on both that local Core and signed go-runtime
v0.1.54; the matrix does not establish complete transaction correctness or
durable COMMIT.

| SQL workload | Verified through GoFrame gdb and signed Core |
| --- | --- |
| Schema discovery | `Tables`, `TableFields` |
| Create and write | `CREATE TABLE`, `CREATE INDEX`, parameterized `INSERT`, model single and batch `Insert`, `Save` upsert, `Update`, `InsertIgnore`, `Delete` |
| Read and predicates | `One`, `All`, `Count`, `AllAndCount`, indexed equality, `IN`, parameterized `IN (SELECT ...)`, `BETWEEN`, `LIKE`, `OR`, `IS NULL`, `IS NOT NULL` |
| Result shaping | projection, `DISTINCT`, ascending and descending order, page/limit/offset |
| Relational and aggregate | aliased `LEFT JOIN` and filtered `INNER JOIN`, `GROUP BY`, parameterized `HAVING`, `COUNT`, `SUM`, `AVG`, `MIN`, `MAX` |
| Exact value | `DECIMAL(18,2)` write and readback without a float round trip |
| Transaction | begin, rollback, commit, and same-process readback of the tested single-PK shapes; secondary-index read-your-writes and durable restart remain failing contracts |
| Independent native sessions | four physical handles on one directory, peer read/write visibility, Busy during an explicit transaction, rollback and commit |
| Constraints and DDL | composite primary key, NOT NULL, unique index, `CREATE TABLE IF NOT EXISTS`, selected `ALTER TABLE` add/drop/type/unique operations, foreign key rejection of an absent parent, and TIMESTAMP/BOOLEAN/VARCHAR/BIGINT declarations |
| Additional query shapes | three-table JOIN, `EXISTS`, `UNION`, CTE, window function, indexed `IN` result and `EXPLAIN`, and DECIMAL comparison/order with an exact `talon.DecimalValue` parameter |

The native driver also has unit coverage for SQL result metadata, exact DECIMAL
conversion, time parameters, generated insert ID handling, and SQL variant
formatting. Those tests use a fake native database and do not establish the
same signed Core interoperability as the matrix above.

The additional shapes passed in the same signed-Core fixture. The fixture does
not execute the SaaS schema documents or a released Server binary. JSON and
date expressions remain outside this GoFrame matrix. A specific consumer
workload should add its generated SQL to the live test and run it before
claiming compatibility.

## Consumer acceptance contracts (2026-10-04)

`TestNativeSQLConsumer*` runs the same bound SQL through direct binary native
SQL, native v2 result SQL, and GoFrame. These tests assert healthy behavior and
fail on wrong answers; reproducing a known defect is not a passing acceptance
result. They are opt-in with `TALON_TEST_EMBEDDED_NATIVE=1`, and also run in the
local signed fixture. Signature/load failures are fatal when enabled.

| Contract | Signed go-runtime v0.1.54 / Core `6010d748aebd3c4e595534ed4a9e959e7e5334cf` |
| --- | --- |
| Composite PK INSERT read-your-writes via secondary-index equality | **FAIL** in all three routes; full scan sees the pending row, index probes do not; EXPLAIN confirms the index path |
| Single-PK point read-your-writes, DELETE then rollback | PASS for the tested shapes |
| Projection DISTINCT versus COUNT(DISTINCT) | Projection returns two operators; aggregate incorrectly returns three: **FAIL** in all three routes |
| COMMIT, immediate process exit without Close, new-process read | **FAIL** in all three routes; lost row or schema; clean Close control passes |
| Typed DECIMAL, int64 extrema, INTEGER nanos | PASS: exact parameters, transaction readback, commit and Close/reopen; no float round trip |
| Directory transaction owner and outer-db callback escape | PASS: second BEGIN and peer/outer SELECT/INSERT/UPDATE/DELETE rejected with typed `busy`, no side effects, owner released after rollback |

The clean locally signed Core `a968c4db18d1f14b51d9d4b2e4f55ec5a642e72c`
also fails the three contracts above. This is local test-signature evidence,
separate from the released runtime. Consumer v0.7.5 tests on actual schema
confirm the indexed visibility and owner behavior, but retain characterization
assertions for the old defect. They are not evidence that it has been repaired.
See [the consumer integration report](SQL_CONSUMER_CONTRACT.zh-CN.md) for the
source/artifact/consumer evidence, logs, Core ownership, and upgrade dependencies.

The SDK preserves the transaction capability sentinel and typed native cause.
BEGIN `busy` remains a refusal. After COMMIT or a dispatched write, invalid
result protocol/affected-row metadata is `CodeResultIndeterminate` with the
protocol error retained in its cause; an explicit Core rejection keeps its
original code. Callers must retain the chain and avoid blind retries. These
malformed-response cases have component fault-injection coverage, not a claimed
native Core fault-injection run. Native SQL receipt recovery is not established
by the conditional-KV receipt API.

Go `time.Time` parameters map to millisecond TIMESTAMP. The nanos contract uses
int64/INTEGER explicitly. Exact DECIMAL round trips do not establish exact
SUM/AVG or all precision/scale and range-index behavior.

The DECIMAL comparison fixture passes a typed `talon.DecimalValue`. A Go
`string` parameter is TEXT and did not match the DECIMAL column in this
fixture; financial queries must pass an exact DECIMAL value. The comparison
fixture does not prove a DECIMAL range index plan.

Performance admission is separate. The driver defaults its `database/sql` pool
to four connections when the signed Core attests `native_shared_core` v1, and
caps it at one on older artifacts. A positive GoFrame `MaxOpenConnCount` or
`SetMaxOpenConnCount` value set before the first database operation can lower
the shared-Core default. Values above four remain capped at four. GoFrame
caches a pool after its first use, so later setter calls do not resize that
existing pool. The four-connection setting passed a local signed-Core
four-connection transaction fixture; no throughput or tail-latency claim follows.
`QueryContext` retains the complete Core result;
the GoFrame projection now converts each row during iteration, avoiding a second
whole-result copy, but this is not a SQL cursor or a bounded result. No concurrent
workload or large-result latency and allocation budget has been verified. For
simple-column `DISTINCT`,
Core hashes selected source values without allocating projected duplicate
rows, but this local test does not measure end-to-end throughput.
The local SQL latency and allocation snapshot is in
[SQL_PERFORMANCE.md](SQL_PERFORMANCE.md).

## Native context contract

The adapter forwards context through BEGIN, query, mutation and COMMIT. ROLLBACK
uses the transaction context and dispatches mandatory cleanup even after expiry.
A terminal error synchronously closes the native session and `driver.Validator`
causes the pool to discard it; `driver.ErrBadConn` is not used for these errors
because it can trigger mutation replay. `CodeResultIndeterminate` remains the
primary outcome and keeps cancellation/durability/protocol causes reachable.

Cancellable calls require Core capability `native_sql_context@1`, feature
`native_sql_context_v1` and all four versioned ABI symbols. The currently pinned
signed runtime does not grant this capability. An old runtime still accepts
non-cancellable calls; bounded calls return `CodeCapabilityUnavailable` before
SQL dispatch. Consumer SDK/runtime upgrades must be explicit and independently
verified. `Connect(ctx)` observes context before and after synchronous Open, but
native opening/admission itself remains without a hard cancellation bound.

This is cooperative cancellation, not an unconditional five-second completion
guarantee. Filesystem/fsync and rollback/session cleanup must finish before owner
release. GoFrame and database/sql transaction cancellation were exercised on the
real embedded path; see `../NATIVE_SQL_CONTEXT_HANDOFF.zh-CN.md` for artifact pins,
commands and the distinction from SaaS/release/production acceptance.
