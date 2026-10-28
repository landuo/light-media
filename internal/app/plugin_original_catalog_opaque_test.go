package app

import (
	"context"
	"encoding/json"
	"math/big"
	"net/url"
	"os"
	"testing"

	"light-media/internal/plugins"
	"light-media/internal/releasemeta"
)

func TestOriginalCatalogOpaqueHTTPActualSDKConvertNameAndResolve(t *testing.T) {
	a, cookie, object := ownedModuleFixture(t, true)
	if len(object.manifest.ChainHandlers) != 4 {
		t.Fatal("requires actual catalog-event fixture")
	}
	model := json.RawMessage(`{"source":"themoviedb","media_id":"120","tmdb_id":120,"type":"电影","title":"Owned opaque","year":"2026","poster_path":"poster","backdrop_path":"backdrop","logo_path":"logo"}`)
	set := func(results map[string]json.RawMessage, mutations map[string]json.RawMessage) {
		t.Helper()
		cfg, _ := json.Marshal(map[string]any{"enabled": true, "modules": map[string]string{"async_recognize_media": "module_execute", "async_obtain_images": "module_images"}, "recognize_results": results, "event_mutations": mutations, "image_mode": "false"})
		saveOwnedModule(t, a, cookie, object, string(cfg))
		object = originalBaseSDKLoaded(t, a, ownedModuleID)
	}
	request := func(action, source, id string, expected int) json.RawMessage {
		t.Helper()
		query := url.Values{"source": {source}, "kind": {"movie"}, "source_id": {id}, "title": {"Original Film"}, "year": {"2020"}}
		response := callTest(t, a.Handler(), "GET", "/api/catalog/"+action+"?"+query.Encode(), "", cookie, "")
		if response.Code != expected {
			t.Fatalf("actual opaque host status %d want%d %s", response.Code, expected, response.Body.String())
		}
		return response.Body.Bytes()
	}
	inputs := func() []struct{ Kwargs map[string]json.RawMessage } {
		t.Helper()
		var snapshot struct {
			Inputs []struct{ Kwargs map[string]json.RawMessage } `json:"module_inputs"`
		}
		if e := object.readSession(context.Background(), "snapshot", &snapshot); e != nil {
			t.Fatal(e)
		}
		return snapshot.Inputs
	}
	set(map[string]json.RawMessage{"owned:abc/?:x": model}, nil)
	response := request("explore/detail", "Owned", "abc/?:x", 200)
	var detail struct {
		Data struct {
			ID       int64
			Title    string
			Poster   string          `json:"poster_path"`
			Original json.RawMessage `json:"original_media"`
		}
	}
	if json.Unmarshal(response, &detail) != nil || detail.Data.ID != 120 || detail.Data.Title != "Owned opaque" || detail.Data.Poster != "plugin-poster" {
		t.Fatal("actual source recognition/images not consumed", string(response))
	}
	before := ownedModuleRead(t, object)
	response = request("explore/resolve", "Owned", "abc/?:x", 200)
	var resolution struct {
		Data struct {
			Media  struct{ ID int64 }
			Method string `json:"match_method"`
		}
	}
	if json.Unmarshal(response, &resolution) != nil || resolution.Data.Media.ID != 120 || resolution.Data.Method != "tmdb" {
		t.Fatal("opaque source id was not resolved to actual plugin TMDB identity", string(response))
	}
	after := ownedModuleRead(t, object)
	if after.Calls["module_execute"] != before.Calls["module_execute"]+1 || after.Calls["module_images"] != before.Calls["module_images"]+1 || after.Calls["catalog_event"] != before.Calls["catalog_event"] {
		t.Fatal("direct recognition incorrectly requested conversion")
	}
	set(map[string]json.RawMessage{"themoviedb:120": model}, map[string]json.RawMessage{"media.recognize.convert": json.RawMessage(`{"mediaid":"Owned:convert","convert_type":"themoviedb","media_dict":{"id":120}}`)})
	before = ownedModuleRead(t, object)
	start := len(inputs())
	response = request("explore/detail", "Owned", "convert", 200)
	calls := inputs()[start:]
	if len(calls) != 2 || string(calls[0].Kwargs["source"]) != `"owned"` || string(calls[1].Kwargs["tmdbid"]) != `120` {
		t.Fatal("real conversion did not reenter async recognition", calls)
	}
	after = ownedModuleRead(t, object)
	if after.Calls["catalog_event"] != before.Calls["catalog_event"]+1 {
		t.Fatal("conversion event not actually called")
	}
	rawAlias := callTest(t, a.Handler(), "GET", "/api/v1/media/Owned:convert?"+url.Values{"type_name": {"电影"}}.Encode(), "", cookie, "")
	var rawModel map[string]json.RawMessage
	if rawAlias.Code != 200 || json.Unmarshal(rawAlias.Body.Bytes(), &rawModel) != nil || len(rawModel) != 58 || string(rawModel["tmdb_id"]) != `120` || string(rawModel["poster_path"]) != `"plugin-poster"` || rawModel["data"] != nil || rawModel["tmdb_info"] != nil {
		t.Fatal("fixed raw alias/response DTO differs", rawAlias.Code, rawAlias.Body.String())
	}
	set(map[string]json.RawMessage{"name:Other Film": model}, map[string]json.RawMessage{"name.recognize": json.RawMessage(`{"name":"Other.Film","year":"2026","season":0}`)})
	start = len(inputs())
	response = request("explore/detail", "Owned", "name", 200)
	calls = inputs()[start:]
	if len(calls) != 3 {
		t.Fatal("unknown prefix -> convert -> native name -> name event -> recognize sequence", len(calls), string(response))
	}
	var meta map[string]json.RawMessage
	_ = json.Unmarshal(calls[2].Kwargs["meta"], &meta)
	if string(meta["name"]) != `"Other Film"` || string(meta["begin_season"]) != `0` || string(calls[2].Kwargs["mtype"]) != `"电视剧"` {
		t.Fatal("name event mutation/zero-season not consumed", meta, calls[2])
	}
	before = ownedModuleRead(t, object)
	request("explore/detail", "douban", "120", 404)
	after = ownedModuleRead(t, object)
	if after.Calls["catalog_event"] != before.Calls["catalog_event"] {
		t.Fatal("known-source falsey unexpectedly invoked unknown-prefix events")
	}
	rawAlias = callTest(t, a.Handler(), "GET", "/api/v1/media/douban:120?"+url.Values{"type_name": {"电影"}}.Encode(), "", cookie, "")
	if rawAlias.Code != 200 || json.Unmarshal(rawAlias.Body.Bytes(), &rawModel) != nil || len(rawModel) != 58 || string(rawModel["tmdb_id"]) != `null` {
		t.Fatal("source raw falsey must return default model", rawAlias.Code, rawAlias.Body.String())
	}
}

func TestOriginalCatalogOpaqueActualDetailControlFlow(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/standard-catalog-modules/opaque-source-oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	var oracle struct {
		Cases []struct {
			Name, Key, Title, Year string
			Responses              map[string]json.RawMessage
			Mutation               json.RawMessage
			Calls                  []struct {
				Kind, Type string
				Input      json.RawMessage
				Kwargs     map[string]string
			}
			Output map[string]json.RawMessage
		}
	}
	if json.Unmarshal(raw, &oracle) != nil || len(oracle.Cases) != 12 {
		t.Fatal("fixed detail caller oracle")
	}
	for _, row := range oracle.Cases {
		t.Run(row.Name, func(t *testing.T) {
			a := newTestApp(t)
			calls := []string{}
			for _, expected := range row.Calls {
				if expected.Kind == "event" {
					want := expected
					a.registerOriginalChainHandler("media.recognize.convert", originalMutableEventHandler{ID: "owned-convert", Invoke: func(_ context.Context, event *plugins.MutableEvent) error {
						calls = append(calls, "event:media.recognize.convert")
						if !originalBaseJSONEqual(want.Input, event.Data) {
							t.Fatal("source convert input", string(event.Data), string(want.Input))
						}
						if string(row.Mutation) != "null" {
							value, _ := decodeSourceJSON(event.Data)
							mutation, _ := decodeSourceJSON(row.Mutation)
							// Mutable event data is one shared dictionary, updated by the
							// concrete handler, exactly as the source event DTO is mutated.
							before := sourceMap(value)
							for key, value := range sourceMap(mutation) {
								before[key] = value
							}
							event.Data, _ = json.Marshal(before)
						}
						return nil
					}})
				}
			}
			got, e := a.originalCatalogDetailRecognition(context.Background(), row.Key, "movie", row.Title, row.Year, func(source, id string) (json.RawMessage, error) {
				calls = append(calls, "recognize:"+source+":"+id)
				return row.Responses[source+":"+id], nil
			}, func(title, year string) (json.RawMessage, error) {
				calls = append(calls, "name:"+title+":"+year)
				return json.RawMessage(`{"tmdb_id":120}`), nil
			})
			if e != nil {
				t.Fatal(e)
			}
			want := []string{}
			recognized := false
			for _, call := range row.Calls {
				switch call.Kind {
				case "recognize":
					want = append(want, "recognize:"+call.Kwargs["source"]+":"+call.Kwargs["mediaid"])
				case "event":
					want = append(want, "event:"+call.Type)
				case "name":
					want = append(want, "name:"+row.Title+":"+row.Year)
				case "images":
					recognized = true
				}
			}
			b, _ := json.Marshal(calls)
			w, _ := json.Marshal(want)
			if !originalBaseJSONEqual(b, w) || originalModuleResultTruthy(got) != recognized {
				t.Fatal("actual fixed endpoint order/result", string(b), string(w), string(got))
			}
		})
	}
}

func TestOriginalCatalogNameActualHelperMutation(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/standard-catalog-modules/opaque-source-oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	var oracle struct {
		Cases []struct {
			Name        string
			PluginFirst bool `json:"plugin_first"`
			Mutation    json.RawMessage
			Recognized  bool
			Calls       []map[string]any
		} `json:"name_cases"`
	}
	if json.Unmarshal(raw, &oracle) != nil || len(oracle.Cases) != 11 {
		t.Fatal("fixed name helper oracle")
	}
	for _, row := range oracle.Cases {
		t.Run(row.Name, func(t *testing.T) {
			a := newTestApp(t)
			a.cfgMu.Lock()
			a.config.RecognizePluginFirst = row.PluginFirst
			a.cfgMu.Unlock()
			observed := []map[string]any{}
			a.registerOriginalChainHandler("name.recognize", originalMutableEventHandler{ID: "owned-name", Invoke: func(_ context.Context, event *plugins.MutableEvent) error {
				var input map[string]any
				_ = json.Unmarshal(event.Data, &input)
				observed = append(observed, map[string]any{"kind": "event", "type": event.Type, "input": input})
				event.Data = row.Mutation
				return nil
			}})
			got, e := a.originalCatalogNameRecognition(context.Background(), "Original Film 2020", "2020", "movie", func(meta *releasemeta.MetaObject) (json.RawMessage, error) {
				observed = append(observed, map[string]any{"kind": "recognize", "name": meta.Name(), "title": meta.Get("title"), "year": meta.Get("year"), "type": meta.Get("type"), "season": meta.Get("begin_season"), "episode": meta.Get("begin_episode")})
				if len(observed) == 1 {
					return nil, nil
				}
				return json.RawMessage(`{"tmdb_id":120}`), nil
			})
			if e != nil || originalModuleResultTruthy(got) != row.Recognized {
				t.Fatal("source name helper result", e, string(got), row.Recognized)
			}
			b, _ := json.Marshal(observed)
			w, _ := json.Marshal(row.Calls)
			if !originalBaseJSONEqual(b, w) {
				t.Fatal("source native-first/name event/mutations", string(b), string(w))
			}
		})
	}
}

func TestOriginalCatalogDetailResponseActualFixedPydantic(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/standard-catalog-modules/opaque-source-oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	var oracle struct {
		Cases []struct {
			Name          string
			Input, Output json.RawMessage
			Status        int
		} `json:"response_cases"`
	}
	if json.Unmarshal(raw, &oracle) != nil || len(oracle.Cases) != 9 {
		t.Fatal("actual source MediaInfo response schema")
	}
	for _, row := range oracle.Cases {
		t.Run(row.Name, func(t *testing.T) {
			got, err := originalCatalogMediaResponse(row.Input)
			if (err == nil) != (row.Status == 200) {
				t.Fatal("actual fixed DTO validation", err, row.Status)
			}
			if err == nil {
				raw, _ := json.Marshal(got)
				if !originalBaseJSONEqual(raw, row.Output) {
					t.Fatal("source response fields/defaults/coercion", string(raw), string(row.Output))
				}
			}
		})
	}
}

func TestOriginalCatalogRecognitionActualSyncAsyncKwargs(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/standard-catalog-modules/recognition-kwargs-source-oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	var oracle struct {
		Cases []struct {
			Name, Method, State string
			Meta, Explicit      json.RawMessage
			Calls               []struct {
				Method string
				Kwargs json.RawMessage
			}
		}
	}
	if json.Unmarshal(raw, &oracle) != nil || len(oracle.Cases) != 30 {
		t.Fatal("actual sync/async request oracle")
	}
	for _, row := range oracle.Cases {
		t.Run(row.Method+"/"+row.Name, func(t *testing.T) {
			meta, _ := decodeSourceJSON(row.Meta)
			explicit, _ := decodeSourceJSON(row.Explicit)
			got, err := originalCatalogRecognitionCallKwargs(sourceMap(meta), sourceMap(explicit))
			if (err == nil) != (row.State == "ok") {
				t.Fatal("source preprocessing error differs", err, row.State)
			}
			if err != nil {
				return
			}
			if len(row.Calls) != 1 || row.Calls[0].Method != row.Method {
				t.Fatal("source method capture")
			}
			raw, _ := json.Marshal(got)
			if !originalBaseJSONEqual(raw, row.Calls[0].Kwargs) {
				t.Fatal("exact source kwargs", string(raw), string(row.Calls[0].Kwargs))
			}
			if row.Name == "native_id" {
				native, err := originalCatalogRecognitionCallKwargs(nil, map[string]any{"tmdbid": int64(120)})
				if err != nil {
					t.Fatal("native typed ID was not accepted", err)
				}
				raw, _ := json.Marshal(native)
				if !originalBaseJSONEqual(raw, row.Calls[0].Kwargs) {
					t.Fatal("native Go ID changed source kwargs")
				}
			}
			if row.Name == "zero_fallback" {
				native, err := originalCatalogRecognitionCallKwargs(map[string]any{"tmdbid": big.NewInt(121), "media_id": "122"}, map[string]any{"tmdbid": big.NewInt(0), "mediaid": big.NewInt(0)})
				if err != nil {
					t.Fatal("actual MetaInfo *big.Int zero precedence", err)
				}
				raw, _ := json.Marshal(native)
				if !originalBaseJSONEqual(raw, row.Calls[0].Kwargs) {
					t.Fatal("parsed big.Int meta changed source zero precedence", string(raw), string(row.Calls[0].Kwargs))
				}
			}
		})
	}
}
