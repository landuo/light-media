package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOriginalTransferStorageActualSDKPublication(t *testing.T) {
	for _, test := range []struct {
		method, mode string
		success      bool
	}{{"hardlink", "", true}, {"copy", "", true}, {"hardlink", "false", false}, {"copy", "error", false}, {"hardlink", "lie", false}, {"copy", "corrupt", false}, {"hardlink", "select-error", true}} {
		t.Run(test.method+"-"+test.mode, func(t *testing.T) {
			a, source, target := importTestApp(t)
			identity := importFixture(t, a, "movie")
			input := filepath.Join(source, "Owned.2024.mkv")
			importWrite(t, input, []byte("owned storage real video"))
			wire := json.RawMessage(`{"id":123,"title":"测试影片","original_title":"Test Film","release_date":"2024-01-01","overview":"owned","genres":[],"credits":{"cast":[],"crew":[]},"external_ids":{},"images":{}}`)
			full, err := sourceTMDBMediaInfo(wire, "movie", "image.tmdb.org", "")
			if err != nil {
				t.Fatal(err)
			}
			failures := map[string]bool{}
			if test.mode == "select-error" {
				failures["storage.operation"] = true
			}
			id := installOwnedTransferObserver(t, a, map[string]map[string]any{}, failures, map[string]any{"storage": true, "storage_root": filepath.Dir(source), "storage_mode": test.mode, "mediainfo": full})
			plan, err := a.previewImport(context.Background(), ImportRequest{MediaType: identity.MediaType, TMDBID: identity.ID, RootID: "root", Mode: test.method, Scraping: importFlag(false), Files: []ImportFileRequest{{Path: input}}})
			if err != nil || len(plan.Files) != 1 || plan.Files[0].Status != "ready" {
				t.Fatal("actual plugin recognition and preview", plan, err)
			}
			job := overwriteStart(t, a, plan)
			err = a.runImport(context.Background(), job, plan.ID, nil)
			a.wg.Wait()
			if (err == nil) != test.success {
				t.Fatal("actual storage outcome", err)
			}
			bytes, readErr := os.ReadFile(plan.Files[0].Target)
			if test.success && (readErr != nil || string(bytes) != "owned storage real video") {
				t.Fatal("actual storage bytes", readErr, string(bytes))
			}
			if !test.success && !os.IsNotExist(readErr) {
				t.Fatal("failed provider published an unverified file", readErr)
			}
			if sourceBytes, err := os.ReadFile(input); err != nil || string(sourceBytes) != "owned storage real video" {
				t.Fatal("provider changed source")
			}
			observed := ownedInputAPI(t, a, "GET", "/api/v1/plugin/"+id+"/observations", "")
			var calls []struct {
				Type string
				Data json.RawMessage
			}
			if observed.Code != 200 || json.Unmarshal(observed.Body.Bytes(), &calls) != nil {
				t.Fatal("actual storage trace")
			}
			found := map[string]int{}
			for _, call := range calls {
				found[call.Type]++
				if call.Type == "storage."+originalTransferType(test.method) {
					var invocation struct {
						Path            string
						PublicationPath string  `json:"publication_path"`
						NewName         *string `json:"new_name"`
					}
					if json.Unmarshal(call.Data, &invocation) != nil || !within(target, invocation.PublicationPath) || !strings.HasPrefix(filepath.Base(invocation.PublicationPath), ".light-media-") {
						t.Fatal("provider did not receive actual guarded staging path")
					}
					if test.method == "hardlink" && invocation.Path != plan.Files[0].Target || test.method == "copy" && (invocation.Path != filepath.Dir(plan.Files[0].Target) || invocation.NewName == nil || *invocation.NewName != filepath.Base(plan.Files[0].Target)) {
						t.Fatal("fixed local StorageBase logical args")
					}
				}
			}
			if found["storage.operation"] != 2 || found["storage.get_folder"] != 1 || found["storage.get_item_strict"] != 1 || found["storage."+originalTransferType(test.method)] != 1 {
				t.Fatal("real selected storage functions were not used", found)
			}
		})
	}
}

func TestOriginalTransferStorageRejectsStoppedObjectAndRebindsAfterReload(t *testing.T) {
	a, source, target := importTestApp(t)
	id := installOwnedTransferObserver(t, a, nil, nil, map[string]any{"storage": true, "storage_root": filepath.Dir(source)})
	selected, err := a.originalSelectTransferStorage(context.Background())
	if err != nil || selected == nil {
		t.Fatal("actual selected object", err)
	}
	old := selected.object
	current, err := a.reloadOriginalPlugin(context.Background(), id)
	if err != nil || current == old {
		t.Fatal("actual reload", err)
	}
	a.wg.Wait()
	path := filepath.Join(target, "Stopped")
	if _, err := selected.call(context.Background(), "get_folder", nil, path, nil, ""); err == nil {
		t.Fatal("stopped object retained a write callback")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("stale provider created a directory")
	}
	rebound, err := a.originalSelectTransferStorage(context.Background())
	if err != nil || rebound == nil || rebound.object != current {
		t.Fatal("rebind after real reload", err)
	}
	if _, err := rebound.call(context.Background(), "get_folder", nil, path, nil, ""); err != nil {
		t.Fatal("current provider", err)
	}
	if stat, err := os.Stat(path); err != nil || !stat.IsDir() {
		t.Fatal("current actual method not effective", err)
	}
}
