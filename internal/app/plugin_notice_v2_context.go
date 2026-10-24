package app

import (
	"light-media/internal/notice"
	"light-media/internal/noticetemplate"
)

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
