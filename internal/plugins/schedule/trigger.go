package schedule

import (
	"errors"
	"fmt"
	"math"
	"math/big"
	"math/rand"
	"strconv"
	"time"
)

// Spec is a trigger definition, not an execution grant. The host binds every
// registration to the actual instance, revision, action and permitted inputs.
type Spec struct {
	Kind          string      `json:"kind"`
	Timezone      string      `json:"timezone,omitempty"`
	LocalTimezone bool        `json:"local_timezone,omitempty"`
	Cron          string      `json:"cron,omitempty"`
	Fields        *CronFields `json:"fields,omitempty"`
	IntervalMS    int64       `json:"interval_ms,omitempty"`
	// Only source_interval accepts this canonical float64 roundtrip text.
	// It preserves nonfinite float values without relaxing standard JSON.
	SourceIntervalHours *string `json:"source_interval_hours,omitempty"`
	SourceStartLocal    bool    `json:"source_start_local,omitempty"`
	RunAt               string  `json:"run_at,omitempty"`
	StartAt             string  `json:"start_at,omitempty"`
	EndAt               string  `json:"end_at,omitempty"`
	JitterSeconds       float64 `json:"jitter,omitempty"`
}

type Trigger struct {
	spec           Spec
	cron           *Cron
	start, end     time.Time
	runAt          time.Time
	jitterRandom   func() float64
	sourceInterval *sourceIntervalState
}

var errTrigger = errors.New("invalid plugin schedule trigger")

// CanonicalTime has a fixed-width fractional part, allowing durable UTC due
// times to sort correctly even outside the Unix-nanosecond range (year 2262).
const CanonicalTime = "2006-01-02T15:04:05.000000000Z"

func FormatTime(t time.Time) string { return t.UTC().Format(CanonicalTime) }

// Location resolves an explicit IANA name or the host's system-local zone.
// LocalTimezone carries no path or zone data and is mutually exclusive with
// Timezone. The legacy string "Local" remains invalid.
func (spec Spec) Location() (*time.Location, error) {
	if spec.LocalTimezone {
		if spec.Timezone != "" {
			return nil, errTrigger
		}
		return systemLocalLocation()
	}
	if spec.Timezone == "" || spec.Timezone == "Local" || len(spec.Timezone) > 128 {
		return nil, errTrigger
	}
	location, err := time.LoadLocation(spec.Timezone)
	if err != nil {
		return nil, errTrigger
	}
	return location, nil
}

func New(spec Spec, anchor time.Time) (*Trigger, error) {
	if (anchor.IsZero() && spec.Kind != "source_interval") || anchor.Year() < 1 || anchor.Year() > 9999 {
		return nil, errTrigger
	}
	location, err := spec.Location()
	if err != nil {
		return nil, err
	}
	if math.IsNaN(spec.JitterSeconds) || math.IsInf(spec.JitterSeconds, 0) || (spec.Kind != "source_interval" && math.Abs(spec.JitterSeconds) > float64(math.MaxInt64)/float64(time.Second)) {
		return nil, errTrigger
	}
	t := &Trigger{spec: spec, jitterRandom: rand.Float64}
	if spec.Kind == "source_interval" {
		if spec.SourceIntervalHours == nil || len(*spec.SourceIntervalHours) > 32 {
			return nil, errTrigger
		}
		hours, err := strconv.ParseFloat(*spec.SourceIntervalHours, 64)
		if err != nil || strconv.FormatFloat(hours, 'g', -1, 64) != *spec.SourceIntervalHours {
			return nil, errTrigger
		}
		text := *spec.SourceIntervalHours
		t.spec.SourceIntervalHours = &text
		us, err := SourceIntervalUSFromHours(hours)
		if err != nil {
			return nil, err
		}
		return newSourceInterval(t, anchor, location, us)
	}
	if spec.SourceIntervalHours != nil || spec.SourceStartLocal {
		return nil, errTrigger
	}

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
			t.cron, err = parseCronFieldsLocation(*spec.Fields, location)
			copy := *spec.Fields
			t.spec.Fields = &copy
		} else {
			t.cron, err = parseCrontabLocation(spec.Cron, location)
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
		if spec.Cron != "" || spec.Fields != nil || spec.IntervalMS != 0 || spec.RunAt == "" || spec.StartAt != "" || spec.EndAt != "" || spec.JitterSeconds != 0 {
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
	if copy.SourceIntervalHours != nil {
		v := *copy.SourceIntervalHours
		copy.SourceIntervalHours = &v
	}

	if copy.Fields != nil {
		fields := *copy.Fields
		copy.Fields = &fields
	}
	return copy
}

// First computes an inclusive first occurrence. A date trigger remains its
// original instant when late: the host applies its persisted misfire policy.
func (t *Trigger) First(at time.Time) time.Time {
	if t.sourceInterval != nil {
		next, present, err := t.SourceFirst(at, true)
		if err != nil || !present {
			return time.Time{}
		}
		return next.UTC()
	}

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
	if next.IsZero() || next.Year() > 9999 || (!t.end.IsZero() && next.After(t.end)) {
		return time.Time{}
	}
	next = t.withJitter(next)
	if t.spec.Kind == "cron" && !t.end.IsZero() && next.After(t.end) {
		next = t.end
	}
	if t.spec.Kind == "interval" && !t.end.IsZero() && next.After(t.end) {
		return time.Time{}
	}
	return next.UTC()
}

func (t *Trigger) Next(previous time.Time) time.Time {
	if t.sourceInterval != nil {
		next, present, err := t.SourceNext(previous)
		if err != nil || !present {
			return time.Time{}
		}
		return next.UTC()
	}

	if t.spec.Kind == "date" {
		return time.Time{}
	}
	if t.spec.Kind == "interval" {
		next := t.withJitter(previous.Add(time.Duration(t.spec.IntervalMS) * time.Millisecond))
		if next.Year() > 9999 || (!t.end.IsZero() && next.After(t.end)) {
			return time.Time{}
		}
		return next
	}
	return t.First(previous.Add(time.Nanosecond))
}

// APScheduler uses uniform(0,jitter); timedelta/fromtimestamp round the result
// to microseconds. The host's RNG is native Go, with the same uniform mapping.
func (t *Trigger) withJitter(at time.Time) time.Time {
	if t.spec.JitterSeconds == 0 {
		return at
	}
	micros := math.RoundToEven(t.jitterRandom() * t.spec.JitterSeconds * 1e6)
	return at.Add(time.Duration(micros) * time.Microsecond)
}

// SourceIntervalError reports an actual Python conversion/calculation error.
// A negative or non-progressing interval is valid state and never gets this
// error merely because an executor cannot make progress.
type SourceIntervalError struct{ Kind, Message string }

func (e *SourceIntervalError) Error() string { return e.Message }
func sourceError(kind, message string) error { return &SourceIntervalError{kind, message} }

type sourceIntervalState struct {
	location                     *time.Location
	stepUS                       *big.Int
	length, startFloat, endFloat float64
	hasEnd, startLocal           bool
	startWall                    time.Time
}

var sourceDayUS = big.NewInt(86400000000)
var sourceSecondUS = big.NewInt(1000000)

func sourceDeltaRange(us *big.Int) error {
	days, rem := new(big.Int), new(big.Int)
	days.QuoRem(us, sourceDayUS, rem)
	if rem.Sign() < 0 {
		days.Sub(days, big.NewInt(1))
	}
	if !days.IsInt64() || days.Int64() < math.MinInt32 || days.Int64() > math.MaxInt32 {
		return sourceError("OverflowError", "Python int too large to convert to C int")
	}
	if days.Int64() < -999999999 || days.Int64() > 999999999 {
		return sourceError("OverflowError", fmt.Sprintf("days=%s; must have magnitude <= 999999999", days.String()))
	}
	return nil
}

// SourceIntervalUSFromHours reproduces timedelta(hours=float), including its
// split integral/fractional accumulation and parity-sensitive microsecond tie.
// It returns raw rounded zero; IntervalTrigger's zero->1sec rule belongs to New.
func SourceIntervalUSFromHours(hours float64) (*big.Int, error) {
	if math.IsNaN(hours) {
		return nil, sourceError("ValueError", "cannot convert float NaN to integer")
	}
	if math.IsInf(hours, 0) {
		return nil, sourceError("OverflowError", "cannot convert float infinity to integer")
	}
	whole, fraction := math.Modf(hours)
	integer, _ := new(big.Float).SetFloat64(whole).Int(nil)
	integer.Mul(integer, big.NewInt(3600000000))
	fractionalUS := float64(fraction * 3600000000)
	part, tail := math.Modf(fractionalUS)
	integralPart, _ := new(big.Float).SetFloat64(part).Int(nil)
	integer.Add(integer, integralPart)
	rounded := math.RoundToEven(tail)
	if math.Abs(tail) == .5 && integer.Bit(0) != 0 {
		rounded = math.Copysign(1, tail)
	}
	integer.Add(integer, big.NewInt(int64(rounded)))
	if err := sourceDeltaRange(integer); err != nil {
		return nil, err
	}
	return integer, nil
}
func sourceMicros(t time.Time) *big.Int {
	value := new(big.Int).Mul(big.NewInt(t.Unix()), sourceSecondUS)
	return value.Add(value, big.NewInt(int64(t.Nanosecond()/1000)))
}
func sourceSeconds(us *big.Int) float64 {
	value, _ := new(big.Rat).SetFrac(new(big.Int).Set(us), sourceSecondUS).Float64()
	return value
}
func sourceTimestamp(t time.Time) float64 { return sourceSeconds(sourceMicros(t)) }
func sourceTimeUS(us *big.Int) time.Time {
	seconds, micros := new(big.Int), new(big.Int)
	seconds.QuoRem(us, sourceSecondUS, micros)
	return time.Unix(seconds.Int64(), micros.Int64()*1000).UTC()
}
func sourceWall(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), t.Nanosecond()/1000*1000, time.UTC)
}
func sourceDateText(t time.Time) string {
	text := t.Format("2006-01-02T15:04:05.000000")
	_, offset := t.Zone()
	if offset == 0 {
		return text + "Z"
	}
	sign := "+"
	if offset < 0 {
		sign = "-"
		offset = -offset
	}
	text += fmt.Sprintf("%s%02d:%02d", sign, offset/3600, offset%3600/60)
	if offset%60 != 0 {
		text += fmt.Sprintf(":%02d", offset%60)
	}
	return text
}
func sourceReadTime(raw string) (time.Time, error) {
	if len(raw) > 64 {
		return time.Time{}, errTrigger
	}
	value, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		value, err = time.Parse("2006-01-02T15:04:05.999999999Z07:00:00", raw)
	}
	if err != nil || value.Year() < 1 || value.Year() > 9999 || value.Nanosecond()%1000 != 0 {
		return time.Time{}, errTrigger
	}
	_, offset := value.Zone()
	if offset <= -86400 || offset >= 86400 {
		return time.Time{}, errTrigger
	}
	return value, nil
}

// Resolve fold0 like ZoneInfo's datetime+timedelta. An imaginary gap retains
// the pre-transition offset; no wall time is normalized before it is recorded.
func sourceWallOffset(wall time.Time, location *time.Location) (int, error) {
	offsets := map[int]bool{}
	for at, end := wall.Add(-72*time.Hour), wall.Add(72*time.Hour); at.Before(end); {
		local := at.In(location)
		_, off := local.Zone()
		offsets[off] = true
		_, next := local.ZoneBounds()
		if next.IsZero() || !next.After(at) {
			break
		}
		at = next
	}
	var exact, gap time.Time
	var exactOffset, gapOffset int
	haveExact, haveGap := false, false
	for off := range offsets {
		candidate := time.Unix(wall.Unix()-int64(off), int64(wall.Nanosecond())).UTC()
		actual := sourceWall(candidate.In(location))
		if actual.Equal(wall) && (!haveExact || candidate.Before(exact)) {
			exact, exactOffset, haveExact = candidate, off, true
		}
		if actual.After(wall) && (!haveGap || candidate.Before(gap)) {
			gap, gapOffset, haveGap = candidate, off, true
		}
	}
	if haveExact {
		return exactOffset, nil
	}
	if haveGap {
		return gapOffset, nil
	}
	return 0, errTrigger
}
func newSourceInterval(t *Trigger, anchor time.Time, location *time.Location, inputUS *big.Int) (*Trigger, error) {
	spec := t.spec
	if inputUS == nil || spec.IntervalMS != 0 || spec.Cron != "" || spec.Fields != nil || spec.RunAt != "" {
		return nil, errTrigger
	}
	micros := new(big.Int).Set(inputUS)
	if err := sourceDeltaRange(micros); err != nil {
		return nil, err
	}
	if micros.Sign() == 0 {
		micros.SetInt64(1000000)
	}

	state := &sourceIntervalState{location: location, stepUS: new(big.Int).Set(micros), length: sourceSeconds(micros), startLocal: spec.SourceStartLocal}
	var start time.Time
	var err error
	if spec.StartAt != "" {
		start, err = sourceReadTime(spec.StartAt)
		if err != nil {
			return nil, err
		}
		if !state.startLocal {
			// convert_to_datetime(string)->localize()->normalize() takes the
			// original float timestamp round trip. An aware datetime bypasses it.
			_, offset := start.Zone()
			start, err = sourceFromTimestamp(sourceTimestamp(start), time.FixedZone("", offset))
			if err != nil {
				return nil, err
			}
		}
		t.spec.StartAt = sourceDateText(start)
	} else {
		local := anchor.In(location)
		civil := sourceWall(local)
		value := new(big.Int).Add(sourceMicros(civil), micros)
		minimum := sourceMicros(time.Date(1, 1, 1, 0, 0, 0, 0, time.UTC))
		maximum := sourceMicros(time.Date(9999, 12, 31, 23, 59, 59, 999999000, time.UTC))
		if value.Cmp(minimum) < 0 || value.Cmp(maximum) > 0 {
			return nil, sourceError("OverflowError", "date value out of range")
		}
		civil = sourceTimeUS(value)
		offset, e := sourceWallOffset(civil, location)
		if e != nil {
			return nil, e
		}
		start = time.Date(civil.Year(), civil.Month(), civil.Day(), civil.Hour(), civil.Minute(), civil.Second(), civil.Nanosecond(), time.FixedZone("", offset))
		state.startLocal = true
		t.spec.SourceStartLocal = true
		t.spec.StartAt = sourceDateText(start)
	}
	state.startWall = sourceWall(start)
	state.startFloat = sourceTimestamp(start)
	t.start = start.UTC()
	if spec.EndAt != "" {
		end, e := sourceReadTime(spec.EndAt)
		if e != nil {
			return nil, e
		}
		_, offset := end.Zone()
		end, e = sourceFromTimestamp(sourceTimestamp(end), time.FixedZone("", offset))
		if e != nil {
			return nil, e
		}
		state.hasEnd = true
		state.endFloat = sourceTimestamp(end)
		t.end = end.UTC()
		t.spec.EndAt = sourceDateText(end)
	}
	// Source permits end<start and negative intervals. No host execution policy
	// is applied here, and no positive clamp/invalid-source exception is invented.
	t.sourceInterval = state
	return t, nil
}
func sourceFromTimestamp(value float64, location *time.Location) (time.Time, error) {
	if math.IsNaN(value) {
		return time.Time{}, sourceError("ValueError", "Invalid value NaN (not a number)")
	}
	if math.IsInf(value, 0) || value >= float64(math.MaxInt64) || value < float64(math.MinInt64) {
		return time.Time{}, sourceError("OverflowError", "timestamp out of range for platform time_t")
	}
	seconds, fraction := math.Modf(value)
	micros := math.RoundToEven(float64(fraction * 1000000))
	utc := time.Unix(int64(seconds), int64(micros)*1000).UTC()
	if utc.Year() < 1 || utc.Year() > 9999 {
		return time.Time{}, sourceError("ValueError", fmt.Sprintf("year %d is out of range", utc.Year()))
	}
	local := utc.In(location)
	if local.Year() < 1 || local.Year() > 9999 {
		return time.Time{}, sourceError("OverflowError", "date value out of range")
	}
	return local, nil
}
func (t *Trigger) sourceFinish(next float64) (time.Time, bool, error) {
	state := t.sourceInterval
	if t.spec.JitterSeconds != 0 {
		jitter := float64(t.jitterRandom() * t.spec.JitterSeconds)
		next = float64(next + jitter)
	}
	if state.hasEnd && next > state.endFloat {
		return time.Time{}, false, nil
	}
	at, err := sourceFromTimestamp(next, state.location)
	return at, err == nil, err
}

// SourceFirst is inclusive and preserves actual source float timestamp math.
// Present distinguishes a valid year1 UTC fire (Go's zeroTime) from source None.
// Execution owners must use the checked API for source_interval and must bound
// non-progress scans without invoking a callback or rewriting the trigger.
func (t *Trigger) SourceFirst(at time.Time, sameClock bool) (time.Time, bool, error) {
	state := t.sourceInterval
	if state == nil {
		return time.Time{}, false, errTrigger
	}
	now := time.Unix(at.Unix(), int64(at.Nanosecond()/1000)*1000).In(state.location)
	startAfter := t.start.After(now)
	if state.startLocal && sameClock {
		startAfter = state.startWall.After(sourceWall(now))
	}
	next := state.startFloat
	if !startAfter {
		timediff := float64(sourceTimestamp(now) - state.startFloat)
		count := math.Ceil(float64(timediff / state.length))
		increment := float64(state.length * count)
		next = float64(state.startFloat + increment)
	}
	return t.sourceFinish(next)
}
func (t *Trigger) SourceNext(previous time.Time) (time.Time, bool, error) {
	state := t.sourceInterval
	if state == nil {
		return time.Time{}, false, errTrigger
	}
	next := float64(sourceTimestamp(previous) + state.length)
	return t.sourceFinish(next)
}
