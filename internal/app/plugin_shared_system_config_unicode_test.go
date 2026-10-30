package app

import (
	"bytes"
	"context"
	"encoding/json"
	"light-media/internal/plugins"
	"testing"
)

func TestSharedSystemConfigSourceUnicodeStorageAndUIBoundary(t *testing.T) {
	dir := t.TempDir()
	markets := []string{}
	a, e := New(Options{DataDir: dir, PluginMarketsOverride: &markets})
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = a.Close() }()
	cookie := setupTestAdmin(t, a)
	p := installSharedSystemConfigFixture(t, a, cookie, sharedSystemConfigAll)
	for _, raw := range []string{`"\ud800"`, `{"\ud800":1}`, `["A => B","\ud800"]`, `"\ud801"`} {
		result, e := systemConfigSet(t, p, raw)
		if e != nil || string(result) != "true" {
			t.Fatal(string(result), e)
		}
		got, readErr := sharedSystemConfigCall(t, p, plugins.SharedSystemConfigGet, plugins.SharedSystemConfigGetV1{Key: plugins.SystemConfigCustomIdentifiers})
		if readErr != nil || !bytes.Contains(got, []byte(`\ud80`)) || bytes.Contains(got, []byte("�")) {
			t.Fatalf("internal source codepoints lost: %s %v", got, readErr)
		}
		outer := callTest(t, a.Handler(), "POST", "/api/v1/plugin/OwnedSystemConfig/shared", `{"method":"system.config.get","payload":{"key":"CustomIdentifiers"}}`, cookie, "")
		if outer.Code != 422 || !bytes.Contains(outer.Body.Bytes(), []byte("SYSTEM_CONFIG_JSON_UNICODE")) {
			t.Fatal("raw outer response accepted", outer.Code, outer.Body)
		}
		row, e := readSharedSystemConfigRow(context.Background(), a.Store.DB)
		if e != nil {
			t.Fatal(e)
		}
		stored, e := a.pluginStore.unseal("core_words", "system", "CustomIdentifiers", []byte(row.sealed))
		if e != nil {
			t.Fatal(e)
		}
		if !bytes.Contains(stored, []byte(`\ud80`)) || bytes.Contains(stored, []byte("�")) {
			t.Fatalf("source surrogate fact changed: %s", stored)
		}
		response := callTest(t, a.Handler(), "GET", "/api/settings/recognition-words", "", cookie, "")
		if response.Code != 400 || !bytes.Contains(response.Body.Bytes(), []byte("SYSTEM_CONFIG_JSON_UNICODE")) {
			t.Fatal(response.Code, response.Body)
		}
	}
	if e = a.Close(); e != nil {
		t.Fatal(e)
	}
	a, e = New(Options{DataDir: dir, PluginMarketsOverride: &markets})
	if e != nil {
		t.Fatal(e)
	}
	p = a.startOriginalPlugin(context.Background(), "OwnedSystemConfig")
	cold, readErr := sharedSystemConfigCall(t, p, plugins.SharedSystemConfigGet, plugins.SharedSystemConfigGetV1{Key: plugins.SystemConfigCustomIdentifiers})
	if readErr != nil || !bytes.Equal(cold, []byte(`"\ud801"`)) {
		t.Fatalf("cold internal source fact: %s %v", cold, readErr)
	}
	for _, test := range []struct{ input, want string }{{`1e400`, `null`}, {`{"nested":[1e400,-1e400]}`, `{"nested":[null,null]}`}} {
		if result, e := systemConfigSet(t, p, test.input); e != nil || string(result) != "true" {
			t.Fatal(string(result), e)
		}
		response := callTest(t, a.Handler(), "GET", "/api/settings/recognition-words", "", cookie, "")
		if response.Code != 200 {
			t.Fatal(response.Code, response.Body)
		}
		var envelope struct {
			Data struct {
				Value json.RawMessage `json:"value"`
			} `json:"data"`
		}
		if e = json.Unmarshal(response.Body.Bytes(), &envelope); e != nil {
			t.Fatal(e)
		}
		assertSharedOracleJSON(t, envelope.Data.Value, json.RawMessage(test.want))
		if _, e = sharedSystemConfigCall(t, p, plugins.SharedSystemConfigGet, plugins.SharedSystemConfigGetV1{Key: plugins.SystemConfigCustomIdentifiers}); e == nil {
			t.Fatal("UI projection wrote null back to the singleton")
		}
	}
	if _, e = systemConfigSet(t, p, `"\ud83d\ude00"`); e != nil {
		t.Fatal(e)
	}
	assertSharedOracleJSON(t, systemConfigGet(t, p), json.RawMessage(`"😀"`))
}
