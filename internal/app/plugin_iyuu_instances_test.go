package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"light-media/internal/plugins"
	iyuu "light-media/internal/plugins/iyuuautoseed"
)

func TestIYUUCustomSiteLookupDoesNotReadSiblingConfig(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	for _, id := range []string{"owned-iyuu-a", "owned-iyuu-b"} {
		uninstallInstall(t, a, id, iyuu.Manifest(), map[string]any{"enabled": false, "sites": []any{}}, false)
	}
	custom, err := a.iyuuCustomSites(ctx)
	if err != nil || len(custom) != 0 {
		t.Fatal("unrelated siblings made empty custom-site lookup ambiguous", custom, err)
	}
	installSiteControlPeerFixture(t, a, "CustomSites", map[string]any{"enabled": true, "sites": []map[string]any{{"id": "owned-custom", "name": "Owned custom", "url": "https://owned.invalid/"}}})
	custom, err = a.iyuuCustomSites(ctx)
	if err != nil || len(custom) != 1 || custom[0]["id"] != "owned-custom" {
		t.Fatal("exact unique custom peer was not returned", custom, err)
	}
	installation, err := a.pluginStore.Installation(ctx, "CustomSites")
	if err != nil {
		t.Fatal(err)
	}
	var manifest plugins.Manifest
	if err = json.Unmarshal(installation.Manifest, &manifest); err != nil {
		t.Fatal(err)
	}
	uninstallInstall(t, a, "owned-custom-second", manifest, map[string]any{"enabled": true, "sites": []any{}}, false)
	_, err = a.iyuuCustomSites(ctx)
	var api *APIError
	if !errors.As(err, &api) || api.Code != "PLUGIN_REFERENCE_AMBIGUOUS" {
		t.Fatal("ambiguous CustomSites must still fail closed", err)
	}
}

func TestIYUUActualServerWorkerSiblingInstancesIsolation(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	transferServerBinary.once.Do(func() { transferServerBinary.path, transferServerBinary.err = buildTransferFixture(true) })
	if transferServerBinary.err != nil {
		t.Fatal(transferServerBinary.err)
	}
	a.pluginCommand = func(id string) (plugins.Command, error) {
		return plugins.Command{Path: transferServerBinary.path, Args: []string{"--plugin-worker", id}}, nil
	}
	ids := []string{"owned-iyuu-first", "owned-iyuu-second"}
	runs := []PluginRun{}
	for i, id := range ids {
		runs = append(runs, uninstallInstall(t, a, id, iyuu.Manifest(), map[string]any{"enabled": false, "onlyonce": false, "clearcache": false, "token": "owned-synthetic", "sites": []any{}, "downloaders": []any{}, "size": i + 1, "labelsafterseed": id}, true))
	}
	for _, run := range runs {
		a.executePluginRun(ctx, run)
		got, err := a.pluginStore.Run(ctx, run.ID)
		if err != nil || got.State != "succeeded" {
			t.Fatal("real worker with sibling failed", run.InstanceID, got.State, got.ErrorCode, err)
		}
	}
	first, err := a.pluginStore.Config(ctx, ids[0])
	if err != nil {
		t.Fatal(err)
	}
	second, err := a.pluginStore.Config(ctx, ids[1])
	if err != nil {
		t.Fatal(err)
	}
	for i, stored := range []PluginStoredValue{first, second} {
		var values map[string]any
		if json.Unmarshal(stored.Value, &values) != nil || values["size"] != float64(i+1) || values["labelsafterseed"] != ids[i] {
			t.Fatal("instance config crossed boundary", ids[i], string(stored.Value))
		}
	}
	var update map[string]any
	if err = json.Unmarshal(first.Value, &update); err != nil {
		t.Fatal(err)
	}
	update["size"] = 13
	_, run, err := a.pluginStore.SaveConfigAndQueue(ctx, ids[0], first.Revision, update)
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := a.pluginStore.claimRunForInstance(ctx, ids[0])
	if err != nil || claimed.ID != run.ID {
		t.Fatal("claim saved instance", err)
	}
	a.executePluginRun(ctx, claimed)
	got, err := a.pluginStore.Run(ctx, claimed.ID)
	if err != nil || got.State != "succeeded" {
		t.Fatal("saved real worker failed", got.State, got.ErrorCode, err)
	}
	changed, err := a.pluginStore.Config(ctx, ids[0])
	if err != nil {
		t.Fatal(err)
	}
	var saved map[string]any
	if json.Unmarshal(changed.Value, &saved) != nil || saved["size"] != float64(13) {
		t.Fatal("first save did not persist", string(changed.Value))
	}
	after, err := a.pluginStore.Config(ctx, ids[1])
	if err != nil || after.Revision != second.Revision || !bytes.Equal(after.Value, second.Value) {
		t.Fatal("saving first changed second", err)
	}
}
