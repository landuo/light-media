package autosignin

import (
	"errors"
	"fmt"
	"math"
	"math/big"
	"sort"
	"strings"
	"time"
)

type ValueError struct{ Kind, Message string }

func (e *ValueError) Error() string { return e.Message }

// ParseRandomSchedule is the installed source's forgiving saved-times reader.
// OverflowError remains outside its KeyError/TypeError/ValueError catch.
func ParseRandomSchedule(date string, raw any) ([]time.Time, error) {
	parts := strings.Split(date, "-")
	if len(parts) != 3 {
		return []time.Time{}, nil
	}
	fields := [3]*big.Int{}
	for i, p := range parts {
		n, e := sourceInteger(p)
		if e != nil {
			return []time.Time{}, nil
		}
		fields[i] = n
	}
	var items []any
	switch v := raw.(type) {
	case nil:
	case []any:
		items = v
	case string:
		for _, r := range v {
			items = append(items, string(r))
		}
	case sourceHTTPMap:
		for _, k := range v.keys {
			items = append(items, k)
		}
	case Object:
		for k := range v {
			items = append(items, k)
		}
	case bool:
		if v {
			return nil, &ValueError{"TypeError", "'bool' object is not iterable"}
		}
	default:
		if Truth(v) {
			return nil, &ValueError{"TypeError", fmt.Sprintf("'%s' object is not iterable", typeName(v))}
		}
	}
	out := []time.Time{}
	seen := map[time.Time]bool{}
	for _, v := range items {
		m, ok := sourceHTTPObject(v)
		if !ok {
			continue
		}
		convertedTimes := [2]*big.Int{}
		valid := true
		for i, key := range []string{"hour", "minute"} {
			value, exists := m[key]
			if !exists {
				valid = false
				break
			}
			n, err := sourceInteger(value)
			if err != nil {
				var converted *ValueError
				if errors.As(err, &converted) && converted.Kind == "OverflowError" {
					return nil, err
				}
				valid = false
				break
			}
			convertedTimes[i] = n
		}
		if !valid {
			continue
		}
		// Both int() conversions precede the range comparison. A huge finite
		// hour cannot suppress a later minute OverflowError or missing-key catch.
		if convertedTimes[0].Sign() < 0 || convertedTimes[0].Cmp(big.NewInt(23)) > 0 || convertedTimes[1].Sign() < 0 || convertedTimes[1].Cmp(big.NewInt(59)) > 0 {
			continue
		}
		ints := [2]int{int(convertedTimes[0].Int64()), int(convertedTimes[1].Int64())}
		// datetime's C-int conversion occurs only after a valid time item.
		dateFields := [3]int{}
		for i, n := range fields {
			if !n.IsInt64() {
				return nil, &ValueError{"OverflowError", "Python int too large to convert to C long"}
			}
			value := n.Int64()
			if value > math.MaxInt32 {
				return nil, &ValueError{"OverflowError", "signed integer is greater than maximum"}
			}
			if value < math.MinInt32 {
				return nil, &ValueError{"OverflowError", "signed integer is less than minimum"}
			}
			dateFields[i] = int(value)
		}
		if dateFields[0] < 1 || dateFields[0] > 9999 {
			continue
		}
		stamp := time.Date(dateFields[0], time.Month(dateFields[1]), dateFields[2], ints[0], ints[1], 0, 0, time.UTC)
		if stamp.Year() != dateFields[0] || int(stamp.Month()) != dateFields[1] || stamp.Day() != dateFields[2] {
			continue
		}
		if !seen[stamp] {
			out = append(out, stamp)
			seen[stamp] = true
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Before(out[j]) })
	return out, nil
}
func randomDay(now time.Time, begin int, choose func(int, int) (int, error)) ([]time.Time, error) {
	trigger := time.Date(now.Year(), now.Month(), now.Day(), begin, 0, 0, 0, time.UTC)
	out := []time.Time{}
	for i := 0; i < 2; i++ {
		minutes, e := choose(120, 360)
		if e != nil {
			return nil, e
		}
		last := trigger
		trigger = trigger.Add(time.Duration(minutes) * time.Minute)
		if trigger.Hour() > 23 || trigger.Hour() < last.Hour() {
			break
		}
		out = append(out, trigger)
	}
	return out, nil
}
func createRandomSchedule(now time.Time, choose func(int, int) (int, error)) ([]time.Time, error) {
	current := time.Date(now.Year(), now.Month(), now.Day(), now.Hour(), now.Minute(), 0, 0, time.UTC)
	triggers, e := randomDay(now, 9, choose)
	if e != nil {
		return nil, e
	}
	future := func(times []time.Time) bool {
		for _, t := range times {
			if t.After(current) {
				return true
			}
		}
		return false
	}
	if future(triggers) {
		return triggers, nil
	}
	if current.Hour() < 23 {
		begin := current.Hour()
		if begin < 9 {
			begin = 9
		}
		triggers, e = randomDay(now, begin, choose)
		if e != nil {
			return nil, e
		}
		if future(triggers) {
			return triggers, nil
		}
		fallback := current.Add(time.Minute)
		if fallback.Hour() <= 23 {
			return []time.Time{fallback}, nil
		}
	}
	return triggers, nil
}
func (e *Engine) randomSchedule(choose func(int, int) (int, error)) ([]time.Time, error) {
	now := e.Now().In(e.ScheduleZone)
	today := now.Format("2006-01-02")
	saved := e.history("random_schedule")
	if Truth(saved) {
		m, ok := saved.(Object)
		if !ok {
			return nil, fmt.Errorf("'%s' object has no attribute 'get'", typeName(saved))
		}
		if m["date"] == today {
			times, err := ParseRandomSchedule(today, m["times"])
			if err != nil {
				return nil, err
			}
			if len(times) > 0 {
				return times, nil
			}
		}
	}
	times, err := createRandomSchedule(now, choose)
	if err != nil {
		return nil, err
	}
	values := []any{}
	for _, t := range times {
		values = append(values, Object{"hour": t.Hour(), "minute": t.Minute()})
	}
	if err = e.putHistory("random_schedule", Object{"date": today, "times": values}); err != nil {
		return nil, err
	}
	return times, nil
}
