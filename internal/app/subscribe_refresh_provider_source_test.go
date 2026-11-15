package app

import (
	"encoding/json"
	"testing"

	"light-media/internal/rsscontext"
	"light-media/internal/sites"
)

func TestSubscriptionRefreshRecognitionUsesGlobalProviderSource(t *testing.T) {
	a, object, _, cfg := originalTransferModuleFixture(t, "copy", "")
	settings := a.Settings()
	source := "douban"
	settings.RecognitionSource = &source
	if err := a.saveSettings(t.Context(), settings); err != nil {
		t.Fatal(err)
	}
	cfg["modules"].(map[string]string)["recognize_media"] = "module_execute"
	cfg["recognize_results"] = map[string]json.RawMessage{
		"name:Refresh Source": historyProviderModel(t, "douban", "705", "刷新来源电影"),
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.pluginStore.saveOriginalPluginConfiguration(t.Context(), object.installation, raw); err != nil {
		t.Fatal(err)
	}
	if err = a.initOriginalLoadedPlugin(t.Context(), object.installation.InstanceID); err != nil {
		t.Fatal(err)
	}
	parser, err := a.originalHostMediaSourceParser(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	row := originalChatLatestRow{Resource: sites.Resource{Title: "Refresh.Source.2024.1080p.WEB-DL"}}
	a.originalChatLatestRecognize(t.Context(), &row, parser)
	if row.Failures != 0 || len(row.Media) == 0 {
		t.Fatalf("subscription refresh recognition failed %#v", row)
	}
	value, err := decodeSourceJSON(row.Media)
	if err != nil {
		t.Fatal(err)
	}
	fields := sourceMap(value)
	if sourceText(fields["source"]) != "douban" || sourceStringValue(fields["douban_id"]) != "705" || fields["tmdb_id"] != nil || sourceSubscriptionTruth(fields["tmdb_id"]) {
		t.Fatalf("subscription refresh media %s", row.Media)
	}
	if identity := rsscontext.ResolveIdentity(originalChatLatestMediaFields(row.Media)).ID; identity == "" || identity == "0" {
		t.Fatalf("subscription refresh identity %q", identity)
	}
}
