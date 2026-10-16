package subscribegroup

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

type referenceFixture struct {
	SHA   string `json:"source_sha256"`
	Cases []struct {
		Name       string `json:"name"`
		Operations []struct {
			Kind     string         `json:"kind"`
			Config   *Configuration `json:"config"`
			Category string         `json:"category"`
			Fallback string         `json:"fallback"`
			ID       string         `json:"id"`
			Download Download       `json:"download"`
		} `json:"operations"`
		Subscriptions []Subscription `json:"subscriptions"`
		Sites         []Site         `json:"sites"`
		RSSSites      []string       `json:"rss_sites"`
		Data          struct {
			History []History `json:"history"`
			Handled []string  `json:"history_handle"`
		} `json:"data"`
		Expected json.RawMessage `json:"expected"`
	} `json:"cases"`
	Parsers []struct {
		Input      string `json:"input"`
		Resolution string `json:"resolution"`
		Quality    string `json:"quality"`
		Effect     string `json:"effect"`
	} `json:"parsers"`
	Form  json.RawMessage `json:"form"`
	Model json.RawMessage `json:"model"`
}

func originalFixture(t *testing.T) referenceFixture {
	t.Helper()
	raw, err := os.ReadFile("../../../testdata/plugins/subscribe-group-original-oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture referenceFixture
	if json.Unmarshal(raw, &fixture) != nil || fixture.SHA != "06870f20e39237e4a69c9615ce005411feaa5e1a97cad50fd3b7b0645bead383" || len(fixture.Cases) != 31 || len(fixture.Parsers) != 36 {
		t.Fatal("incomplete original source oracle")
	}
	return fixture
}
func equalJSON(t *testing.T, actual any, expected json.RawMessage) {
	t.Helper()
	raw, err := json.Marshal(actual)
	if err != nil {
		t.Fatal(err)
	}
	var a, b any
	if json.Unmarshal(raw, &a) != nil || json.Unmarshal(expected, &b) != nil || !reflect.DeepEqual(a, b) {
		t.Fatalf("actual: %s\noriginal: %s", raw, expected)
	}
}
func TestOriginalRuleAndHistoryContract(t *testing.T) {
	fixture := originalFixture(t)
	for _, test := range fixture.Cases {
		t.Run(test.Name, func(t *testing.T) {
			machine := New()
			machine.History = test.Data.History
			machine.Handled = test.Data.Handled
			errors := []string{}
			var page []map[string]any
			for _, op := range test.Operations {
				previous := len(machine.Updates)
				switch op.Kind {
				case "init":
					machine.Init(op.Config, test.Sites)
				case "added":
					category := op.Category
					if category == "" {
						category = op.Fallback
					}
					id := op.ID
					if id == "" {
						id = "sub-one"
					}
					for i := range test.Subscriptions {
						if test.Subscriptions[i].ID == id {
							machine.SubscriptionAdded(&test.Subscriptions[i], category, "2026-10-16 15:00:00")
							break
						}
					}
				case "download":
					if err := machine.DownloadAdded(op.Download, test.Subscriptions, test.RSSSites, "2026-10-16 15:00:00"); err != nil {
						errors = append(errors, err.Error())
					}
				case "page":
					var err error
					page, err = Page(machine.History)
					if err != nil {
						errors = append(errors, err.Error())
					}
				default:
					t.Fatal("unknown reference action", op.Kind)
				}
				// Reference DB rows are mutable objects; carry forward actual planned
				// updates between operations without teaching the planner a database.
				for _, update := range machine.Updates[previous:] {
					for i := range test.Subscriptions {
						if test.Subscriptions[i].ID == update.ID {
							raw, _ := json.Marshal(update.Fields)
							if err := json.Unmarshal(raw, &test.Subscriptions[i]); err != nil {
								t.Fatal(err)
							}
						}
					}
				}
			}
			equalJSON(t, map[string]any{"config": machine.Config, "rules": machine.Rules, "history": machine.History, "handled": machine.Handled, "updates": machine.Updates, "config_writes": machine.ConfigWrites, "state": machine.State(), "page": page, "errors": errors}, test.Expected)
		})
	}
}
func TestOriginalResolutionQualityEffectNormalization(t *testing.T) {
	for _, sample := range originalFixture(t).Parsers {
		t.Run(sample.Input, func(t *testing.T) {
			if got := ParseResolution(sample.Input); got != sample.Resolution {
				t.Errorf("resolution %q != %q", got, sample.Resolution)
			}
			if got := ParseQuality(sample.Input); got != sample.Quality {
				t.Errorf("quality %q != %q", got, sample.Quality)
			}
			if got := ParseEffect(sample.Input); got != sample.Effect {
				t.Errorf("effect %q != %q", got, sample.Effect)
			}
		})
	}
}

func TestOriginalFormAndDefaultModel(t *testing.T) {
	fixture := originalFixture(t)
	var actual map[string]json.RawMessage
	if err := json.Unmarshal(Form(), &actual); err != nil {
		t.Fatal(err)
	}
	equalJSON(t, actual["conf"], fixture.Form)
	equalJSON(t, actual["model"], fixture.Model)
	// Callers cannot mutate the embedded source for later plugin instances.
	copy := Form()
	copy[0] = 'x'
	if !json.Valid(Form()) {
		t.Fatal("form shared mutable bytes")
	}
}

func TestPageNeverTreatsHistoryAsMarkupAndRejectsBrokenEscapes(t *testing.T) {
	page, err := Page([]History{{Name: "<img src=x onerror=alert(1)>", Content: `{"include": "\u003cscript\u003e"}`, Time: "2026-10-16"}})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(page)
	var decoded any
	if json.Unmarshal(raw, &decoded) != nil {
		t.Fatal("unsafe history response")
	}
	// The renderer receives text only, never an html prop or executable event.
	var walk func(any)
	walk = func(value any) {
		switch v := value.(type) {
		case map[string]any:
			for key, item := range v {
				if key == "html" || key == "events" {
					t.Fatal("history gained code authority")
				}
				walk(item)
			}
		case []any:
			for _, item := range v {
				walk(item)
			}
		}
	}
	walk(decoded)
	for _, value := range []string{`\u123`, `\xGG`, `ends\`, `\U00110000`} {
		if _, err := Page([]History{{Content: value}}); err == nil {
			t.Fatal("invalid source escape accepted", value)
		}
	}
}
