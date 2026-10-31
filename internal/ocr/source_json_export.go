package ocr

import "light-media/internal/plugins"

// ParseSourceJSON decodes already-decoded JSON text using the existing source
// JSON value model. Callers remain responsible for the input layer's character
// decoding (strict descriptor UTF-8 differs from requests.Response.json).
func ParseSourceJSON(text []byte) (plugins.OCRValue, error) {
	return parseSourceJSON(text)
}
