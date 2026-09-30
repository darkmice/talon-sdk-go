package goframe

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/gogf/gf/v2/database/gdb"
)

// The root local signed-Core fixture launches this benchmark when
// TALON_TEST_GOFRAME_BENCH=1. Setup and signature verification are excluded
// from operation timing.
func BenchmarkLocalSignedCoreGoFrameSQL(b *testing.B) {
	path := os.Getenv("TALON_GOFRAME_LIVE_DB")
	if path == "" {
		b.Skip("run through the local signed Core benchmark fixture")
	}
	ctx := context.Background()
	db, err := gdb.New(gdb.ConfigNode{Type: DriverName, Name: filepath.Join(b.TempDir(), "data")})
	if err != nil {
		b.Fatal(err)
	}
	if _, err := db.Exec(ctx, "CREATE TABLE perf_groups (id INT PRIMARY KEY, label TEXT)"); err != nil {
		b.Fatal(err)
	}
	if _, err := db.Exec(ctx, "CREATE TABLE perf_items (id INT PRIMARY KEY, group_id INT, score INT, name TEXT)"); err != nil {
		b.Fatal(err)
	}
	if _, err := db.Exec(ctx, "CREATE INDEX perf_items_group ON perf_items (group_id)"); err != nil {
		b.Fatal(err)
	}
	for group := 0; group < 100; group++ {
		if _, err := db.Exec(ctx, "INSERT INTO perf_groups VALUES (?, ?)", group, fmt.Sprintf("label%d", group)); err != nil {
			b.Fatal(err)
		}
	}
	for first := 0; first < 10_000; first += 100 {
		values := make([]string, 0, 100)
		for id := first; id < first+100; id++ {
			values = append(values, fmt.Sprintf("(%d,%d,%d,'name%d')", id, id%100, id%1000, id))
		}
		if _, err := db.Exec(ctx, "INSERT INTO perf_items VALUES "+strings.Join(values, ",")); err != nil {
			b.Fatal(err)
		}
	}
	nativeDB, err := db.Master()
	if err != nil {
		b.Fatal(err)
	}
	cases := []struct {
		name  string
		query func() error
	}{
		{"direct_native_pk", func() error {
			var id int64
			var name string
			return nativeDB.QueryRowContext(ctx, "SELECT id,name FROM perf_items WHERE id=?", 5000).Scan(&id, &name)
		}},
		{"indexed_pk", func() error {
			_, err := db.Model("perf_items").Ctx(ctx).Where("id", 5000).All()
			return err
		}},
		{"projected_distinct", func() error {
			_, err := db.Model("perf_items").Ctx(ctx).Fields("group_id").Distinct().Order("group_id").All()
			return err
		}},
		{"filtered_join", func() error {
			_, err := db.Model("perf_items", "i").Ctx(ctx).
				InnerJoin("perf_groups", "g", "i.group_id=g.id").
				Fields("i.id,g.label").Where("g.label", "label42").Order("i.id").All()
			return err
		}},
		{"group_having", func() error {
			_, err := db.Model("perf_items").Ctx(ctx).Fields("group_id,COUNT(*) AS n").
				Group("group_id").Having("COUNT(*) > ?", 1).Order("group_id").All()
			return err
		}},
		{"in_subquery", func() error {
			_, err := db.Model("perf_items").Ctx(ctx).
				Where("group_id IN (SELECT id FROM perf_groups WHERE label=?)", "label42").Fields("id").All()
			return err
		}},
	}
	for _, benchmark := range cases {
		b.Run(benchmark.name, func(b *testing.B) {
			if err := benchmark.query(); err != nil {
				b.Fatal(err)
			}
			latencies := make([]int64, b.N)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				started := time.Now()
				if err := benchmark.query(); err != nil {
					b.Fatal(err)
				}
				latencies[i] = time.Since(started).Nanoseconds()
			}
			b.StopTimer()
			sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
			b.ReportMetric(float64(latencies[(b.N-1)/2])/1000, "p50-us")
			b.ReportMetric(float64(latencies[(b.N*99+99)/100-1])/1000, "p99-us")
		})
	}
}
