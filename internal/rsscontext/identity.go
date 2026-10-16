package rsscontext

import (
	"light-media/internal/releasemeta"
	"strings"
)

var identitySources = []string{"themoviedb", "douban", "bangumi", "anilist"}
var identityFields = map[string][]string{"themoviedb": {"tmdb_id", "tmdbid"}, "douban": {"douban_id", "doubanid"}, "bangumi": {"bangumi_id", "bangumiid"}, "anilist": {"anilist_id", "anilistid"}}

type Getter interface{ Get(string) any }
type Identity struct {
	Source string `json:"source"`
	ID     string `json:"id"`
}

func NormalizeSource(v any) string {
	if !truth(v) {
		return ""
	}
	s := casefold(normalizeID(v))
	if s == "tmdb" {
		return "themoviedb"
	}
	return s
}
func ResolveIdentity(media Getter) Identity {
	return resolveIdentity(media, nil, nil, map[string]any{})
}
func resolveIdentity(media Getter, source, mediaID any, values map[string]any) Identity {
	normalized := NormalizeSource(source)
	if normalized != "" && mediaID != nil && normalizeID(mediaID) != "" {
		return Identity{normalized, normalizeID(mediaID)}
	}
	copy := map[string]any{}
	for k, v := range values {
		copy[k] = v
	}
	values = copy
	if media != nil {
		s := media.Get("source")
		if !truth(s) {
			s = media.Get("media_source")
		}
		if normalized == "" {
			normalized = NormalizeSource(s)
		}
		id := media.Get("media_id")
		if normalized != "" && id != nil && normalizeID(id) != "" {
			return Identity{normalized, normalizeID(id)}
		}
		for _, s := range identitySources {
			for _, field := range identityFields[s] {
				v := media.Get(field)
				if v != nil && normalizeID(v) != "" {
					values[s] = v
					break
				}
			}
		}
		legacy := normalizeID(media.Get("mediaid"))
		if i := strings.IndexByte(legacy, ':'); i >= 0 {
			src, id := NormalizeSource(legacy[:i]), normalizeID(legacy[i+1:])
			if normalized == "" && src != "" && id != "" {
				return Identity{src, id}
			}
		}
	}
	if normalized != "" {
		return Identity{normalized, normalizeID(values[normalized])}
	}
	for _, s := range identitySources {
		if v := values[s]; v != nil && normalizeID(v) != "" {
			return Identity{s, normalizeID(v)}
		}
	}
	return Identity{}
}
func MediaMatchSource(m *MediaInfo) string {
	if m != nil {
		for _, s := range []struct{ key, name string }{{"tmdb_id", "tmdbid"}, {"douban_id", "doubanid"}, {"bangumi_id", "bangumiid"}, {"anilist_id", "anilistid"}} {
			if truth(m.Get(s.key)) {
				return s.name
			}
		}
		id := ResolveIdentity(m)
		if id.Source != "" && id.ID != "" {
			return "plugin"
		}
	}
	return "unknown"
}
func metaIdentity(m *releasemeta.MetaObject) Identity {
	if m == nil {
		return Identity{}
	}
	return ResolveIdentity(m)
}
