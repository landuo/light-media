package app

import (
	"context"
	"os"
	"testing"

	"light-media/internal/catalog"
	"light-media/internal/plugins"
)

func TestOriginalHostBusinessHistoryActualSDKRecordDeleteRetainsPhysicalFiles(t *testing.T) {
	a, item := historyRemovalFixture(t)
	_, worker := hostBusinessFixture(t, a)
	ctx := context.Background()
	var history plugins.TransferHistoryResultV1
	for _, request := range []plugins.TransferHistoryRequestV1{{ID: item.ID}, {Source: item.Source}, {Target: item.Target}} {
		if err := hostBusinessCall(t, worker, plugins.TransferHistoryReadCapability, request, &history); err != nil || history.Item == nil || history.Item.ID != item.ID || history.Item.Source != item.Source || history.Item.Target != item.Target || !history.Item.Status {
			t.Fatal("source/dest/id history query", err, history)
		}
	}
	if err := hostBusinessCall(t, worker, plugins.TransferHistoryReadCapability, plugins.TransferHistoryRequestV1{Source: "/missing"}, &history); err != nil || history.Item != nil {
		t.Fatal("missing path fabricated record", err)
	}
	var mutation plugins.HostMutationV1
	if _, err := a.Store.DB.Exec(`CREATE TRIGGER owned_host_history_rollback BEFORE INSERT ON records WHEN NEW.kind='transfer_history_hidden' BEGIN SELECT RAISE(ABORT,'owned failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := hostBusinessCall(t, worker, plugins.TransferHistoryDeleteCapability, plugins.TransferHistoryDeleteV1{ID: item.ID}, &mutation); err == nil {
		t.Fatal("history SQL failure swallowed")
	}
	if err := hostBusinessCall(t, worker, plugins.TransferHistoryReadCapability, plugins.TransferHistoryRequestV1{ID: item.ID}, &history); err != nil || history.Item == nil {
		t.Fatal("failed delete hid row", err)
	}
	if _, err := a.Store.DB.Exec(`DROP TRIGGER owned_host_history_rollback`); err != nil {
		t.Fatal(err)
	}
	if err := hostBusinessCall(t, worker, plugins.TransferHistoryDeleteCapability, plugins.TransferHistoryDeleteV1{ID: item.ID}, &mutation); err != nil || !mutation.Changed {
		t.Fatal("record-only delete", err)
	}
	for _, path := range []string{item.Source, item.Target} {
		if _, err := os.Stat(path); err != nil {
			t.Fatal("record delete touched physical file", path, err)
		}
	}
	var plan ImportPlan
	if err := a.Store.Get(ctx, "import_plan", item.PlanID, &plan); err != nil {
		t.Fatal("recovery plan erased", err)
	}
	if err := hostBusinessCall(t, worker, plugins.TransferHistoryReadCapability, plugins.TransferHistoryRequestV1{Source: item.Source}, &history); err != nil || history.Item != nil {
		t.Fatal("deleted history remained", err)
	}
	if err := hostBusinessCall(t, worker, plugins.TransferHistoryDeleteCapability, plugins.TransferHistoryDeleteV1{ID: item.ID}, &mutation); err != nil || mutation.Changed {
		t.Fatal("delete missing row differs", err)
	}
	dir := a.Dir
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	cold, err := New(Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer cold.Close()
	if err = cold.initializeOriginalPlugins(ctx); err != nil {
		t.Fatal(err)
	}
	cold.wg.Wait()
	v, ok := cold.pluginRunningInstances.Load("OwnedHostBusiness")
	if !ok {
		t.Fatal("cold worker missing")
	}
	if err = hostBusinessCall(t, v.(*originalPluginInstance), plugins.TransferHistoryReadCapability, plugins.TransferHistoryRequestV1{Source: item.Source}, &history); err != nil || history.Item != nil {
		t.Fatal("cold history deletion lost", err)
	}
	for _, path := range []string{item.Source, item.Target} {
		if _, err := os.Stat(path); err != nil {
			t.Fatal("cold record delete replay touched file", path, err)
		}
	}
}

func TestOriginalHostBusinessDownloadHistoryActualSDKSelectsLatestHash(t *testing.T) {
	a := newTestApp(t)
	_, worker := hostBusinessFixture(t, a)
	ctx := context.Background()
	hash := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	first := Intent{ID: "owned-first", Hash: hash, Media: catalog.Media{ID: 42, Title: "First", MediaType: "movie"}, DownloadPath: "/owned/download", State: "completed", CreatedAt: now()}
	first.CreatedAt = "2026-10-30T00:00:00Z"
	second := first
	second.ID = "owned-second"
	second.Media.Title = "Second"
	second.CreatedAt = now()
	if err := a.Store.Put(ctx, "intent", first.ID, first); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Store.DB.Exec(`UPDATE records SET updated_at='2026-10-30T00:00:00Z' WHERE kind='intent' AND id=?`, first.ID); err != nil {
		t.Fatal(err)
	}
	if err := a.Store.Put(ctx, "intent", second.ID, second); err != nil {
		t.Fatal(err)
	}
	var got plugins.DownloadHistoryResultV1
	if err := hostBusinessCall(t, worker, plugins.DownloadHistoryReadCapability, plugins.DownloadHistoryRequestV1{Hash: hash}, &got); err != nil || got.Item == nil || got.Item.ID != "owned-second" || got.Item.Title != "Second" {
		t.Fatal("latest hash", err, got)
	}
	if err := hostBusinessCall(t, worker, plugins.DownloadHistoryReadCapability, plugins.DownloadHistoryRequestV1{Hash: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}, &got); err != nil || got.Item != nil {
		t.Fatal("missing hash", err)
	}
	if err := hostBusinessCall(t, worker, plugins.DownloadHistoryReadCapability, plugins.DownloadHistoryRequestV1{Hash: "all"}, &got); err == nil {
		t.Fatal("hash selector widened")
	}
}
