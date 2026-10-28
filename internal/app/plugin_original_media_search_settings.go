package app

import (
	"math"
	"math/big"
	"regexp"
	"strconv"
	"strings"

	"light-media/internal/jinja/exec"
	"light-media/internal/releasemeta/legacyanime"
)

// Ordinary search settings only. These are trusted process inputs, not plugin
// configuration or an additional settings UI, fixed for the App lifetime.
type MediaSearchConfiguration struct {
	MaxNames        *big.Int
	MultipleNames   bool
	KeywordInterval float64
}

var mediaSearchFloatPattern = regexp.MustCompile(`^[+-]?(?:[0-9](?:_?[0-9])*(?:\.(?:[0-9](?:_?[0-9])*)?)?|\.[0-9](?:_?[0-9])*)(?:[eE][+-]?[0-9](?:_?[0-9])*)?$`)

func originalMediaSearchFloat(raw string) (float64, bool) {
	var normalized strings.Builder
	for _, r := range legacyanime.Strip(raw) {
		if r > 127 {
			if n, err := legacyanime.Integer(string(r)); err == nil && n.Sign() >= 0 && n.Cmp(big.NewInt(9)) <= 0 {
				normalized.WriteByte('0' + byte(n.Int64()))
				continue
			}
		}
		normalized.WriteRune(r)
	}
	text := normalized.String()
	unsigned := strings.ToLower(text)
	if strings.HasPrefix(unsigned, "+") || strings.HasPrefix(unsigned, "-") {
		unsigned = unsigned[1:]
	}
	if unsigned == "nan" {
		return math.NaN(), true
	}
	if unsigned == "inf" || unsigned == "infinity" {
		sign := 1
		if strings.HasPrefix(text, "-") {
			sign = -1
		}
		return math.Inf(sign), true
	}
	if !mediaSearchFloatPattern.MatchString(text) {
		return 0, false
	}
	value, err := strconv.ParseFloat(strings.ReplaceAll(text, "_", ""), 64)
	return value, err == nil || math.IsInf(value, 0) || value == 0 // Python float accepts overflow infinity and underflow zero.
}

func MediaSearchEnvironment(lookup func(string) (string, bool)) (*MediaSearchConfiguration, bool) {
	out := &MediaSearchConfiguration{MaxNames: big.NewInt(3), KeywordInterval: 2}
	normalized := false
	if raw, present := lookup("MAX_SEARCH_NAME_LIMIT"); present {
		if value, err := legacyanime.Integer(legacyanime.Strip(raw)); err == nil {
			out.MaxNames = value
			normalized = normalized || value.String() != raw
		} else {
			normalized = true
		}
	}
	if raw, present := lookup("SEARCH_MULTIPLE_NAME"); present {
		switch strings.ToLower(legacyanime.Strip(raw)) {
		case "true", "yes", "1", "on":
			out.MultipleNames = true
		case "false", "no", "0", "off":
		default:
			normalized = true
		}
		normalized = normalized || strconv.FormatBool(out.MultipleNames) != strings.ToLower(raw)
	}
	if raw, present := lookup("SEARCH_KEYWORD_MIN_INTERVAL"); present {
		if value, ok := originalMediaSearchFloat(raw); ok {
			out.KeywordInterval = value
			text := exec.AsValue(value).String()
			if math.IsNaN(value) {
				text = "nan"
			} else if math.IsInf(value, 1) {
				text = "inf"
			} else if math.IsInf(value, -1) {
				text = "-inf"
			}
			normalized = normalized || text != raw
		} else {
			normalized = true
		}
	}
	return out, normalized
}

func cloneMediaSearchConfiguration(value *MediaSearchConfiguration) *MediaSearchConfiguration {
	if value == nil {
		return nil
	}
	out := *value
	if out.MaxNames == nil {
		out.MaxNames = big.NewInt(3)
	} else {
		out.MaxNames = new(big.Int).Set(out.MaxNames)
	}
	return &out
}
