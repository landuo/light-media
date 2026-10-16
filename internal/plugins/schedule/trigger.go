package schedule

import (
	"errors"
	"time"
)

// Spec is a trigger definition, not an execution grant. The host binds every
// registration to the actual instance, revision, action and permitted inputs.
type Spec struct {
	Kind       string      `json:"kind"`
	Timezone   string      `json:"timezone"`
	Cron       string      `json:"cron,omitempty"`
	Fields     *CronFields `json:"fields,omitempty"`
	IntervalMS int64       `json:"interval_ms,omitempty"`
	RunAt      string      `json:"run_at,omitempty"`
	StartAt    string      `json:"start_at,omitempty"`
	EndAt      string      `json:"end_at,omitempty"`
}

type Trigger struct {
	spec       Spec
	cron       *Cron
	start, end time.Time
	runAt      time.Time
}

var errTrigger = errors.New("invalid plugin schedule trigger")

// CanonicalTime has a fixed-width fractional part, allowing durable UTC due
// times to sort correctly even outside the Unix-nanosecond range (year 2262).
const CanonicalTime = "2006-01-02T15:04:05.000000000Z"

func FormatTime(t time.Time) string { return t.UTC().Format(CanonicalTime) }

func New(spec Spec, anchor time.Time) (*Trigger, error) {
	if anchor.IsZero() || anchor.Year() < 1 || anchor.Year() > 9999 || spec.Timezone == "" || spec.Timezone == "Local" || len(spec.Timezone) > 128 {
		return nil, errTrigger
	}
	if _, err := time.LoadLocation(spec.Timezone); err != nil {
		return nil, errTrigger
	}
	t := &Trigger{spec: spec}
	readTime := func(raw string) (time.Time, error) {
		if len(raw) > 64 {
			return time.Time{}, errTrigger
		}
		value, err := time.Parse(time.RFC3339Nano, raw)
		if err != nil || value.Year() < 1 || value.Year() > 9999 {
			return time.Time{}, errTrigger
		}
		return value.UTC(), nil
	}
	var err error
	if spec.StartAt != "" {
		t.start, err = readTime(spec.StartAt)
		if err != nil {
			return nil, err
		}
		t.spec.StartAt = FormatTime(t.start)
	}
	if spec.EndAt != "" {
		t.end, err = readTime(spec.EndAt)
		if err != nil {
			return nil, err
		}
		t.spec.EndAt = FormatTime(t.end)
	}
	if !t.start.IsZero() && !t.end.IsZero() && t.end.Before(t.start) {
		return nil, errTrigger
	}
	switch spec.Kind {
	case "cron":
		if spec.IntervalMS != 0 || spec.RunAt != "" || (spec.Cron == "") == (spec.Fields == nil) {
			return nil, errTrigger
		}
		if spec.Fields != nil {
			t.cron, err = ParseCronFields(*spec.Fields, spec.Timezone)
			copy := *spec.Fields
			t.spec.Fields = &copy
		} else {
			t.cron, err = ParseCrontab(spec.Cron, spec.Timezone)
		}
		if err != nil {
			return nil, err
		}
	case "interval":
		if spec.Cron != "" || spec.Fields != nil || spec.RunAt != "" || spec.IntervalMS < 0 || spec.IntervalMS > (time.Duration(1<<63-1)).Milliseconds() {
			return nil, errTrigger
		}
		if spec.IntervalMS == 0 {
			t.spec.IntervalMS = 1000 // APScheduler's zero interval is one second.
		}
		if t.start.IsZero() {
			t.start = anchor.Add(time.Duration(t.spec.IntervalMS) * time.Millisecond)
			if t.start.Year() > 9999 {
				return nil, errTrigger
			}
			t.spec.StartAt = FormatTime(t.start)
		}
	case "date":
		if spec.Cron != "" || spec.Fields != nil || spec.IntervalMS != 0 || spec.RunAt == "" || spec.StartAt != "" || spec.EndAt != "" {
			return nil, errTrigger
		}
		t.runAt, err = readTime(spec.RunAt)
		if err != nil {
			return nil, err
		}
		t.spec.RunAt = FormatTime(t.runAt)
	default:
		return nil, errTrigger
	}
	return t, nil
}

func (t *Trigger) Spec() Spec {
	copy := t.spec
	if copy.Fields != nil {
		fields := *copy.Fields
		copy.Fields = &fields
	}
	return copy
}

// First computes an inclusive first occurrence. A date trigger remains its
// original instant when late: the host applies its persisted misfire policy.
func (t *Trigger) First(at time.Time) time.Time {
	if t.spec.Kind == "date" {
		return t.runAt
	}
	if at.Before(t.start) {
		at = t.start
	}
	var next time.Time
	if t.spec.Kind == "cron" {
		next = t.cron.First(at)
	} else {
		seconds := at.Unix() - t.start.Unix()
		nanos := at.Nanosecond() - t.start.Nanosecond()
		if nanos < 0 {
			seconds--
			nanos += int(time.Second)
		}
		milliseconds := seconds*1000 + int64(nanos)/int64(time.Millisecond)
		count := milliseconds / t.spec.IntervalMS
		if milliseconds%t.spec.IntervalMS != 0 || int64(nanos)%int64(time.Millisecond) != 0 {
			count++
		}
		elapsed := count * t.spec.IntervalMS
		next = time.Unix(t.start.Unix()+elapsed/1000, int64(t.start.Nanosecond())+elapsed%1000*int64(time.Millisecond)).UTC()
	}
	if !next.IsZero() && (next.Year() > 9999 || (!t.end.IsZero() && next.After(t.end))) {
		return time.Time{}
	}
	return next.UTC()
}

func (t *Trigger) Next(previous time.Time) time.Time {
	if t.spec.Kind == "date" {
		return time.Time{}
	}
	return t.First(previous.Add(time.Nanosecond))
}
