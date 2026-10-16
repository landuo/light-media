package releasemeta

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"regexp"

	"light-media/internal/releasemeta/legacyanime"
)

// SetName follows the installed Python property setter. EmptyMetaObject is all-Chinese;
// setting it does not clear a previously present English name.
func (o *MetaObject) SetName(value any) error {
	s, ok := value.(string)
	if !ok {
		return errors.New("TypeError")
	}
	if allChinese(s) {
		o.Set("cn_name", s)
	} else {
		o.Set("en_name", s)
		o.Set("cn_name", nil)
	}
	return nil
}
func pythonString(value any) string {
	switch v := value.(type) {
	case nil:
		return "None"
	case string:
		return v
	case bool:
		if v {
			return "True"
		}
		return "False"
	case json.Number:
		return string(v)
	case *big.Int:
		return v.String()
	}
	return fmt.Sprint(value)
}
func pythonInteger(value any) (*big.Int, error) {
	if n := number(value); n != nil {
		return n, nil
	}
	switch v := value.(type) {
	case bool:
		if v {
			return big.NewInt(1), nil
		}
		return big.NewInt(0), nil
	case string:
		return legacyanime.Integer(v)
	case float64:
		if f := new(big.Float).SetFloat64(v); f != nil {
			n, _ := f.Int(nil)
			return n, nil
		}
	}
	return nil, errors.New("TypeError")
}
func (o *MetaObject) setRange(kind string, value any) error {
	if !truth(value) {
		return nil
	}
	setFirst := func(v any) error {
		n, e := legacyanime.Integer(pythonString(v))
		if e == nil {
			o.Set("begin_"+kind, n)
		}
		return e
	}
	var list []any
	switch v := value.(type) {
	case []any:
		list = v
	case []string:
		for _, s := range v {
			list = append(list, s)
		}
	}
	if list != nil {
		if len(list) == 1 && legacyanime.IsDigit(pythonString(list[0])) {
			if e := setFirst(list[0]); e != nil {
				return e
			}
			o.Set("end_"+kind, nil)
		} else if len(list) > 1 && legacyanime.IsDigit(pythonString(list[0])) && legacyanime.IsDigit(pythonString(list[len(list)-1])) {
			if e := setFirst(list[0]); e != nil {
				return e
			}
			n, e := legacyanime.Integer(pythonString(list[len(list)-1]))
			if e != nil {
				return e
			}
			o.Set("end_"+kind, n)
			if kind == "episode" {
				count := new(big.Int).Sub(n, o.Number("begin_episode"))
				count.Add(count, big.NewInt(1))
				o.Set("total_episode", count)
			}
		}
	} else if legacyanime.IsDigit(pythonString(value)) {
		if e := setFirst(value); e != nil {
			return e
		}
		o.Set("end_"+kind, nil)
	}
	return nil
}
func (o *MetaObject) SetSeason(v any) error  { return o.setRange("season", v) }
func (o *MetaObject) SetEpisode(v any) error { return o.setRange("episode", v) }
func (o *MetaObject) SetEpisodes(begin, end any) error {
	if truth(begin) {
		o.Set("begin_episode", begin)
	}
	if truth(end) {
		o.Set("end_episode", end)
	}
	if truth(o.Get("begin_episode")) && truth(o.Get("end_episode")) {
		a, b := o.Number("begin_episode"), o.Number("end_episode")
		if a == nil || b == nil {
			return errors.New("TypeError")
		}
		n := new(big.Int).Sub(b, a)
		n.Add(n, big.NewInt(1))
		o.Set("total_episode", n)
	}
	return nil
}
func pyEqual(a, b any) bool {
	numeric := func(v any) *big.Int {
		if n := number(v); n != nil {
			return n
		}
		if v, ok := v.(bool); ok {
			if v {
				return big.NewInt(1)
			}
			return big.NewInt(0)
		}
		return nil
	}
	if an, bn := numeric(a), numeric(b); an != nil && bn != nil {
		return an.Cmp(bn) == 0
	}
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	as, aok := a.(string)
	bs, bok := b.(string)
	return aok && bok && as == bs
}
func (o *MetaObject) isIn(kind string, value any) (bool, error) {
	begin, end := o.Get("begin_"+kind), o.Get("end_"+kind)
	if kind == "season" && begin == nil && end == nil {
		begin = big.NewInt(1)
	}
	var list []any
	switch v := value.(type) {
	case []any:
		list = v
	case []string:
		for _, x := range v {
			list = append(list, x)
		}
	}
	var inRange func(any) (bool, error)
	if end != nil {
		a, b := number(begin), number(end)
		inRange = func(x any) (bool, error) {
			if a == nil || b == nil {
				return false, errors.New("TypeError")
			}
			n := number(x)
			if v, ok := x.(bool); ok {
				if v {
					n = big.NewInt(1)
				} else {
					n = big.NewInt(0)
				}
			}
			return n != nil && n.Cmp(a) >= 0 && n.Cmp(b) <= 0, nil
		}
	} else {
		inRange = func(x any) (bool, error) { return pyEqual(x, begin), nil }
	}
	if list != nil {
		if end != nil && (number(begin) == nil || number(end) == nil) {
			return false, errors.New("TypeError")
		}
		for _, x := range list {
			ok, e := inRange(x)
			if e != nil || !ok {
				return false, e
			}
		}
		return true, nil
	}
	n, e := pythonInteger(value)
	if e != nil {
		return false, e
	}
	return inRange(n)
}
func (o *MetaObject) IsInSeason(v any) (bool, error)  { return o.isIn("season", v) }
func (o *MetaObject) IsInEpisode(v any) (bool, error) { return o.isIn("episode", v) }

// MergeRust ports parser.rs::merge_meta. Presence is tested, including Some("")
// and Some(0); neither episode_group nor web_source is merged by this version.
func (o *MetaObject) MergeRust(source *MetaObject) {
	if o.Text("type") == "未知" && source.Text("type") != "未知" {
		o.Set("type", source.Get("type"))
	}
	if o.rustName() == nil {
		o.Set("cn_name", source.Get("cn_name"))
		o.Set("en_name", source.Get("en_name"))
	}
	for _, key := range []string{"original_name", "year"} {
		if o.Get(key) == nil {
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
	for _, key := range []string{"resource_type", "resource_pix", "resource_team", "customization", "resource_effect", "video_encode", "video_bit", "audio_encode", "fps", "part", "tmdbid", "doubanid", "media_source", "media_id"} {
		if o.Get(key) == nil {
			o.Set(key, source.Get(key))
		}
	}
}
func (o *MetaObject) rustName() *string {
	var value any
	if cn, ok := o.Get("cn_name").(string); ok && allChinese(cn) {
		value = cn
	} else if o.Get("en_name") != nil {
		value = o.Get("en_name")
	} else {
		value = o.Get("cn_name")
	}
	if s, ok := value.(string); ok && s != "" {
		return &s
	}
	return nil
}

var videoBitPattern = regexp.MustCompile(`(?:^|[^A-Za-z0-9İıſK])(8|10|12|16)[\x{9}-\x{d}\x{20}\x{85}\x{a0}\x{1680}\x{2000}-\x{200a}\x{2028}-\x{2029}\x{202f}\x{205f}\x{3000}._-]*[bB][iIİ][tT][sSſ]?(?:[^A-Za-z0-9İıſK]|$)`)

func ExtractVideoBit(value string) *string {
	if m := videoBitPattern.FindStringSubmatch(value); m != nil {
		s := m[1] + "bit"
		return &s
	}
	return nil
}
