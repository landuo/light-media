package app

import (
	"context"
	"time"
)

// Only fixed operation/phase names and elapsed times are recorded. This uses
// the bounded async logger, never the database whose wait is being measured.
func (a *App) performanceStages(ctx context.Context, operation, phase string) func(string) {
	started := time.Now()
	trace, _ := ctx.Value(jobTraceKey{}).(jobTrace)
	return func(next string) {
		elapsed := time.Since(started)
		if phase != "" && elapsed >= 250*time.Millisecond {
			a.writeLog(map[string]any{
				"time": now(), "id": id("log_"), "level": "INFO", "module": "performance",
				"event": "slow_phase", "operation": operation, "message": phase,
				"duration_ms": elapsed.Milliseconds(), "request_id": trace.Request, "job_id": trace.Job,
			})
		}
		started, phase = time.Now(), next
	}
}
