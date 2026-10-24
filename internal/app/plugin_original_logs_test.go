package app

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"light-media/internal/plugins"
)

type ownedInstanceLogProbe struct {
	PID           int  `json:"pid"`
	StaleDenied   bool `json:"stale_denied"`
	CancelDenied  bool `json:"cancel_denied"`
	UnknownDenied bool `json:"unknown_denied"`
	WriteDenied   bool `json:"write_denied"`
	StartupDenied bool `json:"startup_denied"`
}

func ownedLogCall(object *originalPluginInstance, method string, raw json.RawMessage) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := object.callSession(ctx, plugins.Runner{Timeout: 5 * time.Second}, method, raw, nil)
	return err
}

func ownedLogWrite(t *testing.T, object *originalPluginInstance, level plugins.LogLevel, message string) {
	t.Helper()
	raw, err := json.Marshal(plugins.LogEntryV1{Level: level, Message: message})
	if err != nil {
		t.Fatal(err)
	}
	if err := ownedLogCall(object, "write", raw); err != nil {
		t.Fatal("actual SDK log failed", err)
	}
}

func ownedLogRows(t *testing.T, a *App, instance string) []map[string]any {
	t.Helper()
	page, err := a.queryLogs(context.Background(), map[string]string{"plugin_instance_id": instance, "from": "0001-01-01T00:00:00Z", "limit": "500"})
	if err != nil {
		t.Fatal(err)
	}
	rows := []map[string]any{}
	for _, row := range page.Entries {
		if row["event"] != "plugin_message" {
			continue
		}
		if row["plugin_instance_id"] != instance || row["plugin_id"] == nil || row["plugin_id"] == "" || row["plugin_version"] != "1.0" || row["plugin_call_id"] == nil || row["plugin_call_id"] == "" || row["operation"] == nil || row["operation"] == "" || row["plugin_run_id"] != nil {
			t.Fatal("host attribution missing or business run fabricated", row)
		}
		rows = append(rows, row)
	}
	return rows
}

func ownedLogMessage(t *testing.T, rows []map[string]any, message string) map[string]any {
	t.Helper()
	for _, row := range rows {
		if row["message"] == message {
			return row
		}
	}
	t.Fatalf("expected owned log absent: %s", message)
	return nil
}

func ownedLogNoRuns(t *testing.T, a *App) {
	t.Helper()
	var count int
	if err := a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_runs`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("logging created legacy business runs: %d %v", count, err)
	}
}

func TestOriginalInstanceLogActualSDKLifecycleLevelsAndRedaction(t *testing.T) {
	a, _, object := ownedInstanceLogSDKFixture(t, true)
	instance := object.installation.InstanceID
	startup := ownedLogMessage(t, ownedLogRows(t, a, instance), "owned startup")
	if startup["operation"] != "init" {
		t.Fatal("startup did not use actual initializing phase")
	}
	var first, second ownedInstanceLogProbe
	if err := object.readSession(context.Background(), "probe", &first); err != nil {
		t.Fatal(err)
	}
	if err := object.readSession(context.Background(), "probe", &second); err != nil {
		t.Fatal(err)
	}
	for _, probe := range []ownedInstanceLogProbe{first, second} {
		if probe.PID <= 0 || probe.PID == os.Getpid() || !probe.StaleDenied || !probe.CancelDenied || !probe.UnknownDenied || probe.WriteDenied || probe.StartupDenied {
			t.Fatalf("actual SDK scope checks failed: %+v", probe)
		}
	}
	if first.PID != second.PID {
		t.Fatal("persistent worker was replaced")
	}
	ownedLogWrite(t, object, plugins.LogDebug, "owned debug filtered")
	for _, level := range []plugins.LogLevel{plugins.LogInfo, plugins.LogWarning, plugins.LogWarn, plugins.LogError, plugins.LogCritical} {
		ownedLogWrite(t, object, level, "owned level "+string(level))
	}
	ownedLogWrite(t, object, plugins.LogInfo, "owned redaction password=OWNED_PASSWORD token=OWNED_TOKEN https://owned.invalid/private")
	if err := ownedLogCall(object, "fail", json.RawMessage(`{}`)); err == nil {
		t.Fatal("owned callback error reported success")
	}
	ownedLogWrite(t, object, plugins.LogInfo, "owned after error")
	rows := ownedLogRows(t, a, instance)
	for _, row := range rows {
		message, _ := row["message"].(string)
		if strings.Contains(message, "must not appear") || strings.Contains(message, "OWNED_PASSWORD") || strings.Contains(message, "OWNED_TOKEN") || strings.Contains(message, "owned.invalid") || message == "owned debug filtered" {
			t.Fatal("rejected/filtered/unsanitized text reached storage")
		}
	}
	for _, level := range []plugins.LogLevel{plugins.LogInfo, plugins.LogWarning, plugins.LogWarn, plugins.LogError, plugins.LogCritical} {
		row := ownedLogMessage(t, rows, "owned level "+string(level))
		want := string(level)
		if level == plugins.LogWarning {
			want = "WARN"
		}
		if row["level"] != want || row["operation"] != "write" {
			t.Fatal("source level or host phase changed", row)
		}
	}
	ownedLogMessage(t, rows, "owned redaction [REDACTED] [REDACTED] [URL]")
	if ownedLogMessage(t, rows, "owned callback error")["operation"] != "fail" {
		t.Fatal("explicit error log missing callback attribution")
	}
	ownedLogMessage(t, rows, "owned after error")
	critical, err := a.queryLogs(context.Background(), map[string]string{"plugin_instance_id": instance, "level": "CRITICAL", "limit": "50"})
	if err != nil || len(critical.Entries) != 1 || critical.Entries[0]["message"] != "owned level CRITICAL" {
		t.Fatal("critical query lost source severity", err)
	}
	if err := a.stopOriginalPlugin(context.Background(), instance); err != nil {
		t.Fatal(err)
	}
	rows = ownedLogRows(t, a, instance)
	seen := map[any]bool{}
	for _, message := range []string{"owned close", "owned stop_service", "owned stop"} {
		row := ownedLogMessage(t, rows, message)
		if row["operation"] != "stop" || seen[row["plugin_call_id"]] {
			t.Fatal("cleanup borrowed an old call or incorrect phase", row)
		}
		seen[row["plugin_call_id"]] = true
	}
	ownedLogNoRuns(t, a)
}

func TestOriginalInstanceLogActualSDKCloneHistoryStreamAndExports(t *testing.T) {
	a, cookie, object := ownedInstanceLogSDKFixture(t, true)
	instance := object.installation.InstanceID
	response := callTest(t, a.Handler(), "POST", "/api/plugins/"+instance+"/clone", `{"suffix":"Twin"}`, cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	a.wg.Wait()
	cloneID := instance + "twin"
	loaded, ok := a.pluginRunningInstances.Load(cloneID)
	if !ok {
		t.Fatal("actual clone worker missing")
	}
	clone := loaded.(*originalPluginInstance)
	ownedLogWrite(t, object, plugins.LogInfo, "owned source only")
	ownedLogWrite(t, clone, plugins.LogInfo, "owned sibling only")
	history := callTest(t, a.Handler(), "GET", "/api/plugins/"+instance+"/logs?plugin_id="+cloneID, "", cookie, "")
	var page struct {
		Data struct {
			History logPage `json:"history"`
			Resume  string  `json:"resume_id"`
		} `json:"data"`
	}
	if history.Code != 200 || json.Unmarshal(history.Body.Bytes(), &page) != nil || !strings.Contains(history.Body.String(), "owned source only") || strings.Contains(history.Body.String(), "owned sibling only") || page.Data.Resume == "" {
		t.Fatal("actual SDK history mixed siblings")
	}
	for _, entry := range page.Data.History.Entries {
		if entry["plugin_instance_id"] != instance {
			t.Fatal("non-exact history filter")
		}
	}
	if ownedLogMessage(t, ownedLogRows(t, a, cloneID), "owned sibling only")["plugin_id"] != instance {
		t.Fatal("clone implementation attribution changed")
	}
	ownedLogWrite(t, clone, plugins.LogInfo, "owned sibling after boundary")
	ownedLogWrite(t, object, plugins.LogInfo, "owned source after boundary")
	server := httptest.NewServer(a.Handler())
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	request, _ := http.NewRequestWithContext(ctx, "GET", server.URL+"/api/plugins/"+instance+"/logs/stream?last_event_id="+url.QueryEscape(page.Data.Resume), nil)
	request.AddCookie(cookie)
	stream, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Body.Close()
	if stream.StatusCode != 200 {
		t.Fatal("actual scoped stream failed")
	}
	reader := bufio.NewReader(stream.Body)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasPrefix(line, "data:") {
			if !strings.Contains(line, "owned source after boundary") || strings.Contains(line, "sibling") {
				t.Fatal("actual SDK stream mixed siblings", line)
			}
			break
		}
	}
	plain := callTest(t, a.Handler(), "GET", "/api/plugins/"+instance+"/logs/file", "", cookie, "")
	if plain.Code != 200 || !strings.Contains(plain.Body.String(), "owned source only") || strings.Contains(plain.Body.String(), "owned sibling") {
		t.Fatal("actual worker text export mixed siblings")
	}
	archive := callTest(t, a.Handler(), "GET", "/api/plugins/"+instance+"/logs/download", "", cookie, "")
	z, err := zip.NewReader(bytes.NewReader(archive.Body.Bytes()), int64(archive.Body.Len()))
	if archive.Code != 200 || err != nil || len(z.File) != 1 {
		t.Fatal("actual worker ZIP unavailable", err)
	}
	f, err := z.File[0].Open()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(f)
	f.Close()
	if err != nil || !bytes.Contains(raw, []byte("owned source only")) || bytes.Contains(raw, []byte("owned sibling")) {
		t.Fatal("actual worker ZIP mixed siblings", err)
	}
	ownedLogNoRuns(t, a)
}

func TestOriginalInstanceLogActualSDKMalformedAndMissingCapability(t *testing.T) {
	a, _, object := ownedInstanceLogSDKFixture(t, true)
	before := len(ownedLogRows(t, a, object.installation.InstanceID))
	for _, raw := range []string{`null`, `[]`, `"text"`, `{}`, `{"level":"INFO"}`, `{"level":"INFO","message":null}`, `{"level":"TRACE","message":"owned invalid"}`, `{"level":"INFO","message":"owned invalid","plugin_instance_id":"other"}`, `{"level":"INFO","message":"owned invalid","plugin_call_id":"forged"}`, `{"level":"INFO","message":"owned invalid","time":"2000"}`, `{"level":"INFO","message":"owned invalid","path":"other.log"}`, `{"level":"INFO","message":"owned invalid"}{}`, strings.Repeat(" ", plugins.LogPayloadLimit+1) + `{}`} {
		if err := ownedLogCall(object, "resource", json.RawMessage(raw)); err == nil {
			t.Fatal("actual SDK host accepted malformed or self-attributed log")
		}
	}
	oversized, _ := json.Marshal(plugins.LogEntryV1{Level: plugins.LogInfo, Message: strings.Repeat("x", plugins.LogMessageLimit+1)})
	if ownedLogCall(object, "resource", oversized) == nil {
		t.Fatal("oversized host message accepted")
	}
	if len(ownedLogRows(t, a, object.installation.InstanceID)) != before {
		t.Fatal("rejected messages reached existing logger")
	}
	ownedLogWrite(t, object, plugins.LogInfo, strings.Repeat("界", plugins.LogMessageLimit/3))
	ownedLogWrite(t, object, plugins.LogInfo, "")
	ownedLogNoRuns(t, a)
	deniedApp, _, denied := ownedInstanceLogSDKFixture(t, false)
	var probe ownedInstanceLogProbe
	if err := denied.readSession(context.Background(), "probe", &probe); err != nil {
		t.Fatal(err)
	}
	if !probe.StartupDenied || !probe.WriteDenied || !probe.StaleDenied || !probe.CancelDenied || !probe.UnknownDenied || len(ownedLogRows(t, deniedApp, denied.installation.InstanceID)) != 0 {
		t.Fatal("undeclared actual worker gained log capability", probe)
	}
	ownedLogNoRuns(t, deniedApp)
	for _, contract := range []string{pluginPureSDKContract, pluginBaseSDKContract} {
		if validatePluginArtifactContract(ownedInstanceLogManifest(true), contract) == nil {
			t.Fatal("wrong contract admitted logging", contract)
		}
	}
}

func TestOriginalInstanceLogActualHostScopeRevocationAndIdentity(t *testing.T) {
	a, _, object := ownedInstanceLogSDKFixture(t, true)
	identity := plugins.Identity{PluginID: object.installation.PluginID, InstanceID: object.installation.InstanceID, Version: object.installation.Version}
	raw := json.RawMessage(`{"level":"INFO","message":"owned scope"}`)
	callback, revoke := object.originalBaseSessionHost(context.Background(), "scope", nil)
	defer revoke()
	for _, field := range []string{"plugin", "instance", "version"} {
		wrong := identity
		switch field {
		case "plugin":
			wrong.PluginID += "Other"
		case "instance":
			wrong.InstanceID += "Other"
		case "version":
			wrong.Version = "999"
		}
		if value, err := callback(context.Background(), wrong, plugins.LogWriteCapability, raw); err == nil || len(value) != 0 {
			t.Fatal("wrong host-frame identity accepted", field)
		}
	}
	if value, err := callback(context.Background(), identity, "plugin.log.unknown", raw); err == nil || len(value) != 0 {
		t.Fatal("unknown host capability accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	canceled, closeCanceled := object.originalBaseSessionHost(ctx, "scope", nil)
	cancel()
	if value, err := canceled(context.Background(), identity, plugins.LogWriteCapability, raw); !errors.Is(err, context.Canceled) || len(value) != 0 {
		t.Fatal("canceled call accepted", err)
	}
	closeCanceled()
	before := len(ownedLogRows(t, a, identity.InstanceID))
	for _, closeInstead := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		waiting, closePhase := object.originalBaseSessionHost(ctx, "scope", nil)
		result := make(chan error, 1)
		started := make(chan struct{})
		a.pluginMu.Lock()
		go func() {
			close(started)
			_, err := waiting(ctx, identity, plugins.LogWriteCapability, raw)
			result <- err
		}()
		<-started
		if closeInstead {
			closePhase()
		} else {
			cancel()
		}
		a.pluginMu.Unlock()
		select {
		case err := <-result:
			if err == nil {
				t.Error("revoked/canceled scope crossed logger publication lock")
			}
		case <-time.After(5 * time.Second):
			t.Error("blocked callback did not exit")
		}
		cancel()
		closePhase()
	}
	revoke()
	if value, err := callback(context.Background(), identity, plugins.LogWriteCapability, raw); err == nil || len(value) != 0 {
		t.Fatal("expired host callback accepted")
	}
	if len(ownedLogRows(t, a, identity.InstanceID)) != before {
		t.Fatal("invalid host scope emitted logs")
	}
	fakeStop, closeStop := object.originalBaseSessionHost(context.Background(), "stop", nil)
	defer closeStop()
	if _, err := fakeStop(context.Background(), identity, plugins.LogWriteCapability, raw); err == nil {
		t.Fatal("phase name alone granted cleanup authority")
	}
	// Installation replacement revokes an otherwise open ordinary callback;
	// only the real detached cleanup phases may still log for the old object.
	fresh, closeFresh := object.originalBaseSessionHost(context.Background(), "scope", nil)
	defer closeFresh()
	if _, err := a.Store.DB.Exec(`UPDATE plugin_installations SET generation=generation+1 WHERE instance_id=?`, identity.InstanceID); err != nil {
		t.Fatal(err)
	}
	if _, err := fresh(context.Background(), identity, plugins.LogWriteCapability, raw); err == nil {
		t.Fatal("replaced installation retained logging authority")
	}
	if err := a.stopOriginalPlugin(context.Background(), identity.InstanceID); err != nil {
		t.Fatal(err)
	}
	stale, closeStale := object.originalBaseSessionHost(context.Background(), "scope", nil)
	defer closeStale()
	if _, err := stale(context.Background(), identity, plugins.LogWriteCapability, raw); err == nil {
		t.Fatal("unloaded object retained logging authority")
	}
	ownedLogNoRuns(t, a)
}
