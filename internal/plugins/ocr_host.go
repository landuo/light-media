package plugins

import (
	"context"
	"encoding/json"
	"errors"
)

const CaptchaOCRTextCapability = "ocr.captcha.text"

// ImageURL uses the authenticated configured site when it is an HTTP URL or
// relative image path. Data image URLs and ImageBase64 require no site secret.
// SiteID is the existing Sites.Read item's string ID, not a second identity.
type CaptchaOCRRequestV1 struct {
	SourceReference *SiteSourceReferenceV1 `json:"source_reference,omitempty"`
	SiteID          string                 `json:"site_id,omitempty"`
	ImageURL        string                 `json:"image_url,omitempty"`
	ImageBase64     string                 `json:"image_base64,omitempty"`
}

// The fixed helper returns response.json().get("result") or "". Its annotated
// str return does not enforce a runtime type; keep a truthy JSON result intact.
type CaptchaOCRResultV1 struct {
	SchemaVersion  int                         `json:"schema_version"`
	Result         json.RawMessage             `json:"result"`
	SourceResponse *CaptchaOCRSourceResponseV1 `json:"source_response,omitempty"`
}

type CaptchaOCR struct{ Host HostCaller }

func (o CaptchaOCR) Text(ctx context.Context, in CaptchaOCRRequestV1) (CaptchaOCRResultV1, error) {
	var out CaptchaOCRResultV1
	if o.Host == nil {
		return out, errors.New("captcha OCR requires an active host caller")
	}
	raw, err := json.Marshal(in)
	if err != nil {
		return out, err
	}
	value, err := o.Host(ctx, CaptchaOCRTextCapability, raw)
	if err != nil {
		return out, err
	}
	if json.Unmarshal(value, &out) != nil || out.SchemaVersion != 1 || !json.Valid(out.Result) {
		return CaptchaOCRResultV1{}, errors.New("invalid captcha OCR response")
	}
	return out, nil
}

// SourceResponse remains private to the compiled source consumer. The ordinary
// OCR API still returns its original truthy JSON result. Raw service bytes let
// source JSON/type errors take the normal source exception path, not HOST_DENIED.
type CaptchaOCRSourceResponseV1 struct {
	Content     []byte `json:"content"`
	ContentType string `json:"content_type,omitempty"`
}
