package schedule

import (
	"math"
	"strings"
	"time"
)

func compileBuiltinLeaf(n *builtinNode, clock builtinConstructorClock, locations map[string]*time.Location) error {
	d := &n.descriptor
	if d.Timezone == "" || d.Timezone == "Local" || len(d.Timezone) > 128 {
		return errTrigger
	}
	location := locations[d.Timezone]
	if location == nil {
		var err error
		location, err = time.LoadLocation(d.Timezone)
		if err != nil {
			return errTrigger
		}
		locations[d.Timezone] = location
	}
	n.location = location
	parse := func(raw string) (*time.Time, error) {
		if raw == "" {
			return nil, nil
		}
		if len(raw) > 64 {
			return nil, errTrigger
		}
		// The descriptor carries canonical builtin state, not arbitrary Python
		// constructor objects. Explicit offsets remain explicit offsets; naive
		// canonical datetimes use the leaf timezone and source fold=0 localization.
		if value, err := time.Parse(time.RFC3339Nano, raw); err == nil {
			if value.Year() < 1 || value.Year() > 9999 {
				return nil, errTrigger
			}
			// time.Parse may reuse ambient time.Local when its current offset
			// happens to match. The source explicit offset is fixed tzinfo,
			// never a local zone whose future DST rules could alter arithmetic.
			_, offset := value.Zone()
			if offset == 0 {
				value = value.In(time.UTC)
			} else {
				value = value.In(time.FixedZone("UTC"+raw[len(raw)-6:], offset))
			}
			return &value, nil
		}
		raw = strings.Replace(raw, " ", "T", 1)
		layout := "2006-01-02T15:04:05.999999"
		if len(raw) == 10 {
			layout = "2006-01-02"
		}
		wall, err := time.Parse(layout, raw)
		if err != nil || wall.Year() < 1 || wall.Year() > 9999 {
			return nil, errTrigger
		}
		value, err := (&calendarInterval{location: location}).foldZero(wall)
		if err != nil {
			return nil, err
		}
		return &value, nil
	}
	var err error
	n.start, err = parse(d.StartAt)
	if err != nil {
		return err
	}
	n.end, err = parse(d.EndAt)
	if err != nil {
		return err
	}
	switch d.Kind {
	case "cron":
		if d.IntervalMS != 0 || d.RunAt != "" || d.Calendar != nil || (d.Fields == nil) == (d.Cron == "") {
			return errTrigger
		}
		if d.Fields != nil {
			n.cron, err = ParseCronFields(*d.Fields, d.Timezone)
		} else {
			n.cron, err = ParseCrontab(d.Cron, d.Timezone)
		}
		if err != nil {
			return err
		}
		n.cron.location = location
	case "interval":
		if d.Cron != "" || d.Fields != nil || d.RunAt != "" || d.Calendar != nil {
			return errTrigger
		}
		if d.IntervalMS < -(time.Duration(1<<63-1)).Milliseconds() || d.IntervalMS > (time.Duration(1<<63-1)).Milliseconds() {
			return builtinBoundary("interval_range")
		}
		if d.IntervalMS == 0 {
			d.IntervalMS = 1000
		}
		if n.start == nil {
			start, err := builtinAddWall(clock.now(location), time.Duration(d.IntervalMS)*time.Millisecond, location)
			if err != nil {
				return err
			}
			if start.Year() > 9999 {
				return calendarOverflow()
			}
			n.start = &start
			d.StartAt = start.Format(time.RFC3339Nano)
		}
	case "date":
		if d.Cron != "" || d.Fields != nil || d.IntervalMS != 0 || d.Calendar != nil || d.StartAt != "" || d.EndAt != "" || d.Jitter != nil {
			return errTrigger
		}
		n.runAt, err = parse(d.RunAt)
		if err != nil {
			return err
		}
		if n.runAt == nil {
			v := clock.now(location)
			n.runAt = &v
			d.RunAt = v.Format(time.RFC3339Nano)
		}
	case "calendarinterval":
		if d.Calendar == nil || d.Cron != "" || d.Fields != nil || d.IntervalMS != 0 || d.RunAt != "" || d.StartAt != "" || d.EndAt != "" {
			return errTrigger
		}
		jitter := 0.0
		if d.Jitter != nil {
			jitter = *d.Jitter
		}
		n.calendar, err = newBuiltinCalendar(*d.Calendar, d.Timezone, clock, jitter)
		if err != nil {
			return err
		}
		n.calendar.location = location
		snapshot := cloneCalendarSpec(n.calendar.spec)
		d.Calendar = &snapshot
	}
	return nil
}

func (e *builtinEvaluation) leaf(n *builtinNode, previous *time.Time, now time.Time) (*time.Time, error) {
	switch n.descriptor.Kind {
	case "date":
		if previous != nil {
			return nil, nil
		}
		return timeCopy(n.runAt), nil
	case "interval":
		return e.interval(n, previous, now)
	case "cron":
		start := now
		if previous != nil {
			after := previous.Add(time.Microsecond)
			if after.Year() > 9999 {
				return nil, calendarOverflow()
			}
			if after.Before(start) {
				start = after
			}
			start = start.In(n.location)
			if e.timeEqual(start, *previous) {
				start = start.Add(time.Microsecond)
			}
		} else if n.start != nil && n.start.After(start) {
			start = *n.start
		}
		// The established cron parser/field search is reused; only the newly needed
		// original (previous,now) selection, zone projection and RNG layer live here.
		candidate, err := n.cron.builtinFirst(e.ctx, start)
		if err != nil {
			return nil, err
		}
		if candidate == nil {
			return nil, nil
		}
		value := *candidate
		if n.end != nil && e.timeCompare(value, *n.end) > 0 {
			return nil, nil
		}
		next, err := e.applyJitter(n, &value)
		if err != nil {
			return nil, err
		}
		if n.end != nil && e.timeCompare(*next, *n.end) > 0 {
			return timeCopy(n.end), nil
		}
		return next, nil
	case "calendarinterval":
		return e.calendar(n, previous)
	}
	return nil, errTrigger
}

func (e *builtinEvaluation) interval(n *builtinNode, previous *time.Time, now time.Time) (*time.Time, error) {
	// Original Interval uses binary64 timestamp arithmetic (not datetime wall
	// addition). Keep the same operation order before the final microsecond round.
	stamp := func(t time.Time) float64 { return float64(t.Unix()) + float64(t.Nanosecond())/1e9 }
	length := float64(n.descriptor.IntervalMS) / 1000
	var timestamp float64
	if previous != nil {
		timestamp = stamp(*previous) + length
	} else if e.timeCompare(*n.start, now) > 0 {
		timestamp = stamp(*n.start)
	} else {
		timestamp = stamp(*n.start) + length*math.Ceil((stamp(now)-stamp(*n.start))/length)
	}
	if n.descriptor.Jitter != nil {
		draw, err := e.draw(*n.descriptor.Jitter)
		if err != nil {
			return nil, err
		}
		timestamp += draw
	}
	if n.end != nil && timestamp > stamp(*n.end) {
		return nil, nil
	}
	whole, fraction := math.Modf(timestamp)
	if whole < -62135596800 || whole > 253402300799 {
		return nil, calendarOverflow()
	}
	micros := math.RoundToEven(fraction * 1e6)
	value := time.Unix(int64(whole), int64(micros)*1000).In(n.location)
	if value.Year() < 1 || value.Year() > 9999 {
		return nil, calendarOverflow()
	}
	return &value, nil
}

// Unlike the legacy Calendar checked adapter, this operation uses a pointer for
// presence and does not reject a valid 0001 UTC value or infer absence from it.
func (e *builtinEvaluation) calendar(n *builtinNode, previous *time.Time) (*time.Time, error) {
	c := n.calendar
	date := c.start
	var err error
	if previous != nil {
		date, err = e.calendarAdvance(c, *previous)
		if err != nil {
			return nil, err
		}
	}
	for {
		if err = e.ctx.Err(); err != nil {
			return nil, err
		}
		if c.spec.EndDate != nil && date.After(c.end) {
			return nil, nil
		}
		wall := time.Date(date.Year(), date.Month(), date.Day(), c.spec.Hour, c.spec.Minute, c.spec.Second, 0, time.UTC)
		normalized, err := c.foldZero(wall)
		if err != nil {
			return nil, err
		}
		if normalized.Year() < 1 || normalized.Year() > 9999 {
			return nil, calendarBoundary("timezone_date_range")
		}
		if normalized.Hour() != c.spec.Hour || normalized.Minute() != c.spec.Minute || normalized.Second() != c.spec.Second || normalized.Nanosecond() != 0 {
			date, err = e.calendarAdvance(c, normalized)
			if err != nil {
				return nil, err
			}
			continue
		}
		return e.applyJitter(n, &normalized)
	}
}

func (e *builtinEvaluation) draw(jitter float64) (float64, error) {
	if err := e.ctx.Err(); err != nil {
		return 0, err
	}
	value := e.uniform(0, jitter)
	lo, hi := 0.0, jitter
	if hi < lo {
		lo, hi = hi, lo
	}
	if math.IsNaN(value) || math.IsInf(value, 0) || value < lo || value > hi {
		return 0, builtinBoundary("uniform_port")
	}
	return value, nil
}
func (e *builtinEvaluation) applyJitter(n *builtinNode, value *time.Time) (*time.Time, error) {
	if value == nil || n.descriptor.Jitter == nil || *n.descriptor.Jitter == 0 {
		return timeCopy(value), nil
	}
	draw, err := e.draw(*n.descriptor.Jitter)
	if err != nil {
		return nil, err
	}
	micros := math.RoundToEven(draw * 1e6)
	if math.Abs(micros) >= float64(math.MaxInt64/int64(time.Microsecond)) {
		return nil, builtinBoundary("jitter_range")
	}
	result, err := builtinAddWall(*value, time.Duration(micros)*time.Microsecond, e.location(*value))
	if err != nil {
		return nil, err
	}
	return &result, nil
}
