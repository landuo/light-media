package autosignin

import (
	"context"
	"encoding/json"
	"fmt"
	sdk "light-media/builtinplugins/internal/sdk"
	"math"
	"strconv"
	"strings"
)

func (s sdkSiteTransport) sourcePage(ctx context.Context, request PageRequest) (string, error) {
	value, err := s.sourcePageValue(ctx, request)
	if err != nil {
		return "", err
	}
	if value == nil {
		return "", nil
	}
	if text, ok := value.(string); ok {
		return text, nil
	}
	// Existing string-only callers cannot silently erase a real source type.
	return "", fmt.Errorf("source Page value requires its source consumer")
}

func (s sdkSiteTransport) sourcePageValue(ctx context.Context, request PageRequest) (any, error) {
	if _, bound := s.site["_host_source_reference"].(sdk.SiteSourceReferenceV1); !bound {
		s.checkMetadata()
	}

	path, profile, err := s.sourcePath(request.URL)
	if err != nil {
		return "", err
	}
	rawTimeout, specialTimeout, err := sourcePageTimeoutJSON(request.Timeout)
	if err != nil {
		return "", err
	}
	ref := s.reference(ctx)
	result, err := sdk.RenderSite(ctx, s.backend.host, sdk.SiteRenderRequest{SourceReference: &ref, OriginProfile: profile, Path: path, SourceTimeout: rawTimeout, SourceTimeoutFloat: specialTimeout})
	s.backend.fatal(err)
	if result.SourceValueJSON != nil {
		return parseSourceHTTPJSON(string(result.SourceValueJSON))
	}
	if result.HTML == nil {
		return nil, nil
	}
	return *result.HTML, nil
}

// Retain Python binary64 nonfinite timeout values through the source-only
// finite enum, while ordinary values keep their original strict raw JSON.
func sourcePageTimeoutJSON(value any) (json.RawMessage, string, error) {
	special := func(f float64) string {
		if math.IsNaN(f) {
			return "nan"
		}
		if math.IsInf(f, 1) {
			return "infinity"
		}
		if math.IsInf(f, -1) {
			return "-infinity"
		}
		return ""
	}
	kind := ""
	switch v := value.(type) {
	case float64:
		kind = special(v)
	case json.Number:
		// Python json.loads exponent/fraction tokens are floats, but integer
		// tokens remain arbitrary precision and must not become float infinity.
		if strings.ContainsAny(v.String(), ".eE") {
			f, _ := strconv.ParseFloat(v.String(), 64)
			kind = special(f)
		}
	}
	if kind != "" {
		return nil, kind, nil
	}
	raw, e := sourceHTTPJSONBytes(value)
	return raw, "", e
}
