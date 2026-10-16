package rsscontext

import (
	"errors"
	"light-media/internal/releasemeta"
	"light-media/internal/releasemeta/legacyanime"
	"math/big"
	"sort"
	"strings"
	"unicode"
)

func pythonSpace(r rune) bool { return unicode.IsSpace(r) || r >= 0x1c && r <= 0x1f }
func clearUpper(value any) (string, error) {
	if !truth(value) {
		return "", nil
	}
	s, ok := value.(string)
	if !ok {
		return "", errors.New("TypeError")
	}
	var b strings.Builder
	for _, r := range s {
		if strings.ContainsRune("、.。,，·:：;；!！?？'’\"“”()（）[]【】「」-—―+|\\_/&#～~", r) || r >= 0x200b && r <= 0x200d || r == 0xfeff || pythonSpace(r) {
			continue
		}
		b.WriteRune(r)
	}
	return legacyanime.Strip(upper(b.String())), nil
}
func englishWord(s string) bool {
	if s == "" {
		return false
	}
	for _, b := range []byte(s) {
		if !(b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z') {
			return false
		}
	}
	return true
}
func sourceSplit(s string, separators string) []string {
	out := []string{}
	start := 0
	inSep := false
	for at, r := range s {
		sep := pythonSpace(r) || strings.ContainsRune(separators, r)
		if sep {
			if !inSep {
				out = append(out, s[start:at])
				inSep = true
			}
			start = at + len(string(r))
		} else {
			inSep = false
		}
	}
	out = append(out, s[start:])
	return out
}
func stringSlice(v any) []any {
	switch x := v.(type) {
	case []any:
		return x
	case []*big.Int:
		out := make([]any, len(x))
		for i, v := range x {
			out[i] = v
		}
		return out
	case []string:
		out := make([]any, len(x))
		for i, v := range x {
			out[i] = v
		}
		return out
	}
	return nil
}
func intersects(a, b map[string]bool) bool {
	for x := range a {
		if b[x] {
			return true
		}
	}
	return false
}

// MatchTorrent is the actual pure title fallback, not the looser diagnostic
// substring match used by PluginCandidates.
func MatchTorrent(target *MediaInfo, meta *releasemeta.MetaObject, torrent *TorrentInfo) (bool, error) {
	if target == nil || meta == nil || torrent == nil {
		return false, errors.New("match_object_required")
	}
	for _, s := range []struct{ meta, media string }{{"tmdbid", "tmdb_id"}, {"doubanid", "douban_id"}, {"bangumiid", "bangumi_id"}, {"anilistid", "anilist_id"}} {
		if truth(meta.Get(s.meta)) && pyEqual(meta.Get(s.meta), target.Get(s.media)) {
			return true, nil
		}
	}
	titles, names, parsed := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, v := range []any{target.Get("title"), target.Get("original_title")} {
		s, e := clearUpper(v)
		if e != nil {
			return false, e
		}
		if s != "" {
			titles[s] = true
		}
	}
	for _, v := range stringSlice(target.Get("names")) {
		if truth(v) {
			s, e := clearUpper(v)
			if e != nil {
				return false, e
			}
			names[s] = true
		}
	}
	for _, v := range []any{meta.Get("cn_name"), meta.Get("en_name")} {
		s, e := clearUpper(v)
		if e != nil {
			return false, e
		}
		if s != "" {
			parsed[s] = true
		}
	}
	if meta.Get("type") == "电视剧" && target.Get("type") != "电视剧" {
		return false, nil
	}
	if torrent.Get("category") == "电视剧" && target.Get("type") != "电视剧" {
		return false, nil
	}
	if truth(target.Get("year")) {
		if target.Get("type") == "电视剧" {
			if truth(meta.Get("year")) {
				found := false
				if years, ok := target.Get("season_years").(map[string]any); ok {
					for _, v := range years {
						if pyEqual(v, meta.Get("year")) {
							found = true
							break
						}
					}
				}
				if !found {
					return false, nil
				}
			}
		} else {
			if !truth(meta.Get("year")) {
				return false, nil
			}
			n, e := pyInt(target.Get("year"))
			if e != nil {
				return false, e
			}
			before := new(big.Int).Sub(n, big.NewInt(1)).String()
			after := new(big.Int).Add(n, big.NewInt(1)).String()
			if !pyEqual(meta.Get("year"), target.Get("year")) && !pyEqual(meta.Get("year"), before) && !pyEqual(meta.Get("year"), after) {
				return false, nil
			}
		}
	}
	if intersects(parsed, titles) || len(names) > 0 && intersects(parsed, names) {
		return true, nil
	}
	if truth(meta.Get("org_string")) {
		fragments := map[string]bool{}
		for _, part := range sourceSplit(meta.Text("org_string"), "/【】.[]-") {
			if !englishWord(part) {
				s, e := clearUpper(part)
				if e != nil {
					return false, e
				}
				fragments[s] = true
			}
		}
		if intersects(titles, fragments) {
			return true, nil
		}
	}
	if truth(torrent.Get("description")) {
		description, ok := torrent.Get("description").(string)
		if !ok {
			return false, errors.New("TypeError")
		}
		fragments := map[string]bool{}
		for _, part := range sourceSplit(description, "/【】|") {
			if !englishWord(part) {
				s, e := clearUpper(part)
				if e != nil {
					return false, e
				}
				fragments[s] = true
			}
		}
		if intersects(titles, fragments) || intersects(names, fragments) {
			return true, nil
		}
	}
	return false, nil
}
func sortedEpisodeSet(value any) ([]*big.Int, error) {
	items := stringSlice(value)
	if items == nil {
		return nil, errors.New("TypeError")
	}
	set := map[string]*big.Int{}
	for _, v := range items {
		n := number(v)
		if n == nil {
			return nil, errors.New("TypeError")
		}
		set[n.String()] = n
	}
	out := make([]*big.Int, 0, len(set))
	for _, n := range set {
		out = append(out, n)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Cmp(out[j]) < 0 })
	return out, nil
}
func normalizedType(v any) any {
	if v == "未知" {
		return nil
	}
	return v
}
func pluginTypeMatch(c *Context, s *Subscription) bool {
	want := normalizedType(s.Get("type"))
	if !truth(want) {
		return true
	}
	for _, v := range []any{c.Media.Get("type"), metaGet(c.Meta, "type")} {
		v = normalizedType(v)
		if v != nil && !pyEqual(v, want) {
			return false
		}
	}
	return true
}
func metaGet(m *releasemeta.MetaObject, k string) any {
	if m == nil {
		return nil
	}
	return m.Get(k)
}
func normalizedInt(v any) *big.Int {
	if v == nil {
		return nil
	}
	n, e := pyInt(v)
	if e != nil {
		return nil
	}
	return n
}
func pluginSeasonMatch(c *Context, s *Subscription) bool {
	target := normalizedInt(s.Get("season"))
	if target == nil {
		return true
	}
	if c.Meta != nil {
		if c.Meta.Has("season_list") {
			set := map[string]bool{}
			for _, v := range stringSlice(c.Meta.Get("season_list")) {
				if n := normalizedInt(v); n != nil {
					set[n.String()] = true
				}
			}
			if len(set) > 0 {
				return set[target.String()]
			}
		}
		begin, end := normalizedInt(c.Meta.Get("begin_season")), normalizedInt(c.Meta.Get("end_season"))
		if begin != nil && end != nil {
			if begin.Cmp(end) > 0 {
				begin, end = end, begin
			}
			return target.Cmp(begin) >= 0 && target.Cmp(end) <= 0
		}
		if begin != nil {
			return target.Cmp(begin) == 0
		}
		if end != nil {
			return target.Cmp(end) == 0
		}
	}
	season := normalizedInt(c.Media.Get("season"))
	return season == nil || target.Cmp(season) == 0
}
func contextIDs(c *Context, key string) map[string]bool {
	out := map[string]bool{}
	for _, k := range identityFields[key] {
		var v any
		if strings.Contains(k, "_") {
			v = c.Media.Get(k)
		} else {
			v = metaGet(c.Meta, k)
		}
		if id := normalizeID(v); id != "" {
			out[id] = true
		}
	}
	return out
}
func contextIdentities(c *Context) map[Identity]bool {
	out := map[Identity]bool{}
	for _, id := range []Identity{ResolveIdentity(c.Media), metaIdentity(c.Meta)} {
		if id.Source != "" && id.ID != "" {
			out[id] = true
		}
	}
	return out
}
func PluginStrictMatch(c *Context, s *Subscription) bool {
	if !pluginTypeMatch(c, s) || !pluginSeasonMatch(c, s) {
		return false
	}
	for _, src := range identitySources {
		key := identityFields[src][1]
		if id := normalizeID(s.Get(key)); id != "" && contextIDs(c, src)[id] {
			return true
		}
	}
	id := ResolveIdentity(s)
	return id.Source != "" && id.ID != "" && contextIdentities(c)[id]
}
func PluginTitleMatch(c *Context, s *Subscription) (bool, error) {
	for _, src := range identitySources {
		if len(contextIDs(c, src)) > 0 {
			return false, nil
		}
	}
	if len(contextIdentities(c)) > 0 || !pluginTypeMatch(c, s) || !pluginSeasonMatch(c, s) {
		return false, nil
	}
	title, e := clearUpper(s.Get("name"))
	if e != nil || title == "" {
		return false, e
	}
	var name any
	if c.Meta != nil {
		name = c.Meta.Name()
	}
	for _, v := range []any{c.Torrent.Get("title"), metaGet(c.Meta, "title"), name} {
		candidate, e := clearUpper(v)
		if e != nil {
			return false, e
		}
		if candidate != "" && strings.Contains(candidate, title) {
			return true, nil
		}
	}
	return false, nil
}
