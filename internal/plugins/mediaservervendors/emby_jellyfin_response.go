package mediaservervendors

import (
	"bytes"
	"encoding/json"
	"strings"

	encodingdetector "light-media/internal/plugins/iyuuautoseed/compat/encodingdetector"
	"light-media/internal/sourcecharset"
)

// A present empty charset is not the same as absent encoding for Response.text.
func ejResponseEncoding(contentType string) (string, bool) {
	if contentType == "" {
		return "", false
	}
	parts := strings.Split(contentType, ";")
	kind := strings.TrimSpace(parts[0])
	parameters := map[string]string{}
	for _, part := range parts[1:] {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		i := strings.IndexByte(part, '=')
		if i < 0 {
			continue
		}
		key := strings.ToLower(strings.Trim(part[:i], "\"' "))
		value := strings.Trim(part[i+1:], "\"' ")
		parameters[key] = value
	}
	if charset, ok := parameters["charset"]; ok {
		return strings.Trim(charset, "'\""), true
	}
	if strings.Contains(kind, "text") {
		return "ISO-8859-1", true
	}
	if strings.Contains(kind, "application/json") {
		return "utf-8", true
	}
	return "", false
}
func ejGuessJSONUTF(data []byte) string {
	sample := data[:4]
	switch {
	case bytes.Equal(sample, []byte{0, 0, 254, 255}) || bytes.Equal(sample, []byte{255, 254, 0, 0}):
		return "utf-32"
	case bytes.HasPrefix(sample, []byte{239, 187, 191}):
		return "utf-8-sig"
	case bytes.HasPrefix(sample, []byte{254, 255}) || bytes.HasPrefix(sample, []byte{255, 254}):
		return "utf-16"
	}
	switch bytes.Count(sample, []byte{0}) {
	case 0:
		return "utf-8"
	case 2:
		if sample[0] == 0 && sample[2] == 0 {
			return "utf-16-be"
		}
		if sample[1] == 0 && sample[3] == 0 {
			return "utf-16-le"
		}
	case 3:
		if sample[0] == 0 && sample[1] == 0 && sample[2] == 0 {
			return "utf-32-be"
		}
		if sample[1] == 0 && sample[2] == 0 && sample[3] == 0 {
			return "utf-32-le"
		}
	}
	return ""
}
func ejJSONDecoded(text string) (json.RawMessage, error) {
	raw := []byte(text)
	if !json.Valid(raw) {
		return nil, ejSource("JSONDecodeError")
	}
	return append(json.RawMessage(nil), bytes.TrimSpace(raw)...), nil
}

// Character decoding is shared with the mandatory Trime value decoder. The
// existing EJ/Z RawMessage value boundary remains separate and unchanged.
func ejResponseJSON(body []byte, contentType string) (json.RawMessage, error) {
	text, err := ejResponseJSONText(body, contentType)
	if err != nil {
		return nil, err
	}
	return ejJSONDecoded(text)
}
func ejResponseJSONText(body []byte, contentType string) (string, error) {
	encoding, present := ejResponseEncoding(contentType)
	if encoding == "" && len(body) > 3 {
		if guessed := ejGuessJSONUTF(body); guessed != "" {
			text, err := encodingdetector.Decode(body, guessed, false)
			if err == nil {
				return text, nil
			}
			if class := encodingdetector.ErrorClass(err); class != "UnicodeDecodeError" {
				if class != "" {
					return "", ejSource(class)
				}
				return "", &EmbyJellyfinBoundaryError{Kind: "response codec integrity", cause: err}
			}
		}
	}
	if len(body) == 0 {
		return "", nil
	}
	if !present {
		apparent, err := MediaServerRequestsApparentEncoding(body)
		if err != nil {
			if ordinary, ok := err.(*sourcecharset.CNSourceError); ok {
				return "", ejSource(ordinary.Class)
			}
			return "", err
		}
		// Only a genuine detector None/empty result reaches Response.text's source
		// `encoding or "utf-8"`; an error never becomes an invented UTF8 fallback.
		if apparent != nil {
			encoding = *apparent
		}
	}
	if encoding == "" {
		encoding = "utf-8"
	}
	text, err := encodingdetector.Decode(body, encoding, true)
	if class := encodingdetector.ErrorClass(err); err != nil && (class == "LookupError" || class == "TypeError") {
		text, err = encodingdetector.Decode(body, "utf-8", true)
	}
	if err != nil {
		if class := encodingdetector.ErrorClass(err); class != "" {
			return "", ejSource(class)
		}
		return "", &EmbyJellyfinBoundaryError{Kind: "response codec integrity", cause: err}
	}
	return text, nil
}
