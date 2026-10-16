// MetaObject preserves the complete installed MetaBase/MetaInfo instance state.
// Source: actual installed MetaBase SHA0156c105bf92991332a8742b9a4af4e1f3a74cf4b6f08107e0ed6de9d546f292.
package releasemeta

import (
	"encoding/json"
	"errors"
	"math/big"
	"strings"
)

// MetaObject retains instance-key presence separately from class defaults. A missing
// legacy instance attribute and an explicitly assigned Rust null are distinct.
// Numeric values are *big.Int/json.Number, never inferred via float64.
type MetaObject struct {
	Instance map[string]any
	Kind     string
}

var defaults = map[string]any{"isfile": false, "title": "", "type": "未知", "total_season": json.Number("0"), "total_episode": json.Number("0")}

func EmptyMetaObject() *MetaObject              { return &MetaObject{Instance: map[string]any{}} }
func (o *MetaObject) Set(key string, value any) { o.Instance[key] = value }
func (o *MetaObject) Get(key string) any {
	if v, ok := o.Instance[key]; ok {
		return v
	}
	return defaults[key]
}
func (o *MetaObject) Text(key string) string { v, _ := o.Get(key).(string); return v }
func number(v any) *big.Int {
	switch value := v.(type) {
	case *big.Int:
		if value != nil {
			return new(big.Int).Set(value)
		}
	case json.Number:
		n, _ := new(big.Int).SetString(string(value), 10)
		return n
	case int:
		return big.NewInt(int64(value))
	case int64:
		return big.NewInt(value)
	}
	return nil
}
func (o *MetaObject) Number(key string) *big.Int { return number(o.Get(key)) }
func truth(v any) bool {
	if n := number(v); n != nil {
		return n.Sign() != 0
	}
	switch value := v.(type) {
	case nil:
		return false
	case bool:
		return value
	case string:
		return value != ""
	case []any:
		return len(value) > 0
	case []string:
		return len(value) > 0
	case map[string]any:
		return len(value) > 0
	}
	return true
}
func allChinese(value string) bool {
	for _, r := range value {
		if r != ' ' && (r < '\u4e00' || r > '\u9fff') {
			return false
		}
	}
	return true
}
func (o *MetaObject) Name() string {
	cn, en := o.Text("cn_name"), o.Text("en_name")
	if cn != "" && allChinese(cn) {
		return cn
	}
	if en != "" {
		return en
	}
	return cn
}
func label(n *big.Int) string {
	s := n.String()
	if len(s) < 2 {
		return "0" + s
	}
	return s
}
func (o *MetaObject) rangeLabel(prefix, begin, end string) string {
	first := o.Number(begin)
	if first == nil {
		return ""
	}
	out := prefix + label(first)
	if last := o.Number(end); last != nil {
		out += "-" + prefix + label(last)
	}
	return out
}
func (o *MetaObject) Season() string {
	if o.Number("begin_season") != nil {
		return o.rangeLabel("S", "begin_season", "end_season")
	}
	if o.Text("type") == "电视剧" {
		return "S01"
	}
	return ""
}
func (o *MetaObject) Episode() string { return o.rangeLabel("E", "begin_episode", "end_episode") }

// List returns an explicit state error if materializing an enormous source range
// would exhaust the configured metadata policy. This is not a declared source equivalence.
func (o *MetaObject) List(begin, end string) ([]*big.Int, error) {
	first, last := o.Number(begin), o.Number(end)
	out := []*big.Int{}
	if first == nil {
		return out, nil
	}
	if last == nil {
		return append(out, first), nil
	}
	count := new(big.Int).Sub(last, first)
	count.Add(count, big.NewInt(1))
	if count.Sign() < 0 {
		return out, nil
	}
	if !count.IsInt64() || count.Int64() > 100000 {
		return nil, errors.New("metainfo_materialization_limit")
	}
	for n := new(big.Int).Set(first); n.Cmp(last) <= 0; n.Add(n, big.NewInt(1)) {
		out = append(out, new(big.Int).Set(n))
	}
	return out, nil
}
func (o *MetaObject) SeasonList() ([]*big.Int, error) {
	if o.Number("begin_season") == nil && o.Text("type") == "电视剧" {
		return []*big.Int{big.NewInt(1)}, nil
	}
	return o.List("begin_season", "end_season")
}
func (o *MetaObject) EpisodeList() ([]*big.Int, error) { return o.List("begin_episode", "end_episode") }
func (o *MetaObject) Derived() (map[string]any, error) {
	seasons, err := o.SeasonList()
	if err != nil {
		return nil, err
	}
	episodes, err := o.EpisodeList()
	if err != nil {
		return nil, err
	}
	sea, seasonSeq := "", ""
	if n := o.Number("begin_season"); n != nil {
		sea = o.Season()
		seasonSeq = n.String()
	} else if o.Text("type") == "电视剧" {
		seasonSeq = "1"
	}
	epSeq, epSeqs := "", ""
	if len(episodes) > 0 {
		epSeq = episodes[0].String()
		epSeqs = epSeq
		if len(episodes) > 1 {
			epSeqs += "-" + episodes[len(episodes)-1].String()
		}
	}
	labels := []string{}
	for _, ep := range episodes {
		labels = append(labels, label(ep))
	}
	seasonEpisode := ""
	if o.Text("type") == "电视剧" {
		seasonEpisode = o.Season()
		if e := o.Episode(); e != "" {
			if seasonEpisode != "" {
				seasonEpisode += " "
			}
			seasonEpisode += e
		}
	}
	resource := ""
	for _, key := range []string{"resource_type", "resource_effect", "resource_pix"} {
		if text := o.Text(key); text != "" {
			resource += " " + text
		}
	}
	edition := ""
	for _, key := range []string{"resource_type", "resource_effect"} {
		if text := o.Text(key); text != "" {
			edition += " " + text
		}
	}
	var fps any
	if truth(o.Get("fps")) {
		fps = o.Get("fps")
	}
	return map[string]any{"name": o.Name(), "season": o.Season(), "sea": sea, "season_seq": seasonSeq, "season_list": seasons, "episode": o.Episode(), "episode_list": episodes, "episodes": "E" + strings.Join(labels, "E"), "episode_seqs": epSeqs, "episode_seq": epSeq, "season_episode": seasonEpisode, "resource_term": resource, "edition": strings.TrimSpace(edition), "release_group": o.Text("resource_team"), "video_term": o.Text("video_encode"), "audio_term": o.Text("audio_encode"), "frame_rate": fps}, nil
}
func (o *MetaObject) ToDict() (map[string]any, error) {
	derived, err := o.Derived()
	if err != nil {
		return nil, err
	}
	out := map[string]any{}
	for key, value := range o.Instance {
		out[key] = value
	}
	typ := o.Get("type")
	if !truth(typ) {
		typ = nil
	}
	out["type"] = typ
	for _, key := range []string{"season_episode", "edition", "name", "episode_list"} {
		out[key] = derived[key]
	}
	return out, nil
}

// MergeLegacy preserves the source order and truthiness; Rust needs a separate
// None-only merge and must not silently share this implementation.
func (o *MetaObject) MergeLegacy(source *MetaObject) {
	if o.Text("type") == "未知" && source.Text("type") != "未知" {
		o.Set("type", source.Get("type"))
	}
	if o.Name() == "" {
		o.Set("cn_name", source.Get("cn_name"))
		o.Set("en_name", source.Get("en_name"))
	}
	for _, key := range []string{"original_name", "year"} {
		if !truth(o.Get(key)) {
			o.Set(key, source.Get(key))
		}
	}
	if o.Text("type") == "电视剧" {
		for _, group := range [][]string{{"begin_season", "end_season", "total_season"}, {"begin_episode", "end_episode", "total_episode"}} {
			if o.Get(group[0]) == nil {
				for _, key := range group {
					o.Set(key, source.Get(key))
				}
			}
		}
	}
	for _, key := range []string{"resource_type", "resource_pix", "resource_team", "customization", "resource_effect", "video_encode", "video_bit", "audio_encode", "fps", "part"} {
		if !truth(o.Get(key)) {
			o.Set(key, source.Get(key))
		}
	}
	for _, key := range []string{"tmdbid", "doubanid", "media_source", "media_id", "episode_group"} {
		if !truth(o.Get(key)) && truth(source.Get(key)) {
			o.Set(key, source.Get(key))
		}
	}
}
