package app

import (
	"encoding/json"
	"errors"
	"light-media/internal/notice"
	"light-media/internal/sourcejson"
	"math/big"
	"strconv"
	"strings"
)

func siteMessagePythonString(raw json.RawMessage) (string, error) { return sourcejson.String(raw) }

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
