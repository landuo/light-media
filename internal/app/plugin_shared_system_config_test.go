package app

import (
	"context"
	"encoding/json"
	"light-media/internal/plugins"
	"testing"
)

var sharedSystemConfigAll = []string{plugins.SharedSystemConfigGet, plugins.SharedSystemConfigSet}

func systemConfigSet(t *testing.T, p *originalPluginInstance, value string) (json.RawMessage, error) {
	t.Helper()
	return sharedSystemConfigCall(t, p, plugins.SharedSystemConfigSet, plugins.SharedSystemConfigSetV1{Key: plugins.SystemConfigCustomIdentifiers, Value: json.RawMessage(value)})
}
func systemConfigGet(t *testing.T, p *originalPluginInstance) json.RawMessage {
	t.Helper()
	raw, e := sharedSystemConfigCall(t, p, plugins.SharedSystemConfigGet, plugins.SharedSystemConfigGetV1{Key: plugins.SystemConfigCustomIdentifiers})
	if e != nil {
		t.Fatal(e)
	}
	return raw
}
func TestSharedSystemConfigActualWorkerSmoke(t *testing.T) {
	a, _, p := sharedSystemConfigFixture(t, sharedSystemConfigAll)
	if got := string(systemConfigGet(t, p)); got != "null" {
		t.Fatal(got)
	}
	r, e := systemConfigSet(t, p, `["Raw => Native"]`)
	if e != nil || string(r) != "true" {
		t.Fatal(string(r), e)
	}
	if got := a.prepareRecognitionTitle("Raw.S01E02"); got != "Native.S01E02" {
		t.Fatal(got)
	}
	assertSharedOracleJSON(t, systemConfigGet(t, p), json.RawMessage(`["Raw => Native"]`))
	r, e = systemConfigSet(t, p, `["Raw => Native"]`)
	if e != nil || string(r) != "null" {
		t.Fatal(string(r), e)
	}
	rev, _, e := a.recognitionWords(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	if e = a.saveRecognitionWords(context.Background(), rev, []string{"Raw => NativeEdit"}); e != nil {
		t.Fatal(e)
	}
	assertSharedOracleJSON(t, systemConfigGet(t, p), json.RawMessage(`["Raw => NativeEdit"]`))
	t.Logf("real compiled worker snapshot: %s", mustCleanSeedJSON(sharedSystemConfigWorkerSnapshot(t, p)))
}
