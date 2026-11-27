package app

import (
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

func TestFileManagerRetiredLibraryUnderAuthorizedParent(t *testing.T) {
	for _, action := range []string{"rename", "delete"} {
		t.Run(action, func(t *testing.T) {
			a, parent, _ := importTestApp(t)
			ctx := t.Context()
			retired := filepath.Join(parent, "old-library")
			video := filepath.Join(retired, "Show", "episode.mkv")
			importWrite(t, video, []byte("selected library content"))
			stale := filepath.Join(retired, "already-removed.mkv")
			rootID, err := physicalTarget(&a.config, retired, "mixed")
			if err != nil {
				t.Fatal(err)
			}
			for _, asset := range []Asset{
				{ID: "registered", RootID: rootID, Path: video, State: "present"},
				{ID: "stale", RootID: rootID, Path: stale, State: "removed"},
			} {
				if err := a.Store.Put(ctx, "asset", asset.ID, asset); err != nil {
					t.Fatal(err)
				}
			}
			// Removing a directory rule can leave the target in Roots for existing
			// records. The user is operating from the still-authorized parent.
			a.invalidateSettings()
			root := fileBrowserRoot(t, a, parent)
			cookie := setupTestAdmin(t, a)
			list := callTest(t, a.Handler(), "GET", "/api/files/list?"+url.Values{"root_id": {root.ID}, "path": {parent}}.Encode(), "", cookie, "")
			var listing struct {
				Data struct{ Items []browserEntry }
			}
			if err := json.Unmarshal(list.Body.Bytes(), &listing); err != nil || list.Code != 200 {
				t.Fatalf("read parent directory: %d %s", list.Code, list.Body.String())
			}
			found := false
			for _, entry := range listing.Data.Items {
				if entry.Path == retired {
					found = true
					if entry.Protected != "" {
						t.Fatalf("retired library is disabled in file list: %s", entry.Protected)
					}
				}
			}
			if !found {
				t.Fatal("retired library missing from parent listing")
			}
			paths := []string{retired}
			sibling := filepath.Join(parent, "selected.txt")
			importWrite(t, sibling, []byte("selected sibling"))
			if action == "delete" {
				paths = append(paths, sibling)
			}
			input, err := json.Marshal(browserOperationRequest{RootID: root.ID, Action: action, Paths: paths, Path: parent, Name: "renamed-library"})
			if err != nil {
				t.Fatal(err)
			}
			preview := callTest(t, a.Handler(), "POST", "/api/files/operation-preview", string(input), cookie, "")
			var result struct{ Data browserOperation }
			if err := json.Unmarshal(preview.Body.Bytes(), &result); err != nil || preview.Code != 200 || len(result.Data.Items) != len(paths) {
				t.Fatalf("preview for retired library: %d %s", preview.Code, preview.Body.String())
			}
			body, err := json.Marshal(map[string]any{"plan_id": result.Data.ID, "confirm": true})
			if err != nil {
				t.Fatal(err)
			}
			confirm := callTest(t, a.Handler(), "POST", "/api/files/operation-confirm", string(body), cookie, "")
			if confirm.Code != 200 {
				t.Fatalf("confirm for retired library: %d %s", confirm.Code, confirm.Body.String())
			}
			if _, err := os.Lstat(retired); !os.IsNotExist(err) {
				t.Fatalf("original directory still exists: %v", err)
			}
			content, err := os.ReadFile(filepath.Join(result.Data.Items[0].Target, "Show", "episode.mkv"))
			if err != nil || string(content) != "selected library content" {
				t.Fatalf("selected content missing from operation target: %v", err)
			}
			if action == "delete" {
				content, err = os.ReadFile(result.Data.Items[1].Target)
				if err != nil || string(content) != "selected sibling" {
					t.Fatalf("batch did not preserve selected sibling in quarantine: %v", err)
				}
				restore := callTest(t, a.Handler(), "POST", "/api/files/operation-restore", string(body), cookie, "")
				if restore.Code != 200 {
					t.Fatalf("restore retired library: %d %s", restore.Code, restore.Body.String())
				}
				for _, path := range []string{video, sibling} {
					if _, err := os.Stat(path); err != nil {
						t.Fatalf("restore failed for selected item %s: %v", path, err)
					}
				}
			} else if _, err := os.Stat(sibling); err != nil {
				t.Fatalf("rename disturbed unselected sibling: %v", err)
			}
		})
	}
}

func TestFileManagerProtectsConfiguredLibraryRootAndAncestor(t *testing.T) {
	a, parent, _ := importTestApp(t)
	nested := filepath.Join(parent, "mounted")
	library := filepath.Join(nested, "library")
	child := filepath.Join(library, "file.mkv")
	importWrite(t, child, []byte("library content"))
	a.config.Directories = []DirectoryRule{{ID: "current", Enabled: true, LibraryPath: library}}
	// Use the wider mount to address the actual authorized root and app data.
	base := filepath.Dir(parent)
	a.config.AllowedRoots = append(a.config.AllowedRoots, base)
	a.invalidateSettings()
	root := fileBrowserRoot(t, a, base)
	for _, path := range []string{library, nested, parent, a.Dir} {
		_, err := a.previewBrowserOperation(t.Context(), browserOperationRequest{
			Action: "delete", RootID: root.ID, Path: filepath.Dir(path), Paths: []string{path},
		})
		if fileCode(err) != "FILE_PROTECTED" {
			t.Fatalf("configured library boundary %s should be protected: %v", path, err)
		}
	}
	// Re-adding a directory rule between preview and confirmation cancels
	// that earlier approval even when the files themselves haven't changed.
	a.config.Directories = nil
	a.invalidateSettings()
	plan, err := a.previewBrowserOperation(t.Context(), browserOperationRequest{Action: "delete", RootID: root.ID, Path: parent, Paths: []string{nested}})
	if err != nil {
		t.Fatal(err)
	}
	a.config.Directories = []DirectoryRule{{ID: "current", Enabled: true, LibraryPath: library}}
	a.invalidateSettings()
	if _, err := a.confirmBrowserOperation(t.Context(), plan.ID); fileCode(err) != "FILE_PROTECTED" {
		t.Fatalf("confirmation ignored re-added directory rule: %v", err)
	}
	if _, err := os.Stat(child); err != nil {
		t.Fatalf("protected descendant changed: %v", err)
	}
}
