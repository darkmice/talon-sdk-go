# GoFrame native SQL performance snapshot — 2026-09-30

This is a local diagnostic on an Apple M2 Max with 32 GiB RAM, macOS arm64,
Rust 1.92.0, and Go 1.22.0. The database contained 10,000 items in 100 groups.
All queries were warmed five times. Core was built in release mode with LTO;
GoFrame used a locally signed release Core library from source commit
`1695fdfa097076230a37225382b6d88251fb8dac`. Core setup and GoFrame
bundle signing/loading were outside the operation timers. Each run sampled 100
operations; GoFrame ran three repetitions. These p99 values are exploratory
because 100 samples make the 99th percentile sensitive to one outlier.

## Core before and after the SQL fixes

The baseline was Core `c7e0bdc`, before the GoFrame SQL fixes. Only queries
with the same correct result on both versions are compared. Values are p50 in
microseconds; the new version has three runs, with the baseline between the
first two.

| Query | Baseline | New run 1 | New run 2 | New run 3 |
| --- | ---: | ---: | ---: | ---: |
| Indexed primary-key read | 9.79 | 9.38 | 9.88 | 9.42 |
| `COUNT(1)` on 10,000 rows | 644.04 | 735.04 | 626.17 | 630.79 |
| Literal `IN (SELECT ...)`, 100 results | 1920.17 | 1994.33 | 1842.71 | 1861.21 |

There is no clear systematic regression in these three shared paths at this
size. This does not establish performance equivalence over other data sizes,
query plans, concurrency, or machines. Queries whose old results were wrong or
whose syntax failed cannot be used for a before/after speed ratio. On the new
Core, projected `DISTINCT` took 2549–2827 µs p50, filtered JOIN 7350–7816 µs,
and GROUP BY/HAVING 2860–3008 µs, each returning 100 rows.

## Signed Core through Go and GoFrame

The direct native case uses `database/sql` with the Talon driver but bypasses
GoFrame's model builder. The other cases call GoFrame `gdb.Model`. Values are
ranges across three runs on the same signed Core. Setup is excluded.

| Query | p50 µs | p99 µs | Go B/op | Go allocs/op |
| --- | ---: | ---: | ---: | ---: |
| Direct native primary-key read | 43.88–45.71 | 58.04–116.0 | 21,393–21,396 | 554 |
| GoFrame model primary-key read | 69.96–70.92 | 225.2–388.3 | 36,092–36,477 | 902 |
| Projected `DISTINCT` | 3491–3606 | 4354–4876 | 454,190–457,269 | 9,662–9,664 |
| Filtered INNER JOIN | 9580–9681 | 11,637–17,875 | 878,636–879,927 | 18,845–18,846 |
| GROUP BY/HAVING | 4260–4333 | 5310–5406 | 832,446–837,830 | 18,512–18,515 |
| Parameterized `IN (SELECT ...)` | 2767–2798 | 3800–4055 | 462,075–463,977 | 9,836 |

The primary-key comparison locates substantial overhead above the Core query:
the direct Go query is roughly 35 µs slower and has 554 Go allocations, while
the GoFrame model path adds roughly another 25 µs and 348 allocations over
that direct Go query. The Core probe uses a literal and the Go queries use a
bound parameter, so the first difference includes parameter binding as well
as the native bridge. These are end-to-end differences, not function-level
attribution.
The driver currently serializes access through one native connection and
materializes the full result in `QueryContext`; neither parallel throughput nor
large-result memory use has been measured. Two attempts to collect a Go memory
profile ended with `signal: killed`, so the exact allocation sites remain
unverified.

The raw runs are in [perf](perf/). Core's reproducible probe is
`examples/sql_goframe_perf.rs` in talon-core; the GoFrame benchmark is
`BenchmarkLocalSignedCoreGoFrameSQL`. Run the signed integration fixture with
`TALON_TEST_GOFRAME_BENCH=1` and the three `TALON_TEST_LOCAL_CORE_*` artifact
paths documented in the SDK README. No performance budget or production
admission follows from this snapshot. The next performance gate should measure
1, 4, and 8 concurrent callers, a larger dataset, Primary replication, and
allocation profiles before lifting the one-connection cap or releasing this
adapter as performance-ready.

## Same-machine SQLite read comparison

On the same Apple M2 Max, a separate Core `e8605c0` probe compared Talon with
bundled SQLite 3.46.0 through Rust `rusqlite`. Both used identical `CREATE TABLE`,
`CREATE INDEX`, `INSERT`, and SELECT SQL, with 10,000 items and 100 groups. Each
query was warmed five times and timed 100 times in each of three fresh process
runs. Timing includes query parsing/preparation, execution, and full result
materialization; setup and result equality checks are outside the timer. All six
queries returned the same values on both engines. Values below are medians of
the three per-run p50 latencies. The ratio is Talon latency divided by SQLite
latency, so a number above one means SQLite was faster.

| Query | Result rows | Talon p50 µs | SQLite p50 µs | Ratio |
| --- | ---: | ---: | ---: | ---: |
| Indexed primary-key read | 1 | 9.75 | 5.46 | 1.79× |
| `COUNT(1)` | 1 | 640.67 | 95.00 | 6.74× |
| `IN (SELECT ...)` | 100 | 1,896.50 | 29.54 | 64.20× |
| Projected `DISTINCT` | 100 | 2,684.42 | 232.12 | 11.56× |
| Filtered INNER JOIN | 100 | 7,551.17 | 1,473.62 | 5.12× |
| GROUP BY/HAVING | 100 | 2,891.25 | 268.29 | 10.78× |

The execution code explains the larger gaps. Talon resolves `IN (SELECT ...)`
to a value list but does not turn that list into an index lookup; the remaining
WHERE condition takes the row-scan path. `DISTINCT` materializes and sorts all
rows before deduplication, and GROUP BY reads and decodes each row into a hash
aggregate. The filtered JOIN pushes the right-side predicate but still scans
all 10,000 left rows. SQLite uses the `group_id` index for the subquery and a
covering index scan for `DISTINCT` and GROUP BY/HAVING. Talon's `EXPLAIN` is a
static summary that ignores JOIN and GROUP BY details; its output is not a
complete record of the path actually executed.

Three further runs with the same dataset isolate two Talon paths. The p50
figures below are medians of those three runs. `SELECT *` returns four columns,
so its speed advantage over the two-column projection cannot be explained by
less result materialization.

| Talon query shape | p50 µs | Execution distinction |
| --- | ---: | --- |
| `SELECT * ... WHERE id=5000` | 1.25 | Dedicated primary-key fast path |
| `SELECT id,name ... WHERE id=5000` | 9.67 | General parse and projection path |
| `SELECT id ... WHERE group_id=42` | 74.33 | Secondary-index lookup |
| `SELECT id ... WHERE group_id IN (SELECT ...)` | 1,876.04 | Value-list filter after row scan |
| `COUNT(1)` / `COUNT(*)` | 648.04 / 659.54 | Both normalize to prefix counting |

The current `count_prefix` implementation iterates over matching keys, so
the count path is O(N) time despite using O(1) memory. SQLite's `COUNT(*)`
was 5.58 µs p50 in these diagnostic runs; its `COUNT(1)` was 85.50 µs. These
SQL spellings are equivalent for this table, but they hit different SQLite
optimizations. The previous historical Talon-vs-SQLite headline measured
`SELECT *` through Talon's fast path; its `SQL INSERT (batch)` label actually
covered individual `run_sql` calls, without equivalent durability verification.
It cannot be carried over to the projected and analytical SQL queries above.

This comparison is Core versus SQLite's Rust API, not GoFrame versus a SQLite
GoFrame driver. SQLite used its default connection settings; the workload is
warm, single-threaded, read-only, and small enough to fit in memory. It does
not establish write durability, concurrent throughput, large-data behavior,
or a general database ranking. DuckDB was not locally available and was not
measured. Raw comparison timings and plans are in
`perf/core-sqlite-e8605c0-run*.csv` and
`perf/core-sqlite-e8605c0-run*.plans.txt`; the read-path diagnostics are in
`perf/core-sqlite-readpath-run*.csv`. The reproducible probe is
`examples/sql_sqlite_comparison.rs` in talon-core.
