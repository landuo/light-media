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
}

// Nil HTML is the ordinary helper's None result, including browser failures.
// A nonnil empty string is preserved separately from None.
type SiteRenderResult struct {
	HTML *string `json:"html"`
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
	return result, err
}
