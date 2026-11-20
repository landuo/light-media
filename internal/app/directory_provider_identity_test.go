package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"light-media/internal/releasemeta"
)

func TestAutomaticDirectoryRecognitionUsesGlobalProviderSource(t *testing.T) {
	a, object, unused, cfg := originalTransferModuleFixture(t, "copy", "")
	settings := a.Settings()
	source := "douban"
	settings.RecognitionSource = &source
	settings.Directories = []DirectoryRule{{ID: "automatic-source", Name: "自动来源目录", SourcePath: filepath.Dir(unused.Files[0].Source), LibraryPath: settings.Roots[0].Path, RootID: "root", MediaType: "movie", Mode: "copy", MonitorType: "monitor", Enabled: true}}
	if err := a.saveSettings(t.Context(), settings); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(filepath.Dir(unused.Files[0].Source), "Directory.Source.2024.mkv")
	importWrite(t, path, []byte("actual automatic directory source"))
	options, err := a.globalRecognitionOptions(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	parser, err := releasemeta.NewMetaInfoParser(releasemeta.MetaInfoEnvironment{Options: options})
	if err != nil {
		t.Fatal(err)
	}
	meta, err := parser.ParsePath(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	cfg["modules"].(map[string]string)["recognize_media"] = "module_execute"
	delete(cfg["modules"].(map[string]string), "transfer")
	cfg["result"] = nil
	cfg["recognize_results"] = map[string]json.RawMessage{
		"name:" + meta.Name(): historyProviderModel(t, "douban", "704", "目录来源电影"),
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.pluginStore.saveOriginalPluginConfiguration(t.Context(), object.installation, raw); err != nil {
		t.Fatal(err)
	}
	if err = a.initOriginalLoadedPlugin(t.Context(), object.installation.InstanceID); err != nil {
		t.Fatal(err)
	}
	parsed := a.parseManualImportFileContext(t.Context(), ImportFileRequest{Path: path})
	rule := a.Settings().Directories[0]
	request, err := a.directoryFileIdentity(t.Context(), rule, importScanFile{Path: path, Parsed: parsed})
	if err != nil {
		t.Fatal("automatic directory identity", err)
	}
	if request.MediaType != "movie" || request.TMDBID != 0 || request.providerIdentity.ProviderSource != "douban" || request.providerIdentity.ProviderID != "704" || request.providerIdentity.ID != 0 || !request.originalDirectoryProvider {
		t.Fatalf("automatic directory identity request %#v", request)
	}
	if !request.providerImport() {
		t.Fatal("automatic provider identity is not authorized for planning")
	}
	plan, err := a.previewImport(t.Context(), request)
	if err != nil {
		t.Fatal("automatic provider plan", err)
	}
	if plan.Media.ProviderSource != "douban" || plan.Media.ProviderID != "704" || plan.Media.ID != 0 || len(plan.Files) != 1 || plan.Files[0].Status != "ready" {
		t.Fatalf("automatic provider plan identity %#v", plan)
	}
	job, err := a.executeImport(t.Context(), plan.ID, nil)
	if err != nil {
		t.Fatal("automatic provider execution", err)
	}
	if _, err = a.Store.DB.Exec(`UPDATE jobs SET state='running',generation=generation+1 WHERE id=?`, job.ID); err != nil {
		t.Fatal(err)
	}
	job, err = a.jobByID(context.Background(), job.ID)
	if err != nil {
		t.Fatal("automatic provider job", err)
	}
	if err = a.runImport(t.Context(), job, plan.ID, nil); err != nil {
		t.Fatal("automatic provider import", err)
	}
	if _, err = a.finishJob(t.Context(), job, "succeeded", "", "", 0); err != nil {
		t.Fatal(err)
	}
	page := historyParityReadPage(t, a, "")
	if page.Total != 1 || page.Items[0].Status != "success" || page.Items[0].Media.ProviderSource != "douban" || page.Items[0].Media.ProviderID != "704" || page.Items[0].Media.ID != 0 {
		t.Fatalf("automatic provider history %#v", page)
	}
	data, err := os.ReadFile(page.Items[0].Target)
	if err != nil || string(data) != "actual automatic directory source" {
		t.Fatalf("automatic provider target %v", err)
	}
}

func TestAutomaticDirectoryRecognitionProviderTVNumbering(t *testing.T) {
	a, object, unused, cfg := originalTransferModuleFixture(t, "copy", "")
	settings := a.Settings()
	source := "bangumi"
	settings.RecognitionSource = &source
	settings.Roots[0].Kind = "mixed"
	settings.Directories = []DirectoryRule{{ID: "automatic-tv-source", Name: "自动来源剧集", SourcePath: filepath.Dir(unused.Files[0].Source), LibraryPath: settings.Roots[0].Path, RootID: "root", MediaType: "tv", Mode: "copy", MonitorType: "monitor", Enabled: true}}
	if err := a.saveSettings(t.Context(), settings); err != nil {
		t.Fatal(err)
	}
	cfg["modules"].(map[string]string)["recognize_media"] = "module_execute"
	delete(cfg["modules"].(map[string]string), "transfer")
	cfg["result"] = nil
	path := filepath.Join(filepath.Dir(unused.Files[0].Source), "Directory.Show.S01E01.mkv")
	importWrite(t, path, []byte("actual automatic tv source"))
	options, err := a.globalRecognitionOptions(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	parser, err := releasemeta.NewMetaInfoParser(releasemeta.MetaInfoEnvironment{Options: options})
	if err != nil {
		t.Fatal(err)
	}
	meta, err := parser.ParsePath(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	cfg["recognize_results"] = map[string]json.RawMessage{"name:" + meta.Name(): historyProviderModel(t, "bangumi", "706", "目录来源剧集")}
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.pluginStore.saveOriginalPluginConfiguration(t.Context(), object.installation, raw); err != nil {
		t.Fatal(err)
	}
	if err = a.initOriginalLoadedPlugin(t.Context(), object.installation.InstanceID); err != nil {
		t.Fatal(err)
	}
	parsed := a.parseManualImportFileContext(t.Context(), ImportFileRequest{Path: path})
	rule := a.Settings().Directories[0]
	request, err := a.directoryFileIdentity(t.Context(), rule, importScanFile{Path: path, Parsed: parsed})
	if err != nil {
		t.Fatal("automatic tv directory identity", err)
	}
	if request.MediaType != "tv" || request.TMDBID != 0 || request.providerIdentity.ProviderSource != "bangumi" || request.providerIdentity.ProviderID != "706" || request.Files[0].Season != 1 || len(request.Files[0].Episodes) != 1 || request.Files[0].Episodes[0] != 1 {
		t.Fatalf("automatic tv identity %#v", request)
	}
	plan, err := a.previewImport(t.Context(), request)
	if err != nil {
		t.Fatal("automatic tv plan", err)
	}
	if plan.Media.ProviderSource != "bangumi" || plan.Media.ProviderID != "706" || len(plan.Files) != 1 || plan.Files[0].Status != "ready" || plan.Files[0].Season != 1 {
		t.Fatalf("automatic tv plan %#v", plan)
	}
}
