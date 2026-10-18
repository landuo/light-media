package iyuuautoseed

import (
	"encoding/json"
	"os"
	"testing"
	"time"
)

func TestInstalledSiteRateLimiterOracle(t *testing.T) {
	raw, e := os.ReadFile("reference/sites-probe-round9.json")
	if e != nil {
		t.Fatal(e)
	}
	v, e := decodeJSON(raw)
	if e != nil {
		t.Fatal(e)
	}
	cases := v.(map[string]any)["result"].(map[string]any)["calls"].([]any)
	count := 0
	steps := 0
	for _, raw := range cases {
		row := raw.(map[string]any)
		if row["case"] != "limiter" {
			continue
		}
		count++
		values := row["config"].([]any)
		asInt := func(v any) int { n, _ := v.(json.Number).Int64(); return int(n) }
		clock := 0.0
		l := NewSiteRateLimiter(asInt(values[0]), asInt(values[1]), asInt(values[2]), func() float64 { return clock }, time.UTC)
		for _, raw := range row["trace"].([]any) {
			steps++
			step := raw.(map[string]any)
			clock, _ = step["time"].(json.Number).Float64()
			got, msg := l.Check()
			want := step["result"].(map[string]any)["value"].([]any)
			if got != want[0] || msg != want[1] {
				t.Fatalf("config%v time%v got%v/%s want%v", values, clock, got, msg, want)
			}
			attrs := step["attributes"].(map[string]any)
			last, _ := attrs["last_visit_time"].(json.Number).Float64()
			state := l.Snapshot()
			if state.Count != asInt(attrs["count"]) || state.LastVisitTime != last {
				t.Fatalf("state got%+v want%v", state, attrs)
			}
		}
	}
	if count != 7 || steps != 70 {
		t.Fatalf("case counts %d/%d", count, steps)
	}
}
