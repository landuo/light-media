package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"unicode/utf8"
)

const SharedOCRGetCaptchaText = "ocr.get_captcha_text"

// OCRRequest mirrors the four optional source strings. Authority, service
// endpoint, proxy and TLS policy belong to the host, never this wire input.
type OCRRequest struct {
	ImageURL    *string `json:"image_url,omitempty"`
	ImageBase64 *string `json:"image_b64,omitempty"`
	Cookie      *string `json:"cookie,omitempty"`
	UserAgent   *string `json:"ua,omitempty"`
}
type OCRReply struct {
	Value       *OCRValue `json:"value,omitempty"`
	SourceError string    `json:"source_error,omitempty"`
}
type OCRSourceError struct{ Class string }

func (e *OCRSourceError) Error() string { return "ocr: " + e.Class }

type SharedOCR struct{ Host HostCaller }

func (s SharedOCR) GetCaptchaText(ctx context.Context, input OCRRequest) (OCRValue, error) {
	if s.Host == nil {
		return OCRValue{}, errors.New("OCR requires an active host caller")
	}
	if err := ctx.Err(); err != nil {
		return OCRValue{}, err
	}
	for _, value := range []*string{input.ImageURL, input.ImageBase64, input.Cookie, input.UserAgent} {
		if value != nil && !utf8.ValidString(*value) {
			return OCRValue{}, errors.New("OCR input must be valid Unicode text")
		}
	}
	raw, err := json.Marshal(input)
	if err != nil {
		return OCRValue{}, err
	}
	raw, err = s.Host(ctx, SharedOCRGetCaptchaText, raw)
	if err != nil {
		return OCRValue{}, err
	}
	var reply OCRReply
	if err = json.Unmarshal(raw, &reply); err != nil {
		return OCRValue{}, err
	}
	if reply.SourceError != "" {
		if reply.Value != nil {
			return OCRValue{}, errors.New("invalid OCR source error reply")
		}
		switch reply.SourceError {
		case "JSONDecodeError", "AttributeError", "ValueError", "UnicodeDecodeError", "UnicodeEncodeError", "LookupError", "RecursionError":
			return OCRValue{}, &OCRSourceError{Class: reply.SourceError}
		default:
			return OCRValue{}, errors.New("invalid OCR source error")
		}
	}
	if reply.Value == nil || reply.Value.Validate() != nil {
		return OCRValue{}, errors.New("invalid OCR host reply")
	}
	return *reply.Value, nil
}
