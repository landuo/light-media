package sitestatistic

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestTodayIncrementRequiresAdjacentDay(t *testing.T) {
	cur := Data{"domain": "x", "name": "X", "updated_day": "2026-11-13", "upload": float64(100 << 30)}
	gap := Data{"domain": "x", "name": "X", "updated_day": "2026-11-09", "upload": float64(50 << 30)}
	raw, err := json.Marshal(elements(Selected{Day: "2026-11-13", Current: []Data{cur}, Previous: []Data{gap}}, "today"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte(`"series":[50]`)) {
		t.Fatalf("multi-day gap leaked into today's increment: %s", raw)
	}
	if !bytes.Contains(raw, []byte("无法计算")) || bytes.Contains(raw, []byte("VApexChart")) {
		t.Fatalf("expected an explicit no-baseline message, got %s", raw)
	}
	prev := Data{"domain": "x", "name": "X", "updated_day": "2026-11-12", "upload": float64(90 << 30)}
	raw, err = json.Marshal(elements(Selected{Day: "2026-11-13", Current: []Data{cur}, Previous: []Data{prev}}, "today"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(raw, []byte(`"series":[10]`)) {
		t.Fatalf("adjacent day increment missing: %s", raw)
	}
}
