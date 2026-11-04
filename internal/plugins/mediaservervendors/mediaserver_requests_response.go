package mediaservervendors

import (
	"errors"
	"math"
	"math/big"
	"net/http"
	"strconv"
	"unicode/utf8"

	"light-media/internal/ocr"
	"light-media/internal/plugins"
	"light-media/internal/plugins/mediaserverruntime"
	"light-media/internal/sourcecharset"
)

// MediaServerRequestsApparentEncoding supplies the fixed real backend to the
// existing mandatory PlexSDKApparentEncoding function type and EJ/Z JSON path.
// No HTTP, global environment or codec default is changed here.
func MediaServerRequestsApparentEncoding(content []byte) (*string, error) {
	encoding, err := sourcecharset.DetectCharsetNormalizer352(content)
	if err != nil {
		return nil, err
	}
	if !encoding.Present {
		return nil, nil
	}
	name := encoding.Name
	return &name, nil
}

// MediaServerRequestsJSONValue reuses the existing Python JSON value decoder,
// not encoding/json's finite-number/escaped-surrogate coercion.
// Content is already transfer/content-decoded by the owning HTTP consumer.
func MediaServerRequestsJSONValue(content []byte, headers http.Header) (plugins.OCRValue, error) {
	text, err := ejResponseJSONText(content, headers.Get("Content-Type"))
	if err != nil {
		return plugins.OCRValue{}, err
	}
	value, err := ocr.ParseSourceJSON([]byte(text))
	if source, ok := err.(*ocr.SourceError); ok {
		return plugins.OCRValue{}, &mediaserverruntime.SourceError{Class: source.Class}
	}
	if err != nil {
		return plugins.OCRValue{}, err
	}
	return value, nil
}

// MediaServerRequestsJSON matches TrimeHTTPJSON's existing mandatory signature.
// The bridge preserves arbitrary integers, binary64 non-finite values and
// distinct lone surrogate strings/keys. It does not change Trime's downstream
// coercion or claim its entire API is accepted by this decoder attachment.
func MediaServerRequestsJSON(content []byte, headers http.Header) (any, error) {
	value, err := MediaServerRequestsJSONValue(content, headers)
	if err != nil {
		return nil, err
	}
	if err = value.Validate(); err != nil {
		return nil, err
	}
	return mediaServerRequestsValue(value)
}
func mediaServerRequestsValue(value plugins.OCRValue) (any, error) {
	switch value.Kind {
	case "null":
		return nil, nil
	case "bool":
		return value.Bool, nil
	case "int":
		n, ok := new(big.Int).SetString(value.Number, 10)
		if !ok {
			return nil, errors.New("source JSON integer bridge integrity")
		}
		return n, nil
	case "float":
		switch value.Number {
		case "nan":
			return math.NaN(), nil
		case "+inf":
			return math.Inf(1), nil
		case "-inf":
			return math.Inf(-1), nil
		}
		n, err := strconv.ParseFloat(value.Number, 64)
		if err != nil {
			return nil, err
		}
		return n, nil
	case "string":
		return mediaServerRequestsString(value.Points), nil
	case "list":
		out := make([]any, len(value.Items))
		for i, item := range value.Items {
			v, err := mediaServerRequestsValue(item)
			if err != nil {
				return nil, err
			}
			out[i] = v
		}
		return out, nil
	case "object":
		out := make(map[string]any, len(value.Entries))
		for _, entry := range value.Entries {
			v, err := mediaServerRequestsValue(entry.Value)
			if err != nil {
				return nil, err
			}
			out[mediaServerRequestsString(entry.Key)] = v
		}
		return out, nil
	default:
		return nil, errors.New("source JSON value bridge integrity")
	}
}

// WTF-8 is an internal string carrier only. It keeps lone Python codepoints
// distinct rather than silently replacing them with U+FFFD. Origin/header/URL
// admission remains with the concrete caller; this helper never emits HTTP.
func mediaServerRequestsString(points []rune) string {
	out := make([]byte, 0, len(points))
	for _, point := range points {
		if point >= 0xd800 && point <= 0xdfff {
			out = append(out, byte(0xe0|(point>>12)), byte(0x80|((point>>6)&0x3f)), byte(0x80|(point&0x3f)))
		} else {
			out = utf8.AppendRune(out, point)
		}
	}
	return string(out)
}
