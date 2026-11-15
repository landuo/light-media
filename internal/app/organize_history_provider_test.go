package app

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"light-media/internal/plugins"
	"light-media/internal/rsscontext"
)

func historyProviderModel(t *testing.T, source, id, title string) json.RawMessage {
	t.Helper()
	model, err := rsscontext.NewMediaInfo(nil)
	if err != nil {
		t.Fatal(err)
	}
	model.Fields["source"], model.Fields[source+"_id"] = source, id
	model.Fields["type"], model.Fields["title"], model.Fields["original_title"], model.Fields["year"] = "电影", title, title, "2024"
	if source == "bangumi" {
		model.Fields["type"] = "电视剧"
		model.Fields["season"] = 1
		model.Fields["seasons"] = map[string]any{"1": []int{1, 2}}
	}
	raw, err := json.Marshal(model.Fields)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestHistoryProviderIdentityOrdinaryHTTP(t *testing.T) {
	for _, scenario := range []string{"preview", "single", "background-batch"} {
		t.Run(scenario, func(t *testing.T) {
			a, object, unused, config := originalTransferModuleFixture(t, "copy", "")
			login := callTest(t, a.Handler(), "POST", "/api/auth/login", `{"username":"admin","password":"a-strong-test-password"}`, nil, "")
			if login.Code != 200 {
				t.Fatal("browser session login", login.Code)
			}
			var cookie *http.Cookie
			for _, value := range login.Result().Cookies() {
				if value.Name == "lm_session" {
					cookie = value
				}
			}
			if cookie == nil {
				t.Fatal("no ordinary browser session")
			}
			settings := a.Settings()
			settings.Roots[0].Kind = "mixed"
			a.config = settings
			sourceRoot := filepath.Dir(unused.Files[0].Source)
			config["modules"] = map[string]string{"recognize_media": "module_execute"}
			config["result"] = nil
			models := map[string]json.RawMessage{"douban:701": historyProviderModel(t, "douban", "701", "来源电影甲"), "bangumi:702": historyProviderModel(t, "bangumi", "702", "来源电影乙")}
			config["recognize_results"] = models
			raw, err := json.Marshal(config)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = a.pluginStore.saveOriginalPluginConfiguration(t.Context(), object.installation, raw); err != nil {
				t.Fatal(err)
			}
			if err = a.initOriginalLoadedPlugin(t.Context(), object.installation.InstanceID); err != nil {
				t.Fatal(err)
			}
			var selected []transferHistoryItem
			payloads := map[string][]byte{}
			for _, identity := range []struct{ source, id, name string }{{"douban", "701", "First.2024.mkv"}, {"bangumi", "702", "Second.2024.mkv"}} {
				path := filepath.Join(sourceRoot, identity.name)
				data := []byte("real original source " + identity.source)
				importWrite(t, path, data)
				payloads[path] = data
				kind := "movie"
				file := ImportFileRequest{Path: path}
				if identity.source == "bangumi" {
					kind = "tv"
					file.Season = 1
					file.SeasonExplicit = true
					file.Episodes = []int{1}
				}
				model, err := a.originalHostMediaRecognize(t.Context(), plugins.MediaRecognizeRequest{Source: identity.source, MediaID: identity.id, Type: kind})
				if err != nil {
					t.Fatal(err)
				}
				provider := originalChatMediaProvider{Source: identity.source, ID: identity.id}
				info, err := originalChatProviderSearchInformation(model, &provider, kind)
				if err != nil {
					t.Fatal(err)
				}
				// Initial history comes from the existing verified provider import path.
				// Only setup uses its private flag; the web request below is plain JSON.
				plan, err := a.previewImport(t.Context(), ImportRequest{Manual: true, originalRetry: true, providerIdentity: info.Media, originalResolvedMedia: info.MediaInfo, MediaType: kind, RootID: "root", Mode: "copy", Scraping: importFlag(false), Files: []ImportFileRequest{file}})
				if err != nil || len(plan.Files) != 1 || plan.Files[0].Status != "ready" {
					t.Fatal("actual initial source plan", plan, err)
				}
				job, err := a.executeImport(t.Context(), plan.ID, nil)
				if err != nil {
					t.Fatal(err)
				}
				runSoftlinkDirectoryJob(t, a, job.ID)
			}
			page := historyParityReadPage(t, a, "")
			if page.Total != 2 {
				t.Fatal("real initial histories", page)
			}
			for _, item := range page.Items {
				current, e := a.transferHistoryItem(t.Context(), item.ID)
				if e != nil {
					t.Fatal(e)
				}
				selected = append(selected, current)
			}
			destination := filepath.Join(a.Settings().Roots[0].Path, "WebRetry")
			if err = os.MkdirAll(destination, 0750); err != nil {
				t.Fatal(err)
			}
			ids := []string{selected[0].ID}
			if scenario == "background-batch" {
				ids = append(ids, selected[1].ID)
			}
			request := manualOrganizeRequest{HistoryIDs: ids, FromHistory: true, MediaType: "movie", TMDBID: 999, TargetPath: destination, Mode: "copy", Scraping: importFlag(false), Preview: scenario == "preview", Background: scenario == "background-batch"}
			raw, err = json.Marshal(request)
			if err != nil {
				t.Fatal(err)
			}
			response := callTest(t, a.Handler(), "POST", "/api/files/organize", string(raw), cookie, "")
			var envelope struct{ Data manualOrganizeOutput }
			if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &envelope) != nil {
				t.Fatal("ordinary HTTP request", response.Code, response.Body.String())
			}
			out := envelope.Data
			if scenario == "background-batch" {
				if out.PreparationJobID == "" {
					t.Fatal("ordinary queue did not persist preparation", out)
				}
				preparation := runSoftlinkDirectoryJob(t, a, out.PreparationJobID)
				rows, err := a.Store.DB.Query(`SELECT id FROM jobs WHERE kind='organize_file' AND workflow_id=? ORDER BY created_at,id`, preparation.WorkflowID)
				if err != nil {
					t.Fatal(err)
				}
				var jobs []string
				for rows.Next() {
					var id string
					if err = rows.Scan(&id); err != nil {
						t.Fatal(err)
					}
					jobs = append(jobs, id)
				}
				if err = rows.Err(); err != nil {
					t.Fatal(err)
				}
				rows.Close()
				if len(jobs) != 2 {
					t.Fatal("history batch did not create independent candidates", jobs)
				}
				for _, id := range jobs {
					runSoftlinkDirectoryJob(t, a, id)
				}
			} else {
				if out.Failed != 0 || len(out.Items) != 1 || out.Items[0].Media.ID != 0 || out.Items[0].Media.ProviderID != selected[0].Media.ProviderID || out.Items[0].Media.ProviderSource != selected[0].Media.ProviderSource {
					t.Fatal("source ID was treated as stale TMDB", out)
				}
				if scenario == "preview" {
					if out.Submitted != 0 || out.Items[0].JobID != "" || out.Items[0].Status != "ready" {
						t.Fatal("preview submitted execution", out)
					}
				} else {
					if out.Submitted != 1 {
						t.Fatal(out)
					}
					runSoftlinkDirectoryJob(t, a, out.Items[0].JobID)
				}
			}
			page = historyParityReadPage(t, a, "")
			if page.Total != 2 {
				t.Fatal("source identity changed history cardinality", page)
			}
			for _, before := range selected {
				current, err := a.transferHistoryItem(t.Context(), before.ID)
				if err != nil {
					t.Fatal(err)
				}
				acted := before.ID == ids[0] || len(ids) > 1
				if current.ID != before.ID || current.ResourceSource != before.ResourceSource || current.Media.ID != 0 || current.Media.ProviderSource != before.Media.ProviderSource || current.Media.ProviderID != before.Media.ProviderID || current.Media.MediaType != before.Media.MediaType || current.Season != before.Season || !slices.Equal(current.Episodes, before.Episodes) {
					t.Fatal("stable provider/source history", current, before)
				}
				if scenario == "preview" || !acted {
					if current.PlanID != before.PlanID || current.Target != before.Target {
						t.Fatal("preview/unselected row replaced attempt", current, before)
					}
					if _, err = os.Stat(before.Target); err != nil {
						t.Fatal("preview deleted prior target", err)
					}
				} else {
					if current.PlanID == before.PlanID || current.Status != "success" || !within(destination, current.Target) {
						t.Fatal("ordinary retry did not replace latest attempt", current, before)
					}
					if _, err = os.Lstat(before.Target); !os.IsNotExist(err) {
						t.Fatal("old selected target was not cleaned", err)
					}
					if got, e := os.ReadFile(current.Target); e != nil || !bytes.Equal(got, payloads[current.ResourceSource]) {
						t.Fatal("actual retry payload", e)
					}
				}
			}
			snapshot := ownedModuleRead(t, object)
			var call struct{ Kwargs map[string]any }
			if err = json.Unmarshal(snapshot.Inputs["module_execute"], &call); err != nil {
				t.Fatal(err)
			}
			if sourceText(call.Kwargs["source"]) != "douban" && sourceText(call.Kwargs["source"]) != "bangumi" || sourceSubscriptionTruth(call.Kwargs["tmdbid"]) {
				t.Fatal("web recognition did not use exact source namespace", call.Kwargs)
			}
			for path, data := range payloads {
				if got, e := os.ReadFile(path); e != nil || !bytes.Equal(got, data) {
					t.Fatal("history retry changed source", path, e)
				}
			}
		})
	}
}
