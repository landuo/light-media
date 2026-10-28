package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"light-media/internal/catalog"
	"light-media/internal/sites"
)

func TestOriginalResourceSelectionActualSDKFixedCaller(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/standard-event-target/resource-selection-oracle.json")
	var oracle struct {
		Cases []struct {
			Name   string
			Patch  map[string]any
			Fail   bool
			Input  []json.RawMessage
			Events []struct {
				Type  string
				Input json.RawMessage
			}
			Result json.RawMessage
			Error  *string
		}
	}
	if err != nil || json.Unmarshal(raw, &oracle) != nil || len(oracle.Cases) != 9 {
		t.Fatal("fixed ResourceSelection source oracle", err)
	}
	for _, sample := range oracle.Cases {
		t.Run(sample.Name, func(t *testing.T) {
			a := newTestApp(t)
			id := installOwnedTransferObserver(t, a, map[string]map[string]any{"resource.selection": sample.Patch}, map[string]bool{"resource.selection": sample.Fail})
			result, err := a.originalResourceSelection(context.Background(), sample.Input, nil, "Subscribe|owned")
			if sample.Error != nil {
				if err == nil {
					t.Fatal("source malformed selection did not fail")
				}
			} else {
				got, _ := json.Marshal(result)
				if err != nil || !originalBaseJSONEqual(got, sample.Result) {
					t.Fatal("fixed selection not-None consumption", string(got), string(sample.Result), err)
				}
			}
			response := ownedInputAPI(t, a, "GET", "/api/v1/plugin/"+id+"/observations", "")
			var calls []struct {
				Type string
				Data json.RawMessage
			}
			if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &calls) != nil || len(calls) != 1 || calls[0].Type != sample.Events[0].Type || !originalBaseJSONEqual(calls[0].Data, sample.Events[0].Input) {
				t.Fatal("exact source SDK selection input", response.Body.String())
			}
			a.wg.Wait()
		})
	}
}

// The source hands the selected Context to download_single. The native durable
// batch must retain that same value when a local failure interrupts the handoff;
// calling selection again after restart could choose a different download.
func TestOriginalResourceSelectionQualitySelectedContextColdRecovery(t *testing.T) {
	ctx := context.Background()
	var id string
	f := newRecognitionSelectionFixture(t, func(f *recognitionSelectionFixture) {
		f.deferCheck = true
		yes := true
		f.request.BestVersion = &yes
		full, err := sourceTMDBMediaInfo([]byte(`{"id":206484,"name":"诛仙","original_name":"Jade Dynasty","first_air_date":"2022-01-01","number_of_seasons":4,"number_of_episodes":5,"seasons":[{"season_number":4,"episode_count":5}],"genres":[],"credits":{"cast":[],"crew":[]},"external_ids":{}}`), "tv", "image.tmdb.org", "")
		if err != nil {
			t.Fatal(err)
		}
		rows := []originalDownloadContext{}
		err = f.app.withSite(ctx, f.app.Settings().Sites[0], false, func(client *sites.Client) error {
			result, e := client.Search(ctx, f.request.Keyword, 1)
			if e != nil {
				return e
			}
			for _, resource := range result.Resources {
				snapshot, e := f.app.prepareDownloadContext(ctx, "search_resource", resource.SiteID+":"+resource.ID, "", resource)
				if e != nil {
					return e
				}
				if e = f.app.buildOriginalDownloadContext(ctx, &snapshot, Candidate{Resource: resource}, catalog.Media{ID: 206484, MediaType: "tv"}, full); e != nil {
					return e
				}
				snapshot.Original.AllowedEpisodes = []int{2}
				snapshot.Original.Extra = map[string]json.RawMessage{"owned_context_marker": json.RawMessage(`{"frozen":["before","restart"]}`)}
				rows = append(rows, *snapshot.Original)
			}
			return nil
		})
		if err != nil || len(rows) == 0 {
			t.Fatal("actual fixture contexts", err)
		}
		id = installOwnedTransferObserver(t, f.app, map[string]map[string]any{"resource.selection": {"updated": true, "updated_contexts": rows}}, nil, map[string]any{"mediainfo": full})
	})
	a := f.app
	if _, err := a.Store.DB.Exec(`DELETE FROM records WHERE kind='cache' AND id='media:tv:206484'`); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Store.DB.Exec(`CREATE TRIGGER owned_selection_intent_failure BEFORE INSERT ON records WHEN NEW.kind='intent' BEGIN SELECT RAISE(ABORT,'owned_selection_intent_failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := a.safeRun(ctx, f.checkJob); err == nil || !strings.Contains(err.Error(), "owned_selection_intent_failure") || f.adds.Load() != 0 {
		t.Fatal("real local failure before Add", err, f.adds.Load())
	}
	frozen, err := a.readAcquisition(ctx, a.Store.DB, f.checkJob.ID)
	if err != nil || len(frozen.SelectionContexts) == 0 {
		t.Fatal("selected Context not durably handed off", err)
	}
	want, _ := json.Marshal(frozen.SelectionContexts)
	// A new selection would now veto every candidate. Recovery must use the
	// already selected Context, including its private field and allowed episode.
	login := callTest(t, a.Handler(), "POST", "/api/auth/login", `{"username":"admin","password":"a-strong-test-password"}`, nil, "")
	if login.Code != 200 || len(login.Result().Cookies()) == 0 {
		t.Fatal("owned admin login", login.Body.String())
	}
	response := callTest(t, a.Handler(), "PUT", "/api/plugins/"+id, `{"enabled":true,"patches":{"resource.selection":{"updated":true,"updated_contexts":[]}}}`, login.Result().Cookies()[0], "")
	if response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	if _, err = a.Store.DB.Exec(`DROP TRIGGER owned_selection_intent_failure`); err != nil {
		t.Fatal(err)
	}
	dir := a.Dir
	if err = a.Close(); err != nil {
		t.Fatal(err)
	}
	a, err = New(Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	f.app = a
	// New only opens storage; production Start initializes plugins before the
	// job workers. Invoke that real initializer without racing a second worker.
	if err = a.initializeOriginalPlugins(ctx); err != nil {
		t.Fatal("cold plugin initialization", err)
	}
	if _, err = a.Store.DB.Exec(`UPDATE jobs SET state='queued' WHERE id=?`, f.checkJob.ID); err != nil {
		t.Fatal(err)
	}
	job, err := a.claim(ctx)
	if err != nil || job.Kind != "subscription" || job.Generation <= f.checkJob.Generation {
		t.Fatal("actual cold parent claim", job.Kind, job.Generation, err)
	}
	if err = a.safeRun(ctx, job); err != nil || f.adds.Load() != 1 {
		t.Fatal("selected allowed episode not recovered into one actual Add", err, f.adds.Load())
	}
	if changed, err := a.finishJob(ctx, job, "succeeded", "", "", time.Now().Unix()); err != nil || !changed {
		t.Fatal("cold check completion", err)
	}
	a.wg.Wait()
	current, err := a.readAcquisition(ctx, a.Store.DB, job.ID)
	got, _ := json.Marshal(current.SelectionContexts)
	if err != nil || !originalBaseJSONEqual(want, got) {
		t.Fatal("frozen selected Context replaced during recovery", err)
	}
	response = ownedInputAPI(t, a, "GET", "/api/v1/plugin/"+id+"/observations", "")
	var events []struct {
		Type string
		Data json.RawMessage
	}
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &events) != nil {
		t.Fatal("cold SDK observations", response.Body.String())
	}
	selected, added := 0, 0
	for _, event := range events {
		if event.Type == "resource.selection" {
			selected++
		}
		if event.Type == "download.added" {
			added++
			var result struct {
				Context  originalDownloadContext
				Episodes []int
			}
			if json.Unmarshal(event.Data, &result) != nil || !originalBaseJSONEqual(result.Context.Extra["owned_context_marker"], json.RawMessage(`{"frozen":["before","restart"]}`)) || len(result.Episodes) != 1 || result.Episodes[0] != 2 {
				t.Fatal("selected source attributes or episode lost after cold handoff", string(event.Data))
			}
		}
	}
	if selected != 1 || added != 1 {
		t.Fatal("selection replayed or Add broadcast duplicated", selected, added)
	}
}

func TestOriginalResourceSelectionOrdinarySubscriptionActualSDKAndAdmission(t *testing.T) {
	for _, mode := range []string{"normal", "clear", "clear-error", "true-none", "allowed-empty", "allowed-second", "quality-clear", "quality-clear-error"} {
		t.Run(mode, func(t *testing.T) {
			want := 2
			if strings.Contains(mode, "clear") || mode == "allowed-empty" {
				want = 0
			}
			if mode == "allowed-second" {
				want = 1
			}
			var id string
			f := newRecognitionSelectionFixture(t, func(f *recognitionSelectionFixture) {
				f.expectedIntents = &want
				if strings.HasPrefix(mode, "quality-") {
					yes := true
					f.request.BestVersion = &yes
				}
				full, err := sourceTMDBMediaInfo([]byte(`{"id":206484,"name":"诛仙","original_name":"Jade Dynasty","first_air_date":"2022-01-01","number_of_seasons":4,"number_of_episodes":5,"seasons":[{"season_number":4,"episode_count":5}],"alternative_titles":{"results":[{"title":"Jade Dynasty"}]},"genres":[],"credits":{"cast":[],"crew":[]},"external_ids":{}}`), "tv", "image.tmdb.org", "")
				if err != nil {
					t.Fatal(err)
				}
				patch := map[string]any{}
				if strings.Contains(mode, "clear") {
					patch = map[string]any{"updated": true, "updated_contexts": []any{}}
				}
				if mode == "true-none" {
					patch = map[string]any{"updated": true, "updated_contexts": nil}
				}
				if strings.HasPrefix(mode, "allowed-") {
					rows := []originalDownloadContext{}
					site := f.app.Settings().Sites[0]
					err = f.app.withSite(context.Background(), site, false, func(client *sites.Client) error {
						result, e := client.Search(context.Background(), f.request.Keyword, 1)
						if e != nil {
							return e
						}
						for _, resource := range result.Resources {
							snapshot, e := f.app.prepareDownloadContext(context.Background(), "search_resource", resource.SiteID+":"+resource.ID, "", resource)
							if e != nil {
								return e
							}
							if e = f.app.buildOriginalDownloadContext(context.Background(), &snapshot, Candidate{Resource: resource}, catalog.Media{ID: 206484, MediaType: "tv"}, full); e != nil {
								return e
							}
							if mode == "allowed-empty" {
								snapshot.Original.AllowedEpisodes = []int{}
							} else {
								snapshot.Original.AllowedEpisodes = []int{2}
								snapshot.Original.Extra = map[string]json.RawMessage{"owned_context_marker": json.RawMessage(`{"value":[1,2],"purpose":"ordinary context private attribute"}`)}
							}
							rows = append(rows, *snapshot.Original)
						}
						return nil
					})
					if err != nil {
						t.Fatal(err)
					}
					patch = map[string]any{"updated": true, "updated_contexts": rows}
				}
				id = installOwnedTransferObserver(t, f.app, map[string]map[string]any{"resource.selection": patch}, map[string]bool{"resource.selection": strings.HasSuffix(mode, "error")}, map[string]any{"mediainfo": full})
			})
			for i := 0; i < want; i++ {
				job, in := recognitionSelectionClaim(t, f)
				if mode == "allowed-second" && (len(in.Episodes) != 1 || in.Episodes[0] != 2) {
					t.Fatal("source allowed episode intersection", in.Episodes)
				}
				recognitionSelectionRun(t, f, job)
			}
			if f.adds.Load() != int32(want) {
				t.Fatal("actual daemon Add count", f.adds.Load(), want)
			}
			f.app.wg.Wait()
			response := ownedInputAPI(t, f.app, "GET", "/api/v1/plugin/"+id+"/observations", "")
			var rows []struct {
				Type string
				Data json.RawMessage
			}
			if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &rows) != nil {
				t.Fatal("SDK observations")
			}
			selection, added := 0, 0
			for _, row := range rows {
				if row.Type == "resource.selection" {
					selection++
					var data struct {
						Contexts []json.RawMessage
						Origin   string
					}
					if json.Unmarshal(row.Data, &data) != nil || len(data.Contexts) == 0 || !strings.HasPrefix(data.Origin, "Subscribe|") {
						t.Fatal("genuine subscription Context/source missing", string(row.Data))
					}
				}
				if row.Type == "download.added" {
					added++
					if mode == "allowed-second" {
						var data struct{ Context originalDownloadContext }
						if json.Unmarshal(row.Data, &data) != nil || string(data.Context.Extra["owned_context_marker"]) != `{"purpose":"ordinary context private attribute","value":[1,2]}` && !originalBaseJSONEqual(data.Context.Extra["owned_context_marker"], json.RawMessage(`{"value":[1,2],"purpose":"ordinary context private attribute"}`)) {
							t.Fatal("context private attributes lost", string(row.Data))
						}
					}
				}
			}
			if selection != 1 || added != want {
				t.Fatal("actual host selection/download event counts", fmt.Sprint(selection, added, want))
			}
		})
	}
}
