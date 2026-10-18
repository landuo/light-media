package app

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"light-media/internal/download"
	"light-media/internal/plugins"
	iyuu "light-media/internal/plugins/iyuuautoseed"
)

// Explicit wall-clock acceptance, separate from the ordinary fast suite. Seven
// real sequential HTTP calls each fit the native 20-second request deadline,
// while their source scan takes more than the historical global two minutes.
// No request reaches a real downloader, site, or IYUU endpoint.
func TestIYUUActualLongNativeScanSurvivesWorkerHandshakeDeadline(t *testing.T) {
	if os.Getenv("LIGHT_MEDIA_IYUU_LONG_TEST") != "1" {
		t.Skip("explicit owned 126-second HTTP acceptance")
	}
	a := newTestApp(t)
	var completed atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		if r.URL.Path != "/api/v2/torrents/info" || r.Form.Get("filter") != "completed" {
			t.Errorf("unexpected native request %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		timer := time.NewTimer(18 * time.Second)
		defer timer.Stop()
		select {
		case <-timer.C:
			completed.Add(1)
			io.WriteString(w, "[]")
		case <-r.Context().Done():
		}
	}))
	defer server.Close()
	names := []any{}
	for i := range 7 {
		name := fmt.Sprintf("Owned long scan %d", i)
		names = append(names, name)
		a.config.Downloaders = append(a.config.Downloaders, DownloaderConfig{Config: download.Config{ID: fmt.Sprintf("owned-long-%d", i), Kind: "qbittorrent", BaseURL: server.URL}, Name: name})
	}
	a.pluginIYUU.loopbackAPI = server.URL
	executable, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	a.pluginCommand = func(id string) (plugins.Command, error) {
		if id != "IYUUAutoSeed" {
			return plugins.Command{}, fmt.Errorf("unowned worker")
		}
		return plugins.Command{Path: executable, Args: []string{"-test.run=^TestIYUUActualSDKWorker$", "--", "--iyuu-owned-worker"}}, nil
	}
	ctx := context.Background()
	run := uninstallInstall(t, a, "OwnedIYUULong", iyuu.Manifest(), map[string]any{"enabled": false, "token": "owned-synthetic-token", "downloaders": names, "sites": []any{}, "size": 0}, true)
	a.executePluginRun(ctx, run)
	initialized, e := a.pluginStore.Run(ctx, run.ID)
	if e != nil || initialized.State != "succeeded" {
		t.Fatal("actual runtime initialization", initialized.State, e)
	}
	if _, e = a.pluginStore.EnqueueRun(ctx, run.InstanceID, "run", nil); e != nil {
		t.Fatal(e)
	}
	run, e = a.pluginStore.claimRunForInstance(ctx, run.InstanceID)
	if e != nil {
		t.Fatal(e)
	}
	started := time.Now()
	a.executePluginRun(ctx, run)
	result, e := a.pluginStore.Run(ctx, run.ID)
	elapsed := time.Since(started)
	t.Logf("actual native HTTP completions=%d elapsed=%s state=%s code=%s", completed.Load(), elapsed, result.State, result.ErrorCode)
	if e != nil || result.State != "succeeded" || completed.Load() != 7 || elapsed < 2*time.Minute {
		t.Fatal("native source scan was cut by worker handshake deadline", e)
	}
}
