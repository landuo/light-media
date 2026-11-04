package mediaservervendors

import (
	"errors"
	"net/http"
	"strings"

	"light-media/internal/plugins/iyuuautoseed/compat/encodingdetector"
	"light-media/internal/releasemeta/legacyanime"
)

// PlexSDKApparentEncoding is the actual Requests-selected detector dependency.
// In the fixed recovered environment that is charset_normalizer3.5.2, NOT the
// existing chardet-shaped encodingdetector.Detect. The latter is never called.
// nil/empty encoding intentionally converge only at Response.text's final
// `encoding or "utf-8"`, not at a public apparent-encoding DTO.
type PlexSDKApparentEncoding func([]byte) (*string, error)

// NewPlexSDKTextDecoder supplies real header/codec behavior while requiring the
// separately implemented detector. Omitting it is not a successful UTF8 stub.
// This decoder consumes response.content (already transfer/content-decoded),
// never assumes compressed wire bytes equal Response.content.
func NewPlexSDKTextDecoder(apparent PlexSDKApparentEncoding) (PlexSDKTextDecoder, error) {
	if apparent == nil {
		return nil, errors.New("Plex SDK text requires the actual selected apparent-encoding backend")
	}
	return func(raw []byte, headers http.Header) (string, error) {
		encoding := plexSDKHeaderEncoding(headers.Get("Content-Type"))
		if len(raw) == 0 {
			return "", nil
		}
		if encoding == nil {
			var err error
			encoding, err = apparent(raw)
			if err != nil {
				return "", err
			}
		}
		name := "utf-8"
		if encoding != nil && *encoding != "" {
			name = *encoding
		}
		value, err := encodingdetector.Decode(raw, name, true)
		if err != nil {
			class := encodingdetector.ErrorClass(err)
			if class == "LookupError" || class == "TypeError" {
				return encodingdetector.Decode(raw, "utf-8", true)
			}
		}
		return value, err
	}, nil
}
func plexSDKHeaderEncoding(contentType string) *string {
	if contentType == "" {
		return nil
	}
	tokens := strings.Split(contentType, ";")
	kind := legacyanime.Strip(tokens[0])
	params := map[string]string{}
	for _, part := range tokens[1:] {
		part = legacyanime.Strip(part)
		if part == "" {
			continue
		}
		index := strings.IndexByte(part, '=')
		if index < 0 {
			continue
		}
		key := legacyanime.LowerCase(strings.Trim(part[:index], "\"' "))
		value := strings.Trim(part[index+1:], "\"' ")
		params[key] = value // original dictionary keeps the last duplicate value
	}
	if value, present := params["charset"]; present {
		value = strings.Trim(value, "'\"")
		return &value
	}
	if strings.Contains(kind, "text") {
		value := "ISO-8859-1"
		return &value
	}
	if strings.Contains(kind, "application/json") {
		value := "utf-8"
		return &value
	}
	return nil
}
