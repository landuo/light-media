package autosignin

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"regexp"
	"strconv"
	"strings"
	"time"

	"light-media/internal/plugins/schedule"
	"light-media/internal/releasemeta/legacyanime"
)

var ErrScheduleNotReady = errors.New("schedule expression outside verified compatibility")
var cronNumbers = regexp.MustCompile(`[0-9]+`)

func cronDisplayField(value string) string {
	return cronNumbers.ReplaceAllStringFunc(strings.ToLower(value), func(n string) string {
		number, err := strconv.ParseUint(n, 10, 64)
		if err != nil {
			return n
		}
		return strconv.FormatUint(number, 10)
	})
}
func sourceFloat(text string) (any, error) {
	text = legacyanime.Strip(text)
	lower := legacyanime.LowerCase(text)
	switch lower {
	case "nan", "+nan", "-nan":
		return Object{"nonfinite": "nan"}, nil
	case "inf", "+inf", "infinity", "+infinity":
		return Object{"nonfinite": "inf"}, nil
	case "-inf", "-infinity":
		return Object{"nonfinite": "-inf"}, nil
	}
	// Python accepts decimal Unicode digits and only digit-surrounded underscores.
	var normalized strings.Builder
	runes := []rune(text)
	for i, r := range runes {
		if r == '_' {
			if i == 0 || i == len(runes)-1 {
				return nil, errors.New("invalid float")
			}
			if _, err := legacyanime.Integer(string(runes[i-1])); err != nil {
				return nil, err
			}
			if _, err := legacyanime.Integer(string(runes[i+1])); err != nil {
				return nil, err
			}
			continue
		}
		if value, err := legacyanime.Integer(string(r)); err == nil {
			normalized.WriteString(value.String())
		} else {
			normalized.WriteRune(r)
		}
	}
	value, err := strconv.ParseFloat(normalized.String(), 64)
	if err != nil {
		var number *strconv.NumError
		if !errors.As(err, &number) || number.Err != strconv.ErrRange {
			return nil, err
		}
	}
	if math.IsInf(value, 1) {
		return Object{"nonfinite": "inf"}, nil
	}
	if math.IsInf(value, -1) {
		return Object{"nonfinite": "-inf"}, nil
	}
	return value, nil
}

// Services emits the source's descriptor, never starts a task. Nonfinite hours
// are normalized only for the offline reference encoding, not a valid host job.
func (e *Engine) Services(randomInt func(int, int) (int, error), processTimezone string) ([]any, error) {
	service := func(trigger any, kwargs Object, id string) []any {
		return []any{Object{"id": id, "name": "站点自动签到服务", "trigger": trigger, "func": Object{"callable": "sign_in"}, "kwargs": kwargs}}
	}
	if Truth(e.Enabled) && Truth(e.Cron) {
		text := legacyanime.Strip(pythonScalar(e.Cron))
		if strings.Count(text, " ") == 4 {
			for _, r := range text {
				if r > 127 {
					return nil, ErrScheduleNotReady
				}
			}
			if _, err := schedule.ParseCrontab(pythonScalar(e.Cron), processTimezone); err != nil {
				return []any{}, nil
			}
			fields := strings.Fields(text)
			descriptor := Object{"trigger": "CronTrigger", "fields": Object{"year": "*", "month": cronDisplayField(fields[3]), "day": cronDisplayField(fields[2]), "week": "*", "day_of_week": cronDisplayField(fields[4]), "hour": cronDisplayField(fields[1]), "minute": cronDisplayField(fields[0]), "second": "0"}, "timezone": processTimezone}
			return service(descriptor, Object{}, "AutoSignIn"), nil
		}
		parts := strings.Split(text, "/")
		hours := text
		if len(parts) == 2 {
			hours = parts[0]
			window := strings.Split(parts[1], "-")
			if len(window) == 2 {
				start, err := sourceInteger(window[0])
				if err != nil {
					return []any{}, nil
				}
				e.StartTime = json.Number(start.String())
				end, err := sourceInteger(window[1])
				if err != nil {
					return []any{}, nil
				}
				e.EndTime = json.Number(end.String())
			}
			if !Truth(e.StartTime) || !Truth(e.EndTime) {
				return []any{}, nil
			}
		}
		value, err := sourceFloat(hours)
		if err != nil {
			return []any{}, nil
		}
		return service("interval", Object{"hours": value}, "AutoSignIn"), nil
	}
	if Truth(e.Enabled) {
		if randomInt == nil {
			randomInt = func(lo, hi int) (int, error) { return lo + rand.IntN(hi-lo+1), nil }
		}
		now := e.Now()
		trigger := time.Date(now.Year(), now.Month(), now.Day(), 9, 0, 0, 0, now.Location())
		out := []any{}
		for i := 0; i < 2; i++ {
			interval, err := randomInt(120, 360)
			if err != nil {
				return nil, err
			}
			if interval < 120 || interval > 360 {
				return nil, fmt.Errorf("invalid random interval")
			}
			previous := trigger
			trigger = trigger.Add(time.Duration(interval) * time.Minute)
			if trigger.Hour() > 23 || trigger.Hour() < previous.Hour() {
				break
			}
			out = append(out, service("cron", Object{"hour": trigger.Hour(), "minute": trigger.Minute()}, fmt.Sprintf("AutoSignIn|%d:%d", trigger.Hour(), trigger.Minute()))...)
		}
		return out, nil
	}
	return []any{}, nil
}
