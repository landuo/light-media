package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"light-media/internal/download"
	"light-media/internal/plugins"
	"light-media/internal/plugins/builtin"
	iyuu "light-media/internal/plugins/iyuuautoseed"
	"light-media/internal/plugins/uninstall"
	"light-media/internal/sites"
)

func TestIYUUActualSDKWorker(t *testing.T) {
	for _, arg := range os.Args {
		if arg == "--iyuu-owned-uninstall-worker" {
			if plugins.Serve(context.Background(), os.Stdin, os.Stdout, uninstall.Manifest(), uninstall.Handle) != nil {
				os.Exit(3)
			}
			os.Exit(0)
		}
		if arg == "--iyuu-owned-worker" {
			if plugins.Serve(context.Background(), os.Stdin, os.Stdout, iyuu.Manifest(), iyuu.Handle) != nil {
				os.Exit(3)
			}
			os.Exit(0)
		}
	}
}
func invokeIYUUActualSDK(t *testing.T, a *App, run PluginRun) PluginRun {
	got, e := invokeIYUUActualSDKOutcome(t, a, run, true)
	if e != nil {
		t.Fatal("actual deferred owner", e)
	}
	return got
}
func prepareIYUUActualSDK(t *testing.T, a *App, run PluginRun) *pluginDeferredCall {
	t.Helper()
	ctx := context.Background()
	in, e := a.pluginStore.Installation(ctx, run.InstanceID)
	if e != nil {
		t.Fatal(e)
	}
	d, e := a.preparePluginDeferred(ctx, in, run)
	if e != nil || d == nil {
		t.Fatal("actual deferred admission", e)
	}
	executable, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	a.pluginProcess <- struct{}{}
	manifest := iyuu.Manifest()
	flag := "--iyuu-owned-worker"
	if in.PluginID == "PluginUnInstall" {
		manifest = uninstall.Manifest()
		flag = "--iyuu-owned-uninstall-worker"
	}
	worker, e := (plugins.Runner{Timeout: 20 * time.Second}).Run(ctx, plugins.Command{Path: executable, Args: []string{"-test.run=^TestIYUUActualSDKWorker$", "--", flag}}, manifest, run.InstanceID, "invoke", run.Input, d.handshake)
	<-a.pluginProcess
	if e != nil {
		t.Fatal("actual SDK child", e)
	}
	if e = d.ready(worker); e != nil {
		t.Fatal(e)
	}
	return d
}
func invokeIYUUActualSDKOutcome(t *testing.T, a *App, run PluginRun, finish bool) (PluginRun, error) {
	t.Helper()
	ctx := context.Background()
	d := prepareIYUUActualSDK(t, a, run)
	result, e := d.execute(ctx, &run)
	if e != nil {
		return run, e
	}
	if !finish {
		return run, nil
	}
	if e = a.pluginStore.FinishRun(ctx, run.ID, "succeeded", "", result); e != nil {
		t.Fatal(e)
	}
	got, e := a.pluginStore.Run(ctx, run.ID)
	if e != nil {
		t.Fatal(e)
	}
	return got, nil
}

func TestIYUUActualAppSDKConfigScheduleAndNativeHTTP(t *testing.T) {
	for _, kind := range []string{"qbittorrent", "transmission"} {
		t.Run(kind, func(t *testing.T) { testIYUUActualAppSDK(t, kind, false) })
	}
}
func TestIYUUActualAppSDKUnknownBlocksLaterRun(t *testing.T) {
	for _, kind := range []string{"qbittorrent", "transmission"} {
		t.Run(kind, func(t *testing.T) { testIYUUActualAppSDK(t, kind, true) })
	}
}

func TestIYUUAlreadyClaimedAndPreparedWorkCannotCrossLaterUnknown(t *testing.T) {
	for _, kind := range []string{"qbittorrent", "transmission"} {
		for _, stage := range []string{"claimed", "prepared"} {
			t.Run(kind+"/"+stage, func(t *testing.T) { testIYUUActualAppSDK(t, kind, true, stage) })
		}
	}
}

func TestIYUUActualAppSDKDownloaderAPIKeyAndOptions(t *testing.T) {
	testIYUUActualAppSDK(t, "qbittorrent", false, "key-options")
}

func TestIYUUActualAppSDKDaemonDefaultDirectories(t *testing.T) {
	for _, kind := range []string{"qbittorrent", "transmission"} {
		t.Run(kind+"/nil", func(t *testing.T) { testIYUUActualAppSDK(t, kind, false, "directory-nil") })
		t.Run(kind+"/observed-explicit-outside-app-roots", func(t *testing.T) { testIYUUActualAppSDK(t, kind, false, "directory-explicit") })
	}
	t.Run("qbittorrent/empty", func(t *testing.T) { testIYUUActualAppSDK(t, "qbittorrent", false, "directory-empty") })
}

func TestIYUUActualAppSDKDaemonDirectoryChangeAndLostACK(t *testing.T) {
	for _, kind := range []string{"qbittorrent", "transmission"} {
		for _, stage := range []string{"directory-before-change", "directory-after-change", "directory-lost-add"} {
			t.Run(kind+"/"+stage, func(t *testing.T) { testIYUUActualAppSDK(t, kind, false, stage) })
		}
	}
}

func testIYUUActualAppSDK(t *testing.T, kind string, lostACK bool, stages ...string) {
	stage := "pending"
	if len(stages) > 0 {
		stage = stages[0]
	}
	directoryMode := ""
	if strings.HasPrefix(stage, "directory-") {
		directoryMode = stage
		stage = "pending"
	}
	effectArrived, allowEffect := make(chan struct{}, 1), make(chan struct{})
	var allowOnce sync.Once
	releaseEffect := func() { allowOnce.Do(func() { close(allowEffect) }) }
	defer releaseEffect()
	a := newTestApp(t)
	ctx := context.Background()
	original := strings.Repeat("a", 40)
	seed := strings.Repeat("b", 40)
	observed := strings.Repeat("c", 40)
	var mu sync.Mutex
	temp := ""
	added := false
	calls := map[string]int{}
	sourcePath := filepath.Join(a.Dir, "owned-source")
	if directoryMode != "" {
		sourcePath = "/daemon/owned-default"
	}
	var sourceDirectory any = sourcePath
	if directoryMode != "" && directoryMode != "directory-empty" && directoryMode != "directory-explicit" {
		sourceDirectory = nil
	} else if directoryMode == "directory-empty" {
		sourceDirectory = ""
	}
	sourceWire := sourcePath
	if directoryMode != "" && directoryMode != "directory-explicit" {
		sourceWire = ""
	}
	actualTaskPath := sourcePath
	if stage == "key-options" {
		actualTaskPath = "/daemon/auto-movie"
	}
	destinationReads := 0
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		calls[r.URL.Path]++
		r.ParseForm()
		if stage == "key-options" && strings.HasPrefix(r.URL.Path, "/api/v2/") && r.Header.Get("Authorization") != "Bearer owned-iyuu-api-key" {
			t.Error("actual App did not bind API key to native downloader")
		}
		switch r.URL.Path {
		case "/transmission/rpc":
			var envelope map[string]any
			if json.NewDecoder(r.Body).Decode(&envelope) != nil {
				t.Error("actual TR envelope")
				return
			}
			method, _ := envelope["method"].(string)
			calls["tr:"+method]++
			args, _ := envelope["params"].(map[string]any)
			respond := func(v any) {
				json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": envelope["id"], "result": v})
			}
			switch method {
			case "session_get":
				directory := sourcePath
				if args["fields"] == nil {
					destinationReads++
					if directoryMode == "directory-before-change" && destinationReads > 1 {
						directory = "/daemon/changed"
					}
				}
				respond(map[string]any{"version": "4.1", "download_dir": directory})
			case "torrent_get":
				ids, _ := args["ids"].([]any)
				hash, status := original, 6
				if len(ids) > 0 {
					if ids[0] == seed {
						respond(map[string]any{"torrents": []any{}})
						return
					}
					hash, status = observed, 0
				}
				directory := any(actualTaskPath)
				if hash == original {
					directory = sourceDirectory
				}
				respond(map[string]any{"torrents": []any{map[string]any{"hash_string": hash, "download_dir": directory, "labels": []string{"original"}, "status": status, "percent_done": 1, "total_size": 1073741824}}})
			case "torrent_add":
				if args["download_dir"] != sourceDirectory || args["paused"] != true {
					t.Error("actual TR paused/save path contract")
				}
				added = true
				if directoryMode == "directory-after-change" {
					actualTaskPath = "/daemon/changed"
				}
				if directoryMode == "directory-lost-add" {
					connection, _, err := w.(http.Hijacker).Hijack()
					if err == nil {
						connection.Close()
					}
					return
				}
				respond(map[string]any{"torrent_added": map[string]any{"hash_string": observed}})
			case "torrent_start":
				ids, _ := args["ids"].([]any)
				if len(ids) != 1 || ids[0] != observed {
					t.Error("TR start did not use added identity")
				}
				if lostACK {
					if stage == "prepared" {
						effectArrived <- struct{}{}
						select {
						case <-allowEffect:
						case <-r.Context().Done():
							return
						}
					}
					conn, _, e := w.(http.Hijacker).Hijack()
					if e == nil {
						conn.Close()
					}
					return
				}
				respond(map[string]any{})
			default:
				t.Errorf("unexpected TR method %s", method)
			}
		case "/api/v2/torrents/categories":
			json.NewEncoder(w).Encode(map[string]any{"movie": map[string]any{"savePath": actualTaskPath}})
		case "/api/v2/app/preferences":
			directory := sourcePath
			destinationReads++
			if directoryMode == "directory-before-change" && destinationReads > 1 {
				directory = "/daemon/changed"
			}
			json.NewEncoder(w).Encode(map[string]any{"save_path": directory})
		case "/api/v2/app/webapiVersion":
			io.WriteString(w, "2.11.0")
		case "/api/v2/torrents/info":
			if r.Form.Get("filter") == "completed" {
				json.NewEncoder(w).Encode([]any{map[string]any{"hash": original, "save_path": sourceDirectory, "tags": "original", "category": "movie", "state": "pausedUP", "progress": 1, "amount_left": 0, "total_size": 1073741824}})
			} else if r.Form.Get("hashes") == seed {
				io.WriteString(w, "[]")
			} else if added {
				category := ""
				auto := false
				if stage == "key-options" {
					category = "movie"
					auto = true
				}
				json.NewEncoder(w).Encode([]any{map[string]any{"hash": observed, "save_path": actualTaskPath, "category": category, "auto_tmm": auto, "tags": temp, "state": "pausedUP", "progress": 1, "amount_left": 0}})
			} else {
				io.WriteString(w, "[]")
			}
		case "/api/v2/torrents/add":
			r.ParseMultipartForm(1 << 20)
			tags := strings.Split(r.FormValue("tags"), ",")
			temp = tags[len(tags)-1]
			expectedAuto := "False"
			if stage == "key-options" {
				expectedAuto = "True"
				if r.FormValue("sequentialDownload") != "True" || r.FormValue("firstLastPiecePrio") != "True" || r.FormValue("category") != "movie" {
					t.Error("actual App lost downloader options")
				}
			}
			if r.FormValue("savepath") != sourceWire || r.FormValue("autoTMM") != expectedAuto {
				t.Error("actual App changed source savepath")
			}
			added = true
			if directoryMode == "directory-after-change" {
				actualTaskPath = "/daemon/changed"
			}
			if directoryMode == "directory-lost-add" {
				connection, _, err := w.(http.Hijacker).Hijack()
				if err == nil {
					connection.Close()
				}
				return
			}
			io.WriteString(w, "Ok.")
		case "/api/v2/torrents/removeTags", "/api/v2/torrents/recheck", "/api/v2/torrents/start":
			if r.Form.Get("hashes") != observed {
				t.Error("actual App effects did not use observed addition identity")
			}
			if lostACK && r.URL.Path == "/api/v2/torrents/removeTags" {
				if stage == "prepared" {
					effectArrived <- struct{}{}
					select {
					case <-allowEffect:
					case <-r.Context().Done():
						return
					}
				}
				conn, _, e := w.(http.Hijacker).Hijack()
				if e == nil {
					conn.Close()
				}
				return
			}
		case "/reseed/sites/index":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"sites": []any{map[string]any{"id": 1, "base_url": server.URL + "/", "download_page": "download.php?id={id}"}}}})
		case "/reseed/sites/reportExisting":
			io.WriteString(w, `{"code":0,"data":{"sid_sha1":"synthetic"}}`)
		case "/reseed/index/index":
			json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{original: map[string]any{"torrent": []any{map[string]any{"sid": 1, "torrent_id": 7, "info_hash": seed}}}}})
		case "/download.php":
			if r.Header.Get("Cookie") != "owned=synthetic" {
				t.Error("canonical site binding lost cookie")
			}
			io.WriteString(w, "d4:infod6:lengthi1e4:name9:owned.mkvee")
		default:
			t.Errorf("unexpected actual App route %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	a.config = Settings{Downloaders: []DownloaderConfig{{Config: download.Config{ID: "owned-qb", Kind: kind, BaseURL: server.URL}, Name: "Owned qB"}}, Sites: []SiteConfig{{Config: sites.Config{ID: "owned-site", Name: "Owned site", Kind: "hhanclub", BaseURL: server.URL, Cookie: "owned=synthetic"}, Enabled: true}}, AllowedRoots: []string{a.Dir}}
	if stage == "key-options" {
		a.config.Downloaders[0].APIKey = "owned-iyuu-api-key"
		a.config.Downloaders[0].AutoCategory = true
		a.config.Downloaders[0].Sequential = true
		a.config.Downloaders[0].FirstLast = true
	}
	if e := a.saveSettings(ctx, a.Settings()); e != nil {
		t.Fatal("persist actual native fixture settings", e)
	}
	if e := a.bootstrapNativeSiteIdentities(ctx); e != nil {
		t.Fatal(e)
	}
	a.pluginIYUU.loopbackAPI = server.URL
	a.pluginIYUU.pollSleep = func(context.Context, time.Duration) error { return nil }
	a.pluginStore.notices.send = func(context.Context, pluginNoticeQueueEntry) (int, error) { return 1, nil }
	config := map[string]any{"enabled": true, "onlyonce": false, "clearcache": false, "cron": "0 0 * * *", "downloaders": []any{"Owned qB"}, "sites": []any{json.Number("1")}, "token": "owned-synthetic-token", "size": json.Number("0"), "reuse_save_path": true, "labelsafterseed": "owned", "auto_start": true, "notify": true}
	if stage == "key-options" {
		config["reuse_save_path"] = false
		config["categoryafterseed"] = "movie"
	}
	run := uninstallInstall(t, a, "OwnedIYUU", iyuu.Manifest(), config, true)
	if _, _, found := builtin.Lookup("IYUUAutoSeed"); !found {
		t.Fatal("owned catalog overlay missing")
	}
	run = invokeIYUUActualSDK(t, a, run)
	if run.State != "succeeded" {
		t.Fatal(run.State)
	}
	var schedules int
	if e := a.Store.DB.QueryRow("SELECT COUNT(*) FROM plugin_schedules WHERE instance_id='OwnedIYUU' AND active=1").Scan(&schedules); e != nil || schedules != 2 {
		t.Fatalf("persistent schedules %d %v", schedules, e)
	}
	queued, e := a.pluginStore.EnqueueRun(ctx, "OwnedIYUU", "run", nil)
	if e != nil {
		t.Fatal(e)
	}
	var second PluginRun
	var secondDeferred *pluginDeferredCall
	if lostACK {
		// Both admissions predate the unknown outcome. A later claim still
		// requires a current safe owner, even though enqueue was valid then.
		if second, e = a.pluginStore.EnqueueRun(ctx, "OwnedIYUU", "run", nil); e != nil {
			t.Fatal("second original queue admission", e)
		}
	}
	run, e = a.pluginStore.claimRunForInstance(ctx, queued.InstanceID)
	if e != nil {
		t.Fatal(e)
	}
	if directoryMode == "directory-before-change" || directoryMode == "directory-after-change" || directoryMode == "directory-lost-add" {
		run, e = invokeIYUUActualSDKOutcome(t, a, run, directoryMode == "directory-before-change")
		if directoryMode == "directory-before-change" {
			if e != nil || run.State != "succeeded" {
				t.Fatal("known-undispatched directory change", run.State, e)
			}
		} else {
			if directoryMode == "directory-lost-add" && !errors.Is(e, iyuu.ErrUnconfirmed) {
				t.Fatal("lost add acknowledgement", e)
			}
			if directoryMode == "directory-after-change" && (e == nil || !strings.Contains(e.Error(), "destination changed")) {
				t.Fatal("unobserved daemon destination continued", e)
			}
			if e = a.pluginStore.FinishRun(ctx, run.ID, "failed", "OWNED_DESTINATION_RECONCILE", nil); e != nil {
				t.Fatal(e)
			}
			iyuuAssertUnknownAdmissionsWarmCold(t, a, run)
		}
		mu.Lock()
		defer mu.Unlock()
		addKey := "/api/v2/torrents/add"
		wantAdds := 1
		if kind == "transmission" {
			addKey = "tr:torrent_add"
		}
		if directoryMode == "directory-before-change" {
			wantAdds = 0
		}
		if calls[addKey] != wantAdds {
			t.Fatal("directory mutation repeated or dispatched", calls[addKey], wantAdds)
		}
		for _, key := range []string{"/api/v2/torrents/removeTags", "/api/v2/torrents/recheck", "/api/v2/torrents/start", "tr:torrent_start", "tr:torrent_verify"} {
			if calls[key] != 0 {
				t.Fatal("controls followed unconfirmed destination", key, calls[key])
			}
		}
		return
	}
	if lostACK {
		if stage != "pending" {
			second, e = a.pluginStore.claimRunForInstance(ctx, second.InstanceID)
			if e != nil {
				t.Fatal(e)
			}
			if stage == "prepared" {
				secondDeferred = prepareIYUUActualSDK(t, a, second)
			}
		}
		var secondOutcome error
		if stage == "prepared" {
			firstDeferred := prepareIYUUActualSDK(t, a, run)
			firstDone, secondDone := make(chan error, 1), make(chan error, 1)
			go func() { _, err := firstDeferred.execute(ctx, &run); firstDone <- err }()
			select {
			case <-effectArrived:
			case <-time.After(5 * time.Second):
				t.Fatal("first owned native effect did not arrive")
			}
			go func() { _, err := secondDeferred.execute(ctx, &second); secondDone <- err }()
			select {
			case err := <-secondDone:
				t.Fatal("second prepared did not wait for actual owner", err)
			case <-time.After(75 * time.Millisecond):
			}
			waiting, _, err := a.pluginStore.readDeferred(ctx, a.Store.DB, second)
			if err != nil || waiting.Phase != "prepared" {
				t.Fatal("second entered started while first holds owner", waiting.Phase, err)
			}
			releaseEffect()
			select {
			case e = <-firstDone:
			case <-time.After(5 * time.Second):
				t.Fatal("first native outcome timeout")
			}
			select {
			case secondOutcome = <-secondDone:
			case <-time.After(5 * time.Second):
				t.Fatal("second owner waiter timeout")
			}
		} else {
			run, e = invokeIYUUActualSDKOutcome(t, a, run, false)
		}
		if !errors.Is(e, iyuu.ErrUnconfirmed) {
			t.Fatal("lost native ACK did not remain unknown", e)
		}
		// An operator may acknowledge the failed App task. That cannot manufacture
		// a native receipt or permit a new source timeline to repeat the effect.
		if e = a.pluginStore.FinishRun(ctx, run.ID, "failed", "IYUU_OWNED_UNKNOWN", nil); e != nil {
			t.Fatal(e)
		}
		if stage != "pending" {
			in, e := a.pluginStore.Installation(ctx, second.InstanceID)
			if e != nil {
				t.Fatal(e)
			}
			if stage == "claimed" {
				_, e = a.preparePluginDeferred(ctx, in, second)
			} else {
				e = secondOutcome
			}
			if !errors.Is(e, iyuu.ErrUnconfirmed) {
				t.Fatal("previously claimed/prepared crossed unknown", stage, e)
			}
			record, _, readErr := a.pluginStore.readDeferred(ctx, a.Store.DB, second)
			if stage == "claimed" && !errors.Is(readErr, sql.ErrNoRows) {
				t.Fatal("blocked preparation created receipt", record.Phase, readErr)
			}
			if stage == "prepared" && (readErr != nil || record.Phase != "prepared") {
				t.Fatal("blocked start changed prepared receipt", record.Phase, readErr)
			}
			if e = a.pluginStore.FinishRun(ctx, second.ID, "failed", "IYUU_UNCONFIRMED", nil); e != nil {
				t.Fatal(e)
			}
		}
		iyuuAssertUnknownDoesNotHideMissingRootFact(t, a, run)
		iyuuAssertUnknownConfigWritersBlocked(t, a, run.InstanceID)
		iyuuAssertUnknownAdmissionsWarmCold(t, a, run)

		mu.Lock()
		defer mu.Unlock()
		key := "/api/v2/torrents/removeTags"
		if kind == "transmission" {
			key = "tr:torrent_start"
		}
		if calls[key] != 1 {
			t.Fatal("unknown native effect repeated", key, calls[key])
		}
		return
	}
	// The real final config/checkpoint transaction succeeds, then the actual
	// deferred call loses its final acknowledgement. Cold recovery must finish
	// from the original immutable CP without replaying any HTTP/native effect.
	a.pluginIYUU.afterCheckpoint = func() error { return errors.New("owned lost final checkpoint acknowledgement") }
	run, e = invokeIYUUActualSDKOutcome(t, a, run, false)
	if e == nil {
		t.Fatal("missing owned final ACK failure")
	}
	t.Log("owned execution interrupted", e)
	a.pluginIYUU.afterCheckpoint = nil
	dir := a.Dir
	if e = a.Close(); e != nil {
		t.Fatal(e)
	}
	t.Setenv("TZ", "UTC") // old source notice wall time must remain unchanged
	a, e = New(Options{DataDir: dir})
	if e != nil {
		t.Fatal("actual App close/reopen recovery", e)
	}
	t.Cleanup(func() { _ = a.Close() })
	run, e = a.pluginStore.Run(ctx, run.ID)
	if e != nil || run.State != "succeeded" {
		t.Fatal("cold final CP not recovered", run.State, e)
	}
	installation, e := a.pluginStore.Installation(ctx, "OwnedIYUU")
	if e != nil {
		t.Fatal(e)
	}
	owner, e := a.iyuuOwner(ctx, installation)
	if e != nil {
		t.Fatal(e)
	}
	checkpoint, found, e := owner.journal.ReadCheckpoint(ctx, "OwnedIYUU", uint64(run.Generation))
	if e != nil || !found || checkpoint.Core.Counters.Success != 1 {
		t.Fatalf("actual durable runtime %+v %v", checkpoint, e)
	}
	stored, e := a.pluginStore.Config(ctx, "OwnedIYUU")
	if e != nil {
		t.Fatal(e)
	}
	fields, e := iyuuSourceMap(stored.Value)
	if e != nil {
		t.Fatal(e)
	}
	if len(fields["success_caches"].([]any)) != 1 {
		t.Fatal("source cache did not reach actual plugin config")
	}
	history, e := a.pluginStore.Data(ctx, "OwnedIYUU", original)
	if e != nil || !strings.Contains(string(history.Value), seed) {
		t.Fatalf("source history did not reach actual plugin data: %s %v", history.Value, e)
	}
	var routeID string
	if e = a.Store.DB.QueryRow(`SELECT id FROM plugin_notice_routes WHERE instance_id='OwnedIYUU' AND source_token IN (SELECT token FROM plugin_iyuu_notices)`).Scan(&routeID); e != nil {
		t.Fatal("actual native IYUU notice not accepted", e)
	}
	route, _, e := a.pluginStore.readNoticeRoute(ctx, a.Store.DB, routeID)
	if e != nil || !route.Complete || route.HistoryID == "" {
		t.Fatal("actual native notice routing/history", route, e)
	}
	iyuuAssertActualBusinessSourceLogs(t, a, run)
	mu.Lock()
	defer mu.Unlock()
	routes := []string{"/api/v2/torrents/add", "/api/v2/torrents/removeTags", "/api/v2/torrents/recheck", "/api/v2/torrents/start", "/download.php"}
	if kind == "transmission" {
		routes = []string{"tr:torrent_add", "tr:torrent_start", "/download.php"}
	}
	for _, route := range routes {
		if calls[route] != 1 {
			t.Errorf("actual route %s calls%d", route, calls[route])
		}
	}
}

func TestIYUUActualNativeSiteDeletedConsumer(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("site deletion consumer performed unexpected network IO")
	}))
	defer server.Close()
	a.config = Settings{Sites: []SiteConfig{{Config: sites.Config{ID: "owned-site", Name: "Owned site", Kind: "hhanclub", BaseURL: server.URL}, Enabled: true}}}
	if e := a.bootstrapNativeSiteIdentities(ctx); e != nil {
		t.Fatal(e)
	}
	identity, e := a.Store.nativeSiteByAppID(ctx, "owned-site")
	if e != nil {
		t.Fatal(e)
	}
	config := map[string]any{"enabled": true, "cron": "0 0 * * *", "onlyonce": false, "clearcache": false, "downloaders": []any{"Owned qB"}, "sites": []any{json.Number(itoaSourceSite(identity.SourceID))}, "token": "owned-synthetic-token", "size": json.Number("0")}
	run := uninstallInstall(t, a, "OwnedIYUU", iyuu.Manifest(), config, true)
	invokeIYUUActualSDK(t, a, run)
	if _, e = a.deleteManagedSite(ctx, "owned-site"); e != nil {
		t.Fatal(e)
	}
	if e = a.dispatchPluginEvents(ctx, time.Now().Add(time.Second)); e != nil {
		t.Fatal(e)
	}
	run, e = a.pluginStore.claimRunForInstance(ctx, "OwnedIYUU")
	if e != nil {
		t.Fatal(e)
	}
	if run.Action != "event" {
		t.Fatal("not actual domain event", run.Action)
	}
	invokeIYUUActualSDK(t, a, run)
	stored, e := a.pluginStore.Config(ctx, "OwnedIYUU")
	if e != nil {
		t.Fatal(e)
	}
	fields, e := iyuuSourceMap(stored.Value)
	if e != nil {
		t.Fatal(e)
	}
	if fields["enabled"] != false || len(fields["sites"].([]any)) != 0 {
		t.Fatalf("native source deletion not applied %+v", fields)
	}
}
