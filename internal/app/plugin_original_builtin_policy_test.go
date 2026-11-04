package app

import (
	"context"
	"errors"
	"light-media/internal/plugins/schedule"
	"testing"
	"time"
)

// Policy checks are owned target adaptation evidence, not invented APS output.
// The unchanged source differential roots establish normal algorithm semantics.
func TestOriginalBuiltinClockBudgetPolicy(t *testing.T) {
	limits := originalBuiltinClockLimits()
	if limits.MaxNodes != 1024 || limits.MaxDepth != 32 || limits.MaxEvaluations != 65536 || originalBuiltinClockMaxDue != 4096 || originalBuiltinClockTimeout != 250*time.Millisecond {
		t.Fatal("production policy changed", limits)
	}
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	date := schedule.BuiltinDescriptor{Kind: "date", Timezone: "UTC", RunAt: at.Format(time.RFC3339Nano)}
	wantBoundary := func(err error, code string) {
		t.Helper()
		var boundary *schedule.BuiltinBoundaryError
		if !errors.As(err, &boundary) || boundary.Code != code {
			t.Fatalf("want %s, got %T %v", code, err, err)
		}
	}
	many := schedule.BuiltinDescriptor{Kind: "or", Children: make([]schedule.BuiltinDescriptor, limits.MaxNodes)}
	for i := range many.Children {
		many.Children[i] = date
	}
	_, err := schedule.NewBuiltin(many, at, limits)
	wantBoundary(err, "descriptor_nodes")
	deep := date
	for i := 0; i < limits.MaxDepth; i++ {
		deep = schedule.BuiltinDescriptor{Kind: "or", Children: []schedule.BuiltinDescriptor{deep}}
	}
	_, err = schedule.NewBuiltin(deep, at, limits)
	wantBoundary(err, "descriptor_depth")
	later := date
	later.RunAt = at.Add(time.Hour).Format(time.RFC3339Nano)
	// Two unequal Date children intentionally cannot converge. Only target code
	// executes it, under the exact shared evaluation bound; original is not run.
	impossible, err := schedule.NewBuiltin(schedule.BuiltinDescriptor{Kind: "and", Children: []schedule.BuiltinDescriptor{date, later}}, at, limits)
	if err != nil {
		t.Fatal(err)
	}
	evaluation, err := impossible.NewEvaluation(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	value, err := evaluation.Next(nil, at)
	wantBoundary(err, "evaluation_budget")
	if value != nil {
		t.Fatal("budget became datetime")
	}
	turn := &originalBuiltinClockTurn{}
	for i := 0; i < originalBuiltinClockMaxDue; i++ {
		if err = turn.collectDue(); err != nil {
			t.Fatal(i, err)
		}
	}
	wantBoundary(turn.collectDue(), "due_collection_budget")
	trigger, err := schedule.NewBuiltin(date, at, limits)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	admitted, err := originalBeginBuiltinClock(ctx, trigger)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	defer admitted.close()
	value, err = admitted.evaluation.Next(nil, at)
	if value != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("actual cancellation not propagated", value, err)
	}
	// Deterministic expired parent deadline checks propagation, not a claim that
	// a real 250ms timer elapsed. Production always creates the above deadline.
	expired, stop := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer stop()
	if _, err = originalBeginBuiltinClock(expired, trigger); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("deadline not propagated", err)
	}
	// Explicit present zero must survive host storage, independently of display.
	job := &originalDirectJob{boundedClock: true}
	zero := time.Time{}
	originalSetBuiltinDue(job, &zero)
	if !originalJobHasDue(job) || !job.due.IsZero() {
		t.Fatal("legal zero became None")
	}
	originalSetBuiltinDue(job, nil)
	if originalJobHasDue(job) {
		t.Fatal("None became zero datetime")
	}
}
