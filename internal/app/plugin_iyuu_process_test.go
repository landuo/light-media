package app

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"light-media/internal/download"
)

// Runs the real IYUU executable through the plugin manager. The installed
// source's +3s date consumes onlyonce before dispatch, and a source downloader
// with no completed torrents cannot trigger an IYUU request or daemon mutation.
func TestIYUUSourceWorkerInstalledOnceAndStopped(t *testing.T) {
	worker := os.Getenv("SOURCE_IYUU_WORKER")
	if worker == "" {
		t.Skip("requires the actual IYUU 2.22 worker")
	}
	var reads atomic.Int32
	completed := make(chan time.Time, 2)
	release := make(chan struct{})
	var releaseOnce sync.Once
	finishRead := func() { releaseOnce.Do(func() { close(release) }) }
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/login":
			http.SetCookie(w, &http.Cookie{Name: "owned-auth", Value: "owned", Path: "/"})
			_, _ = io.WriteString(w, "Ok.")
		case "/api/v2/app/webapiVersion":
			_, _ = io.WriteString(w, "2.11.4")
		case "/api/v2/torrents/info":
			if _, err := r.Cookie("owned-auth"); err != nil {
				t.Error("source read did not retain the downloader login cookie")
			}
			reads.Add(1)
			completed <- time.Now()
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
			_, _ = io.WriteString(w, "[]")
		default:
			t.Errorf("unexpected downloader operation %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer func() { finishRead(); peer.Close() }()

	a := newTestApp(t)
	cookie := setupTestAdmin(t, a)
	ctx := context.Background()
	settings := a.Settings()
	settings.Downloaders = []DownloaderConfig{{Config: download.Config{
		ID: "owned", Kind: "qbittorrent", BaseURL: peer.URL, Username: "owned", Password: "owned",
	}, Name: "Owned qB"}}
	if err := a.saveSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	manifest := installOwnedResourceProcessWorker(t, a, cookie, worker)
	if manifest.PluginID != "IYUUAutoSeed" || manifest.Version != "2.22" {
		t.Fatal("unexpected IYUU executable manifest")
	}
	path := "/api/plugins/" + manifest.PluginID
	form := callTest(t, a.Handler(), "GET", path+"/form", "", cookie, "")
	if form.Code != http.StatusOK || !json.Valid(form.Body.Bytes()) ||
		!bytes.Contains(form.Body.Bytes(), []byte("Owned qB")) {
		t.Fatal("actual native plugin form did not expose the configured downloader")
	}

	config := `{"enabled":true,"cron":"13 3 * * mon","token":"owned-test-token","downloaders":["Owned qB"],"onlyonce":true,"clearcache":true,"success_caches":["old"]}`
	start := time.Now()
	result := callTest(t, a.Handler(), "PUT", path, config, cookie, "")
	assertOriginalRegistrySuccess(t, result.Code, result.Body.Bytes())
	privateAwait(t, "source init persisted consumed flags and cleared cache", func() bool {
		stored, err := a.pluginStore.Config(ctx, manifest.PluginID)
		var state struct {
			OnlyOnce   *bool    `json:"onlyonce"`
			ClearCache *bool    `json:"clearcache"`
			Success    []string `json:"success_caches"`
		}
		return err == nil && json.Unmarshal(stored.Value, &state) == nil && state.OnlyOnce != nil &&
			!*state.OnlyOnce && state.ClearCache != nil && !*state.ClearCache && state.Success != nil && len(state.Success) == 0
	})
	object := originalBaseSDKLoaded(t, a, manifest.PluginID)
	services, err := object.getServices(ctx)
	if err != nil || len(services) != 1 || services[0].ID != "IYUUAutoSeed" ||
		services[0].Handler != "auto_seed" || services[0].Trigger.Cron != "13 3 * * mon" || !services[0].Trigger.LocalTimezone {
		t.Fatal("actual public service declaration did not match the source cron", services, err)
	}
	select {
	case at := <-completed:
		if at.Sub(start) < 3*time.Second {
			t.Fatal("source date fired early")
		}
	case <-time.After(7 * time.Second):
		t.Fatal("actual private date did not reach the owned downloader")
	}

	object.submitted.mu.Lock()
	privateLeases := 0
	for lease := range object.submitted.leases {
		if lease.privateScheduler && lease.active.Load() {
			privateLeases++
		}
	}
	object.submitted.mu.Unlock()
	if privateLeases != 2 {
		t.Error("source private interval did not get a fresh callback while the date pass was active")
	}
	finishRead()
	result = callTest(t, a.Handler(), "POST", path+"/uninstall", "{}", cookie, "")
	assertOriginalRegistrySuccess(t, result.Code, result.Body.Bytes())
	privateAwait(t, "uninstalled IYUU process stopped", func() bool {
		_, loaded := a.pluginRunningInstances.Load(manifest.PluginID)
		object.sessionMu.Lock()
		defer object.sessionMu.Unlock()
		return !loaded && object.session == nil
	})
	if reads.Load() != 1 {
		t.Fatal("IYUU one-shot dispatched more than once")
	}
	t.Log("real worker installed; form, persisted init flags, public cron and +3s private date reached loopback qB; next interval had a fresh private lease; uninstall stopped the process")
}
