package app

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

func browserSoftlinkRoot(t *testing.T, a *App, path string) browserRoot {
	t.Helper()
	for _, root := range a.browserRoots() {
		if within(root.Path, path) {
			return root
		}
	}
	t.Fatal("actual authorized browser root missing")
	return browserRoot{}
}
func browserSoftlinkItems(t *testing.T, a *App, cookie *http.Cookie, root browserRoot, path string) map[string]browserEntry {
	t.Helper()
	r := callTest(t, a.Handler(), "GET", "/api/files/list?"+url.Values{"root_id": {root.ID}, "path": {path}}.Encode(), "", cookie, "")
	var response struct {
		Data struct{ Items []browserEntry }
	}
	if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &response) != nil {
		t.Fatal("actual list", r.Code, r.Body.String())
	}
	out := map[string]browserEntry{}
	for _, entry := range response.Data.Items {
		out[entry.Path] = entry
	}
	return out
}

func TestBrowserOwnedSoftlinkActualReadRecognizeAndReorganize(t *testing.T) {
	a, prior, asset, contents := softlinkLifecycleFixture(t, "softlink")
	cookie := setupTestAdmin(t, a)
	root := browserSoftlinkRoot(t, a, asset.Path)
	unknown := filepath.Join(filepath.Dir(asset.Path), "Unowned.mkv")
	if err := os.Symlink(prior.Files[0].Source, unknown); err != nil {
		t.Fatal(err)
	}
	items := browserSoftlinkItems(t, a, cookie, root, filepath.Dir(asset.Path))
	entry := items[asset.Path]
	if entry.Type != "file" || !entry.Softlink || entry.Broken || entry.Size != int64(len(contents[prior.Files[0].Source])) || items[unknown].Type != "symlink" || items[unknown].Softlink {
		t.Fatal("owned and unknown entry projection", items)
	}
	query := url.Values{"root_id": {root.ID}, "path": {asset.Path}}.Encode()
	r := callTest(t, a.Handler(), "GET", "/api/files/content?"+query, "", cookie, "")
	if r.Code != 200 || !bytes.Equal(r.Body.Bytes(), contents[prior.Files[0].Source]) {
		t.Fatal("exact owned link download", r.Code, r.Body.String())
	}
	raw, _ := json.Marshal(map[string]any{"path": asset.Path})
	r = callTest(t, a.Handler(), "POST", "/api/files/recognize", string(raw), cookie, "")
	var recognized struct {
		Data struct {
			Media struct {
				ID int64 `json:"id"`
			}
		}
	}
	if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &recognized) != nil || recognized.Data.Media.ID != asset.Media.ID {
		t.Fatal("known media details", r.Code, r.Body.String())
	}
	raw, _ = json.Marshal(map[string]any{"action": "delete", "root_id": root.ID, "paths": []string{asset.Path}})
	r = callTest(t, a.Handler(), "POST", "/api/files/operation-preview", string(raw), cookie, "")
	if r.Code == 200 {
		t.Fatal("interactive operation bypassed registered ownership")
	}
	if _, err := browserStat(asset.Path); err == nil {
		t.Fatal("generic browserStat started following links")
	}
	if _, err := browserManifest(t.Context(), asset.Path); err == nil {
		t.Fatal("generic browser manifest started following links")
	}
	target := filepath.Join(a.Settings().Roots[0].Path, "BrowserCopy")
	if err := os.MkdirAll(target, 0750); err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(manualOrganizeRequest{Paths: []string{asset.Path}, Mode: "copy", TargetPath: target, MediaType: "movie", TMDBID: asset.Media.ID, Scraping: importFlag(false)})
	r = callTest(t, a.Handler(), "POST", "/api/files/organize", string(raw), cookie, "")
	var queued struct{ Data manualOrganizeOutput }
	if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &queued) != nil || queued.Data.Failed != 0 || queued.Data.Submitted != 1 {
		t.Fatal("actual linked file manual organize", r.Code, r.Body.String())
	}
	runSoftlinkDirectoryJob(t, a, queued.Data.Items[0].JobID)
	current, err := a.transferHistoryItem(t.Context(), prior.Files[0].ID)
	if err != nil || current.ID != prior.Files[0].ID || current.ResourceSource != asset.SourcePath || current.Mode != "copy" || !within(target, current.Target) || current.Status != "success" {
		t.Fatal("linked path lost original resource/history", current, err)
	}
	if data, e := os.ReadFile(current.Target); e != nil || !bytes.Equal(data, contents[prior.Files[0].Source]) {
		t.Fatal("actual selected copy", e)
	}
	for path, data := range contents {
		if got, e := os.ReadFile(path); e != nil || !bytes.Equal(got, data) {
			t.Fatal("browser read/reorganize changed source", path, e)
		}
	}
}

func TestBrowserOwnedSoftlinkBrokenAndReplacedEntries(t *testing.T) {
	for _, branch := range []string{"broken", "new-inode"} {
		t.Run(branch, func(t *testing.T) {
			a, plan, asset, _ := softlinkLifecycleFixture(t, "softlink")
			cookie := setupTestAdmin(t, a)
			root := browserSoftlinkRoot(t, a, asset.Path)
			if branch == "broken" {
				if err := os.Remove(plan.Files[0].Source); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.Remove(asset.Path); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(plan.Files[0].Source, asset.Path); err != nil {
					t.Fatal(err)
				}
			}
			entry := browserSoftlinkItems(t, a, cookie, root, filepath.Dir(asset.Path))[asset.Path]
			if branch == "broken" && (!entry.Softlink || !entry.Broken || entry.Type != "file") {
				t.Fatal("known broken entry lost", entry)
			}
			if branch == "new-inode" && (entry.Softlink || entry.Type != "symlink") {
				t.Fatal("same-text replacement was adopted", entry)
			}
			r := callTest(t, a.Handler(), "GET", "/api/files/content?"+url.Values{"root_id": {root.ID}, "path": {asset.Path}}.Encode(), "", cookie, "")
			if r.Code == 200 {
				t.Fatal("invalid link served content")
			}
		})
	}
}

func TestBrowserOwnedDirectoryLinkActualSDKListingAndContent(t *testing.T) {
	a, _, asset, contents := softlinkDirectoryLifecycleFixture(t, false, false)
	login := callTest(t, a.Handler(), "POST", "/api/auth/login", `{"username":"admin","password":"a-strong-test-password"}`, nil, "")
	if login.Code != 200 {
		t.Fatal("existing admin login", login.Code)
	}
	var cookie *http.Cookie
	for _, value := range login.Result().Cookies() {
		if value.Name == "lm_session" {
			cookie = value
		}
	}
	if cookie == nil {
		t.Fatal("browser session missing")
	}
	manifest := installOrderWorker(t, a, cookie, os.Getenv("OWNED_MODULE_OVERRIDE_BINARY"))
	response := callTest(t, a.Handler(), "PUT", "/api/plugins/"+manifest.PluginID, `{"enabled":true,"modules":{"list_files":"module_execute"},"result":null}`, cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	a.wg.Wait()
	leaf := filepath.Join(asset.Path, "BDMV", "STREAM", "00001.m2ts")
	root := browserSoftlinkRoot(t, a, leaf)
	entry := browserSoftlinkItems(t, a, cookie, root, filepath.Dir(leaf))[leaf]
	if !entry.Softlink || entry.Type != "file" || entry.Broken || entry.Size != int64(len(contents[filepath.Join(asset.SourcePath, "BDMV", "STREAM", "00001.m2ts")])) {
		t.Fatal("SDK list did not preserve exact aggregate member", entry)
	}
	r := callTest(t, a.Handler(), "GET", "/api/files/content?"+url.Values{"root_id": {root.ID}, "path": {leaf}}.Encode(), "", cookie, "")
	if r.Code != 200 || !bytes.Equal(r.Body.Bytes(), contents[filepath.Join(asset.SourcePath, "BDMV", "STREAM", "00001.m2ts")]) {
		t.Fatal("actual aggregate member read", r.Code, r.Body.String())
	}
}
