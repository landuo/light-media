package plugins

import (
	"context"
	"encoding/json"
	"errors"
)

const SiteRenderCapability = "sites.render"

// SiteRenderRequest uses a configured site handle. Cookies, user agents, proxy
// credentials and browser executable paths remain within the host.
type SiteRenderRequest struct {
	ID             string `json:"id"`
	Path           string `json:"path,omitempty"`
	Headless       *bool  `json:"headless,omitempty"`
	TimeoutSeconds *int   `json:"timeout_seconds,omitempty"`
	// Source mode retains the original private row and raw helper timeout.
	// Ordinary native ID callers keep the existing integer timeout contract.
	SourceReference *SiteSourceReferenceV1 `json:"source_reference,omitempty"`
	OriginProfile   string                 `json:"origin_profile,omitempty"`
	SourceTimeout   json.RawMessage        `json:"source_timeout,omitempty"`
	// Only the three source float values that strict IPC JSON cannot carry.
	SourceTimeoutFloat string `json:"source_timeout_float,omitempty"`
}

// Nil HTML is the ordinary helper's None result, including browser failures.
// A nonnil empty string is preserved separately from None.
type SiteRenderResult struct {
	HTML *string `json:"html"`
	// Only SourceReference Page may return the original FS response JSON value.
	// Bytes keep the strict outer envelope, including nonfinite/source strings;
	// this is one bounded value, never the complete solver response/cookies.
	SourceValueJSON []byte `json:"source_value_json,omitempty"`
}

func RenderSite(ctx context.Context, host HostCaller, request SiteRenderRequest) (SiteRenderResult, error) {
	if host == nil {
		return SiteRenderResult{}, errors.New("site rendering requires an active host caller")
	}
	raw, err := json.Marshal(request)
	if err != nil {
		return SiteRenderResult{}, err
	}
	raw, err = host(ctx, SiteRenderCapability, raw)
	if err != nil {
		return SiteRenderResult{}, err
	}
	var result SiteRenderResult
	err = json.Unmarshal(raw, &result)
	if err == nil && result.SourceValueJSON != nil {
		if request.SourceReference == nil || len(result.SourceValueJSON) > MaxPayloadBytes {
			return SiteRenderResult{}, errors.New("invalid source Page value boundary")
		}
	}
	return result, err
}
