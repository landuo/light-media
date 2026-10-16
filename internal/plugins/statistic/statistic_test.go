package statistic

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

func sameJSON(t *testing.T, label string, actual any, expected json.RawMessage) {
	t.Helper()
	raw, err := json.Marshal(actual)
	if err != nil {
		t.Fatal(err)
	}
	var a, b any
	if json.Unmarshal(raw, &a) != nil || json.Unmarshal(expected, &b) != nil {
		t.Fatal("invalid oracle JSON")
	}
	if !reflect.DeepEqual(a, b) {
		t.Errorf("%s differs from original\nactual: %s\nexpected: %s", label, raw, expected)
	}
}

func TestInstalledSiteStatisticOriginalOracle(t *testing.T) {
	raw, err := os.ReadFile("../../../testdata/plugins/site-statistic-original-oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		SourceSHA string `json:"source_sha256"`
		Cases     []struct {
			Name          string                     `json:"name"`
			Latest        []Data                     `json:"latest"`
			History       []Data                     `json:"history"`
			Active        []string                   `json:"active_domains"`
			NotifyType    string                     `json:"notify_type"`
			Day           string                     `json:"day"`
			Current       json.RawMessage            `json:"current"`
			Previous      json.RawMessage            `json:"previous"`
			Page          json.RawMessage            `json:"page"`
			Dashboards    map[string]json.RawMessage `json:"dashboards"`
			Notifications []struct {
				Title string `json:"title"`
				Text  string `json:"text"`
			} `json:"notifications"`
			LastNotify *struct {
				Fingerprint string `json:"fingerprint"`
			} `json:"last_notify"`
		} `json:"cases"`
		Formats []struct {
			Value *float64 `json:"value"`
			Text  string   `json:"text"`
		} `json:"formats"`
	}
	if json.Unmarshal(raw, &fixture) != nil || fixture.SourceSHA != "763a88be85185a94082a2f131ed1c80d8306abe24b10ebe9ae95447cfe5da17d" {
		t.Fatal("invalid source oracle")
	}
	for _, c := range fixture.Cases {
		t.Run(c.Name, func(t *testing.T) {
			selected, err := Select(Snapshot{Latest: c.Latest, History: c.History, ActiveDomains: c.Active})
			if err != nil {
				t.Fatal(err)
			}
			if selected.Day != c.Day {
				t.Fatal("date label differs", selected.Day, c.Day)
			}
			sameJSON(t, "current", selected.Current, c.Current)
			sameJSON(t, "previous", selected.Previous, c.Previous)
			sameJSON(t, "page", Page(selected), c.Page)
			for mode, want := range c.Dashboards {
				sameJSON(t, mode, map[string]any{"cols": map[string]int{"cols": 12}, "attrs": map[string]any{}, "elements": []Node{n("VRow", nil, Elements(selected, mode)...)}}, want)
			}
			notice := BuildNotification(selected, c.NotifyType, "2026-10-16")
			if len(c.Notifications) == 0 {
				if notice != nil {
					t.Fatal("unexpected notice", notice)
				}
				return
			}
			if len(c.Notifications) != 1 || notice == nil || notice.Title != c.Notifications[0].Title || notice.Text != c.Notifications[0].Text || c.LastNotify == nil || notice.Fingerprint != c.LastNotify.Fingerprint {
				t.Fatalf("notification differs: %#v, expected %#v", notice, c.Notifications)
			}
		})
	}
	for _, format := range fixture.Formats {
		if actual := FormatSize(format.Value); actual != format.Text {
			t.Errorf("format %v: %q, expected %q", format.Value, actual, format.Text)
		}
	}
}

func TestSiteStatisticsRejectInvalidHistory(t *testing.T) {
	for _, day := range []string{"2026-02-30", "yesterday", "2026-10-16T00:00:00Z"} {
		if _, err := Select(Snapshot{Latest: []Data{{UpdatedDay: day}}}); err == nil {
			t.Fatal("invalid date accepted", day)
		}
	}
}
