package app

import (
	"context"
	"encoding/json"
	"os"
	"testing"
)

func TestOriginalCatalogImagesActualSDKLiveMutationFixedSource(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/standard-catalog-modules/images-source-oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	// Source live_after is observed separately from its public to_dict output.
	var wire struct {
		Cases []struct {
			Name                  string
			Handlers              []string
			Input, Images, Output json.RawMessage
			Live                  json.RawMessage `json:"live_after"`
			Calls                 []struct{ Kind string }
		}
	}
	if json.Unmarshal(raw, &wire) != nil || len(wire.Cases) != 12 {
		t.Fatal("actual fixed image caller/dispatcher/model/system oracle")
	}
	for _, row := range wire.Cases {
		t.Run(row.Name, func(t *testing.T) {
			a, cookie, source := ownedModuleFixture(t, true)
			makeConfig := func(mode string, active bool) string {
				b, _ := json.Marshal(map[string]any{"enabled": active, "modules": map[string]string{"async_obtain_images": "module_images"}, "image_mode": mode})
				return string(b)
			}
			if len(row.Handlers) > 0 {
				saveOwnedModule(t, a, cookie, source, makeConfig(row.Handlers[0], true))
			} else {
				saveOwnedModule(t, a, cookie, source, makeConfig("", false))
			}
			if len(row.Handlers) > 1 {
				clone := ownedModuleClone(t, a, cookie, "images")
				saveOwnedModule(t, a, cookie, clone, makeConfig(row.Handlers[1], true))
			}
			providerCalls := 0
			got, e := a.originalObtainCatalogImages(context.Background(), row.Input, func(model json.RawMessage) (json.RawMessage, error) {
				return originalCatalogTMDBImages(model, "themoviedb", func(kind string, id any, lang any) (json.RawMessage, error) {
					providerCalls++
					if kind != "movie" || sourceStringValue(id) != "120" || lang != "ja" {
						t.Fatal("fixed system image provider kwargs", kind, id, lang)
					}
					return row.Images, nil
				})
			})
			if e != nil {
				t.Fatal(e)
			}
			public, e := originalPublicCatalogMedia(got)
			if e != nil || !originalBaseJSONEqual(public, row.Output) {
				t.Fatalf("source public result differs\ngot %s\nwant%s", public, row.Output)
			}
			// Source vars excludes computed to_dict properties; compare its actual live
			// attributes without replacing its private provider payload by publicNone.
			value, _ := decodeSourceJSON(got)
			fields := sourceMap(value)
			want, _ := decodeSourceJSON(row.Live)
			for key, value := range sourceMap(want) {
				actual, _ := json.Marshal(fields[key])
				expected, _ := json.Marshal(value)
				if !originalBaseJSONEqual(actual, expected) {
					t.Fatal("source live input mutation/private field differs", key, string(actual), string(expected))
				}
			}
			expectedCalls := 0
			for _, call := range row.Calls {
				if call.Kind == "provider" {
					expectedCalls++
				}
			}
			if providerCalls != expectedCalls {
				t.Fatal("source image scalar/list/error/predicate ordering", providerCalls, expectedCalls)
			}
			if len(row.Handlers) > 0 {
				snapshot := ownedModuleRead(t, originalBaseSDKLoaded(t, a, ownedModuleID))
				var call struct {
					Kwargs struct {
						MediaInfo map[string]json.RawMessage `json:"mediainfo"`
					} `json:"kwargs"`
				}
				if json.Unmarshal(snapshot.Inputs["module_images"], &call) != nil || string(call.Kwargs.MediaInfo["tmdb_info"]) != `{"owned_private":1}` {
					t.Fatal("live provider input was incorrectly public-serialized")
				}
			}
		})
	}
}

func TestOriginalCatalogDetailHTTPActuallyCallsRecognizeThenImages(t *testing.T) {
	raw, e := os.ReadFile("../../testdata/standard-catalog-modules/images-source-oracle.json")
	if e != nil {
		t.Fatal(e)
	}
	var oracle struct {
		Cases []struct {
			Name          string
			Input, Output json.RawMessage
		}
	}
	if json.Unmarshal(raw, &oracle) != nil {
		t.Fatal("fixed actual detail caller oracle")
	}
	var input, want json.RawMessage
	for _, row := range oracle.Cases {
		if row.Name == "mutate_false" {
			input, want = row.Input, row.Output
		}
	}
	a, cookie, source := ownedModuleFixture(t, true)
	cfg, _ := json.Marshal(map[string]any{"enabled": true, "modules": map[string]string{"async_recognize_media": "module_execute", "async_obtain_images": "module_images"}, "result": input, "image_mode": "false"})
	saveOwnedModule(t, a, cookie, source, string(cfg))
	source = originalBaseSDKLoaded(t, a, ownedModuleID)
	before := ownedModuleRead(t, source)
	response := callTest(t, a.Handler(), "GET", "/api/catalog/detail?kind=movie&id=120", "", cookie, "")
	var body struct {
		Data struct {
			ID       int64
			Title    string
			Original json.RawMessage `json:"original_media"`
		}
	}
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &body) != nil || body.Data.ID != 120 || body.Data.Title != "Owned source" || !originalBaseJSONEqual(body.Data.Original, want) {
		t.Fatalf("actual native detail did not consume source recognition/image stages: %d %s", response.Code, response.Body.String())
	}
	after := ownedModuleRead(t, source)
	if after.PID != before.PID || after.Calls["init"] != before.Calls["init"] || after.Calls["module_execute"] != before.Calls["module_execute"]+1 || after.Calls["module_images"] != before.Calls["module_images"]+1 || after.Calls["get_module"] != before.Calls["get_module"]+2 {
		t.Fatal("actual host order/getters or object reuse changed")
	}
	var call struct{ Kwargs json.RawMessage }
	if json.Unmarshal(after.Inputs["module_execute"], &call) != nil || !originalBaseJSONEqual(call.Kwargs, json.RawMessage(`{"meta":null,"mtype":"电影","source":"themoviedb","mediaid":"120","tmdbid":120,"doubanid":null,"bangumiid":null,"anilistid":null,"episode_group":null,"cache":true}`)) {
		t.Fatal("fixed detail recognition kwargs", string(call.Kwargs))
	}
	// This is a read-only metadata caller; it must accept an actual plugin model's
	// resolved ID rather than imposing subscription's frozen-native identity.
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(input, &fields)
	fields["tmdb_id"] = json.RawMessage(`121`)
	fields["media_id"] = json.RawMessage(`"121"`)
	changed, _ := json.Marshal(fields)
	cfg, _ = json.Marshal(map[string]any{"enabled": true, "modules": map[string]string{"async_recognize_media": "module_execute", "async_obtain_images": "module_images"}, "result": json.RawMessage(changed), "image_mode": "false"})
	saveOwnedModule(t, a, cookie, source, string(cfg))
	response = callTest(t, a.Handler(), "GET", "/api/catalog/detail?kind=movie&id=120", "", cookie, "")
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &body) != nil || body.Data.ID != 121 {
		t.Fatal("read-only catalog incorrectly rejected a resolved plugin identity", response.Code)
	}
}
