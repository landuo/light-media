package autosignin

import (
	"context"
	sdk "light-media/builtinplugins/internal/sdk"
)

type sdkTjuptPublic struct{ backend sdkBackend }

func (s sdkTjuptPublic) get(ctx context.Context, address, profile string) (*PageResponse, error) {
	result, err := (sdk.Network{Host: s.backend.host}).Get(ctx, sdk.NetworkGetRequestV1{URL: address, Profile: profile})
	s.backend.fatal(err)
	if result.Response == nil {
		return nil, nil
	}
	content := append([]byte(nil), result.Response.Content...)
	r := &PageResponse{Status: result.Response.StatusCode, Content: content}
	// Preserve the captured Content-Type before applying Requests text decoding.
	r.getText = func() (string, error) { return responseText(content, result.Response.ContentType) }
	return r, nil
}
func (s sdkTjuptPublic) Suggest(ctx context.Context, label string) (*PageResponse, error) {
	// Source f-string is passed directly to RequestUtils, including delimiters;
	// preserve it instead of QueryEscape, which would change '#' and '&'.
	return s.get(ctx, "https://movie.douban.com/j/subject_suggest?q="+label, sdk.NetworkGetSourcePublicProfile)
}
func (s sdkTjuptPublic) Image(ctx context.Context, address string) (*PageResponse, error) {
	return s.get(ctx, address, sdk.NetworkGetSourceDoubanImageProfile)
}
