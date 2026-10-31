// Package ocr implements the ordinary non-LLM helper, not an OCR engine.
package ocr

import (
	"context"
	"encoding/base64"
	"light-media/internal/plugins"
	"light-media/internal/plugins/iyuuautoseed/compat/encodingdetector"
	"light-media/internal/requestsjson"
	"strings"
	"unicode"
	"unicode/utf8"
)

const DefaultHost = "https://movie-pilot.org"

type Input struct{ ImageURL, ImageBase64, Cookie, UserAgent *string }
type Request struct {
	Method, URL       string
	Cookie, UserAgent *string
	Base64Image       string
}
type Response struct {
	Status      int
	Content     []byte
	ContentType string
}
type Transport func(context.Context, Request) (*Response, error)
type Client struct {
	Endpoint  string
	Transport Transport
}
type SourceError struct{ Class string }

func (e *SourceError) Error() string { return "ocr: " + e.Class }
func optional(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
func sourceSpace(r rune) bool { return unicode.IsSpace(r) || r >= '\x1c' && r <= '\x1f' }

// Only ASCII letters can match these ASCII sentinels. Full Unicode simple
// lower would map dotted capital I to i unlike Python's i+combining-dot.
func lowerSentinelASCII(value string) string {
	bytes := []byte(value)
	for i, c := range bytes {
		if c >= 'A' && c <= 'Z' {
			bytes[i] = c + ('a' - 'A')
		}
	}
	return string(bytes)
}
func ExtractDataURL(value string) string {
	value = strings.TrimFunc(value, sourceSpace)
	if !strings.HasPrefix(lowerSentinelASCII(value), "data:image/") {
		return ""
	}
	metadata, data, ok := strings.Cut(value, ",")
	if !ok || !strings.Contains(lowerSentinelASCII(metadata), ";base64") {
		return ""
	}
	return strings.TrimFunc(data, sourceSpace)
}
func NormalizeBase64(value string) string {
	if data := ExtractDataURL(value); data != "" {
		value = data
	}
	value = strings.Map(func(r rune) rune {
		if sourceSpace(r) {
			return -1
		}
		return r
	}, value)
	if remainder := utf8.RuneCountInString(value) % 4; remainder != 0 {
		value += strings.Repeat("=", 4-remainder)
	}
	return value
}
func empty() plugins.OCRValue { return plugins.OCRValue{Kind: "string", Points: []rune{}} }
func (c Client) GetCaptchaText(ctx context.Context, in Input) (plugins.OCRValue, error) {
	if err := ctx.Err(); err != nil {
		return plugins.OCRValue{}, err
	}
	encoded := NormalizeBase64(optional(in.ImageBase64))
	if address := optional(in.ImageURL); address != "" {
		if data := ExtractDataURL(address); data != "" {
			encoded = NormalizeBase64(data)
		} else {
			response, err := c.Transport(ctx, Request{Method: "GET", URL: address, Cookie: in.Cookie, UserAgent: in.UserAgent})
			if err != nil {
				return plugins.OCRValue{}, err
			}
			if response != nil {
				if len(response.Content) == 0 {
					return empty(), nil
				}
				encoded = base64.StdEncoding.EncodeToString(response.Content)
			}
		}
	}
	if encoded == "" {
		return empty(), nil
	}
	response, err := c.Transport(ctx, Request{Method: "POST", URL: c.Endpoint, Base64Image: encoded})
	if err != nil {
		return plugins.OCRValue{}, err
	}
	if response == nil || (response.Status >= 400 && response.Status < 600) {
		return empty(), nil
	}
	return result(response.Content, response.ContentType)
}
func result(data []byte, contentType string) (plugins.OCRValue, error) {
	text, err := requestsjson.ResponseJSONText(data, contentType)
	if err != nil {
		if class := encodingdetector.ErrorClass(err); class != "" {
			return plugins.OCRValue{}, &SourceError{Class: class}
		}
		return plugins.OCRValue{}, err
	}
	value, err := parseSourceJSON([]byte(text))
	if err != nil {
		return plugins.OCRValue{}, err
	}
	if value.Kind != "object" {
		return plugins.OCRValue{}, &SourceError{Class: "AttributeError"}
	}
	for _, entry := range value.Entries {
		if string(entry.Key) == "result" {
			if entry.Value.Truthy() {
				return entry.Value, nil
			}
			break
		}
	}
	return empty(), nil
}
