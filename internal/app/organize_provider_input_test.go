package app

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func manualProviderHTTPFixture(t *testing.T) (*App, *originalPluginInstance, string, map[string]any, *http.Cookie) {
	t.Helper()
	a, object, unused, cfg := originalTransferModuleFixture(t, "copy", "")
	login := callTest(t, a.Handler(), "POST", "/api/auth/login", `{"username":"admin","password":"a-strong-test-password"}`, nil, "")
	if login.Code != 200 {
		t.Fatal(login.Code)
	}
	var cookie *http.Cookie
	for _, value := range login.Result().Cookies() {
		if value.Name == "lm_session" {
			cookie = value
		}
	}
	if cookie == nil {
		t.Fatal("missing actual login cookie")
	}
	settings := a.Settings()
	settings.Roots[0].Kind = "mixed"
	if err := a.saveSettings(t.Context(), settings); err != nil {
		t.Fatal(err)
	}
	cfg["modules"] = map[string]string{"recognize_media": "module_execute"}
	cfg["result"] = nil
	return a, object, filepath.Dir(unused.Files[0].Source), cfg, cookie
}
func configureManualProvider(t *testing.T, a *App, object *originalPluginInstance, cfg map[string]any) {
	t.Helper()
	raw, _ := json.Marshal(cfg)
	if _, err := a.pluginStore.saveOriginalPluginConfiguration(t.Context(), object.installation, raw); err != nil {
		t.Fatal(err)
	}
	if err := a.initOriginalLoadedPlugin(t.Context(), object.installation.InstanceID); err != nil {
		t.Fatal(err)
	}
}
func manualProviderCall(t *testing.T, a *App, cookie *http.Cookie, request manualOrganizeRequest) manualOrganizeOutput {
	t.Helper()
	raw, _ := json.Marshal(request)
	r := callTest(t, a.Handler(), "POST", "/api/files/organize", string(raw), cookie, "")
	var response struct{ Data manualOrganizeOutput }
	if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &response) != nil {
		t.Fatal("ordinary provider organize", r.Code, r.Body.String())
	}
	return response.Data
}

func TestManualOrganizeExplicitProviderIDsOrdinaryHTTP(t *testing.T) {
	for _, source := range []string{"douban", "bangumi"} {
		t.Run(source, func(t *testing.T) {
			a, object, root, cfg, cookie := manualProviderHTTPFixture(t)
			id, name, kind := "701", "Manual.2024.mkv", "movie"
			if source == "bangumi" {
				id, name, kind = "702", "Manual.S01E01.mkv", "tv"
			}
			cfg["recognize_results"] = map[string]json.RawMessage{source + ":" + id: historyProviderModel(t, source, id, "直接来源整理")}
			configureManualProvider(t, a, object, cfg)
			path := filepath.Join(root, name)
			data := []byte("actual manual provider media")
			importWrite(t, path, data)
			in := manualOrganizeRequest{Paths: []string{path}, TargetPath: a.Settings().Roots[0].Path, Mode: "copy", MediaType: kind, MediaSource: source, MediaID: id, Scraping: importFlag(false), Preview: true}
			if kind == "tv" {
				one := 1
				in.Season = &one
				in.EpisodeDetail = "1"
			}
			preview := manualProviderCall(t, a, cookie, in)
			if preview.Failed != 0 || len(preview.Items) != 1 || preview.Items[0].Media.ProviderSource != source || preview.Items[0].Media.ProviderID != id || preview.Items[0].Media.ID != 0 {
				t.Fatal("provider preview identity", preview)
			}
			if page := historyParityReadPage(t, a, ""); page.Total != 0 {
				t.Fatal("preview wrote execution history", page)
			}
			in.Preview = false
			out := manualProviderCall(t, a, cookie, in)
			if out.Failed != 0 || out.Submitted != 1 {
				t.Fatal(out)
			}
			runSoftlinkDirectoryJob(t, a, out.Items[0].JobID)
			page := historyParityReadPage(t, a, "")
			if page.Total != 1 || page.Items[0].Status != "success" || page.Items[0].Media.ID != 0 || page.Items[0].Media.ProviderSource != source || page.Items[0].Media.ProviderID != id {
				t.Fatal("actual source history", page)
			}
			got, e := os.ReadFile(page.Items[0].Target)
			if e != nil || !bytes.Equal(got, data) {
				t.Fatal("actual source publication", e)
			}
		})
	}
}

func TestManualOrganizeGlobalSourceAutomaticRecognitionHTTP(t *testing.T) {
	a, object, root, cfg, cookie := manualProviderHTTPFixture(t)
	cfg["recognize_results"] = map[string]json.RawMessage{"name:Auto": historyProviderModel(t, "douban", "703", "自动来源整理")}
	configureManualProvider(t, a, object, cfg)
	settings := a.Settings()
	source := "douban"
	settings.RecognitionSource = &source
	raw, _ := json.Marshal(settings)
	r := callTest(t, a.Handler(), "PUT", "/api/settings", string(raw), cookie, "")
	if r.Code != 200 {
		t.Fatal("save actual global source", r.Code, r.Body.String())
	}
	if a.effectiveRecognitionSource() != "douban" || a.originalHostMediaRecognitionSource(map[string]any{}) != "douban" || a.originalHostMediaRecognition.Source != "themoviedb" {
		t.Fatal("effective source did not use persisted snapshot")
	}
	path := filepath.Join(root, "Auto.2024.mkv")
	importWrite(t, path, []byte("automatic source bytes"))
	out := manualProviderCall(t, a, cookie, manualOrganizeRequest{Paths: []string{path}, TargetPath: a.Settings().Roots[0].Path, Mode: "copy", MediaType: "movie", Scraping: importFlag(false)})
	if out.Failed != 0 || out.Submitted != 1 {
		t.Fatal("global auto recognition", out)
	}
	runSoftlinkDirectoryJob(t, a, out.Items[0].JobID)
	page := historyParityReadPage(t, a, "")
	if page.Total != 1 || page.Items[0].Status != "success" || page.Items[0].Media.ProviderSource != "douban" || page.Items[0].Media.ProviderID != "703" || page.Items[0].Media.ID != 0 {
		t.Fatal(page)
	}
}
