package app

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"light-media/internal/download"
)

func TestCleanInvalidSeedInstalledLinuxAMD64WebCommands(t *testing.T) {
	bundle := os.Getenv("SOURCE_CLEAN_INVALID_SEED_PACKAGE_DIR")
	if runtime.GOARCH != "amd64" || bundle == "" {
		t.Skip("requires the indexed Linux amd64 CleanInvalidSeed package")
	}
	t.Setenv("MP_SERVER_HOST", "")
	a, source, _ := importTestApp(t)
	mapped := filepath.Join(source, "mapped")
	const activeName, invalidName, deadName = "Active.mkv", "Invalid.mkv", "Dead-tracker.mkv"
	const orphanName, freshName = "Orphan.mkv", "Fresh.mkv"
	active, orphan, fresh := filepath.Join(mapped, activeName), filepath.Join(mapped, orphanName), filepath.Join(mapped, freshName)
	retained := filepath.Join(source, invalidName)
	old := time.Now().Add(-72 * time.Hour)
	for _, name := range []string{active, orphan, fresh, retained} {
		importWrite(t, name, []byte("owned fixture"))
		if name != fresh {
			if err := os.Chtimes(name, old, old); err != nil {
				t.Fatal(err)
			}
		}
	}
	const remote = "/owned/downloader"
	activeHash, invalidHash, deadHash := strings.Repeat("a", 40), strings.Repeat("b", 40), strings.Repeat("c", 40)
	task := func(hash, name, content string) map[string]any {
		return map[string]any{"hash": hash, "name": name, "content_path": content, "category": "owned",
			"tags": "fixture", "size": 13, "added_on": 1700000000, "state": "uploading"}
	}
	tasks := []map[string]any{
		task(activeHash, activeName, remote+"/"+activeName),
		task(invalidHash, invalidName, "/owned/retained/"+invalidName),
		task(deadHash, deadName, "/owned/retained/"+deadName),
	}
	var listReads, trackerReads, removeCalls, outsideCalls atomic.Int32
	qb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/login":
			_, _ = io.WriteString(w, "Ok.")
		case "/api/v2/app/version":
			_, _ = io.WriteString(w, "v5.0.0")
		case "/api/v2/torrents/info":
			switch r.URL.Query().Get("hashes") {
			case "":
				if r.URL.RawQuery != "" {
					t.Errorf("expected all native tasks, got %q", r.URL.RawQuery)
				}
				listReads.Add(1)
				if removeCalls.Load() == 0 {
					workflowJSON(w, tasks)
				} else {
					workflowJSON(w, []map[string]any{tasks[0], tasks[2]})
				}
			case invalidHash:
				workflowJSON(w, tasks[1:2])
			default:
				t.Errorf("unexpected task snapshot selector %q", r.URL.RawQuery)
				http.NotFound(w, r)
			}
		case "/api/v2/torrents/trackers":
			trackerReads.Add(1)
			host, status, message := "working-tracker.example", 4, "torrent not registered with this tracker"
			switch r.URL.Query().Get("hash") {
			case activeHash:
				status, message = 2, ""
			case invalidHash:
			case deadHash:
				host = "dead-tracker.example"
			default:
				t.Errorf("unexpected tracker hash %q", r.URL.RawQuery)
				http.NotFound(w, r)
				return
			}
			workflowJSON(w, []map[string]any{{"url": "https://" + host + "/announce", "tier": 0, "status": status, "msg": message}})
		case "/api/v2/torrents/delete":
			if r.Method != http.MethodPost || r.ParseForm() != nil || r.Form.Get("hashes") != invalidHash || r.Form.Get("deleteFiles") != "false" {
				t.Error("qB removal did not select exactly the corroborated task while keeping data")
				http.Error(w, "bad remove scope", http.StatusBadRequest)
				return
			}
			removeCalls.Add(1)
		default:
			t.Errorf("unexpected selected qB request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(qb.Close)
	outside := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/auth/login" {
			_, _ = io.WriteString(w, "Ok.")
			return
		}
		outsideCalls.Add(1)
		http.NotFound(w, r)
	}))
	t.Cleanup(outside.Close)
	const selected = "Owned Clean Seed QB"
	a.config.Downloaders = []DownloaderConfig{
		{Config: download.Config{ID: "owned-clean-seed-qb", Kind: "qb", BaseURL: qb.URL}, Name: selected},
		{Config: download.Config{ID: "owned-unselected-qb", Kind: "qb", BaseURL: outside.URL}, Name: "Unselected QB"},
	}
	admin := setupTestAdmin(t, a)
	a.Start(t.Context())
	installMergeSiteSwitchPackage(t, a, admin, bundle, "CleanInvalidSeed")
	api := httptest.NewServer(a.Handler())
	t.Cleanup(api.Close)
	config := mustCleanSeedJSON(map[string]any{
		"enabled": true, "notify": true, "onlyonce": false, "label_only": false,
		"detect_invalid_files": false, "delete_invalid_files": false, "delete_invalid_torrents": false,
		"downloaders": []string{selected}, "download_dirs": mapped + ":" + remote, "min_seeding_days": 1,
	})
	configured := callTest(t, a.Handler(), http.MethodPut, "/api/plugins/CleanInvalidSeed", string(config), admin, "")
	assertOriginalRegistrySuccess(t, configured.Code, configured.Body.Bytes())

	const history = "/api/message/web?count=100"
	const done = "✅ 远程命令执行完成！"
	const begin = "🚀 开始执行远程命令..."
	completed := 0
	send := func(command, noticeTitle string) string {
		t.Helper()
		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, api.URL+"/api/message/web",
			strings.NewReader(string(mustCleanSeedJSON(map[string]string{"text": command}))))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(admin)
		resp, err := api.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		var response struct {
			Data struct{ Accepted, Success bool } `json:"data"`
		}
		err = json.NewDecoder(resp.Body).Decode(&response)
		resp.Body.Close()
		if err != nil || resp.StatusCode != http.StatusOK || !response.Data.Accepted || !response.Data.Success {
			t.Fatalf("web command %q not accepted: %d %+v %v", command, resp.StatusCode, response.Data, err)
		}
		completed++
		var rows []map[string]any
		privateAwait(t, command+" completion notice", func() bool {
			rows = originalWebHistoryRead(t, a, admin, history)
			ends := 0
			for _, row := range rows {
				if row["title"] == done && row["channel"] == "Web" && row["userid"] == "admin" {
					ends++
				}
			}
			return ends == completed
		})
		foundReport := false
		report := ""
		// Newest first. Limit the lookup to notices from this command.
		for _, row := range rows[1:] {
			if row["title"] == begin && row["channel"] == "Web" && row["userid"] == "admin" {
				if foundReport {
					return report
				}
				break
			}
			if row["title"] == noticeTitle {
				foundReport = true
				report, _ = row["text"].(string)
			}
		}
		t.Fatalf("missing ordered %q report and start notice for %s: %v", noticeTitle, command, rows)
		return ""
	}
	requireReport := func(report string, required, absent []string) {
		t.Helper()
		for _, value := range required {
			if !strings.Contains(report, value) {
				t.Errorf("notice is missing %q: %s", value, report)
			}
		}
		for _, value := range absent {
			if strings.Contains(report, value) {
				t.Errorf("notice unexpectedly contains %q: %s", value, report)
			}
		}
	}
	preserved := func(paths ...string) {
		t.Helper()
		for _, path := range paths {
			if got, err := os.ReadFile(path); err != nil || string(got) != "owned fixture" {
				t.Fatalf("fixture data changed: %s %v", path, err)
			}
		}
	}

	// The installed reference source.py requires a second working torrent on
	// the same tracker domain before reporting an explicitly rejected torrent.
	requireReport(send("/detect_invalid_torrents", "🧹 【清理无效做种】"),
		[]string{"检测到 1 个失效做种", invalidName, "torrent not registered with this tracker"}, []string{activeName, deadName, "已删除"})
	requireReport(send("/detect_invalid_files", "🔍 【检测无效源文件 - 发现问题】"),
		[]string{orphanName, "检测到 1 个未做种的无效源文件", "过滤掉 1 个存在时间不足 1 天的文件"}, []string{activeName, freshName, "已删除"})
	preserved(active, orphan, fresh, retained)
	if listReads.Load() < 2 || trackerReads.Load() < 3 || removeCalls.Load() != 0 {
		t.Fatalf("detection did not stay read-only: task lists=%d tracker reads=%d removals=%d", listReads.Load(), trackerReads.Load(), removeCalls.Load())
	}

	requireReport(send("/delete_invalid_files", "🧹 【清理无效源文件 - 已清理】"),
		[]string{orphanName, "已删除无效源文件"}, []string{activeName, freshName})
	if _, err := os.Stat(orphan); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("explicit file command did not remove the exact orphan: %v", err)
	}
	requireReport(send("/delete_invalid_torrents", "🗑️ 【清理无效做种 - 删除结果】"),
		[]string{"已删除 1 个失效种子", invalidName}, []string{activeName, deadName})
	if removeCalls.Load() != 1 || outsideCalls.Load() != 0 {
		t.Fatalf("unexpected downloader effects: removals=%d unselected calls=%d", removeCalls.Load(), outsideCalls.Load())
	}
	preserved(active, fresh, retained)
	t.Log("exact indexed worker handled four authorized Web commands through the running Linux host: source notices, scoped native qB reads, keep-data removal, and isolated orphan cleanup")
}

func TestCleanInvalidSeedInstalledLinuxAMD64TransmissionSchedules(t *testing.T) {
	bundle := os.Getenv("SOURCE_CLEAN_INVALID_SEED_PACKAGE_DIR")
	if runtime.GOARCH != "amd64" || bundle == "" {
		t.Skip("requires the indexed Linux amd64 CleanInvalidSeed package")
	}
	t.Setenv("MP_SERVER_HOST", "")
	t.Setenv("TZ", "UTC")

	const activeName, invalidName = "Active-transmission.mkv", "Invalid-transmission.mkv"
	const label = "owned-invalid"
	activeHash, invalidHash := strings.Repeat("d", 40), strings.Repeat("e", 40)
	makeTask := func(id int, name, hash string, working, labeled bool) map[string]any {
		message := "torrent banned"
		if working {
			// The installed source's native Transmission TrackerStats path
			// requires this exact result together with lastAnnounceSucceeded.
			message = "Success"
		}
		labels := []string{"keep", "already-owned"}
		if labeled {
			labels = append(labels, label)
		}
		return map[string]any{
			"id": id, "name": name, "hashString": hash, "downloadDir": "/owned/transmission",
			"status": 6, "totalSize": 1024, "labels": labels, "addedDate": 1700000000,
			"trackerStats": []map[string]any{{"announce": "https://owned-tracker.example/announce",
				"tier": 0, "hasAnnounced": true, "lastAnnounceSucceeded": working, "lastAnnounceResult": message}},
		}
	}
	var available, labeled atomic.Bool
	var taskLists, labelWrites atomic.Int32
	firstList := make(chan time.Time, 1)
	transmission := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var call struct {
			Method    string
			Tag       uint64
			Arguments struct {
				IDs    []string
				Labels []string
			}
		}
		if r.Method != http.MethodPost || r.URL.Path != "/transmission/rpc" || json.NewDecoder(r.Body).Decode(&call) != nil {
			t.Error("invalid Transmission protocol request")
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		reply := func(arguments map[string]any) {
			workflowJSON(w, map[string]any{"tag": call.Tag, "result": "success", "arguments": arguments})
		}
		switch call.Method {
		case "session_get":
			workflowJSON(w, map[string]any{"result": "method name not recognized", "arguments": map[string]any{}})
		case "session-get":
			reply(map[string]any{"version": "3.00", "rpc-version": 16})
		case "torrent-get":
			invalid := makeTask(2, invalidName, invalidHash, available.Load(), labeled.Load())
			switch {
			case len(call.Arguments.IDs) == 0:
				if taskLists.Add(1) == 1 {
					firstList <- time.Now()
				}
				reply(map[string]any{"torrents": []any{makeTask(1, activeName, activeHash, true, false), invalid}})
			case len(call.Arguments.IDs) == 1 && call.Arguments.IDs[0] == invalidHash:
				reply(map[string]any{"torrents": []any{invalid}})
			default:
				t.Errorf("unexpected Transmission snapshot selectors: %v", call.Arguments.IDs)
				http.Error(w, "unexpected selectors", http.StatusBadRequest)
			}
		case "torrent-set":
			labels := map[string]bool{}
			for _, item := range call.Arguments.Labels {
				labels[item] = true
			}
			if len(call.Arguments.IDs) != 1 || call.Arguments.IDs[0] != invalidHash ||
				len(call.Arguments.Labels) != 3 || !labels["keep"] || !labels["already-owned"] || !labels[label] {
				t.Errorf("Transmission did not merge exactly the existing labels with the configured label: %v", call.Arguments)
				http.Error(w, "wrong label operation", http.StatusBadRequest)
				return
			}
			labelWrites.Add(1)
			labeled.Store(true)
			reply(map[string]any{})
		default:
			t.Errorf("unexpected Transmission method %s", call.Method)
			http.Error(w, "unexpected Transmission method", http.StatusBadRequest)
		}
	}))
	t.Cleanup(transmission.Close)

	a := newTestApp(t)
	const instance, downloaderName = "CleanInvalidSeed", "Owned Transmission for Clean Seed"
	a.config.Downloaders = []DownloaderConfig{
		{Config: download.Config{ID: "owned-clean-seed-tr", Kind: "tr", BaseURL: transmission.URL + "/transmission/rpc"}, Name: downloaderName},
	}
	admin := setupTestAdmin(t, a)
	a.Start(t.Context())
	installMergeSiteSwitchPackage(t, a, admin, bundle, instance)
	api := a.Handler()
	const pluginPath = "/api/plugins/" + instance
	form := callTest(t, api, http.MethodGet, pluginPath+"/form", "", admin, "")
	if form.Code != http.StatusOK || !strings.Contains(form.Body.String(), downloaderName) ||
		!strings.Contains(form.Body.String(), `"model":"downloaders"`) {
		t.Fatalf("live form lacks configured Transmission option: %d %s", form.Code, form.Body)
	}

	const cron = "17 5 1 1 *"
	const schedulePath = "/api/plugins/services"
	configuration := mustCleanSeedJSON(map[string]any{
		"enabled": true, "notify": true, "onlyonce": true, "cron": cron,
		"downloaders": []string{downloaderName}, "label_only": true, "label": label,
		"delete_invalid_torrents": true, "detect_invalid_files": false, "delete_invalid_files": false,
		"more_logs": true, "exclude_categories": "excluded-category", "exclude_labels": "excluded-label",
	})
	started := time.Now()
	saved := callTest(t, api, http.MethodPut, pluginPath, string(configuration), admin, "")
	assertOriginalRegistrySuccess(t, saved.Code, saved.Body.Bytes())
	raw, present, err := a.readOriginalBaseConfigForInit(t.Context(), instance)
	var config struct {
		OnlyOnce  *bool  `json:"onlyonce"`
		Cron      string `json:"cron"`
		LabelOnly bool   `json:"label_only"`
		NotifyAll bool   `json:"notify_all"`
	}
	if err != nil || !present || json.Unmarshal(raw, &config) != nil || config.OnlyOnce == nil ||
		*config.OnlyOnce || config.Cron != cron || !config.LabelOnly {
		t.Fatal("initialization did not persist onlyonce=false while retaining the cron and label-only settings", err)
	}

	// Source init_plugin schedules its private date job three seconds ahead.
	select {
	case first := <-firstList:
		if first.Sub(started) < 3*time.Second {
			t.Fatal("private date service queried Transmission before the source delay")
		}
	case <-time.After(8 * time.Second):
		t.Fatal("private date service never queried Transmission")
	}
	awaitNotice := func(title, fragment string) {
		t.Helper()
		privateAwait(t, title+" "+fragment, func() bool {
			for _, row := range originalWebHistoryRead(t, a, admin, "/api/message/web?count=100") {
				if text, _ := row["text"].(string); row["title"] == title && strings.Contains(text, fragment) {
					return true
				}
			}
			return false
		})
	}
	awaitNotice("🗑️ 【清理无效做种 - 删除结果】", "已标记 1 个失效种子")
	if taskLists.Load() != 1 || labelWrites.Load() != 1 || !labeled.Load() {
		t.Fatalf("private label-only schedule did not perform exactly one Transmission merge: lists=%d labels=%d", taskLists.Load(), labelWrites.Load())
	}
	logs, err := a.readLogs(map[string]string{"module": "plugins", "limit": "100"})
	verbose := false
	for _, log := range logs {
		message, _ := log["message"].(string)
		if strings.Contains(message, "处理 ["+invalidName+"]") && strings.Contains(message, "is_invalid: [True]") {
			verbose = true
			break
		}
	}
	if err != nil || !verbose {
		t.Fatal("the source's optional per-torrent diagnostic did not reach the plugin log", err)
	}

	assertAccepted := func(response *httptest.ResponseRecorder, action string) {
		t.Helper()
		var result struct {
			Data struct{ Success, Accepted bool }
		}
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &result) != nil || !result.Data.Success || !result.Data.Accepted {
			t.Fatalf("%s did not accept the source action: %d %s", action, response.Code, response.Body)
		}
	}
	toggled := callTest(t, api, http.MethodPost, "/api/message/web", `{"text":"/toggle_notify_all"}`, admin, "")
	assertAccepted(toggled, "toggle_notify_all")
	privateAwait(t, "toggle_notify_all notice is addressed to the web user", func() bool {
		for _, row := range originalWebHistoryRead(t, a, admin, "/api/message/web?count=100") {
			if row["title"] == "🔔 已开启全量通知" && row["channel"] == "Web" && row["userid"] == "admin" {
				return true
			}
		}
		return false
	})
	raw, present, err = a.readOriginalBaseConfigForInit(t.Context(), instance)
	if err != nil || !present || json.Unmarshal(raw, &config) != nil || !config.NotifyAll || config.OnlyOnce == nil || *config.OnlyOnce {
		t.Fatal("toggle_notify_all did not persist the changed notification preference", err)
	}

	// Original get_service exposes the configured cron only as the public
	// service. A manual invocation should use the same clean function.
	publicService := func() PluginServiceInfo {
		t.Helper()
		response := callTest(t, api, http.MethodGet, schedulePath, "", admin, "")
		var body struct{ Data []PluginServiceInfo }
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &body) != nil {
			t.Fatalf("public service list unavailable: %d %s", response.Code, response.Body)
		}
		var found PluginServiceInfo
		for _, item := range body.Data {
			if item.InstanceID == instance {
				if found.ID != "" {
					t.Fatal("private date job leaked into the public service list")
				}
				found = item
			}
		}
		return found
	}
	service := publicService()
	if service.ID != instance+"_"+instance || service.ServiceID != instance ||
		service.Name != "清理无效做种" || !service.CanRun || service.NextRun == nil || *service.NextRun == "" {
		t.Fatalf("public cron did not retain source metadata: %+v", service)
	}

	available.Store(true)
	body := mustCleanSeedJSON(map[string]string{"instance_id": instance, "service_id": service.ServiceID})
	run := callTest(t, api, http.MethodPost, schedulePath+"/run", string(body), admin, "")
	assertAccepted(run, "manual public schedule")
	awaitNotice("🗑️ 【清理无效做种 - 删除结果】", "无需标记任何种子")
	awaitNotice("📊 【清理无效做种 - 详细信息】", "所有 tracker 工作正常")
	privateAwait(t, "public cron remains runnable after its manual execution", func() bool {
		item := publicService()
		return item.CanRun && item.NextRun != nil && *item.NextRun != ""
	})
	if taskLists.Load() != 2 || labelWrites.Load() != 1 {
		t.Fatalf("manual service skipped its read or repeated the label write: lists=%d labels=%d", taskLists.Load(), labelWrites.Load())
	}
	for _, row := range originalWebHistoryRead(t, a, admin, "/api/message/web?count=100") {
		if row["title"] == "✅ 远程命令执行完成！" {
			t.Fatal("toggle_notify_all emitted the completion notice despite its original early return")
		}
	}
	t.Log("private date job persisted the cleared flag and merged Transmission labels once; the public cron remained independently runnable and reported recovery without a second mutation")
}

func TestCleanInvalidSeedInstalledLinuxAMD64ScheduleRescansRemovedOrphan(t *testing.T) {
	bundle := os.Getenv("SOURCE_CLEAN_INVALID_SEED_PACKAGE_DIR")
	if runtime.GOARCH != "amd64" || bundle == "" {
		t.Skip("requires the indexed Linux amd64 CleanInvalidSeed package")
	}
	t.Setenv("MP_SERVER_HOST", "")
	a, source, _ := importTestApp(t)
	orphan := filepath.Join(source, "Old-orphan.mkv")
	fresh := filepath.Join(source, "Fresh-control.mkv")
	importWrite(t, orphan, []byte("old owned orphan"))
	importWrite(t, fresh, []byte("fresh owned control"))
	old := time.Now().Add(-72 * time.Hour)
	if err := os.Chtimes(orphan, old, old); err != nil {
		t.Fatal(err)
	}

	var reads [2]atomic.Int32
	names := []string{"Owned first qB", "Owned second qB"}
	for i, name := range names {
		peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.Method == http.MethodPost && r.URL.Path == "/api/v2/auth/login":
				_, _ = io.WriteString(w, "Ok.")
			case r.Method == http.MethodGet && r.URL.Path == "/api/v2/torrents/info" && r.URL.RawQuery == "":
				reads[i].Add(1)
				_, _ = io.WriteString(w, "[]")
			default:
				t.Errorf("unexpected %s request to %s: %s", r.Method, name, r.URL.RequestURI())
				http.NotFound(w, r)
			}
		}))
		t.Cleanup(peer.Close)
		a.config.Downloaders = append(a.config.Downloaders, DownloaderConfig{
			Config: download.Config{ID: strings.ReplaceAll(name, " ", "-"), Kind: "qb", BaseURL: peer.URL},
			Name:   name,
		})
	}

	const instance = "CleanInvalidSeed"
	admin := setupTestAdmin(t, a)
	a.Start(t.Context())
	installMergeSiteSwitchPackage(t, a, admin, bundle, instance)
	api := a.Handler()
	config := mustCleanSeedJSON(map[string]any{
		"enabled": true, "notify": true, "onlyonce": false, "cron": "17 5 1 1 *",
		"downloaders": names, "download_dirs": source + ":/owned/remote-downloads", "min_seeding_days": 1,
		"detect_invalid_files": true, "delete_invalid_files": true, "delete_invalid_torrents": false,
	})
	saved := callTest(t, api, http.MethodPut, "/api/plugins/"+instance, string(config), admin, "")
	assertOriginalRegistrySuccess(t, saved.Code, saved.Body.Bytes())
	const services = "/api/plugins/services"
	runBody := mustCleanSeedJSON(map[string]string{"instance_id": instance, "service_id": instance})
	run := callTest(t, api, http.MethodPost, services+"/run", string(runBody), admin, "")
	var result struct {
		Data struct{ Success, Accepted bool }
	}
	if run.Code != http.StatusOK || json.Unmarshal(run.Body.Bytes(), &result) != nil || !result.Data.Success || !result.Data.Accepted {
		t.Fatalf("manual public service did not accept the source job: %d %s", run.Code, run.Body)
	}

	// The installed source calls detect_invalid_files at the end of EACH
	// downloader's cleanup. The second pass must see the first pass's unlink.
	const cleaned = "🧹 【清理无效源文件 - 已清理】"
	const normal = "✅ 【检测无效源文件 - 一切正常】"
	var fileNotices []map[string]any
	privateAwait(t, "both source file passes finish and the public service returns to waiting", func() bool {
		fileNotices = nil
		for _, row := range originalWebHistoryRead(t, a, admin, "/api/message/web?count=100") {
			if row["title"] == cleaned || row["title"] == normal {
				fileNotices = append(fileNotices, row)
			}
		}
		if len(fileNotices) < 2 {
			return false
		}
		list := callTest(t, api, http.MethodGet, services, "", admin, "")
		var body struct{ Data []PluginServiceInfo }
		if list.Code != http.StatusOK || json.Unmarshal(list.Body.Bytes(), &body) != nil {
			t.Fatalf("public service list unavailable: %d %s", list.Code, list.Body)
		}
		for _, item := range body.Data {
			if item.InstanceID == instance && item.ServiceID == instance {
				return item.CanRun
			}
		}
		return false
	})
	if len(fileNotices) != 2 || fileNotices[0]["title"] != normal || fileNotices[1]["title"] != cleaned {
		t.Fatalf("expected one cleanup followed by one clean rescan: %v", fileNotices)
	}
	normalText, _ := fileNotices[0]["text"].(string)
	cleanedText, _ := fileNotices[1]["text"].(string)
	if !strings.Contains(normalText, "未发现无效源文件") || strings.Contains(normalText, filepath.Base(orphan)) ||
		!strings.Contains(cleanedText, filepath.Base(orphan)) || !strings.Contains(cleanedText, "检测到 1 个未做种的无效源文件") ||
		strings.Contains(cleanedText, filepath.Base(fresh)) {
		t.Fatalf("unexpected source file notices: first=%q second=%q", cleanedText, normalText)
	}
	if _, err := os.Stat(orphan); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the first pass did not remove its only orphan: %v", err)
	}
	if data, err := os.ReadFile(fresh); err != nil || string(data) != "fresh owned control" {
		t.Fatalf("cleanup affected the newer file: %v", err)
	}
	if reads[0].Load() < 3 || reads[1].Load() < 3 {
		t.Fatalf("both source passes should query both selected inventories: %d %d", reads[0].Load(), reads[1].Load())
	}
	var effects, confirmed int
	err := a.Store.DB.QueryRowContext(t.Context(), `SELECT COUNT(*), COALESCE(SUM(CAST(payload AS TEXT)='"confirmed"'),0)
		FROM records WHERE kind='plugin_clean_seed_effect'`).Scan(&effects, &confirmed)
	if err != nil || effects != 1 || confirmed != 1 {
		t.Fatalf("expected one confirmed local cleanup effect, got %d/%d: %v", confirmed, effects, err)
	}
	t.Log("one public service run removed the old orphan during the first downloader pass and reported a clean second pass against both isolated qB inventories")
}
