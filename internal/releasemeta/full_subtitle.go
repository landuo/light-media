package releasemeta

import (
	"github.com/dlclark/regexp2/v2"
	"math/big"
	"strconv"
	"strings"
)

// Full-object subtitle updates retain arbitrary-precision existing legacy state;
// a pre-existing large season/episode does not make parsing a subtitle fail.
func fullSubtitle(m *MetaObject, text, mode string) error {
	if text == "" {
		return nil
	}
	text = " " + text + " "
	regexMode := mode
	if mode == "legacy" {
		regexMode = "legacy_metabase"
	}
	match := func(p string) *regexp2.Match { return fullRXFirst(p, text, true, regexMode) }
	arabic := func(s string) *big.Int {
		if mode == "legacy" {
			n, e := fullInt(s, mode)
			if e == nil {
				return n
			}
			return nil
		}
		n, e := strconv.ParseInt(s, 10, 64)
		if e != nil {
			return nil
		}
		return big.NewInt(n)
	}
	cn := func(s string) *big.Int {
		if mode == "legacy" {
			n, ok := fullLegacyChineseNumber(s)
			if ok {
				return n
			}
			return nil
		}
		n, ok := rustChineseNumber(s)
		if ok {
			return big.NewInt(n)
		}
		return nil
	}
	flag := func() { m.Set("type", "电视剧"); m.Set("_subtitle_flag", true) }
	update := func(kind string, begin, end *big.Int) {
		if m.Get("begin_"+kind) == nil {
			m.Set("begin_"+kind, begin)
			m.Set("total_"+kind, 1)
		}
		if m.Get("begin_"+kind) != nil && m.Get("end_"+kind) == nil && end != nil && m.Number("begin_"+kind).Cmp(end) != 0 {
			m.Set("end_"+kind, end)
			n := new(big.Int).Sub(end, m.Number("begin_"+kind))
			n.Add(n, big.NewInt(1))
			m.Set("total_"+kind, n)
		}
		flag()
	}
	under := func(n *big.Int, limit int64, inclusive bool) bool {
		if n == nil {
			return false
		}
		c := n.Cmp(big.NewInt(limit))
		return c < 0 || inclusive && c == 0
	}
	if cap := match(`Episode\s+(\d{1,4})`); cap != nil {
		n := arabic(cap.GroupByNumber(1).String())
		if n != nil && under(n, 10000, false) {
			if m.Get("begin_episode") == nil {
				m.Set("begin_episode", n)
				m.Set("total_episode", 1)
			}
			flag()
		}
		return nil
	}
	if fullRXHas(`[全第季集话話期幕]`, text, true, regexMode) {
		if cap := match(`[全共]\s*([0-9一二三四五六七八九十]+)\s*季`); cap != nil {
			n := cn(cap.GroupByNumber(1).String())
			if n != nil && m.Get("begin_season") == nil && m.Get("begin_episode") == nil {
				m.Set("total_season", n)
				m.Set("begin_season", 1)
				m.Set("end_season", n)
				flag()
			}
			return nil
		}
		if cap := match(`(?<![全共]\s*)[第\s]+([0-9一二三四五六七八九十S\-]+)\s*季(?!\s*[全共])`); cap != nil {
			parts := strings.Split(strings.ReplaceAll(strings.ToUpper(cap.GroupByNumber(1).String()), "S", ""), "-")
			a := cn(parts[0])
			var b *big.Int
			if len(parts) > 1 {
				b = cn(parts[1])
			}
			valid := under(a, 100, true) && (len(parts) == 1 || under(b, 100, true))
			if valid {
				update("season", a, b)
			} else if mode == "legacy" {
				return nil
			}
		}
		if cap := match(`[第]*\s*([0-9一二三四五六七八九十百零]+)\s*[集话話期幕]?\s*-\s*第*\s*([0-9一二三四五六七八九十百零]+)\s*[集话話期幕]`); cap != nil {
			a, b := cn(cap.GroupByNumber(1).String()), cn(cap.GroupByNumber(2).String())
			if under(a, 10000, false) && under(b, 10000, false) {
				update("episode", a, b)
				return nil
			} else if mode == "legacy" {
				return nil
			}
		}
		if cap := match(`(?<![全共]\s*)[第\s]+([0-9一二三四五六七八九十百零EP]+)\s*[集话話期幕](?!\s*[全共])`); cap != nil {
			parts := strings.Split(strings.NewReplacer("E", "", "P", "").Replace(strings.ToUpper(cap.GroupByNumber(1).String())), "-")
			a := cn(parts[0])
			var b *big.Int
			if len(parts) > 1 {
				b = cn(parts[1])
			}
			if under(a, 10000, false) && (len(parts) == 1 || under(b, 10000, false)) {
				update("episode", a, b)
				return nil
			} else if mode == "legacy" {
				return nil
			}
		}
		if cap := match(`([0-9一二三四五六七八九十百零]+)\s*集\s*全|[全共]\s*([0-9一二三四五六七八九十百零]+)\s*[集话話期幕]`); cap != nil {
			n := cn(firstCapture(cap.Groups()))
			if n != nil && m.Get("begin_episode") == nil {
				m.Set("total_episode", n)
				flag()
			}
			return nil
		}
	}
	if cap := match(`(?<!\d)\[?\s*(\d{1,4})\s*-\s*(\d{1,4})\s*(?:(?:Fin|End)(?![a-z0-9])|完结(?![\u4e00-\u9fff]))(?:\s*\](?!\d)|(?!\s*(?:\]\d|\d))\s*)`); cap != nil {
		a, b := arabic(cap.GroupByNumber(1).String()), arabic(cap.GroupByNumber(2).String())
		if a != nil && b != nil && a.Sign() > 0 && a.Cmp(b) <= 0 && under(b, 10000, false) && !(a.Cmp(big.NewInt(1900)) >= 0 && b.Cmp(big.NewInt(2155)) <= 0) && m.Get("begin_episode") == nil {
			m.Set("begin_episode", a)
			m.Set("end_episode", b)
			m.Set("total_episode", b)
			flag()
		}
	}
	return nil
}
