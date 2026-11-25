package iyuuautoseed

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	sdk "light-media/builtinplugins/internal/sdk"
)

func TestIYUUSDKSourceBoundaries(t *testing.T) {
	const configured = `{"schema_version":1,"items":[{"id":"host-pt","source_id":3,"source_fields":{"id":3,"name":"owned managed"}}]}`
	custom := `{"schema_version":1,"snapshot":"` + strings.Repeat("a", 64) +
		`","config":{"enabled":true,"sites":[{"id":"4","name":"owned custom","cookie":"OWNED_PRIVATE"}]}}`
	t.Run("form-inventory", func(t *testing.T) {
		host := orderedIYUUHost(t,
			iyuuSDKStep{sdk.SitesSourceCustomConfigCapability, custom, nil},
			iyuuSDKStep{sdk.SitesReadCapability, configured, assertOrderedSourceSites(t)},
			iyuuSDKStep{sdk.DownloaderConfigurationsReadCapability, `{"schema_version":1,"downloaders":[
				{"id":"qb","name":"owned source","kind":"qbittorrent","enabled":true},
				{"id":"qb2","name":"owned source","kind":"qbittorrent","enabled":true},
				{"id":"tr","name":"inactive","kind":"transmission","enabled":false}]}`, nil},
			iyuuSDKStep{sdk.DownloaderConfigurationsReadCapability, `{"schema_version":1,"downloaders":[
				{"id":"tr","name":"owned destination","kind":"transmission","enabled":true}]}`, nil})
		raw, err := formFromHost(t.Context(), host)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), "OWNED_PRIVATE") {
			t.Fatal("custom credentials leaked into the form")
		}
		tree, err := decodeJSON(raw)
		if err != nil {
			t.Fatal(err)
		}
		selects := make(map[string]any)
		collectFormSelects(tree, selects)
		for name, expected := range map[string]string{
			"sites":           `[{"title":"owned managed","value":3},{"title":"owned custom","value":"4"}]`,
			"downloaders":     `[{"title":"owned source","value":"owned source"}]`,
			"auto_downloader": `[{"title":"owned destination","value":"owned destination"}]`,
		} {
			want, err := decodeJSON([]byte(expected))
			if err != nil || !reflect.DeepEqual(selects[name], want) {
				t.Fatalf("wrong %s selection: %#v", name, selects[name])
			}
		}
	})

	t.Run("managed-sites-without-imported-provenance", func(t *testing.T) {
		host := orderedIYUUHost(t, iyuuSDKStep{sdk.SitesReadCapability,
			`{"schema_version":1,"items":[
				{"id":"managed","source_id":7,"name":"native name"},
				{"id":"local","name":"local name"}]}`, assertOrderedSourceSites(t)})
		sites, err := managedSiteOptions(t.Context(), sdk.Sites{Host: host})
		want := []SiteOption{{ID: json.Number("7"), Name: "native name"}, {ID: "local", Name: "local name"}}
		if err != nil || !reflect.DeepEqual(sites, want) {
			t.Fatalf("host identities without imported metadata: %#v, %v", sites, err)
		}
	})

	t.Run("pre-stop-write-and-site-deletion", func(t *testing.T) {
		live := NewConfig()
		host := orderedIYUUHost(t,
			iyuuSDKStep{sdk.SitesReadCapability, configured, assertOrderedSourceSites(t)},
			iyuuSDKStep{sdk.SitesSourceCustomConfigCapability, custom, nil},
			iyuuSDKStep{sdk.BaseUpdateConfig, `true`, assertConfigSites(t, []any{json.Number("3"), "4"}, true)},
			iyuuSDKStep{sdk.BaseGetConfig, `{"sites":[8,"4",3],"enabled":false,"cron":"different saved cron"}`, nil},
			iyuuSDKStep{sdk.BaseUpdateConfig, `true`, assertConfigSites(t, []any{json.Number("8"), "4"}, true)})
		err := prepareConfigFromHost(t.Context(), host, &live, json.RawMessage(
			`{"enabled":true,"onlyonce":true,"sites":["4",3],"cron":"0 1 * * *"}`))
		if err != nil {
			t.Fatal(err)
		}
		err = siteDeletedFromHost(t.Context(), host, &live, json.RawMessage(`{"type":"site.deleted","data":{"site_id":"3"}}`))
		if err != nil {
			t.Fatal(err)
		}
	})

	t.Run("native-downloader-read-projection", func(t *testing.T) {
		hash := strings.Repeat("a", 40)
		host := orderedIYUUHost(t,
			iyuuSDKStep{sdk.DownloaderSourceCompletedCapability,
				`{"schema_version":1,"kind":"qbittorrent","downloader_id":"owned","native_error":false,
					"tasks":[{"hash":"` + hash + `","qbittorrent":{"availability":0.75,"completed":9007199254740992,"total_size":9007199254740993}}]}`,
				func(raw json.RawMessage) {
					var request sdk.DownloaderSourceCompletedRequestV1
					if json.Unmarshal(raw, &request) != nil || !request.IncludeQBPackFields || request.DownloaderID != "owned" || request.Hashes != nil {
						t.Fatal("source completion request lost the native pack-field opt-in or widened the call")
					}
				}},
			iyuuSDKStep{sdk.DownloaderSourceTasksCapability,
				`{"schema_version":1,"kind":"qbittorrent","downloader_id":"owned","native_error":true,"tasks":[]}`,
				func(raw json.RawMessage) {
					var request sdk.DownloaderTasksRequestV1
					if json.Unmarshal(raw, &request) != nil || !reflect.DeepEqual(request.Hashes, []string{hash}) {
						t.Fatal("recheck lookup did not keep the exact hash")
					}
				}})
		reads := sourceDownloaderReads{client: sdk.Downloaders{Host: host}}
		service := Service{Ref: "owned", Kind: "qbittorrent"}
		torrents, err := reads.Completed(t.Context(), service)
		if err != nil || len(torrents) != 1 {
			t.Fatalf("source completed: %v", err)
		}
		fields := torrents[0].Fields
		if fields["availability"] != json.Number("0.75") || fields["completed"] != json.Number("9007199254740992") ||
			fields["total_size"] != json.Number("9007199254740993") {
			t.Fatalf("qB pack fields changed: %#v", fields)
		}
		torrents, err = reads.Torrents(t.Context(), service, []string{hash})
		if err != nil || torrents == nil || len(torrents) != 0 {
			t.Fatalf("native error should project to the source wrapper's empty list: %v", err)
		}
	})
}

type iyuuSDKStep struct {
	method, reply string
	check         func(json.RawMessage)
}

func orderedIYUUHost(t *testing.T, steps ...iyuuSDKStep) sdk.HostCaller {
	t.Helper()
	next := 0
	t.Cleanup(func() {
		if next != len(steps) {
			t.Errorf("executed %d of %d source host calls", next, len(steps))
		}
	})
	return func(_ context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
		t.Helper()
		if next == len(steps) || method != steps[next].method {
			t.Fatalf("unexpected source host call at %d: %s", next, method)
		}
		step := steps[next]
		next++
		if step.check != nil {
			step.check(raw)
		}
		return json.RawMessage(step.reply), nil
	}
}

func assertOrderedSourceSites(t *testing.T) func(json.RawMessage) {
	return func(raw json.RawMessage) {
		var request sdk.SiteHostRequestV1
		if json.Unmarshal(raw, &request) != nil || !request.OrderByPriority || !request.SourceProjection || request.ActiveOnly {
			t.Fatal("site query lost source order or excluded inactive records")
		}
	}
}

func assertConfigSites(t *testing.T, expected []any, enabled bool) func(json.RawMessage) {
	return func(raw json.RawMessage) {
		var update struct {
			Config map[string]any `json:"config"`
		}
		decoder := json.NewDecoder(strings.NewReader(string(raw)))
		decoder.UseNumber()
		if err := decoder.Decode(&update); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(update.Config["sites"], expected) || update.Config["enabled"] != enabled ||
			update.Config["cron"] != "0 1 * * *" || update.Config["onlyonce"] != true {
			t.Fatalf("configuration write used wrong source fields: %#v", update.Config)
		}
	}
}

func collectFormSelects(value any, out map[string]any) {
	switch value := value.(type) {
	case []any:
		for _, item := range value {
			collectFormSelects(item, out)
		}
	case map[string]any:
		if props, ok := value["props"].(map[string]any); ok {
			if model, ok := props["model"].(string); ok {
				out[model] = props["items"]
			}
		}
		collectFormSelects(value["content"], out)
	}
}
