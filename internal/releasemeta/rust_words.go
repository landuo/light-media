package releasemeta

import (
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/dlclark/regexp2/v2"
	"light-media/internal/pyregex"
)

// Rust 0.2.6 uses checked i64 arithmetic and its own replacement expansion;
// the legacy WordsMatcher deliberately remains a separate implementation.
var errRustFallback = errors.New("release_meta_rust_fallback")

func prepareRustWords(title string, list []string) (string, error) {
	deadline := time.Now().Add(2 * time.Second)
	for index, word := range list {
		if index%16 == 0 && time.Now().After(deadline) {
			return "", errors.New("release_meta_words_limit")
		}
		if word == "" || strings.HasPrefix(word, "#") {
			continue
		}
		switch {
		case strings.Contains(word, " => ") && strings.Contains(word, " && ") && strings.Contains(word, " >> ") && strings.Contains(word, " <> "):
			if m := rxFirst(`^\s*(.*?)\s*=>\s*(.*?)\s*&&\s*(.*?)\s*<>\s*(.*?)\s*>>\s*(.*?)\s*$`, word, false); m != nil {
				parameters := []string{}
				for _, group := range m.Groups()[1:] {
					parameters = append(parameters, strings.TrimSpace(group.String()))
				}
				var replaced bool
				title, replaced = replaceRustWord(title, parameters[0], parameters[1])
				if replaced {
					var fallback bool
					title, fallback = offsetRustWord(title, parameters[2], parameters[3], parameters[4])
					if fallback {
						return "", errRustFallback
					}
				}
			}
		case strings.Contains(word, " => "):
			parts := strings.Split(word, " => ")
			title, _ = replaceRustWord(title, parts[0], parts[1])
		case strings.Contains(word, " >> ") && strings.Contains(word, " <> "):
			parts := strings.Split(word, " <> ")
			offsets := strings.Split(parts[1], " >> ")
			if len(offsets) > 1 {
				var fallback bool
				title, fallback = offsetRustWord(title, parts[0], offsets[0], offsets[1])
				if fallback {
					return "", errRustFallback
				}
			}
		default:
			if strings.TrimSpace(word) != "" {
				title, _ = replaceRustWord(title, word, "")
			}
		}
		if len(title) > 64<<10 {
			return "", errors.New("release_meta_words_limit")
		}
	}
	return title, nil
}

func replaceRustWord(title, pattern, replacement string) (string, bool) {
	re, err := pyregex.Compile(pattern, false)
	if err != nil {
		return title, false
	}
	matched := false
	result, err := re.ReplaceFunc(title, func(match regexp2.Match) string {
		matched = true
		var out strings.Builder
		chars := []rune(replacement)
		for i := 0; i < len(chars); i++ {
			if chars[i] != '\\' || i+1 == len(chars) {
				out.WriteRune(chars[i])
				continue
			}
			i++
			if chars[i] >= '0' && chars[i] <= '9' {
				start := i
				for i+1 < len(chars) && chars[i+1] >= '0' && chars[i+1] <= '9' {
					i++
				}
				if n, err := strconv.Atoi(string(chars[start : i+1])); err == nil && n < len(match.Groups()) {
					out.WriteString(match.GroupByNumber(n).String())
				}
			} else {
				out.WriteRune(chars[i])
			}
		}
		return out.String()
	}, -1, -1)
	if err != nil {
		return title, false
	}
	return result, matched
}

func offsetRustWord(title, front, back, arithmetic string) (string, bool) {
	for _, condition := range []string{front, back} {
		if condition != "" {
			re, err := pyregex.Compile(condition, false)
			if err != nil {
				return title, false
			}
			if matched, err := re.MatchString(title); err != nil || !matched {
				return title, false
			}
		}
	}
	re, err := pyregex.Compile(`(?<=`+front+`.*?)[0-9一二三四五六七八九十]+(?=.*?`+back+`)`, false)
	if err != nil {
		return title, false
	}
	fallback := false
	result, err := re.ReplaceFunc(title, func(match regexp2.Match) string {
		original := match.String()
		number, valid := rustChineseNumber(original)
		if !valid {
			return original
		}
		parser := rustArithmetic{text: strings.ReplaceAll(arithmetic, "EP", " "+strconv.FormatInt(number, 10)+" ")}
		n, ok := parser.parse(0)
		parser.space()
		if !ok || parser.at != len(parser.text) {
			return original
		}
		if strings.ContainsAny(original, "一二三四五六七八九十") {
			if n < 0 {
				fallback = true // The source wrapper retries Python after this Rust panic.
				return original
			}
			return rustChineseFormat(n)
		}
		if strings.HasPrefix(original, "0") {
			if n < 0 {
				magnitude := new(big.Int).Neg(big.NewInt(n)).String()
				return "-" + strings.Repeat("0", max(0, len(original)-len(magnitude))) + magnitude
			}
			return fmt.Sprintf("%0*d", len(original), n)
		}
		return strconv.FormatInt(n, 10)
	}, -1, -1)
	if err != nil {
		return title, false
	}
	return result, fallback
}

type rustArithmetic struct {
	text      string
	at, depth int
}

func (p *rustArithmetic) space() {
	for p.at < len(p.text) && unicode.IsSpace(rune(p.text[p.at])) {
		p.at++
	}
}

func (p *rustArithmetic) parse(min int) (int64, bool) {
	p.depth++
	defer func() { p.depth-- }()
	if p.depth > 64 {
		return 0, false
	}
	p.space()
	if p.at == len(p.text) {
		return 0, false
	}
	var left int64
	if sign := p.text[p.at]; sign == '+' || sign == '-' {
		p.at++
		n, ok := p.parse(3)
		if !ok {
			return 0, false
		}
		if sign == '-' {
			value := new(big.Int).Neg(big.NewInt(n))
			returnValue := value.IsInt64()
			if !returnValue {
				return 0, false
			}
			n = value.Int64()
		}
		left = n
	} else if p.text[p.at] == '(' {
		p.at++
		n, ok := p.parse(0)
		p.space()
		if !ok || p.at == len(p.text) || p.text[p.at] != ')' {
			return 0, false
		}
		p.at++
		left = n
	} else {
		start := p.at
		for p.at < len(p.text) && p.text[p.at] >= '0' && p.text[p.at] <= '9' {
			p.at++
		}
		n, err := strconv.ParseInt(p.text[start:p.at], 10, 64)
		if err != nil {
			return 0, false
		}
		left = n
	}
	for {
		p.space()
		if p.at == len(p.text) {
			return left, true
		}
		op := p.text[p.at]
		priority := 1
		if strings.ContainsRune("*/%", rune(op)) {
			priority = 2
		} else if op != '+' && op != '-' {
			return left, true
		}
		if priority < min {
			return left, true
		}
		p.at++
		right, ok := p.parse(priority + 1)
		if !ok || (op == '/' || op == '%') && right == 0 {
			return 0, false
		}
		value, other := big.NewInt(left), big.NewInt(right)
		switch op {
		case '+':
			value.Add(value, other)
		case '-':
			value.Sub(value, other)
		case '*':
			value.Mul(value, other)
		case '/':
			value.Quo(value, other)
		case '%':
			value.Rem(value, other)
		}
		if !value.IsInt64() {
			return 0, false
		}
		left = value.Int64()
	}
}

func rustChineseNumber(text string) (int64, bool) {
	if n, err := strconv.ParseInt(strings.TrimSpace(text), 10, 64); err == nil {
		return n, true
	}
	value, current := int64(0), int64(0)
	for _, r := range strings.TrimSpace(text) {
		if at := strings.IndexRune("零一二三四五六七八九", r); at >= 0 {
			current = int64(at / 3)
		} else if r == '〇' {
			current = 0
		} else if r == '两' {
			current = 2
		} else if r == '十' || r == '百' {
			unit := int64(10)
			if r == '百' {
				unit = 100
			}
			if current == 0 {
				current = 1
			}
			value += current * unit
			current = 0
		} else {
			return 0, false
		}
	}
	return value + current, text != ""
}

func rustChineseFormat(n int64) string {
	digits := []rune("零一二三四五六七八九")
	switch {
	case n < 10:
		return string(digits[n])
	case n == 10:
		return "十"
	case n < 20:
		return "十" + string(digits[n%10])
	case n < 100:
		if n%10 == 0 {
			return string(digits[n/10]) + "十"
		}
		return string(digits[n/10]) + "十" + string(digits[n%10])
	default:
		return strconv.FormatInt(n, 10)
	}
}
