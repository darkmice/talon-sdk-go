# GoFrame native SQL compatibility

This matrix describes the GoFrame v2.8.3 `gdb` adapter against an embedded,
signed Talon Core. It is based on `TestLocalSignedCoreGoFrameInterop`, invoked
through `TestLocalSignedCoreKVInterop` with an ephemeral test signature and
Core commit `1695fdfa097076230a37225382b6d88251fb8dac`. It is local
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

The native driver also has unit coverage for SQL result metadata, exact DECIMAL
conversion, time parameters, generated insert ID handling, and SQL variant
formatting. Those tests use a fake native database and do not establish the
same signed Core interoperability as the matrix above.

Common SQL shapes still lacking a GoFrame-to-signed-Core test include multiple
joins, nested subqueries and `EXISTS`, `UNION`, CTEs, window functions, JSON
and date expressions, and schema migrations. Core's own SQL tests cover many of
these independently; that does not establish adapter behavior. A specific
consumer workload should add its generated SQL to the live test before
claiming compatibility.

Performance admission is separate. The driver currently caps its `database/sql`
pool at one native connection, and `QueryContext` converts the complete Core
result into Go rows before iteration. No concurrent workload or large-result
latency and allocation budget has been verified. For simple-column `DISTINCT`,
Core hashes selected source values without allocating projected duplicate
rows, but this local test does not measure end-to-end throughput.
