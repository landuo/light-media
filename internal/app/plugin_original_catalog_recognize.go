package app

import (
	"context"
	"encoding/json"
	"fmt"
	"light-media/internal/releasemeta/legacyanime"
	"light-media/internal/sourcejson"
	"math"
	"math/big"
	"strconv"
	"strings"
)

// Explicit detail/opaque-source requests reuse the fixed ChainBase module
// boundary. This is distinct from subscription's frozen-native-TMDB identity
// projection: read-only metadata may legitimately resolve a different sourceID.
func originalCatalogRecognitionKwargs(source, mediaID, kind string) (map[string]any, error) {
	var mtype any
	if kind == "movie" {
		mtype = "电影"
	} else if kind == "tv" {
		mtype = "电视剧"
	}
	return originalCatalogRecognitionCallKwargs(nil, map[string]any{"source": source, "mediaid": mediaID, "mtype": mtype})
}

func originalCatalogRecognitionMetaKwargs(meta map[string]any) (map[string]any, error) {
	return originalCatalogRecognitionCallKwargs(meta, nil)
}

// Exact ChainBase request-ID precedence shared by existing ordinary sync and
// async callers. The requested mediaid remains distinct from its numeric alias.
func originalCatalogRecognitionCallKwargs(meta map[string]any, explicit map[string]any) (map[string]any, error) {
	// Parsed MetaInfo uses *big.Int while the wire keeps arbitrary integers as
	// json.Number. Normalize once before applying source truth/zero precedence.
	raw, err := json.Marshal(map[string]any{"meta": meta, "explicit": explicit})
	if err != nil {
		return nil, originalCatalogMediaInvalid()
	}
	value, err := decodeSourceJSON(raw)
	if err != nil {
		return nil, originalCatalogMediaInvalid()
	}
	meta, explicit = sourceMap(sourceMap(value)["meta"]), sourceMap(sourceMap(value)["explicit"])
	out := map[string]any{"meta": meta, "mtype": nil, "source": nil, "mediaid": nil, "tmdbid": nil, "doubanid": nil, "bangumiid": nil, "anilistid": nil, "episode_group": nil, "cache": true}
	for key, value := range explicit {
		if _, present := out[key]; present {
			out[key] = value
		}
	}
	for _, pair := range []struct{ key, field string }{{"tmdbid", "tmdbid"}, {"doubanid", "doubanid"}, {"source", "media_source"}, {"mediaid", "media_id"}, {"episode_group", "episode_group"}} {
		if !sourceSubscriptionTruth(out[pair.key]) {
			out[pair.key] = meta[pair.field]
		}
	}
	requested := out["mediaid"]
	source := originalCatalogSource(originalCatalogIdentityString(out["source"]))
	if !sourceSubscriptionTruth(out["source"]) {
		source = ""
	}
	ids := map[string]any{"tmdbid": nil, "doubanid": nil, "bangumiid": nil, "anilistid": nil}
	toInt := func(value any) (any, error) {
		if value == nil || !legacyanime.IsDigit(originalCatalogIdentityString(value)) {
			return nil, nil
		}
		n, err := legacyanime.Integer(originalCatalogIdentityString(value))
		if err != nil {
			return nil, originalCatalogMediaInvalid()
		}
		return json.Number(n.String()), nil
	}
	if source != "" {
		key := map[string]string{"themoviedb": "tmdbid", "douban": "doubanid", "bangumi": "bangumiid", "anilist": "anilistid"}[source]
		if key != "" {
			value := out[key]
			if sourceSubscriptionTruth(requested) {
				value = requested
			}
			if source == "douban" {
				if sourceSubscriptionTruth(value) {
					ids[key] = originalCatalogIdentityString(value)
				}
			} else {
				var err error
				ids[key], err = toInt(value)
				if err != nil {
					return nil, err
				}
			}
		}
	} else {
		for _, pair := range []struct{ source, key string }{{"themoviedb", "tmdbid"}, {"douban", "doubanid"}, {"bangumi", "bangumiid"}, {"anilist", "anilistid"}} {
			if sourceSubscriptionTruth(out[pair.key]) {
				source = pair.source
				if pair.source == "douban" {
					ids[pair.key] = originalCatalogIdentityString(out[pair.key])
				} else {
					n, err := originalCatalogRecognitionInteger(out[pair.key])
					if err != nil {
						return nil, originalCatalogMediaInvalid()
					}
					ids[pair.key] = n
				}
				break
			}
		}
	}
	for key, value := range ids {
		out[key] = value
	}
	if source == "" {
		out["source"] = nil
	} else {
		out["source"] = source
	}
	out["mediaid"] = requested
	if !sourceSubscriptionTruth(out["mtype"]) && !sourceSubscriptionTruth(out["tmdbid"]) && (meta["type"] == "电影" || meta["type"] == "电视剧") {
		out["mtype"] = meta["type"]
	}
	return out, nil
}

func originalCatalogIdentityString(value any) string {
	raw, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	text, err := sourcejson.String(raw)
	if err != nil {
		return ""
	}
	return text
}

// Source int(value), distinct from Pydantic's DTO integer validation: this
// truncates a finite float and accepts Python base-ten Unicode digit strings.
func originalCatalogRecognitionInteger(value any) (json.Number, error) {
	switch v := value.(type) {
	case string:
		n, err := legacyanime.Integer(v)
		if err != nil {
			return "", err
		}
		return json.Number(n.String()), nil
	case bool:
		if v {
			return "1", nil
		}
		return "0", nil
	case *big.Int:
		if v == nil {
			return "", fmt.Errorf("integer required")
		}
		return json.Number(v.String()), nil
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	text := string(raw)
	if strings.ContainsAny(text, ".eE") {
		number, err := strconv.ParseFloat(text, 64)
		if err != nil || math.IsNaN(number) || math.IsInf(number, 0) {
			return "", fmt.Errorf("integer required")
		}
		n, _ := new(big.Float).SetFloat64(math.Trunc(number)).Int(nil)
		return json.Number(n.String()), nil
	}
	n, ok := new(big.Int).SetString(text, 10)
	if !ok {
		return "", fmt.Errorf("integer required")
	}
	return json.Number(n.String()), nil
}

func (a *App) originalCatalogRecognize(ctx context.Context, kwargs map[string]any, native func() (json.RawMessage, error)) (json.RawMessage, error) {
	return a.originalCatalogRecognizeMethod(ctx, "async_recognize_media", kwargs, native)
}

func (a *App) originalCatalogRecognizeMethod(ctx context.Context, method string, kwargs map[string]any, native func() (json.RawMessage, error)) (json.RawMessage, error) {
	if method != "recognize_media" && method != "async_recognize_media" {
		return nil, originalCatalogMediaInvalid()
	}
	encoded, err := json.Marshal(kwargs)
	if err != nil {
		return nil, err
	}
	result, err := a.invokeOriginalPluginModules(ctx, method, nil, json.RawMessage(`[]`), encoded)
	if err != nil {
		return nil, err
	}
	callNative := func() (json.RawMessage, error) {
		model, err := native()
		if err == nil {
			return model, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		identity := method
		if source, ok := ctx.Value(originalHostMediaNativeSourceKey{}).(string); ok {
			switch source {
			case "douban", "bangumi", "anilist":
				identity = source
			}
		}
		moduleName := originalCatalogSystemModuleName(identity)
		a.reportOriginalPluginExecutionError(ctx, "system", moduleName+"发生了错误", map[string]any{"type": "module", "module_id": originalCatalogSystemModuleID(identity), "module_name": moduleName, "module_method": method}, err)
		return nil, nil
	}
	if originalModuleResultEmpty(result) {
		return callNative()
	}
	var list []json.RawMessage
	if json.Unmarshal(result, &list) == nil {
		// Outer list accumulation still executes the system module, whose scalar
		// model return is discarded. A truthy list then fails the fixed wrapper's
		// MediaInfo attribute access; an empty list is no recognition.
		if _, err = callNative(); err != nil {
			return nil, err
		}
		if len(list) == 0 {
			return nil, nil
		}
		return nil, originalCatalogMediaInvalid()
	}
	if !originalModuleResultTruthy(result) {
		return nil, nil
	}
	return originalLiveMediaImagesJSON(result)
}
