package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"light-media/internal/plugins"
	"light-media/internal/rsscontext"
)

type originalMediaImagesStateKey struct{}
type originalMediaImagesState struct{ model json.RawMessage }

func originalMediaImagesInvalid() error {
	return &APIError{"PLUGIN_MEDIA_IMAGES_INVALID", "插件图片模块返回的MediaInfo输入快照无效", 500}
}
func newOriginalMediaImagesState(raw json.RawMessage) (*originalMediaImagesState, error) {
	model, err := originalLiveMediaImagesJSON(raw)
	if err != nil {
		return nil, err
	}
	return &originalMediaImagesState{model: model}, nil
}
func (s *originalMediaImagesState) input(args json.RawMessage, keywords map[string]json.RawMessage) (json.RawMessage, error) {
	fields := map[string]json.RawMessage{}
	for key, value := range keywords {
		fields[key] = value
	}
	fields["mediainfo"] = s.model
	return json.Marshal(map[string]any{"args": args, "kwargs": fields, "media_images_contract": plugins.MediaImagesContract})
}
func (s *originalMediaImagesState) returned(raw json.RawMessage) (json.RawMessage, error) {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil || fields["media_images_contract"] == nil {
		return raw, nil
	}
	var snapshot plugins.MediaImagesSnapshot
	if json.Unmarshal(raw, &snapshot) != nil || snapshot.Contract != plugins.MediaImagesContract || !json.Valid(snapshot.Value) || snapshot.RateLimited && !snapshot.Failed {
		return nil, originalMediaImagesInvalid()
	}
	model, err := originalLiveMediaImagesJSON(snapshot.Input)
	if err != nil {
		return nil, originalMediaImagesInvalid()
	}
	s.model = model // the source input survives a callback exception
	if snapshot.RateLimited {
		return snapshot.Value, plugins.ErrModuleRateLimited
	}
	if snapshot.Failed {
		return snapshot.Value, &APIError{"PLUGIN_OPERATION_FAILED", "图片模块执行失败", 500}
	}
	return snapshot.Value, nil
}

// The caller ignores the ordinary module return and retains its original live
// input. System modules still run after None/list results; scalarFalse/{} stop
// dispatch. The private provider fields remain available until public output.
func (a *App) originalObtainCatalogImages(ctx context.Context, raw json.RawMessage, native func(json.RawMessage) (json.RawMessage, error)) (json.RawMessage, error) {
	return a.originalObtainCatalogImagesMethod(ctx, "async_obtain_images", raw, native)
}

func (a *App) originalObtainCatalogImagesMethod(ctx context.Context, method string, raw json.RawMessage, native func(json.RawMessage) (json.RawMessage, error)) (json.RawMessage, error) {
	if method != "obtain_images" && method != "async_obtain_images" {
		return nil, originalMediaImagesInvalid()
	}
	state, err := newOriginalMediaImagesState(raw)
	if err != nil {
		return nil, err
	}
	kwargs, _ := json.Marshal(map[string]json.RawMessage{"mediainfo": state.model})
	result, err := a.invokeOriginalPluginModules(context.WithValue(ctx, originalMediaImagesStateKey{}, state), method, nil, json.RawMessage(`[]`), kwargs)
	if err != nil {
		return nil, err
	}
	var list []json.RawMessage
	if originalModuleResultEmpty(result) || json.Unmarshal(result, &list) == nil && !bytes.Equal(bytes.TrimSpace(result), []byte(`null`)) {
		updated, nativeErr := native(state.model)
		if len(updated) > 0 {
			state.model, err = originalLiveMediaImagesJSON(updated)
			if err != nil {
				return nil, err
			}
		}
		if nativeErr != nil {
			a.reportOriginalPluginExecutionError(ctx, "system", "TheMovieDb发生了错误", map[string]any{"type": "module", "module_id": "TheMovieDbModule", "module_name": "TheMovieDb", "module_method": method}, nativeErr)
		}
	}
	return state.model, nil
}

// This is the fixed system image mutation/selection, not a list returned to the
// caller. The best missing file_path must not cause selection of a lower row.
func originalApplyTMDBImages(model, images json.RawMessage) (json.RawMessage, error) {
	return originalApplyTMDBImagesDomain(model, images, "")
}
func originalApplyTMDBImagesDomain(model, images json.RawMessage, domain string) (json.RawMessage, error) {
	value, err := decodeSourceJSON(model)
	if err != nil || sourceMap(value) == nil {
		return model, originalMediaImagesInvalid()
	}
	fields := sourceMap(value)
	imageValue, err := decodeSourceJSON(images)
	if err != nil {
		return model, err
	}
	if !sourceSubscriptionTruth(imageValue) {
		return model, nil
	}
	if rows, ok := imageValue.([]any); ok {
		if len(rows) == 0 {
			return model, nil
		}
		imageValue = rows[0]
	}
	info := sourceMap(imageValue)
	if info == nil {
		return model, fmt.Errorf("invalid source image response")
	}
	current := func() (json.RawMessage, error) { return json.Marshal(fields) }
	for _, pair := range []struct{ field, key string }{{"backdrop_path", "backdrops"}, {"logo_path", "logos"}, {"poster_path", "posters"}} {
		if sourceSubscriptionTruth(fields[pair.field]) {
			continue
		}
		rows, ok := info[pair.key].([]any)
		if info[pair.key] != nil && !ok {
			raw, _ := current()
			return raw, fmt.Errorf("invalid source image list")
		}
		if len(rows) == 0 {
			continue
		}
		type image struct {
			fields         map[string]any
			average, count float64
		}
		ranked := []image{}
		for _, row := range rows {
			m := sourceMap(row)
			if m == nil {
				raw, _ := current()
				return raw, fmt.Errorf("invalid source image row")
			}
			rank := image{fields: m}
			for target, key := range map[*float64]string{&rank.average: "vote_average", &rank.count: "vote_count"} {
				if sourceSubscriptionTruth(m[key]) {
					number, e := originalCatalogImageNumber(m[key])
					if e != nil {
						raw, _ := current()
						return raw, e
					}
					*target = number
				}
			}
			ranked = append(ranked, rank)
		}
		sort.SliceStable(ranked, func(i, j int) bool {
			if ranked[i].average == ranked[j].average {
				return ranked[i].count > ranked[j].count
			}
			return ranked[i].average > ranked[j].average
		})
		path := ranked[0].fields["file_path"]
		if !sourceSubscriptionTruth(path) {
			continue
		}
		text, ok := path.(string)
		if !ok {
			raw, _ := current()
			return raw, fmt.Errorf("invalid source image path")
		}
		imagePath, e := sourceImagePath(text, domain)
		if e != nil {
			raw, _ := current()
			return raw, e
		}
		fields[pair.field] = imagePath
	}
	return current()
}
func originalCatalogImageNumber(value any) (float64, error) {
	switch x := value.(type) {
	case json.Number:
		return x.Float64()
	case bool:
		if x {
			return 1, nil
		}
		return 0, nil
	}
	return 0, fmt.Errorf("invalid source image score")
}

func originalCatalogTMDBImages(raw json.RawMessage, recognizeSource string, provider func(kind string, id any, originalLanguage any) (json.RawMessage, error), domains ...string) (json.RawMessage, error) {
	value, err := decodeSourceJSON(raw)
	if err != nil || sourceMap(value) == nil {
		return raw, originalMediaImagesInvalid()
	}
	fields := sourceMap(value)
	needs := true
	if sourceText(fields["source"]) == "themoviedb" || recognizeSource == "themoviedb" {
		needs = sourceSubscriptionTruth(fields["tmdb_id"]) && !(sourceSubscriptionTruth(fields["logo_path"]) && sourceSubscriptionTruth(fields["poster_path"]) && sourceSubscriptionTruth(fields["backdrop_path"]))
	}
	if !needs {
		return raw, nil
	}
	kind := "tv"
	if sourceText(fields["type"]) == "电影" {
		kind = "movie"
	}
	images, err := provider(kind, fields["tmdb_id"], fields["original_language"])
	// The fixed TmdbApi image helper catches provider exceptions and returns{}.
	if err != nil || len(images) == 0 {
		return raw, nil
	}
	domain := ""
	if len(domains) > 0 {
		domain = domains[0]
	}
	return originalApplyTMDBImagesDomain(raw, images, domain)
}

func originalPublicCatalogMedia(raw json.RawMessage) (json.RawMessage, error) {
	value, err := decodeSourceJSON(raw)
	if err != nil || sourceMap(value) == nil {
		return nil, originalMediaImagesInvalid()
	}
	model, err := rsscontext.NewMediaInfo(nil)
	if err != nil {
		return nil, err
	}
	if err = model.FromDict(sourceMap(value)); err != nil {
		return nil, err
	}
	fields, err := model.ToDict()
	if err != nil {
		return nil, err
	}
	return json.Marshal(fields)
}

func originalLiveMediaImagesJSON(raw json.RawMessage) (json.RawMessage, error) {
	return originalFilterSourceMediaJSON(raw)
}
