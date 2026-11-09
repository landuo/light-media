package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"light-media/internal/download"
	"light-media/internal/sites"
)

func TestBrushFlowInstalledAMD64LongPass(t *testing.T) {
	bundle := os.Getenv("SOURCE_BRUSH_FLOW_PACKAGE_DIR")
	if runtime.GOARCH != "amd64" || bundle == "" {
		t.Skip("requires the installed BrushFlow amd64 package in the owned fixture")
	}
	t.Setenv("MP_SERVER_HOST", "")
	t.Setenv("TZ", "Asia/Shanghai")
	a := newTestApp(t)
	cookie := setupTestAdmin(t, a)
	downloadRoot := t.TempDir()

	// A source task consumes the complete returned list. Six independent
	// detail pages exceed both the old 64-callback ceiling and its cumulative
	// 64 MiB wire allowance. Each individual page is below the host cap.
	const count = 6
	const detailBytes = 9 << 20
	type candidate struct {
		id, title, hash string
		material        []byte
	}
	candidates := make([]candidate, 0, count)
	byID, byHash := map[string]candidate{}, map[string]candidate{}
	var listed strings.Builder
	var expected []string
	for i := range count {
		id := strconv.Itoa(101 + i)
		title := "Owned promotion release " + id
		material, hash := workflowTorrent("owned-long-pass-" + id + ".mkv")
		v := candidate{id, title, hash, material}
		candidates = append(candidates, v)
		byID[id], byHash[hash] = v, v
		expected = append(expected, "add:"+hash, "start:"+hash)
		html := strings.ReplaceAll(workflowHTML(title, "", true), "?id=101", "?id="+id)
		if i == 0 {
			listed.WriteString(html[:strings.Index(html, "<tr><td>类别")])
		}
		listed.WriteString(strings.TrimSuffix(html[strings.Index(html, "<tr><td>类别"):], "</table>"))
	}
	listed.WriteString("</table>")
	listing := listed.String()
	tail := strings.Repeat("x", detailBytes)

	var state struct {
		sync.Mutex
		taskID  string
		samples int
		pages   int
		tags    map[string]string
		started map[string]bool
		order   []string
	}
	state.tags, state.started = map[string]string{}, map[string]bool{}
	tracker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/torrents.php":
			_, _ = io.WriteString(w, listing)
		case "/details.php", "/download.php":
			item, ok := byID[r.URL.Query().Get("id")]
			if !ok {
				http.NotFound(w, r)
				return
			}
			if r.URL.Path == "/download.php" {
				w.Header().Set("Content-Type", "application/x-bittorrent")
				_, _ = w.Write(item.material)
				return
			}
			state.Lock()
			state.pages++
			state.Unlock()
			end := time.Now().In(time.FixedZone("UTC+8", 8*3600)).Add(2 * time.Hour).Format(time.DateTime)
			fmt.Fprintf(w, `<a href="download.php?id=%s">下载</a><img class="pro_free">剩余时间<span title="%s">2h</span><div id="kothercopy">`, item.id, end)
			_, _ = io.WriteString(w, tail)
			_, _ = io.WriteString(w, "</div>")
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(tracker.Close)
	qb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		state.Lock()
		defer state.Unlock()
		switch r.URL.Path {
		case "/api/v2/auth/login":
			http.SetCookie(w, &http.Cookie{Name: "SID", Value: "owned-brush-stream", Path: "/"})
		case "/api/v2/app/version":
			_, _ = io.WriteString(w, "v5.0.5")
			return
		case "/api/v2/app/webapiVersion":
			_, _ = io.WriteString(w, "2.11.4")
			return
		case "/api/v2/app/preferences":
			workflowJSON(w, map[string]any{"save_path": downloadRoot, "auto_tmm_enabled": false})
			return
		case "/api/v2/torrents/categories":
			workflowJSON(w, map[string]any{})
			return
		case "/api/v2/transfer/info":
			state.samples++
			workflowJSON(w, map[string]int{"up_info_speed": 512, "dl_info_speed": 1024})
			return
		case "/api/v2/torrents/tags":
			workflowJSON(w, []string{})
			return
		case "/api/v2/torrents/info":
			selection := r.FormValue("hashes")
			rows := []any{}
			for _, item := range candidates {
				tags, exists := state.tags[item.hash]
				if !exists || selection != "" && !slices.Contains(strings.Split(selection, "|"), item.hash) {
					continue
				}
				task := workflowTask(item.hash, 0)
				task["name"], task["save_path"], task["tags"] = item.title, downloadRoot, tags
				if state.started[item.hash] {
					task["state"] = "downloading"
				}
				rows = append(rows, task)
			}
			workflowJSON(w, rows)
			return
		case "/api/v2/torrents/add":
			file, _, err := r.FormFile("torrent__0")
			if err != nil {
				t.Error("owned peer did not receive torrent bytes", err)
				break
			}
			body, err := io.ReadAll(file)
			_ = file.Close()
			var hash string
			for _, item := range candidates {
				if bytes.Equal(item.material, body) {
					hash = item.hash
					break
				}
			}
			if err != nil || hash == "" || state.tags[hash] != "" || r.FormValue("stopped") != "True" || r.FormValue("paused") != "True" {
				t.Error("source protected Add was duplicated, malformed, or started early")
				break
			}
			state.tags[hash] = r.FormValue("tags")
			state.order = append(state.order, "add:"+hash)
		case "/api/v2/torrents/removeTags", "/api/v2/torrents/deleteTags":
			hash, tag := r.FormValue("hashes"), r.FormValue("tags")
			if hash != "" {
				tags := strings.Split(state.tags[hash], ",")
				state.tags[hash] = strings.Join(slices.DeleteFunc(tags, func(s string) bool { return s == tag }), ",")
			}
		case "/api/v2/torrents/start", "/api/v2/torrents/resume":
			hash := r.FormValue("hashes")
			key := "task." + state.taskID + ".torrents"
			raw, err := a.pluginStore.originalBaseGetData(r.Context(), brushPackageFixtureID, &key)
			var records map[string]struct {
				ClockVersion int `json:"promotion_clock_version"`
			}
			if err != nil || json.Unmarshal(raw, &records) != nil || records[hash].ClockVersion != 2 || byHash[hash].hash == "" || state.started[hash] {
				t.Error("start did not follow the durable exact-hash v2 record", err)
			}
			state.started[hash] = true
			state.order = append(state.order, "start:"+hash)
		default:
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, "Ok.")
	}))
	t.Cleanup(qb.Close)
	settings := a.Settings()
	settings.Sites = []SiteConfig{{Config: sites.Config{ID: "owned-stream-site", Name: "Owned stream site", Kind: "jiayuan", BaseURL: tracker.URL, Cookie: "owned=stream-fixture"}, Enabled: true}}
	settings.Downloaders = []DownloaderConfig{{Config: download.Config{ID: "owned-stream-qb", Kind: "qbittorrent", BaseURL: qb.URL}, Name: "Owned stream qB", DownloadPath: downloadRoot}}
	settings.AllowedRoots = []string{downloadRoot}
	if err := a.saveSettings(t.Context(), settings); err != nil {
		t.Fatal(err)
	}
	a.Start(t.Context())
	installBrushCandidate(t, a, cookie, bundle)
	identity, err := a.Store.nativeSiteByAppID(t.Context(), "owned-stream-site")
	if err != nil {
		t.Fatal(err)
	}
	sourceID, err := a.Store.nativeSourcePublicSiteID(t.Context(), a.Store.DB, identity)
	if err != nil || sourceID < 1 {
		t.Fatal("owned site missing source ID", err)
	}
	const base = "/api/plugins/" + brushPackageFixtureID + "/api"
	api := func(method, path string, body any) json.RawMessage {
		t.Helper()
		wire := ""
		if body != nil {
			wire = string(mustCleanSeedJSON(body))
		}
		res := callTest(t, a.Handler(), method, base+path, wire, cookie, "")
		var result struct {
			Success bool
			Data    json.RawMessage
		}
		if res.Code != http.StatusOK || json.Unmarshal(res.Body.Bytes(), &result) != nil || !result.Success {
			t.Fatalf("BrushFlow %s %s: %d %s", method, path, res.Code, res.Body.String())
		}
		return result.Data
	}
	api("POST", "/settings", map[string]any{"enabled": true})
	created := api("POST", "/tasks", map[string]any{"name": "Owned long protected run", "enabled": true,
		"site_id": sourceID, "downloader": "Owned stream qB", "save_path": downloadRoot, "freeleech": "free",
		"del_no_free": true, "notify": false, "except_subscribe": false, "timezone_offset": 8})
	var detail struct{ Task struct{ ID string } }
	if json.Unmarshal(created, &detail) != nil || detail.Task.ID == "" {
		t.Fatal("source task creation did not return its persistent identity")
	}
	state.Lock()
	state.taskID = detail.Task.ID
	state.Unlock()
	api("POST", "/tasks/"+detail.Task.ID+"/run", nil)

	// A saved final run receipt proves the same callback reached its source
	// end after all effects. No fresh invocation or candidate replay is used.
	key := "task." + detail.Task.ID + ".runs"
	deadline := time.Now().Add(75 * time.Second)
	for time.Now().Before(deadline) {
		raw, err := a.pluginStore.originalBaseGetData(t.Context(), brushPackageFixtureID, &key)
		var runs []struct {
			Result     string `json:"result"`
			AddedCount int    `json:"added_count"`
		}
		if err == nil && json.Unmarshal(raw, &runs) == nil && len(runs) > 0 {
			state.Lock()
			if runs[0].Result != "completed" || runs[0].AddedCount != count || state.samples != 5 || state.pages != count || !slices.Equal(state.order, expected) {
				t.Errorf("source long pass truncated or reordered: result=%+v samples=%d pages=%d operations=%v", runs[0], state.samples, state.pages, state.order)
			}
			state.Unlock()
			if !t.Failed() {
				t.Logf("one source pass saved all %d ordered protected pairs after %d detail bytes (base64 replies exceed 64 MiB)", count, count*detailBytes)
			}
			return
		}
		time.Sleep(60 * time.Millisecond)
	}
	t.Fatal("source pass did not persist its final run result")
}
