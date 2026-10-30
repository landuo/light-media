package app

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"light-media/internal/download"
)

func TestSharedTransmissionQueryRealUIHTTP(t *testing.T) {
	if os.Getenv("TRANSMISSION_QUERY_ORACLE") == "" {
		t.Skip("owned source/daemon harness required")
	}
	a, cookie, _ := ownedTransmissionQuerySDKFixture(t, true)
	config := a.Settings()
	config.Downloaders = []DownloaderConfig{{Config: download.Config{ID: "owned-tr", Kind: "transmission", BaseURL: os.Getenv("TRANSMISSION_URL"), Username: os.Getenv("TRANSMISSION_USERNAME"), Password: os.Getenv("TRANSMISSION_PASSWORD")}}}
	ownedDownloaderSave(t, a, config)
	form := callTest(t, a.Handler(), "GET", "/api/plugins/OwnedTransmissionQuery/form", "", cookie, "")
	if form.Code != 200 || !strings.Contains(form.Body.String(), "ids_json") || !strings.Contains(form.Body.String(), "status_json") || !strings.Contains(form.Body.String(), "tags_json") {
		t.Fatal("ordinary form fields missing")
	}
	page := callTest(t, a.Handler(), "GET", "/api/plugins/OwnedTransmissionQuery/page", "", cookie, "")
	if page.Code != 200 || !strings.Contains(page.Body.String(), "VAlert") || !strings.Contains(page.Body.String(), "plugin/OwnedTransmissionQuery/query") {
		t.Fatal("unconfigured page hid error or actual query button")
	}
	failed := callTest(t, a.Handler(), "POST", "/api/v1/plugin/OwnedTransmissionQuery/query", "{}", cookie, "")
	if failed.Code == 200 {
		t.Fatal("unconfigured API became success")
	}
	configure := func(app *App, id, selector string) {
		raw, _ := json.Marshal(map[string]any{"downloader_id": "owned-tr", "ids_json": selector, "status_json": "null", "tags_json": "null"})
		response := callTest(t, app.Handler(), "PUT", "/api/plugins/"+id, string(raw), cookie, "")
		assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
		app.wg.Wait()
	}
	query := func(app *App, id string, want int) {
		t.Helper()
		response := callTest(t, app.Handler(), "POST", "/api/v1/plugin/"+id+"/query", "{}", cookie, "")
		if response.Code != 200 {
			t.Fatal("ordinary finite HTTP query failed", response.Code)
		}
		var result struct {
			Success bool `json:"success"`
			Summary struct {
				Count    int                 `json:"count"`
				Instance string              `json:"instance_id"`
				Tasks    []map[string]string `json:"tasks"`
			} `json:"summary"`
		}
		if json.Unmarshal(response.Body.Bytes(), &result) != nil || !result.Success || result.Summary.Count != want || len(result.Summary.Tasks) != want || result.Summary.Instance != id {
			t.Fatal("live UI summary did not match selected instance/count")
		}
		for _, forbidden := range []string{"trackerList", "trackerStats", "trackers", "uploadedEver", os.Getenv("TRANSMISSION_PASSWORD"), os.Getenv("TRANSMISSION_USERNAME"), os.Getenv("TRANSMISSION_URL")} {
			if forbidden != "" && strings.Contains(response.Body.String(), forbidden) {
				t.Fatal("ordinary HTTP summary exposed raw task material or credentials")
			}
		}
		page := callTest(t, app.Handler(), "GET", "/api/plugins/"+id+"/page", "", cookie, "")
		if page.Code != 200 || !strings.Contains(page.Body.String(), "plugin/"+id+"/query") {
			t.Fatal("page query button not bound to actual clone instance")
		}
	}
	configure(a, "OwnedTransmissionQuery", "null")
	query(a, "OwnedTransmissionQuery", 2)
	response := callTest(t, a.Handler(), "POST", "/api/plugins/OwnedTransmissionQuery/clone", `{"suffix":"UITwin"}`, cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	a.wg.Wait()
	configure(a, "OwnedTransmissionQueryuitwin", `"ffffffffffffffffffffffffffffffffffffffff"`)
	query(a, "OwnedTransmissionQueryuitwin", 0)
	query(a, "OwnedTransmissionQuery", 2)
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	cold, err := New(Options{DataDir: a.Dir})
	if err != nil {
		t.Fatal(err)
	}
	defer cold.Close()
	if err = cold.initializeOriginalPlugins(context.Background()); err != nil {
		t.Fatal(err)
	}
	query(cold, "OwnedTransmissionQueryuitwin", 0)
	query(cold, "OwnedTransmissionQuery", 2)
	t.Log("actual normal form/config/page/HTTP API→standard SDK→real Transmission; unconfigured failure, safe count/name/status only, independent clone config and cold recovery; this is HTTP integration, not browser interaction")
}
