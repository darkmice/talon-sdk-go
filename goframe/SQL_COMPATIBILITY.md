# GoFrame native SQL compatibility

This matrix describes the GoFrame v2.8.3 `gdb` adapter against an embedded,
signed Talon Core. It is based on `TestLocalSignedCoreGoFrameInterop`, invoked
through `TestLocalSignedCoreKVInterop` with an ephemeral test signature and
clean Core commit `a968c4db18d1f14b51d9d4b2e4f55ec5a642e72c`. It is local
interoperability evidence, not a released Core or production performance claim.

| SQL workload | Verified through GoFrame gdb and signed Core |
| --- | --- |
| Schema discovery | `Tables`, `TableFields` |
| Create and write | `CREATE TABLE`, `CREATE INDEX`, parameterized `INSERT`, model single and batch `Insert`, `Save` upsert, `Update`, `InsertIgnore`, `Delete` |
| Read and predicates | `One`, `All`, `Count`, `AllAndCount`, indexed equality, `IN`, parameterized `IN (SELECT ...)`, `BETWEEN`, `LIKE`, `OR`, `IS NULL`, `IS NOT NULL` |
| Result shaping | projection, `DISTINCT`, ascending and descending order, page/limit/offset |
| Relational and aggregate | aliased `LEFT JOIN` and filtered `INNER JOIN`, `GROUP BY`, parameterized `HAVING`, `COUNT`, `SUM`, `AVG`, `MIN`, `MAX` |
| Exact value | `DECIMAL(18,2)` write and readback without a float round trip |
| Transaction | begin, rollback, commit, and readback |
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

The DECIMAL comparison fixture passes a typed `talon.DecimalValue`. A Go
`string` parameter is TEXT and did not match the DECIMAL column in this
fixture; financial queries must pass an exact DECIMAL value. The comparison
fixture does not prove a DECIMAL range index plan.

Performance admission is separate. The driver caps its `database/sql` pool at
four connections when the signed Core attests `native_shared_core` v1, and at
one on older artifacts. The four-connection setting passed a local signed-Core
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
