package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"
)

func TestOriginalFolderFailedUninstallSourceWarmRetryCold(t *testing.T) {
	var oracle struct {
		Cases []struct {
			Name  string
			Input struct {
				Source  string
				Folders map[string]json.RawMessage
			}
			WarmAfterFailure map[string]json.RawMessage `json:"warm_after_failure"`
			DiskAfterFailure map[string]json.RawMessage `json:"disk_after_failure"`
			WarmAfterRetry   map[string]json.RawMessage `json:"warm_after_retry"`
			DiskAfterRetry   map[string]json.RawMessage `json:"disk_after_retry"`
			Cold             map[string]json.RawMessage
		}
	}
	raw, err := os.ReadFile("../../testdata/plugin-uninstall-folder-20261030/source-observations.json")
	if err != nil || json.Unmarshal(raw, &oracle) != nil || len(oracle.Cases) != 3 {
		t.Fatal("original source observations", err)
	}
	for _, c := range oracle.Cases {
		t.Run(c.Name, func(t *testing.T) {
			a := newTestApp(t)
			ctx := context.Background()
			saved, err := a.pluginStore.SaveOrganization(ctx, PluginOrganization{Folders: c.Input.Folders, Order: json.RawMessage(`["unchanged-order"]`)})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = a.Store.DB.Exec(`CREATE TRIGGER owned_folder_fail BEFORE UPDATE ON plugin_organization BEGIN SELECT RAISE(ABORT,'owned folder write failure'); END`); err != nil {
				t.Fatal(err)
			}
			if err = a.removeOriginalPluginFromFolders(ctx, c.Input.Source); err == nil {
				t.Fatal("real SQLite failure missing")
			}
			check := func(store *PluginStore, disk bool, want map[string]json.RawMessage) {
				t.Helper()
				var got PluginOrganization
				var e error
				if disk {
					got, e = store.organization(ctx, store.store.DB)
				} else {
					got, e = store.Organization(ctx)
				}
				if e != nil || !originalBaseJSONEqual(mustCleanSeedJSON(got.Folders), mustCleanSeedJSON(want)) {
					t.Fatalf("disk=%v folders=%s want=%s err=%v", disk, mustCleanSeedJSON(got.Folders), mustCleanSeedJSON(want), e)
				}
				if string(got.Order) != string(saved.Order) {
					t.Fatal("folder operation changed user order")
				}
			}
			check(a.pluginStore, false, c.WarmAfterFailure)
			check(a.pluginStore, true, c.DiskAfterFailure)
			if _, err = a.Store.DB.Exec(`DROP TRIGGER owned_folder_fail`); err != nil {
				t.Fatal(err)
			}
			if err = a.removeOriginalPluginFromFolders(ctx, c.Input.Source); err != nil {
				t.Fatal(err)
			}
			check(a.pluginStore, false, c.WarmAfterRetry)
			check(a.pluginStore, true, c.DiskAfterRetry)
			cold, err := NewPluginStore(ctx, a.Store)
			if err != nil {
				t.Fatal(err)
			}
			check(cold, false, c.Cold)
		})
	}
}

func TestOriginalFolderCacheOrderOnlyAndExplicitNativeRepair(t *testing.T) {
	a := newTestApp(t)
	cookie := setupTestAdmin(t, a)
	ctx := context.Background()
	before := PluginOrganization{Folders: map[string]json.RawMessage{"owned": json.RawMessage(`["source","clone"]`)}, Order: json.RawMessage(`[]`)}
	saved, err := a.pluginStore.SaveOrganization(ctx, before)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.Store.DB.Exec(`CREATE TRIGGER owned_folder_fail BEFORE UPDATE ON plugin_organization BEGIN SELECT RAISE(ABORT,'owned failure'); END`); err != nil {
		t.Fatal(err)
	}
	if a.removeOriginalPluginFromFolders(ctx, "source") == nil {
		t.Fatal("missing SQL failure")
	}
	if _, err = a.Store.DB.Exec(`DROP TRIGGER owned_folder_fail`); err != nil {
		t.Fatal(err)
	}
	// The native projection owns two independent original values in one row.
	// Updating only user order must not repair a failed global folder write.
	response := organizationUIRequest(t, a, cookie, "PUT", map[string]any{"order": []string{"new-order"}})
	if response.Code != 200 {
		t.Fatal(response.Code, response.Body.String())
	}
	hot, err := a.pluginStore.Organization(ctx)
	if err != nil || string(hot.Folders["owned"]) != `["clone"]` {
		t.Fatal(hot, err)
	}
	disk, err := a.pluginStore.organization(ctx, a.Store.DB)
	if err != nil || string(disk.Folders["owned"]) != `["source","clone"]` || string(disk.Order) != `["new-order"]` {
		t.Fatal(disk, err)
	}
	if _, err = a.pluginStore.SaveOrganization(ctx, saved); err == nil {
		t.Fatal("host CAS was bypassed by warm cache")
	}
	// Explicitly saving the old disk folders is a real user mutation, not the
	// absent-member retry. It must replace the newer warm snapshot even though
	// its bytes equal the still-persisted value.
	response = organizationUIRequest(t, a, cookie, "PUT", map[string]any{"folders": before.Folders})
	if response.Code != 200 {
		t.Fatal(response.Code, response.Body.String())
	}
	hot, err = a.pluginStore.Organization(ctx)
	if err != nil || string(hot.Folders["owned"]) != `["source","clone"]` {
		t.Fatal(hot, err)
	}
	hot.Folders["owned"][2] = 'X'
	again, err := a.pluginStore.Organization(ctx)
	if err != nil || string(again.Folders["owned"]) != `["source","clone"]` {
		t.Fatal("getter leaked mutable cache", again, err)
	}
}

func TestOriginalFolderCacheConcurrentOrderWritesKeepFailedWarmValue(t *testing.T) {
	a := newTestApp(t)
	cookie := setupTestAdmin(t, a)
	ctx := context.Background()
	if _, err := a.pluginStore.SaveOrganization(ctx, PluginOrganization{Folders: map[string]json.RawMessage{"owned": json.RawMessage(`["source","clone"]`)}, Order: json.RawMessage(`[]`)}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Store.DB.Exec(`CREATE TRIGGER owned_folder_fail BEFORE UPDATE ON plugin_organization BEGIN SELECT RAISE(ABORT,'owned failure'); END`); err != nil {
		t.Fatal(err)
	}
	if a.removeOriginalPluginFromFolders(ctx, "source") == nil {
		t.Fatal("missing SQL failure")
	}
	if _, err := a.Store.DB.Exec(`DROP TRIGGER owned_folder_fail`); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 16)
	for i := 0; i < 16; i++ {
		go func(i int) {
			body := fmt.Sprintf(`{"order":["order-%d"]}`, i)
			response := callTest(t, a.Handler(), "PUT", "/api/plugins/organization", body, cookie, "")
			if response.Code != 200 {
				done <- fmt.Errorf("order HTTP %d", response.Code)
				return
			}
			hot, e := a.pluginStore.Organization(ctx)
			if e == nil && string(hot.Folders["owned"]) != `["clone"]` {
				e = fmt.Errorf("warm value repaired by order writer")
			}
			done <- e
		}(i)
	}
	for i := 0; i < 16; i++ {
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(15 * time.Second):
			t.Fatal("cache/SQLite lock inversion")
		}
	}
	disk, err := a.pluginStore.organization(ctx, a.Store.DB)
	if err != nil || string(disk.Folders["owned"]) != `["source","clone"]` {
		t.Fatal(disk, err)
	}
}
