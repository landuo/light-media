package app

import (
	"sort"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata"
)

func shanghai() *time.Location {
	loc, e := time.LoadLocation("Asia/Shanghai")
	if e != nil {
		return time.FixedZone("Asia/Shanghai", 8*3600)
	}
	return loc
}

func normalizeSchedule(in Schedule) (Schedule, error) {
	s := in
	s.Weekdays = append([]int{}, in.Weekdays...)
	s.Exceptions = append([]ScheduleException{}, in.Exceptions...)
	if s.Mode == "" {
		s.Mode = "follow"
	}
	if s.Source == "" {
		s.Source = "metadata"
	}
	if s.Rule == "" {
		s.Rule = "weekly"
	}
	if s.Timezone == "" {
		s.Timezone = "Asia/Shanghai"
	}
	if s.Mode != "follow" && s.Mode != "fixed" && s.Mode != "manual" {
		return s, bad("INVALID_SCHEDULE", "检查模式须为follow、fixed或manual")
	}
	if s.Source != "metadata" && s.Source != "custom" {
		return s, bad("INVALID_SCHEDULE", "更新来源须为metadata或custom")
	}
	if s.Rule == "every_days" || s.Rule == "interval" {
		s.Rule = "every_n_days"
	}
	if s.Rule != "weekly" && s.Rule != "daily" && s.Rule != "every_n_days" {
		return s, bad("INVALID_SCHEDULE", "更新周期无效")
	}
	if _, e := time.LoadLocation(s.Timezone); e != nil {
		return s, bad("INVALID_TIMEZONE", "时区名称无效")
	}
	if s.Time != "" {
		if t, e := time.Parse("15:04", s.Time); e != nil || t.Format("15:04") != s.Time {
			return s, bad("INVALID_SCHEDULE", "更新时间须为HH:MM")
		}
	}
	if s.StartDate != "" {
		if _, e := parseDay(s.StartDate, time.UTC); e != nil {
			return s, bad("INVALID_SCHEDULE", "周期起始日期无效")
		}
	}
	if s.EveryDays == 0 {
		s.EveryDays = 1
	}
	if s.IntervalMinutes == 0 {
		// MoviePilot searches subscriptions once per 24 hours by default.
		s.IntervalMinutes = 1440
	}
	if s.WindowHours == 0 {
		s.WindowHours = 6
	}
	if s.ActiveMinutes == 0 {
		s.ActiveMinutes = 30
	}
	if s.FallbackMinutes == 0 {
		// Outside the update window, follow MoviePilot's 24-hour search cadence.
		s.FallbackMinutes = 1440
	}
	if s.EveryDays < 1 || s.EveryDays > 365 || s.IntervalMinutes < 5 || s.IntervalMinutes > 10080 || s.ActiveMinutes < 5 || s.ActiveMinutes > 1440 || s.FallbackMinutes < 30 || s.FallbackMinutes > 10080 || s.WaitMinutes < 0 || s.WaitMinutes > 1440 || s.WindowHours < 1 || s.WindowHours > 48 {
		return s, bad("INVALID_SCHEDULE", "检查间隔、活跃窗口或等待时间超出范围")
	}
	seen := map[int]bool{}
	for _, d := range s.Weekdays {
		if d < 0 || d > 6 || seen[d] {
			return s, bad("INVALID_SCHEDULE", "星期值须为0至6且不重复")
		}
		seen[d] = true
	}
	sort.Ints(s.Weekdays)
	if s.Source == "custom" && s.Rule == "weekly" && len(s.Weekdays) == 0 {
		return s, bad("INVALID_SCHEDULE", "自定义每周周期至少需要选择一天")
	}
	if s.Source == "custom" && s.Rule == "every_n_days" && s.StartDate == "" {
		return s, bad("INVALID_SCHEDULE", "每N天周期需要起始日期")
	}
	if len(s.Exceptions) > 100 {
		return s, bad("INVALID_SCHEDULE", "例外日期过多")
	}
	exceptions := map[string]bool{}
	for _, x := range s.Exceptions {
		if _, e := parseDay(x.Date, time.UTC); e != nil {
			return s, bad("INVALID_SCHEDULE", "例外日期无效")
		}
		if x.Action != "skip" && x.Action != "postpone" && x.Action != "extra" {
			return s, bad("INVALID_SCHEDULE", "例外操作须为skip、postpone或extra")
		}
		if x.Action == "postpone" {
			if _, e := parseDay(x.TargetDate, time.UTC); e != nil || x.TargetDate <= x.Date {
				return s, bad("INVALID_SCHEDULE", "延期日期须晚于原日期")
			}
		}
		if x.Episode < 0 || x.Episode > 9999 {
			return s, bad("INVALID_SCHEDULE", "例外集号无效")
		}
		key := x.Date + ":" + strconv.Itoa(x.Episode)
		if exceptions[key] {
			return s, bad("INVALID_SCHEDULE", "同一天同集号的例外重复")
		}
		exceptions[key] = true
	}
	return s, nil
}
func parseDay(value string, loc *time.Location) (time.Time, error) {
	t, e := time.ParseInLocation("2006-01-02", value, loc)
	if e != nil || t.Format("2006-01-02") != value {
		return t, bad("INVALID_DATE", "日期须为YYYY-MM-DD")
	}
	return t, nil
}
func dateOnly(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, t.Location())
}
func scheduleMatches(s Schedule, day time.Time) bool {
	if s.StartDate != "" && day.Format("2006-01-02") < s.StartDate {
		return false
	}
	switch s.Rule {
	case "daily":
		return true
	case "weekly":
		for _, d := range s.Weekdays {
			if int(day.Weekday()) == d {
				return true
			}
		}
	case "every_n_days":
		start, e := parseDay(s.StartDate, day.Location())
		if e != nil {
			return false
		}
		y, m, d := day.Date()
		sy, sm, sd := start.Date()
		delta := int(time.Date(y, m, d, 0, 0, 0, 0, time.UTC).Sub(time.Date(sy, sm, sd, 0, 0, 0, 0, time.UTC)).Hours() / 24)
		return delta >= 0 && delta%s.EveryDays == 0
	}
	return false
}

type scheduleOccurrence struct {
	Date         string `json:"date"`
	OriginalDate string `json:"original_date,omitempty"`
	State        string `json:"state"`
}

func customOccurrences(s Schedule, from, to time.Time) []scheduleOccurrence {
	out := []scheduleOccurrence{}
	seen := map[string]bool{}
	appendOne := func(date, original, state string) {
		if date < from.Format("2006-01-02") || date > to.Format("2006-01-02") {
			return
		}
		key := date + ":" + state
		if !seen[key] {
			seen[key] = true
			out = append(out, scheduleOccurrence{date, original, state})
		}
	}
	// Include earlier originals whose postponed date enters the requested range.
	for _, x := range s.Exceptions {
		if x.Episode == 0 && x.Action == "postpone" {
			appendOne(x.TargetDate, x.Date, "postponed")
		}
		if x.Episode == 0 && x.Action == "extra" {
			appendOne(x.Date, "", "predicted")
		}
	}
	for day := dateOnly(from); !day.After(to); day = day.AddDate(0, 0, 1) {
		if !scheduleMatches(s, day) {
			continue
		}
		date := day.Format("2006-01-02")
		handled := false
		for _, x := range s.Exceptions {
			if x.Date == date && x.Episode == 0 {
				switch x.Action {
				case "skip":
					appendOne(date, "", "skipped")
					handled = true
				case "postpone":
					handled = true
				}
			}
		}
		if !handled {
			appendOne(date, "", "predicted")
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Date < out[j].Date })
	return out
}
func occurrenceWindow(s Schedule, date string, loc *time.Location) (time.Time, time.Time) {
	day, e := parseDay(date, loc)
	if e != nil {
		return time.Time{}, time.Time{}
	}
	if s.Time == "" {
		return day, day.AddDate(0, 0, 1).Add(time.Duration(s.WindowHours) * time.Hour)
	}
	clock, _ := time.Parse("15:04", s.Time)
	start := time.Date(day.Year(), day.Month(), day.Day(), clock.Hour(), clock.Minute(), 0, 0, loc).Add(time.Duration(s.WaitMinutes) * time.Minute)
	return start, start.Add(time.Duration(s.WindowHours) * time.Hour)
}
func nextScheduleCheck(s Schedule, after time.Time, metadataDates []string) time.Time {
	if s.Mode == "manual" {
		return time.Time{}
	}
	if s.Mode == "fixed" {
		return after.Add(time.Duration(s.IntervalMinutes) * time.Minute)
	}
	loc, _ := time.LoadLocation(s.Timezone)
	local := after.In(loc)
	dates := append([]string{}, metadataDates...)
	if s.Source == "custom" {
		dates = nil
		for _, o := range customOccurrences(s, local.AddDate(0, 0, -2), local.AddDate(0, 0, 8)) {
			if o.State != "skipped" {
				dates = append(dates, o.Date)
			}
		}
	}
	active := false
	starts := []time.Time{}
	for _, date := range dates {
		start, end := occurrenceWindow(s, date, loc)
		if start.IsZero() {
			continue
		}
		if !local.Before(start) && local.Before(end) {
			active = true
		}
		if start.After(local) {
			starts = append(starts, start)
		}
	}
	interval := s.FallbackMinutes
	if active {
		interval = s.ActiveMinutes
	}
	next := after.Add(time.Duration(interval) * time.Minute)
	for _, start := range starts {
		if start.Before(next) {
			next = start
		}
	}
	return next
}
func previewSchedule(in Schedule) (any, error) {
	s, e := normalizeSchedule(in)
	if e != nil {
		return nil, e
	}
	at := time.Now()
	checks := []string{}
	for range 5 {
		at = nextScheduleCheck(s, at, nil)
		if at.IsZero() {
			break
		}
		checks = append(checks, at.UTC().Format(time.RFC3339))
	}
	updates := []scheduleOccurrence{}
	if s.Source == "custom" {
		loc, _ := time.LoadLocation(s.Timezone)
		start := time.Now().In(loc)
		for _, o := range customOccurrences(s, start, start.AddDate(0, 0, 370)) {
			updates = append(updates, o)
			if len(updates) == 5 {
				break
			}
		}
	}
	note := "预计更新只影响检查频率，不创建标准集；窗口外仍低频补漏"
	if s.Source == "metadata" {
		note += "；具体更新日将在选择订阅后由单集元数据提供"
	}
	if s.Mode == "manual" {
		note = "仅手动检查，不安排自动检查"
	}
	return map[string]any{"schedule": s, "next_checks": checks, "expected_updates": updates, "note": note}, nil
}
func effectiveEpisodeDate(s Subscription, ep int, original string) (string, bool) {
	date := original
	if s.EpisodeDates[ep] != "" {
		date = s.EpisodeDates[ep]
	}
	for _, x := range s.Schedule.Exceptions {
		if x.Date == date && (x.Episode == 0 || x.Episode == ep) {
			switch x.Action {
			case "skip":
				return date, true
			case "postpone":
				return x.TargetDate, false
			}
		}
	}
	return strings.TrimSpace(date), false
}
