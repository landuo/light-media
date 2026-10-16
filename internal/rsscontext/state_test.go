package rsscontext

import (
	"context"
	"encoding/json"
	"light-media/internal/releasemeta"
	"math/big"
	"reflect"
	"sync"
	"testing"
)

func TestFramesAndCopyIsolation(t *testing.T) {
	words := []string{"Synthetic => Cached"}
	env := releasemeta.MetaInfoEnvironment{Options: releasemeta.Options{SourceMode: "rust", CustomWords: words, ReleaseGroups: []string{}, Customization: nil, MediaExts: []string{}}, StreamingPlatforms: map[string]string{}}
	frame := Frame{Revision: "cache-v1", Global: env}
	r := RecognizeFunc(func(context.Context, RecognitionRequest) (*MediaInfo, error) {
		return NewNormalizedMediaInfo(map[string]any{"type": "电视剧", "title": "Synthetic Show", "tmdb_id": 44})
	})
	engine, err := NewEngine(frame, r)
	if err != nil {
		t.Fatal(err)
	}
	words[0] = "Synthetic => Changed"
	env.StreamingPlatforms["WEB"] = "mutation"
	torrent, _ := NewTorrentInfo(map[string]any{"title": "Synthetic Show S04E02", "description": "Original description", "enclosure": "synthetic://torrent", "labels": []any{"raw"}})
	cache, err := engine.Produce(context.Background(), torrent, "rss")
	if err != nil {
		t.Fatal(err)
	}
	if cache.Meta.Text("org_string") != "Cached Show S04E02" {
		t.Fatal("caller modified frozen parser")
	}
	if cache.MetaFrame.Global.Options.CustomWords[0] != "Synthetic => Cached" || len(cache.MetaFrame.Global.StreamingPlatforms) != 0 {
		t.Fatal("caller modified source frame")
	}
	cache.Set("allowed_episodes", []*big.Int{big.NewInt(2), big.NewInt(4)})
	delete(cache.Fields, "confirmed_full_coverage")
	selection := []string{}
	sf := localFrame(&selection, Frame{Revision: "selection-v2", Global: env})
	cache.Selection = &sf
	shallow, copy := cache.ShallowCopy(), cache.DeepCopy()
	if shallow.Meta != cache.Meta || shallow.Media != cache.Media || shallow.Torrent != cache.Torrent {
		t.Fatal("core shallow copy lost source identity")
	}
	shallow.Set("match_source", "title")
	if cache.Get("match_source") == "title" {
		t.Fatal("core container flags leaked")
	}
	shallow.Torrent.Get("labels").([]any)[0] = "shared"
	if cache.Torrent.Get("labels").([]any)[0] != "shared" {
		t.Fatal("core nested object unexpectedly cloned")
	}
	copy.Meta.Set("en_name", "mutated")
	copy.Media.Set("title", "mutated")
	copy.Torrent.Get("labels").([]any)[0] = "private"
	copy.Fields["allowed_episodes"].([]*big.Int)[0].SetInt64(99)
	if cache.Meta.Text("en_name") == "mutated" || cache.Media.Get("title") == "mutated" || cache.Torrent.Get("labels").([]any)[0] == "private" || cache.Fields["allowed_episodes"].([]*big.Int)[0].Int64() != 2 {
		t.Fatal("plugin deep clone leaked")
	}
	encoded, err := cache.EncodeState()
	if err != nil {
		t.Fatal(err)
	}
	restored, err := DecodeState(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(canon(view(cache)), canon(view(restored))) {
		t.Fatal("serialized fields changed")
	}
	if _, ok := restored.Fields["confirmed_full_coverage"]; ok {
		t.Fatal("missing class default became present")
	}
	o := restored.MetaFrame.Global.Options
	if o.ReleaseGroups == nil || o.Customization != nil || o.MediaExts == nil || restored.MetaFrame.Global.StreamingPlatforms == nil {
		t.Fatal("global nil/empty presence changed")
	}
	if restored.Selection == nil || !restored.Selection.Present || restored.Selection.Words == nil || len(restored.Selection.Words) != 0 {
		t.Fatal("local None/[] changed")
	}
	if restored.Torrent.Get("title") != "Synthetic Show S04E02" || restored.Torrent.Get("description") != "Original description" {
		t.Fatal("raw title or subtitle changed")
	}
	restored.Meta.Set("en_name", "restored change")
	if cache.Meta.Text("en_name") == "restored change" {
		t.Fatal("decode aliases original")
	}
}
func TestIndependentConcurrentSelections(t *testing.T) {
	frame := Frame{Global: releasemeta.MetaInfoEnvironment{Options: releasemeta.Options{SourceMode: "legacy"}}}
	engine, err := NewEngine(frame, RecognizeFunc(func(context.Context, RecognitionRequest) (*MediaInfo, error) {
		return NewNormalizedMediaInfo(map[string]any{"type": "电视剧", "title": "Synthetic Show", "tmdb_id": 44})
	}))
	if err != nil {
		t.Fatal(err)
	}
	torrent, _ := NewTorrentInfo(map[string]any{"title": "Synthetic Show S04E02", "enclosure": "synthetic://torrent"})
	cache, err := engine.Produce(context.Background(), torrent, "rss")
	if err != nil {
		t.Fatal(err)
	}
	sub := NewSubscription(map[string]any{"name": "Synthetic Show", "type": "电视剧", "season": 4, "tmdbid": 44})
	p, err := engine.PrepareSubscription(context.Background(), sub)
	if err != nil {
		t.Fatal(err)
	}
	p.ClearTargetAfterLibraryCheck()
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				result, err := engine.IdentityCandidate(context.Background(), cache, p, "synthetic", MatchFacts{})
				if err != nil || result.Reason != "identity_candidate" {
					t.Errorf("selection: %v %s", err, result.Reason)
				}
				clone := result.Context.DeepCopy()
				clone.Meta.Set("en_name", "isolated")
				data, err := clone.EncodeState()
				if err != nil {
					t.Error(err)
					return
				}
				if _, err = DecodeState(data); err != nil {
					t.Error(err)
					return
				}
			}
		}()
	}
	wg.Wait()
	if cache.Meta.Text("en_name") == "isolated" {
		t.Fatal("parallel candidate mutated cache")
	}
}
func TestInvalidRetryCounterShortCircuit(t *testing.T) {
	calls := 0
	engine, _ := NewEngine(Frame{Global: releasemeta.MetaInfoEnvironment{Options: releasemeta.Options{SourceMode: "rust"}}}, RecognizeFunc(func(context.Context, RecognitionRequest) (*MediaInfo, error) { calls++; return nil, nil }))
	identified, _ := NewNormalizedMediaInfo(map[string]any{"tmdb_id": 44})
	c := NewContext(nil, identified, nil, Frame{})
	c.Set("media_recognize_fail_count", nil)
	if err := engine.Preflight(context.Background(), c); err == nil || err.Error() != "TypeError" {
		t.Fatal("source elif validates the counter even on an identified object", err)
	}
	c.Media, _ = NewMediaInfo(nil)
	for _, counter := range []any{nil, "2"} {
		c.Set("media_recognize_fail_count", counter)
		if err := engine.Preflight(context.Background(), c); err == nil || err.Error() != "TypeError" {
			t.Fatal("invalid source counter was accepted")
		}
	}
	c.Set("media_recognize_fail_count", json.Number("2.5"))
	if err := engine.Preflight(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	if !pyEqual(c.Get("media_recognize_fail_count"), 3.5) || calls != 1 {
		t.Fatal("source float retry count changed")
	}
}
