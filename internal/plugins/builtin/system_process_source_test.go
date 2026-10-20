package builtin

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

func TestSystemProcessInstalledSourceOracle(t *testing.T) {
	raw, err := os.ReadFile("testdata/system-process-original.json")
	if err != nil {
		t.Fatal(err)
	}
	var oracle struct {
		Form  []json.RawMessage `json:"form"`
		Cases []struct {
			Name      string            `json:"name"`
			Config    json.RawMessage   `json:"config"`
			State     bool              `json:"state"`
			Dashboard []json.RawMessage `json:"dashboard"`
			Page      json.RawMessage   `json:"page"`
		} `json:"cases"`
	}
	if err = json.Unmarshal(raw, &oracle); err != nil {
		t.Fatal(err)
	}
	check := func(t *testing.T, got json.RawMessage, want json.RawMessage) {
		t.Helper()
		var a, b any
		if json.Unmarshal(got, &a) != nil || json.Unmarshal(want, &b) != nil || !reflect.DeepEqual(a, b) {
			t.Fatalf("installed source output differs: got %s want %s", got, want)
		}
	}
	form, err := HandleSystemProcess(context.Background(), "config_form", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	var f struct {
		Conf  json.RawMessage `json:"conf"`
		Model json.RawMessage `json:"model"`
	}
	if json.Unmarshal(form, &f) != nil {
		t.Fatal("invalid form")
	}
	check(t, f.Conf, oracle.Form[0])
	check(t, f.Model, oracle.Form[1])
	for _, c := range oracle.Cases {
		t.Run(c.Name, func(t *testing.T) {
			var cfg SystemProcessConfiguration
			if err := json.Unmarshal(c.Config, &cfg); err != nil {
				t.Fatal(err)
			}
			if cfg.State() != c.State {
				t.Fatal("source truth differs")
			}
			input, _ := json.Marshal(map[string]any{"config": c.Config})
			host := func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
				return json.RawMessage(`[]`), nil
			}
			for _, method := range []string{"page", "dashboard"} {
				got, err := HandleSystemProcess(context.Background(), method, input, host)
				if err != nil {
					t.Fatal(method, err)
				}
				var obj map[string]json.RawMessage
				if err = json.Unmarshal(got, &obj); err != nil {
					t.Fatal(err)
				}
				if method == "page" {
					check(t, obj["page"], c.Page)
				} else {
					for i, k := range []string{"cols", "attrs", "elements"} {
						check(t, obj[k], c.Dashboard[i])
					}
				}
			}
		})
	}
}
