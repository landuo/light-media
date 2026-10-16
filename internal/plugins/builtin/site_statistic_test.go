package builtin

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

func TestSiteStatisticOriginalFormAndWorkerActions(t *testing.T) {
	raw, err := os.ReadFile("../../../testdata/plugins/site-statistic-original-oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Form  any `json:"form"`
		Model any `json:"model"`
	}
	if json.Unmarshal(raw, &fixture) != nil {
		t.Fatal("invalid oracle")
	}
	form, err := HandleSiteStatistic(context.Background(), "config_form", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Conf  any `json:"conf"`
		Model any `json:"model"`
	}
	json.Unmarshal(form, &got)
	if !reflect.DeepEqual(got.Conf, fixture.Form) || !reflect.DeepEqual(got.Model, fixture.Model) {
		t.Fatal("original form/defaults changed")
	}
	if err := SiteStatisticManifest().Validate(); err != nil {
		t.Fatal(err)
	}
	for action, capability := range map[string]string{"init": "site.statistics.configure", "reload": "site.statistics.configure", "event": "site.statistics.notify", "service": "site.statistics.refresh"} {
		input := json.RawMessage(`{"test":"synthetic-binding"}`)
		invocation, _ := json.Marshal(map[string]any{"action": action, "config": map[string]any{}, "input": input})
		called := false
		_, err := HandleSiteStatistic(context.Background(), "invoke", invocation, func(ctx context.Context, method string, payload json.RawMessage) (json.RawMessage, error) {
			called = true
			if method != capability {
				t.Fatal(method, capability)
			}
			if action == "event" || action == "service" {
				if string(payload) != string(input) {
					t.Fatal("binding changed")
				}
			}
			return json.RawMessage(`{}`), nil
		})
		if err != nil || !called {
			t.Fatal("worker did not call actual host", action, err)
		}
	}
}
