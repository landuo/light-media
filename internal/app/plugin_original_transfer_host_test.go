package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func installOwnedTransferObserver(t *testing.T, a *App, patches map[string]map[string]any, failures map[string]bool, extra ...map[string]any) string {
	t.Helper()
	cookie := setupTestAdmin(t, a)
	settings := a.Settings()
	settings.APIToken = "owned-transfer-events-test-key"
	if err := a.saveSettings(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	worker := os.Getenv("OWNED_TRANSFER_EVENTS_BINARY")
	if worker == "" {
		t.Fatal("actual separate SDK transfer worker required")
	}
	manifest := installOrderWorker(t, a, cookie, worker)
	config := map[string]any{"enabled": true, "patches": patches, "fail": failures}
	for _, fields := range extra {
		for key, value := range fields {
			config[key] = value
		}
	}
	raw, _ := json.Marshal(config)
	response := callTest(t, a.Handler(), "PUT", "/api/plugins/"+manifest.PluginID, string(raw), cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	a.wg.Wait()
	return manifest.PluginID
}

func TestOriginalTransferActualNativePublishAndInterception(t *testing.T) {
	for _, mode := range []string{"normal", "cancel", "cancel-error", "ignored-target", "overwrite-true", "overwrite-false", "size-override", "escape"} {
		t.Run(mode, func(t *testing.T) {
			a, source, target := importTestApp(t)
			importFixture(t, a, "movie")
			policy := "never"
			if mode == "overwrite-false" {
				policy = "always"
			}
			if mode == "size-override" {
				policy = "size"
			}
			overwriteRule(a, source, target, policy, true)
			input, output := filepath.Join(source, "Owned.2024.mkv"), filepath.Join(target, "Plugin", "Changed.mkv")
			importWrite(t, input, []byte("owned new video"))
			patches := map[string]map[string]any{"transfer.rename": {"updated": true, "updated_str": "Plugin/Changed.mkv"}}
			failures := map[string]bool{}
			switch mode {
			case "cancel", "cancel-error":
				patches["transfer.intercept"] = map[string]any{"cancel": true, "reason": "owned cancellation"}
				failures["transfer.intercept"] = mode == "cancel-error"
			case "ignored-target":
				patches["transfer.intercept"] = map[string]any{"target_path": "/outside/ignored.mkv", "transfer_type": "move"}
			case "overwrite-true":
				patches["transfer.overwrite.check"] = map[string]any{"overwrite": true}
			case "overwrite-false":
				patches["transfer.overwrite.check"] = map[string]any{"overwrite": false, "reason": "owned overwrite denied"}
			case "size-override":
				patches["transfer.overwrite.check"] = map[string]any{"source_size": 999, "target_size": 1}
			case "escape":
				patches["transfer.rename"]["updated_str"] = "/outside/escape.mkv"
			}
			if strings.HasPrefix(mode, "overwrite-") || mode == "size-override" {
				importWrite(t, output, []byte("old owned video bytes larger than source"))
			}
			id := installOwnedTransferObserver(t, a, patches, failures)
			installMetadataFixture(t, a, "owned-transfer-fixture", []string{"/3/movie/123"}, func(r *http.Request) (*http.Response, error) {
				body := `{"id":123,"title":"测试影片","original_title":"Test Film","release_date":"2024-01-01","overview":"owned","genres":[],"credits":{"cast":[],"crew":[]},"external_ids":{},"images":{}}`
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}, nil
			})
			plan, err := a.previewImport(context.Background(), ImportRequest{MediaType: "movie", TMDBID: 123, RootID: "root", Mode: "hardlink", Scraping: importFlag(false), Files: []ImportFileRequest{{Path: input}}})
			if err != nil || len(plan.Files) != 1 {
				t.Fatal("actual preview", plan, err)
			}
			if mode == "escape" {
				job := overwriteStart(t, a, plan)
				if err := a.runImport(context.Background(), job, plan.ID, nil); fileCode(err) != "REVIEW_PLUGIN_TRANSFER_TARGET" {
					t.Fatal("runtime native naming escaped authorized root", err)
				}
				return
			}
			if plan.Files[0].Status != "ready" {
				t.Fatal("executable preparation", plan)
			}
			j := overwriteStart(t, a, plan)
			err = a.runImport(context.Background(), j, plan.ID, nil)
			var executed ImportPlan
			if e := a.Store.Get(context.Background(), "import_plan", plan.ID, &executed); e != nil || executed.Files[0].Target != output {
				t.Fatal("fixed event result consumed at actual native execution", executed, e)
			}
			a.wg.Wait()
			denied := mode == "cancel" || mode == "cancel-error" || mode == "overwrite-false"
			if (err != nil) != denied {
				t.Fatal("actual file result", mode, err)
			}
			bytes, readErr := os.ReadFile(output)
			if !denied && (readErr != nil || string(bytes) != "owned new video") {
				t.Fatal("actual publication", readErr, string(bytes))
			}
			if mode == "overwrite-false" && string(bytes) != "old owned video bytes larger than source" {
				t.Fatal("veto changed prior target")
			}
			if strings.HasPrefix(mode, "cancel") && !os.IsNotExist(readErr) {
				t.Fatal("cancel created target")
			}
			if bytes, err := os.ReadFile(input); err != nil || string(bytes) != "owned new video" {
				t.Fatal("source retained", err)
			}
			observed := ownedInputAPI(t, a, "GET", "/api/v1/plugin/"+id+"/observations", "")
			var calls []struct {
				Type string
				Data json.RawMessage
			}
			if observed.Code != 200 || json.Unmarshal(observed.Body.Bytes(), &calls) != nil {
				t.Fatal("actual SDK trace")
			}
			kinds := []string{}
			for _, call := range calls {
				kinds = append(kinds, call.Type)
			}
			expected := []string{"storage.operation", "storage.operation", "transfer.rename.build", "transfer.rename"}
			if strings.HasPrefix(mode, "overwrite-") || mode == "size-override" {
				expected = append(expected, "transfer.overwrite.check")
			}
			if mode != "overwrite-false" {
				expected = append(expected, "transfer.intercept")
			}
			resultEvent := "transfer.complete"
			if denied {
				resultEvent = "transfer.failed"
			}
			expected = append(expected, resultEvent)
			got, _ := json.Marshal(kinds)
			want, _ := json.Marshal(expected)
			if string(got) != string(want) {
				t.Fatal("fixed source call order", string(got), string(want))
			}
			var result struct {
				History     string `json:"transfer_history_id"`
				Meta, Media map[string]any
				Result      struct {
					Success     bool
					FileCount   int      `json:"file_count"`
					FileListNew []string `json:"file_list_new"`
					Message     *string
				} `json:"transferinfo"`
			}
			if json.Unmarshal(calls[len(calls)-1].Data, &result) != nil || result.Result.Success == denied || result.History == "" {
				t.Fatal("fixed result/history binding", string(calls[len(calls)-1].Data))
			}
			history, err := a.transferHistoryItem(context.Background(), result.History)
			if err != nil || history.Source != input || !denied && history.FileStatus != "imported" || denied && history.FileStatus != "error" {
				t.Fatal("actual committed history", history, err)
			}
			if !denied && (result.Result.FileCount != 1 || len(result.Result.FileListNew) != 1 || result.Result.FileListNew[0] != output) {
				t.Fatal("result must describe actual published material")
			}
			if denied && (result.Result.Message == nil || *result.Result.Message == "") {
				t.Fatal("failure retained concrete reason")
			}
		})
	}
}
