package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"light-media/internal/download"
	"light-media/internal/sites"
)

func TestOriginalResourceDownloadActualSDKFixedCaller(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/standard-event-target/resource-download-oracle.json")
	var oracle struct {
		Cases []struct {
			Name     string
			Patch    map[string]any
			Fail     bool
			Selected []int
			Context  json.RawMessage
			Events   []struct {
				Type  string
				Input json.RawMessage
			}
			Cancelled bool
			Result    json.RawMessage
		}
	}
	if err != nil || json.Unmarshal(raw, &oracle) != nil || len(oracle.Cases) != 9 {
		t.Fatal("fixed resource download caller oracle", err)
	}
	for _, sample := range oracle.Cases {
		t.Run(sample.Name, func(t *testing.T) {
			a := newTestApp(t)
			root := t.TempDir()
			settings := a.Settings()
			settings.AllowedRoots = []string{root}
			if err := a.saveSettings(context.Background(), settings); err != nil {
				t.Fatal(err)
			}
			mapped := func(raw []byte) []byte { return bytes.ReplaceAll(raw, []byte("/owned-downloads"), []byte(root)) }
			patchRaw, _ := json.Marshal(sample.Patch)
			var patch map[string]any
			if json.Unmarshal(mapped(patchRaw), &patch) != nil {
				t.Fatal("patch")
			}
			id := installOwnedTransferObserver(t, a, map[string]map[string]any{"resource.download": patch}, map[string]bool{"resource.download": sample.Fail})
			var original originalDownloadContext
			if json.Unmarshal(sample.Context, &original) != nil {
				t.Fatal("source Context")
			}
			snapshot := downloadContextSnapshot{Original: &original}
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/v2/app/preferences" {
					http.NotFound(w, r)
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]string{"save_path": root})
			}))
			defer peer.Close()
			client, err := download.New(download.Config{ID: "owned-resource", Kind: "qb", BaseURL: peer.URL, APIKey: "owned-protocol-key"})
			if err != nil {
				t.Fatal(err)
			}
			path, err := a.originalResourceDownload(context.Background(), &snapshot, root, sample.Selected, "Manual", json.RawMessage(`null`), client)
			if sample.Cancelled {
				if pluginErrorCode(err) != "REVIEW_DOWNLOAD_CANCELLED" || !strings.Contains(err.Error(), "下载被事件取消") {
					t.Fatal("source cancellation", err)
				}
			} else {
				var result struct {
					SavePath *string `json:"save_path"`
					Context  originalDownloadContext
				}
				if json.Unmarshal(mapped(sample.Result), &result) != nil {
					t.Fatal("source result")
				}
				want := root
				if result.SavePath != nil {
					want = *result.SavePath
				}
				if err != nil || path != want {
					t.Fatal("source path consumption", path, want, err)
				}
				got, _ := json.Marshal(snapshot.Original)
				wantRaw, _ := json.Marshal(result.Context)
				if !originalBaseJSONEqual(got, wantRaw) {
					t.Fatal("source selected episodes/context", string(got), string(wantRaw))
				}
				a.emitOriginalDownloadAdded(context.Background(), "owned-accepted-hash", &snapshot, json.RawMessage(`"Actual downloader"`))
				a.wg.Wait()
			}
			response := ownedInputAPI(t, a, "GET", "/api/v1/plugin/"+id+"/observations", "")
			var calls []struct {
				Type string
				Data json.RawMessage
			}
			if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &calls) != nil || len(calls) != len(sample.Events) || calls[0].Type != "resource.download" || !originalBaseJSONEqual(calls[0].Data, mapped(sample.Events[0].Input)) {
				t.Fatal("actual SDK exact source inputs", response.Code, response.Body.String(), string(mapped(sample.Events[0].Input)))
			}
			for i, event := range sample.Events {
				if calls[i].Type != event.Type || !originalBaseJSONEqual(calls[i].Data, mapped(event.Input)) {
					t.Fatal("fixed source exact event payload", i, string(calls[i].Data), string(mapped(event.Input)))
				}
			}
			a.wg.Wait()
		})
	}
}

func TestOriginalDownloadContextPreservesPrivateSearchModelsThroughNativeBoundary(t *testing.T) {
	a, in, adds := nativeProducerFixture(t)
	ctx := context.Background()
	var resource sites.Resource
	site, err := a.siteForRequest(ctx, in.SiteID)
	if err != nil {
		t.Fatal(err)
	}
	err = a.withSite(ctx, site, false, func(client *sites.Client) error {
		rows, e := client.Search(ctx, in.Query, 1)
		if len(rows.Resources) != 1 {
			return bad("OWNED_FIXTURE", "missing owned native resource")
		}
		resource = rows.Resources[0]
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := a.originalTitleFilterResourceModel(ctx, Candidate{Resource: resource})
	if err != nil {
		t.Fatal(err)
	}
	var torrent map[string]json.RawMessage
	if json.Unmarshal(raw, &torrent) != nil {
		t.Fatal("actual source torrent projection")
	}
	torrent["owned_private"], _ = json.Marshal(map[string]any{"enclosure_token": "synthetic-private-source-only"})
	torrent["enclosure"] = json.RawMessage(`"https://owned.invalid/private-context-only"`)
	resource.ModuleSourceFields, _ = json.Marshal(torrent)
	resource.ModuleMediaFields, err = sourceTMDBMediaInfo([]byte(`{"id":206484,"name":"Owned media","original_name":"Owned Original","first_air_date":"2024-01-01","genres":[],"credits":{"cast":[],"crew":[]},"images":{},"external_ids":{},"owned_provider_private":"synthetic-private-media-only"}`), "tv", "image.tmdb.org", "")
	if err != nil {
		t.Fatal(err)
	}
	if err = a.cacheSearchResources(ctx, []sites.Resource{resource}, map[string]string{resource.ID: in.Query}, 1, "tv:206484"); err != nil {
		t.Fatal("actual encrypted search context", err)
	}
	var public []byte
	if err = a.Store.DB.QueryRow(`SELECT payload FROM records WHERE kind='resource' AND id=?`, resource.SiteID+":"+resource.ID).Scan(&public); err != nil || bytes.Contains(public, []byte("synthetic-private")) {
		t.Fatal("private model exposed in public cache", err)
	}
	id := installOwnedTransferObserver(t, a, nil, nil)
	in.TMDBID = 0
	in.MediaType = "" // Existing manual Add still permits an optional identity.
	native, err := a.addNativeDownload(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if native.TMDBID != 0 || native.Media != nil {
		t.Fatal("private search metadata changed optional public identity")
	}
	job, err := a.claim(ctx)
	if err != nil || job.Kind != "native_download" {
		t.Fatal("actual native job")
	}
	if err = a.safeRun(ctx, job); err != nil || adds.Load() != 1 {
		t.Fatal("real native material and Add", err, adds.Load())
	}
	a.wg.Wait()
	response := ownedInputAPI(t, a, "GET", "/api/v1/plugin/"+id+"/observations", "")
	var events []struct {
		Type string
		Data struct{ Context originalDownloadContext }
	}
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &events) != nil || len(events) != 2 {
		t.Fatal("actual SDK context events", response.Body.String())
	}
	for _, event := range events {
		if !originalBaseJSONEqual(event.Data.Context.Torrent, resource.ModuleSourceFields) || !originalBaseJSONEqual(event.Data.Context.Media, resource.ModuleMediaFields) {
			t.Fatal("full private source models changed across cache/queue boundary", event.Type, string(event.Data.Context.Media))
		}
	}
	var sealed []byte
	if err = a.Store.DB.QueryRow(`SELECT payload FROM records WHERE kind='download_context' AND id=?`, "search_resource:"+resource.SiteID+":"+resource.ID).Scan(&sealed); err != nil || json.Valid(sealed) || bytes.Contains(sealed, []byte("synthetic-private")) {
		t.Fatal("search context plaintext", err)
	}
	if err = a.cacheSearchResources(ctx, []sites.Resource{{ID: resource.ID, SiteID: resource.SiteID, Title: resource.Title}}, nil, 1, ""); err != nil {
		t.Fatal(err)
	}
	cached, err := a.loadDownloadContext(ctx, "search_resource", resource.SiteID+":"+resource.ID, "")
	if err != nil || cached != nil {
		t.Fatal("native result retained stale plugin model", err)
	}
}

func TestOriginalDownloadEventsActualNativeAddAndEncryptedContext(t *testing.T) {
	for _, mode := range []string{"normal", "cancel", "cancel-error", "unsafe-path", "sql-failure"} {
		t.Run(mode, func(t *testing.T) {
			a, in, adds := nativeProducerFixture(t)
			in.TMDBID = 0
			in.MediaType = ""
			patch := map[string]map[string]any{}
			fail := map[string]bool{}
			if strings.HasPrefix(mode, "cancel") {
				patch["resource.download"] = map[string]any{"cancel": true, "reason": "owned native veto"}
				fail["resource.download"] = mode == "cancel-error"
			}
			if mode == "unsafe-path" {
				patch["resource.download"] = map[string]any{"options": map[string]any{"save_path": "/outside/owned-rejected"}}
			}
			id := installOwnedTransferObserver(t, a, patch, fail)
			native, err := a.addNativeDownload(context.Background(), in)
			if err != nil {
				t.Fatal("native queued", err)
			}
			if mode == "sql-failure" {
				if _, err = a.Store.DB.Exec(`CREATE TRIGGER owned_download_fact_failure BEFORE INSERT ON domain_events WHEN NEW.type='download.accepted' BEGIN SELECT RAISE(ABORT,'owned download fact'); END`); err != nil {
					t.Fatal(err)
				}
			}
			job, err := a.claim(context.Background())
			if err != nil || job.Kind != "native_download" {
				t.Fatal("native job", job.Kind, err)
			}
			err = a.safeRun(context.Background(), job)
			wantAdd := mode == "normal" || mode == "sql-failure"
			if (adds.Load() == 1) != wantAdd || (err == nil) != (mode == "normal") {
				t.Fatal("actual native Add side effect", adds.Load(), err)
			}
			a.wg.Wait()
			read := func() []struct {
				Type string
				Data json.RawMessage
			} {
				response := ownedInputAPI(t, a, "GET", "/api/v1/plugin/"+id+"/observations", "")
				var rows []struct {
					Type string
					Data json.RawMessage
				}
				if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &rows) != nil {
					t.Fatal("actual trace")
				}
				return rows
			}
			rows := read()
			want := 1
			if mode == "normal" {
				want = 2
			}
			if len(rows) != want || rows[0].Type != "resource.download" {
				t.Fatal("actual lifecycle event order", rows)
			}
			if mode == "normal" {
				if rows[1].Type != "download.added" {
					t.Fatal("accepted broadcast")
				}
				var data struct {
					Hash     string
					Context  originalDownloadContext
					Episodes []int
					Source   string
				}
				if json.Unmarshal(rows[1].Data, &data) != nil || data.Hash != native.Hash || data.Source != "Manual" || len(data.Episodes) != 1 || data.Episodes[0] != 1 || data.Context.SelectedEpisodes == nil {
					t.Fatal("actual full download context", string(rows[1].Data))
				}
				var sealed []byte
				if err = a.Store.DB.QueryRow(`SELECT payload FROM records WHERE kind='download_context' AND id=?`, "native_download:"+native.ID).Scan(&sealed); err != nil || json.Valid(sealed) || bytes.Contains(sealed, []byte("synthetic-cookie")) {
					t.Fatal("private download context leaked", err)
				}
				if err = a.runNativeDownload(context.Background(), job, native.ID); err != nil {
					t.Fatal("accepted rerun", err)
				}
				a.wg.Wait()
				if adds.Load() != 1 || len(read()) != 2 {
					t.Fatal("accepted repeat replayed Add or broadcast")
				}
			}
		})
	}
}
