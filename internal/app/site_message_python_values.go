package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math"
	"math/big"
	"strconv"
	"strings"

	"light-media/internal/jinja/exec"
	"light-media/internal/notice"
)

// Stringify the original JSON, not a decoded Go map: Python dict str/repr keeps
// insertion order. Containers use repr for their members; a top-level string
// is the string itself. Duplicate object keys retain their first position and
// last value, as when Python's JSON decoder constructs a dict.
func siteMessagePythonString(raw json.RawMessage) (string, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	text, e := siteMessagePythonValue(dec, false)
	if e != nil {
		return "", e
	}
	if _, e = dec.Token(); e != io.EOF {
		return "", notice.SourceError{Class: "ValueError"}
	}
	return text, nil
}

func siteMessagePythonValue(dec *json.Decoder, repr bool) (string, error) {
	token, e := dec.Token()
	if e != nil {
		return "", notice.SourceError{Class: "ValueError"}
	}
	switch v := token.(type) {
	case nil:
		return "None", nil
	case bool:
		if v {
			return "True", nil
		}
		return "False", nil
	case string:
		if repr {
			return exec.PythonRepr(exec.AsValue(v)), nil
		}
		return v, nil
	case json.Number:
		if strings.ContainsAny(string(v), ".eE") {
			number, err := siteMessageJSONFloat(v)
			if err != nil {
				return "", err
			}
			if math.IsInf(number, 1) {
				return "inf", nil
			}
			if math.IsInf(number, -1) {
				return "-inf", nil
			}
			return exec.AsValue(number).String(), nil
		}
		number, ok := new(big.Int).SetString(string(v), 10)
		if !ok {
			return "", notice.SourceError{Class: "ValueError"}
		}
		return number.String(), nil
	case json.Delim:
		switch v {
		case '[':
			values := []string{}
			for dec.More() {
				value, err := siteMessagePythonValue(dec, true)
				if err != nil {
					return "", err
				}
				values = append(values, value)
			}
			end, err := dec.Token()
			if err != nil || end != json.Delim(']') {
				return "", notice.SourceError{Class: "ValueError"}
			}
			return "[" + strings.Join(values, ", ") + "]", nil
		case '{':
			keys := []string{}
			values := map[string]string{}
			for dec.More() {
				keyToken, err := dec.Token()
				key, ok := keyToken.(string)
				if err != nil || !ok {
					return "", notice.SourceError{Class: "ValueError"}
				}
				value, err := siteMessagePythonValue(dec, true)
				if err != nil {
					return "", err
				}
				if _, exists := values[key]; !exists {
					keys = append(keys, key)
				}
				values[key] = value
			}
			end, err := dec.Token()
			if err != nil || end != json.Delim('}') {
				return "", notice.SourceError{Class: "ValueError"}
			}
			pairs := make([]string, len(keys))
			for i, key := range keys {
				pairs[i] = exec.PythonRepr(exec.AsValue(key)) + ": " + values[key]
			}
			return "{" + strings.Join(pairs, ", ") + "}", nil
		}
	}
	return "", notice.SourceError{Class: "ValueError"}
}

// Python json.loads converts exponent/decimal lexemes to binary64. Overflow is
// infinity rather than a JSON decoder exception; a bad lexeme remains invalid.
func siteMessageJSONFloat(value json.Number) (float64, error) {
	f, err := strconv.ParseFloat(string(value), 64)
	if err != nil {
		var numberError *strconv.NumError
		if !errors.As(err, &numberError) || !errors.Is(numberError.Err, strconv.ErrRange) {
			return 0, notice.SourceError{Class: "ValueError"}
		}
	}
	return f, nil
}

// Match values supported by Python sqlite3's parameter binding. In particular,
// an integer outside signed 64-bit range raises before querying, and a decimal
// source is a REAL parameter, not the original numeric lexeme as TEXT.
func siteMessageSourceSQLValue(source any) (any, error) {
	switch value := source.(type) {
	case nil, string, bool, int64, float64:
		return value, nil
	case int:
		return int64(value), nil
	case json.Number:
		if strings.ContainsAny(string(value), ".eE") {
			return siteMessageJSONFloat(value)
		}
		number, ok := new(big.Int).SetString(string(value), 10)
		if !ok {
			return nil, notice.SourceError{Class: "ProgrammingError"}
		}
		if !number.IsInt64() {
			return nil, notice.SourceError{Class: "OverflowError"}
		}
		return number.Int64(), nil
	default:
		return nil, notice.SourceError{Class: "ProgrammingError"}
	}
}
