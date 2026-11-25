package autosignin

import (
	"errors"
	"light-media/builtinplugins/plugins/mpserverstatus/encodingdetector"
	"strings"
)

func responseText(data []byte, contentType string) (string, error) {
	if len(data) == 0 {
		return "", nil
	}
	// requests.utils._parse_content_type_header splits on semicolons and strips
	// quoted parameter values; only parameter names are lowercased.
	parts := strings.Split(contentType, ";")
	encoding := ""
	hasCharset := false
	for _, p := range parts[1:] {
		key, value, found := strings.Cut(p, "=")
		if found && strings.ToLower(strings.Trim(strings.TrimFunc(key, sourcePythonSpace), "\"' ")) == "charset" {
			hasCharset = true
			encoding = strings.Trim(strings.TrimFunc(value, sourcePythonSpace), "\"' ")
		}
	}
	if !hasCharset {
		if strings.Contains(parts[0], "text") {
			encoding = "ISO-8859-1"
		} else if strings.Contains(parts[0], "application/json") {
			encoding = "utf-8"
		} else {
			var e error
			encoding, _, e = encodingdetector.Detect(data)
			if e != nil {
				return "", e
			}
		}
	}
	if encoding == "" {
		encoding = "utf-8"
	}
	text, e := encodingdetector.Decode(data, encoding, true)
	if e != nil {
		class := encodingdetector.ErrorClass(e)
		if class == "LookupError" || class == "TypeError" {
			return encodingdetector.Decode(data, "utf-8", true)
		}
	}
	if e != nil {
		var codec *encodingdetector.CodecError
		if errors.As(e, &codec) {
			reason := codec.Reason
			if codec.Encoding == "idna" && codec.Class == "UnicodeError" {
				reason = "Unsupported error handling replace"
			}
			return text, sourceTextCodecError{cause: e, message: reason}
		}
	}
	return text, e
}

type sourceTextCodecError struct {
	cause   error
	message string
}

func (e sourceTextCodecError) Error() string { return e.message }
func (e sourceTextCodecError) Unwrap() error { return e.cause }

// Base.get_page_source uses chardet and a strict first decode, then logs its
// exception and freshly reads Response.text as fallback. Host log errors stay
// terminal and cannot become a source decoding/network failure.
func decodeSourcePage(content []byte, fallback func() string, log func(string, string) error) (string, error) {
	encoding, _, err := encodingdetector.Detect(content)
	if err == nil {
		var text string
		text, err = encodingdetector.Decode(content, encoding, false)
		if err == nil {
			return text, nil
		}
	}
	if log != nil {
		if failure := log("ERROR", "chardet解码失败："+sourcePageDecodeError(content, encoding, err)); failure != nil {
			panic(&boundaryFailure{failure})
		}
	}
	return fallback(), nil
}
