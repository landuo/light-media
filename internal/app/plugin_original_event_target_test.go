package app

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestOriginalBroadcastEventTargetSourceValues(t *testing.T) {
	for _, test := range []struct {
		name, raw, target string
		restricted        bool
	}{
		{"null", `null`, "", false},
		{"false", `false`, "", false},
		{"zero", `0`, "", false},
		{"negative zero", `-0`, "", false},
		{"decimal zero", `0.0`, "", false},
		{"empty string", `""`, "", false},
		{"empty array", `[]`, "", false},
		{"empty dictionary", `{}`, "", false},
		{"source float underflow", `1e-10000`, "", false},
		{"string", `"OwnedEventTargetFixture"`, "OwnedEventTargetFixture", true},
		{"clone class", `"OwnedEventTargetFixturetwin"`, "OwnedEventTargetFixturetwin", true},
		{"case sensitive", `"ownedeventtargetfixture"`, "ownedeventtargetfixture", true},
		{"true", `true`, "True", true},
		{"large integer", `9007199254740993`, "", true},
		{"nonzero decimal", `0.01`, "", true},
		{"array", `["OwnedEventTargetFixture"]`, "", true},
		{"dictionary", `{"id":"OwnedEventTargetFixture"}`, "", true},
		{"positive float overflow", `1e400`, "inf", true},
		{"negative float overflow", `-1e400`, "", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			raw := json.RawMessage(`{"__mp_target_plugin_id":` + test.raw + `,"big":9007199254740993,"nested":{"__mp_target_plugin_id":"keep","value":9007199254740995},"false":false,"null":null}`)
			before := append([]byte(nil), raw...)
			clean, target, restricted, err := originalBroadcastEventData("message.action", raw)
			if err != nil || target != test.target || restricted != test.restricted {
				t.Fatal(target, restricted, err)
			}
			if !bytes.Equal(raw, before) {
				t.Fatal("broadcast mutated caller payload")
			}
			var got map[string]json.RawMessage
			if err := json.Unmarshal(clean, &got); err != nil {
				t.Fatal(err)
			}
			if _, leaked := got["__mp_target_plugin_id"]; leaked || string(got["big"]) != "9007199254740993" || string(got["false"]) != "false" || string(got["null"]) != "null" || !strings.Contains(string(got["nested"]), `"__mp_target_plugin_id":"keep"`) || !strings.Contains(string(got["nested"]), "9007199254740995") {
				t.Fatal("source top-level removal or exact JSON changed", string(clean))
			}
			other, target, restricted, err := originalBroadcastEventData("plugin.action", raw)
			if err != nil || target != "" || restricted || !bytes.Equal(other, clean) {
				t.Fatal("non-MessageAction event was targeted or retained internal key", string(other), target, restricted, err)
			}
		})
	}
	for _, raw := range []string{`{"big":9007199254740993}`, `null`, `false`, `0`, `[]`, `[9007199254740993]`, `"scalar"`} {
		clean, target, restricted, err := originalBroadcastEventData("message.action", json.RawMessage(raw))
		if err != nil || target != "" || restricted || string(clean) != raw {
			t.Fatal("unrelated payload rewritten", raw, string(clean), target, restricted, err)
		}
	}
}
