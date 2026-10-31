package requestsjson

import (
	"bytes"
	"light-media/internal/plugins/iyuuautoseed/compat/encodingdetector"
	"strings"
)

func headerEncoding(contentType string) string {
	parts := strings.Split(contentType, ";")
	for _, p := range parts[1:] {
		kv := strings.SplitN(p, "=", 2)
		if len(kv) == 2 && strings.ToLower(strings.Trim(kv[0], " \t\r\n'\"")) == "charset" {
			return strings.Trim(strings.TrimSpace(kv[1]), "'\"")
		}
	}
	ct := strings.TrimSpace(parts[0])
	if strings.Contains(ct, "text") {
		return "ISO-8859-1"
	}
	if strings.Contains(ct, "application/json") {
		return "utf-8"
	}
	return ""
}
func decodeResponseBytes(raw []byte, enc string) (string, error) {
	if enc == "" {
		var err error
		enc, _, err = encodingdetector.Detect(raw)
		if err != nil {
			return "", err
		}
	}
	text, err := encodingdetector.Decode(raw, enc, true)
	if err != nil {
		if class := encodingdetector.ErrorClass(err); class == "LookupError" || class == "TypeError" {
			return encodingdetector.Decode(raw, "utf-8", true)
		}
		return "", err
	}
	return text, nil
}
func responseText(raw []byte, contentType string) (string, error) {
	return decodeResponseBytes(raw, headerEncoding(contentType))
}
func ResponseJSONText(raw []byte, contentType string) (string, error) {
	enc := headerEncoding(contentType)
	if enc == "" && len(raw) > 3 {
		guess := ""
		switch {
		case bytes.HasPrefix(raw, []byte{0, 0, 254, 255}) || bytes.HasPrefix(raw, []byte{255, 254, 0, 0}):
			guess = "utf-32"
		case bytes.HasPrefix(raw, []byte{239, 187, 191}):
			guess = "utf-8-sig"
		case bytes.HasPrefix(raw, []byte{254, 255}) || bytes.HasPrefix(raw, []byte{255, 254}):
			guess = "utf-16"
		default:
			n := bytes.Count(raw[:4], []byte{0})
			if n == 0 {
				guess = "utf-8"
			} else if n == 2 {
				if raw[0] == 0 && raw[2] == 0 {
					guess = "utf-16-be"
				} else if raw[1] == 0 && raw[3] == 0 {
					guess = "utf-16-le"
				}
			} else if n == 3 {
				if raw[0] == 0 && raw[1] == 0 && raw[2] == 0 {
					guess = "utf-32-be"
				} else if raw[1] == 0 && raw[2] == 0 && raw[3] == 0 {
					guess = "utf-32-le"
				}
			}
		}
		if guess != "" {
			if text, err := encodingdetector.Decode(raw, guess, false); err == nil {
				return text, nil
			} else if encodingdetector.ErrorClass(err) != "UnicodeDecodeError" {
				return "", err
			}
		}
	}
	return responseText(raw, contentType)
}
