package app

import (
	"context"
	"encoding/base32"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"light-media/internal/catalog"
	"light-media/internal/download"
	bf "light-media/internal/plugins/brushflow"
	sr "light-media/internal/plugins/seedrescue"
	"light-media/internal/sites"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func brushFinish(t *testing.T, a *App, r PluginRun) PluginRun {
	t.Helper()
	a.executePluginRun(context.Background(), r)
	done, e := a.pluginStore.Run(context.Background(), r.ID)
	if e != nil || done.State != "succeeded" {
		state, _, stateErr := a.pluginStore.brushFlowRead(context.Background(), a.Store.DB, r.InstanceID, r.Generation)
		t.Fatalf("SDK %s state=%s code=%s error=%v owner=%+v ownerErr=%v", r.Action, done.State, done.ErrorCode, e, state, stateErr)
	}
	return done
}
func brushInstall(t *testing.T, a *App, cfg bf.Row) (PluginInstallation, PluginRun) {
	t.Helper()
	r := uninstallInstall(t, a, bf.PluginID, bf.NativeManifest(), cfg, true)
	in, e := a.pluginStore.Installation(context.Background(), bf.PluginID)
	if e != nil {
		t.Fatal(e)
	}
	return in, r
}
func TestBrushFlowActualSDKConfigureAPIAndCold(t *testing.T) {
	a := newTestApp(t)
	useBuiltInPluginProcess(t, a)
	cookie := setupTestAdmin(t, a)
	cfg := bf.DefaultConfig()
	in, r := brushInstall(t, a, cfg)
	brushFinish(t, a, r)
	s, _, e := a.pluginStore.brushFlowRead(context.Background(), a.Store.DB, in.InstanceID, in.Generation)
	if e != nil || s.AppliedRevision < 1 || len(s.Runs) != 1 {
		t.Fatal(s, e)
	}
	form := callTest(t, a.Handler(), "GET", "/api/plugins/BrushFlowLanduo/form", "", cookie, "")
	if form.Code != 200 || !strings.Contains(form.Body.String(), `"render_mode":"vue"`) {
		t.Fatal(form.Code, form.Body.String())
	}
	status := callTest(t, a.Handler(), "GET", "/api/plugins/BrushFlowLanduo/api/status", "", cookie, "")
	if status.Code != 200 || !strings.Contains(status.Body.String(), `"success":true`) {
		t.Fatal(status.Code, status.Body.String())
	}
	body := `{"enabled":true,"show_sidebar_nav":true}`
	update := callTest(t, a.Handler(), "POST", "/api/plugins/BrushFlowLanduo/api/settings", body, cookie, "")
	if update.Code != 200 || !strings.Contains(update.Body.String(), `"success":true`) {
		t.Fatal(update.Code, update.Body.String())
	}
	nav, e := a.brushFlowSidebar(context.Background(), in)
	if e != nil || len(nav) != 1 || nav[0]["nav_key"] != "main" {
		t.Fatal(nav, e)
	}
	a.pluginBrushFlow.owners = nil
	status = callTest(t, a.Handler(), "GET", "/api/plugins/BrushFlowLanduo/api/status", "", cookie, "")
	if status.Code != 200 || !strings.Contains(status.Body.String(), `"success":true`) {
		t.Fatal("cold readonly", status.Code, status.Body.String())
	}
	var count int
	a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_runs WHERE instance_id='BrushFlowLanduo' AND state='reconcile_pending'`).Scan(&count)
	if count != 0 {
		t.Fatal("unexpected unknown", count)
	}
	raw, _ := json.Marshal(bf.NativeManifest())
	if !strings.Contains(string(raw), `"/tasks/{task_id}/clear"`) {
		t.Fatal(string(raw))
	}
}

func TestBrushFlowActualHTTPTaskCRUDNativeBrushAndDelete(t *testing.T) {
	brushActualFlow(t, false, "qbittorrent", false, false)
}
func TestBrushFlowActualLostAddACKNoWarmOrColdRedispatch(t *testing.T) {
	brushActualFlow(t, true, "qbittorrent", false, false)
}
func brushActualFlow(t *testing.T, lost bool, kind string, observe, busy bool) {
	a := newTestApp(t)
	useBuiltInPluginProcess(t, a)
	cookie := setupTestAdmin(t, a)
	ctx := context.Background()
	a.pluginBrushFlow.sleep = func(context.Context, time.Duration) error { return nil }
	torrent := []byte("d4:infod6:lengthi1e4:name5:owned12:piece lengthi16384e6:pieces20:aaaaaaaaaaaaaaaaaaaaee")
	hash, e := sr.InfoHashFromTorrent(torrent)
	if e != nil {
		t.Fatal(e)
	}
	var adds, deletes, tagDeletes, reannounces atomic.Int32
	magnetMode := kind == "qb-magnet-base32"
	subscriptionMode := kind == "qb-subscription-names"
	rssInvalid := kind == "qb-rss-invalid"
	rssAtom := kind == "qb-rss-atom"
	rssEncoding := kind == "qb-rss-declared-encoding"
	rssRecover := kind == "qb-rss-recover"
	rssLarge := kind == "qb-rss-large-number"
	var metadataCalls atomic.Int32
	rawHash, _ := hex.DecodeString(hash)
	magnet := "magnet:?xt=urn:btih:" + strings.ToLower(base32.StdEncoding.EncodeToString(rawHash))
	var known atomic.Bool
	var tagsMu sync.Mutex
	tags := ""
	var taskID string
	var trLimits atomic.Int32
	qb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(kind, "transmission") {
			var request map[string]any
			json.NewDecoder(r.Body).Decode(&request)
			method, _ := request["method"].(string)
			args, _ := request["params"].(map[string]any)
			legacy := request["jsonrpc"] == nil
			if kind == "transmission-legacy" && !legacy {
				json.NewEncoder(w).Encode(map[string]any{"result": "method name not recognized", "arguments": map[string]any{}, "tag": request["id"]})
				return
			}
			if legacy {
				args, _ = request["arguments"].(map[string]any)
			}
			result := map[string]any{}
			switch strings.ReplaceAll(method, "-", "_") {
			case "session_get":
				result = map[string]any{"version": "4.1.1", "download_dir": "/daemon/default"}
				if legacy {
					result = map[string]any{"version": "4.0.6", "download-dir": "/daemon/default"}
				}
			case "session_stats":
				result = map[string]any{"upload_speed": 1, "download_speed": 0, "cumulative_stats": map[string]any{"uploaded_bytes": 10, "downloaded_bytes": 1}}
			case "torrent_get":
				rows := []any{}
				if known.Load() {
					tagsMu.Lock()
					labels := strings.Split(tags, ",")
					tagsMu.Unlock()
					row := map[string]any{"id": 1, "hash_string": hash, "name": "Owned brush", "status": 6, "labels": labels, "total_size": 1, "percent_done": 1, "uploaded_ever": 10, "downloaded_ever": 1, "upload_ratio": 10, "added_date": 1, "done_date": 1, "activity_date": 1, "tracker_list": "http://owned.invalid/announce"}
					if legacy {
						row = map[string]any{"id": 1, "hashString": hash, "name": "Owned brush", "status": 6, "labels": labels, "totalSize": 1, "percentDone": 1, "uploadedEver": 10, "downloadedEver": 1, "uploadRatio": 10, "addedDate": 1, "doneDate": 1, "activityDate": 1, "trackerList": "http://owned.invalid/announce"}
					}
					if observe {
						if legacy {
							row["downloadDir"] = "/daemon/default"
						} else {
							row["download_dir"] = "/daemon/default"
						}
					}
					rows = append(rows, row)
				}
				result["torrents"] = rows
			case "torrent_add":
				adds.Add(1)
				if args["paused"] != false || args["download_dir"] != nil || args["download-dir"] != nil {
					t.Error("TR active default directory", args)
				}
				labels := []string{}
				for _, v := range args["labels"].([]any) {
					labels = append(labels, v.(string))
				}
				if len(labels) != 3 || labels[0] != "已整理" || labels[1] != bf.GlobalTag {
					t.Error("source TR labels", labels)
				}
				tagsMu.Lock()
				tags = strings.Join(labels, ",")
				tagsMu.Unlock()
				known.Store(true)
				if lost {
					h := w.(http.Hijacker)
					conn, _, _ := h.Hijack()
					conn.Close()
					return
				}
				result["torrent_added"] = map[string]any{"hash_string": hash, "id": 1}
				if legacy {
					result = map[string]any{"torrent-added": map[string]any{"hashString": hash, "id": 1}}
				}
			case "torrent_set":
				trLimits.Add(1)
				limit := args["upload_limit"]
				if legacy {
					limit = args["uploadLimit"]
				}
				if limit != float64(10) {
					t.Error("TR source KB limits", args)
				}
			case "torrent_remove":
				value := args["delete_local_data"]
				if legacy {
					value = args["delete-local-data"]
				}
				if value != true {
					t.Error("TR deleteFiles", args)
				}
				deletes.Add(1)
				known.Store(false)
			default:
				t.Error("unexpected TR", method, args)
			}
			if legacy {
				json.NewEncoder(w).Encode(map[string]any{"result": "success", "arguments": result, "tag": request["tag"]})
			} else {
				json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": request["id"], "result": result})
			}
			return
		}

		switch r.URL.Path {
		case "/api/v2/app/webapiVersion":
			io.WriteString(w, "2.11.3")
		case "/api/v2/app/preferences":
			io.WriteString(w, `{"save_path":"/daemon/default","use_subcategories":false,"use_category_paths_in_manual_mode":false}`)
		case "/api/v2/torrents/categories":
			io.WriteString(w, `{}`)
		case "/api/v2/torrents/tags":
			io.WriteString(w, `[]`)
		case "/api/v2/transfer/info":
			io.WriteString(w, `{"up_info_speed":1,"dl_info_speed":0,"up_info_data":10,"dl_info_data":1}`)
		case "/api/v2/torrents/info":
			r.ParseForm()
			if known.Load() {
				tagsMu.Lock()
				label := tags
				tagsMu.Unlock()
				extra := ""
				if observe {
					extra = `,"save_path":"/daemon/default","category":"owned-category","auto_tmm":false`
				}
				fmt.Fprintf(w, `[{"hash":%q,"name":"Owned brush","state":"uploading","progress":1,"amount_left":0,"total_size":1,"downloaded":1,"uploaded":10,"ratio":10,"tags":%q,"added_on":1,"completion_on":1,"last_activity":1%s}]`, hash, label, extra)
			} else {
				io.WriteString(w, `[]`)
			}
		case "/api/v2/torrents/add":
			adds.Add(1)
			r.ParseMultipartForm(1 << 20)
			if magnetMode && r.FormValue("urls") != magnet {
				t.Error("source magnet add URL", r.Form)
			}
			if r.FormValue("paused") != "False" || r.FormValue("stopped") != "False" || r.FormValue("savepath") != "" || r.FormValue("category") != "owned-category" || r.FormValue("upLimit") != "10240" {
				t.Error("original add arguments", r.Form)
			}
			tagsMu.Lock()
			tags = r.FormValue("tags")
			tagsMu.Unlock()
			known.Store(true)
			if lost {
				h := w.(http.Hijacker)
				conn, _, _ := h.Hijack()
				conn.Close()
				return
			}
			io.WriteString(w, "Ok.")
		case "/api/v2/torrents/removeTags":
			r.ParseForm()
			if r.Form.Get("hashes") != hash {
				t.Error("remove temporary tag", r.Form)
			}
		case "/api/v2/torrents/deleteTags":
			tagDeletes.Add(1)
			io.WriteString(w, "")
		case "/api/v2/torrents/reannounce":
			reannounces.Add(1)
		case "/api/v2/torrents/delete":
			r.ParseForm()
			if r.Form.Get("deleteFiles") != "True" || r.Form.Get("hashes") != hash {
				t.Error("delete wire", r.Form)
			}
			deletes.Add(1)
			known.Store(false)
		default:
			t.Error("unexpected qB", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer qb.Close()
	var origin string
	emptyRecipe := kind == "qb-empty-recipe"
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/rss.php":
			w.Header().Set("Content-Type", "application/rss+xml")
			if rssInvalid {
				io.WriteString(w, "RSS Link has expired, You need to get a new one!")
				return
			}
			if rssAtom {
				fmt.Fprintf(w, `<feed xmlns="http://www.w3.org/2005/Atom"><entry><title>Owned brush</title><link href="%s/details.php?id=9"/><enclosure url="%s/download.php?id=9" length="1"/></entry></feed>`, origin, origin)
				return
			}
			if rssRecover {
				fmt.Fprintf(w, `<rss><channel><item><title>Owned brush</WRONG><link>%s/details.php?id=9</link><enclosure url="%s/download.php?id=9" length="1"/></item></channel></rss>`, origin, origin)
				return
			}
			if rssEncoding {
				w.Header().Set("Content-Type", "text/xml; charset=ISO-8859-1")
				w.Write([]byte(`<?xml version="1.0" encoding="gb18030"?><rss><channel><item><title>Owned brush `))
				w.Write([]byte{0xb2, 0xe2, 0xca, 0xd4})
				fmt.Fprintf(w, `</title><link>%s/details.php?id=9</link><enclosure url="%s/download.php?id=9" length="1"/></item></channel></rss>`, origin, origin)
				return
			}

			if rssLarge {
				fmt.Fprintf(w, `<rss><channel><item><title>Owned brush</title><link>%s/details.php?id=9</link><enclosure url="%s/download.php?id=9" length="9007199254740993"/></item></channel></rss>`, origin, origin)
				return
			}

			enclosure := origin + "/download.php?id=9"
			if emptyRecipe {
				enclosure = "[]" + enclosure
			}
			if magnetMode {
				enclosure = magnet
			}
			fmt.Fprintf(w, `<rss><channel><item><title>Owned brush</title><description>source</description><link>%s/details.php?id=9</link><downloadvolumefactor>0</downloadvolumefactor><seeders>20</seeders><enclosure url="%s" length="1"/></item></channel></rss>`, origin, enclosure)
		case "/download.php":
			if magnetMode {
				t.Error("magnet must not fetch torrent URL")
			}
			expectedCookie := "session=owned"
			if emptyRecipe {
				expectedCookie = ""
			}
			if r.Header.Get("Cookie") != expectedCookie {
				t.Error("bound cookie", r.Header.Get("Cookie"))
			}
			w.Header().Set("Content-Type", "application/x-bittorrent")
			w.Write(torrent)
		default:
			t.Error("unexpected site", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer site.Close()
	origin = site.URL
	a.cfgMu.Lock()
	a.config.Sites = []SiteConfig{{Config: sites.Config{ID: "brush-site", Kind: "nexusphp", Name: "Owned", BaseURL: origin, Cookie: "session=owned"}, SiteOptions: SiteOptions{RSS: origin + "/rss.php"}, Enabled: true}}
	nativeKind := kind
	if strings.HasPrefix(kind, "qb-") {
		nativeKind = "qbittorrent"
	}
	if strings.HasPrefix(kind, "transmission") {
		nativeKind = "transmission"
	}
	a.config.Downloaders = []DownloaderConfig{{Config: download.Config{ID: "brush-qb", Kind: nativeKind, BaseURL: qb.URL}, Name: "Owned qB"}}
	a.cfgMu.Unlock()
	if e = a.bootstrapNativeSiteIdentities(ctx); e != nil {
		t.Fatal(e)
	}
	if observe {
		// A private loopback fixture is intentionally not admissible through the
		// product's public HTTPS tracker-settings API. Persist only this synthetic
		// encrypted fixture so a genuinely fresh App exercises normal cold reads.
		sealed, err := a.Store.Seal(a.Settings())
		if err != nil {
			t.Fatal(err)
		}
		if _, err = a.Store.DB.Exec(`INSERT INTO records VALUES('config','main',?,?) ON CONFLICT(kind,id) DO UPDATE SET payload=excluded.payload,updated_at=excluded.updated_at`, sealed, now()); err != nil {
			t.Fatal(err)
		}
	}
	if subscriptionMode {
		withoutProxyEnv(t)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			metadataCalls.Add(1)
			if r.URL.Path != "/3/movie/123" || r.URL.Query().Get("append_to_response") != "images,credits,alternative_titles,translations,release_dates,external_ids" {
				t.Error("source metadata names request", r.URL)
			}
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"id":123,"alternative_titles":{"titles":[{"title":"Uncached foreign title","iso_3166_1":"RU"}]},"translations":{"translations":[{"data":{"title":"Owned brush"}}]}}`)
		}))
		defer server.Close()
		target, _ := url.Parse(server.URL)
		transport := http.DefaultTransport
		http.DefaultTransport = brushFlowMetadataRoundTrip(func(r *http.Request) (*http.Response, error) {
			if r.URL.Scheme != "https" || r.URL.Host != "api.themoviedb.org" {
				t.Error("metadata origin escaped", r.URL)
			}
			copy := r.Clone(r.Context())
			u := *r.URL
			copy.URL = &u
			copy.URL.Scheme, copy.URL.Host = target.Scheme, target.Host
			return server.Client().Transport.RoundTrip(copy)
		})
		client := catalog.New(catalog.Credentials{APIKey: "owned_metadata"})
		http.DefaultTransport = transport
		a.cfgMu.Lock()
		a.config.TMDB = TMDBConfig{APIKey: "owned_metadata"}
		a.cfgMu.Unlock()
		a.catMu.Lock()
		a.catClient = client
		a.catCreds = a.Settings().TMDB
		a.catMu.Unlock()
		subscription := Subscription{ID: "owned-subscription", Media: catalog.Media{ID: 123, MediaType: "movie", Title: "Stored subscription"}}
		if err := a.Store.Put(ctx, "subscriptions", subscription.ID, subscription); err != nil {
			t.Fatal(err)
		}
	}
	identity, e := a.Store.nativeSiteByAppID(ctx, "brush-site")
	if e != nil {
		t.Fatal(e)
	}
	cfg := bf.DefaultConfig()
	cfg["enabled"] = true
	in, init := brushInstall(t, a, cfg)
	brushFinish(t, a, init)
	cleanup, e := a.pluginStore.claimRunForInstance(ctx, in.InstanceID)
	if e != nil {
		t.Fatal("cleanup queue", e)
	}
	brushFinish(t, a, cleanup)
	api := func(method, path, body string) map[string]any {
		t.Helper()
		w := callTest(t, a.Handler(), method, "/api/plugins/BrushFlowLanduo/api/"+path, body, cookie, "")
		if w.Code != 200 {
			t.Fatal(method, path, w.Code, w.Body.String())
		}
		var out map[string]any
		if json.Unmarshal(w.Body.Bytes(), &out) != nil {
			t.Fatal(w.Body.String())
		}
		return out
	}
	task := map[string]any{"name": "Owned brush", "site_id": identity.SourceID, "downloader": "Owned qB", "notify": true, "rss_support": true, "freeleech": "", "qb_category": "owned-category", "up_speed": 10, "except_subscribe": false, "seed_ratio": 1}
	if subscriptionMode {
		task["except_subscribe"] = true
	}
	created := api("POST", "tasks", string(mustCleanSeedJSON(task)))
	if created["success"] != true {
		t.Fatal(created)
	}
	data, _ := created["data"].(map[string]any)
	taskID, _ = data["id"].(string)
	if taskID == "" {
		var s struct {
			Tasks []struct {
				ID string `json:"id"`
			}
		}
		stored, _ := a.pluginStore.Config(ctx, in.InstanceID)
		json.Unmarshal(stored.Value, &s)
		if len(s.Tasks) != 1 {
			t.Fatal("task config", string(stored.Value))
		}
		taskID = s.Tasks[0].ID
	}
	detail := api("GET", "tasks/"+taskID, " ")
	if detail["success"] != true {
		t.Fatal(detail)
	}
	submit := api("POST", "tasks/"+taskID+"/run", "")
	if submit["success"] != true {
		t.Fatal(submit)
	}
	pending, e := a.pluginStore.claimRunForInstance(ctx, in.InstanceID)
	if e != nil {
		t.Fatal("queued brush", e)
	}
	if lost {
		a.executePluginRun(ctx, pending)
		done, _ := a.pluginStore.Run(ctx, pending.ID)
		if done.State != "reconcile_pending" {
			t.Fatal("unknown add", done.State, done.ErrorCode)
		}
		if adds.Load() != 1 || tagDeletes.Load() != 0 {
			t.Fatal("unknown must stop finally", adds.Load(), tagDeletes.Load())
		}
		if _, e = a.pluginStore.EnqueueRun(ctx, in.InstanceID, "run", nil); e == nil {
			t.Fatal("warm unknown admitted new write")
		}
		if observe {
			dir := a.Dir
			if e = a.Close(); e != nil {
				t.Fatal(e)
			}
			cold, e := New(Options{DataDir: dir})
			if e != nil {
				t.Fatal(e)
			}
			defer cold.Close()
			a = cold
			useBuiltInPluginProcess(t, a)
			a.pluginBrushFlow.sleep = func(context.Context, time.Duration) error { return nil }
			result := api("POST", "tasks/"+taskID+"/run", "")
			if result["success"] != true {
				t.Fatal("readonly observation did not recover", result)
			}
			next, e := a.pluginStore.claimRunForInstance(ctx, in.InstanceID)
			if e != nil {
				t.Fatal(e)
			}
			brushFinish(t, a, next)
			if adds.Load() != 1 || tagDeletes.Load() != 0 {
				t.Fatal("observed unknown replayed write or cleanup", adds.Load(), tagDeletes.Load())
			}
			var events int
			a.Store.DB.QueryRow(`SELECT COUNT(*) FROM domain_events WHERE json_extract(payload,'$.event_type')='plugin.triggered'`).Scan(&events)
			if events != 0 {
				t.Fatal("unknown add manufactured original event", events)
			}
			old, _ := a.pluginStore.Run(ctx, pending.ID)
			if old.State != "failed" || old.ErrorCode != "BRUSH_FLOW_OBSERVED" {
				t.Fatal("observation receipt", old.State, old.ErrorCode)
			}
			return
		}
		a.pluginBrushFlow.owners = nil
		if _, e = a.pluginStore.EnqueueRun(ctx, in.InstanceID, "run", nil); e == nil {
			t.Fatal("cold unknown admitted")
		}
		if a.pluginStore.recoverPluginRuns(ctx) != nil {
			t.Fatal("cold recovery failed")
		}
		if adds.Load() != 1 {
			t.Fatal("redispatched", adds.Load())
		}
		return
	}
	if busy {
		entered := make(chan struct{})
		release := make(chan struct{})
		var once sync.Once
		a.pluginBrushFlow.sleep = func(ctx context.Context, _ time.Duration) error {
			once.Do(func() {
				close(entered)
				select {
				case <-release:
				case <-ctx.Done():
				}
			})
			return ctx.Err()
		}
		done := make(chan struct{})
		go func() { a.executePluginRun(ctx, pending); close(done) }()
		select {
		case <-entered:
		case <-time.After(10 * time.Second):
			t.Fatal("source never entered native host")
		}
		start := time.Now()
		if v := api("GET", "status", ""); v["success"] != true {
			t.Fatal(v)
		}
		if v := api("POST", "tasks/"+taskID+"/check", ""); v["success"] != false {
			t.Fatal("same task was admitted", v)
		}
		if time.Since(start) > 5*time.Second {
			t.Fatal("SDK read/busy waited behind native host")
		}
		close(release)
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Fatal("source did not finish")
		}
		actual, _ := a.pluginStore.Run(ctx, pending.ID)
		if actual.State != "succeeded" {
			t.Fatal(actual.State, actual.ErrorCode)
		}
	} else {
		brushFinish(t, a, pending)
	}
	if rssInvalid {
		if adds.Load() != 0 {
			t.Fatal("original non-XML expired text cannot trigger download/renew", adds.Load())
		}
		return
	}
	if subscriptionMode {
		if adds.Load() != 0 || metadataCalls.Load() != 1 {
			t.Fatal("real TMDB translation did not exclude source candidate", adds.Load(), metadataCalls.Load())
		}
		runAgain := func() {
			if out := api("POST", "tasks/"+taskID+"/run", ""); out["success"] != true {
				t.Fatal(out)
			}
			next, err := a.pluginStore.claimRunForInstance(ctx, in.InstanceID)
			if err != nil {
				t.Fatal(err)
			}
			brushFinish(t, a, next)
		}
		runAgain()
		if metadataCalls.Load() != 1 {
			t.Fatal("source id_name cache was not retained", metadataCalls.Load())
		}
		subscription := Subscription{ID: "owned-subscription", Media: catalog.Media{ID: 123, MediaType: "movie", Title: "Renamed subscription"}}
		if err := a.Store.Put(ctx, "subscriptions", subscription.ID, subscription); err != nil {
			t.Fatal(err)
		}
		runAgain()
		if metadataCalls.Load() != 2 || adds.Load() != 0 {
			t.Fatal("source rename cache invalidation", metadataCalls.Load(), adds.Load())
		}
		return
	}
	if adds.Load() != 1 || kind == "qbittorrent" && tagDeletes.Load() != 2 || strings.HasPrefix(kind, "transmission") && trLimits.Load() != 1 {
		t.Fatal("source add/finally", adds.Load(), tagDeletes.Load())
	}
	detail = api("GET", "tasks/"+taskID, "")
	if rssEncoding && !strings.Contains(string(mustCleanSeedJSON(detail)), "测试") {
		t.Fatal("XML declared codec ignored", detail)
	}
	if !strings.Contains(string(mustCleanSeedJSON(detail)), `"total":1`) {
		t.Fatal("task record missing", detail)
	}
	// The same source candidate must be skipped on the next scheduled/queued pass.
	again := api("POST", "tasks/"+taskID+"/run", "")
	if again["success"] != true {
		t.Fatal(again)
	}
	second, e := a.pluginStore.claimRunForInstance(ctx, in.InstanceID)
	if e != nil {
		t.Fatal(e)
	}
	brushFinish(t, a, second)
	if adds.Load() != 1 {
		t.Fatal("candidate page identity failed dedup", adds.Load())
	}
	var events int
	if e = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM domain_events WHERE json_extract(payload,'$.event_type')='plugin.triggered'`).Scan(&events); e != nil || events != 1 {
		t.Fatal("confirmed source event", events, e)
	}
	brushFlowVerifyOriginalTrigger(t, a)
	if rssLarge {
		var raw []byte
		if e := a.Store.DB.QueryRow(`SELECT payload FROM domain_events WHERE json_extract(payload,'$.event_type')='plugin.triggered'`).Scan(&raw); e != nil {
			t.Fatal(e)
		}
		var event DomainEvent
		if json.Unmarshal(raw, &event) != nil {
			t.Fatal("event decode")
		}
		payload, err := a.pluginStore.brushFlowTriggeredPayload(ctx, a.Store.DB, event)
		if err != nil || !strings.Contains(string(payload), `"size":9007199254740993`) {
			t.Fatal("source numeric receipt changed", err, string(payload))
		}
	}
	service := callTest(t, a.Handler(), "POST", "/api/plugins/services/run", string(mustCleanSeedJSON(map[string]string{"instance_id": in.InstanceID, "service_id": "Task_" + taskID + "_Check"})), cookie, "")
	if service.Code != 200 {
		t.Fatal("public original service dispatch", service.Code, service.Body.String())
	}
	check, e := a.pluginStore.claimRunForInstance(ctx, in.InstanceID)
	if e != nil {
		t.Fatal(e)
	}
	brushFinish(t, a, check)
	if deletes.Load() != 1 || kind == "qbittorrent" && reannounces.Load() != 1 || strings.HasPrefix(kind, "transmission") && reannounces.Load() != 0 {
		t.Fatal("source check delete/reannounce", deletes.Load(), reannounces.Load())
	}
	api("POST", "tasks/"+taskID+"/check", "")
	more, e := a.pluginStore.claimRunForInstance(ctx, in.InstanceID)
	if e != nil {
		t.Fatal(e)
	}
	brushFinish(t, a, more)
	if deletes.Load() != 1 {
		t.Fatal("repeated check repeated delete", deletes.Load())
	}
	api("POST", "tasks/"+taskID+"/clear", "")
	task["name"] = "Renamed"
	api("PUT", "tasks/"+taskID, string(mustCleanSeedJSON(task)))
	api("POST", "tasks/"+taskID+"/state", `{"enabled":false}`)
	removed := api("DELETE", "tasks/"+taskID, "")
	if removed["success"] != true {
		t.Fatal(removed)
	}
}

func TestBrushFlowActualTransmissionModernAndLegacy(t *testing.T) {
	for _, kind := range []string{"transmission", "transmission-legacy"} {
		t.Run(kind, func(t *testing.T) { brushActualFlow(t, false, kind, false, false) })
	}
}
func TestBrushFlowActualTransmissionLostACK(t *testing.T) {
	brushActualFlow(t, true, "transmission", false, false)
}

func TestBrushFlowActualColdAddObservationNeverReplaysWriteOrEmitsEvent(t *testing.T) {
	for _, kind := range []string{"qbittorrent", "transmission"} {
		t.Run(kind, func(t *testing.T) { brushActualFlow(t, true, kind, true, false) })
	}
}

func TestBrushFlowActualConcurrentSDKReadAndSameTaskBusy(t *testing.T) {
	brushActualFlow(t, false, "qbittorrent", false, true)
}

func TestBrushFlowActualEmptyRedirectRecipeUsesDirectSuffixWithoutCookie(t *testing.T) {
	brushActualFlow(t, false, "qb-empty-recipe", false, false)
}

func TestBrushFlowActualBase32MagnetPreservesURIAndNormalizesReceiptIdentity(t *testing.T) {
	brushActualFlow(t, false, "qb-magnet-base32", false, false)
}

type brushFlowMetadataRoundTrip func(*http.Request) (*http.Response, error)

func (f brushFlowMetadataRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestBrushFlowActualSDKSubscriptionNamesFetchAndIDNameCache(t *testing.T) {
	brushActualFlow(t, false, "qb-subscription-names", false, false)
}

func TestBrushFlowActualSDKAtomDeclaredEncodingAndRecovery(t *testing.T) {
	for _, kind := range []string{"qb-rss-atom", "qb-rss-declared-encoding", "qb-rss-recover", "qb-rss-invalid"} {
		t.Run(kind, func(t *testing.T) { brushActualFlow(t, false, kind, false, false) })
	}
}

func TestBrushFlowActualSDKLargeRSSCandidateAndEventPreserveNumber(t *testing.T) {
	brushActualFlow(t, false, "qb-rss-large-number", false, false)
}
