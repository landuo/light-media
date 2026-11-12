package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"light-media/internal/catalog"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

type RecommendPreferences struct {
	Enabled map[string]bool `json:"enabled"`
}

// A null JSON value otherwise silently becomes false in a map[string]bool.
// Require actual booleans so malformed updates cannot disable a source.
func (p *RecommendPreferences) UnmarshalJSON(data []byte) error {
	var raw struct {
		Enabled json.RawMessage `json:"enabled"`
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&raw); err != nil {
		return err
	}
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return errors.New("recommend preferences must be an object")
	}
	values := map[string]json.RawMessage{}
	if len(raw.Enabled) > 0 {
		if err := json.Unmarshal(raw.Enabled, &values); err != nil {
			return err
		}
		if values == nil {
			return errors.New("recommend enabled must be an object")
		}
	}
	enabled := make(map[string]bool, len(values))
	for id, rawValue := range values {
		switch string(bytes.TrimSpace(rawValue)) {
		case "true":
			enabled[id] = true
		case "false":
			enabled[id] = false
		default:
			return errors.New("recommend flags must be booleans")
		}
	}
	p.Enabled = enabled
	return nil
}

func normalizeRecommendPreferences(in RecommendPreferences) (RecommendPreferences, error) {
	out := RecommendPreferences{Enabled: map[string]bool{}}
	for _, source := range catalog.RecommendSources() {
		out.Enabled[source.ID] = true
	}
	for id, enabled := range in.Enabled {
		if _, ok := out.Enabled[id]; !ok {
			return RecommendPreferences{}, bad("INVALID_RECOMMEND_PREFERENCES", "推荐设置包含未知来源")
		}
		out.Enabled[id] = enabled
	}
	return out, nil
}

func (a *App) catalogRecommendPreferences(ctx context.Context) (RecommendPreferences, error) {
	var stored RecommendPreferences
	err := a.Store.Get(ctx, "ui_preferences", "catalog_recommend", &stored)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return RecommendPreferences{}, err
	}
	builtin := RecommendPreferences{Enabled: map[string]bool{}}
	known := map[string]bool{}
	for _, source := range catalog.RecommendSources() {
		known[source.ID] = true
		if value, ok := stored.Enabled[source.ID]; ok {
			builtin.Enabled[source.ID] = value
		}
	}
	out, err := normalizeRecommendPreferences(builtin)
	if err != nil {
		return out, err
	}
	// Fixed /user/config/Recommend retains keys for temporarily absent plugin
	// sources. Reading preferences must not broadcast another source event.
	for key, value := range stored.Enabled {
		if !known[key] {
			out.Enabled[key] = value
		}
	}
	return out, nil
}

func (a *App) saveCatalogRecommendPreferences(ctx context.Context, in RecommendPreferences) (RecommendPreferences, error) {
	base := RecommendPreferences{Enabled: map[string]bool{}}
	extraEnabled := map[string]bool{}
	known := map[string]bool{}
	for _, source := range catalog.RecommendSources() {
		known[source.ID] = true
	}
	for key, value := range in.Enabled {
		if known[key] {
			base.Enabled[key] = value
		} else {
			extraEnabled[key] = value
		}
	}
	out, err := normalizeRecommendPreferences(base)
	if err != nil {
		return RecommendPreferences{}, err
	}
	for key, value := range extraEnabled {
		out.Enabled[key] = value
	}
	if err = a.Store.Put(ctx, "ui_preferences", "catalog_recommend", out); err != nil {
		return RecommendPreferences{}, err
	}
	return out, nil
}

func parseRecommendQuery(rawQuery string) (string, int, error) {
	invalid := func() (string, int, error) {
		return "", 0, bad("INVALID_RECOMMEND_QUERY", "请选择有效的推荐来源和页码")
	}
	query, err := url.ParseQuery(rawQuery)
	if err != nil {
		return invalid()
	}
	for key, values := range query {
		if (key != "id" && key != "page") || len(values) != 1 || len(values[0]) > 64 {
			return invalid()
		}
	}
	id := query.Get("id")
	known := false
	for _, source := range catalog.RecommendSources() {
		known = known || source.ID == id
	}
	if !known {
		return invalid()
	}
	page := 1
	if query.Has("page") {
		page, err = strconv.Atoi(query.Get("page"))
		if err != nil || page < 1 || page > 500 {
			return invalid()
		}
	}
	return id, page, nil
}

func recommendCacheKey(id string, page int, now time.Time) string {
	// Daily namespacing also rolls over calendar weekdays and AniList seasons.
	day := now.In(time.FixedZone("Asia/Shanghai", 8*60*60)).Format("2006-01-02")
	return fmt.Sprintf("recommend:v1:%s:%d:%s", id, page, day)
}

func (a *App) catalogRecommendAPI(r *http.Request) (any, error) {
	id, page, err := parseRecommendQuery(r.URL.RawQuery)
	if err != nil {
		return nil, err
	}
	transport, err := a.metadataAPITransportForConfig()
	if err != nil {
		return nil, err
	}
	explorer := catalog.NewExplorer(a.catalog(), transport)
	ctx := r.Context()
	var result catalog.ExplorePage
	method, kwargs := originalCatalogRecommendModule(id, page)
	cachedAt, err := a.cachedOriginalRecommendation(ctx, recommendCacheKey(id, page, time.Now()), &result, func() (catalog.ExplorePage, error) {
		if id == "bangumi_calendar" {
			full, err := a.originalCatalogFeed(ctx, method, kwargs, 1, func() (catalog.ExplorePage, error) { return explorer.BangumiCalendarModule(ctx) })
			if err != nil {
				return full, err
			}
			return originalCatalogCalendarPage(full, page, 30), nil
		}
		return a.originalCatalogFeed(ctx, method, kwargs, page, func() (catalog.ExplorePage, error) { return explorer.Recommend(ctx, id, page) })
	})
	if err != nil {
		return nil, catalogExploreError(err)
	}
	var media []catalog.Media
	for _, item := range result.Results {
		if item.Source == "themoviedb" {
			media = append(media, item.Media)
		}
	}
	if err = a.decorateMedia(ctx, media); err != nil {
		return nil, err
	}
	for i, index := 0, 0; i < len(result.Results); i++ {
		if result.Results[i].Source == "themoviedb" {
			result.Results[i].Media = media[index]
			index++
		}
	}
	return struct {
		catalog.ExplorePage
		CachedAt time.Time `json:"cached_at"`
	}{result, cachedAt}, nil
}
