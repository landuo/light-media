package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// MoviePilot overwrites a destination file during a directory transfer even if
// another earlier transfer produced it. The exact recorded leaf is taken over
// and its previous asset is superseded; unrelated files of that asset remain.
func TestDirectoryMergeTakesOverAnotherAssetsLeaf(t *testing.T) {
	a, object, unused, cfg := originalTransferModuleFixture(t, "copy", "")
	cfg["modules"].(map[string]string)["transfer"] = "module_execute"
	cfg["result"] = nil
	raw, _ := json.Marshal(cfg)
	if _, err := a.pluginStore.saveOriginalPluginConfiguration(t.Context(), object.installation, raw); err != nil {
		t.Fatal(err)
	}
	if err := a.initOriginalLoadedPlugin(t.Context(), object.installation.InstanceID); err != nil {
		t.Fatal(err)
	}
	login := callTest(t, a.Handler(), "POST", "/api/auth/login", `{"username":"admin","password":"a-strong-test-password"}`, nil, "")
	if login.Code != 200 {
		t.Fatal("fixture login", login.Code)
	}
	overwriteRule(a, filepath.Dir(unused.Files[0].Source), a.Settings().Roots[0].Path, "never", true)
	a.config.Directories[0].MediaType = "movie"
	if err := a.saveSettings(t.Context(), a.Settings()); err != nil {
		t.Fatal(err)
	}
	root := a.Settings().Roots[0].Path

	single := manualOrganizeRequest{Paths: []string{unused.Files[0].Source}, TargetPath: root, Mode: "copy", MediaType: "movie", TMDBID: 123, Scraping: importFlag(false), Preview: true}
	firstPreview, err := a.manualOrganize(t.Context(), single)
	if err != nil || firstPreview.Failed != 0 || len(firstPreview.Items) != 1 {
		t.Fatal("single-file preview", firstPreview, err)
	}
	single.Preview = false
	first, err := a.manualOrganize(t.Context(), single)
	if err != nil || first.Failed != 0 || first.Submitted != 1 {
		t.Fatal("single-file submission", first, err)
	}
	firstJob, err := scanJob(a.Store.DB.QueryRow(`SELECT `+jobColumns+` FROM jobs WHERE id=?`, first.Items[0].JobID))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.Store.DB.Exec(`UPDATE jobs SET state='running',generation=generation+1 WHERE id=?`, firstJob.ID); err != nil {
		t.Fatal(err)
	}
	if firstJob, err = scanJob(a.Store.DB.QueryRow(`SELECT `+jobColumns+` FROM jobs WHERE id=?`, firstJob.ID)); err != nil {
		t.Fatal(err)
	}
	if err = a.runImport(t.Context(), firstJob, first.Items[0].PlanID, nil); err != nil {
		t.Fatal("single-file import", err)
	}
	if _, err = a.finishJob(t.Context(), firstJob, "succeeded", "", "", 0); err != nil {
		t.Fatal(err)
	}
	var firstPlan ImportPlan
	if err = a.Store.Get(t.Context(), "import_plan", first.Items[0].PlanID, &firstPlan); err != nil {
		t.Fatal(err)
	}
	if err = a.Store.Get(t.Context(), "import_plan", firstPlan.ID, &firstPlan); err != nil {
		t.Fatal(err)
	}
	oldTarget := firstPreview.Items[0].Target
	oldAsset := firstPlan.Files[0].AssetID
	if oldAsset == "" {
		// The plan was reloaded before the run assigned the asset.
		if err = a.Store.Get(t.Context(), "import_plan", firstPlan.ID, &firstPlan); err != nil {
			t.Fatal(err)
		}
		oldAsset = firstPlan.Files[0].AssetID
	}

	dir := filepath.Join(filepath.Dir(unused.Files[0].Source), "Owned.2024.BluRay")
	importWrite(t, filepath.Join(dir, filepath.Base(oldTarget)), []byte("directory replacement bytes"))
	importWrite(t, filepath.Join(dir, "BDMV/index.bdmv"), []byte("directory index"))
	if err := os.Mkdir(filepath.Join(dir, "CERTIFICATE"), 0750); err != nil {
		t.Fatal(err)
	}
	dirRequest := manualOrganizeRequest{Paths: []string{dir}, TargetPath: root, Mode: "copy", MediaType: "movie", TMDBID: 123, Scraping: importFlag(false), Preview: true}
	dirPreview, err := a.manualOrganize(t.Context(), dirRequest)
	if err != nil || dirPreview.Failed != 0 || len(dirPreview.Items) != 1 {
		t.Fatal("directory preview", dirPreview, err)
	}
	dirRequest.Preview = false
	second, err := a.manualOrganize(t.Context(), dirRequest)
	if err != nil || second.Failed != 0 || second.Submitted != 1 {
		t.Fatal("directory submission", second, err)
	}
	secondJob, err := scanJob(a.Store.DB.QueryRow(`SELECT `+jobColumns+` FROM jobs WHERE id=?`, second.Items[0].JobID))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.Store.DB.Exec(`UPDATE jobs SET state='running',generation=generation+1 WHERE id=?`, secondJob.ID); err != nil {
		t.Fatal(err)
	}
	if secondJob, err = scanJob(a.Store.DB.QueryRow(`SELECT `+jobColumns+` FROM jobs WHERE id=?`, secondJob.ID)); err != nil {
		t.Fatal(err)
	}
	if err = a.runImport(t.Context(), secondJob, second.Items[0].PlanID, nil); err != nil {
		t.Fatal("cross-asset directory import", err)
	}
	if _, err = a.finishJob(t.Context(), secondJob, "succeeded", "", "", 0); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(oldTarget); err != nil || string(data) != "directory replacement bytes" {
		t.Fatal("taken-over leaf", err, string(data))
	}
	page := historyParityReadPage(t, a, "")
	if page.Total != 2 {
		t.Fatalf("takeover histories %#v", page.Items)
	}
	var prior Asset
	if err := a.Store.Get(t.Context(), "asset", oldAsset, &prior); err != nil || prior.State != "superseded" {
		t.Fatalf("old asset not superseded %#v %v", prior, err)
	}
	var slots int
	if err := a.Store.DB.QueryRow(`SELECT COUNT(*) FROM asset_slots WHERE asset_id=?`, oldAsset).Scan(&slots); err != nil || slots != 0 {
		t.Fatal("old asset slots", slots, err)
	}
	var survivor managedFile
	if getErr := a.Store.Get(t.Context(), "managed_file", tokenHash(oldTarget), &survivor); getErr == nil {
		t.Fatalf("old leaf record survived the takeover %#v", survivor)
	}
}
