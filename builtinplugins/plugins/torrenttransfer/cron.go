// Package schedule implements the calendar semantics used by the installed
// MoviePilot plugins. It computes times; only the host may queue executions.
package torrenttransfer

import (
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata"
)

var errCron = errors.New("invalid plugin cron trigger")

// CronFields corresponds to APScheduler 3's named cron fields. Empty fields
// follow its least-significant-explicit-field defaults, including second=0
// for the five-field expressions used by the installed plugins.
type CronFields struct {
	Year      string `json:"year,omitempty"`
	Month     string `json:"month,omitempty"`
	Day       string `json:"day,omitempty"`
	Week      string `json:"week,omitempty"`
	DayOfWeek string `json:"day_of_week,omitempty"`
	Hour      string `json:"hour,omitempty"`
	Minute    string `json:"minute,omitempty"`
	Second    string `json:"second,omitempty"`
}

type numberField struct {
	min, max int
	allowed  []bool
}

func (f numberField) has(n int) bool { return n >= f.min && n <= f.max && f.allowed[n-f.min] }

func (f numberField) next(n int) int {
	if n < f.min {
		n = f.min
	}
	for ; n <= f.max; n++ {
		if f.has(n) {
			return n
		}
	}
	return -1
}

var monthNames = strings.Fields("jan feb mar apr may jun jul aug sep oct nov dec")
var weekdayNames = strings.Fields("mon tue wed thu fri sat sun")
var ordinalDay = regexp.MustCompile(`^(1st|2nd|3rd|4th|5th|last) +([0-9a-z_]+)`)
var namedRange = regexp.MustCompile(`^([a-z]+)(?:-([a-z]+))?`)

type dayPosition struct{ ordinal, weekday int }

type Cron struct {
	fields    [8]numberField
	lastDay   bool
	positions []dayPosition
	location  *time.Location
}

// ParseCrontab uses Monday=0 and AND between day-of-month/day-of-week, matching
// APScheduler 3. Unix cron parsers commonly use different meanings for both.
func ParseCrontab(expression, timezone string) (*Cron, error) {
	parts := strings.FieldsFunc(expression, sourcePythonSpace)
	if len(parts) != 5 || len(expression) > 4096 {
		return nil, errCron
	}
	return ParseCronFields(CronFields{Minute: parts[0], Hour: parts[1], Day: parts[2], Month: parts[3], DayOfWeek: parts[4]}, timezone)
}

func ParseCronFields(fields CronFields, timezone string) (*Cron, error) {
	if timezone == "" || timezone == "Local" || len(timezone) > 128 {
		return nil, errCron
	}
	location, err := time.LoadLocation(timezone)
	if err != nil {
		return nil, errCron
	}
	values := []string{fields.Year, fields.Month, fields.Day, fields.Week, fields.DayOfWeek, fields.Hour, fields.Minute, fields.Second}
	defaults := []string{"*", "1", "1", "*", "*", "0", "0", "0"}
	minimum := []int{1970, 1, 1, 1, 0, 0, 0, 0}
	maximum := []int{9999, 12, 31, 53, 6, 23, 59, 59}
	last := -1
	for i, value := range values {
		if value != "" {
			last = i
		}
	}
	c := &Cron{location: location}
	for i, value := range values {
		if value == "" {
			value = "*"
			if last >= 0 && i > last {
				value = defaults[i]
			}
		}
		if len(value) > 4096 {
			return nil, errCron
		}
		names := []string(nil)
		if i == 1 {
			names = monthNames
		} else if i == 4 {
			names = weekdayNames
		}
		f := numberField{min: minimum[i], max: maximum[i], allowed: make([]bool, maximum[i]-minimum[i]+1)}
		for _, term := range strings.Split(strings.ToLower(value), ",") {
			term = strings.TrimSpace(term)
			if i == 2 {
				if match := ordinalDay.FindStringSubmatch(term); match != nil {
					position := dayPosition{ordinal: 6, weekday: -1}
					if match[1] != "last" {
						position.ordinal = int(match[1][0] - '0')
					}
					for n, name := range weekdayNames {
						if name == match[2] {
							position.weekday = n
						}
					}
					if position.weekday == -1 {
						return nil, errCron
					}
					c.positions = append(c.positions, position)
					continue
				}
				if strings.HasPrefix(term, "last") {
					c.lastDay = true
					continue
				}
			}
			if err := parseTerm(&f, term, names); err != nil {
				return nil, err
			}
		}
		c.fields[i] = f
	}
	return c, nil
}

func parseTerm(field *numberField, term string, names []string) error {
	// APScheduler 3's named-range expressions match a prefix. In particular,
	// jan-mar/2 and mon-fri/2 retain the whole named range, not a step of two.
	// Preserve the installed implementation's behavior during migration.
	if len(names) != 0 {
		if match := namedRange.FindStringSubmatch(term); match != nil {
			first, last := -1, -1
			for i, name := range names {
				if name == match[1] {
					first = field.min + i
				}
				if name == match[2] {
					last = field.min + i
				}
			}
			if match[2] == "" {
				last = first
			}
			if first == -1 || last < first {
				return errCron
			}
			for n := first; n <= last; n++ {
				field.allowed[n-field.min] = true
			}
			return nil
		}
	}
	base, stepText, stepped := strings.Cut(term, "/")
	step := 1
	if stepped {
		var err error
		step, err = strconv.Atoi(stepText)
		if err != nil || step < 1 || step > field.max-field.min {
			return errCron
		}
	}
	value := func(s string) (int, error) {
		for i, name := range names {
			if name == s {
				return field.min + i, nil
			}
		}
		if s == "" || strings.Trim(s, "0123456789") != "" {
			return 0, errCron
		}
		n, err := strconv.Atoi(s)
		if err != nil || n < field.min || n > field.max {
			return 0, errCron
		}
		return n, nil
	}
	first, last := field.min, field.max
	if base != "*" {
		left, right, ranged := strings.Cut(base, "-")
		var err error
		first, err = value(left)
		if err != nil {
			return err
		}
		last = first
		if ranged {
			last, err = value(right)
			if err != nil {
				return err
			}
		} else if stepped {
			last = field.max
		}
	}
	span := last - first
	if last == 0 {
		span = field.max - first // APScheduler 3's range validation uses last-or-max.
	}
	if first > last || (stepped && step > span) {
		return errCron
	}
	for n := first; n <= last; n += step {
		field.allowed[n-field.min] = true
	}
	return nil
}

func (c *Cron) dayMatches(date time.Time) bool {
	year, month, day := date.Date()
	weekday := (int(date.Weekday()) + 6) % 7
	_, week := date.ISOWeek()
	if !c.fields[3].has(week) || !c.fields[4].has(weekday) {
		return false
	}
	last := time.Date(year, month+1, 0, 0, 0, 0, 0, time.UTC).Day()
	if c.fields[2].has(day) || (c.lastDay && day == last) {
		return true
	}
	for _, position := range c.positions {
		if weekday == position.weekday && ((position.ordinal == 6 && day+7 > last) || (position.ordinal == (day-1)/7+1)) {
			return true
		}
	}
	return false
}

// First is inclusive; Next is exclusive. Both DST folds are considered. A
// spring gap retains APScheduler 3's pre-transition offset when it constructs
// a not-yet-passed wall-clock time (e.g. 02:30 may execute at actual 03:30).
func (c *Cron) Next(after time.Time) time.Time { return c.First(after.Add(time.Nanosecond)) }

func (c *Cron) First(at time.Time) time.Time {
	local := at.In(c.location)
	startYear, startMonth, startDay := local.Date()
	for year := c.fields[0].next(startYear); year != -1; year = c.fields[0].next(year + 1) {
		monthMin := 1
		if year == startYear {
			monthMin = int(startMonth)
		}
		for month := c.fields[1].next(monthMin); month != -1; month = c.fields[1].next(month + 1) {
			dayMin := 1
			if year == startYear && month == int(startMonth) {
				dayMin = startDay
			}
			last := time.Date(year, time.Month(month)+1, 0, 0, 0, 0, 0, time.UTC).Day()
			for day := dayMin; day <= last; day++ {
				date := time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC)
				if !c.dayMatches(date) {
					continue
				}
				if candidate := c.firstOnDay(date, at); !candidate.IsZero() {
					return candidate
				}
			}
		}
	}
	return time.Time{}
}

func (c *Cron) firstOnDay(date, at time.Time) time.Time {
	// ZoneBounds enumerates real offset transitions, including half-hour folds
	// and skipped calendar days. Avoid assuming that every local day has 24h.
	offsets := map[int]bool{}
	end := date.Add(72 * time.Hour)
	for point := date.Add(-48 * time.Hour); point.Before(end); {
		zoned := point.In(c.location)
		_, offset := zoned.Zone()
		offsets[offset] = true
		_, next := zoned.ZoneBounds()
		if next.IsZero() || !next.After(point) {
			break
		}
		point = next
	}
	var best time.Time
	for offset := range offsets {
		base := date.Add(-time.Duration(offset) * time.Second)
		minimum := at.Unix() - base.Unix()
		if at.Nanosecond() != 0 {
			minimum++
		}
		if minimum < 0 {
			minimum = 0
		}
		if minimum > 86399 {
			continue
		}
		found := false
		for hour := c.fields[5].next(int(minimum / 3600)); hour != -1 && !found; hour = c.fields[5].next(hour + 1) {
			minuteMin := 0
			if hour == int(minimum/3600) {
				minuteMin = int(minimum % 3600 / 60)
			}
			for minute := c.fields[6].next(minuteMin); minute != -1 && !found; minute = c.fields[6].next(minute + 1) {
				secondMin := 0
				if hour == int(minimum/3600) && minute == int(minimum%3600/60) {
					secondMin = int(minimum % 60)
				}
				for second := c.fields[7].next(secondMin); second != -1; second = c.fields[7].next(second + 1) {
					candidate := base.Add(time.Duration(hour*3600+minute*60+second) * time.Second)
					local := candidate.In(c.location)
					if candidate.Before(at) {
						continue
					}
					exact := local.Year() == date.Year() && local.Month() == date.Month() && local.Day() == date.Day() && local.Hour() == hour && local.Minute() == minute && local.Second() == second
					if !exact {
						wall := date.Add(time.Duration(hour*3600+minute*60+second) * time.Second)
						actualWall := time.Date(local.Year(), local.Month(), local.Day(), local.Hour(), local.Minute(), local.Second(), 0, time.UTC)
						now := at.In(c.location)
						nowWall := time.Date(now.Year(), now.Month(), now.Day(), now.Hour(), now.Minute(), now.Second(), now.Nanosecond(), time.UTC)
						if !actualWall.After(wall) || wall.Before(nowWall) {
							continue
						}
					}
					if best.IsZero() || candidate.Before(best) {
						best = candidate
					}
					found = true
					break
				}
			}
		}
	}
	return best
}
