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
