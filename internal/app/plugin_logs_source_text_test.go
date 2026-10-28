package app

import (
	"encoding/json"
	"os"
	"regexp"
	"testing"
)

func TestPluginLogsFixedFullTextReversesPhysicalMessageLines(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/standard-plugin-logs/text-source-oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	var source struct {
		Cases []struct{ Name, Input, Expected string }
	}
	if json.Unmarshal(raw, &source) != nil || len(source.Cases) != 3 {
		t.Fatal("fixed full-text oracle")
	}
	for _, sample := range source.Cases[:2] {
		t.Run(sample.Name, func(t *testing.T) {
			a, cookie := pluginLogsFixture(t, 0)
			message := "first\nTraceback (owned):\n  detail"
			if sample.Name == "two-records" {
				message = "first\ncontinuation"
			}
			a.writeLog(map[string]any{"time": "2026-10-26T12:00:00Z", "level": "INFO", "module": "plugins", "message": message, "plugin_instance_id": "one"})
			if sample.Name == "two-records" {
				a.writeLog(map[string]any{"time": "2026-10-26T12:00:01Z", "level": "ERROR", "module": "plugins", "message": "second", "plugin_instance_id": "one"})
			}
			response := callTest(t, a.Handler(), "GET", "/api/plugins/one/logs/file", "", cookie, "")
			if response.Code != 200 || regexp.MustCompile(`[0-9]{4}-[0-9]{2}-[0-9]{2} [0-9]{2}:[0-9]{2}:[0-9]{2},[0-9]{3}`).ReplaceAllString(response.Body.String(), "OWNED_TIMESTAMP") != sample.Expected {
				t.Fatalf("fixed physical text mismatch status%d\ngot=%q\nwant=%q", response.Code, response.Body.String(), sample.Expected)
			}
		})
	}
}
