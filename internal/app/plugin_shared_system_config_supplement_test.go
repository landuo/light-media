package app

import (
	"bytes"
	"context"
	"encoding/json"
	"light-media/internal/plugins"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type systemConfigSourceState struct {
	RawJSON    string         `json:"raw_json"`
	RawError   map[string]any `json:"raw_json_error"`
	Order      []string       `json:"iteration_order"`
	PythonType string         `json:"python_type"`
}
type systemConfigSupplementObservation struct {
	Case     string                  `json:"case"`
	Method   string                  `json:"method"`
	Returned json.RawMessage         `json:"returned"`
	State    systemConfigSourceState `json:"state"`
}
type systemConfigSupplementPhase struct {
	Input        string                              `json:"input_literal"`
	Initial      systemConfigSourceState             `json:"initial"`
	Observations []systemConfigSupplementObservation `json:"observations"`
}

func assertSystemConfigIndependentState(t *testing.T, p *originalPluginInstance, source systemConfigSourceState) {
	t.Helper()
	raw, e := sharedSystemConfigCall(t, p, plugins.SharedSystemConfigGet, plugins.SharedSystemConfigGetV1{Key: plugins.SystemConfigCustomIdentifiers})
	if source.RawError != nil {
		if e == nil {
			t.Fatal("source JSON failure omitted", string(raw))
		}
		return
	}
	if e != nil {
		t.Fatal(e)
	}
	assertSharedOracleJSON(t, raw, json.RawMessage(source.RawJSON))
	if source.PythonType == "float" && !bytes.ContainsAny(raw, ".eE") {
		t.Fatalf("source hot float became integer token: %s", raw)
	}
	if source.PythonType == "int" && bytes.ContainsAny(raw, ".eE") {
		t.Fatalf("source integer became float: %s", raw)
	}
	if source.Order != nil {
		keys := []string{}
		d := json.NewDecoder(bytes.NewReader(raw))
		_, _ = d.Token()
		for d.More() {
			key, e := d.Token()
			if e != nil {
				t.Fatal(e)
			}
			keys = append(keys, key.(string))
			var value json.RawMessage
			if e = d.Decode(&value); e != nil {
				t.Fatal(e)
			}
		}
		got, _ := json.Marshal(keys)
		want, _ := json.Marshal(source.Order)
		if !bytes.Equal(got, want) {
			t.Fatalf("source iteration order %s want %s", got, want)
		}
	}
}
func TestSharedSystemConfigIndependentSupplementActualWorkers(t *testing.T) {
	raw, e := os.ReadFile(filepath.Join("..", "..", "testdata", "plugin-shared-host-20261030", "system-config", "source", "independent-supplement.json"))
	if e != nil {
		t.Fatal(e)
	}
	var data struct {
		Groups map[string][]systemConfigSupplementPhase `json:"groups"`
	}
	if e = json.Unmarshal(raw, &data); e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{"dict-reorder", "bool-number-equality", "numeric-float-integral", "numeric-negative-zero", "numeric-integer-precise", "numeric-integer-large", "numeric-nonfinite", "numeric-nested-nonfinite"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			markets := []string{}
			a, e := New(Options{DataDir: dir, PluginMarketsOverride: &markets})
			if e != nil {
				t.Fatal(e)
			}
			defer func() { _ = a.Close() }()
			cookie := setupTestAdmin(t, a)
			p := installSharedSystemConfigFixture(t, a, cookie, sharedSystemConfigAll)
			for index, phase := range data.Groups[name] {
				if index > 0 {
					if e = a.Close(); e != nil {
						t.Fatal(e)
					}
					a, e = New(Options{DataDir: dir, PluginMarketsOverride: &markets})
					if e != nil {
						t.Fatal(e)
					}
					p = a.startOriginalPlugin(context.Background(), "OwnedSystemConfig")
					if p == nil {
						t.Fatal("cold worker missing")
					}
				}
				assertSystemConfigIndependentState(t, p, phase.Initial)
				var plan struct {
					Operations []systemConfigOracleInput `json:"operations"`
				}
				if e = json.Unmarshal([]byte(phase.Input), &plan); e != nil {
					t.Fatal(e)
				}
				for i, op := range plan.Operations {
					observation := phase.Observations[i]
					t.Logf("independent source %s phase%d case=%s", name, index, op.Case)
					switch op.Method {
					case "set":
						result, e := systemConfigSet(t, p, string(op.Value))
						if e != nil {
							t.Fatal(e)
						}
						assertSharedOracleJSON(t, result, observation.Returned)
					case "prepare":
						var returned []json.RawMessage
						if e = json.Unmarshal(observation.Returned, &returned); e != nil {
							t.Fatal(e)
						}
						var expected string
						if e = json.Unmarshal(returned[0], &expected); e != nil {
							t.Fatal(e)
						}
						title := op.Title
						if title == "" {
							title = "A"
						} // Independent source driver's explicit default.
						if got := a.prepareRecognitionTitle(title); got != expected {
							t.Fatalf("source prepare %q native %q", expected, got)
						}
					case "get":
					case "mutate_return":
						value := systemConfigGet(t, p)
						if len(value) > 0 {
							value[0] = 'x'
						}
					default:
						t.Fatal("unsupported supplement method", op.Method)
					}
					assertSystemConfigIndependentState(t, p, observation.State)
				}
			}
		})
	}
}
func TestSharedSystemConfigSourceIgnoredLargeCommentActualWorker(t *testing.T) {
	a, _, p := sharedSystemConfigFixture(t, sharedSystemConfigAll)
	raw, _ := json.Marshal([]string{"#" + strings.Repeat("x", 65536), "A => B"})
	if _, e := systemConfigSet(t, p, string(raw)); e != nil {
		t.Fatal(e)
	}
	assertSharedOracleJSON(t, systemConfigGet(t, p), raw)
	if got := a.prepareRecognitionTitle("A"); got != "B" {
		t.Fatalf("fixed original ignored-large-rule actual source returned B; native=%q", got)
	}
}
