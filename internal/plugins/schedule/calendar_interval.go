package schedule

// CalendarInterval follows the consumed APScheduler 3.11.2 calendarinterval
// algorithm. The original MIT notice is retained with this slice. No source
// runtime, tzlocal discovery or additional scheduling authority is introduced.
import (
	"bytes"
	"encoding/json"
	"math"
	"math/rand"
	"strconv"
	"time"
)

type CalendarIntervalSpec struct {
	Years     int64   `json:"years,omitempty"`
	Months    int64   `json:"months,omitempty"`
	Weeks     int64   `json:"weeks,omitempty"`
	Days      int64   `json:"days,omitempty"`
	Hour      int     `json:"hour,omitempty"`
	Minute    int     `json:"minute,omitempty"`
	Second    int     `json:"second,omitempty"`
	StartDate *string `json:"start_date,omitempty"`
	EndDate   *string `json:"end_date,omitempty"`
}

// Null integers are not the source constructor's omitted integer defaults.
// This strictness is confined to the newly introduced calendar object.
func (s *CalendarIntervalSpec) UnmarshalJSON(raw []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return errTrigger
	}
	var out CalendarIntervalSpec
	for key, value := range fields {
		switch key {
		case "years", "months", "weeks", "days":
			if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
				return errTrigger
			}
			var n int64
			if err := json.Unmarshal(value, &n); err != nil {
				return errTrigger
			}
			switch key {
			case "years":
				out.Years = n
			case "months":
				out.Months = n
			case "weeks":
				out.Weeks = n
			case "days":
				out.Days = n
			}
		case "hour", "minute", "second":
			if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
				return errTrigger
			}
			var n int
			if err := json.Unmarshal(value, &n); err != nil {
				return errTrigger
			}
			switch key {
			case "hour":
				out.Hour = n
			case "minute":
				out.Minute = n
			case "second":
				out.Second = n
			}
		case "start_date":
			if err := json.Unmarshal(value, &out.StartDate); err != nil {
				return errTrigger
			}
		case "end_date":
			if err := json.Unmarshal(value, &out.EndDate); err != nil {
				return errTrigger
			}
		default:
			return errTrigger
		}
	}
	*s = out
	return nil
}

func cloneCalendarSpec(s CalendarIntervalSpec) CalendarIntervalSpec {
	if s.StartDate != nil {
		v := *s.StartDate
		s.StartDate = &v
	}
	if s.EndDate != nil {
		v := *s.EndDate
		s.EndDate = &v
	}
	return s
}

// CalendarIntervalBoundaryError marks a finite target boundary, not original
// exhaustion (None) or a claim that the source constructor rejects this input.
type CalendarIntervalBoundaryError struct{ Code string }

func (e *CalendarIntervalBoundaryError) Error() string {
	return "calendar interval finite boundary: " + e.Code
}

// CalendarIntervalSourceError is a bounded ordinary exception from a known
// source operation outside the source date-construction retry block.
type CalendarIntervalSourceError struct{ Class, Message string }

func (e *CalendarIntervalSourceError) Error() string { return e.Class + ": " + e.Message }

// The observed ordinary exception arguments are the singleton (Message,).
// No generic Python object/exception serialization is exposed.
func calendarValueError(message string) error {
	return &CalendarIntervalSourceError{Class: "ValueError", Message: message}
}
func calendarOverflow() error {
	return &CalendarIntervalSourceError{Class: "OverflowError", Message: "date value out of range"}
}

func calendarBoundary(code string) error { return &CalendarIntervalBoundaryError{Code: code} }

type calendarInterval struct {
	spec         CalendarIntervalSpec
	location     *time.Location
	start, end   time.Time // civil dates represented in UTC for date arithmetic only
	months, days int64
}

func newCalendarInterval(s CalendarIntervalSpec, zone string, anchor time.Time, jitter float64) (*calendarInterval, error) {
	if s.Years < 0 || s.Months < 0 || s.Weeks < 0 || s.Days < 0 || jitter < 0 {
		return nil, calendarBoundary("negative_or_nonforward")
	}
	if s.Years > 9999 || s.Months > 119988 || s.Weeks > 521722 || s.Days > 3652058 {
		return nil, calendarBoundary("interval_range")
	}
	months := s.Years*12 + s.Months
	days := s.Weeks*7 + s.Days
	if months > 119988 || days > 3652058 {
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
		y, m, d := anchor.In(time.Local).Date()
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

func (c *calendarInterval) advance(previous time.Time) (time.Time, error) {
	year, month, day := previous.Date()
	if year < 1 || year > 9999 {
		return time.Time{}, calendarBoundary("previous_date_range")
	}
	index := int64(year-1)*12 + int64(month) - 1
	for {
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

// foldZero maps a civil wall time using ZoneInfo's fold=0 convention. Exact
// folds choose the earlier instant. In a gap the pre-transition offset is
// retained; the caller decides whether to normalize or preserve that wall time.
func (c *calendarInterval) foldZero(wall time.Time) (time.Time, error) {
	offsets := map[int]bool{}
	var gapOffset int
	gap := false
	end := wall.Add(72 * time.Hour)
	for point := wall.Add(-48 * time.Hour); point.Before(end); {
		_, offset := point.In(c.location).Zone()
		offsets[offset] = true
		_, transition := point.In(c.location).ZoneBounds()
		if transition.IsZero() || !transition.After(point) {
			break
		}
		_, after := transition.In(c.location).Zone()
		if after > offset {
			lo := transition.UTC().Add(time.Duration(offset) * time.Second)
			hi := transition.UTC().Add(time.Duration(after) * time.Second)
			if !wall.Before(lo) && wall.Before(hi) {
				gap = true
				gapOffset = offset
			}
		}
		point = transition
	}
	var best time.Time
	found := false
	for offset := range offsets {
		candidate := wall.Add(-time.Duration(offset) * time.Second)
		local := candidate.In(c.location)
		if local.Year() == wall.Year() && local.Month() == wall.Month() && local.Day() == wall.Day() && local.Hour() == wall.Hour() && local.Minute() == wall.Minute() && local.Second() == wall.Second() && local.Nanosecond() == wall.Nanosecond() {
			if !found || candidate.Before(best) {
				best = candidate
				found = true
			}
		}
	}
	if found {
		return best.In(c.location), nil
	}
	if gap {
		return wall.Add(-time.Duration(gapOffset) * time.Second).In(c.location), nil
	}
	return time.Time{}, calendarBoundary("timezone_transition")
}

func (c *calendarInterval) withJitter(at time.Time, jitter float64, draw func() float64) (time.Time, error) {
	if jitter == 0 {
		return at, nil
	}
	fraction := draw()
	if math.IsNaN(fraction) || fraction < 0 || fraction > 1 {
		return time.Time{}, calendarBoundary("jitter_draw")
	}
	micros := math.RoundToEven(fraction * jitter * 1e6)
	if math.IsInf(micros, 0) || micros >= float64(math.MaxInt64/int64(time.Microsecond)) {
		return time.Time{}, calendarBoundary("jitter_range")
	}
	// Python aware datetime + timedelta uses wall arithmetic, not UTC elapsed
	// arithmetic. In particular, the resulting wall time may be in a DST gap.
	wall := time.Date(at.Year(), at.Month(), at.Day(), at.Hour(), at.Minute(), at.Second(), at.Nanosecond(), time.UTC).Add(time.Duration(micros) * time.Microsecond)
	if wall.Year() < 1 || wall.Year() > 9999 {
		return time.Time{}, calendarOverflow()
	}
	normalized, err := c.foldZero(wall)
	if err != nil {
		return time.Time{}, err
	}
	// Preserve the original civil fields when Python returns an imaginary time;
	// a fixed-offset representation retains both those fields and its timestamp.
	offset := int(wall.Unix() - normalized.Unix())
	result := time.Date(wall.Year(), wall.Month(), wall.Day(), wall.Hour(), wall.Minute(), wall.Second(), wall.Nanosecond(), time.FixedZone(c.location.String(), offset))
	return result, nil
}

func (c *calendarInterval) next(previous *time.Time, jitter float64, draw func() float64) (time.Time, error) {
	date := c.start
	var err error
	if previous != nil {
		date, err = c.advance(*previous)
		if err != nil {
			return time.Time{}, err
		}
	}
	for {
		if c.spec.EndDate != nil && date.After(c.end) {
			return time.Time{}, nil
		}
		wall := time.Date(date.Year(), date.Month(), date.Day(), c.spec.Hour, c.spec.Minute, c.spec.Second, 0, time.UTC)
		normalized, e := c.foldZero(wall)
		if e != nil {
			return time.Time{}, e
		}
		if normalized.Year() < 1 || normalized.Year() > 9999 {
			return time.Time{}, calendarBoundary("timezone_date_range")
		}
		// Original code compares timetz only, not the civil date. A full-day
		// transition can therefore return the next date with the same clock.
		if normalized.Hour() != c.spec.Hour || normalized.Minute() != c.spec.Minute || normalized.Second() != c.spec.Second || normalized.Nanosecond() != 0 {
			date, err = c.advance(normalized)
			if err != nil {
				return time.Time{}, err
			}
			continue
		}
		result, e := c.withJitter(normalized, jitter, draw)
		if e != nil {
			return time.Time{}, e
		}
		if result.IsZero() {
			return time.Time{}, calendarBoundary("zero_time_sentinel")
		}
		if previous != nil && !result.After(*previous) {
			return time.Time{}, calendarBoundary("nonprogress")
		}
		return result, nil
	}
}

// CalendarTrigger has no unchecked First/Next methods. Thus an original
// calendar exception cannot be silently mapped to the zero-time None sentinel
// through the old three-kind Trigger API.
type CalendarTrigger struct {
	spec         Spec
	calendar     *calendarInterval
	jitterRandom func() float64
}

func NewCalendar(spec Spec, anchor time.Time) (*CalendarTrigger, error) {
	if spec.Kind != "calendarinterval" || spec.Calendar == nil || spec.Cron != "" || spec.Fields != nil || spec.IntervalMS != 0 || spec.RunAt != "" || spec.StartAt != "" || spec.EndAt != "" {
		return nil, errTrigger
	}
	if anchor.IsZero() || anchor.Year() < 1 || anchor.Year() > 9999 || spec.Timezone == "" || spec.Timezone == "Local" || len(spec.Timezone) > 128 {
		return nil, errTrigger
	}
	if math.IsNaN(spec.JitterSeconds) || math.IsInf(spec.JitterSeconds, 0) || math.Abs(spec.JitterSeconds) > float64(math.MaxInt64)/float64(time.Second) {
		return nil, errTrigger
	}
	calendar, err := newCalendarInterval(*spec.Calendar, spec.Timezone, anchor, spec.JitterSeconds)
	if err != nil {
		return nil, err
	}
	copy := cloneCalendarSpec(calendar.spec)
	spec.Calendar = &copy
	return &CalendarTrigger{spec: spec, calendar: calendar, jitterRandom: rand.Float64}, nil
}
func (t *CalendarTrigger) Spec() Spec {
	copy := t.spec
	calendar := cloneCalendarSpec(*copy.Calendar)
	copy.Calendar = &calendar
	return copy
}
func (t *CalendarTrigger) FirstChecked(at time.Time) (time.Time, error) {
	return t.calendar.next(nil, t.spec.JitterSeconds, t.jitterRandom)
}
func (t *CalendarTrigger) NextChecked(previous time.Time) (time.Time, error) {
	return t.calendar.next(&previous, t.spec.JitterSeconds, t.jitterRandom)
}

// Old kinds are exact adapters around their original computation paths.
func (t *Trigger) FirstChecked(at time.Time) (time.Time, error)      { return t.First(at), nil }
func (t *Trigger) NextChecked(previous time.Time) (time.Time, error) { return t.Next(previous), nil }
