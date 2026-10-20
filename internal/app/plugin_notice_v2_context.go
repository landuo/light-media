package app

import (
	"bytes"
	"encoding/json"
	"math/big"
	"reflect"
	"strconv"
	"strings"

	"light-media/internal/catalog"
	"light-media/internal/jinja/exec"
	"light-media/internal/notice"
	"light-media/internal/noticetemplate"
)

func noticeV2NormalizeNumberValues(value any) (any, error) {
	if value == nil {
		return nil, nil
	}
	switch v := value.(type) {
	case json.Number:
		if !strings.ContainsAny(string(v), ".eE") {
			n, ok := new(big.Int).SetString(string(v), 10)
			if !ok {
				return nil, notice.ErrTemplateDependency
			}
			return n, nil
		}
		return strconv.ParseFloat(string(v), 64)
	case map[string]any:
		out := map[string]any{}
		for key, item := range v {
			value, e := noticeV2NormalizeNumberValues(item)
			if e != nil {
				return nil, e
			}
			out[key] = value
		}
		return out, nil
	case *big.Int:
		return new(big.Int).Set(v), nil
	}
	r := reflect.ValueOf(value)
	if r.Kind() == reflect.Slice {
		out := []any{}
		for i := 0; i < r.Len(); i++ {
			item, e := noticeV2NormalizeNumberValues(r.Index(i).Interface())
			if e != nil {
				return nil, e
			}
			out = append(out, item)
		}
		return out, nil
	}
	return value, nil
}

func larkBusinessNoticeContext(payload pluginLarkBusinessNotice) (noticetemplate.ContextInput, error) {
	var out noticetemplate.ContextInput
	if payload.AbsentContext {
		// Original command messages have meta/mediainfo/torrentinfo/transferinfo
		// all None. This is a distinct trusted projection, never an empty or
		// fabricated TMDB MediaInfo. No partially supplied object is accepted.
		absent := func(raw json.RawMessage) bool {
			return len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
		}
		if !absent(payload.Registration) || !absent(payload.Meta) || !absent(payload.MediaInfo) || !absent(payload.ProviderInfo) || !reflect.DeepEqual(payload.Media, catalog.Media{}) {
			return out, notice.ErrTemplateDependency
		}
		out.Extra = map[string]any{}
		for key, value := range payload.Kwargs {
			v, e := noticeV2NormalizeNumberValues(value)
			if e != nil {
				return out, e
			}
			out.Extra[key] = v
		}
		return out, nil
	}
	if len(payload.ProviderInfo) == 0 || len(payload.MediaInfo) == 0 {
		return out, notice.ErrTemplateDependency
	}
	meta, e := sourceNoticeMeta(payload.Meta)
	if e != nil {
		return out, e
	}
	value, e := noticeV2NormalizeNumberValues(meta)
	if e != nil {
		return out, e
	}
	out.Meta = value.(map[string]any)
	media, e := decodeSourceJSON(payload.MediaInfo)
	if e != nil || sourceMap(media) == nil || sourceMap(media)["source"] != "themoviedb" {
		return out, notice.ErrTemplateDependency
	}
	value, e = noticeV2NormalizeNumberValues(media)
	if e != nil {
		return out, e
	}
	out.Media = value.(map[string]any)
	id, ok := out.Media["tmdb_id"].(*big.Int)
	if !ok || !id.IsInt64() || id.Int64() != payload.Media.ID {
		return out, notice.ErrTemplateDependency
	}
	provider, e := decodeSourceJSON(payload.ProviderInfo)
	if e != nil {
		return out, e
	}
	// The source writes these dictionaries in provider-season order. Preserve
	// integer keys (including season 0) and overwrite an equal key in place.
	for _, field := range []string{"seasons", "season_years"} {
		dict := exec.NewDict()
		values := sourceMap(out.Media[field])
		for _, row := range sourceRows(sourceMap(provider)["seasons"]) {
			s := sourceMap(row)
			key, e := noticeV2NormalizeNumberValues(s["season_number"])
			if e != nil || key == nil {
				continue
			}
			text := exec.AsValue(key).String()
			value, present := values[text]
			if !present {
				continue
			}
			found := false
			for _, p := range dict.Pairs {
				if p.Key.EqualValueTo(exec.AsValue(key)) {
					p.Value, found = exec.AsValue(value), true
					break
				}
			}
			if !found {
				dict.Pairs = append(dict.Pairs, &exec.Pair{Key: exec.AsValue(key), Value: exec.AsValue(value)})
			}
		}
		out.Media[field] = dict
	}
	out.Extra = map[string]any{}
	for key, value := range payload.Kwargs {
		out.Extra[key], e = noticeV2NormalizeNumberValues(value)
		if e != nil {
			return out, e
		}
	}
	return out, nil
}

func noticeV2PackContext(in noticetemplate.ContextInput) (noticeV2Value, error) {
	var include any
	if in.IncludeRawObjects != nil {
		include = *in.IncludeRawObjects
	}
	return noticeV2Encode(map[string]any{"meta": in.Meta, "media": in.Media, "torrent": in.Torrent, "transfer": in.Transfer, "episodes": in.Episodes, "extension": in.FileExtension, "include_raw": include, "extra": in.Extra})
}

func noticeV2UnpackContext(encoded noticeV2Value) (noticetemplate.ContextInput, error) {
	var out noticetemplate.ContextInput
	value, e := noticeV2Decode(encoded)
	if e != nil {
		return out, e
	}
	m, ok := value.(map[string]any)
	if !ok || len(m) != 8 {
		return out, notice.ErrTemplateDependency
	}
	object := func(key string) (map[string]any, error) {
		if m[key] == nil {
			return nil, nil
		}
		value, ok := m[key].(map[string]any)
		if !ok {
			return nil, notice.ErrTemplateDependency
		}
		return value, nil
	}
	for key, target := range map[string]*noticetemplate.Object{"meta": &out.Meta, "media": &out.Media, "torrent": &out.Torrent, "transfer": &out.Transfer, "extra": &out.Extra} {
		*target, e = object(key)
		if e != nil {
			return out, e
		}
	}
	if m["episodes"] != nil {
		items, ok := m["episodes"].([]any)
		if !ok {
			return out, notice.ErrTemplateDependency
		}
		if items != nil {
			out.Episodes = []noticetemplate.Object{}
		}
		for _, item := range items {
			value, ok := item.(map[string]any)
			if !ok {
				return out, notice.ErrTemplateDependency
			}
			out.Episodes = append(out.Episodes, value)
		}
	}
	if m["include_raw"] != nil {
		value, ok := m["include_raw"].(bool)
		if !ok {
			return out, notice.ErrTemplateDependency
		}
		out.IncludeRawObjects = &value
	}
	out.FileExtension = m["extension"]
	return out, nil
}
