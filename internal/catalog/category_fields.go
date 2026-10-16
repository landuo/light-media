package catalog

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

// CategoryField keeps equality fingerprints rather than entire credits,
// collection or season objects. Requested-but-absent fields remain present in
// the map with Present=false so a cached partial detail is not fetched forever.
type CategoryField struct {
	Present  bool     `json:"present"`
	Hashes   []string `json:"hashes,omitempty"`
	Integers []string `json:"integers,omitempty"`
}

// NormalizeCategoryValue matches Python str.upper(), including expansions such
// as ß -> SS. Whitespace is significant in MoviePilot's comma-separated values.
func NormalizeCategoryValue(text string) string {
	for i := 0; i < len(text); i++ {
		if text[i] >= 0x80 {
			return cases.Upper(language.Und).String(text)
		}
	}
	return strings.ToUpper(text)
}

func CategoryValueHash(text string) string {
	digest := sha256.Sum256([]byte(NormalizeCategoryValue(text)))
	return hex.EncodeToString(digest[:])
}

func validCategoryFieldName(name string) bool {
	if name == "" || len(name) > 128 {
		return false
	}
	for _, char := range name {
		if unicode.IsControl(char) {
			return false
		}
	}
	return true
}

func (c *Client) categoryFields(body []byte, names []string) (map[string]CategoryField, error) {
	if len(names) > 128 {
		return nil, &Error{Code: "invalid_input"}
	}
	var values map[string]json.RawMessage
	if decode(body, &values) != nil || values == nil {
		return nil, &Error{Code: "invalid_response"}
	}
	result := make(map[string]CategoryField, len(names))
	for _, name := range names {
		if !validCategoryFieldName(name) {
			return nil, &Error{Code: "invalid_input"}
		}
		raw, exists := values[name]
		if !exists {
			result[name] = CategoryField{}
			continue
		}
		field, err := c.categoryField(raw)
		if err != nil {
			return nil, &Error{Code: "invalid_response"}
		}
		result[name] = field
	}
	return result, nil
}

func (c *Client) categoryField(raw json.RawMessage) (CategoryField, error) {
	field := CategoryField{}
	if len(bytes.TrimSpace(raw)) == 0 {
		return field, fmt.Errorf("empty category JSON")
	}
	seen := map[string]bool{}
	add := func(text string) {
		for _, secret := range []string{c.credentials.APIKey, c.credentials.AccessToken} {
			if secret != "" {
				text = strings.ReplaceAll(text, secret, "[redacted]")
			}
		}
		hash := CategoryValueHash(text)
		if seen[hash] {
			return
		}
		seen[hash] = true
		field.Hashes = append(field.Hashes, hash)
		// Numeric ranges expand to canonical decimal strings in the original.
		// A string "001" must not match a range that expands to "1".
		if integer, ok := new(big.Int).SetString(text, 10); ok && integer.Sign() >= 0 && integer.String() == text {
			field.Integers = append(field.Integers, text)
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if len(raw) > 0 && bytes.TrimSpace(raw)[0] == '[' {
		if _, err := decoder.Token(); err != nil {
			return field, err
		}
		for decoder.More() {
			value, err := categoryPythonValue(decoder, 0)
			if err != nil {
				return field, err
			}
			field.Present = true
			add(value.text)
			if len(field.Hashes) > 4096 {
				return field, fmt.Errorf("category values limit")
			}
		}
		if _, err := decoder.Token(); err != nil {
			return field, err
		}
	} else {
		value, err := categoryPythonValue(decoder, 0)
		if err != nil {
			return field, err
		}
		field.Present = value.truthy
		if field.Present {
			add(value.text)
		}
	}
	if _, err := decoder.Token(); err != io.EOF {
		return field, fmt.Errorf("trailing category JSON")
	}
	return field, nil
}

type categoryPython struct {
	text, repr string
	truthy     bool
}

// This formats JSON using the original helper's str()/repr() semantics; it does
// not evaluate Python or execute code. Object order follows the JSON source.
func categoryPythonValue(decoder *json.Decoder, depth int) (categoryPython, error) {
	if depth > 32 {
		return categoryPython{}, fmt.Errorf("category JSON nesting limit")
	}
	token, err := decoder.Token()
	if err != nil {
		return categoryPython{}, err
	}
	value := categoryPython{}
	switch item := token.(type) {
	case nil:
		value.text, value.repr = "None", "None"
	case bool:
		value.text = "False"
		if item {
			value.text = "True"
		}
		value.repr, value.truthy = value.text, item
	case string:
		value.text, value.repr, value.truthy = item, pythonCategoryString(item), item != ""
	case json.Number:
		value.text = item.String()
		if len(value.text) > 4300 {
			return value, fmt.Errorf("category number limit")
		}
		if strings.ContainsAny(value.text, ".eE") {
			float, err := strconv.ParseFloat(value.text, 64)
			if err != nil {
				return value, err
			}
			exponential := strconv.FormatFloat(float, 'e', -1, 64)
			_, exponent, _ := strings.Cut(exponential, "e")
			power, _ := strconv.Atoi(exponent)
			value.text = exponential
			if power >= -4 && power < 16 {
				value.text = strconv.FormatFloat(float, 'f', -1, 64)
			}
			if !strings.ContainsAny(value.text, ".e") {
				value.text += ".0"
			}
			value.truthy = float != 0
		} else {
			integer, ok := new(big.Int).SetString(value.text, 10)
			if !ok {
				return value, fmt.Errorf("invalid category number")
			}
			value.text, value.truthy = integer.String(), integer.Sign() != 0
		}
		value.repr = value.text
	case json.Delim:
		if item != '[' && item != '{' {
			return value, fmt.Errorf("invalid category delimiter")
		}
		var parts []string
		for decoder.More() {
			prefix := ""
			if item == '{' {
				key, err := decoder.Token()
				name, ok := key.(string)
				if err != nil || !ok {
					return value, fmt.Errorf("invalid category property")
				}
				prefix = pythonCategoryString(name) + ": "
			}
			child, err := categoryPythonValue(decoder, depth+1)
			if err != nil {
				return value, err
			}
			parts = append(parts, prefix+child.repr)
		}
		end, err := decoder.Token()
		if err != nil || (item == '[' && end != json.Delim(']')) || (item == '{' && end != json.Delim('}')) {
			return value, fmt.Errorf("invalid category container")
		}
		left, right := "[", "]"
		if item == '{' {
			left, right = "{", "}"
		}
		value.text = left + strings.Join(parts, ", ") + right
		value.repr, value.truthy = value.text, len(parts) > 0
	default:
		return value, fmt.Errorf("invalid category JSON token")
	}
	return value, nil
}

func pythonCategoryString(text string) string {
	quote := '\''
	if strings.ContainsRune(text, '\'') && !strings.ContainsRune(text, '"') {
		quote = '"'
	}
	var out strings.Builder
	out.WriteRune(quote)
	for _, char := range text {
		switch {
		case char == quote || char == '\\':
			out.WriteByte('\\')
			out.WriteRune(char)
		case char == '\n':
			out.WriteString(`\n`)
		case char == '\r':
			out.WriteString(`\r`)
		case char == '\t':
			out.WriteString(`\t`)
		case !unicode.IsPrint(char) || (unicode.Is(unicode.Zs, char) && char != ' '):
			if char <= 0xff {
				fmt.Fprintf(&out, `\x%02x`, char)
			} else if char <= 0xffff {
				fmt.Fprintf(&out, `\u%04x`, char)
			} else {
				fmt.Fprintf(&out, `\U%08x`, char)
			}
		default:
			out.WriteRune(char)
		}
	}
	out.WriteRune(quote)
	return out.String()
}
