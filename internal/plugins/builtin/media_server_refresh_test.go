package builtin

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"light-media/internal/plugins"
)

func TestMediaServerRefreshUsesOriginalFieldsAndHostOwnedDebounce(t *testing.T) {
	ctx := context.Background()
	methods := []string{}
	host := func(_ context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
		methods = append(methods, method)
		switch method {
		case "media.servers":
			return json.RawMessage(`[{"title":"飞牛影视","value":"fnos"}]`), nil
		case "events.configure":
			if string(raw) != `{"active":true,"delay_ms":1500,"event_types":["import.completed"],"handler":"refresh"}` {
				t.Fatal("incorrect event registration", string(raw))
			}
			return json.RawMessage(`{"registered":true}`), nil
		case "media.refresh":
			if string(raw) != `{"batch_id":"pb_fixture"}` {
				t.Fatal("plugin passed private paths or changed batch", string(raw))
			}
			return nil, errors.New("synthetic scan rejected")
		}
		return nil, errors.New("unexpected capability")
	}
	form, err := HandleMediaServerRefresh(ctx, "config_form", nil, host)
	if err != nil || !strings.Contains(string(form), "延迟时间（秒）") || !strings.Contains(string(form), "飞牛影视") || !strings.Contains(string(form), `"clearable":true`) {
		t.Fatal("original configuration form missing", err)
	}
	config := json.RawMessage(`{"enabled":true,"delay":"1.5","mediaservers":["fnos"]}`)
	payload, _ := json.Marshal(plugins.Invocation{Action: "init", Config: config})
	if _, err := HandleMediaServerRefresh(ctx, "invoke", payload, host); err != nil {
		t.Fatal(err)
	}
	payload, _ = json.Marshal(plugins.Invocation{Action: "event", Config: config, Input: json.RawMessage(`{"batch_id":"pb_fixture"}`)})
	if _, err := HandleMediaServerRefresh(ctx, "invoke", payload, host); err == nil {
		t.Fatal("scan rejection reported success")
	}
	if strings.Join(methods, ",") != "media.servers,events.configure,media.refresh" {
		t.Fatal("unexpected plugin operations", methods)
	}
}

func TestMediaServerRefreshDisabledHasNoRefresh(t *testing.T) {
	config := json.RawMessage(`{"enabled":false,"delay":0,"mediaservers":["fnos"]}`)
	payload, _ := json.Marshal(plugins.Invocation{Action: "event", Config: config, Input: json.RawMessage(`{"batch_id":"pb_fixture"}`)})
	host := func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
		t.Fatal("disabled plugin performed a host operation")
		return nil, nil
	}
	result, err := HandleMediaServerRefresh(context.Background(), "invoke", payload, host)
	if err != nil || string(result) != `{"skipped":true}` {
		t.Fatal(err)
	}
}

func TestMediaServerRefreshPreservesLongAndAlreadyElapsedDelays(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want time.Duration
	}{{`"604800"`, 7 * 24 * time.Hour}, {`-60`, 0}, {`"0.0001"`, time.Millisecond}} {
		config := MediaServerRefreshConfiguration{Delay: json.RawMessage(tc.raw)}
		got, err := config.Duration()
		if err != nil || got != tc.want {
			t.Fatal("original configured delay changed", tc.raw, got, err)
		}
	}
}
