package rsscontext

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"light-media/internal/releasemeta/legacyanime"
	"math"
	"math/big"
	"strconv"
	"strings"
)

//go:embed schema.json
var installedSchema []byte
var schema struct {
	Defaults   map[string]map[string]any
	Properties map[string][]string
	Casefold   map[string]string
	Unicode    string
}

func init() {
	d := json.NewDecoder(strings.NewReader(string(installedSchema)))
	d.UseNumber()
	if e := d.Decode(&schema); e != nil {
		panic(e)
	}
}
func deep(v any) any {
	switch x := v.(type) {
	case map[string]any:
		r := map[string]any{}
		for k, v := range x {
			r[k] = deep(v)
		}
		return r
	case []any:
		if x == nil {
			return []any(nil)
		}
		r := make([]any, len(x))
		for i, v := range x {
			r[i] = deep(v)
		}
		return r
	case []string:
		if x == nil {
			return []string(nil)
		}
		return append(make([]string, 0, len(x)), x...)
	case []*big.Int:
		if x == nil {
			return []*big.Int(nil)
		}
		out := make([]*big.Int, len(x))
		for i, v := range x {
			if v != nil {
				out[i] = new(big.Int).Set(v)
			}
		}
		return out
	case []int:
		if x == nil {
			return []int(nil)
		}
		return append(make([]int, 0, len(x)), x...)
	case *big.Int:
		if x != nil {
			return new(big.Int).Set(x)
		}
	}
	return v
}
func number(v any) *big.Int {
	switch x := v.(type) {
	case *big.Int:
		if x != nil {
			return new(big.Int).Set(x)
		}
	case json.Number:
		n, _ := new(big.Int).SetString(string(x), 10)
		return n
	case int:
		return big.NewInt(int64(x))
	case int64:
		return big.NewInt(x)
	case bool:
		if x {
			return big.NewInt(1)
		}
		return new(big.Int)
	}
	return nil
}
func truth(v any) bool {
	if n := number(v); n != nil {
		return n.Sign() != 0
	}
	switch x := v.(type) {
	case nil:
		return false
	case string:
		return x != ""
	case bool:
		return x
	case []any:
		return len(x) > 0
	case []string:
		return len(x) > 0
	case map[string]any:
		return len(x) > 0
	case float64:
		return x != 0
	case json.Number:
		n, e := strconv.ParseFloat(string(x), 64)
		return e == nil && n != 0
	}
	return true
}
func pyString(v any) string {
	switch x := v.(type) {
	case nil:
		return "None"
	case string:
		return x
	case bool:
		if x {
			return "True"
		}
		return "False"
	case json.Number:
		if strings.ContainsAny(string(x), ".eE") {
			f, e := strconv.ParseFloat(string(x), 64)
			if e == nil {
				return pythonFloat(f)
			}
		}
		return string(x)
	case *big.Int:
		return x.String()
	case float64:
		return pythonFloat(x)
	}
	return fmt.Sprint(v)
}
func pythonFloat(f float64) string {
	format := byte('g')
	a := math.Abs(f)
	if a == 0 || a >= 1e-4 && a < 1e16 {
		format = 'f'
	}
	s := strconv.FormatFloat(f, format, -1, 64)
	if !strings.ContainsAny(s, ".eE") {
		s += ".0"
	}
	return s
}
func normalizeID(v any) string {
	if v == nil {
		return ""
	}
	return legacyanime.Strip(pyString(v))
}
func numericRat(value any) *big.Rat {
	if n := number(value); n != nil {
		return new(big.Rat).SetInt(n)
	}
	var f float64
	switch v := value.(type) {
	case float64:
		f = v
	case json.Number:
		var e error
		f, e = strconv.ParseFloat(string(v), 64)
		if e != nil {
			return nil
		}
	default:
		return nil
	}
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return nil
	}
	return new(big.Rat).SetFloat64(f)
}
func pyEqual(a, b any) bool {
	if an, bn := numericRat(a), numericRat(b); an != nil && bn != nil {
		return an.Cmp(bn) == 0
	}
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	as, aok := a.(string)
	bs, bok := b.(string)
	return aok && bok && as == bs
}
func pyInt(v any) (*big.Int, error) {
	if n := number(v); n != nil {
		return n, nil
	}
	if s, ok := v.(string); ok {
		return legacyanime.Integer(s)
	}
	if n, ok := v.(json.Number); ok {
		f, e := strconv.ParseFloat(string(n), 64)
		if e == nil {
			b, _ := new(big.Float).SetFloat64(f).Int(nil)
			return b, nil
		}
	}
	if f, ok := v.(float64); ok && !math.IsInf(f, 0) && !math.IsNaN(f) {
		b, _ := new(big.Float).SetFloat64(f).Int(nil)
		return b, nil
	}
	return nil, errors.New("TypeError")
}
func casefold(s string) string {
	var b strings.Builder
	for _, r := range s {
		if v, ok := schema.Casefold[strconv.Itoa(int(r))]; ok {
			b.WriteString(v)
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}
func upper(s string) string {
	var b strings.Builder
	for _, r := range s {
		if v, ok := pythonUpperMappings[r]; ok {
			b.WriteString(v)
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

type MediaType string

type MediaInfo struct{ Fields map[string]any }

func NewMediaInfo(fields map[string]any) (*MediaInfo, error) {
	m := &MediaInfo{Fields: deep(schema.Defaults["MediaInfo"]).(map[string]any)}
	for k, v := range fields {
		if _, ok := schema.Defaults["MediaInfo"][k]; !ok {
			return nil, errors.New("TypeError")
		}
		m.Fields[k] = v
	}
	for _, key := range []string{"tmdb_info", "douban_info", "bangumi_info", "anilist_info"} {
		if truth(m.Fields[key]) {
			return nil, errors.New("provider_detail_normalizer_required")
		}
	}
	return m, nil
}
func NewNormalizedMediaInfo(fields map[string]any) (*MediaInfo, error) {
	copy := deep(fields)
	if copy == nil {
		copy = map[string]any{}
	}
	values := copy.(map[string]any)
	if text, ok := values["type"].(string); ok {
		if text != "电影" && text != "电视剧" && text != "未知" {
			return nil, errors.New("ValueError")
		}
		values["type"] = MediaType(text)
	}
	return NewMediaInfo(values)
}
func (m *MediaInfo) Get(k string) any {
	if m == nil {
		return nil
	}
	if v, ok := m.Fields[k]; ok {
		if typ, ok := v.(MediaType); ok {
			return string(typ)
		}
		return v
	}
	if k == "recognize_cache_hit" {
		return false
	}
	return nil
}
func (m *MediaInfo) Set(k string, v any) { m.Fields[k] = v }
func (m *MediaInfo) FromDict(fields map[string]any) error {
	for k, v := range fields {
		property := false
		for _, p := range schema.Properties["MediaInfo"] {
			if k == p {
				property = true
				break
			}
		}
		if !property {
			m.Set(k, v)
		}
	}
	if typ, ok := m.Get("type").(string); ok && typ != "电影" && typ != "电视剧" && typ != "未知" {
		return errors.New("ValueError")
	}
	if typ, ok := m.Fields["type"].(string); ok {
		m.Fields["type"] = MediaType(typ)
	}
	return nil
}
func (m *MediaInfo) DeepCopy() *MediaInfo {
	if m == nil {
		return nil
	}
	return &MediaInfo{Fields: deep(m.Fields).(map[string]any)}
}
func (m *MediaInfo) Clear() {
	for _, k := range []string{"tmdb_info", "douban_info", "bangumi_info", "anilist_info", "seasons", "next_episode_to_air"} {
		m.Set(k, map[string]any{})
	}
	for _, k := range []string{"genres", "season_info", "names", "actors", "directors", "production_companies", "production_countries", "spoken_languages", "networks", "episode_groups"} {
		m.Set(k, []any{})
	}
}
func (m *MediaInfo) TitleYear() string {
	if !truth(m.Get("title")) {
		return ""
	}
	if truth(m.Get("year")) {
		return pyString(m.Get("title")) + " (" + pyString(m.Get("year")) + ")"
	}
	return pyString(m.Get("title"))
}
func (m *MediaInfo) DetailLink() string {
	if truth(m.Get("tmdb_id")) {
		kind := "tv"
		if m.Get("type") == "电影" {
			kind = "movie"
		}
		return "https://www.themoviedb.org/" + kind + "/" + pyString(m.Get("tmdb_id"))
	}
	for _, s := range []struct{ k, url string }{{"douban_id", "https://movie.douban.com/subject/"}, {"bangumi_id", "http://bgm.tv/subject/"}, {"anilist_id", "https://anilist.co/anime/"}} {
		if truth(m.Get(s.k)) {
			return s.url + pyString(m.Get(s.k))
		}
	}
	return ""
}
func (m *MediaInfo) ToDict() (map[string]any, error) {
	if m == nil {
		return nil, nil
	}
	out := map[string]any{}
	for k, v := range m.Fields {
		out[k] = v
	}
	if truth(m.Fields["type"]) {
		if _, ok := m.Fields["type"].(MediaType); !ok {
			return nil, errors.New("AttributeError")
		}
	}
	typ := m.Get("type")
	if !truth(typ) {
		typ = nil
	}
	out["type"] = typ
	out["detail_link"] = m.DetailLink()
	out["title_year"] = m.TitleYear()
	ids := map[string]any{"themoviedb": m.Get("tmdb_id"), "douban": m.Get("douban_id"), "bangumi": m.Get("bangumi_id"), "anilist": m.Get("anilist_id")}
	source := m.Get("source")
	if !truth(source) {
		source = nil
		for _, key := range identitySources {
			if ids[key] != nil {
				source = key
				break
			}
		}
	}
	id := m.Get("media_id")
	if !truth(id) {
		if key, ok := source.(string); ok {
			id = ids[key]
		}
	}
	out["source"], out["mediaid_prefix"] = source, source
	if id == nil {
		out["media_id"] = nil
	} else {
		out["media_id"] = pyString(id)
	}
	for _, key := range []string{"tmdb_info", "douban_info", "bangumi_info", "anilist_info"} {
		out[key] = nil
	}
	return out, nil
}

type TorrentInfo struct{ Fields map[string]any }

func NewTorrentInfo(fields map[string]any) (*TorrentInfo, error) {
	t := &TorrentInfo{Fields: deep(schema.Defaults["TorrentInfo"]).(map[string]any)}
	for k, v := range fields {
		if _, ok := t.Fields[k]; !ok {
			return nil, errors.New("TypeError")
		}
		t.Fields[k] = v
	}
	return t, nil
}
func (t *TorrentInfo) Get(k string) any {
	if t == nil {
		return nil
	}
	return t.Fields[k]
}
func (t *TorrentInfo) Set(k string, v any) { t.Fields[k] = v }
func (t *TorrentInfo) DeepCopy() *TorrentInfo {
	if t == nil {
		return nil
	}
	return &TorrentInfo{Fields: deep(t.Fields).(map[string]any)}
}
func (t *TorrentInfo) ToDict() (map[string]any, error) {
	if t == nil {
		return nil, nil
	}
	if truth(t.Get("freedate")) {
		return nil, errors.New("freedate_clock_adapter_required")
	}
	out := map[string]any{}
	for k, v := range t.Fields {
		out[k] = v
	}
	factor := "未知"
	if t.Get("uploadvolumefactor") != nil && t.Get("downloadvolumefactor") != nil {
		u, _ := strconv.ParseFloat(pyString(t.Get("uploadvolumefactor")), 64)
		d, _ := strconv.ParseFloat(pyString(t.Get("downloadvolumefactor")), 64)
		factor = map[string]string{"1.00 1.00": "普通", "1.00 0.00": "免费", "2.00 1.00": "2X", "4.00 1.00": "4X", "2.00 0.00": "2X免费", "4.00 0.00": "4X免费", "1.00 0.50": "50%", "2.00 0.50": "2X 50%", "1.00 0.70": "70%", "1.00 0.30": "30%", "1.00 0.75": "75%", "1.00 0.25": "25%"}[fmt.Sprintf("%.2f %.2f", u, d)]
		if factor == "" {
			factor = "未知"
		}
	}
	out["volume_factor"], out["freedate_diff"] = factor, ""
	return out, nil
}
