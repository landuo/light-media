package app

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"light-media/internal/plugins"
)

func TestOriginalLarkLegacyMaterialRecoveryLinuxAMD64(t *testing.T) {
	observerBinary := os.Getenv("OWNED_TRANSFER_EVENTS_BINARY")
	if runtime.GOARCH != "amd64" || observerBinary == "" {
		t.Skip("requires the Linux amd64 SDK event observer")
	}
	for _, env := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "all_proxy"} {
		t.Setenv(env, "")
	}
	ctx := t.Context()
	a, source, library := importTestApp(t)
	m := importFixture(t, a, "movie")
	file := m.Title + " (2024) 1080p.mkv"
	release := m.Title + ".2024.1080p.WEB-DL.x264-TEST"
	initial, hash := workflowTorrent(file)
	// Add top-level metadata outside `info`. The selected hash is unchanged,
	// but the restored bytes can only come from the new tracker confirmation.
	recovered := append([]byte("d7:comment5:owned"), initial[1:]...)
	var legacy, completed atomic.Bool
	var firstGet, recoveryGet, confirmations, adds, fileReads atomic.Int32
	tracker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/torrents.php":
			_, _ = io.WriteString(w, workflowHTML(release, "", false))
		case "/download.php":
			if r.URL.Query().Get("id") != "101" || r.Header.Get("Cookie") != "fixture-cookie" {
				t.Error("tracker confirmation lost the original download URL or same-origin cookie")
				http.Error(w, "invalid tracker request", http.StatusBadRequest)
				return
			}
			switch {
			case r.Method == http.MethodGet && !legacy.Load():
				firstGet.Add(1)
				_, _ = w.Write(initial)
			case r.Method == http.MethodGet:
				recoveryGet.Add(1)
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				_, _ = io.WriteString(w, `<form action="?"><input name="confirm" value="owned">下载种子文件</form>`)
			case r.Method == http.MethodPost && legacy.Load():
				if err := r.ParseForm(); err != nil || r.PostForm.Get("confirm") != "owned" || len(r.PostForm) != 1 {
					t.Error("tracker did not receive exactly the original confirmation form", err)
					http.Error(w, "invalid tracker confirmation", http.StatusBadRequest)
					return
				}
				confirmations.Add(1)
				_, _ = w.Write(recovered)
			default:
				t.Error("unexpected tracker operation", r.Method)
				http.NotFound(w, r)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(tracker.Close)
	qbURL := originalLarkNativeProviderDownloadPeer(t, source, file, hash, recovered, &adds, &fileReads, &completed)
	workflowConfigure(t, a, tracker.URL, qbURL)
	a.config.Downloaders[0].DownloadPath, a.config.Downloaders[0].SharedPaths = source, true
	a.config.Directories = []DirectoryRule{{ID: "owned-legacy", Name: "来源下载测试目录", SourcePath: source,
		LibraryPath: library, RootID: "root", MediaType: "movie", Mode: "hardlink", MonitorType: "downloader", Enabled: true}}
	if err := a.saveSettings(ctx, a.Settings()); err != nil {
		t.Fatal(err)
	}
	metadata := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/3/movie/123" {
			t.Errorf("unexpected metadata request: %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		workflowJSON(w, map[string]any{"id": m.ID, "title": m.Title, "original_title": m.OriginalTitle,
			"release_date": "2024-01-01", "original_language": m.OriginalLanguage, "origin_country": m.OriginCountry,
			"production_countries": m.ProductionCountries, "genres": m.Genres, "overview": m.Overview})
	}))
	t.Cleanup(metadata.Close)
	installMetadataFixture(t, a, "owned-legacy-tmdb", []string{"/3/movie/123"}, ownedLoopbackFixtureTransport(t, metadata).RoundTrip)
	postAdd := make(chan []byte, 2)
	callback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var call struct{ Kwargs map[string]json.RawMessage }
		if r.Method != http.MethodPost || r.URL.Path != "/owned-post-add" || json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&call) != nil {
			t.Error("invalid SDK download_added call")
			http.Error(w, "invalid callback", http.StatusBadRequest)
			return
		}
		content, ok := plugins.DecodeModuleResultBytes(call.Kwargs["torrent_content"])
		if !ok {
			t.Error("recovered source callback lost the torrent bytes type")
		}
		select {
		case postAdd <- content:
		default:
			t.Error("unexpected repeated SDK download_added call")
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(callback.Close)
	admin := setupTestAdmin(t, a)
	observer := installOrderWorker(t, a, admin, observerBinary).PluginID
	configured := callTest(t, a.Handler(), "PUT", "/api/plugins/"+observer,
		string(mustCleanSeedJSON(map[string]any{"enabled": true, "post_url": callback.URL + "/owned-post-add"})), admin, "")
	assertOriginalRegistrySuccess(t, configured.Code, configured.Body.Bytes())

	preview, err := a.previewDownload(ctx, PreviewRequest{SearchRequest: SearchRequest{MediaType: m.MediaType, TMDBID: m.ID},
		SiteID: "fixture-site", ResourceID: "101", DownloadPath: source}, false)
	if err != nil {
		t.Fatal(err)
	}
	intent, err := a.confirmDownload(ctx, preview.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	frozen, err := a.loadDownloadContext(ctx, "intent", intent.ID, hash)
	if err != nil || frozen == nil || frozen.Original == nil || intent.MaterialRef == "" || firstGet.Load() != 1 || adds.Load() != 0 {
		t.Fatal("could not seed a confirmed legacy source intent", err)
	}
	legacyRef := intent.MaterialRef
	intent.ChatDownload, preview.ChatDownload = true, true
	intent.MaterialRef, intent.MaterialState, preview.MaterialRef, preview.MaterialState = "", "", "", ""
	frozen.ChatOrigin = &originalChatDownloadOrigin{Channel: "飞书", Source: "Lark", UserID: "ou_owned_legacy", Username: "ou_owned_legacy"}
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err = txPut(ctx, tx, "download_preview", preview.ID, preview); err != nil {
		t.Fatal(err)
	}
	if err = txPut(ctx, tx, "intent", intent.ID, intent); err != nil {
		t.Fatal(err)
	}
	if err = a.saveDownloadContextTx(ctx, tx, *frozen); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM records WHERE kind IN ('material_info','material_blob') AND id=?`, legacyRef); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	a.materialMu.Lock()
	delete(a.materials, preview.ID)
	a.materialMu.Unlock()
	legacy.Store(true)

	job, err := a.claim(ctx)
	if err != nil || job.Kind != "download" {
		t.Fatalf("claim actual legacy download: %s %v", job.Kind, err)
	}
	if err = a.runDownload(ctx, job, intent.ID); err != nil {
		t.Fatal(err)
	}
	if err = a.Store.Get(ctx, "intent", intent.ID, &intent); err != nil || intent.State != "accepted" || intent.MaterialRef != "" ||
		recoveryGet.Load() != 1 || confirmations.Load() != 1 || adds.Load() != 1 {
		t.Fatalf("legacy source did not perform one confirmation and exact Add: state=%s GET=%d POST=%d add=%d err=%v",
			intent.State, recoveryGet.Load(), confirmations.Load(), adds.Load(), err)
	}
	archived, err := a.loadDownloadContext(ctx, "intent", intent.ID, hash)
	if err != nil || archived == nil || archived.OriginalMaterialRef == "" || archived.OriginalMaterialRef == legacyRef {
		t.Fatal("restored material is missing from the authenticated intent context", err)
	}
	var info materialInfo
	if err = a.Store.Get(ctx, "material_info", archived.OriginalMaterialRef, &info); err != nil || !info.Pinned || info.Hash != hash {
		t.Fatal("restored material was not pinned to the original stable hash", err)
	}
	data, err := a.resolveDownloadMaterial(ctx, archived.OriginalMaterialRef, materialPurpose, false)
	if err != nil || !bytes.Equal(data, recovered) {
		t.Fatal("sealed legacy material differs from the accepted confirmation bytes", err)
	}
	select {
	case raw := <-postAdd:
		if !bytes.Equal(raw, recovered) {
			t.Fatal("actual SDK download_added did not receive restored exact bytes")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("actual SDK download_added did not observe the accepted recovered torrent")
	}
	t.Log("confirmed one seeded legacy Lark intent through tracker GET+POST, pinned exact restored bytes without rewriting its material reference, and delivered those bytes to one qB Add and the SDK callback")
}
