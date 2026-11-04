package schedule

import (
	"strconv"
	"time"
)

// This is the narrow source _create_trigger(None) route. A nil descriptor is
// None, not an empty object. The scheduler timezone is an explicit host input.
// The two ordinary constructor clocks stay distinct: datetime.now(trigger tz)
// versus date.today() in the process-local zone. No title/parser/provider work.
func NewBuiltinWithDefaults(d *BuiltinDescriptor, schedulerTimezone string, limits BuiltinLimits) (*BuiltinTrigger, error) {
	return newBuiltinWithClock(d, schedulerTimezone, limits, builtinConstructorClock{
		now:       func(location *time.Location) time.Time { return time.Now().Truncate(time.Microsecond).In(location) },
		localDate: func() time.Time { y, m, day := time.Now().Date(); return time.Date(y, m, day, 0, 0, 0, 0, time.UTC) },
	})
}

type builtinConstructorClock struct {
	now       func(*time.Location) time.Time
	localDate func() time.Time
}

func builtinAddWall(at time.Time, delta time.Duration, location *time.Location) (time.Time, error) {
	wall := builtinWall(at).Add(delta)
	if wall.Year() < 1 || wall.Year() > 9999 {
		return time.Time{}, calendarOverflow()
	}
	normalized, err := (&calendarInterval{location: location}).foldZero(wall)
	if err != nil {
		return time.Time{}, err
	}
	if builtinWall(normalized) == wall {
		return normalized, nil
	}
	offset := int(wall.Unix() - normalized.Unix())
	return time.Date(wall.Year(), wall.Month(), wall.Day(), wall.Hour(), wall.Minute(), wall.Second(), wall.Nanosecond(), time.FixedZone(location.String(), offset)), nil
}

// Unlike the old standalone adapter, signed finite values are not blanket
// rejected. A single Next may return an earlier datetime. Bounded due collection
// is the host policy boundary; target ranges remain explicit, not source errors.
func newBuiltinCalendar(s CalendarIntervalSpec, zone string, clock builtinConstructorClock, jitter float64) (*calendarInterval, error) {
	if s.Years < -9999 || s.Years > 9999 || s.Months < -119988 || s.Months > 119988 || s.Weeks < -521722 || s.Weeks > 521722 || s.Days < -3652058 || s.Days > 3652058 {
		return nil, calendarBoundary("interval_range")
	}
	months := s.Years*12 + s.Months
	days := s.Weeks*7 + s.Days
	if months < -119988 || months > 119988 || days < -3652058 || days > 3652058 {
		return nil, calendarBoundary("interval_range")
	}
	location, err := time.LoadLocation(zone)
	if err != nil {
		return nil, errTrigger
	}
	c := &calendarInterval{spec: cloneCalendarSpec(s), location: location, months: months, days: days}
	parseDate := func(raw string) (time.Time, error) {
		if len(raw) != 10 || raw[4] != '-' || raw[7] != '-' {
			return time.Time{}, calendarBoundary("noncanonical_date")
		}
		for i, ch := range raw {
			if i != 4 && i != 7 && (ch < '0' || ch > '9') {
				return time.Time{}, calendarBoundary("noncanonical_date")
			}
		}
		year, _ := strconv.Atoi(raw[:4])
		month, _ := strconv.Atoi(raw[5:7])
		day, _ := strconv.Atoi(raw[8:])
		if year < 1 {
			return time.Time{}, calendarValueError("year 0 is out of range")
		}
		if month < 1 || month > 12 {
			return time.Time{}, calendarValueError("month must be in 1..12")
		}
		if day < 1 || day > time.Date(year, time.Month(month)+1, 0, 0, 0, 0, 0, time.UTC).Day() {
			return time.Time{}, calendarValueError("day is out of range for month")
		}
		return time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC), nil
	}
	if s.StartDate == nil {
		y, m, d := clock.localDate().Date()
		if y < 1 || y > 9999 {
			return nil, calendarBoundary("default_date_range")
		}
		c.start = time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	} else {
		c.start, err = parseDate(*s.StartDate)
		if err != nil {
			return nil, err
		}
	}
	// Spec returns the resolved constructor snapshot, not a fresh default date.
	start := c.start.Format("2006-01-02")
	c.spec.StartDate = &start
	if s.EndDate != nil {
		c.end, err = parseDate(*s.EndDate)
		if err != nil {
			return nil, err
		}
	}
	if s.Hour < 0 || s.Hour > 23 {
		return nil, calendarValueError("hour must be in 0..23")
	}
	if s.Minute < 0 || s.Minute > 59 {
		return nil, calendarValueError("minute must be in 0..59")
	}
	if s.Second < 0 || s.Second > 59 {
		return nil, calendarValueError("second must be in 0..59")
	}
	if s.Years == 0 && s.Months == 0 && s.Weeks == 0 && s.Days == 0 {
		return nil, calendarValueError("interval must be at least 1 day long")
	}
	if s.EndDate != nil && c.end.Before(c.start) {
		return nil, calendarValueError("end_date cannot be earlier than start_date")
	}
	return c, nil
}

func (e *builtinEvaluation) calendarAdvance(c *calendarInterval, previous time.Time) (time.Time, error) {
	year, month, day := previous.Date()
	if year < 1 || year > 9999 {
		return time.Time{}, calendarBoundary("previous_date_range")
	}
	index := int64(year-1)*12 + int64(month) - 1
	for {
		if err := e.ctx.Err(); err != nil {
			return time.Time{}, err
		}
		if e.remaining <= 0 {
			return time.Time{}, builtinBoundary("evaluation_budget")
		}
		e.remaining--
		index += c.months
		if index < 0 || index >= 9999*12 {
			return time.Time{}, calendarBoundary("year_range")
		}
		year = int(index/12) + 1
		month = time.Month(index%12 + 1)
		if day > time.Date(year, month+1, 0, 0, 0, 0, 0, time.UTC).Day() {
			continue
		}
		date := time.Date(year, month, day, 0, 0, 0, 0, time.UTC).AddDate(0, 0, int(c.days))
		if date.Year() < 1 || date.Year() > 9999 {
			return time.Time{}, calendarOverflow()
		}
		return date, nil
	}
}
