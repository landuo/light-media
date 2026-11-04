package schedule

import (
	"context"
	"math/rand"
	"time"
)

// Next is the original two-argument operation, not a per-child cursor. A nil
// result alone is source None. A nonnil pointer may contain Go's zero time:
// Python's 0001-01-01T00:00:00 UTC is a valid, present datetime.
func (t *BuiltinTrigger) Next(ctx context.Context, previous *time.Time, now time.Time) (*time.Time, error) {
	return t.nextObserved(ctx, previous, now, func(lo, hi float64) float64 { return lo + (hi-lo)*rand.Float64() }, nil)
}

type builtinObservation struct {
	Path     []int
	Phase    string
	Kind     string
	Previous *time.Time
	Now      time.Time
	Value    *time.Time
	Err      error
}
type builtinEvaluation struct {
	ctx       context.Context
	remaining int
	locations map[string]*time.Location
	uniform   func(float64, float64) float64
	observe   func(builtinObservation)
}

func timeCopy(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	v := *value
	return &v
}
func (t *BuiltinTrigger) nextObserved(ctx context.Context, previous *time.Time, now time.Time, uniform func(float64, float64) float64, observe func(builtinObservation)) (*time.Time, error) {
	if ctx == nil || uniform == nil {
		return nil, builtinBoundary("invalid_evaluation_port")
	}
	if now.Year() < 1 || now.Year() > 9999 || (previous != nil && (previous.Year() < 1 || previous.Year() > 9999)) {
		return nil, errTrigger
	}
	e := &builtinEvaluation{ctx: ctx, remaining: t.limits.MaxEvaluations, locations: t.locations, uniform: uniform, observe: observe}
	return e.next(t.root, timeCopy(previous), now, nil)
}
func (e *builtinEvaluation) next(n *builtinNode, previous *time.Time, now time.Time, path []int) (value *time.Time, err error) {
	if err = e.ctx.Err(); err != nil {
		return nil, err
	}
	if e.remaining <= 0 {
		return nil, builtinBoundary("evaluation_budget")
	}
	e.remaining--
	originalNow := now
	if e.observe != nil {
		e.observe(builtinObservation{Path: append([]int(nil), path...), Phase: "enter", Kind: n.descriptor.Kind, Previous: timeCopy(previous), Now: now})
		defer func() {
			phase := "return"
			if err != nil {
				phase = "error"
			}
			e.observe(builtinObservation{Path: append([]int(nil), path...), Phase: phase, Kind: n.descriptor.Kind, Previous: timeCopy(previous), Now: originalNow, Value: timeCopy(value), Err: err})
		}()
	}
	switch n.descriptor.Kind {
	case "and", "or":
		for {
			// Deliberately evaluate every child in order before testing any None.
			// An ordinary exception still interrupts the list comprehension immediately.
			values := make([]*time.Time, len(n.children))
			for i, child := range n.children {
				childPath := append(append([]int(nil), path...), i)
				values[i], err = e.next(child, previous, now, childPath)
				if err != nil {
					return nil, err
				}
			}
			var low, high *time.Time
			missing := false
			for _, v := range values {
				if v == nil {
					missing = true
					continue
				}
				if low == nil || e.timeCompare(*v, *low) < 0 {
					low = v
				}
				if high == nil || e.timeCompare(*v, *high) > 0 {
					high = v
				}
			}
			if n.descriptor.Kind == "or" {
				if low == nil {
					return nil, nil
				}
				return e.applyJitter(n, low)
			}
			if missing {
				return nil, nil
			}
			if low == nil {
				return nil, &BuiltinSourceError{Class: "ValueError", Message: "min() iterable argument is empty"}
			}
			if e.timeEqual(*low, *high) {
				return e.applyJitter(n, values[0])
			}
			// Original AND advances only now. Never replace previous, suppress repeated
			// children, sort the descriptor, or treat repeated results as exhaustion.
			now = *high
		}
	default:
		return e.leaf(n, previous, now)
	}
}

// Named ZoneInfo locations are interned per tree. A returned imaginary wall
// time uses a fixed-offset Go value but retains this known tzinfo identity.
func (e *builtinEvaluation) location(t time.Time) *time.Location {
	if location := e.locations[t.Location().String()]; location != nil {
		return location
	}
	return t.Location()
}
func (e *builtinEvaluation) timeCompare(a, b time.Time) int {
	if e.location(a) == e.location(b) {
		a = builtinWall(a)
		b = builtinWall(b)
	}
	if a.Before(b) {
		return -1
	}
	if a.After(b) {
		return 1
	}
	return 0
}
func (e *builtinEvaluation) timeEqual(a, b time.Time) bool {
	if e.location(a) == e.location(b) {
		return builtinWall(a) == builtinWall(b)
	}
	if !a.Equal(b) {
		return false
	}
	// Python's inter-zone equality is false when either tzinfo's UTC offset
	// depends on fold, even if the two selected instants happen to coincide.
	return !builtinFoldDependent(a, e.location(a)) && !builtinFoldDependent(b, e.location(b))
}
func builtinFoldDependent(t time.Time, location *time.Location) bool {
	wall := builtinWall(t)
	end := wall.Add(72 * time.Hour)
	for point := wall.Add(-48 * time.Hour); point.Before(end); {
		zoned := point.In(location)
		_, before := zoned.Zone()
		_, transition := zoned.ZoneBounds()
		if transition.IsZero() || !transition.After(point) {
			break
		}
		_, after := transition.In(location).Zone()
		lo := transition.UTC().Add(time.Duration(before) * time.Second)
		hi := transition.UTC().Add(time.Duration(after) * time.Second)
		if hi.Before(lo) {
			lo, hi = hi, lo
		}
		if !wall.Before(lo) && wall.Before(hi) {
			return true
		}
		point = transition
	}
	return false
}
func builtinWall(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), time.UTC)
}
