package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"light-media/internal/download"
	"light-media/internal/plugins"
	transfer "light-media/internal/plugins/torrenttransfer"
)

// This is deliberately not opt-in: useTransferServerProcess builds the ordinary
// cmd/server worker, not a reduced test entry point. The App API is served over
// owned loopback HTTP; only downloaders are synthetic protocol daemons.
func TestTransferActualServerInstallConfigServiceDualIdentity(t *testing.T) {
	for _, kind := range []string{"qbittorrent", "transmission"} {
		t.Run(kind, func(t *testing.T) {
			a := newTestApp(t)
			useTransferServerProcess(t, a)
			ctx := context.Background()
			cookie := setupTestAdmin(t, a)
			api := httptest.NewServer(a.Handler())
			t.Cleanup(api.Close)
			// Deny accidental fallback before dialing; this client can reach only
			// this test's owned API listener, never an environment proxy or redirect.
			ownedAddress := api.Listener.Addr().String()
			dialer := &net.Dialer{Timeout: 5 * time.Second}
			transport := &http.Transport{
				Proxy: nil,
				DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
					if network != "tcp" || address != ownedAddress {
						return nil, fmt.Errorf("owned API dial target rejected")
					}
					return dialer.DialContext(ctx, network, address)
				},
			}
			t.Cleanup(transport.CloseIdleConnections)
			client := &http.Client{
				Transport: transport,
				Timeout:   15 * time.Second, // HTTP guard; production form budget remains 10s.
				CheckRedirect: func(*http.Request, []*http.Request) error {
					return fmt.Errorf("owned API redirect rejected")
				},
			}
			request := func(method, path string, body any) json.RawMessage {
				t.Helper()
				var raw []byte
				var err error
				if body != nil {
					raw, err = json.Marshal(body)
					if err != nil {
						t.Fatal(err)
					}
				}
				req, err := http.NewRequest(method, api.URL+path, strings.NewReader(string(raw)))
				if err != nil {
					t.Fatal(err)
				}
				req.Header.Set("Content-Type", "application/json")
				req.AddCookie(cookie)
				response, err := client.Do(req)
				if err != nil {
					t.Fatalf("owned API %s %s: %v", method, path, err)
				}
				defer response.Body.Close()
				data, err := io.ReadAll(io.LimitReader(response.Body, 2<<20))
				if err != nil {
					t.Fatal(err)
				}
				if response.StatusCode != http.StatusOK {
					t.Fatalf("owned API %s %s status=%d body=%s", method, path, response.StatusCode, data)
				}
				var envelope struct {
					Data json.RawMessage `json:"data"`
				}
				if err := json.Unmarshal(data, &envelope); err != nil {
					t.Fatal(err)
				}
				return envelope.Data
			}
			type instanceFixture struct {
				id, from, to, sourcePath, targetPath, backup string
				src, dst                                     *transferAppDaemon
				config                                       map[string]any
			}
			hash, material := transferTestMaterial()
			fixtures := make([]instanceFixture, 2)
			settings := Settings{AllowedRoots: []string{a.Dir}}
			names := []string{}
			for i, id := range []string{"TorrentTransfer", "TorrentTransfermusic"} {
				f := &fixtures[i]
				f.id, f.from, f.to = id, fmt.Sprintf("source-%d", i), fmt.Sprintf("destination-%d", i)
				f.sourcePath, f.targetPath, f.backup = filepath.Join(a.Dir, id, "source"), filepath.Join(a.Dir, id, "destination"), filepath.Join(a.Dir, id, "backup")
				for _, dir := range []string{f.sourcePath, f.targetPath, f.backup} {
					if err := os.MkdirAll(dir, 0700); err != nil {
						t.Fatal(err)
					}
				}
				if err := os.WriteFile(filepath.Join(f.sourcePath, "owned.bin"), []byte("x"), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(f.backup, hash+".torrent"), material, 0600); err != nil {
					t.Fatal(err)
				}
				f.src, f.dst = newTransferAppDaemon(t, kind, hash, true), newTransferAppDaemon(t, kind, hash, false)
				f.src.mu.Lock()
				f.src.dir = f.sourcePath
				f.src.mu.Unlock()
				f.dst.mu.Lock()
				f.dst.dir = f.targetPath
				f.dst.mu.Unlock()
				key := ""
				if kind == "qbittorrent" {
					key = "transfer-owned-key"
				}
				settings.Downloaders = append(settings.Downloaders,
					DownloaderConfig{Config: download.Config{ID: id + "-from", Kind: kind, BaseURL: f.src.server.URL, APIKey: key, Sequential: true, FirstLast: true}, Name: f.from},
					DownloaderConfig{Config: download.Config{ID: id + "-to", Kind: kind, BaseURL: f.dst.server.URL, APIKey: key, Sequential: true, FirstLast: true}, Name: f.to})
				names = append(names, f.from, f.to)
			}
			if err := a.saveSettings(ctx, settings); err != nil {
				t.Fatal(err)
			}
			a.pluginTransfer.pollSleep = func(context.Context, time.Duration) error { return nil }
			wantConf, defaults, err := transfer.Form(names)
			if err != nil {
				t.Fatal(err)
			}
			confJSON, err := json.Marshal(wantConf)
			if err != nil {
				t.Fatal(err)
			}
			var canonicalConf any
			if err := json.Unmarshal(confJSON, &canonicalConf); err != nil {
				t.Fatal(err)
			}
			for i := range fixtures {
				f := &fixtures[i]
				request("POST", "/api/plugins/install", map[string]string{"plugin_id": f.id})
				initial := transferAppNext(t, a)
				if initial.InstanceID != f.id || initial.Action != "init" {
					t.Fatal("wrong install run identity", initial.InstanceID, initial.Action)
				}
				var form struct {
					Conf     any            `json:"conf"`
					Model    map[string]any `json:"model"`
					Revision int64          `json:"revision"`
				}
				if err := json.Unmarshal(request("GET", "/api/plugins/"+f.id+"/form", nil), &form); err != nil {
					t.Fatal(err)
				}
				// Full source schema comparison covers original fields and both dynamically
				// populated downloader selectors, not just a label or HTTP 200.
				if !reflect.DeepEqual(form.Conf, canonicalConf) || !reflect.DeepEqual(form.Model, defaults) || form.Revision < 1 {
					t.Fatal("source form/defaults/inventory mismatch", f.id)
				}
				transferServerObserveForm(t, a, f.id, canonicalConf, defaults)
				f.config = map[string]any{}
				for k, v := range defaults {
					f.config[k] = v
				}
				for k, v := range map[string]any{"enabled": true, "onlyonce": false, "cron": "0 0 * * *", "fromdownloader": f.from, "todownloader": f.to, "fromtorrentpath": f.backup, "frompath": f.sourcePath, "topath": f.targetPath, "autostart": true, "skipverify": false, "transferemptylabel": true, "deletesource": false, "deleteduplicate": false, "notify": true} {
					f.config[k] = v
				}
				request("PUT", "/api/plugins/"+f.id+"/config", map[string]any{"revision": form.Revision, "config": f.config})
				configured := transferAppNext(t, a)
				if configured.InstanceID != f.id || configured.Action != "init" || configured.ConfigRevision <= initial.ConfigRevision {
					t.Fatal("wrong config initialization identity/revision")
				}
				stored, err := a.pluginStore.Config(ctx, f.id)
				if err != nil {
					t.Fatal(err)
				}
				var actual map[string]any
				if err := json.Unmarshal(stored.Value, &actual); err != nil || !reflect.DeepEqual(actual, f.config) {
					t.Fatal("configuration not persisted independently", f.id)
				}
				if err := json.Unmarshal(request("GET", "/api/plugins/"+f.id+"/form", nil), &form); err != nil || form.Revision != stored.Revision || !reflect.DeepEqual(form.Model, f.config) || !reflect.DeepEqual(form.Conf, canonicalConf) {
					t.Fatal("HTTP form did not project this instance's persisted configuration", f.id)
				}
			}
			for i := range fixtures {
				f, other := &fixtures[i], &fixtures[1-i]
				otherConfig, err := a.pluginStore.Config(ctx, other.id)
				if err != nil {
					t.Fatal(err)
				}
				otherCheckpoint := transferCheckpoint(t, a, other.id)
				beforeSource, beforeTarget := other.src.snapshot(), other.dst.snapshot()
				otherHistory, otherHistoryErr := a.pluginStore.Data(ctx, other.id, other.from+"-"+hash)
				if otherHistoryErr != nil && !missingPluginValue(otherHistoryErr) {
					t.Fatal(otherHistoryErr)
				}
				request("POST", "/api/plugins/services/run", map[string]string{"instance_id": f.id, "service_id": f.id + ".TorrentTransfer"})
				run := transferAppNext(t, a)
				if run.InstanceID != f.id || run.Action != "service" || run.State != "succeeded" || run.ErrorCode != "" {
					t.Fatal("wrong persisted successful service identity")
				}
				cp := transferCheckpoint(t, a, f.id)
				if cp.Counts.Success != 1 || cp.SourceError != "" || cp.Identity.Instance != f.id || cp.Identity.Run != run.ID || cp.Identity.Generation != uint64(run.Generation) || cp.Identity.Revision != uint64(run.ConfigRevision) {
					t.Fatalf("business checkpoint identity/count mismatch for %s: %+v", f.id, cp)
				}
				history, err := a.pluginStore.Data(ctx, f.id, f.from+"-"+hash)
				var row map[string]any
				if err != nil || json.Unmarshal(history.Value, &row) != nil || row["to_download"] != f.to || row["delete_source"] != false {
					t.Fatal("successful instance history not persisted", f.id, err)
				}
				if _, err := a.pluginStore.Data(ctx, other.id, f.from+"-"+hash); err == nil || !missingPluginValue(err) {
					t.Fatal("history namespace leaked or lookup failed", err)
				}
				afterConfig, err := a.pluginStore.Config(ctx, other.id)
				if err != nil || !reflect.DeepEqual(afterConfig, otherConfig) || !reflect.DeepEqual(transferCheckpoint(t, a, other.id), otherCheckpoint) {
					t.Fatal("service changed other instance config/checkpoint")
				}
				if !reflect.DeepEqual(beforeSource, other.src.snapshot()) || !reflect.DeepEqual(beforeTarget, other.dst.snapshot()) {
					t.Fatal("service dispatched IO to other instance daemon pair")
				}
				afterHistory, afterHistoryErr := a.pluginStore.Data(ctx, other.id, other.from+"-"+hash)
				if (otherHistoryErr == nil) != (afterHistoryErr == nil) || (afterHistoryErr != nil && !missingPluginValue(afterHistoryErr)) || !reflect.DeepEqual(otherHistory, afterHistory) {
					t.Fatal("service changed other instance KV history")
				}
				calls := f.dst.snapshot()
				wantVerify := 0
				if kind == "qbittorrent" {
					wantVerify = 1
				}
				if calls["/api/v2/torrents/add"]+calls["torrent_add"] != 1 || calls["/api/v2/torrents/recheck"]+calls["torrent_verify"] != wantVerify || calls["/api/v2/torrents/start"]+calls["/api/v2/torrents/resume"]+calls["torrent_start"] != 1 {
					t.Fatal("target add/verify/start business effects missing or duplicated", calls)
				}
				sourceCalls := f.src.snapshot()
				if sourceCalls["/api/v2/torrents/info"]+sourceCalls["torrent_get"] == 0 || sourceCalls["/api/v2/torrents/delete"]+sourceCalls["torrent_remove"] != 0 {
					t.Fatal("source discovery/preservation mismatch", sourceCalls)
				}
				f.dst.mu.Lock()
				added, path := f.dst.added, f.dst.dir
				f.dst.mu.Unlock()
				f.src.mu.Lock()
				removed := f.src.removed
				f.src.mu.Unlock()
				if !added || path != f.targetPath || removed {
					t.Fatal("native target path or preserved source mismatch")
				}
				original, err := os.ReadFile(filepath.Join(f.sourcePath, "owned.bin"))
				if err != nil || string(original) != "x" {
					t.Fatal("owned source data changed")
				}
				backup, err := os.ReadFile(filepath.Join(f.backup, hash+".torrent"))
				if err != nil || !reflect.DeepEqual(backup, material) {
					t.Fatal("owned torrent material changed")
				}
				var runs []PluginRun
				if err := json.Unmarshal(request("GET", "/api/plugins/"+f.id+"/runs", nil), &runs); err != nil {
					t.Fatal(err)
				}
				found := false
				for _, saved := range runs {
					if saved.ID == run.ID {
						found = saved.InstanceID == f.id && saved.Action == "service" && saved.State == "succeeded"
					}
				}
				if !found {
					t.Fatal("public persisted service run missing")
				}
				if len(a.pluginProcess) != 0 {
					t.Fatal("process pool slot leaked after HTTP and service completion")
				}
			}
		})
	}
}

// HTTP does not expose its Runner observation stream. This separate invocation
// uses the SAME ordinary executable and real App host, without a new production
// hook, to assert host identity, stop acknowledgement and successful cmd.Wait.
func transferServerObserveForm(t *testing.T, a *App, id string, wantConf any, wantModel map[string]any) {
	t.Helper()
	in := mustTransferInstallation(t, a, id)
	command, err := a.pluginExecutable(in.PluginID)
	if err != nil {
		t.Fatal(err)
	}
	var events []plugins.RunObservation
	hostCalls := 0
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	runner := plugins.Runner{Timeout: 10 * time.Second, ObservePhase: func(event plugins.RunObservation) { events = append(events, event) }}
	result, err := runner.Run(ctx, command, transfer.Manifest(in.PluginID), in.InstanceID, "config_form", nil, func(hostCtx context.Context, identity plugins.Identity, method string, payload json.RawMessage) (json.RawMessage, error) {
		hostCalls++
		if identity.PluginID != in.PluginID || identity.InstanceID != in.InstanceID || identity.Version != in.Version || method != transfer.FormCapability || string(payload) != "{}" {
			return nil, fmt.Errorf("server form host identity/capability mismatch")
		}
		return a.pluginHostCall(hostCtx, in, nil, identity, method, payload)
	})
	if err != nil {
		t.Fatalf("ordinary server form runner failed: %s", transferFormDiagnosticError(err))
	}
	if hostCalls != 1 || result.HostCalls != 1 || result.ReadWireBytes == 0 || result.SentWireBytes == 0 {
		t.Fatal("actual server host exchange missing")
	}
	wantPhases := []plugins.RunPhase{plugins.PhaseRunEntered, plugins.PhaseStartBegin, plugins.PhaseStartEnd, plugins.PhaseHandshakeBegin, plugins.PhaseHandshakeEnd, plugins.PhaseNegotiateBegin, plugins.PhaseNegotiateEnd, plugins.PhaseInvokeBegin, plugins.PhaseInvokeEnd, plugins.PhaseStopBegin, plugins.PhaseStopEnd, plugins.PhaseWaitBegin, plugins.PhaseWaitEnd, plugins.PhaseRunReturned}
	phases := make([]plugins.RunPhase, len(events))
	for i, event := range events {
		phases[i] = event.Phase
		if event.ContextState != plugins.RunContextActive {
			t.Fatal("server runner context not active", event)
		}
	}
	if !reflect.DeepEqual(phases, wantPhases) {
		t.Fatal("ordinary server stop/wait sequence mismatch", phases)
	}
	var form struct {
		Conf  any            `json:"conf"`
		Model map[string]any `json:"model"`
	}
	if err := json.Unmarshal(result.Result, &form); err != nil || !reflect.DeepEqual(form.Conf, wantConf) || !reflect.DeepEqual(form.Model, wantModel) {
		t.Fatal("ordinary server form result mismatch")
	}
}
