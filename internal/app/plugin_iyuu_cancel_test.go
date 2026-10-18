package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"light-media/internal/download"
	"light-media/internal/plugins"
	iyuu "light-media/internal/plugins/iyuuautoseed"
)

func TestIYUUActualNativeParentCancellationPreservesColdEvidence(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	requested := make(chan struct{})
	cleanupHTTP := make(chan struct{})
	var once sync.Once
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		calls.Add(1)
		once.Do(func() { close(requested) })
		select {
		case <-r.Context().Done():
		case <-cleanupHTTP:
		}
	}))
	defer server.Close()
	defer close(cleanupHTTP)
	a.config.Downloaders = []DownloaderConfig{{Config: download.Config{ID: "owned-cancel", Kind: "qbittorrent", BaseURL: server.URL}, Name: "Owned cancel"}}
	if e := a.saveSettings(ctx, a.Settings()); e != nil {
		t.Fatal(e)
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
	run := uninstallInstall(t, a, "OwnedIYUUCancel", iyuu.Manifest(), map[string]any{"enabled": false, "token": "owned-synthetic-token", "downloaders": []any{"Owned cancel"}, "sites": []any{}, "size": 0}, true)
	a.executePluginRun(ctx, run)
	if _, e = a.pluginStore.EnqueueRun(ctx, run.InstanceID, "run", nil); e != nil {
		t.Fatal(e)
	}
	run, e = a.pluginStore.claimRunForInstance(ctx, run.InstanceID)
	if e != nil {
		t.Fatal(e)
	}
	parent, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		select {
		case <-requested:
			cancel()
		case <-parent.Done():
		}
	}()
	started := time.Now()
	a.executePluginRun(parent, run)
	if time.Since(started) > 5*time.Second || calls.Load() != 1 {
		t.Fatal("native parent cancellation did not stop HTTP promptly", time.Since(started), calls.Load())
	}
	run, e = a.pluginStore.Run(ctx, run.ID)
	if e != nil || run.State != "reconcile_pending" {
		t.Fatal("cancellation lost conservative run state", run.State, e)
	}
	record, _, e := a.pluginStore.readDeferred(ctx, a.Store.DB, run)
	if e != nil || record.Phase != "uncertain" {
		t.Fatal("cancellation lost original deferred evidence", record.Phase, e)
	}
	iyuuAssertUnknownConfigWritersBlocked(t, a, run.InstanceID)
	dir := a.Dir
	if e = a.Close(); e != nil {
		t.Fatal(e)
	}
	b, e := New(Options{DataDir: dir})
	if e != nil {
		t.Fatal("unconfirmed IYUU blocked whole application cold startup", e)
	}
	defer b.Close()
	recovered, e := b.pluginStore.Run(ctx, run.ID)
	if e != nil || recovered.State != "reconcile_pending" {
		t.Fatal("cold startup discarded pending evidence", recovered.State, e)
	}
	if e = b.initializeNativePlugins(ctx); e != nil {
		t.Fatal(e)
	}
	if _, e = b.pluginStore.SaveConfig(ctx, run.InstanceID, recovered.ConfigRevision, map[string]any{"enabled": false}); !errors.Is(e, iyuu.ErrUnconfirmed) {
		t.Fatal("cold config writer crossed canceled source evidence", e)
	}
	if calls.Load() != 1 {
		t.Fatal("cold startup replayed external HTTP", calls.Load())
	}
}
