package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	iyuu "light-media/internal/plugins/iyuuautoseed"
	"light-media/internal/sites"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func iyuuAssertActualBusinessSourceLogs(t *testing.T, a *App, run PluginRun) {
	t.Helper()
	cookie := setupTestAdmin(t, a)
	response := callTest(t, a.Handler(), "GET", "/api/plugins/"+run.InstanceID+"/logs?limit=500", "", cookie, "")
	var body struct {
		Data struct {
			History logPage `json:"history"`
		} `json:"data"`
	}
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &body) != nil {
		t.Fatal("actual source log API", response.Code, response.Body.String())
	}
	expected := map[int]bool{746: false, 759: false, 763: false, 813: false, 905: false, 924: false, 1169: false, 1198: false, 1205: false, 842: false}
	for _, entry := range body.Data.History.Entries {
		event, _ := entry["event"].(string)
		if !strings.HasPrefix(event, "iyuu.source.") {
			continue
		}
		if entry["plugin_instance_id"] != run.InstanceID || entry["plugin_run_id"] != run.ID || entry["plugin_id"] != "IYUUAutoSeed" || entry["plugin_version"] != "2.19" || entry["module"] != "plugins" {
			t.Fatal("source diagnostic lost immutable ownership", entry)
		}
		n, e := strconv.Atoi(strings.TrimPrefix(event, "iyuu.source."))
		if e != nil {
			t.Fatal(e)
		}
		if _, ok := expected[n]; ok {
			expected[n] = true
			if entry["level"] != "INFO" {
				t.Fatal("source diagnostic changed level", entry)
			}
		}
	}
	for n, seen := range expected {
		if !seen {
			t.Fatal("actual App SDK HTTP branch log missing", n)
		}
	}
	for _, secret := range []string{"owned-synthetic-token", "owned=synthetic", "http://127.0.0.1", "passkey="} {
		if strings.Contains(response.Body.String(), secret) {
			t.Fatal("actual source log API leaked credential or URL", secret)
		}
	}
}

// This tests the real shared logger/public scope boundary using only the
// source's empty-inventory branch. Network/native effects are forbidden here;
// full native SDK/HTTP business logs are exercised by the separate fixture.
type iyuuLogOnlyHost struct {
	iyuu.Host
	name string
}

func (h iyuuLogOnlyHost) Inventory(context.Context) ([]iyuu.Service, error) {
	return []iyuu.Service{{Ref: "owned-log", Name: h.name, Kind: "qbittorrent"}}, nil
}
func (h iyuuLogOnlyHost) Completed(context.Context, iyuu.Service) ([]iyuu.NativeTorrent, error) {
	return []iyuu.NativeTorrent{}, nil
}
func (h iyuuLogOnlyHost) SaveConfig(context.Context, map[string]any) error { return nil }

type iyuuNoLogAPI struct{ iyuu.SeedAPI }

func TestIYUUSourceLogsRedactBeforeWriteAndStayInExactRunScope(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	cookie := setupTestAdmin(t, a)
	token := "owned-long-IYUU-credential-7b5f6e"
	noteSecret := "owned-note-passkey-19a38b"
	a.cfgMu.Lock()
	a.config.Sites = []SiteConfig{{Config: sites.Config{ID: "owned-log-site", Name: "owned", BaseURL: "https://owned.invalid/"}, SiteOptions: SiteOptions{Note: siteJSON(`{"passkey":"` + noteSecret + `"}`)}}}
	a.cfgMu.Unlock()
	if e := a.bootstrapNativeSiteIdentities(ctx); e != nil {
		t.Fatal(e)
	}
	name := token + " " + url.QueryEscape(token) + " " + base64.StdEncoding.EncodeToString([]byte(token)) + " " + noteSecret + ` 'passkey':'owned-unconfigured-value' password=owned-unconfigured-password https://user:private@secret.example/private-key`
	input := map[string]any{"enabled": true, "token": token, "downloaders": []any{name}, "sites": []any{}, "size": json.Number("0")}
	init := uninstallInstall(t, a, "OwnedIYUULogRedaction", iyuu.Manifest(), input, true)
	invokeIYUUActualSDK(t, a, init)
	if _, e := a.pluginStore.EnqueueRun(ctx, init.InstanceID, "run", nil); e != nil {
		t.Fatal(e)
	}
	run, e := a.pluginStore.claimRunForInstance(ctx, init.InstanceID)
	if e != nil {
		t.Fatal(e)
	}
	in, e := a.pluginStore.Installation(ctx, init.InstanceID)
	if e != nil {
		t.Fatal(e)
	}
	raw, e := json.Marshal(input)
	if e != nil {
		t.Fatal(e)
	}
	run.RequestID = token
	logCtx := a.iyuuSourceLogContext(ctx, in, run, raw, a.Settings())
	// A later settings edit cannot erase a running source call's redaction snapshot.
	a.cfgMu.Lock()
	a.config.Sites[0].Note = ""
	a.cfgMu.Unlock()
	config := iyuu.NewConfig()
	if _, e = config.InitContext(logCtx, input, nil); e != nil {
		t.Fatal(e)
	}
	core, e := iyuu.NewCore(config, iyuuLogOnlyHost{name: name}, iyuuNoLogAPI{})
	if e != nil {
		t.Fatal(e)
	}
	if e = core.AutoSeed(logCtx); e != nil {
		t.Fatal(e)
	}

	// Real source WARNING/DEBUG branches obey the existing temporary module
	// debug policy; no test-only event is injected into the sink.
	emptyInput := map[string]any{"enabled": true, "token": token, "downloaders": []any{}, "sites": []any{}, "size": json.Number("0")}
	emptyConfig := iyuu.NewConfig()
	if _, e = emptyConfig.InitContext(logCtx, emptyInput, nil); e != nil {
		t.Fatal(e)
	}
	snapshot, e := emptyConfig.Snapshot()
	if e != nil {
		t.Fatal(e)
	}
	checkEmpty := func() {
		checker, err := iyuu.RestoreCore(iyuu.State{Config: snapshot, Queue: map[string][]string{"unselected": {}}}, iyuuLogOnlyHost{name: name}, iyuuNoLogAPI{})
		if err != nil {
			t.Fatal(err)
		}
		if err = checker.CheckRecheck(logCtx); err != nil {
			t.Fatal(err)
		}
	}
	checkEmpty() // Source DEBUG206 is filtered at the default INFO threshold.
	debug := callTest(t, a.Handler(), "PATCH", "/api/settings/logging", `{"debug":{"scope":"module","id":"plugins","enabled":true}}`, cookie, "")
	if debug.Code != 200 {
		t.Fatal(debug.Code, debug.Body.String())
	}
	checkEmpty()
	if _, e = (iyuu.Resolver{}).Resolve(logCtx, map[string]any{"torrent_id": json.Number("7")}, iyuu.Site{Ref: "unused", Fields: map[string]any{"url": "https://kp.m-team.cc/"}}, "unused"); e != nil {
		t.Fatal(e)
	}
	response := callTest(t, a.Handler(), "GET", "/api/plugins/"+run.InstanceID+"/logs?limit=500", "", cookie, "")
	if response.Code != 200 {
		t.Fatal(response.Code, response.Body.String())
	}
	for _, secret := range []string{token, url.QueryEscape(token), base64.StdEncoding.EncodeToString([]byte(token)), noteSecret, "owned-unconfigured-value", "owned-unconfigured-password", "secret.example", "private-key", "user:private"} {
		if strings.Contains(response.Body.String(), secret) {
			t.Fatal("source logging leaked secret into real shared sink", secret)
		}
	}
	if !strings.Contains(response.Body.String(), "[REDACTED]") || !strings.Contains(response.Body.String(), "iyuu.source.759") || !strings.Contains(response.Body.String(), run.ID) {
		t.Fatal("source log absent rather than redacted/scoped", response.Body.String())
	}

	var body struct {
		Data struct {
			History logPage `json:"history"`
		} `json:"data"`
	}
	if e := json.Unmarshal(response.Body.Bytes(), &body); e != nil {
		t.Fatal(e)
	}
	levels := map[string]int{}
	for _, entry := range body.Data.History.Entries {
		switch entry["event"] {
		case "iyuu.source.206":
			if entry["level"] != "DEBUG" {
				t.Fatal("source debug severity", entry)
			}
			levels["DEBUG"]++
		case "iyuu.source.179":
			if entry["level"] != "WARN" {
				t.Fatal("source warning severity", entry)
			}
			levels["WARN"]++
		case "iyuu.source.1306":
			if entry["level"] != "ERROR" {
				t.Fatal("source error severity", entry)
			}
			levels["ERROR"]++
		}
	}
	if levels["DEBUG"] != 1 || levels["WARN"] != 2 || levels["ERROR"] != 1 {
		t.Fatal("actual source severity/module filter", levels)
	}

	flushTestLogs(t, a)
	files, e := filepath.Glob(filepath.Join(a.Dir, "logs", "*.jsonl"))
	if e != nil || len(files) == 0 {
		t.Fatal("actual durable logs absent", files, e)
	}
	for _, file := range files {
		content, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for _, secret := range []string{token, url.QueryEscape(token), base64.StdEncoding.EncodeToString([]byte(token)), noteSecret, "owned-unconfigured-value", "owned-unconfigured-password", "secret.example", "private-key", "user:private"} {
			if strings.Contains(string(content), secret) {
				t.Fatal("source credential reached durable log before API redaction", secret)
			}
		}
	}
	other := callTest(t, a.Handler(), "GET", "/api/plugins/"+run.InstanceID+"/logs?plugin_instance_id=other", "", cookie, "")
	if other.Code != 400 {
		t.Fatal("source log scope override", other.Code)
	}
}
