package app

import (
	"context"
	"fmt"
	"testing"
)

// A synthetic pack measures the cost moved from GET into execution writes.
// No media, downloader or network service is involved.
func BenchmarkHistoryWritePlan(b *testing.B) {
	for _, count := range []int{1, 24, 100} {
		b.Run(fmt.Sprintf("files_%d", count), func(b *testing.B) {
			store, err := OpenStore(b.TempDir())
			if err != nil {
				b.Fatal(err)
			}
			defer store.Close()
			ctx := context.Background()
			plan := ImportPlan{ID: "bench-pack", CreatedAt: now(), Files: []PlanFile{}}
			for i := 0; i < count; i++ {
				plan.Files = append(plan.Files, PlanFile{ID: fmt.Sprintf("file-%d", i), Source: fmt.Sprintf("/synthetic/source/%d.mkv", i), Target: fmt.Sprintf("/synthetic/target/%d.mkv", i), Status: "ready"})
			}
			if err := store.Put(ctx, "import_plan", plan.ID, plan); err != nil {
				b.Fatal(err)
			}
			tx, err := store.DB.BeginTx(ctx, nil)
			if err != nil {
				b.Fatal(err)
			}
			if _, err = enqueueTx(ctx, tx, "import", map[string]any{"id": plan.ID}, "bench"); err != nil {
				tx.Rollback()
				b.Fatal(err)
			}
			if err = tx.Commit(); err != nil {
				b.Fatal(err)
			}
			b.ResetTimer()
			for n := 0; n < b.N; n++ {
				if n%2 == 0 {
					plan.Files[n%count].Status = "imported"
				} else {
					plan.Files[n%count].Status = "ready"
				}
				if err := store.Put(ctx, "import_plan", plan.ID, plan); err != nil {
					b.Fatal(err)
				}
			}
			b.StopTimer()
			var rows int
			if err := store.DB.QueryRow(`SELECT COUNT(*) FROM transfer_history_current`).Scan(&rows); err != nil || rows != count {
				b.Fatalf("saved rows=%d want=%d: %v", rows, count, err)
			}
		})
	}
}
