package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"regexp"
	"strconv"
	"strings"

	"light-media/internal/notice"
)

var originalRatingDecimal = regexp.MustCompile(`^[+-]?(?:[0-9]+(?:\.[0-9]*)?|\.[0-9]+)(?:[eE][+-]?[0-9]+)?$`)

// PluginRatingRequest uses Pydantic's ordinary float input, including numeric
// strings and booleans. Request bounds/multiple_of remain a separate check.
func originalRatingFloat(raw json.RawMessage) (float64, error) {
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if decoder.Decode(&value) != nil {
		return 0, errors.New("invalid rating float")
	}
	text := ""
	switch input := value.(type) {
	case bool:
		if input {
			return 1, nil
		}
		return 0, nil
	case json.Number:
		text = input.String()
	case string:
		text = strings.TrimSpace(input)
		if strings.HasPrefix(text, "_") || strings.HasSuffix(text, "_") || strings.Contains(text, "__") {
			return 0, errors.New("invalid rating float")
		}
		text = strings.ReplaceAll(text, "_", "")
	default:
		return 0, errors.New("invalid rating float")
	}
	if !originalRatingDecimal.MatchString(text) {
		return 0, errors.New("invalid rating float")
	}
	parsed, err := strconv.ParseFloat(text, 64)
	if math.IsNaN(parsed) || math.IsInf(parsed, 0) || err != nil && parsed != 0 {
		return 0, errors.New("invalid rating float")
	}
	return parsed, nil
}

func originalRatingInteger(raw json.RawMessage) (json.Number, error) {
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if decoder.Decode(&value) != nil {
		return "", errors.New("invalid rating integer")
	}
	parsed, ok := notice.CoerceSourceInteger(value)
	if !ok {
		return "", errors.New("invalid rating integer")
	}
	return parsed, nil
}
