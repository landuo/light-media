package mediaservervendors

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"math/big"
	"strconv"
	"strings"

	"light-media/internal/plugins"
	"light-media/internal/releasemeta/legacyanime"
)

// Existing pinned Python Unicode/integer helpers, not strings.ToLower/TrimSpace
// or the different Pydantic model integer conversion.
func ugreenScanInteger(raw json.RawMessage) (int, bool, error) {
	raw = bytes.TrimSpace(raw)
	if !json.Valid(raw) {
		return 0, false, &EmbyJellyfinBoundaryError{Kind: "invalid owned Ugreen JSON"}
	}
	var number *big.Int
	switch raw[0] {
	case 't':
		number = big.NewInt(1)
	case 'f':
		number = big.NewInt(0)
	case 'n', '[', '{':
		return 0, false, nil
	case '"':
		points, err := plugins.DecodeSystemConfigJSONString(raw)
		if err != nil {
			return 0, false, err
		}
		value, err := legacyanime.Integer(string(points))
		if err != nil {
			var source legacyanime.SourceError
			if errors.As(err, &source) {
				return 0, false, nil
			}
			return 0, false, err
		}
		number = value
	default:
		if bytes.ContainsAny(raw, ".eE") {
			f, err := strconv.ParseFloat(string(raw), 64)
			if math.IsInf(f, 0) || math.IsNaN(f) {
				return 0, false, nil
			}
			if err != nil {
				return 0, false, err
			}
			number, _ = new(big.Float).SetFloat64(f).Int(nil)
		} else {
			var ok bool
			number, ok = new(big.Int).SetString(string(raw), 10)
			if !ok {
				return 0, false, &EmbyJellyfinBoundaryError{Kind: "invalid Ugreen integer"}
			}
		}
	}
	for _, allowed := range []int64{1, 2, 3} {
		if number.Cmp(big.NewInt(allowed)) == 0 {
			return int(allowed), true, nil
		}
	}
	return 0, false, nil
}
func ugreenScanType(mode, explicit json.RawMessage) (int, error) {
	for _, raw := range []json.RawMessage{explicit, mode} {
		n, ok, err := ugreenScanInteger(raw)
		if err != nil {
			return 0, err
		}
		if ok {
			return n, nil
		}
	}
	if !ejTruth(mode) {
		return 2, nil
	}
	var text string
	trim := bytes.TrimSpace(mode)
	if trim[0] == '"' {
		points, err := plugins.DecodeSystemConfigJSONString(trim)
		if err != nil {
			return 0, err
		}
		text = string(points)
	} else {
		var err error
		text, err = ejPythonText(mode, false)
		if err != nil {
			return 0, err
		}
	}
	text = legacyanime.LowerCase(legacyanime.Strip(text))
	switch text {
	case "new_and_modified", "new_modified", "add", "added", "new", "scan_new_modified":
		return 1, nil
	case "supplement_missing", "supplement", "additional", "missing", "scan_missing":
		return 2, nil
	case "full_override", "override", "cover", "replace", "scan_override":
		return 3, nil
	default:
		return 2, nil
	}
}
func ugreenVerify(raw json.RawMessage) (bool, error) {
	trim := bytes.TrimSpace(raw)
	if bytes.Equal(trim, []byte(`true`)) {
		return true, nil
	}
	if bytes.Equal(trim, []byte(`false`)) {
		return false, nil
	}
	if ejNull(trim) {
		return true, nil
	}
	var text string
	if trim[0] == '"' {
		points, err := plugins.DecodeSystemConfigJSONString(trim)
		if err != nil {
			return false, err
		}
		text = string(points)
	} else {
		var err error
		text, err = ejPythonText(trim, false)
		if err != nil {
			return false, err
		}
	}
	switch legacyanime.LowerCase(legacyanime.Strip(text)) {
	case "0", "false", "no", "off":
		return false, nil
	default:
		return true, nil
	}
}
func ugreenUTF8(raw json.RawMessage) (string, error) {
	trim := bytes.TrimSpace(raw)
	if len(trim) == 0 || trim[0] != '"' {
		return "", ejSource("AttributeError")
	}
	return ejText(trim)
}
func ugreenStandardize(raw json.RawMessage) (json.RawMessage, error) {
	text, err := ugreenUTF8(raw)
	if err != nil {
		return nil, err
	}
	if text != "" {
		if !strings.HasSuffix(text, "/") {
			text += "/"
		}
		if !strings.HasPrefix(text, "http://") && !strings.HasPrefix(text, "https://") {
			text = "http://" + text
		}
	}
	return plugins.EncodeSystemConfigJSONString([]rune(strings.TrimRight(text, "/")))
}
func (v *Ugreen) sessionKey() (string, error) {
	host, err := ugreenStandardize(v.host)
	if err != nil {
		return "", err
	}
	normalized, err := ugreenUTF8(host)
	if err != nil {
		return "", err
	}
	user, err := ugreenUTF8(v.username)
	if err != nil {
		return "", err
	}
	input := legacyanime.LowerCase(normalized) + "|" + legacyanime.LowerCase(legacyanime.Strip(user))
	sum := sha256.Sum256([]byte(input))
	return hex.EncodeToString(sum[:]), nil
}
func (v *Ugreen) passwordDigest() (string, error) {
	password, err := ugreenUTF8(v.password)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(password))
	return hex.EncodeToString(sum[:]), nil
}
func ugreenKey(text string) json.RawMessage {
	raw, _ := plugins.EncodeSystemConfigJSONString([]rune(text))
	return raw
}
func ugreenSet(fields *ejMembers, key string, value json.RawMessage) {
	canonical := ugreenKey(key)
	if _, ok := fields.values[string(canonical)]; !ok {
		fields.keys = append(fields.keys, canonical)
	}
	fields.values[string(canonical)] = ejCopy(value)
}
func ugreenDelete(fields *ejMembers, key string) bool {
	canonical := ugreenKey(key)
	if _, ok := fields.values[string(canonical)]; !ok {
		return false
	}
	delete(fields.values, string(canonical))
	for i, k := range fields.keys {
		if bytes.Equal(k, canonical) {
			fields.keys = append(fields.keys[:i], fields.keys[i+1:]...)
			break
		}
	}
	return true
}
func ugreenObjectRaw(fields ejMembers) json.RawMessage {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, key := range fields.keys {
		if i > 0 {
			b.WriteByte(',')
		}
		b.Write(key)
		b.WriteByte(':')
		b.Write(fields.values[string(key)])
	}
	b.WriteByte('}')
	return b.Bytes()
}
