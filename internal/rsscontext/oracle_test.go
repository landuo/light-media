package rsscontext

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"light-media/internal/releasemeta"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func canon(v any) any {
	data, e := json.Marshal(v)
	if e != nil {
		panic(e)
	}
	var x any
	if e = json.Unmarshal(data, &x); e != nil {
		panic(e)
	}
	return x
}
func view(c *Context) any {
	if c == nil {
		return nil
	}
	dict, err := c.ToDict()
	if err == nil {
		return dict
	}
	_, isEnum := c.Media.Fields["type"].(MediaType)
	return map[string]any{"serialization_error": err.Error(), "type_is_enum": isEnum, "media_fields": c.Media.Fields}
}
func stringsOf(v any) []string {
	if v == nil {
		return nil
	}
	out := []string{}
	for _, v := range v.([]any) {
		out = append(out, v.(string))
	}
	return out
}
func mapOf(v any) map[string]any {
	if v == nil {
		return nil
	}
	return v.(map[string]any)
}
func TestActualInstalledRSS(t *testing.T) {
	file, e := os.Open("../../testdata/plugins/rss-context-full/reference/installed-oracle.ndjson.gz")
	if e != nil {
		t.Fatal(e)
	}
	defer file.Close()
	reader, e := gzip.NewReader(file)
	if e != nil {
		t.Fatal(e)
	}
	defer reader.Close()
	decoder := json.NewDecoder(reader)
	decoder.UseNumber()
	count := 0
	for {
		var test struct {
			Case   map[string]any
			Result map[string]any
			Error  string
		}
		if e = decoder.Decode(&test); e == io.EOF {
			break
		} else if e != nil {
			t.Fatal(e)
		}
		count++
		t.Run(test.Case["name"].(string), func(t *testing.T) {
			c := test.Case
			phaseResponse := map[string]any{"produce": c["produce"], "preflight": c["preflight"], "local_rebuild": c["local"], "target": c["target"]}
			calls := []any{}
			recognize := RecognizeFunc(func(_ context.Context, r RecognitionRequest) (*MediaInfo, error) {
				meta, err := r.Meta.ToDict()
				if err != nil {
					return nil, err
				}
				calls = append(calls, map[string]any{"phase": r.Phase, "api": r.API, "meta": canon(meta), "kwargs": canon(r.Kwargs)})
				if phaseResponse[r.Phase] == nil {
					return nil, nil
				}
				return NewNormalizedMediaInfo(mapOf(phaseResponse[r.Phase]))
			})
			frame := Frame{Revision: "cache", Global: releasemeta.MetaInfoEnvironment{Options: releasemeta.Options{SourceMode: c["mode"].(string), CustomWords: stringsOf(c["global_words"])}}}
			engine, err := NewEngine(frame, recognize)
			if err != nil {
				t.Fatal(err)
			}
			torrent, err := NewTorrentInfo(map[string]any{"site": 1, "title": c["title"], "description": c["description"], "category": c["category"], "enclosure": c["enclosure"]})
			if err != nil {
				t.Fatal(err)
			}
			cache, err := engine.Produce(context.Background(), torrent, c["stype"].(string))
			if err != nil {
				t.Fatal(err)
			}
			result := map[string]any{}
			if cache == nil {
				result = map[string]any{"produced": nil, "calls": calls}
			} else {
				for k, v := range mapOf(c["meta_override"]) {
					cache.Meta.Set(k, v)
				}
				for k, v := range mapOf(c["media_override"]) {
					cache.Media.Set(k, v)
				}
				for _, k := range stringsOf(c["context_delete"]) {
					delete(cache.Fields, k)
				}
				for k, v := range mapOf(c["context_override"]) {
					cache.Set(k, v)
				}
				cache.EnsureCompatibility(c["stype"].(string))
				result["produced"] = canon(view(cache))
				n, _ := pyInt(c["preflight_times"])
				for i := int64(0); i < n.Int64(); i++ {
					if err = engine.Preflight(context.Background(), cache); err != nil {
						if test.Error == err.Error() {
							return
						}
						t.Fatal(err)
					}
				}
				result["preflight"] = canon(view(cache))
				selectionFrame := cloneFrame(frame)
				selectionFrame.Revision = "selection"
				if c["selection_global_words"] != nil {
					selectionFrame.Global.Options.CustomWords = stringsOf(c["selection_global_words"])
				}
				selectEngine, err := NewEngine(selectionFrame, recognize)
				if err != nil {
					t.Fatal(err)
				}
				sub := NewSubscription(mapOf(c["subscription"]))
				prepared, err := selectEngine.PrepareSubscription(context.Background(), sub)
				if err != nil {
					t.Fatal(err)
				}
				prepared.Sites = stringSlice(c["sites"])
				prepared.Domains = stringsOf(c["domains"])
				result["target_before_clear"], err = prepared.Media.ToDict()
				if err != nil {
					t.Fatal(err)
				}
				result["target_before_clear"] = canon(result["target_before_clear"])
				prepared.ClearTargetAfterLibraryCheck()
				result["target_meta"], err = prepared.Meta.ToDict()
				if err != nil {
					t.Fatal(err)
				}
				fact := MatchFacts{}
				if c["missing"] != nil {
					fact.MissingEpisodesPresent = true
					fact.MissingEpisodes = stringSlice(c["missing"])
				}
				matched, err := selectEngine.IdentityCandidate(context.Background(), cache, prepared, "synthetic", fact)
				if err != nil {
					t.Fatal(err)
				}
				selected := matched.Context
				result["eligible"] = matched.Reason == "identity_candidate"
				result["selected"] = view(selected)
				result["cached_final"] = view(cache)
				plugins, err := PluginCandidates([]*Context{cache}, sub, c["allow_title"].(bool), c["stype"].(string))
				if err != nil {
					t.Fatal(err)
				}
				pluginView := []any{}
				for _, p := range plugins {
					pluginView = append(pluginView, view(p))
				}
				result["plugin"] = pluginView
				result["calls"] = calls
				alias := map[string]any{"selected_meta_is_cache": selected != nil && selected.Meta == cache.Meta, "selected_media_is_cache": selected != nil && selected.Media == cache.Media, "selected_media_is_target": selected != nil && selected.Media == prepared.Media, "selected_torrent_is_cache": selected != nil && selected.Torrent == cache.Torrent, "plugin_meta_is_cache": len(plugins) > 0 && plugins[0].Meta == cache.Meta, "plugin_media_is_cache": len(plugins) > 0 && plugins[0].Media == cache.Media}
				result["alias"] = alias
				for _, object := range append([]*Context{cache, selected}, plugins...) {
					if object == nil {
						continue
					}
					encoded, err := object.EncodeState()
					if err != nil {
						t.Fatal(err)
					}
					decoded, err := DecodeState(encoded)
					if err != nil {
						t.Fatal(err)
					}
					if !reflect.DeepEqual(canon(view(object)), canon(view(decoded))) {
						t.Fatal("state roundtrip changed object")
					}
					if decoded.Meta == object.Meta || decoded.Media == object.Media || decoded.Torrent == object.Torrent {
						t.Fatal("state roundtrip aliases original")
					}
				}
			}
			if test.Error != "" {
				t.Fatalf("expected source error %s", test.Error)
			}
			got, want := canon(result), canon(test.Result)
			if !reflect.DeepEqual(got, want) {
				a, _ := json.MarshalIndent(got, "", "  ")
				b, _ := json.MarshalIndent(want, "", "  ")
				file := strings.ReplaceAll(c["name"].(string), "/", "-")
				diagnostics := t.TempDir()
				if e := os.WriteFile(filepath.Join(diagnostics, "got.json"), a, 0600); e != nil {
					t.Fatal(e)
				}
				if e := os.WriteFile(filepath.Join(diagnostics, "want.json"), b, 0600); e != nil {
					t.Fatal(e)
				}
				t.Fatalf("actual source mismatch %s; diagnostics %s", file, diagnostics)
			}
		})
	}
	if count != 110 {
		t.Fatalf("missing cases: %d", count)
	}
}
