package artifacts

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"light-media/internal/sourcejson"
)

// Python's standard JSON reader accepts these three constants, and rejects an
// integer token above its default 4300 digit budget. This adapter is exclusively
// for non-executable source display documents; native artifact decoding stays strict.
func pythonJSONPrepare(raw []byte) ([]byte, map[string]string, error) {
	prefix := fmt.Sprintf("__source_json_constant_%x_", sha256.Sum256(raw))
	for bytes.Contains(raw, []byte(prefix)) {
		prefix += "_"
	}
	constants := map[string]string{}
	var out bytes.Buffer
	for i := 0; i < len(raw); {
		start := i
		if raw[i] == '"' {
			i++
			for i < len(raw) {
				if raw[i] == '\\' {
					i += 2
					continue
				}
				if raw[i] == '"' {
					i++
					break
				}
				i++
			}
			if i > len(raw) {
				return nil, nil, errors.New("invalid Python JSON string")
			}
			out.Write(raw[start:i])
			continue
		}
		constant := ""
		for _, candidate := range []string{"NaN", "Infinity", "-Infinity"} {
			if bytes.HasPrefix(raw[i:], []byte(candidate)) {
				constant = candidate
				break
			}
		}
		if constant != "" {
			// Constants are legal values only. Quoting them for Go's decoder
			// must not repair an unquoted object property into a valid key.
			next := bytes.TrimLeft(raw[i+len(constant):], " \t\r\n")
			if len(next) > 0 && next[0] == ':' {
				return nil, nil, errors.New("unquoted Python JSON property")
			}
			marker, _ := json.Marshal(prefix + constant)
			constants[string(marker)] = constant
			out.Write(marker)
			i += len(constant)
			continue
		}
		if raw[i] == '-' || raw[i] >= '0' && raw[i] <= '9' {
			i++
			for i < len(raw) && strings.ContainsRune("0123456789.eE+-", rune(raw[i])) {
				i++
			}
			token := raw[start:i]
			if !bytes.ContainsAny(token, ".eE") && len(bytes.TrimPrefix(token, []byte("-"))) > 4300 {
				return nil, nil, errors.New("Python JSON integer exceeds 4300 digits")
			}
			out.Write(token)
			continue
		}
		out.WriteByte(raw[i])
		i++
	}
	return out.Bytes(), constants, nil
}

func pythonJSONRestore(raw []byte, constants map[string]string) json.RawMessage {
	for marker, value := range constants {
		raw = bytes.ReplaceAll(raw, []byte(marker), []byte(value))
	}
	return raw
}

func pythonSourceString(raw json.RawMessage) (string, error) {
	prepared, constants, err := pythonJSONPrepare(raw)
	if err != nil {
		return "", err
	}
	value, err := sourcejson.String(prepared)
	if err != nil {
		return "", err
	}
	for marker, token := range constants {
		var plain string
		_ = json.Unmarshal([]byte(marker), &plain)
		replacement := map[string]string{"NaN": "nan", "Infinity": "inf", "-Infinity": "-inf"}[token]
		if value == plain {
			value = replacement
		} else {
			value = strings.ReplaceAll(value, "'"+plain+"'", replacement)
		}
	}
	return value, nil
}
