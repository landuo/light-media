package subscribegroup

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"math/big"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

// RawPage retains get_page's old single-object history, nullable display fields
// and failure behavior. It must not turn malformed legacy data into an empty
// page or rewrite it into the typed History format used for new appends.
func RawPage(raw json.RawMessage) ([]map[string]any, error) {
	if len(raw) == 0 {
		raw = json.RawMessage(`null`)
	}
	if !json.Valid(raw) {
		return nil, errors.New("JSONDecodeError")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if decoder.Decode(&value) != nil {
		return nil, errors.New("JSONDecodeError")
	}
	if !historyTruthy(value) {
		return Page(nil)
	}
	values, isList := value.([]any)
	if !isList {
		values = []any{value}
	}
	entries := make([]map[string]any, len(values))
	for i, value := range values {
		entry, ok := value.(map[string]any)
		if !ok {
			return nil, errors.New("AttributeError")
		}
		entries[i] = entry
	}
	key := func(entry map[string]any) any {
		if value := entry["time"]; historyTruthy(value) {
			return value
		}
		return json.Number("0")
	}
	var sortErr error
	sort.SliceStable(entries, func(i, j int) bool {
		cmp, err := compareHistoryValues(key(entries[i]), key(entries[j]))
		if err != nil {
			sortErr = err
			return false
		}
		return cmp > 0
	})
	if sortErr != nil {
		return nil, sortErr
	}
	headers := []map[string]any{}
	for _, label := range []string{"执行时间", "订阅名称", "更新类型", "更新内容"} {
		headers = append(headers, map[string]any{"component": "th", "props": map[string]any{"class": "text-start ps-4"}, "text": label})
	}
	rows := []map[string]any{}
	for _, entry := range entries {
		content := ""
		if value := entry["content"]; historyTruthy(value) {
			text, ok := value.(string)
			if !ok {
				return nil, errors.New("AttributeError")
			}
			var err error
			content, err = unescapeHistory(text)
			if err != nil {
				return nil, err
			}
		}
		cells := []map[string]any{{"component": "td", "props": map[string]any{"class": "whitespace-nowrap break-keep text-high-emphasis"}, "text": entry["time"]}, {"component": "td", "text": entry["name"]}, {"component": "td", "text": entry["type"]}, {"component": "td", "text": content}}
		rows = append(rows, map[string]any{"component": "tr", "props": map[string]any{"class": "text-sm"}, "content": cells})
	}
	table := map[string]any{"component": "VTable", "props": map[string]any{"hover": true}, "content": []map[string]any{{"component": "thead", "content": headers}, {"component": "tbody", "content": rows}}}
	return []map[string]any{{"component": "VRow", "content": []map[string]any{{"component": "VCol", "props": map[string]any{"cols": 12}, "content": []map[string]any{table}}}}}, nil
}

func historyTruthy(value any) bool {
	switch v := value.(type) {
	case nil:
		return false
	case bool:
		return v
	case string:
		return v != ""
	case json.Number:
		n, ok := historyNumber(v)
		return !ok || n.infinity != 0 || n.rational.Sign() != 0
	case map[string]any:
		return len(v) != 0
	case []any:
		return len(v) != 0
	}
	return true
}

type historyNumeric struct {
	rational *big.Rat
	infinity int
}

func historyNumber(value any) (historyNumeric, bool) {
	switch n := value.(type) {
	case bool:
		if n {
			return historyNumeric{rational: big.NewRat(1, 1)}, true
		}
		return historyNumeric{rational: big.NewRat(0, 1)}, true
	case json.Number:
		if strings.ContainsAny(string(n), ".eE") {
			// Python's JSON decoder uses float for decimal/exponent values.
			// ParseFloat bounds huge exponents without allocating huge powers.
			f, err := strconv.ParseFloat(string(n), 64)
			if err != nil && !errors.Is(err, strconv.ErrRange) {
				return historyNumeric{}, false
			}
			if math.IsInf(f, 1) {
				return historyNumeric{infinity: 1}, true
			}
			if math.IsInf(f, -1) {
				return historyNumeric{infinity: -1}, true
			}
			return historyNumeric{rational: new(big.Rat).SetFloat64(f)}, true
		}
		integer, ok := new(big.Int).SetString(string(n), 10)
		if !ok {
			return historyNumeric{}, false
		}
		return historyNumeric{rational: new(big.Rat).SetInt(integer)}, true
	}
	return historyNumeric{}, false
}

func compareHistoryValues(a, b any) (int, error) {
	if an, ok := historyNumber(a); ok {
		if bn, ok := historyNumber(b); ok {
			if an.infinity != 0 || bn.infinity != 0 {
				return an.infinity - bn.infinity, nil
			}
			return an.rational.Cmp(bn.rational), nil
		}
	}
	if as, ok := a.(string); ok {
		if bs, ok := b.(string); ok {
			return bytes.Compare([]byte(as), []byte(bs)), nil
		}
	}
	if al, ok := a.([]any); ok {
		if bl, ok := b.([]any); ok {
			for i := 0; i < len(al) && i < len(bl); i++ {
				if reflect.DeepEqual(al[i], bl[i]) {
					continue
				}
				cmp, err := compareHistoryValues(al[i], bl[i])
				if err != nil || cmp != 0 {
					return cmp, err
				}
			}
			return len(al) - len(bl), nil
		}
	}
	return 0, errors.New("TypeError")
}
