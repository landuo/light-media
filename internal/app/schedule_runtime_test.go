package app

import (
	"testing"
	"time"
)

func scheduleTime(t *testing.T, value, zone string) time.Time {
	t.Helper()
	loc, e := time.LoadLocation(zone)
	if e != nil {
		t.Fatal(e)
	}
	at, e := time.ParseInLocation("2006-01-02 15:04", value, loc)
	if e != nil {
		t.Fatal(e)
	}
	return at
}
func fridaySchedule(t *testing.T) Schedule {
	t.Helper()
	s, e := normalizeSchedule(Schedule{Source: "custom", Rule: "weekly", Weekdays: []int{5}, Timezone: "Asia/Shanghai", Mode: "follow", Time: "18:00", WaitMinutes: 30})
	if e != nil {
		t.Fatal(e)
	}
	return s
}
func TestScheduleOldMissingEpisodesCheckedBeforeFriday(t *testing.T) {
	s := fridaySchedule(t)
	monday := scheduleTime(t, "2026-10-12 10:00", "Asia/Shanghai")
	next := nextScheduleCheck(s, monday, nil)
	if !next.Equal(monday.Add(6 * time.Hour)) {
		t.Fatalf("old misses must use fallback before Friday: %v", next)
	}
	friday := scheduleTime(t, "2026-10-16 17:00", "Asia/Shanghai")
	next = nextScheduleCheck(s, friday, nil)
	if next.Format("15:04") != "18:30" {
		t.Fatalf("should enter configured window: %v", next)
	}
	active := scheduleTime(t, "2026-10-16 19:00", "Asia/Shanghai")
	next = nextScheduleCheck(s, active, nil)
	if !next.Equal(active.Add(30 * time.Minute)) {
		t.Fatalf("active window not half hourly: %v", next)
	}
}
func TestMetadataDateOnlyAndManualFixedModes(t *testing.T) {
	s, e := normalizeSchedule(Schedule{Source: "metadata", Timezone: "Asia/Shanghai"})
	if e != nil {
		t.Fatal(e)
	}
	at := scheduleTime(t, "2026-10-16 12:00", "Asia/Shanghai")
	if next := nextScheduleCheck(s, at, []string{"2026-10-16"}); !next.Equal(at.Add(30 * time.Minute)) {
		t.Fatalf("known day should be active: %v", next)
	}
	s.Mode = "fixed"
	s.IntervalMinutes = 120
	if next := nextScheduleCheck(s, at, []string{"2026-10-16"}); !next.Equal(at.Add(2 * time.Hour)) {
		t.Fatalf("fixed interval: %v", next)
	}
	s.Mode = "manual"
	if !nextScheduleCheck(s, at, nil).IsZero() {
		t.Fatal("manual mode scheduled an automatic check")
	}
}
func TestEveryNDaysUsesCalendarDaysAcrossDST(t *testing.T) {
	s, e := normalizeSchedule(Schedule{Source: "custom", Rule: "every_n_days", EveryDays: 2, StartDate: "2026-03-07", Timezone: "America/New_York"})
	if e != nil {
		t.Fatal(e)
	}
	if !scheduleMatches(s, scheduleTime(t, "2026-03-09 00:00", "America/New_York")) || scheduleMatches(s, scheduleTime(t, "2026-03-08 00:00", "America/New_York")) {
		t.Fatal("N-day recurrence used elapsed 24-hour blocks across DST")
	}
}
func TestScheduleExceptionsAndValidation(t *testing.T) {
	s := fridaySchedule(t)
	s.Exceptions = []ScheduleException{{Date: "2026-10-16", Action: "postpone", TargetDate: "2026-10-18"}, {Date: "2026-10-23", Action: "skip"}}
	start := scheduleTime(t, "2026-10-16 00:00", "Asia/Shanghai")
	end := start.AddDate(0, 0, 8)
	occ := customOccurrences(s, start, end)
	if len(occ) != 2 || occ[0].Date != "2026-10-18" || occ[0].OriginalDate != "2026-10-16" || occ[1].State != "skipped" {
		t.Fatalf("exceptions: %+v", occ)
	}
	if _, e := normalizeSchedule(Schedule{Source: "custom", Rule: "weekly"}); e == nil {
		t.Fatal("empty custom weekday accepted")
	}
	for _, change := range []func(*Schedule){func(x *Schedule) { x.Weekdays = []int{7} }, func(x *Schedule) { x.Time = "25:00" }, func(x *Schedule) { x.Timezone = "not/a-zone" }, func(x *Schedule) { x.ActiveMinutes = 1 }, func(x *Schedule) {
		x.Exceptions = []ScheduleException{{Date: "2026-10-16", Action: "postpone", TargetDate: "2026-10-15"}}
	}} {
		x := fridaySchedule(t)
		change(&x)
		if _, e := normalizeSchedule(x); e == nil {
			t.Fatalf("invalid schedule accepted %+v", x)
		}
	}
}
func TestEffectiveEpisodeDateDoesNotCreateNumbers(t *testing.T) {
	s := Subscription{EpisodeDates: map[int]string{5: "2026-10-16"}, Schedule: Schedule{Exceptions: []ScheduleException{{Date: "2026-10-16", Action: "postpone", TargetDate: "2026-10-18", Episode: 5}}}}
	if date, skip := effectiveEpisodeDate(s, 5, "2026-10-15"); date != "2026-10-18" || skip {
		t.Fatalf("explicit postponed known episode: %s %t", date, skip)
	}
	if date, skip := effectiveEpisodeDate(s, 6, ""); date != "" || skip {
		t.Fatal("date override created metadata for a different episode")
	}
}
