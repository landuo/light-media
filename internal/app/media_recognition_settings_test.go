package app

import (
	"context"
	"encoding/json"
	"testing"

	"light-media/internal/plugins"
)

func TestMediaRecognitionSourceSettingsPersistenceAndHostRead(t *testing.T) {
	a, object, _, _, cookie := manualProviderHTTPFixture(t)
	if a.Settings().RecognitionSource != nil || a.publicSettings()["effective_recognition_source"] != a.originalHostMediaRecognition.Source {
		t.Fatal("legacy setting no longer inherits startup source")
	}
	settings := a.Settings()
	source := "douban"
	settings.RecognitionSource = &source
	raw, _ := json.Marshal(settings)
	r := callTest(t, a.Handler(), "PUT", "/api/settings", string(raw), cookie, "")
	if r.Code != 200 || a.effectiveRecognitionSource() != "douban" {
		t.Fatal("source persistence", r.Code, r.Body.String())
	}
	host, err := a.originalHostSettingsResource(t.Context(), originalBaseCallScope{object: object, phase: "api", validate: func(ctx context.Context) error { return ctx.Err() }}, plugins.SettingsReadCapability, json.RawMessage(`{"key":"RECOGNIZE_SOURCE"}`))
	var result struct {
		Value string `json:"value"`
	}
	if err != nil || json.Unmarshal(host, &result) != nil || result.Value != "douban" {
		t.Fatal("SDK host source snapshot", string(host), err)
	}
	// An old client or another settings section omits this new nullable field.
	unrelated := a.Settings()
	unrelated.RecognitionSource = nil
	raw, _ = json.Marshal(unrelated)
	r = callTest(t, a.Handler(), "PUT", "/api/settings", string(raw), cookie, "")
	if r.Code != 200 || a.effectiveRecognitionSource() != "douban" {
		t.Fatal("unrelated save reset source", r.Code)
	}
	invalid := a.Settings()
	badSource := "not-a-source"
	invalid.RecognitionSource = &badSource
	raw, _ = json.Marshal(invalid)
	r = callTest(t, a.Handler(), "PUT", "/api/settings", string(raw), cookie, "")
	if r.Code == 200 || a.effectiveRecognitionSource() != "douban" {
		t.Fatal("invalid source saved", r.Code)
	}
	dir := a.Dir
	if err = a.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := New(Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	if reopened.effectiveRecognitionSource() != "douban" || reopened.Settings().RecognitionSource == nil {
		t.Fatal("source lost after reopen")
	}
}
