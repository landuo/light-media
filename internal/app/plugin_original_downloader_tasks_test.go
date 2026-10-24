package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"light-media/internal/download"
	"light-media/internal/plugins"
)

const ownedDownloaderHash = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
const ownedDownloaderOtherHash = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

func ownedDownloaderSave(t *testing.T, a *App, config Settings) {
	t.Helper()
	a.settingsMu.Lock()
	err := a.saveSettings(context.Background(), config)
	a.settingsMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
}

func ownedDownloaderChanges(t *testing.T, a *App) int64 {
	t.Helper()
	var changes int64
	if err := a.Store.DB.QueryRow(`SELECT total_changes()`).Scan(&changes); err != nil {
		t.Fatal(err)
	}
	return changes
}

func ownedDownloaderNoWrites(t *testing.T, a *App, before int64) {
	t.Helper()
	if after := ownedDownloaderChanges(t, a); after != before {
		t.Fatalf("read wrote database: %d -> %d", before, after)
	}
	var runs int
	if err := a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_runs`).Scan(&runs); err != nil || runs != 0 {
		t.Fatal("read used old plugin runs", err)
	}
}

func ownedDownloaderRequest(id string, hashes ...string) plugins.DownloaderTasksRequestV1 {
	if hashes == nil {
		hashes = []string{}
	}
	return plugins.DownloaderTasksRequestV1{DownloaderID: id, Hashes: hashes}
}

func TestOriginalDownloaderTasksActualSDKQBSelectionCredentialsAndClone(t *testing.T) {
	for _, bearer := range []bool{false, true} {
		t.Run(fmt.Sprint("bearer=", bearer), func(t *testing.T) {
			a, cookie, object := ownedDownloaderTasksSDKFixture(t, true)
			var login, reads, unexpected, other atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v2/auth/login":
					login.Add(1)
					_ = r.ParseForm()
					if bearer || r.Method != http.MethodPost || r.Form.Get("username") != "owned-user" || r.Form.Get("password") != "OWNED_PASSWORD_SECRET" {
						t.Error("credentials were not host-bound")
					}
					http.SetCookie(w, &http.Cookie{Name: "SID", Value: "OWNED_SESSION_SECRET", Path: "/"})
					_, _ = io.WriteString(w, "Ok.")
				case "/api/v2/torrents/info":
					reads.Add(1)
					_ = r.ParseForm()
					if r.Method != http.MethodPost || r.Form.Get("hashes") != ownedDownloaderHash || len(r.Form) != 1 {
						t.Error("request widened or changed native qB selection")
					}
					if bearer {
						if r.Header.Get("Authorization") != "Bearer OWNED_API_KEY_SECRET" {
							t.Error("missing bound API key")
						}
					} else if c, err := r.Cookie("SID"); err != nil || c.Value != "OWNED_SESSION_SECRET" {
						t.Error("missing bound session")
					}
					_, _ = io.WriteString(w, `[{"hash":"`+ownedDownloaderOtherHash+`","name":"unselected"},{"hash":"`+ownedDownloaderHash+`","name":"owned task","state":"pausedUP","total_size":9007199254740993,"uploaded":18446744073709551615,"downloaded":9007199254740995,"progress":1,"ratio":1.25,"tags":"owned, second","save_path":"/owned/tasks","tracker":"https://tracker.invalid/OWNED_PASSKEY_SECRET","magnet_uri":"OWNED_MAGNET_SECRET","password":"OWNED_PASSWORD_SECRET","unknown":{"cookie":"OWNED_COOKIE_SECRET"}}]`)
				default:
					unexpected.Add(1)
					http.Error(w, "owned unexpected endpoint", 400)
				}
			}))
			defer server.Close()
			otherServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { other.Add(1); http.Error(w, "wrong downloader", 400) }))
			defer otherServer.Close()
			config := a.Settings()
			no := false
			selected := download.Config{ID: "owned-qb", Kind: "qbittorrent", BaseURL: server.URL, Username: "owned-user", Password: "OWNED_PASSWORD_SECRET"}
			if bearer {
				selected.APIKey = "OWNED_API_KEY_SECRET"
			}
			config.Downloaders = []DownloaderConfig{
				{Config: download.Config{ID: "other-qb", Kind: "qbittorrent", BaseURL: otherServer.URL}, Name: "DuplicateName"},
				{Config: selected, Name: "DuplicateName", Enabled: &no},
			}
			config.DefaultDownloader = "other-qb"
			ownedDownloaderSave(t, a, config)
			before := ownedDownloaderChanges(t, a)
			settings, _ := json.Marshal(a.Settings())
			messages := append([]byte{}, a.systemMessages.get("system")...)
			empty := ownedDownloaderTasksRead(t, object, ownedDownloaderRequest("owned-qb"))
			if empty.Result.Tasks == nil || len(empty.Result.Tasks) != 0 || login.Load()+reads.Load()+other.Load() != 0 {
				t.Fatal("empty hashes dispatched or manufactured a task")
			}
			request := ownedDownloaderRequest("owned-qb", strings.ToUpper(ownedDownloaderHash), ownedDownloaderHash)
			got := ownedDownloaderTasksRead(t, object, request)
			if got.PID != empty.PID || !got.HadPrevious || got.Result.DownloaderID != "owned-qb" || got.Result.Kind != "qbittorrent" || len(got.Result.Tasks) != 1 {
				t.Fatal("exact ID selection, persistent process or stale callback failed")
			}
			row := got.Result.Tasks[0]
			if row.Hash != ownedDownloaderHash || row.Transmission != nil || row.QBittorrent == nil || row.QBittorrent.TotalSize == nil || *row.QBittorrent.TotalSize != "9007199254740993" || row.QBittorrent.Uploaded == nil || *row.QBittorrent.Uploaded != "18446744073709551615" || row.QBittorrent.Downloaded == nil || *row.QBittorrent.Downloaded != "9007199254740995" {
				t.Fatal("selected native task or integer precision changed")
			}
			encoded, _ := json.Marshal(got)
			for _, secret := range []string{"OWNED_PASSWORD_SECRET", "OWNED_API_KEY_SECRET", "OWNED_SESSION_SECRET", "OWNED_PASSKEY_SECRET", "OWNED_MAGNET_SECRET", "OWNED_COOKIE_SECRET", server.URL, "unselected"} {
				if bytes.Contains(encoded, []byte(secret)) {
					t.Fatal("credential or unselected task escaped the DTO")
				}
			}
			if reads.Load() != 1 || unexpected.Load() != 0 || other.Load() != 0 || bearer && login.Load() != 0 || !bearer && login.Load() != 1 {
				t.Fatal("wrong native endpoint count")
			}
			after, _ := json.Marshal(a.Settings())
			if !bytes.Equal(settings, after) || !bytes.Equal(messages, a.systemMessages.get("system")) {
				t.Fatal("task read changed configuration or messages")
			}
			ownedDownloaderNoWrites(t, a, before)

			cloneResponse := callTest(t, a.Handler(), "POST", "/api/plugins/"+object.installation.PluginID+"/clone", `{"suffix":"Twin"}`, cookie, "")
			assertOriginalRegistrySuccess(t, cloneResponse.Code, cloneResponse.Body.Bytes())
			a.wg.Wait()
			value, ok := a.pluginRunningInstances.Load(object.installation.PluginID + "twin")
			if !ok || value == object {
				t.Fatal("public clone missing")
			}
			before = ownedDownloaderChanges(t, a)
			cloned := ownedDownloaderTasksRead(t, value.(*originalPluginInstance), request)
			if cloned.PID == got.PID || !reflect.DeepEqual(cloned.Result, got.Result) {
				t.Fatal("clone failed independent current-instance resource call")
			}
			ownedDownloaderNoWrites(t, a, before)
			if reads.Load() != 2 || unexpected.Load() != 0 || other.Load() != 0 {
				t.Fatal("clone selected unexpected daemon")
			}
		})
	}
}

func TestOriginalDownloaderTasksActualSDKTransmissionDialects(t *testing.T) {
	for _, modern := range []bool{false, true} {
		t.Run(fmt.Sprint("modern=", modern), func(t *testing.T) {
			a, _, object := ownedDownloaderTasksSDKFixture(t, true)
			var selected, unexpected atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/transmission/rpc" {
					unexpected.Add(1)
					http.Error(w, "wrong endpoint", 400)
					return
				}
				u, p, ok := r.BasicAuth()
				if !ok || u != "owned-user" || p != "OWNED_TR_PASSWORD_SECRET" {
					t.Error("Transmission credentials not host-bound")
				}
				if r.Header.Get("X-Transmission-Session-Id") != "owned-session" {
					w.Header().Set("X-Transmission-Session-Id", "owned-session")
					w.WriteHeader(409)
					return
				}
				var envelope map[string]json.RawMessage
				if json.NewDecoder(r.Body).Decode(&envelope) != nil {
					t.Error("bad RPC envelope")
					return
				}
				var method string
				_ = json.Unmarshal(envelope["method"], &method)
				if !modern && method == "session_get" {
					_, _ = io.WriteString(w, `{"result":"method name not recognized","arguments":{}}`)
					return
				}
				key, id := "arguments", envelope["tag"]
				if modern {
					key, id = "params", envelope["id"]
				}
				respond := func(result string) {
					if modern {
						_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":%s}`, id, result)
					} else {
						_, _ = fmt.Fprintf(w, `{"tag":%s,"result":"success","arguments":%s}`, id, result)
					}
				}
				switch method {
				case "session_get", "session-get":
					respond(`{"version":"4.1"}`)
				case "torrent_get", "torrent-get":
					selected.Add(1)
					var args struct {
						IDs    []string `json:"ids"`
						Fields []string `json:"fields"`
					}
					if json.Unmarshal(envelope[key], &args) != nil || !reflect.DeepEqual(args.IDs, []string{ownedDownloaderHash}) || len(args.Fields) == 0 {
						t.Error("Transmission exact hash request widened")
					}
					hashKey, sizeKey, uploadKey, downloadedKey, progressKey := "hashString", "totalSize", "uploadedEver", "downloadedEver", "percentDone"
					if modern {
						hashKey, sizeKey, uploadKey, downloadedKey, progressKey = "hash_string", "total_size", "uploaded_ever", "downloaded_ever", "percent_done"
					}
					respond(fmt.Sprintf(`{"torrents":[{"%s":"%s","name":"unselected"},{"%s":"%s","id":9007199254740993,"name":"owned tr","status":5,"%s":9007199254740995,"%s":18446744073709551615,"%s":9007199254740997,"%s":0.75,"labels":["owned"],"trackerList":"OWNED_TRACKER_SECRET","errorString":"OWNED_ERROR_SECRET"}]}`, hashKey, ownedDownloaderOtherHash, hashKey, ownedDownloaderHash, sizeKey, uploadKey, downloadedKey, progressKey))
				default:
					unexpected.Add(1)
					http.Error(w, "unexpected RPC", 400)
				}
			}))
			defer server.Close()
			config := a.Settings()
			config.Downloaders = []DownloaderConfig{{Config: download.Config{ID: "owned-tr", Kind: "transmission", BaseURL: server.URL, Username: "owned-user", Password: "OWNED_TR_PASSWORD_SECRET"}}}
			ownedDownloaderSave(t, a, config)
			before := ownedDownloaderChanges(t, a)
			result := ownedDownloaderTasksRead(t, object, ownedDownloaderRequest("owned-tr", ownedDownloaderHash)).Result
			if result.Kind != "transmission" || len(result.Tasks) != 1 || result.Tasks[0].Hash != ownedDownloaderHash || result.Tasks[0].QBittorrent != nil || result.Tasks[0].Transmission == nil {
				t.Fatal("wrong native source projection")
			}
			row := result.Tasks[0].Transmission
			if row.ID == nil || *row.ID != "9007199254740993" || row.UploadedEver == nil || *row.UploadedEver != "18446744073709551615" || row.TotalSize == nil || *row.TotalSize != "9007199254740995" || row.DownloadedEver == nil || *row.DownloadedEver != "9007199254740997" || row.Status == nil || *row.Status != "5" || row.PercentDone == nil || *row.PercentDone != "0.75" {
				t.Fatal("native numbers/status were rounded or business-filtered")
			}
			encoded, _ := json.Marshal(result)
			for _, secret := range []string{"OWNED_TR_PASSWORD_SECRET", "OWNED_TRACKER_SECRET", "OWNED_ERROR_SECRET", server.URL, "unselected"} {
				if bytes.Contains(encoded, []byte(secret)) {
					t.Fatal("private or unselected Transmission field escaped DTO")
				}
			}
			if selected.Load() != 1 || unexpected.Load() != 0 {
				t.Fatal("read dispatched a mutation or wrong selected request")
			}
			ownedDownloaderNoWrites(t, a, before)
		})
	}
}

func TestOriginalDownloaderTasksActualScopeAndCapabilityBoundaries(t *testing.T) {
	a, _, object := ownedDownloaderTasksSDKFixture(t, true)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); _, _ = io.WriteString(w, `[]`) }))
	defer server.Close()
	config := a.Settings()
	config.DefaultDownloader = "owned"
	config.Downloaders = []DownloaderConfig{{Config: download.Config{ID: "owned", Kind: "qbittorrent", BaseURL: server.URL}, Name: "DuplicateName"}}
	ownedDownloaderSave(t, a, config)
	identity := plugins.Identity{PluginID: object.installation.PluginID, InstanceID: object.installation.InstanceID, Version: object.installation.Version}
	callback, closePhase := object.originalBaseSessionHost(context.Background(), "snapshot", nil)
	defer closePhase()
	valid := json.RawMessage(`{"downloader_id":"owned","hashes":[]}`)
	before := ownedDownloaderChanges(t, a)
	for _, raw := range []string{
		`null`, `[]`, `{}`, `{"downloader_id":"owned"}`, `{"downloader_id":"owned","hashes":null}`,
		`{"downloader_id":"","hashes":[]}`, `{"downloader_id":"DuplicateName","hashes":[]}`, `{"downloader_id":"missing","hashes":[]}`,
		`{"downloader_id":"owned","hashes":["all"]}`, `{"downloader_id":"owned","hashes":[],"url":"http://other.invalid"}`,
		`{"downloader_id":"owned","hashes":[],"plugin_id":"other"}`, `{"downloader_id":"owned","hashes":[],"action":"delete"}`,
		`{"downloader_id":"owned","hashes":[]} {}`, strings.Repeat(" ", (128<<10)+1) + string(valid),
	} {
		value, err := callback(context.Background(), identity, plugins.DownloaderTasksReadCapability, json.RawMessage(raw))
		if err == nil || len(value) != 0 {
			t.Fatal("ambiguous/malformed/foreign selector accepted")
		}
	}
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
		if value, err := callback(context.Background(), wrong, plugins.DownloaderTasksReadCapability, valid); err == nil || len(value) != 0 {
			t.Fatal("wrong SDK identity accepted", field)
		}
	}
	for _, method := range []string{"downloader.tasks.add", "downloader.tasks.pause", "downloader.tasks.resume", "downloader.tasks.delete", "downloader.rpc", "http.request", "plugin.notify"} {
		if value, err := callback(context.Background(), identity, method, valid); err == nil || len(value) != 0 {
			t.Fatal("undeclared write/network scope accepted", method)
		}
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if value, err := callback(cancelled, identity, plugins.DownloaderTasksReadCapability, valid); !errors.Is(err, context.Canceled) || len(value) != 0 {
		t.Fatal("cancelled read accepted")
	}
	closePhase()
	if value, err := callback(context.Background(), identity, plugins.DownloaderTasksReadCapability, valid); err == nil || len(value) != 0 {
		t.Fatal("closed actual scope accepted")
	}
	fresh, closeFresh := object.originalBaseSessionHost(context.Background(), "snapshot", nil)
	defer closeFresh()
	if value, err := fresh(context.Background(), identity, plugins.DownloaderTasksReadCapability, valid); err != nil || len(value) == 0 {
		t.Fatal("fresh actual scope failed", err)
	}
	if requests.Load() != 0 {
		t.Fatal("denied/empty selector contacted daemon")
	}
	ownedDownloaderNoWrites(t, a, before)
	for _, contract := range []string{pluginPureSDKContract, pluginBaseSDKContract} {
		if err := validatePluginArtifactContract(ownedDownloaderTasksManifest(true), contract); err == nil {
			t.Fatal("non-resource contract accepted downloader capability")
		}
	}

	deniedApp, _, denied := ownedDownloaderTasksSDKFixture(t, false)
	deniedConfig := deniedApp.Settings()
	deniedConfig.Downloaders = config.Downloaders
	ownedDownloaderSave(t, deniedApp, deniedConfig)
	before = ownedDownloaderChanges(t, deniedApp)
	if _, err := denied.callSession(context.Background(), plugins.Runner{Timeout: 10 * time.Second}, "resource", valid, nil); err == nil {
		t.Fatal("actual undeclared SDK callback accepted")
	}
	ownedDownloaderNoWrites(t, deniedApp, before)
	if requests.Load() != 0 {
		t.Fatal("undeclared capability contacted daemon")
	}
}

func TestOriginalDownloaderTasksActualDispatchRevocation(t *testing.T) {
	for _, mode := range []string{"revoke-before-selected", "cancel-before-selected", "reconfigure-before-selected", "revoke-after-selected"} {
		t.Run(mode, func(t *testing.T) {
			a, _, object := ownedDownloaderTasksSDKFixture(t, true)
			entered, release := make(chan struct{}), make(chan struct{})
			var selected atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/v2/auth/login" {
					if mode != "revoke-after-selected" {
						close(entered)
						<-release
					}
					http.SetCookie(w, &http.Cookie{Name: "SID", Value: "owned", Path: "/"})
					_, _ = io.WriteString(w, "Ok.")
					return
				}
				if r.URL.Path != "/api/v2/torrents/info" {
					t.Error("unexpected endpoint")
					http.Error(w, "unexpected", 400)
					return
				}
				selected.Add(1)
				if mode == "revoke-after-selected" {
					close(entered)
					<-release
				}
				_, _ = io.WriteString(w, `[{"hash":"`+ownedDownloaderHash+`","name":"owned"}]`)
			}))
			defer server.Close()
			config := a.Settings()
			config.Downloaders = []DownloaderConfig{{Config: download.Config{ID: "owned", Kind: "qbittorrent", BaseURL: server.URL, Username: "owned", Password: "owned"}}}
			ownedDownloaderSave(t, a, config)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			callback, revoke := object.originalBaseSessionHost(ctx, "snapshot", nil)
			defer revoke()
			identity := plugins.Identity{PluginID: object.installation.PluginID, InstanceID: object.installation.InstanceID, Version: object.installation.Version}
			type outcome struct {
				raw json.RawMessage
				err error
			}
			done := make(chan outcome, 1)
			before := ownedDownloaderChanges(t, a)
			go func() {
				raw, err := callback(ctx, identity, plugins.DownloaderTasksReadCapability, json.RawMessage(`{"downloader_id":"owned","hashes":["`+ownedDownloaderHash+`"]}`))
				done <- outcome{raw, err}
			}()
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				close(release)
				t.Fatal("owned protocol barrier never entered")
			}
			switch mode {
			case "cancel-before-selected":
				cancel()
			case "reconfigure-before-selected":
				config = a.Settings()
				config.Downloaders[0].Password = "owned-replaced"
				ownedDownloaderSave(t, a, config)
				before = ownedDownloaderChanges(t, a)
			default:
				revoke()
			}
			close(release)
			select {
			case out := <-done:
				if out.err == nil || len(out.raw) != 0 {
					t.Fatal("revoked/cancelled/rebound read returned data")
				}
				if mode == "cancel-before-selected" && !errors.Is(out.err, context.Canceled) {
					t.Fatal("cancellation meaning lost", out.err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("revoked read did not finish")
			}
			want := int32(0)
			if mode == "revoke-after-selected" {
				want = 1
			}
			if selected.Load() != want {
				t.Fatal("live scope was not checked before each dispatch")
			}
			ownedDownloaderNoWrites(t, a, before)
		})
	}
}

func TestOriginalDownloaderTaskProjectionRejectsMalformedAndDuplicateRows(t *testing.T) {
	for _, raw := range []string{`null`, `{}`, `[null]`, `[{"name":"missing hash"}]`, `[{"hash":"all"}]`, `[{"hash":"` + ownedDownloaderHash + `","uploaded":"not a number"}]`, `[{"hash":"` + ownedDownloaderHash + `"},{"hash":"` + ownedDownloaderHash + `"}]`} {
		if out, err := originalDownloaderTaskProjection("qbittorrent", []string{ownedDownloaderHash}, json.RawMessage(raw)); err == nil || out != nil {
			t.Fatal("malformed/ambiguous daemon response accepted")
		}
	}
}

func TestOriginalDownloaderTasksActualSDKProtocolFailureIsNotEmptySuccess(t *testing.T) {
	a, _, object := ownedDownloaderTasksSDKFixture(t, true)
	var mode atomic.Int32
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path != "/api/v2/torrents/info" || r.Method != http.MethodPost {
			t.Error("unexpected protocol request")
			http.Error(w, "unexpected", 400)
			return
		}
		switch mode.Load() {
		case 0:
			http.Error(w, "OWNED_REMOTE_ERROR_SECRET", 500)
		case 1:
			_, _ = io.WriteString(w, `<html>OWNED_LOGIN_SECRET</html>`)
		case 2:
			_, _ = io.WriteString(w, `[{"hash":"`+ownedDownloaderHash+`","uploaded":"9007199254740993"}]`)
		default:
			_, _ = io.WriteString(w, `[]`)
		}
	}))
	defer server.Close()
	config := a.Settings()
	config.Downloaders = []DownloaderConfig{{Config: download.Config{ID: "owned", Kind: "qbittorrent", BaseURL: server.URL}}}
	ownedDownloaderSave(t, a, config)
	raw := json.RawMessage(`{"downloader_id":"owned","hashes":["` + ownedDownloaderHash + `"]}`)
	before := ownedDownloaderChanges(t, a)
	for i := int32(0); i < 3; i++ {
		mode.Store(i)
		result, err := object.callSession(context.Background(), plugins.Runner{Timeout: 10 * time.Second}, "resource", raw, nil)
		if err == nil || len(result.Result) != 0 {
			t.Fatal("failed protocol became successful empty task read")
		}
		if strings.Contains(err.Error(), "OWNED_") || strings.Contains(err.Error(), server.URL) {
			t.Fatal("remote response leaked through error")
		}
	}
	mode.Store(3)
	got := ownedDownloaderTasksRead(t, object, ownedDownloaderRequest("owned", ownedDownloaderHash))
	if got.Result.Tasks == nil || len(got.Result.Tasks) != 0 || requests.Load() != 4 {
		t.Fatal("genuine empty result or read retry count changed")
	}
	ownedDownloaderNoWrites(t, a, before)
}
