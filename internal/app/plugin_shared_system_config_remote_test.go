package app

import (
	"bytes"
	"context"
	"encoding/json"
	"light-media/internal/plugins"
	"os"
	"slices"
	"strings"
	"testing"
)

type sharedRemoteSnapshot struct {
	Get  json.RawMessage `json:"get"`
	Rows []struct {
		Value json.RawMessage `json:"value"`
	} `json:"rows"`
	Prepare []json.RawMessage `json:"prepare"`
}
type sharedRemoteSourceGroup struct {
	UIInput []string `json:"ui_input"`
	Cases   []struct {
		Initial  json.RawMessage      `json:"initial"`
		After    sharedRemoteSnapshot `json:"after"`
		Returned json.RawMessage      `json:"service_returned"`
	} `json:"cases"`
	Observations map[string]json.RawMessage `json:"observations"`
}

func sharedRemoteSource(t *testing.T) map[string]sharedRemoteSourceGroup {
	t.Helper()
	data, e := os.ReadFile("../../testdata/plugin-shared-host-20261030/system-config/source/remote-reachable.json")
	if e != nil {
		t.Fatal(e)
	}
	var source struct {
		Groups map[string]sharedRemoteSourceGroup `json:"groups"`
	}
	if e = json.Unmarshal(data, &source); e != nil {
		t.Fatal(e)
	}
	return source.Groups
}
func sharedRemoteObserved(t *testing.T, group sharedRemoteSourceGroup, key string) sharedRemoteSnapshot {
	t.Helper()
	var snapshot sharedRemoteSnapshot
	if e := json.Unmarshal(group.Observations[key], &snapshot); e != nil {
		t.Fatal(key, e)
	}
	return snapshot
}
func assertSharedRemoteSnapshot(t *testing.T, a *App, p *originalPluginInstance, want sharedRemoteSnapshot) {
	t.Helper()
	assertSharedRemoteSnapshotWithTitle(t, a, p, want, "A")
}
func assertSharedRemoteSnapshotWithTitle(t *testing.T, a *App, p *originalPluginInstance, want sharedRemoteSnapshot, inputTitle string) {
	t.Helper()
	assertSharedRemoteCodepoints(t, systemConfigGet(t, p), want.Get)
	title, e := a.prepareRecognitionTitleChecked(context.Background(), inputTitle)
	if e != nil {
		t.Fatal(e)
	}
	got, _ := json.Marshal(title)
	if len(want.Prepare) < 1 {
		t.Fatal("source title absent")
	}
	assertSharedOracleJSON(t, got, want.Prepare[0])
	_, rows, e := a.readSharedSystemConfigRows(context.Background(), a.Store.DB)
	if e != nil {
		t.Fatal(e)
	}
	if len(rows) != len(want.Rows) {
		t.Fatalf("source row count %d, target %d", len(want.Rows), len(rows))
	}
	for i, row := range rows {
		assertSharedRemoteCodepoints(t, row.Value, want.Rows[i].Value)
	}
	// Source row numbers themselves are not exposed by the selected API.
}
func sharedRemoteRun(t *testing.T, p *originalPluginInstance) {
	t.Helper()
	result, e := sharedSystemConfigCall(t, p, "owned.remote.empty_url_sync", map[string]any{})
	if e != nil || string(result) != "null" {
		t.Fatalf("actual typed producer: %s %v", result, e)
	}
}
func TestSharedSystemConfigRemoteSourceProducer(t *testing.T) {
	source := sharedRemoteSource(t)
	group := source["ordinary-ordinary"]
	dir := t.TempDir()
	markets := []string{}
	a, e := New(Options{DataDir: dir, PluginMarketsOverride: &markets})
	if e != nil {
		t.Fatal(e)
	}
	defer func() { a.Close() }()
	cookie := setupTestAdmin(t, a)
	p := installSharedSystemConfigFixture(t, a, cookie, sharedSystemConfigAll)
	for _, item := range group.Cases {
		if _, e := systemConfigSet(t, p, string(item.Initial)); e != nil {
			t.Fatal(e)
		}
		sharedRemoteRun(t, p)
		assertSharedRemoteSnapshot(t, a, p, item.After)
	}
	payload, _ := json.Marshal(map[string]string{"text": strings.Join(group.UIInput, "\n")})
	response := callTest(t, a.Handler(), "PUT", "/api/settings/recognition-words", string(payload), cookie, "")
	if response.Code != 200 {
		t.Fatal(response.Code, response.Body)
	}
	assertSharedRemoteSnapshot(t, a, p, sharedRemoteObserved(t, group, "after_ui"))
	sharedRemoteRun(t, p)
	assertSharedRemoteSnapshot(t, a, p, sharedRemoteObserved(t, group, "after_service"))
	if e = a.Close(); e != nil {
		t.Fatal(e)
	}
	a, e = New(Options{DataDir: dir, PluginMarketsOverride: &markets})
	if e != nil {
		t.Fatal(e)
	}
	p = a.startOriginalPlugin(context.Background(), "OwnedSystemConfig")
	if p == nil {
		t.Fatal("cold actual worker absent")
	}
	assertSharedRemoteSnapshot(t, a, p, sharedRemoteObserved(t, source["ordinary-cold"], "cold"))
}

// Compare the complete source string list without encoding/json's lossy string
// decoding. Source expected values are the independent original process output.
func assertSharedRemoteCodepoints(t *testing.T, got, want json.RawMessage) {
	t.Helper()
	if bytes.Equal(bytes.TrimSpace(want), []byte("null")) {
		if !bytes.Equal(bytes.TrimSpace(got), []byte("null")) {
			t.Fatalf("want null, got %s", got)
		}
		return
	}
	var g, w []json.RawMessage
	if e := json.Unmarshal(got, &g); e != nil {
		t.Fatal(e)
	}
	if e := json.Unmarshal(want, &w); e != nil {
		t.Fatal(e)
	}
	if len(g) != len(w) {
		t.Fatalf("source strings %d, target %d", len(w), len(g))
	}
	for i := range w {
		gp, e := plugins.DecodeSystemConfigJSONString(g[i])
		if e != nil {
			t.Fatal(e)
		}
		wp, e := plugins.DecodeSystemConfigJSONString(w[i])
		if e != nil {
			t.Fatal(e)
		}
		if !slices.Equal(gp, wp) {
			t.Fatalf("source code points element %d differ: got %U want %U", i, gp, wp)
		}
	}
}
func TestSharedSystemConfigRemoteUnicodeProducer(t *testing.T) {
	source := sharedRemoteSource(t)
	group := source["extra-unicode-remote-unicode-remote"]
	dir := t.TempDir()
	markets := []string{}
	a, e := New(Options{DataDir: dir, PluginMarketsOverride: &markets})
	if e != nil {
		t.Fatal(e)
	}
	defer func() { a.Close() }()
	cookie := setupTestAdmin(t, a)
	p := installSharedSystemConfigFixture(t, a, cookie, sharedSystemConfigAll)
	response := callTest(t, a.Handler(), "PUT", "/api/settings/recognition-words", `{"text":"A => \ud800\n\ud800 => B"}`, cookie, "")
	if response.Code != 200 {
		t.Fatal(response.Code, response.Body)
	}
	assertSharedRemoteSnapshot(t, a, p, sharedRemoteObserved(t, group, "after_ui"))
	sharedRemoteRun(t, p)
	assertSharedRemoteSnapshot(t, a, p, sharedRemoteObserved(t, group, "after_remote"))
	response = callTest(t, a.Handler(), "POST", "/api/v1/plugin/OwnedSystemConfig/shared", `{"method":"system.config.get","payload":{"key":"CustomIdentifiers"}}`, cookie, "")
	if response.Code != 422 || !strings.Contains(response.Body.String(), "SYSTEM_CONFIG_JSON_UNICODE") {
		t.Fatal("outer raw HTTP boundary", response.Code, response.Body)
	}
	response = callTest(t, a.Handler(), "GET", "/api/settings/recognition-words", "", cookie, "")
	if response.Code != 400 {
		t.Fatal("native UI renderer", response.Code, response.Body)
	}
	if e = a.Close(); e != nil {
		t.Fatal(e)
	}
	a, e = New(Options{DataDir: dir, PluginMarketsOverride: &markets})
	if e != nil {
		t.Fatal(e)
	}
	p = a.startOriginalPlugin(context.Background(), "OwnedSystemConfig")
	if p == nil {
		t.Fatal("cold actual worker absent")
	}
	assertSharedRemoteSnapshot(t, a, p, sharedRemoteObserved(t, source["extra-unicode-remote-cold"], "cold"))
}

func TestSharedSystemConfigRemoteCombinedStrip(t *testing.T) {
	source := sharedRemoteSource(t)
	group := source["extra-strip-combined-strip-combined"]
	dir := t.TempDir()
	markets := []string{}
	a, e := New(Options{DataDir: dir, PluginMarketsOverride: &markets})
	if e != nil {
		t.Fatal(e)
	}
	defer func() { a.Close() }()
	cookie := setupTestAdmin(t, a)
	p := installSharedSystemConfigFixture(t, a, cookie, sharedSystemConfigAll)
	response := callTest(t, a.Handler(), "PUT", "/api/settings/recognition-words", `{"text":"\u001cA => B && S <> E >> EP+1"}`, cookie, "")
	if response.Code != 200 {
		t.Fatal(response.Code, response.Body)
	}
	assertSharedRemoteSnapshotWithTitle(t, a, p, sharedRemoteObserved(t, group, "after_ui"), "A S1 E")
	sharedRemoteRun(t, p)
	assertSharedRemoteSnapshotWithTitle(t, a, p, sharedRemoteObserved(t, group, "after_remote"), "A S1 E")
	if e = a.Close(); e != nil {
		t.Fatal(e)
	}
	a, e = New(Options{DataDir: dir, PluginMarketsOverride: &markets})
	if e != nil {
		t.Fatal(e)
	}
	p = a.startOriginalPlugin(context.Background(), "OwnedSystemConfig")
	if p == nil {
		t.Fatal("cold actual worker absent")
	}
	assertSharedRemoteSnapshotWithTitle(t, a, p, sharedRemoteObserved(t, source["extra-strip-combined-cold"], "cold"), "A S1 E")
}

func TestSharedSystemConfigRemoteIgnoredWhitespace(t *testing.T) {
	source := sharedRemoteSource(t)
	group := source["extra-ignored-whitespace-ignored-whitespace"]
	dir := t.TempDir()
	markets := []string{}
	a, e := New(Options{DataDir: dir, PluginMarketsOverride: &markets})
	if e != nil {
		t.Fatal(e)
	}
	defer func() { a.Close() }()
	cookie := setupTestAdmin(t, a)
	p := installSharedSystemConfigFixture(t, a, cookie, sharedSystemConfigAll)
	payload, _ := json.Marshal(map[string]string{"text": strings.Repeat(" ", 65537) + "\nA => B"})
	response := callTest(t, a.Handler(), "PUT", "/api/settings/recognition-words", string(payload), cookie, "")
	if response.Code != 200 {
		t.Fatal(response.Code, response.Body)
	}
	assertSharedRemoteSnapshotWithTitle(t, a, p, sharedRemoteObserved(t, group, "after_ui"), "A")
	sharedRemoteRun(t, p)
	assertSharedRemoteSnapshotWithTitle(t, a, p, sharedRemoteObserved(t, group, "after_remote"), "A")
	if e = a.Close(); e != nil {
		t.Fatal(e)
	}
	a, e = New(Options{DataDir: dir, PluginMarketsOverride: &markets})
	if e != nil {
		t.Fatal(e)
	}
	p = a.startOriginalPlugin(context.Background(), "OwnedSystemConfig")
	if p == nil {
		t.Fatal("cold actual worker absent")
	}
	assertSharedRemoteSnapshotWithTitle(t, a, p, sharedRemoteObserved(t, source["extra-ignored-whitespace-cold"], "cold"), "A")
}
