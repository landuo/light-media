package app

import (
	"context"
	"encoding/json"
	"light-media/internal/plugins"
	"os"
	"testing"
)

func TestOriginalHostBusinessSettingsActualSDKFixedSourceFalseyAndCold(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/plugin-host-business-20261031/source-oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	var oracle struct {
		Commit   string `json:"source_commit"`
		Settings struct {
			Cases []struct {
				Name           string
				Initial        json.RawMessage
				Next           json.RawMessage
				ExpectedReturn *bool           `json:"expected_return"`
				ExpectedValue  json.RawMessage `json:"expected_value"`
				DBPresent      bool            `json:"db_present"`
			}
		}
	}
	if err = json.Unmarshal(raw, &oracle); err != nil || oracle.Commit != "8a83efeacd3e26b82be495e17d50146e31c96a60" || len(oracle.Settings.Cases) != 6 {
		t.Fatal("fixed SystemConfigOper oracle missing", err)
	}
	for _, sample := range oracle.Settings.Cases {
		t.Run(sample.Name, func(t *testing.T) {
			a := newTestApp(t)
			_, worker := hostBusinessFixture(t, a)
			var result plugins.SettingsHostResultV1
			if err := hostBusinessCall(t, worker, plugins.SettingsReadCapability, plugins.SettingsHostRequestV1{Key: "CustomIdentifiers"}, &result); err != nil || string(result.Value) != "null" {
				t.Fatal("missing source setting is not None", err, string(result.Value))
			}
			if string(sample.Initial) != "null" {
				if err := hostBusinessCall(t, worker, plugins.SettingsWriteCapability, plugins.SettingsHostWriteV1{Key: "CustomIdentifiers", Value: sample.Initial}, &result); err != nil {
					t.Fatal("initial", err)
				}
			}
			if err := hostBusinessCall(t, worker, plugins.SettingsWriteCapability, plugins.SettingsHostWriteV1{Key: "CustomIdentifiers", Value: sample.Next}, &result); err != nil {
				t.Fatal("source set", err)
			}
			if (result.Changed == nil) != (sample.ExpectedReturn == nil) || result.Changed != nil && *result.Changed != *sample.ExpectedReturn || !originalBaseJSONEqual(result.Value, sample.ExpectedValue) {
				t.Fatalf("source return/value differs got=%+v want=%s", result, sample.ExpectedValue)
			}
			var count int
			if err := a.Store.DB.QueryRow(`SELECT COUNT(*) FROM recognition_words WHERE id='CustomIdentifiers'`).Scan(&count); err != nil || (count == 1) != sample.DBPresent {
				t.Fatal("source falsey persistent deletion differs", count, err)
			}
			if err := hostBusinessCall(t, worker, plugins.SettingsReadCapability, plugins.SettingsHostRequestV1{Key: "CustomIdentifiers"}, &result); err != nil || !originalBaseJSONEqual(result.Value, sample.ExpectedValue) {
				t.Fatal("hot cache value differs", err, string(result.Value))
			}
			dir := a.Dir
			if err := a.Close(); err != nil {
				t.Fatal(err)
			}
			cold, err := New(Options{DataDir: dir})
			if err != nil {
				t.Fatal(err)
			}
			defer cold.Close()
			if err = cold.initializeOriginalPlugins(context.Background()); err != nil {
				t.Fatal(err)
			}
			cold.wg.Wait()
			v, ok := cold.pluginRunningInstances.Load("OwnedHostBusiness")
			if !ok {
				t.Fatal("cold worker missing")
			}
			if err = hostBusinessCall(t, v.(*originalPluginInstance), plugins.SettingsReadCapability, plugins.SettingsHostRequestV1{Key: "CustomIdentifiers"}, &result); err != nil {
				t.Fatal(err)
			}
			want := sample.ExpectedValue
			if !sample.DBPresent {
				want = json.RawMessage(`null`)
			}
			if !originalBaseJSONEqual(result.Value, want) {
				t.Fatalf("cold source cache recovery differs got=%s want=%s", result.Value, want)
			}
		})
	}
}

func TestOriginalHostBusinessSettingsActualSDKOrdinarySharedRecognizeKeys(t *testing.T) {
	a := newTestApp(t)
	_, worker := hostBusinessFixture(t, a)
	var result plugins.SettingsHostResultV1
	if err := hostBusinessCall(t, worker, plugins.SettingsReadCapability, plugins.SettingsHostRequestV1{Key: "MEDIA_RECOGNIZE_SHARE"}, &result); err != nil || string(result.Value) != "true" {
		t.Fatal("source default sharing true", err, string(result.Value))
	}
	if err := hostBusinessCall(t, worker, plugins.SettingsWriteCapability, plugins.SettingsHostWriteV1{Key: "MEDIA_RECOGNIZE_SHARE", Value: json.RawMessage(`false`)}, &result); err != nil || a.originalHostMediaShareEnabled() {
		t.Fatal("native sharing switch not updated", err)
	}
	if err := hostBusinessCall(t, worker, plugins.SettingsWriteCapability, plugins.SettingsHostWriteV1{Key: "MEDIA_RECOGNIZE_SHARE_API", Value: json.RawMessage(`"http://127.0.0.1:9/recognize/share"`)}, &result); err != nil || a.Settings().MediaRecognizeShareAPI != "http://127.0.0.1:9/recognize/share" {
		t.Fatal("native recognizer endpoint not updated", err)
	}
	for _, invalid := range []json.RawMessage{json.RawMessage(`"/relative"`), json.RawMessage(`"https://user:secret@owned.invalid/recognize"`), json.RawMessage(`"https://owned.invalid/recognize?secret=owned"`)} {
		if err := hostBusinessCall(t, worker, plugins.SettingsWriteCapability, plugins.SettingsHostWriteV1{Key: "MEDIA_RECOGNIZE_SHARE_API", Value: invalid}, &result); err == nil {
			t.Fatal("invalid recognizer endpoint accepted")
		}
	}
	if err := a.Store.Put(context.Background(), "plugin_host_media_recognize_count", "main", int64(7)); err != nil {
		t.Fatal(err)
	}
	if err := hostBusinessCall(t, worker, plugins.SettingsReadCapability, plugins.SettingsHostRequestV1{Key: "MediaRecognizeShareCount"}, &result); err != nil || string(result.Value) != "7" {
		t.Fatal("ordinary recognizer counter", err, string(result.Value))
	}
	if err := hostBusinessCall(t, worker, plugins.SettingsWriteCapability, plugins.SettingsHostWriteV1{Key: "MediaRecognizeShareCount", Value: json.RawMessage(`1000`)}, &result); err == nil {
		t.Fatal("plugin fabricated recognizer success counter")
	}
}
