package commandrouter

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"
)

func TestInstalledFourteenCommandDefinitionsAndSplitting(t *testing.T) {
	raw, e := os.ReadFile("../../testdata/lark-command-source/oracle.json")
	if e != nil {
		t.Fatal(e)
	}
	var corpus struct {
		Commands map[string]struct {
			ID, Type, Description string
			Category              *string
			Arguments             int
		}
		Routes []struct {
			Text, Error string
			Expected    []map[string]any
		}
	}
	if e = json.Unmarshal(raw, &corpus); e != nil || len(corpus.Commands) != 14 || len(corpus.Routes) != 22 {
		t.Fatal(e)
	}
	var ordered struct{ Commands json.RawMessage }
	if e = json.Unmarshal(raw, &ordered); e != nil {
		t.Fatal(e)
	}
	dec := json.NewDecoder(bytes.NewReader(ordered.Commands))
	if _, e = dec.Token(); e != nil {
		t.Fatal(e)
	}
	names := []string{}
	for dec.More() {
		k, e := dec.Token()
		if e != nil {
			t.Fatal(e)
		}
		names = append(names, k.(string))
		var skip json.RawMessage
		if e = dec.Decode(&skip); e != nil {
			t.Fatal(e)
		}
	}
	presets := Presets()
	if len(presets) != 14 {
		t.Fatal(len(presets))
	}
	for i, c := range presets {
		if c.Name != names[i] {
			t.Fatal("source command insertion order lost", c.Name, names[i])
		}
		t.Run(c.Name, func(t *testing.T) {
			s, ok := corpus.Commands[c.Name]
			if !ok || s.ID != c.ID || s.Description != c.Description || s.Arguments != c.Arguments || (s.Category == nil) != (c.Category == nil) || s.Category != nil && *s.Category != *c.Category || s.Type == "scheduler" && c.Kind != "scheduler" {
				t.Fatal(c, s)
			}
		})
	}
	for _, r := range corpus.Routes {
		t.Run(r.Text, func(t *testing.T) {
			name, args := Split(r.Text)
			c, exists := Lookup(name)
			_ = c
			var invocation map[string]any
			for _, e := range r.Expected {
				if e["cmd"] != nil {
					invocation = e
				}
			}
			if invocation != nil {
				if !exists || name != invocation["cmd"] || args != invocation["data_str"] {
					t.Fatal(name, args, invocation)
				}
			} else if exists {
				t.Fatal("unknown source command dispatched", name)
			}
		})
	}
}
func TestSourcePresetAuditRejectsUnknownInstallation(t *testing.T) {
	if e := AdmitPreset([]Installation{{"LarkMessager", "0.9.1"}}); e != nil {
		t.Fatal(e)
	}
	for _, i := range []Installation{{"other", "1"}, {"LarkMessager", "next"}, {"LarkMessager", ""}} {
		if AdmitPreset([]Installation{i}) == nil {
			t.Fatal("unknown registry assumed empty", i)
		}
	}
}
