package schedule

import (
	"encoding/json"
	"os"
	"testing"
	"time"
)

func instant(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

func TestInstalledAPSchedulerOracle(t *testing.T) {
	raw, err := os.ReadFile("../../../testdata/plugins/apscheduler-3.11.3-times.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Version string `json:"version"`
		Cases   []struct {
			Cron     string      `json:"cron"`
			Fields   *CronFields `json:"fields"`
			Timezone string      `json:"timezone"`
			Start    string      `json:"start"`
			Times    []string    `json:"times"`
			Error    string      `json:"error"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(raw, &fixture); err != nil || fixture.Version != "3.11.3" || len(fixture.Cases) < 30 {
		t.Fatal("invalid original-implementation oracle", err)
	}
	for _, tc := range fixture.Cases {
		label, _ := json.Marshal(tc)
		t.Run(string(label), func(t *testing.T) {
			var cron *Cron
			var err error
			if tc.Cron != "" {
				cron, err = ParseCrontab(tc.Cron, tc.Timezone)
			} else {
				cron, err = ParseCronFields(*tc.Fields, tc.Timezone)
			}
			if tc.Error != "" {
				if err == nil {
					t.Fatal("expression rejected by the installed original was accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			at := instant(t, tc.Start)
			for index, expected := range tc.Times {
				actual := cron.First(at)
				if !actual.Equal(instant(t, expected)) {
					t.Fatalf("occurrence %d: got %v, original %s", index, actual, expected)
				}
				at = actual.Add(time.Microsecond)
			}
			if len(tc.Times) < 8 && !cron.First(at).IsZero() {
				t.Fatal("exhausted original expression still produced an occurrence")
			}
		})
	}
}

func TestTriggerIntervalAnchorAndFractionalPrecisionAcrossLongSpans(t *testing.T) {
	anchor := instant(t, "2026-10-16T00:00:00.123456789Z")
	trigger, err := New(Spec{Kind: "interval", Timezone: "Asia/Shanghai", IntervalMS: 30000}, anchor)
	if err != nil {
		t.Fatal(err)
	}
	start := anchor.Add(30 * time.Second)
	if !trigger.First(anchor).Equal(start) || !trigger.First(start).Equal(start) || !trigger.Next(start).Equal(start.Add(30*time.Second)) || !trigger.First(start.Add(time.Nanosecond)).Equal(start.Add(30*time.Second)) {
		t.Fatal("interval anchor or fractional boundary changed")
	}
	far := instant(t, "2999-01-01T00:00:00.123456790Z")
	if result := trigger.First(far); !result.Equal(instant(t, "2999-01-01T00:00:30.123456789Z")) {
		t.Fatal("interval used overflowing Unix nanos/duration", result)
	}
	copy := trigger.Spec()
	reopened, err := New(copy, anchor.Add(24*time.Hour))
	if err != nil || !reopened.First(anchor).Equal(start) {
		t.Fatal("restart reset the interval anchor", err)
	}
	zero, err := New(Spec{Kind: "interval", Timezone: "UTC"}, anchor)
	if err != nil || !zero.First(anchor).Equal(anchor.Add(time.Second)) {
		t.Fatal("original zero interval default differs", err)
	}
}

func TestTriggerDateAndEndBounds(t *testing.T) {
	at := instant(t, "2026-10-16T00:00:00Z")
	date, err := New(Spec{Kind: "date", Timezone: "UTC", RunAt: "2026-10-15T16:00:00-08:00"}, at)
	if err != nil || !date.First(at.Add(time.Hour)).Equal(at) || !date.Next(at).IsZero() {
		t.Fatal("late one-shot was silently lost or repeated", err)
	}
	cron, err := New(Spec{Kind: "cron", Timezone: "UTC", Cron: "* * * * *", StartAt: "2026-10-16T00:01:00Z", EndAt: "2026-10-16T00:02:00Z"}, at)
	if err != nil || !cron.First(at).Equal(at.Add(time.Minute)) || !cron.Next(at.Add(time.Minute)).Equal(at.Add(2*time.Minute)) || !cron.Next(at.Add(2*time.Minute)).IsZero() {
		t.Fatal("inclusive cron start/end changed", err)
	}
	interval, err := New(Spec{Kind: "interval", Timezone: "UTC", IntervalMS: 1000, EndAt: "2026-10-16T00:00:02Z"}, at)
	if err != nil || !interval.Next(at.Add(time.Second)).Equal(at.Add(2*time.Second)) || !interval.Next(at.Add(2*time.Second)).IsZero() {
		t.Fatal("interval inclusive end changed", err)
	}
	if FormatTime(at.Add(time.Nanosecond)) <= FormatTime(at) || FormatTime(instant(t, "2999-01-01T00:00:00Z")) <= FormatTime(at) {
		t.Fatal("durable due timestamps do not sort chronologically")
	}
}

func TestTriggerRejectsConflictingKindsAndInvalidScopes(t *testing.T) {
	at := instant(t, "2026-10-16T00:00:00Z")
	for _, spec := range []Spec{
		{Kind: "cron", Timezone: "UTC"},
		{Kind: "cron", Timezone: "UTC", Cron: "* * * * *", Fields: &CronFields{}},
		{Kind: "cron", Timezone: "UTC", Cron: "* * * * *", RunAt: FormatTime(at)},
		{Kind: "cron", Timezone: "Local", Cron: "* * * * *"},
		{Kind: "cron", Timezone: "not/a-zone", Cron: "* * * * *"},
		{Kind: "interval", Timezone: "UTC", IntervalMS: -1},
		{Kind: "interval", Timezone: "UTC", IntervalMS: 1, StartAt: "2026-10-17T00:00:00Z", EndAt: "2026-10-16T00:00:00Z"},
		{Kind: "date", Timezone: "UTC", RunAt: "2026-10-16T00:00:00"},
		{Kind: "date", Timezone: "UTC", RunAt: FormatTime(at), IntervalMS: 1},
		{Kind: "unknown", Timezone: "UTC"},
	} {
		if _, err := New(spec, at); err == nil {
			t.Fatal("invalid or conflicting trigger accepted", spec)
		}
	}
}
