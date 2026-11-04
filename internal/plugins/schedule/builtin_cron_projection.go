package schedule

import (
	"context"
	"time"
)

// This is the already delivered Cron field search with only its return presence
// and civil-zone projection made explicit for builtin composition. The old
// Cron.First/Next functions are not changed. A gap keeps the constructed wall
// fields and pre-transition offset instead of projecting the instant into the
// post-transition clock. Ordinary exact candidates retain the real Location.
func (c *Cron) builtinFirst(ctx context.Context, at time.Time) (*time.Time, error) {
	local := at.In(c.location)
	startYear, startMonth, startDay := local.Date()
	for year := c.fields[0].next(startYear); year != -1; year = c.fields[0].next(year + 1) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		monthMin := 1
		if year == startYear {
			monthMin = int(startMonth)
		}
		for month := c.fields[1].next(monthMin); month != -1; month = c.fields[1].next(month + 1) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			dayMin := 1
			if year == startYear && month == int(startMonth) {
				dayMin = startDay
			}
			last := time.Date(year, time.Month(month)+1, 0, 0, 0, 0, 0, time.UTC).Day()
			for day := dayMin; day <= last; day++ {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				date := time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC)
				if !c.dayMatches(date) {
					continue
				}
				candidate, err := c.builtinFirstOnDay(ctx, date, at)
				if err != nil {
					return nil, err
				}
				if candidate != nil {
					return candidate, nil
				}
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return nil, nil
}

func (c *Cron) builtinFirstOnDay(ctx context.Context, date, at time.Time) (*time.Time, error) {
	// ZoneBounds enumerates real offset transitions, including half-hour folds
	// and skipped calendar days. Avoid assuming that every local day has 24h.
	offsets := map[int]bool{}
	end := date.Add(72 * time.Hour)
	for point := date.Add(-48 * time.Hour); point.Before(end); {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		zoned := point.In(c.location)
		_, offset := zoned.Zone()
		offsets[offset] = true
		_, next := zoned.ZoneBounds()
		if next.IsZero() || !next.After(point) {
			break
		}
		point = next
	}
	var best *time.Time
	for offset := range offsets {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
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
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			minuteMin := 0
			if hour == int(minimum/3600) {
				minuteMin = int(minimum % 3600 / 60)
			}
			for minute := c.fields[6].next(minuteMin); minute != -1 && !found; minute = c.fields[6].next(minute + 1) {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				secondMin := 0
				if hour == int(minimum/3600) && minute == int(minimum%3600/60) {
					secondMin = int(minimum % 60)
				}
				for second := c.fields[7].next(secondMin); second != -1; second = c.fields[7].next(second + 1) {
					if err := ctx.Err(); err != nil {
						return nil, err
					}
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
					if best == nil || candidate.Before(*best) {
						projected := local
						if !exact {
							projected = time.Date(date.Year(), date.Month(), date.Day(), hour, minute, second, 0, time.FixedZone(c.location.String(), offset))
						}
						best = &projected
					}
					found = true
					break
				}
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return best, nil
}
