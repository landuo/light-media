package schedule

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Only two fixed DateTrigger constructor arguments, with explicit presence.
// UnexpectedKeyword represents a rejected input key, never a callable/registry.
type BuiltinDateArguments struct {
	RunDate           *string
	Timezone          *string
	TimezonePresent   bool
	UnexpectedKeyword *string
}

func NewBuiltinDateFromArguments(args BuiltinDateArguments, scheduler *time.Location, local func() *time.Location, limits BuiltinLimits) (*BuiltinTrigger, error) {
	if args.UnexpectedKeyword != nil {
		return nil, &BuiltinSourceError{Class: "TypeError", Message: fmt.Sprintf("DateTrigger.__init__() got an unexpected keyword argument '%s'", *args.UnexpectedKeyword)}
	}
	if scheduler == nil || local == nil {
		return nil, builtinBoundary("invalid_date_constructor_port")
	}
	location := scheduler
	if args.TimezonePresent {
		if args.Timezone == nil {
			location = local()
		} else {
			if *args.Timezone == "" || *args.Timezone == "Local" {
				return nil, builtinBoundary("date_timezone_name")
			}
			var err error
			location, err = time.LoadLocation(*args.Timezone)
			if err != nil {
				return nil, err
			}
		}
	}
	return NewBuiltinDateWithArguments(args.RunDate, location, limits)
}

// Fixed DateTrigger constructor for the host's explicit trigger=None branch.
// location is host-resolved scheduler timezone, named timezone, or actual
// process-local timezone for an explicit None. It is not a wire object/RPC.
// The constructed-trigger branch never calls this or consumes trigger kwargs.
func NewBuiltinDateWithArguments(runDate *string, location *time.Location, limits BuiltinLimits) (*BuiltinTrigger, error) {
	return newBuiltinDateWithArguments(runDate, location, limits, func(l *time.Location) time.Time { return time.Now().Truncate(time.Microsecond).In(l) })
}

var builtinDateArgumentPattern = regexp.MustCompile(`^(\d{4})-(\d{1,2})-(\d{1,2})(?:[ T](\d{1,2}):(\d{1,2}):(\d{1,2})(?:\.(\d{1,6}))?(Z|[+-]\d\d:\d\d)?)?$`)

func newBuiltinDateWithArguments(runDate *string, location *time.Location, limits BuiltinLimits, now func(*time.Location) time.Time) (*BuiltinTrigger, error) {
	if location == nil || now == nil {
		return nil, builtinBoundary("invalid_date_constructor_port")
	}
	if limits.MaxNodes <= 0 || limits.MaxDepth <= 0 || limits.MaxEvaluations <= 0 {
		return nil, builtinBoundary("explicit_evaluation_policy_required")
	}
	var value time.Time
	if runDate == nil {
		value = now(location)
	} else {
		raw := strings.TrimSuffix(*runDate, "\n") // Python regexp '$' permits one final LF.
		parts := builtinDateArgumentPattern.FindStringSubmatch(raw)
		if parts == nil {
			return nil, &BuiltinSourceError{Class: "ValueError", Message: "Invalid date string"}
		}
		number := func(i int) int { n, _ := strconv.Atoi(parts[i]); return n }
		y, m, d, h, minute, second, micro := number(1), number(2), number(3), number(4), number(5), number(6), number(7)
		invalid := func(message string) (*BuiltinTrigger, error) {
			return nil, &BuiltinSourceError{Class: "ValueError", Message: message}
		}
		if y < 1 {
			return invalid("year 0 is out of range")
		}
		if m < 1 || m > 12 {
			return invalid("month must be in 1..12")
		}
		if d < 1 || d > time.Date(y, time.Month(m)+1, 0, 0, 0, 0, 0, time.UTC).Day() {
			return invalid("day is out of range for month")
		}
		if h > 23 {
			return invalid("hour must be in 0..23")
		}
		if minute > 59 {
			return invalid("minute must be in 0..59")
		}
		if second > 59 {
			return invalid("second must be in 0..59")
		}
		wall := time.Date(y, time.Month(m), d, h, minute, second, micro*1000, time.UTC)
		if parts[8] == "Z" {
			value = wall
		} else if parts[8] != "" {
			hours, _ := strconv.Atoi(parts[8][1:3])
			minutes, _ := strconv.Atoi(parts[8][4:])
			offset := hours*3600 + minutes*60
			if parts[8][0] == '-' {
				offset = -offset
			}
			if offset <= -86400 || offset >= 86400 {
				return nil, builtinBoundary("date_offset_range")
			}
			value = time.Date(y, time.Month(m), d, h, minute, second, micro*1000, time.FixedZone("UTC"+parts[8], offset))
		} else {
			// convert_to_datetime -> localize -> normalize chooses fold0 and normalizes
			// gaps. This is the already source-verified timezone projection, not Go's
			// platform-dependent time.Date choice in an ambiguous local time.
			var err error
			value, err = (&calendarInterval{location: location}).foldZero(wall)
			if err != nil {
				return nil, err
			}
		}
	}
	if value.Year() < 1 || value.Year() > 9999 {
		return nil, calendarBoundary("date_range")
	}
	d := BuiltinDescriptor{Kind: "date", Timezone: location.String(), RunAt: value.Format(time.RFC3339Nano)}
	n := &builtinNode{descriptor: d, location: location, runAt: timeCopy(&value)}
	return &BuiltinTrigger{descriptor: d, root: n, limits: limits, locations: map[string]*time.Location{location.String(): location}}, nil
}
