package builtin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"light-media/internal/plugins"
	"light-media/internal/plugins/subscribegroup"
)

func TestSubscribeGroupCatalogFormAndMinimalHostAuthority(t *testing.T) {
	manifest, _, found := Lookup("SubscribeGroup")
	if !found || manifest.Validate() != nil || manifest.Version != "2.8.7" || !manifest.HasPage || !reflect.DeepEqual(manifest.Capabilities, []string{"subscriptions.rules.configure", "subscriptions.rules.apply", "subscriptions.rules.history"}) {
		t.Fatal("missing pinned native implementation")
	}
	form, err := HandleSubscribeGroup(context.Background(), "config_form", nil, nil)
	if err != nil || !bytes.Equal(form, subscribegroup.Form()) {
		t.Fatal("source form/default models changed", err)
	}
	calls := []string{}
	host := func(_ context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
		calls = append(calls, method+":"+string(raw))
		return json.RawMessage(`{"updated":1}`), nil
	}
	for _, action := range []string{"init", "reload", "event"} {
		input, _ := json.Marshal(plugins.Invocation{Action: action, Config: json.RawMessage(`{"enabled":true}`), Input: json.RawMessage(`{"batch_id":"pb_fixture","patch":{"include":"evil"},"metadata":{"team":"evil"}}`)})
		if _, err := HandleSubscribeGroup(context.Background(), "invoke", input, host); err != nil {
			t.Fatal(err)
		}
	}
	if !reflect.DeepEqual(calls, []string{`subscriptions.rules.configure:{}`, `subscriptions.rules.configure:{}`, `subscriptions.rules.apply:{"batch_id":"pb_fixture"}`}) {
		t.Fatal("worker gained authority over metadata/patches", calls)
	}
	for _, input := range []string{`{"action":"event","input":{}}`, `{"action":"service","input":{}}`, `{"action":"page","input":{}}`} {
		if _, err := HandleSubscribeGroup(context.Background(), "invoke", json.RawMessage(input), host); err == nil {
			t.Fatal("unsupported action accepted", input)
		}
	}
}

func TestSubscribeGroupPageRetainsLegacyObjectAndSurfacesSourceErrors(t *testing.T) {
	for _, raw := range []string{`{"time":"2026-10-16","name":"old","content":"\\u4e2d"}`, `null`} {
		result, err := HandleSubscribeGroup(context.Background(), "page", json.RawMessage(`{"action":"page","config":{}}`), func(_ context.Context, method string, input json.RawMessage) (json.RawMessage, error) {
			if method != "subscriptions.rules.history" || string(input) != `{}` {
				t.Fatal("unexpected page authority", method, string(input))
			}
			return json.RawMessage(raw), nil
		})
		if err != nil || !bytes.Contains(result, []byte(`"render_mode":"vuetify"`)) {
			t.Fatal("source page missing", err)
		}
		if raw != `null` && !bytes.Contains(result, []byte(`"text":"中"`)) {
			t.Fatal("legacy object's content was lost", string(result))
		}
	}
	for _, raw := range []string{`["bad"]`, `{"content":true}`, `[{"time":"2026"},{}]`, `{"content":"\\u123"}`} {
		if result, err := HandleSubscribeGroup(context.Background(), "page", json.RawMessage(`{"action":"page"}`), func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
			return json.RawMessage(raw), nil
		}); err == nil || len(result) != 0 {
			t.Fatal("source failure replaced by an empty page", raw, string(result), err)
		}
	}
	if _, err := HandleSubscribeGroup(context.Background(), "page", json.RawMessage(`{"action":"page"}`), func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
		return nil, errors.New("synthetic read failure")
	}); err == nil {
		t.Fatal("history read failure swallowed")
	}
}
