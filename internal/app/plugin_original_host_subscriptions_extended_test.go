package app

import (
	"bytes"
	"context"
	"encoding/json"
	"math/big"
	"os"
	"reflect"
	"testing"

	"light-media/internal/catalog"
	"light-media/internal/download"
	"light-media/internal/plugins"
)

func originalHostSourceScalarEqual(a, b json.RawMessage) bool {
	var left, right any
	x := json.NewDecoder(bytes.NewReader(a))
	x.UseNumber()
	y := json.NewDecoder(bytes.NewReader(b))
	y.UseNumber()
	if x.Decode(&left) != nil || y.Decode(&right) != nil {
		return false
	}
	var equal func(any, any) bool
	equal = func(a, b any) bool {
		if an, ok := a.(json.Number); ok {
			bn, ok := b.(json.Number)
			if !ok {
				return false
			}
			ar, aok := new(big.Rat).SetString(string(an))
			br, bok := new(big.Rat).SetString(string(bn))
			return aok && bok && ar.Cmp(br) == 0
		}
		if am, ok := a.(map[string]any); ok {
			bm, ok := b.(map[string]any)
			if !ok || len(am) != len(bm) {
				return false
			}
			for k, v := range am {
				w, exists := bm[k]
				if !exists || !equal(v, w) {
					return false
				}
			}
			return true
		}
		if as, ok := a.([]any); ok {
			bs, ok := b.([]any)
			if !ok || len(as) != len(bs) {
				return false
			}
			for i, v := range as {
				if !equal(v, bs[i]) {
					return false
				}
			}
			return true
		}
		return reflect.DeepEqual(a, b)
	}
	return equal(left, right)
}

func TestOriginalHostBusinessSubscriptionExtendedActualSDKFixedORMAndNativeConsumption(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/plugin-host-business-20261031/subscription-source-orm-r5/oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	var oracle struct {
		SourceCommit string `json:"source_commit"`
		Cases        []struct {
			Name                   string
			Payload, Before, After map[string]json.RawMessage
			ErrorClass             *string `json:"error_class"`
		}
	}
	if json.Unmarshal(raw, &oracle) != nil || oracle.SourceCommit != "8a83efeacd3e26b82be495e17d50146e31c96a60" || len(oracle.Cases) != 35 {
		t.Fatal("actual original ORM oracle missing")
	}
	a := newTestApp(t)
	_, worker := hostBusinessFixture(t, a)
	ctx := context.Background()
	config := a.Settings()
	config.Downloaders = []DownloaderConfig{{Config: download.Config{ID: "owned-downloader", Kind: "qbittorrent", BaseURL: "http://127.0.0.1:9"}, Name: "Owned downloader"}, {Config: download.Config{ID: "owned-default", Kind: "qbittorrent", BaseURL: "http://127.0.0.1:8"}, Name: "Owned default"}}
	config.DefaultDownloader = "owned-default"
	ownedDownloaderSave(t, a, config)
	s := Subscription{ID: "owned-extended", Media: catalog.Media{ID: 42, MediaType: "tv", Title: "Owned", Year: 2024}, Season: 1, TotalEpisodes: 12, StartEpisode: 1, State: "paused", SourceState: "S", Revision: 1, Schedule: Schedule{Mode: "manual"}, CreatedAt: now()}
	if err = a.Store.Put(ctx, "subscription", s.ID, s); err != nil {
		t.Fatal(err)
	}
	var emptyNull plugins.SubscriptionHostResultV1
	if err = hostBusinessCall(t, worker, plugins.SubscriptionsUpdateCapability, json.RawMessage(`{"id":"owned-extended","patch":{"imdbid":null}}`), &emptyNull); err != nil {
		t.Fatal(err)
	}
	initial := Subscription{}
	if err = a.Store.Get(ctx, "subscription", s.ID, &initial); err != nil || initial.Revision != 1 {
		t.Fatal("equal native/source NULL changed revision", err)
	}
	for _, sample := range oracle.Cases {
		t.Run(sample.Name, func(t *testing.T) {
			before := Subscription{}
			if err := a.Store.Get(ctx, "subscription", s.ID, &before); err != nil {
				t.Fatal(err)
			}
			payload := map[string]json.RawMessage{}
			for k, v := range sample.Payload {
				payload[k] = v
			}
			if string(payload["downloader"]) == `"Owned downloader"` {
				payload["downloader"] = json.RawMessage(`"owned-downloader"`)
			}
			encoded, _ := json.Marshal(payload)
			var patch plugins.SubscriptionHostPatchV1
			decodeErr := json.Unmarshal(encoded, &patch)
			var result plugins.SubscriptionHostResultV1
			callErr := decodeErr
			if callErr == nil {
				callErr = hostBusinessCall(t, worker, plugins.SubscriptionsUpdateCapability, plugins.SubscriptionHostUpdateV1{ID: s.ID, Revision: before.Revision, Patch: patch}, &result)
			}
			if sample.ErrorClass != nil {
				if callErr == nil {
					t.Fatal("source constraint or explicit typed boundary did not fail")
				}
				current := Subscription{}
				if err := a.Store.Get(ctx, "subscription", s.ID, &current); err != nil || !originalBaseJSONEqual(mustCleanSeedJSON(before), mustCleanSeedJSON(current)) {
					t.Fatal("rejected patch changed native row", err)
				}
				return
			}
			if callErr != nil || len(result.Items) != 1 {
				t.Fatal("source ORM patch failed", callErr)
			}
			var row map[string]json.RawMessage
			if err := json.Unmarshal(result.Items[0], &row); err != nil {
				t.Fatal(err)
			}
			for key := range sample.Payload {
				if !originalHostSourceScalarEqual(row[key], sample.After[key]) {
					t.Fatalf("source ORM field %s differs: target=%s source=%s", key, row[key], sample.After[key])
				}
			}
			current := Subscription{}
			if err := a.Store.Get(ctx, "subscription", s.ID, &current); err != nil {
				t.Fatal(err)
			}
			sourceNoChange := true
			for key := range sample.Payload {
				if !originalHostSourceScalarEqual(sample.Before[key], sample.After[key]) {
					sourceNoChange = false
				}
			}
			if sourceNoChange && current.Revision != before.Revision {
				t.Fatal("equal/empty source patch changed revision")
			}
			if sample.Name == "all_native_fields" {
				state := subscriptionQualityState(current)
				if current.Media.Title != "Updated" || current.Media.IMDbID != "tt0000042" || current.Media.Year != 2025 || current.Season != 2 || state.TotalEpisode != 24 || state.StartEpisode != 3 || !state.BestVersion || !state.BestVersionFull || state.CurrentPriority == nil || *state.CurrentPriority != 99 || state.EpisodePriority["3"] != 99 || current.State != "active" || state.State != "P" || !current.SourceSearchIMDb || !current.ManualTotalEpisode {
					t.Fatal("source fields did not drive native model/quality state", current)
				}
				if got := subscriptionTargetEpisodes(current, []int{1, 2, 3, 24, 25}); !reflect.DeepEqual(got, []int{3, 24}) {
					t.Fatal("native scope ignored patch", got)
				}
				if _, ready, err := a.ordinaryCompletionBasis(ctx, a.Store.DB, current, nil); err != nil || ready {
					t.Fatal("pending/quality native gate ignored patch", err)
				}
				d, err := a.downloader(current.DownloaderID)
				if err != nil || d.ID != "owned-downloader" {
					t.Fatal("native downloader selection ignored patch", err)
				}
				recognized, err := a.subscriptionRecognitionContext(ctx, current.ID, current.Revision)
				if err != nil || a.prepareRecognitionTitleContext(recognized, "Owned") != "New" {
					t.Fatal("native subscription matcher ignored encrypted words", err)
				}
			}
			if sample.Name == "empty_values" {
				d, err := a.downloader(current.DownloaderID)
				if err != nil || d.ID != "owned-default" {
					t.Fatal("empty downloader did not restore native default", err)
				}
			}
		})
	}
	// Invalid identities/unsupported native business columns cannot silently
	// become inert JSON properties through the finite patch.
	for _, raw := range []string{`{"id":2}`, `{"tmdbid":1}`, `{"type":"电影"}`, `{"episode_group":"x"}`, `{"username":"someone"}`} {
		var patch plugins.SubscriptionHostPatchV1
		if json.Unmarshal([]byte(raw), &patch) == nil {
			t.Fatal("immutable/unsupported column accepted", raw)
		}
	}
	var tempRows, persistedTables int
	if err = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM temp.plugin_host_subscription_flag_affinity`).Scan(&tempRows); err != nil || tempRows != 0 {
		t.Fatal("flag affinity cell retained data", err, tempRows)
	}
	if err = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name='plugin_host_subscription_flag_affinity'`).Scan(&persistedTables); err != nil || persistedTables != 0 {
		t.Fatal("flag affinity became persistent schema", err)
	}
	film := s
	film.ID = "owned-extended-film"
	film.Media.MediaType = "movie"
	film.State = "paused"
	film.SourceState = "S"
	if err = a.Store.Put(ctx, "subscription", film.ID, film); err != nil {
		t.Fatal(err)
	}
	var filmResult plugins.SubscriptionHostResultV1
	if err = hostBusinessCall(t, worker, plugins.SubscriptionsUpdateCapability, json.RawMessage(`{"id":"owned-extended-film","patch":{"season":2,"total_episode":24,"start_episode":3}}`), &filmResult); err != nil {
		t.Fatal(err)
	}
	var filmRow map[string]json.RawMessage
	_ = json.Unmarshal(filmResult.Items[0], &filmRow)
	for _, key := range []string{"season", "total_episode", "start_episode"} {
		if !originalHostSourceScalarEqual(filmRow[key], oracle.Cases[0].After[key]) {
			t.Fatal("movie DB columns were hidden by UI defaults", key)
		}
	}
	var result plugins.SubscriptionHostResultV1
	if err = hostBusinessCall(t, worker, plugins.SubscriptionsUpdateCapability, json.RawMessage(`{"id":"owned-extended","patch":{"state":"R","custom_words":"Owned => Persisted","best_version":-1}}`), &result); err != nil {
		t.Fatal(err)
	}
	current := Subscription{}
	if err = a.Store.Get(ctx, "subscription", s.ID, &current); err != nil {
		t.Fatal(err)
	}
	beforeWords, _ := a.subscriptionWords(ctx, s.ID)
	if _, err = a.Store.DB.Exec(`CREATE TRIGGER owned_sub_patch_words_fail BEFORE INSERT ON subscription_words BEGIN SELECT RAISE(ABORT,'owned failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err = hostBusinessCall(t, worker, plugins.SubscriptionsUpdateCapability, json.RawMessage(`{"id":"owned-extended","patch":{"name":"must rollback","custom_words":"Owned => Failed"}}`), &result); err == nil {
		t.Fatal("failed words write reported success")
	}
	after := Subscription{}
	if err = a.Store.Get(ctx, "subscription", s.ID, &after); err != nil || !originalBaseJSONEqual(mustCleanSeedJSON(current), mustCleanSeedJSON(after)) {
		t.Fatal("words failure partially changed row", err)
	}
	if text, _ := a.subscriptionWords(ctx, s.ID); text != beforeWords {
		t.Fatal("words failure changed live subscription words")
	}
	if _, err = a.Store.DB.Exec(`DROP TRIGGER owned_sub_patch_words_fail`); err != nil {
		t.Fatal(err)
	}
	dir := a.Dir
	if err = a.Close(); err != nil {
		t.Fatal(err)
	}
	cold, err := New(Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer cold.Close()
	if err = cold.initializeOriginalPlugins(ctx); err != nil {
		t.Fatal(err)
	}
	cold.wg.Wait()
	loaded, ok := cold.pluginRunningInstances.Load("OwnedHostBusiness")
	if !ok {
		t.Fatal("cold worker absent")
	}
	if err = hostBusinessCall(t, loaded.(*originalPluginInstance), plugins.SubscriptionsReadCapability, plugins.SubscriptionHostRequestV1{ID: s.ID}, &result); err != nil {
		t.Fatal(err)
	}
	var row map[string]json.RawMessage
	if json.Unmarshal(result.Items[0], &row) != nil || string(row["best_version"]) != "-1" {
		t.Fatal("cold integer source flag changed")
	}
	current = Subscription{}
	if err = cold.Store.Get(ctx, "subscription", s.ID, &current); err != nil {
		t.Fatal(err)
	}
	recognized, err := cold.subscriptionRecognitionContext(ctx, s.ID, current.Revision)
	if err != nil || cold.prepareRecognitionTitleContext(recognized, "Owned") != "Persisted" {
		t.Fatal("cold encrypted subscription matcher lost patch", err)
	}
}
