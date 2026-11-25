package iyuuautoseed

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"errors"
	"io"
	"strings"

	"github.com/andybalholm/brotli"
	"light-media/builtinplugins/internal/encodingdetector"
)

var errAPIResponseLimit = errors.New("IYUU response exceeds limit")

func readAPIContent(body io.Reader, encoding string) ([]byte, error) {
	raw, err := io.ReadAll(io.LimitReader(body, maxAPIResponse+1))
	if len(raw) > maxAPIResponse {
		return nil, errAPIResponseLimit
	}
	if err != nil {
		return nil, err
	}
	encs := strings.Split(encoding, ",")
	for i := len(encs) - 1; i >= 0; i-- {
		var reader io.Reader
		var closer io.Closer
		switch strings.ToLower(strings.TrimSpace(encs[i])) {
		case "", "identity":
			continue
		case "gzip", "x-gzip":
			r, e := gzip.NewReader(bytes.NewReader(raw))
			if e != nil {
				return nil, e
			}
			reader = r
			closer = r
		case "deflate":
			r, e := zlib.NewReader(bytes.NewReader(raw))
			if e != nil {
				r = flate.NewReader(bytes.NewReader(raw))
			}
			reader = r
			closer = r
		case "br":
			reader = brotli.NewReader(bytes.NewReader(raw))
		default:
			return nil, errors.New("unsupported source content encoding")
		}
		decoded, e := io.ReadAll(io.LimitReader(reader, maxAPIResponse+1))
		if closer != nil {
			closer.Close()
		}
		if len(decoded) > maxAPIResponse {
			return nil, errAPIResponseLimit
		}
		if e != nil {
			return nil, e
		}
		raw = decoded
	}
	return raw, nil
}
func apiHeaderEncoding(contentType string) string {
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
func decodeAPIResponseBytes(raw []byte, enc string) (string, error) {
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
func apiResponseText(raw []byte, contentType string) (string, error) {
	return decodeAPIResponseBytes(raw, apiHeaderEncoding(contentType))
}
func apiResponseJSONText(raw []byte, contentType string) (string, error) {
	enc := apiHeaderEncoding(contentType)
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
	return apiResponseText(raw, contentType)
}
