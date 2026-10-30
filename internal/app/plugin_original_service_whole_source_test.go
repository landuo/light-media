package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// The oracle is captured from normally imported, fixed MoviePilot components,
// not calculated from the target. The host's established secret-redaction
// boundary is recorded separately from unmodified source error observations.
func TestOriginalServiceWholeSourceProgress(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", "testdata", "standard-services", "whole-source-progress.json"))
	if err != nil {
		t.Fatal(err)
	}
	var source struct {
		Commit string `json:"source_commit"`
		Cases  []struct {
			Case       string                     `json:"case"`
			Input      map[string]json.RawMessage `json:"input"`
			Raw        map[string]json.RawMessage `json:"raw_source_progress"`
			Projection map[string]json.RawMessage `json:"safe_projection"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(body, &source); err != nil {
		t.Fatal(err)
	}
	if source.Commit != "8a83efeacd3e26b82be495e17d50146e31c96a60" || len(source.Cases) != 6 {
		t.Fatal("fixed whole-source oracle identity or six manual scenarios missing")
	}
	a, cookie := installOwnedInputFixture(t)
	outputs := make([]map[string]any, 0, len(source.Cases))
	seen := map[string]bool{}
	for _, sample := range source.Cases {
		if seen[sample.Case] {
			t.Fatal("duplicate source case", sample.Case)
		}
		seen[sample.Case] = true
		t.Run(sample.Case, func(t *testing.T) {
			config, err := json.Marshal(map[string]any{"enabled": true, "services": []map[string]any{{
				"id": "owned_progress", "name": "Owned manual", "handler": "run_service",
				"trigger": map[string]any{"kind": "interval", "timezone": "UTC", "interval_ms": 3600000}, "input": sample.Input,
			}}})
			if err != nil {
				t.Fatal(err)
			}
			r := callTest(t, a.Handler(), "PUT", "/api/plugins/OwnedInput", string(config), cookie, "")
			assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
			a.wg.Wait()
			r = callTest(t, a.Handler(), "GET", "/api/v1/system/runscheduler?jobid=OwnedInput_owned_progress", "", cookie, "")
			assertOriginalServiceWireSuccess(t, r.Code, r.Body.Bytes())
			a.wg.Wait()
			got := ownedProgressRead(t, a, cookie)
			for _, key := range []string{"status", "success", "enable", "value", "error"} {
				var want, actual any
				if json.Unmarshal(sample.Projection[key], &want) != nil || json.Unmarshal(got[key], &actual) != nil || !reflect.DeepEqual(want, actual) {
					t.Fatalf("%s source projection %s target %s", key, sample.Projection[key], got[key])
				}
			}
			var wantData, actualData map[string]json.RawMessage
			if json.Unmarshal(sample.Projection["data"], &wantData) != nil || json.Unmarshal(got["data"], &actualData) != nil || string(wantData["owned"]) != "9007199254740993" || string(actualData["owned"]) != string(wantData["owned"]) {
				t.Fatal("source integer payload not preserved exactly")
			}
			// Error-empty is intentionally distinct from successful null.
			if sample.Case == "manual_error-empty" && string(sample.Raw["error"]) != `""` {
				t.Fatal("raw source empty error lost")
			}
			if sample.Case == "manual_list-fail" && string(sample.Raw["error"]) != "null" {
				t.Fatal("raw source successful null lost")
			}
			outputs = append(outputs, map[string]any{"case": sample.Case, "observed": got, "raw_source_error": sample.Raw["error"], "safe_error": sample.Projection["error"]})
		})
	}
	out, err := json.MarshalIndent(outputs, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	outputPath := os.Getenv("OWNED_SERVICE_TARGET_OBSERVATIONS")
	if outputPath == "" {
		outputPath = filepath.Join(t.TempDir(), "whole-source-target.json")
	}
	if err := os.WriteFile(outputPath, append(out, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
}
