// Same actual installed cn2an algorithm as the existing checked helper, retaining
// Python arbitrary precision for the complete object's total fields.
package releasemeta

import (
	"light-media/internal/releasemeta/legacyanime"
	"math/big"
	"regexp"
	"strconv"
	"strings"
)

func fullLegacyChineseNumber(value string) (*big.Int, bool) {
	value = legacyanime.Strip(value)
	if err := legacyValidateChineseNumber(value); err != nil {
		return nil, false
	}
	if m := legacyAnimeFirstMatch(`^([0-9]+)([十百千万亿]?)$`, value, false); m != nil {
		digits := m.GroupByNumber(1).String()
		unit := m.GroupByNumber(2).String()
		var n *big.Int
		if unit == "" {
			f, e := strconv.ParseFloat(digits, 64)
			if e != nil {
				return nil, false
			}
			n, _ = new(big.Float).SetFloat64(f).Int(nil)
		} else {
			n, _ = new(big.Int).SetString(digits, 10)
			mult := map[string]int64{"十": 10, "百": 100, "千": 1000, "万": 10000, "亿": 100000000}
			n.Mul(n, big.NewInt(mult[unit]))
		}
		return n, true
	}
	value = strings.NewReplacer("零十", "零一十", "零百", "零一百").Replace(value)
	value = regexp.MustCompile(`[0-9]+`).ReplaceAllStringFunc(value, func(v string) string { v, _ = legacyArabicChinese(v); return v })
	value = legacyAnimeReplace(`(万)零(?=[零〇一壹幺二贰两三叁四肆五伍六陆七柒八捌九玖][千仟])`, value, `${1}`, false)
	value = legacyAnimeReplace(`([万亿])([零〇一壹幺二贰两三叁四肆五伍六陆七柒八捌九玖][十拾百佰])`, value, `${1}零${2}`, false)
	value = legacyAnimeReplace(`([千仟])([零〇一壹幺二贰两三叁四肆五伍六陆七柒八捌九玖]?[十拾])`, value, `${1}零${2}`, false)
	value = legacyAnimeReplace(`([百佰])([十拾])`, value, `${1}一${2}`, false)
	value = strings.NewReplacer("零十", "零一十", "零拾", "零一拾").Replace(value)
	nums := map[rune]int64{'零': 0, '一': 1, '二': 2, '三': 3, '四': 4, '五': 5, '六': 6, '七': 7, '八': 8, '九': 9}
	units := map[rune]int64{'十': 10, '百': 100, '千': 1000, '万': 10000, '亿': 100000000}
	if legacyCNAll.FindString(value) == value {
		out := new(big.Int)
		for _, r := range value {
			out.Mul(out, big.NewInt(10))
			out.Add(out, big.NewInt(nums[r]))
		}
		return out, true
	}
	if legacyCNNormal.FindString(value) != value {
		var last rune
		for _, r := range value {
			if _, ok := units[r]; ok {
				last = r
			}
		}
		back := map[int64]string{10: "十", 100: "百", 1000: "千", 10000: "万", 100000000: "亿"}
		value += back[units[last]/10]
	}
	out, unit, large := new(big.Int), int64(1), int64(1)
	runes := []rune(value)
	for i := len(runes) - 1; i >= 0; i-- {
		r := runes[i]
		if n, ok := nums[r]; ok {
			out.Add(out, new(big.Int).Mul(big.NewInt(n), big.NewInt(unit)))
		} else {
			unit = units[r]
			if unit%10000 == 0 {
				if unit > large {
					large = unit
				} else {
					large *= unit
					unit = large
				}
			}
			if unit < large {
				unit *= large
			}
			if i == 0 {
				out.Add(out, big.NewInt(unit))
			}
		}
	}
	return out, true
}
