// Package noticetemplate implements the original notification template boundary
// in Go. Object projections are supplied by trusted business adapters.
package noticetemplate

import (
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"light-media/internal/jinja/exec"
	"light-media/internal/releasemeta/legacyanime"
)

type Object = map[string]any
type SourceError struct{ Class string }

func (e SourceError) Error() string { return e.Class }

// ContextInput contains original attribute values, not renamed application DTOs.
// Poster/backdrop are the already evaluated original get_*_image results.
// Nil means no object; a non-nil empty object still exists and can raise the
// source AttributeError. Torrent is intentionally mutated when HTML is removed.
type ContextInput struct {
	Meta, Media, Torrent, Transfer Object
	Episodes                       []Object
	FileExtension                  any
	IncludeRawObjects              *bool
	Extra                          Object
}

func truth(v any) bool {
	if v == nil {
		return false
	}
	switch n := v.(type) {
	case bool:
		return n
	case json.Number:
		f, _ := strconv.ParseFloat(string(n), 64)
		return f != 0
	case *big.Int:
		return n.Sign() != 0
	}
	r := reflect.ValueOf(v)
	switch r.Kind() {
	case reflect.String, reflect.Array, reflect.Slice, reflect.Map:
		return r.Len() != 0
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return r.Int() != 0
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return r.Uint() != 0
	case reflect.Float32, reflect.Float64:
		return r.Float() != 0
	}
	return true
}
func sourceString(v any) (string, error) {
	switch x := v.(type) {
	case nil:
		return "None", nil
	case string:
		return x, nil
	case bool:
		if x {
			return "True", nil
		}
		return "False", nil
	case json.Number:
		if !strings.ContainsAny(string(x), ".eE") {
			return string(x), nil
		}
		f, e := strconv.ParseFloat(string(x), 64)
		if e != nil {
			return "", SourceError{"ValueError"}
		}
		return floatString(f), nil
	case *big.Int:
		return x.String(), nil
	case float64:
		return floatString(x), nil
	case float32:
		return floatString(float64(x)), nil
	}
	r := reflect.ValueOf(v)
	if r.IsValid() {
		switch r.Kind() {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			return strconv.FormatInt(r.Int(), 10), nil
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			return strconv.FormatUint(r.Uint(), 10), nil
		}
	}
	return "", SourceError{"TypeError"}
}
func floatString(f float64) string {
	if math.IsInf(f, 1) {
		return "inf"
	}
	if math.IsInf(f, -1) {
		return "-inf"
	}
	if math.IsNaN(f) {
		return "nan"
	}
	s := strconv.FormatFloat(f, 'g', -1, 64)
	if strings.Contains(s, "e") {
		parts := strings.Split(s, "e")
		exp, _ := strconv.Atoi(parts[1])
		if exp >= -4 && exp < 16 {
			s = strconv.FormatFloat(f, 'f', -1, 64)
		}
	}
	if !strings.ContainsAny(s, ".e") {
		s += ".0"
	}
	return s
}
func get(o Object, k string) (any, error) {
	v, ok := o[k]
	if !ok {
		return nil, SourceError{"AttributeError"}
	}
	return v, nil
}
func convertTitle(v any) (any, error) {
	if !truth(v) {
		return v, nil
	}
	s, ok := v.(string)
	if !ok {
		return nil, SourceError{"AttributeError"}
	}
	return strings.Map(func(r rune) rune {
		if strings.ContainsRune(`\/:*?"<>|`, r) {
			return r + 0xFEE0
		}
		return r
	}, s), nil
}
func seq(v any) ([]any, error) {
	if v == nil {
		return nil, SourceError{"TypeError"}
	}
	r := reflect.ValueOf(v)
	if r.Kind() != reflect.Slice && r.Kind() != reflect.Array {
		return nil, SourceError{"TypeError"}
	}
	out := make([]any, r.Len())
	for i := range out {
		out[i] = r.Index(i).Interface()
	}
	return out, nil
}
func join(v any, sep string) (string, error) {
	values, e := seq(v)
	if e != nil {
		return "", e
	}
	out := make([]string, len(values))
	for i, x := range values {
		s, ok := x.(string)
		if !ok {
			return "", SourceError{"TypeError"}
		}
		out[i] = s
	}
	return strings.Join(out, sep), nil
}
func isDigit(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if unicode.IsDigit(r) {
			continue
		}
		if strings.ContainsRune("²³¹⁰⁴⁵⁶⁷⁸⁹₀₁₂₃₄₅₆₇₈₉", r) {
			continue
		}
		return false
	}
	return true
}
func decimalASCII(s string) string {
	var out strings.Builder
	for _, r := range s {
		if r > 127 && unicode.IsDigit(r) {
			found := false
			for _, rg := range unicode.Digit.R16 {
				if uint32(r) >= uint32(rg.Lo) && uint32(r) <= uint32(rg.Hi) && (uint32(r)-uint32(rg.Lo))%uint32(rg.Stride) == 0 {
					out.WriteByte('0' + byte((uint32(r)-uint32(rg.Lo))/uint32(rg.Stride)%10))
					found = true
					break
				}
			}
			if !found {
				for _, rg := range unicode.Digit.R32 {
					if uint32(r) >= rg.Lo && uint32(r) <= rg.Hi && (uint32(r)-rg.Lo)%rg.Stride == 0 {
						out.WriteByte('0' + byte((uint32(r)-rg.Lo)/rg.Stride%10))
						found = true
						break
					}
				}
			}
			if found {
				continue
			}
		}
		out.WriteRune(r)
	}
	return out.String()
}

var stripSize = regexp.MustCompile(`[\t-\r\x1c-\x20\x{0085}\x{00a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}]|B|iB`)

func fileSize(v any) (string, error) {
	if v == nil {
		return "", nil
	}
	s, e := sourceString(v)
	if e != nil {
		return "", e
	}
	// Source passes re.I as the fourth positional argument: count=2, not flags.
	// Preserve both its two-substitution limit and case-sensitive matching.
	indices := stripSize.FindAllStringIndex(s, 2)
	for i := len(indices) - 1; i >= 0; i-- {
		s = s[:indices[i][0]] + s[indices[i][1]:]
	}
	if isDigit(strings.ReplaceAll(s, ".", "")) {
		f, e := strconv.ParseFloat(decimalASCII(s), 64)
		if e != nil && !math.IsInf(f, 0) {
			return "", nil
		}
		bounds := []float64{1023, 1048575, 1073741823, 1099511627775}
		idx := -1
		for i, b := range bounds {
			if f <= b {
				idx = i - 1
				break
			}
			idx = i
		}
		if idx < 0 {
			return floatString(f) + "B", nil
		}
		denom := bounds[idx] + 1
		val := f / denom
		if !math.IsInf(val, 0) {
			rounded, _ := strconv.ParseFloat(strconv.FormatFloat(val, 'f', 2, 64), 64)
			val = rounded
		}
		return floatString(val) + []string{"K", "M", "G", "T"}[idx], nil
	}
	if strings.ContainsAny(s, "KMGTPkmgtp") {
		return s, nil
	}
	return s + "B", nil
}

func BuildContext(in ContextInput) (Object, error) {
	out := Object{}
	if in.Meta != nil {
		m := in.Meta
		for target, source := range map[string]string{"original_name": "title", "name": "name", "en_name": "en_name", "year": "year", "season": "season_seq", "season_fmt": "season", "episode": "episode_seqs", "part": "part", "customization": "customization", "fps": "fps", "resourceType": "resource_type", "effect": "resource_effect", "edition": "edition", "videoFormat": "resource_pix", "resource_term": "resource_term", "releaseGroup": "resource_team", "videoCodec": "video_encode", "videoBit": "video_bit", "audioCodec": "audio_encode", "webSource": "web_source"} {
			v, e := get(m, source)
			if e != nil {
				return nil, e
			}
			out[target] = v
		}
		out["title_year"] = m["name"]
		if truth(m["year"]) {
			name, e := sourceString(m["name"])
			if e != nil {
				return nil, e
			}
			year, e := sourceString(m["year"])
			if e != nil {
				return nil, e
			}
			out["title_year"] = name + " (" + year + ")"
		}
		season, e := sourceString(m["season"])
		if e != nil {
			return nil, e
		}
		ep, e := get(m, "episode")
		if e != nil {
			return nil, e
		}
		episode, e := sourceString(ep)
		if e != nil {
			return nil, e
		}
		out["season_episode"] = season + episode
		out["total_episodes"] = len(in.Episodes)
		out["episode_title"], out["episode_date"] = nil, nil
		begin, e := get(m, "begin_episode")
		if e != nil {
			return nil, e
		}
		if truth(begin) {
			for _, row := range in.Episodes {
				if numericEqual(row["episode_number"], begin) {
					t, e := convertTitle(row["name"])
					if e != nil {
						return nil, e
					}
					out["episode_title"] = t
					if truth(row["air_date"]) {
						out["episode_date"] = row["air_date"]
					}
					break
				}
			}
		}
	}
	if in.Media != nil {
		m := in.Media
		for _, key := range []string{"title", "en_title", "original_title"} {
			v, e := get(m, key)
			if e != nil {
				return nil, e
			}
			v, e = convertTitle(v)
			if e != nil {
				return nil, e
			}
			out[key] = v
		}
		metaSeason := out["season"]
		season, e := get(m, "season")
		if e != nil {
			return nil, e
		}
		// The original eagerly formats this even when a meta season wins below.
		if season != nil {
			if _, e := formatInt(season); e != nil {
				return nil, e
			}
		}
		if !truth(out["season"]) {
			out["season"] = season
		}
		if !truth(out["season_fmt"]) {
			if season == nil {
				out["season_fmt"] = nil
			} else {
				n, e := formatInt(season)
				if e != nil {
					return nil, e
				}
				negative := n.Sign() < 0
				abs := new(big.Int).Abs(n).String()
				if negative {
					if len(abs) < 1 {
						abs = "0" + abs
					}
					out["season_fmt"] = "S-" + abs
				} else {
					if len(abs) < 2 {
						abs = "0" + abs
					}
					out["season_fmt"] = "S" + abs
				}
			}
		}
		for _, key := range []string{"year", "title_year"} {
			v, e := get(m, key)
			if e != nil {
				return nil, e
			}
			if truth(v) {
				out[key] = v
			}
		}
		for target, source := range map[string]string{"type": "type", "category": "category", "vote_average": "vote_average", "poster": "poster", "backdrop": "backdrop", "overview": "overview", "tmdbid": "tmdb_id", "imdbid": "imdb_id", "doubanid": "douban_id", "bangumiid": "bangumi_id", "anilistid": "anilist_id"} {
			v, e := get(m, source)
			if e != nil {
				return nil, e
			}
			out[target] = v
		}
		out["season_year"] = nil
		years, e := get(m, "season_years")
		if e != nil {
			return nil, e
		}
		hasYears := truth(years)
		if ordered, ok := years.(*exec.Dict); ok {
			hasYears = ordered != nil && len(ordered.Pairs) > 0
		}
		if hasYears && truth(metaSeason) {
			n, e := sourceInt(metaSeason)
			if e != nil {
				return nil, e
			}
			switch y := years.(type) {
			case *exec.Dict:
				if y == nil {
					return nil, SourceError{"AttributeError"}
				}
				for _, pair := range y.Pairs {
					if integerDictionaryKeyEqual(pair.Key.Interface(), n) {
						out["season_year"] = pair.Value.Interface()
						break
					}
				}
			case map[string]any:
				out["season_year"] = y[n.String()]
			case map[int]any:
				if n.IsInt64() {
					out["season_year"] = y[int(n.Int64())]
				}
			default:
				return nil, SourceError{"AttributeError"}
			}
		}
		actors, e := get(m, "actors")
		if e != nil {
			return nil, e
		}
		rows, e := seq(actors)
		if e != nil {
			return nil, e
		}
		if len(rows) > 5 {
			rows = rows[:5]
		}
		names := []any{}
		for _, row := range rows {
			o, ok := row.(map[string]any)
			if !ok {
				return nil, SourceError{"TypeError"}
			}
			name, ok := o["name"]
			if !ok {
				return nil, SourceError{"KeyError"}
			}
			names = append(names, name)
		}
		out["actors"], e = join(names, "、 ")
		if e != nil {
			return nil, e
		}
		source, e := get(m, "source")
		if e != nil {
			return nil, e
		}
		ids := map[string]any{"themoviedb": m["tmdb_id"], "douban": m["douban_id"], "bangumi": m["bangumi_id"], "anilist": m["anilist_id"]}
		if !truth(source) {
			source = nil
			for _, k := range []string{"themoviedb", "douban", "bangumi", "anilist"} {
				if ids[k] != nil {
					source = k
					break
				}
			}
		}
		out["media_source"] = source
		id, e := get(m, "media_id")
		if e != nil {
			return nil, e
		}
		if !truth(id) {
			if key, ok := source.(string); ok {
				id = ids[key]
			} else {
				id = nil
				if source != nil {
					kind := reflect.ValueOf(source).Kind()
					if kind == reflect.Map || kind == reflect.Slice {
						return nil, SourceError{"TypeError"}
					}
				}
			}
		}
		if id != nil {
			id, e = sourceString(id)
			if e != nil {
				return nil, e
			}
		}
		out["media_id"] = id
	}
	if in.Transfer != nil {
		for target, source := range map[string]string{"transfer_type": "transfer_type", "file_count": "file_count", "err_msg": "message"} {
			v, e := get(in.Transfer, source)
			if e != nil {
				return nil, e
			}
			out[target] = v
		}
		size, e := get(in.Transfer, "total_size")
		if e != nil {
			return nil, e
		}
		out["total_size"], e = fileSize(size)
		if e != nil {
			return nil, e
		}
	}
	if in.Torrent != nil {
		t := in.Torrent
		size, e := get(t, "size")
		if e != nil {
			return nil, e
		}
		if truth(size) {
			s, e := sourceString(size)
			if e != nil {
				return nil, e
			}
			if isDigit(strings.ReplaceAll(s, ".", "")) {
				size, e = fileSize(size)
				if e != nil {
					return nil, e
				}
			}
		} else {
			size = 0
		}
		desc, e := get(t, "description")
		if e != nil {
			return nil, e
		}
		if truth(desc) {
			s, ok := desc.(string)
			if !ok {
				return nil, SourceError{"TypeError"}
			}
			t["description"] = htmlTags.ReplaceAllString(s, "")
		}
		for target, source := range map[string]string{"torrent_title": "title", "pubdate": "pubdate", "freedate": "freedate_diff", "seeders": "seeders", "volume_factor": "volume_factor", "description": "description", "site_name": "site_name"} {
			v, e := get(t, source)
			if e != nil {
				return nil, e
			}
			out[target] = v
		}
		hit, e := get(t, "hit_and_run")
		if e != nil {
			return nil, e
		}
		out["hit_and_run"] = "否"
		if truth(hit) {
			out["hit_and_run"] = "是"
		}
		labels, e := get(t, "labels")
		if e != nil {
			return nil, e
		}
		out["labels"], e = join(labels, " ")
		if e != nil {
			return nil, e
		}
		out["size"] = size
	}
	if truth(in.FileExtension) {
		out["fileExt"] = in.FileExtension
	}
	for k, v := range in.Extra {
		out[k] = v
	}
	if in.IncludeRawObjects == nil || *in.IncludeRawObjects {
		if in.Meta != nil {
			out["__meta__"] = in.Meta
		} else {
			out["__meta__"] = nil
		}
		if in.Media != nil {
			raw := Object{}
			for k, v := range in.Media {
				raw[k] = v
			}
			raw["type"] = Object{"value": in.Media["type"]}
			out["__mediainfo__"] = raw
		} else {
			out["__mediainfo__"] = nil
		}
		if in.Torrent != nil {
			out["__torrentinfo__"] = in.Torrent
		} else {
			out["__torrentinfo__"] = nil
		}
		if in.Transfer != nil {
			out["__transferinfo__"] = in.Transfer
		} else {
			out["__transferinfo__"] = nil
		}
		if in.Episodes != nil {
			out["__episodes_info__"] = in.Episodes
		} else {
			out["__episodes_info__"] = nil
		}
	}
	for k, v := range out {
		if v == nil {
			delete(out, k)
		}
	}
	return out, nil
}

// Python dict int-key equality includes True == 1 and integral float keys,
// while the string "1" is a distinct key. Do not stringify the ordered keys.
func integerDictionaryKeyEqual(key any, n *big.Int) bool {
	if _, text := key.(string); text {
		return false
	}
	if key == nil {
		return false
	}
	if v, ok := key.(*big.Int); ok {
		return v != nil && v.Cmp(n) == 0
	}
	if v, ok := key.(bool); ok {
		return v && n.Cmp(big.NewInt(1)) == 0 || !v && n.Sign() == 0
	}
	if v, ok := key.(json.Number); ok {
		r, valid := new(big.Rat).SetString(string(v))
		return valid && r.Cmp(new(big.Rat).SetInt(n)) == 0
	}
	v := reflect.ValueOf(key)
	switch v.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return big.NewInt(v.Int()).Cmp(n) == 0
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return new(big.Int).SetUint64(v.Uint()).Cmp(n) == 0
	case reflect.Float32, reflect.Float64:
		r := new(big.Rat).SetFloat64(v.Float())
		return r != nil && r.Cmp(new(big.Rat).SetInt(n)) == 0
	}
	return false
}

var htmlTags = regexp.MustCompile(`(?s)<[^>]+>`)

func sourceInt(v any) (*big.Int, error) {
	if b, ok := v.(bool); ok {
		if b {
			return big.NewInt(1), nil
		}
		return big.NewInt(0), nil
	}
	if s, ok := v.(string); ok {
		n, e := legacyanime.Integer(s)
		if e != nil {
			return nil, SourceError{"ValueError"}
		}
		return n, nil
	}
	s, e := sourceString(v)
	if e != nil {
		return nil, e
	}
	if n, ok := new(big.Int).SetString(s, 10); ok {
		return n, nil
	}
	f, e := strconv.ParseFloat(s, 64)
	if e != nil || math.IsInf(f, 0) || math.IsNaN(f) {
		return nil, SourceError{"ValueError"}
	}
	n, _ := new(big.Float).SetFloat64(f).Int(nil)
	return n, nil
}
func formatInt(v any) (*big.Int, error) {
	switch x := v.(type) {
	case string:
		return nil, SourceError{"ValueError"}
	case json.Number:
		if strings.ContainsAny(string(x), ".eE") {
			return nil, SourceError{"ValueError"}
		}
	case float64, float32:
		return nil, SourceError{"ValueError"}
	}
	return sourceInt(v)
}
func numericEqual(a, b any) bool {
	_, as := a.(string)
	_, bs := b.(string)
	if as || bs {
		return reflect.DeepEqual(a, b)
	}
	sa, ea := sourceString(a)
	sb, eb := sourceString(b)
	if ea != nil || eb != nil {
		return false
	}
	ra, ok1 := new(big.Rat).SetString(sa)
	rb, ok2 := new(big.Rat).SetString(sb)
	if ok1 && ok2 {
		return ra.Cmp(rb) == 0
	}
	return fmt.Sprint(a) == fmt.Sprint(b)
}
