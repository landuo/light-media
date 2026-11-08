package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"light-media/internal/download"
	"light-media/internal/plugins"
)

func sourceDownloaderFixture(t *testing.T) (*App, *originalPluginInstance) {
	t.Helper()
	binary := os.Getenv("PLUGIN_SOURCE_DOWNLOADER_FIXTURE_BINARY")
	if binary == "" {
		t.Fatal("PLUGIN_SOURCE_DOWNLOADER_FIXTURE_BINARY must name an independently built source-outcome SDK worker")
	}
	a := newTestApp(t)
	cookie := setupTestAdmin(t, a)
	m := installOwnedResourceProcessWorker(t, a, cookie, binary)
	value, ok := a.pluginRunningInstances.Load(m.PluginID)
	if !ok {
		t.Fatal("actual source outcome worker not loaded")
	}
	return a, value.(*originalPluginInstance)
}
func TestSourceDownloaderNativeFailuresActualSDKKeepLegacyErrors(t *testing.T) {
	for _, kind := range []string{"qbittorrent", "transmission"} {
		for _, operation := range []string{"tasks", "completed", "add", "control"} {
			t.Run(kind+"/"+operation, func(t *testing.T) {
				a, object := sourceDownloaderFixture(t)
				var selected atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					selected.Add(1)
					http.Error(w, "OWNED_PRIVATE_VENDOR_BODY", 500)
				}))
				defer server.Close()
				a.cfgMu.Lock()
				a.config.Downloaders = []DownloaderConfig{{Config: download.Config{ID: "owned", Kind: kind, BaseURL: server.URL}}}
				a.cfgMu.Unlock()
				var request any
				switch operation {
				case "tasks":
					request = plugins.DownloaderTasksRequestV1{DownloaderID: "owned", Hashes: []string{ownedDownloaderHash}}
				case "completed":
					request = plugins.DownloaderCompletedRequestV1{DownloaderID: "owned"}
				case "add":
					request = plugins.DownloaderAddRequestV1{DownloaderID: "owned", Torrent: []byte("owned material"), Paused: true}
				case "control":
					request = plugins.DownloaderControlRequestV1{DownloaderID: "owned", Hashes: []string{ownedDownloaderHash}, Action: "start"}
				}
				var raw json.RawMessage
				if err := hostDownloaderCall(t, object, "source_"+operation, request, &raw); err != nil {
					t.Fatal("ordinary vendor failure did not cross actual SDK as typed source result", err)
				}
				if strings.Contains(string(raw), "PRIVATE") {
					t.Fatal("private vendor response leaked", string(raw))
				}
				if operation == "tasks" || operation == "completed" {
					var out plugins.DownloaderSourceTasksResultV1
					if json.Unmarshal(raw, &out) != nil || !out.NativeError || len(out.Tasks) != 0 || out.Tasks == nil || out.Kind != kind {
						t.Fatal("query source failure lost", string(raw))
					}
				} else {
					var out plugins.DownloaderSourceMutationResultV1
					if json.Unmarshal(raw, &out) != nil || !out.NativeError || out.Accepted || out.Outcome != "rejected" {
						t.Fatal("preflight source false lost", string(raw))
					}
				}
				before := selected.Load()
				if before < 1 {
					t.Fatal("no actual native request")
				}
				if err := hostDownloaderCall(t, object, "legacy_"+operation, request, nil); err == nil {
					t.Fatal("legacy capability silently changed its error contract")
				}
				if selected.Load() <= before {
					t.Fatal("legacy comparison never reached native peer")
				}
			})
		}
	}
}
func TestSourceDownloaderActualSDKUnknownAndRevokedWriteFacts(t *testing.T) {
	for _, operation := range []string{"add", "control"} {
		for _, mode := range []string{"confirmed", "unknown", "confirmed_revoked", "unknown_revoked"} {
			t.Run(operation+"/"+mode, func(t *testing.T) {
				a, object := sourceDownloaderFixture(t)
				var writes atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == "/api/v2/app/webapiVersion" {
						io.WriteString(w, "2.11.4")
						return
					}
					if r.URL.Path != "/api/v2/torrents/add" && r.URL.Path != "/api/v2/torrents/start" {
						t.Error("unexpected native request", r.URL.Path)
						http.Error(w, "unexpected", 400)
						return
					}
					writes.Add(1)
					if strings.HasSuffix(mode, "_revoked") {
						a.cfgMu.Lock()
						disabled := false
						a.config.Downloaders[0].Enabled = &disabled
						a.cfgMu.Unlock()
					}
					if strings.HasPrefix(mode, "unknown") {
						hijack, ok := w.(http.Hijacker)
						if !ok {
							t.Error("no actual EOF injection")
							return
						}
						conn, _, err := hijack.Hijack()
						if err != nil {
							t.Error(err)
							return
						}
						conn.Close()
						return
					}
					if operation == "add" {
						io.WriteString(w, `{"added_torrent_ids":{"`+ownedDownloaderHash+`":{}},"errors":{}}`)
					}
				}))
				defer server.Close()
				a.cfgMu.Lock()
				a.config.Downloaders = []DownloaderConfig{{Config: download.Config{ID: "owned", Kind: "qbittorrent", BaseURL: server.URL}}}
				a.cfgMu.Unlock()
				var request any = plugins.DownloaderAddRequestV1{DownloaderID: "owned", Torrent: []byte("owned material"), Paused: true}
				if operation == "control" {
					request = plugins.DownloaderControlRequestV1{DownloaderID: "owned", Hashes: []string{ownedDownloaderHash}, Action: "start"}
				}
				var out plugins.DownloaderSourceMutationResultV1
				if err := hostDownloaderCall(t, object, "source_"+operation, request, &out); err != nil {
					t.Fatal("actual write facts discarded", err)
				}
				unknown := strings.HasPrefix(mode, "unknown")
				if out.NativeError != unknown || out.Accepted == unknown || out.Outcome != map[bool]string{true: "unknown", false: "confirmed"}[unknown] {
					t.Fatal("native error/receipt conflated", out)
				}
				wantCode := ""
				if strings.HasSuffix(mode, "_revoked") {
					wantCode = "CALL_CHANGED_AFTER_MUTATION"
				}
				if out.FailureCode != wantCode {
					t.Fatal("scope loss erased mutation evidence", out)
				}
				if operation == "add" && !unknown && (len(out.AddedIDs) != 1 || out.AddedIDs[0] != ownedDownloaderHash) {
					t.Fatal("confirmed added IDs lost", out)
				}
				if writes.Load() != 1 {
					t.Fatal("selected write automatically replayed", writes.Load())
				}
			})
		}
	}
}
func TestSourceDownloaderGuardRejectionCannotBecomeNativeFalseAfterRecovery(t *testing.T) {
	for _, method := range []string{plugins.DownloaderSourceTasksCapability, plugins.DownloaderSourceCompletedCapability, plugins.DownloaderSourceAddCapability, plugins.DownloaderSourceControlCapability} {
		t.Run(method, func(t *testing.T) {
			a, object := sourceDownloaderFixture(t)
			var network atomic.Int32
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				network.Add(1)
				t.Error("guarded request reached network")
				http.Error(w, "unexpected", 500)
			}))
			defer peer.Close()
			a.cfgMu.Lock()
			a.config.Downloaders = []DownloaderConfig{{Config: download.Config{ID: "owned", Kind: "qbittorrent", BaseURL: peer.URL}}}
			a.cfgMu.Unlock()
			var checks atomic.Int32
			sentinel := errors.New("OWNED_PRIVATE_SCOPE_CAUSE")
			scope := originalBaseCallScope{installation: object.installation, object: object, validate: func(context.Context) error {
				if checks.Add(1) == 3 {
					return sentinel
				}
				return nil
			}}
			var request any
			switch method {
			case plugins.DownloaderSourceTasksCapability:
				request = plugins.DownloaderTasksRequestV1{DownloaderID: "owned", Hashes: []string{ownedDownloaderHash}}
			case plugins.DownloaderSourceCompletedCapability:
				request = plugins.DownloaderCompletedRequestV1{DownloaderID: "owned"}
			case plugins.DownloaderSourceAddCapability:
				request = plugins.DownloaderAddRequestV1{DownloaderID: "owned", Torrent: []byte("owned"), Paused: true}
			case plugins.DownloaderSourceControlCapability:
				request = plugins.DownloaderControlRequestV1{DownloaderID: "owned", Hashes: []string{ownedDownloaderHash}, Action: "start"}
			}
			raw, _ := json.Marshal(request)
			identity := plugins.Identity{PluginID: object.installation.PluginID, InstanceID: object.installation.InstanceID, Version: object.installation.Version}
			out, err := a.pluginOriginalResourceHost(context.Background(), scope, identity, method, raw)
			var denied *originalDownloaderGuardError
			if err == nil || len(out) != 0 || !errors.As(err, &denied) || !errors.Is(err, sentinel) || checks.Load() < 4 || network.Load() != 0 {
				t.Fatal("transient transport guard was swallowed as vendor false", string(out), err, checks.Load(), network.Load())
			}
			if strings.Contains(err.Error(), "PRIVATE") {
				t.Fatal("native wrapper exposed its retained private cause")
			}
		})
	}
}
func TestSourceDownloaderActualSDKCanonicalScopeAndEmptyMaterial(t *testing.T) {
	a, object := sourceDownloaderFixture(t)
	var network atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { network.Add(1); http.Error(w, "unexpected", 500) }))
	defer peer.Close()
	a.cfgMu.Lock()
	a.config.Downloaders = []DownloaderConfig{{Config: download.Config{ID: "owned", Kind: "qbittorrent", BaseURL: peer.URL}}}
	a.cfgMu.Unlock()
	invalid := []struct {
		method  string
		request any
	}{{"source_tasks", plugins.DownloaderTasksRequestV1{DownloaderID: "owned"}}, {"source_completed", plugins.DownloaderCompletedRequestV1{DownloaderID: "owned", Hashes: []string{"all"}}}, {"source_add", plugins.DownloaderAddRequestV1{DownloaderID: "owned", Torrent: []byte("owned"), Magnet: "magnet:?xt=urn:btih:" + ownedDownloaderHash}}, {"source_control", plugins.DownloaderControlRequestV1{DownloaderID: "owned", Hashes: []string{"all"}, Action: "start"}}}
	for _, item := range invalid {
		if err := hostDownloaderCall(t, object, item.method, item.request, nil); err == nil {
			t.Fatal("invalid source request became native false", item.method)
		}
	}
	for _, item := range []struct {
		method string
		value  any
	}{
		{plugins.DownloaderSourceTasksCapability, map[string]any{"downloader_id": "owned"}},
		{plugins.DownloaderSourceCompletedCapability, map[string]any{"downloader_id": "owned", "hashes": []string{"all"}}},
		{plugins.DownloaderSourceAddCapability, map[string]any{"downloader_id": "owned", "rpc": "torrent-delete"}},
	} {
		if err := hostDownloaderCall(t, object, "resource", map[string]any{"method": item.method, "value": item.value}, nil); err == nil {
			t.Fatal("raw worker bypassed source resource validation", item.method)
		}
	}
	for _, kind := range []string{"qbittorrent", "transmission"} {
		a.cfgMu.Lock()
		a.config.Downloaders[0].Kind = kind
		a.cfgMu.Unlock()
		var out plugins.DownloaderSourceMutationResultV1
		if err := hostDownloaderCall(t, object, "source_add", plugins.DownloaderAddRequestV1{DownloaderID: "owned", Torrent: []byte{}}, &out); err != nil || out.NativeError || out.Accepted || out.Outcome != "rejected" {
			t.Fatal("empty material broadened or reported as transport error", kind, out, err)
		}
	}
	callback, closePhase := object.originalBaseSessionHost(context.Background(), "resource", nil)
	raw, _ := json.Marshal(plugins.DownloaderTasksRequestV1{DownloaderID: "owned", Hashes: []string{}})
	identity := plugins.Identity{PluginID: object.installation.PluginID, InstanceID: object.installation.InstanceID, Version: object.installation.Version}
	wrong := identity
	wrong.InstanceID += "other"
	if _, err := callback(context.Background(), wrong, plugins.DownloaderSourceTasksCapability, raw); err == nil {
		t.Fatal("foreign identity accepted")
	}
	closePhase()
	if _, err := callback(context.Background(), identity, plugins.DownloaderSourceTasksCapability, raw); err == nil {
		t.Fatal("retired scope accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := object.callSession(ctx, plugins.Runner{Timeout: time.Second}, "source_tasks", raw, nil); err == nil {
		t.Fatal("cancellation accepted")
	}
	if network.Load() != 0 {
		t.Fatal("invalid or empty material reached downloader", network.Load())
	}
	t.Log(fmt.Sprintf("actual source worker and native boundary rejected%d invalid selectors; empty QB/TR materials dispatched0 requests", len(invalid)))
}

func TestSourceDownloaderSuccessActualSDKKeepsRowsAndEmptySelection(t *testing.T) {
	for _, kind := range []string{"qbittorrent", "transmission"} {
		t.Run(kind, func(t *testing.T) {
			a, object := sourceDownloaderFixture(t)
			var reads atomic.Int32
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if kind == "qbittorrent" {
					if r.URL.Path != "/api/v2/torrents/info" {
						t.Error(r.URL.Path)
						return
					}
					reads.Add(1)
					fmt.Fprintf(w, `[{"hash":"%s","progress":1,"amount_left":0,"state":"uploading","tags":"owned","uploaded":9007199254740993}]`, ownedDownloaderHash)
					return
				}
				var request map[string]json.RawMessage
				if json.NewDecoder(r.Body).Decode(&request) != nil {
					t.Error("invalid native RPC")
					return
				}
				var method string
				json.Unmarshal(request["method"], &method)
				switch method {
				case "session-get":
					io.WriteString(w, `{"result":"success","arguments":{"version":"3.00","rpc-version":16}}`)
				case "torrent-get":
					reads.Add(1)
					fmt.Fprintf(w, `{"result":"success","arguments":{"torrents":[{"hashString":"%s","status":6,"labels":["owned"],"uploadedEver":9007199254740993}]}}`, ownedDownloaderHash)
				case "session_get":
					fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":{"version":"4.1"}}`, request["id"])
				case "torrent_get":
					reads.Add(1)
					fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":{"torrents":[{"hash_string":"%s","status":6,"labels":["owned"],"uploaded_ever":9007199254740993}]}}`, request["id"], ownedDownloaderHash)
				default:
					t.Error("unexpected native RPC", method)
				}
			}))
			defer peer.Close()
			a.cfgMu.Lock()
			a.config.Downloaders = []DownloaderConfig{{Config: download.Config{ID: "owned", Kind: kind, BaseURL: peer.URL}}}
			a.cfgMu.Unlock()
			for _, method := range []string{"source_tasks", "source_completed"} {
				var request any = plugins.DownloaderTasksRequestV1{DownloaderID: "owned", Hashes: []string{ownedDownloaderHash}}
				if method == "source_completed" {
					request = plugins.DownloaderCompletedRequestV1{DownloaderID: "owned", Tags: []string{"owned"}}
				}
				var out plugins.DownloaderSourceTasksResultV1
				if err := hostDownloaderCall(t, object, method, request, &out); err != nil || out.NativeError || len(out.Tasks) != 1 || out.Tasks[0].Hash != ownedDownloaderHash {
					t.Fatal("successful source row lost", method, out, err)
				}
				if kind == "qbittorrent" {
					if out.Tasks[0].QBittorrent.Uploaded == nil || *out.Tasks[0].QBittorrent.Uploaded != "9007199254740993" {
						t.Fatal("QB source precision lost")
					}
				} else if out.Tasks[0].Transmission.UploadedEver == nil || *out.Tasks[0].Transmission.UploadedEver != "9007199254740993" {
					t.Fatal("TR source precision lost")
				}
			}
			before := reads.Load()
			var out plugins.DownloaderSourceTasksResultV1
			if err := hostDownloaderCall(t, object, "source_tasks", plugins.DownloaderTasksRequestV1{DownloaderID: "owned", Hashes: []string{}}, &out); err != nil || out.NativeError || len(out.Tasks) != 0 || reads.Load() != before {
				t.Fatal("empty source selection broadened", out, err, reads.Load(), before)
			}
		})
	}
}
func TestSourceDownloaderDisabledMutationsRemainErrors(t *testing.T) {
	a, object := sourceDownloaderFixture(t)
	var requests atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); http.Error(w, "unexpected", 500) }))
	defer peer.Close()
	disabled := false
	a.cfgMu.Lock()
	a.config.Downloaders = []DownloaderConfig{{Config: download.Config{ID: "owned", Kind: "qbittorrent", BaseURL: peer.URL}, Enabled: &disabled}}
	a.cfgMu.Unlock()
	for _, item := range []struct {
		method string
		value  any
	}{{"source_completed", plugins.DownloaderCompletedRequestV1{DownloaderID: "owned"}}, {"source_add", plugins.DownloaderAddRequestV1{DownloaderID: "owned", Torrent: []byte("owned")}}, {"source_control", plugins.DownloaderControlRequestV1{DownloaderID: "owned", Hashes: []string{ownedDownloaderHash}, Action: "start"}}} {
		if err := hostDownloaderCall(t, object, item.method, item.value, nil); err == nil {
			t.Fatal("disabled source operation converted to native false", item.method)
		}
	}
	if requests.Load() != 0 {
		t.Fatal("disabled source operation reached native peer")
	}
}

func TestSourceDownloaderCookieFallbackCannotSwallowTransientScope(t *testing.T) {
	a, object := sourceDownloaderFixture(t)
	var checks, wire atomic.Int32
	var responded, denied atomic.Bool
	sentinel := errors.New("OWNED_PRIVATE_COOKIE_SCOPE")
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		wire.Add(1)
		if r.URL.Path != "/api/v2/app/webapiVersion" {
			t.Error("transient cookie guard fell through to write", r.URL.Path)
		}
		responded.Store(true)
		io.WriteString(w, "2.11.4")
	}))
	defer peer.Close()
	a.cfgMu.Lock()
	a.config.Downloaders = []DownloaderConfig{{Config: download.Config{ID: "owned", Kind: "qbittorrent", BaseURL: peer.URL}}}
	a.cfgMu.Unlock()
	scope := originalBaseCallScope{installation: object.installation, object: object, validate: func(context.Context) error {
		checks.Add(1)
		if responded.Load() && !denied.Swap(true) {
			return sentinel
		}
		return nil
	}}
	address, cookie := "https://owned.invalid/material", "owned=synthetic"
	raw, _ := json.Marshal(plugins.DownloaderAddRequestV1{DownloaderID: "owned", URL: &address, Cookie: &cookie, Paused: true})
	identity := plugins.Identity{PluginID: object.installation.PluginID, InstanceID: object.installation.InstanceID, Version: object.installation.Version}
	out, err := a.pluginOriginalResourceHost(context.Background(), scope, identity, plugins.DownloaderSourceAddCapability, raw)
	if len(out) != 0 || !errors.Is(err, sentinel) || wire.Load() != 1 || checks.Load() < 5 {
		t.Fatal("cookie native guard became ordinary fallback/false", string(out), err, wire.Load(), checks.Load())
	}
}
