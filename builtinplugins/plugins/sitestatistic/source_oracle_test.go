package sitestatistic

import (
	"bytes"
	"encoding/json"
	"math/big"
	"os"
	"reflect"
	"testing"
)

type semanticNumber struct{ Value string }

func semanticJSON(raw []byte) any {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var v any
	if e := d.Decode(&v); e != nil {
		panic(e)
	}
	var normalize func(any) any
	normalize = func(v any) any {
		switch x := v.(type) {
		case json.Number:
			r, ok := new(big.Rat).SetString(string(x))
			if ok {
				return semanticNumber{r.RatString()}
			}
		case []any:
			for i := range x {
				x[i] = normalize(x[i])
			}
		case map[string]any:
			for k := range x {
				x[k] = normalize(x[k])
			}
		}
		return v
	}
	return normalize(v)
}
func stripChartNodes(v any) any {
	switch x := v.(type) {
	case []any:
		out := []any{}
		for _, item := range x {
			if m, ok := item.(map[string]any); ok && (m["component"] == "VApexChart" || m["component"] == "VAlert") {
				continue
			}
			filtered := stripChartNodes(item)
			if m, ok := filtered.(map[string]any); ok && m["component"] == "VCol" {
				if content, ok := m["content"].([]any); ok && len(content) == 0 {
					continue
				}
			}
			out = append(out, filtered)
		}
		return out
	case map[string]any:
		out := map[string]any{}
		for k, item := range x {
			out[k] = stripChartNodes(item)
		}
		return out
	}
	return v
}

func sameSourceJSONWithoutCharts(t *testing.T, label string, actual any, expected json.RawMessage) {
	t.Helper()
	raw, e := json.Marshal(actual)
	if e != nil {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(stripChartNodes(semanticJSON(raw)), stripChartNodes(semanticJSON(expected))) {
		t.Errorf("%s differs from actual source312\ngot %s\nwant %s", label, raw, expected)
	}
}

func sameSourceJSON(t *testing.T, label string, actual any, expected json.RawMessage) {
	t.Helper()
	raw, e := json.Marshal(actual)
	if e != nil {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(semanticJSON(raw), semanticJSON(expected)) {
		t.Errorf("%s differs from actual source312\ngot %s\nwant %s", label, raw, expected)
	}
}
func TestActualImagePython312StatisticsOracle(t *testing.T) {
	raw, e := os.ReadFile("reference/source-python312.json")
	if e != nil {
		t.Fatal(e)
	}
	var source struct {
		Python                 string
		Historical, Additional []struct {
			Name                    string
			Latest, History         []Data
			Active                  []string `json:"active_domains"`
			Mode                    any      `json:"notify_type"`
			Day                     any
			Current, Previous, Page json.RawMessage
			Dashboards              map[string]json.RawMessage
			Notices                 []struct{ Title, Text string } `json:"notifications"`
			Last                    *struct{ Fingerprint string }  `json:"last_notify"`
		}
		Formats []struct {
			Value any
			Text  string
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if e = decoder.Decode(&source); e != nil {
		t.Fatal(e)
	}
	if len(source.Historical) != 24 || len(source.Additional) != 6 || len(source.Formats) != 14 {
		t.Fatal("source inventory changed")
	}
	for _, c := range append(source.Historical, source.Additional...) {
		t.Run(c.Name, func(t *testing.T) {
			selected, e := Select(Snapshot{Latest: c.Latest, History: c.History, ActiveDomains: c.Active})
			if e != nil {
				t.Fatal(e)
			}
			if !sourceEqual(selected.Day, c.Day) {
				t.Fatal("day", selected.Day, c.Day)
			}
			sameSourceJSON(t, "current", selected.Current, c.Current)
			sameSourceJSON(t, "previous", selected.Previous, c.Previous)
			page, e := Page(selected)
			if e != nil {
				t.Fatal(e)
			}
			sameSourceJSONWithoutCharts(t, "page", page, c.Page)
			for mode, want := range c.Dashboards {
				elements, e := Elements(selected, mode)
				if e != nil {
					t.Fatal(e)
				}
				if mode == "today" {
					// Today's chart intentionally diverges: only adjacent calendar
					// days may be subtracted. Covered by TestTodayIncrementRequiresAdjacentDay.
					continue
				}
				sameSourceJSONWithoutCharts(t, "dashboard."+mode, map[string]any{"cols": map[string]int{"cols": 12}, "attrs": map[string]any{}, "elements": []Node{{"component": "VRow", "content": elements}}}, want)
			}
			notice, e := BuildNotification(selected, c.Mode, "2026-10-16")
			if e != nil {
				t.Fatal(e)
			}
			if len(c.Notices) == 0 {
				if notice != nil {
					t.Fatal("unexpected notification")
				}
			} else if notice == nil || notice.Title != c.Notices[0].Title || notice.Text != c.Notices[0].Text || c.Last == nil || notice.Fingerprint != c.Last.Fingerprint {
				t.Fatalf("source notification differs: %#v", notice)
			}
		})
	}
	for i, c := range source.Formats {
		if got := FormatSize(c.Value); got != c.Text {
			t.Errorf("format %d: got %q want %q", i, got, c.Text)
		}
	}
}
