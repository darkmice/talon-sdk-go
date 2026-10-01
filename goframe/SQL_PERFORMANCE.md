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

## Core read-path optimization, 2026-09-30

Core `6c78f4c` extends its existing literal primary-key fast path to
simple column projections and passes small positive `IN` lists, including
resolved subquery values, to the primary or secondary index. Larger lists
retain the row-scan path to avoid many random probes. The comparison program
was rerun on the same machine and dataset, with three fresh processes and 100
timed operations per query in each process. Values are medians of per-run p50
latencies in microseconds; before values are from the earlier Core comparison.

| Query | Before Talon | After Talon | Improvement | SQLite in after runs |
| --- | ---: | ---: | ---: | ---: |
| Literal projected primary-key read | 9.75 | 1.50 | 6.50× | 5.42 |
| `IN (SELECT ...)`, 100 results | 1,896.50 | 119.04 | 15.93× | 29.33 |

The optimized primary-key result uses Talon's literal-query fast path. SQLite
is prepared on each call in this probe, while a reused SQLite prepared
statement has a different timing boundary. GoFrame uses bound parameters and
its native result contract, so this literal fast-path improvement has **not**
been verified through GoFrame. The indexed `IN` path is shared by bound SELECT
execution, but its GoFrame latency also requires a new signed-Core run. The
other four query shapes were not targeted and remain in roughly the previous
latency bands. `COUNT`, covering-index DISTINCT/GROUP BY, JOIN order, and Go
bridge allocations remain separate optimization work.

The after-run raw timings are in `perf/core-sqlite-optimized-run*.csv` and the
static plan summaries in `perf/core-sqlite-optimized-plans.txt`. The Core
changes passed 819 SQL unit tests before the probe-count guard; targeted
transaction, wide-list fallback, and projection regressions passed after it.
These are local Core measurements, not a new
signed GoFrame or release-performance result.

## Filtered JOIN route experiment, 2026-09-30

The previous JOIN executor did not recognize a right-table alias such as
`g.label` as a pushdown candidate. With that ownership fixed, a selective
filtered INNER JOIN can use the existing index on the left join column to fetch
matching rows instead of decoding all 10,000 left rows. The indexed route is
currently limited to non-transactional integer equality joins with at most
eight filtered right rows, no pagination, and no chain JOIN. The fallback keeps
the existing scan behavior.

The same release-mode `sql_sqlite_comparison` probe verified equal Talon and
SQLite results on each run. One pre-change run gave Talon 7,656.12 µs p50 and
SQLite 1,455.58 µs p50. Three fresh post-change runs gave Talon 119.54,
125.62, and 124.58 µs p50 (median 124.58 µs), and SQLite 1,446.62, 1,458.04,
and 1,463.88 µs p50 (median 1,458.04 µs). The observed Talon improvement is
roughly 60× for this selective JOIN; in the post-change runs it is roughly 12×
faster than the tested SQLite plan. The raw timings are in
`perf/core-sqlite-join-route.csv`. Talon's static `EXPLAIN` still reports a full
scan for this query and should not be used as execution-path evidence.

After consolidating the JOIN strategy into one execution-plan enum, three more
fresh processes gave Talon 143.46, 142.25, and 126.79 µs p50 (median 142.25
µs), versus SQLite 1,535.25, 1,557.25, and 1,539.12 µs (median 1,539.12 µs).
This retains a large improvement over the 7,656.12 µs pre-change run. The
additional samples do not isolate whether the smaller difference between the
two post-change sets came from code generation or machine variation.

These numbers are Core-only, literal SQL timings. They do not establish the
latency through a signed Core artifact, the Go driver, or GoFrame. The `COUNT`,
`DISTINCT`, and GROUP BY paths remain separate opportunities.

At that snapshot, the next count opportunity was structural: multi-row SQL INSERT does not build
`column_stats`, so this fixture's `COUNT(1)` falls back to `count_prefix` and
visits every index key. The existing `ColumnStats.count` counts non-NULL numeric
values, which is not a general table-row count. A constant-time exact `COUNT(*)`
therefore needs a separate row-count invariant maintained across inserts,
replacements, deletes, transactions, and reopened engines; using an arbitrary
column statistic would risk wrong answers.

The later `02ceb59` Core candidate addresses the hot read with an exact
snapshot-sequence cache; its signed GoFrame result is recorded below.

## Covering-index DISTINCT experiment, 2026-09-30

Simple `SELECT DISTINCT group_id FROM items ORDER BY group_id` now reads only
the existing integer index. It seeks to the next distinct value rather than
decoding every indexed row; after 256 distinct values it switches to one
ordered index scan so an all-unique workload does not pay for 10,000 separate
seeks. The route applies only to a single indexed integer projection without
WHERE, transaction overlay, `DISTINCT ON`, or unrelated ordering. NULL ordering
and pagination follow the existing query semantics. Other shapes retain the
general SELECT path.

On the 10,000-row/100-group fixture, three pre-change p50s were 2,750.83,
2,624.62, and 2,670.21 µs (median 2,670.21). Three final p50s were 74.88,
76.38, and 79.50 µs (median 76.38), about 35× faster. SQLite's final-run
median was 230.54 µs. The benchmark verifies equal result values on every
run; its timing includes parsing and full result materialization.

With 10,000 unique integer values, the final indexed route returned 10,000
rows in 1,046.50, 1,051.71, and 1,235.54 µs p50 (median 1,051.71). The
same Talon query without that secondary index had a 2,749.33 µs median, though
one of its three runs showed substantial machine noise. SQLite's indexed
median was 878.92 µs. The raw measurements are in
`perf/core-sqlite-distinct-route.csv`, and the high-cardinality probe is
reproducible with `sql_sqlite_comparison 10000 100 distinct_cardinality`.
These are Core-only results, not signed GoFrame end-to-end results. The static
Talon `EXPLAIN` does not report this new index route.

## Local signed Core through GoFrame, 2026-09-30

Core `24bcf7d` was rebuilt from a clean worktree. Its self-manifest reported
`git_dirty=false` and the available `native_sql_result` and `native_kv_read`
capabilities. The SDK's opt-in fixture signed the library with an ephemeral
test key, verified it through the normal bundle policy, passed the native KV
and GoFrame SQL integration tests, and ran the GoFrame benchmark three times.
This is local test-artifact evidence, not a talon-bin release signature.

| GoFrame query | p50 µs, three runs | Median B/op | Median allocs/op |
| --- | ---: | ---: | ---: |
| Model primary-key read | 70.54–76.33 | 36,097 | 902 |
| Projected `DISTINCT` | 607.2–623.6 | 457,312 | 9,664 |
| Filtered INNER JOIN | 1,246–1,296 | 874,004 | 18,843 |
| GROUP BY/HAVING | 3,984–4,103 | 835,130 | 18,513 |
| Parameterized `IN (SELECT ...)` | 688.2–711.3 | 462,135 | 9,835 |

The prior signed Core snapshot had 3,491–3,606 µs for `DISTINCT`, 9,580–9,681
µs for this JOIN, and 2,767–2,798 µs for the `IN` subquery. These lower
end-to-end latencies confirm that the Core query-route gains reach GoFrame.
Allocation counts remain close to the older snapshot, so reducing native
result decoding and GoFrame materialization is a separate opportunity. The
full local benchmark output is in `perf/goframe-24bcf7d-signed.txt`.

## Exact count and indexed GROUP BY through locally signed Core, 2026-10-01

The same SDK fixture rebuilt clean Core commits `24bcf7d` and `02ceb59`,
verified each self-manifest and an ephemeral test signature, then ran the
GoFrame integration test and the same release benchmark (`benchtime=100x`,
`count=3`). The benchmark now includes `Model.Count()` on the 10,000-row table
and verifies its returned row count. Both Core libraries passed integration.
Raw measurements, including allocations and p99, are in
`perf/goframe-24bcf7d-with-count.txt` and
`perf/goframe-02ceb59-signed.txt`.

| GoFrame model query | `24bcf7d` median p50 | `02ceb59` median p50 | Change | Median allocs/op |
| --- | ---: | ---: | ---: | ---: |
| `Model.Count()` | 906.2 µs | 46.0 µs | 19.7× faster | 542 |
| GROUP BY/HAVING | 4,223 µs | 2,296 µs | 1.84× faster | 18,513 → 18,514 |

The other query shapes remained within their earlier latency bands. The Core
fixture measures GROUP BY at about 3,002→917 µs, so GoFrame and native result
materialization still consume a substantial share of the end-to-end request.
GROUP BY allocations remain near 18.5K per operation. `COUNT(*)` first reads a
stable snapshot to populate the exact row-count cache; the numbers above
measure repeated warm queries. A write to any Talon keyspace invalidates that
cache through the shared storage sequence, so mixed write workloads still
need a separate performance check. These are local test signatures, not a
talon-bin release or production artifact.

## SDK native SQL result decoding, 2026-10-01

The GROUP BY query through direct `database/sql` used about 18,140 Go
allocations per call, versus about 18,512 through GoFrame. A separate SDK
benchmark with the same 100-row/two-integer-column result shape attributed
14,480 allocations to `decodeSQLResult`. The decoder had been calling the
strict JSON decoder twice on the whole result and again on each tagged cell.
It now makes one whole-result strict decode call and parses cells already
covered by that validation without another strict decode.

Using the same clean, locally signed Core `02ceb59` artifact and GoFrame data,
three 20-call repetitions gave 18,510–18,512 → 10,423–10,424 allocations per
GoFrame GROUP BY call. Median p50 changed from 2,239 to 1,714 µs, while
median allocated bytes changed from 832,920 to 387,075 per call. The isolated
SDK decoder changed from 14,480 to 6,393 allocations per call; its 8,087
allocation reduction closely matches the end-to-end reduction. Strictness
regressions cover missing/unknown result fields, duplicate nested keys,
numeric overflow, row width, and every tagged cell kind. The raw numbers and
method are in `perf/goframe-02ceb59-sdk-decode-before-after.txt`.

Go's `-memprofile` flag ended with `signal: killed` even on a `goframe` test
running no benchmarks, so no pprof profile was available. The p50 difference
is a small local sample; concurrency and large results remain unmeasured.

## Strict JSON duplicate-key scan, 2026-10-01

After the SDK result decoder change above, the remaining duplicate-key scan
used `json.Decoder.Token()` for every scalar. The shared strict JSON path now
validates syntax with `json.Valid`, then scans object keys without allocating
boxed scalar tokens. Escaped keys still use `encoding/json` normalization
before comparison. A fixed-seed 3,000-case differential test and boundary
cases match the previous scanner's acceptance, duplicate-key classification,
and 128-level depth limit; all Go package tests pass.

On the same locally signed Core `02ceb59`, GoFrame GROUP BY changed from
10,423–10,424 to 4,681–4,683 allocations per operation, and median bytes
from 387,075 to 287,588. Median p50 changed from 1,714 to 1,666 µs across
three 20-call repetitions. The latency difference is modest; the stable
allocation reduction is the main result. The isolated duplicate-key scan
changed from 3,271 to 410 allocations for the 100-row payload. The outer
native response and inner SQL result are each checked, explaining why the
end-to-end reduction is close to twice the isolated reduction. A small JSON
response also improved from 43 to 6 scanner allocations. Raw measurements
are in `perf/goframe-02ceb59-duplicate-scan-before-after.txt`. This is local
single-caller evidence, not a released talon-bin performance result.
