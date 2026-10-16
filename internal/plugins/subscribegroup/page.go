package subscribegroup

import (
	"errors"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
)

// Decode the source's content.encode('utf-8').decode('unicode_escape'),
// including its Latin-1 interpretation of unescaped UTF-8 history bytes.
// This is display text only, never HTML or a script/expression interpreter.
func unescapeHistory(value string) (string, error) {
	result := []rune{}
	for i := 0; i < len(value); {
		c := value[i]
		i++
		if c != '\\' {
			result = append(result, rune(c))
			continue
		}
		if i == len(value) {
			return "", errors.New("UnicodeDecodeError")
		}
		c = value[i]
		i++
		switch c {
		case '\n':
		case '\\', '\'', '"':
			result = append(result, rune(c))
		case 'a':
			result = append(result, '\a')
		case 'b':
			result = append(result, '\b')
		case 'f':
			result = append(result, '\f')
		case 'n':
			result = append(result, '\n')
		case 'r':
			result = append(result, '\r')
		case 't':
			result = append(result, '\t')
		case 'v':
			result = append(result, '\v')
		case 'x', 'u', 'U':
			count := 2
			if c == 'u' {
				count = 4
			}
			if c == 'U' {
				count = 8
			}
			if i+count > len(value) {
				return "", errors.New("UnicodeDecodeError")
			}
			n, err := strconv.ParseUint(value[i:i+count], 16, 32)
			if err != nil || n > 0x10ffff {
				return "", errors.New("UnicodeDecodeError")
			}
			i += count
			result = append(result, rune(n))
		default:
			if c >= '0' && c <= '7' {
				start := i - 1
				for i < len(value) && i < start+3 && value[i] >= '0' && value[i] <= '7' {
					i++
				}
				n, _ := strconv.ParseUint(value[start:i], 8, 16)
				result = append(result, rune(n))
			} else {
				result = append(result, '\\', rune(c))
			}
		}
	}
	// JSON transports a UTF-16 pair as the same Unicode scalar in Go/JS.
	var b strings.Builder
	for i := 0; i < len(result); i++ {
		r := result[i]
		if r >= 0xd800 && r <= 0xdbff && i+1 < len(result) && result[i+1] >= 0xdc00 && result[i+1] <= 0xdfff {
			r = utf16.DecodeRune(r, result[i+1])
			i++
		}
		b.WriteRune(r)
	}
	return b.String(), nil
}
func Page(history []History) ([]map[string]any, error) {
	if len(history) == 0 {
		return []map[string]any{{"component": "div", "text": "暂无数据", "props": map[string]any{"class": "text-center"}}}, nil
	}
	ordered := append([]History{}, history...)
	// Source sorting is stable; equal timestamps keep append order.
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].Time > ordered[j].Time })
	headers := []map[string]any{}
	for _, label := range []string{"执行时间", "订阅名称", "更新类型", "更新内容"} {
		headers = append(headers, map[string]any{"component": "th", "props": map[string]any{"class": "text-start ps-4"}, "text": label})
	}
	rows := []map[string]any{}
	for _, entry := range ordered {
		content, err := unescapeHistory(entry.Content)
		if err != nil {
			return nil, err
		}
		cells := []map[string]any{{"component": "td", "props": map[string]any{"class": "whitespace-nowrap break-keep text-high-emphasis"}, "text": entry.Time}, {"component": "td", "text": entry.Name}, {"component": "td", "text": entry.Type}, {"component": "td", "text": content}}
		rows = append(rows, map[string]any{"component": "tr", "props": map[string]any{"class": "text-sm"}, "content": cells})
	}
	table := map[string]any{"component": "VTable", "props": map[string]any{"hover": true}, "content": []map[string]any{{"component": "thead", "content": headers}, {"component": "tbody", "content": rows}}}
	return []map[string]any{{"component": "VRow", "content": []map[string]any{{"component": "VCol", "props": map[string]any{"cols": 12}, "content": []map[string]any{table}}}}}, nil
}
