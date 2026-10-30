package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"light-media/internal/plugins"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type systemConfigOracleObservation struct {
	Case      string            `json:"case"`
	Method    string            `json:"method"`
	Returned  json.RawMessage   `json:"returned"`
	Get       json.RawMessage   `json:"get"`
	Error     map[string]any    `json:"error"`
	Persisted []json.RawMessage `json:"persisted"`
}
type systemConfigOracleOperation struct {
	Source    systemConfigOracleObservation `json:"source"`
	Persisted []json.RawMessage             `json:"persisted"`
}
type systemConfigOracleGroup struct {
	Initial    []json.RawMessage               `json:"initial_rows"`
	Operations []systemConfigOracleOperation   `json:"operations"`
	Cold       []systemConfigOracleObservation `json:"cold"`
}
type systemConfigOracleInput struct {
	Case   string          `json:"case"`
	Method string          `json:"method"`
	Value  json.RawMessage `json:"value"`
	Title  string          `json:"title"`
	SQL    string          `json:"fixture_sql"`
}

func systemConfigHasNonfinite(raw json.RawMessage) bool {
	return bytes.Contains(raw, []byte(`"$source_python_float"`))
}
func assertSystemConfigSourceValue(t *testing.T, got, want json.RawMessage) {
	t.Helper()
	if !systemConfigHasNonfinite(want) {
		assertSharedOracleJSON(t, got, want)
		return
	}
	_, nonfinite, e := sharedSystemConfigJSON(got)
	if e != nil || !nonfinite {
		t.Fatalf("expected source nonfinite, got %s: %v", got, e)
	}
}
func assertSystemConfigStored(t *testing.T, a *App, want []json.RawMessage) {
	t.Helper()
	observed, e := readSharedSystemConfigRow(context.Background(), a.Store.DB)
	if e != nil {
		t.Fatal(e)
	}
	if len(want) == 0 {
		if observed.present {
			t.Fatal("source deleted row remains persisted")
		}
		return
	}
	if len(want) != 1 || !observed.present {
		t.Fatal("missing source row")
	}
	raw, e := a.pluginStore.unseal("core_words", "system", "CustomIdentifiers", []byte(observed.sealed))
	if e != nil {
		t.Fatal(e)
	}
	assertSystemConfigSourceValue(t, raw, want[0])
	if bytes.Contains([]byte(observed.sealed), []byte("Raw =>")) {
		t.Fatal("plaintext rules in encrypted column")
	}
}
func assertSystemConfigRead(t *testing.T, p *originalPluginInstance, want json.RawMessage) {
	t.Helper()
	raw, e := sharedSystemConfigCall(t, p, plugins.SharedSystemConfigGet, plugins.SharedSystemConfigGetV1{Key: plugins.SystemConfigCustomIdentifiers})
	if systemConfigHasNonfinite(want) {
		if e == nil {
			t.Fatalf("source raw JSON nonfinite boundary incorrectly succeeded: %s", raw)
		}
		return
	}
	if e != nil {
		t.Fatal(e)
	}
	assertSharedOracleJSON(t, raw, want)
}
func assertSystemConfigConsumer(t *testing.T, a *App, title string, source systemConfigOracleObservation) {
	t.Helper()
	_, _, e := a.recognitionWords(context.Background())
	if source.Error != nil {
		if e == nil {
			t.Fatal("source consumer rejected raw JSON but native accepted")
		}
		var api *APIError
		if !errors.As(e, &api) {
			t.Fatal(e)
		}
		want := "SYSTEM_CONFIG_CONSUMER_TYPE"
		if source.Error["class"] == "AttributeError" {
			want = "SYSTEM_CONFIG_CONSUMER_ATTRIBUTE"
		}
		if api.Code != want {
			t.Fatalf("consumer error %s want %s", api.Code, want)
		}
		if got := a.prepareRecognitionTitle(title); got != "" {
			t.Fatalf("invalid rules silently consumed: %s", got)
		}
		return
	}
	if e != nil {
		t.Fatal(e)
	}
	var result []json.RawMessage
	if e = json.Unmarshal(source.Returned, &result); e != nil || len(result) != 2 {
		t.Fatal(e)
	}
	var want string
	_ = json.Unmarshal(result[0], &want)
	if got := a.prepareRecognitionTitle(title); got != want {
		t.Fatalf("native title %q want source %q", got, want)
	}
}
func TestSharedSystemConfigSourceOracleActualWorkers(t *testing.T) {
	root := filepath.Join("..", "..", "testdata", "plugin-shared-host-20261030", "system-config", "source")
	bytes, e := os.ReadFile(filepath.Join(root, "oracle.json"))
	if e != nil {
		t.Fatal(e)
	}
	var oracle struct {
		Groups map[string]systemConfigOracleGroup `json:"groups"`
	}
	if e = json.Unmarshal(bytes, &oracle); e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{"ordinary", "raw-values", "failure-hot", "failure-delete", "failure-insert", "native-ui", "json-numbers"} {
		t.Run(name, func(t *testing.T) {
			group := oracle.Groups[name]
			var plan struct {
				Operations []systemConfigOracleInput `json:"operations"`
			}
			input, e := os.ReadFile(filepath.Join(root, name+".json"))
			if e != nil {
				t.Fatal(e)
			}
			if e = json.Unmarshal(input, &plan); e != nil {
				t.Fatal(e)
			}
			dir := t.TempDir()
			markets := []string{}
			a, e := New(Options{DataDir: dir, PluginMarketsOverride: &markets})
			if e != nil {
				t.Fatal(e)
			}
			defer func() { _ = a.Close() }()
			cookie := setupTestAdmin(t, a)
			p := installSharedSystemConfigFixture(t, a, cookie, sharedSystemConfigAll)
			for _, raw := range group.Initial {
				if _, e = systemConfigSet(t, p, string(raw)); e != nil {
					t.Fatal(e)
				}
			}
			for i, op := range plan.Operations {
				source := group.Operations[i]
				t.Logf("source ordinary default DAO/WordsMatcher case %s/%s", name, op.Case)
				if op.SQL != "" {
					sql := strings.ReplaceAll(op.SQL, "ON systemconfig", "ON recognition_words")
					sql = strings.ReplaceAll(sql, "NEW.key", "NEW.id")
					if _, e = a.Store.DB.Exec(sql); e != nil {
						t.Fatal(e)
					}
				}
				var result json.RawMessage
				var callError error
				switch op.Method {
				case "set", "deepcopy_set":
					result, callError = systemConfigSet(t, p, string(op.Value))
				case "get":
					result, callError = sharedSystemConfigCall(t, p, plugins.SharedSystemConfigGet, plugins.SharedSystemConfigGetV1{Key: plugins.SystemConfigCustomIdentifiers})
				case "deepcopy_get":
					result = systemConfigGet(t, p)
					result = append(result, 0) // Local caller mutation must not alter host cache.
				case "prepare":
					assertSystemConfigConsumer(t, a, op.Title, source.Source)
				case "async_set":
					v, e := a.setSharedSystemConfigFromUI(context.Background(), op.Value)
					callError = e
					if e == nil {
						result, _ = json.Marshal(v)
					}
				case "ui_set":
					var lines []string
					if e = json.Unmarshal(op.Value, &lines); e != nil {
						t.Fatal(e)
					}
					response := callTest(t, a.Handler(), "PUT", "/api/settings/recognition-words", string(mustCleanSeedJSON(map[string]any{"text": strings.Join(lines, "\n")})), cookie, "")
					if response.Code != 200 {
						t.Fatalf("native UI endpoint %d: %s", response.Code, response.Body)
					}
				default:
					t.Fatalf("unhandled original operation %s", op.Method)
				}
				if op.Method != "prepare" && op.Method != "deepcopy_get" && op.Method != "ui_set" {
					if source.Source.Error != nil {
						if callError == nil {
							t.Fatalf("expected source SQL failure, got %s", result)
						}
					} else {
						if callError != nil {
							t.Fatal(callError)
						}
						assertSharedOracleJSON(t, result, source.Source.Returned)
					}
				}
				assertSystemConfigRead(t, p, source.Source.Get)
				assertSystemConfigStored(t, a, source.Persisted)
			}
			before := sharedSystemConfigWorkerSnapshot(t, p)
			if e = a.Close(); e != nil {
				t.Fatal(e)
			}
			a, e = New(Options{DataDir: dir, PluginMarketsOverride: &markets})
			if e != nil {
				t.Fatal("cold startup rejected source JSON", e)
			}
			p = a.startOriginalPlugin(context.Background(), "OwnedSystemConfig")
			if p == nil {
				t.Fatal("cold external worker absent")
			}
			after := sharedSystemConfigWorkerSnapshot(t, p)
			if string(before["pid"]) == string(after["pid"]) {
				t.Fatal("cold reused worker")
			}
			t.Logf("real worker cold old=%s new=%s", before["pid"], after["pid"])
			for _, source := range group.Cold {
				if source.Method == "prepare" {
					assertSystemConfigConsumer(t, a, "Raw.S01E02", source)
				}
				assertSystemConfigRead(t, p, source.Get)
				assertSystemConfigStored(t, a, source.Persisted)
			}
		})
	}
}
