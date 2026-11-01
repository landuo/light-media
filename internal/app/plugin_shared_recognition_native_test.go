package app

import (
	"context"
	"encoding/json"
	"errors"
	"light-media/internal/catalog"
	"testing"
)

// These are the exact review witnesses plus the already-implemented source
// category/model components, not fabricated full-provider/source-run evidence.
func TestOriginalRecognitionNativeModelAndGroupBoundaries(t *testing.T) {
	client := catalog.New(catalog.Credentials{APIKey: "owned"})
	policy := mediaCategoryPolicy{"movie": {{name: "Owned", conditions: []mediaCategoryCondition{{field: "original_language", include: map[string]bool{"EN": true}}, {field: "release_year", include: map[string]bool{"2026": true}}, {field: "production_countries", include: map[string]bool{"US": true}}}}}}
	raw := json.RawMessage(`{"id":42,"title":"Owned","original_title":"Owned","original_language":"en","release_date":"2026-11-01","genres":[],"production_countries":[{"iso_3166_1":"us"}],"external_ids":{"imdb_id":"owned-not-imdb"},"credits":{"cast":[{"id":1,"name":"Owned","order":-1}]},"alternative_titles":{"titles":[]},"translations":{"translations":[]},"release_dates":{"results":[]}}`)
	provider, err := sourceTMDBProviderInfo(raw, "movie")
	if err != nil {
		t.Fatal(err)
	}
	value, err := decodeSourceJSON(provider)
	if err != nil {
		t.Fatal(err)
	}
	info := sourceMap(value)
	category, err := originalRecognitionCategory(client, policy, "movie", info)
	if err != nil || category != "Owned" {
		t.Fatal("unrelated compact validation reached source category", category, err)
	}
	model, err := sourceTMDBRecognitionModel(provider, "movie", "", category)
	if err != nil {
		t.Fatal(err)
	}
	value, err = decodeSourceJSON(model)
	if err != nil {
		t.Fatal(err)
	}
	fields := sourceMap(value)
	if fields["imdb_id"] != "owned-not-imdb" || fields["category"] != "Owned" {
		t.Fatal("raw source model fields lost", fields["imdb_id"], fields["category"])
	}
	// The source get_info genre.get failure escapes to the core module catch.
	badGenre, e := decodeSourceJSON([]byte(`{"id":42,"genres":[null]}`))
	if e != nil {
		t.Fatal(e)
	}
	if out, e := originalRecognitionProviderInfo(sourceMap(badGenre), "movie"); out != nil {
		t.Fatal("malformed genre produced model")
	} else if source, direct := e.(*originalMediaRecognitionSourceError); !direct || source.Class != "AttributeError" {
		t.Fatal("definite source exception lost direct identity", e)
	}
	if _, e := originalRecognitionProviderInfo(info, "movie"); e != nil {
		t.Fatal("later valid augmentation did not recover", e)
	}
	// Source field matching is pure even when a value equals a synthetic key.
	overlap := mediaCategoryPolicy{"movie": {{name: "Owned overlap", conditions: []mediaCategoryCondition{{field: "original_language", include: map[string]bool{"OWNED": true}}}}}}
	if got, e := originalRecognitionCategory(client, overlap, "movie", map[string]any{"original_language": "owned"}); e != nil || got != "Owned overlap" {
		t.Fatal("credential redactor altered source category", got, e)
	}
	delete(info, "title")
	delete(info, "id")
	info["release_date"] = "2026bad" // source category slices four characters, not a date parser.
	if category, err = originalRecognitionCategory(client, policy, "movie", info); err != nil || category != "Owned" {
		t.Fatal("source category depends on compact title/id/date", category, err)
	}

	logs := []string{}
	log := func(level, message string) { logs = append(logs, level) }
	for _, raw := range []string{`[{"episodes":null}]`, `[{"episodes":[1]}]`} {
		v, e := decodeSourceJSON([]byte(raw))
		if e != nil {
			t.Fatal(e)
		}
		before := len(logs)
		rows, e := originalRecognitionGroupRows(v, log)
		if e != nil || len(rows) != 0 || len(logs) != before+1 || logs[len(logs)-1] != "ERROR" {
			t.Fatal("group shape error did not source-catch to empty", rows, e)
		}
	}
	good, e := decodeSourceJSON([]byte(`[{"order":true,"episodes":[{"air_date":"2026-01-02"}]},{"order":1,"episodes":[{},{}]}]`))
	if e != nil {
		t.Fatal(e)
	}
	rows, err := originalRecognitionGroupRows(good, log)
	if err != nil || len(rows) != 2 {
		t.Fatal(rows, err)
	}
	seasons := newOriginalRecognitionSeasonValues()
	for _, row := range rows {
		g := sourceMap(row)
		if err = seasons.set(g["order"], len(sourceRows(g["episodes"]))); err != nil {
			t.Fatal(err)
		}
	}
	if len(seasons.identities) != 1 || len(seasons.values) != 1 || seasons.values["true"] != 2 || len(rows) != 2 {
		t.Fatal("True/1 first-key identity or full group list lost")
	}
	reverse := newOriginalRecognitionSeasonValues()
	if err = reverse.set(json.Number("1"), 1); err != nil {
		t.Fatal(err)
	}
	if err = reverse.set(true, 2); err != nil || reverse.values["1"] != 2 || len(reverse.values) != 1 {
		t.Fatal("reverse equal numeric key wrong", err)
	}
	zero := newOriginalRecognitionSeasonValues()
	if e := zero.set(json.Number("-0.0"), 1); e != nil {
		t.Fatal(e)
	}
	if e := zero.set(false, 2); e != nil {
		t.Fatal(e)
	}
	if e := zero.set(json.Number("0"), 3); e != nil || len(zero.identities) != 1 || zero.values["-0.0"] != 3 {
		t.Fatal("negative zero key did not retain Python first spelling/equality", e)
	}
	before := len(logs)
	if info, err = originalRecognitionResponseObject(json.RawMessage(`[]`), log); err != nil || info != nil || len(logs) != before {
		t.Fatal("empty source array incorrectly logged ERROR", err)
	}
	if info, err = originalRecognitionResponseObject(json.RawMessage(`[{}]`), log); err != nil || info != nil || len(logs) != before+1 {
		t.Fatal("truthy non-object did not source-catch", err)
	}
	// Controlled configuration changes reproduce only the proven ABA seam.
	// No provider call, real credential, proxy change or environment mutation.
	a := newTestApp(t)
	disableResetFixtureStatistics(t, a)
	a.cfgMu.Lock()
	a.config.TMDB.APIKey = "owned-A"
	a.config.TMDB.AccessToken = ""
	a.cfgMu.Unlock()
	guardCalls := 0
	parent := context.WithValue(context.Background(), originalMediaRecognitionGuardKey{}, func(context.Context) error {
		guardCalls++
		a.cfgMu.Lock()
		defer a.cfgMu.Unlock()
		if guardCalls == 1 {
			a.config.TMDB.APIKey = "owned-B"
		} else {
			a.config.TMDB.APIKey = "owned-A"
		}
		return nil
	})
	if _, client, _, err := a.originalRecognitionClient(parent); err == nil || client != nil {
		t.Fatal("ABA configuration rebound client to another authority", err)
	}
	denied := errors.New("owned outer scope denied")
	inherited := context.WithValue(context.Background(), originalMediaRecognitionGuardKey{}, func(context.Context) error { return denied })
	if _, client, _, err := a.originalRecognitionClient(inherited); err != denied || client != nil {
		t.Fatal("parent authority changed", err)
	}
	allowed := context.WithValue(context.Background(), originalMediaRecognitionGuardKey{}, func(context.Context) error { return nil })
	bound, client, snapshot, err := a.originalRecognitionClient(allowed)
	if err != nil || client == nil || snapshot.TMDB.APIKey != "owned-A" {
		t.Fatal("exact client snapshot failed", err)
	}
	a.cfgMu.Lock()
	a.config.TMDB.APIKey = "owned-B"
	a.cfgMu.Unlock()
	if err := originalMediaRecognitionGuard(bound); err == nil {
		t.Fatal("bound client forgot its original snapshot")
	}

}
