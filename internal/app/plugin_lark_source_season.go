package app

import (
	"errors"
	"math/big"
	"regexp"
	"strings"
)

// The only an2cn call in the original media list receives a Python integer and
// mode="low". This is that cn2an0.5.24 integer branch, including its 16-digit
// source limit; unrelated decimal, currency and preprocessing APIs are unused.
func larkSourceSeasonName(n *big.Int) (string, error) {
	if n == nil {
		return "", nil
	}
	s := new(big.Int).Abs(n).String()
	if len(s) > 16 {
		return "", errors.New("cn2an_integer_range")
	}
	digits := []rune("零一二三四五六七八九")
	units := []string{"", "十", "百", "千", "万", "十", "百", "千", "亿", "十", "百", "千", "万", "十", "百", "千"}
	out := ""
	for i, d := range s {
		value := int(d - '0')
		position := len(s) - i - 1
		if value != 0 {
			out += string(digits[value]) + units[position]
		} else {
			if position%4 == 0 {
				out += string(digits[0]) + units[position]
			}
			if i > 0 && !strings.HasSuffix(out, "零") {
				out += "零"
			}
		}
	}
	for _, pair := range [][2]string{{"零零", "零"}, {"零万", "万"}, {"零亿", "亿"}, {"亿万", "亿"}} {
		out = strings.ReplaceAll(out, pair[0], pair[1])
	}
	out = strings.Trim(out, "零")
	out = regexp.MustCompile(`([万亿])零([一二三四五六七八九壹贰叁肆伍陆柒捌玖][千仟])`).ReplaceAllString(out, `${1}${2}`)
	if strings.HasPrefix(out, "一十") {
		out = strings.TrimPrefix(out, "一")
	}
	if out == "" {
		out = "零"
	}
	if n.Sign() < 0 {
		out = "负" + out
	}
	return out, nil
}
