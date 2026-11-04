package schedule

import (
	"context"
	"math/rand"
	"time"
)

// BuiltinEvaluation is owned by one scheduler calculation turn. It is not a
// registry or durable cursor: each Next still receives the explicit original
// previous and now. The shared budget cannot be reset by an AND retry, nested
// child, due-time collection loop or the post-submission second Next.
// A session is sequential; BuiltinTrigger itself remains safe for concurrent
// independent sessions.
type BuiltinEvaluation struct {
	trigger    *BuiltinTrigger
	evaluation *builtinEvaluation
}

func (t *BuiltinTrigger) NewEvaluation(ctx context.Context) (*BuiltinEvaluation, error) {
	if ctx == nil {
		return nil, builtinBoundary("invalid_evaluation_port")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &BuiltinEvaluation{trigger: t, evaluation: &builtinEvaluation{
		ctx: ctx, remaining: t.limits.MaxEvaluations, locations: t.locations,
		uniform: func(lo, hi float64) float64 { return lo + (hi-lo)*rand.Float64() },
	}}, nil
}
func (s *BuiltinEvaluation) Next(previous *time.Time, now time.Time) (*time.Time, error) {
	if now.Year() < 1 || now.Year() > 9999 || (previous != nil && (previous.Year() < 1 || previous.Year() > 9999)) {
		return nil, errTrigger
	}
	return s.evaluation.next(s.trigger.root, timeCopy(previous), now, nil)
}
