package catalog

import (
	"context"
	"net/url"
	"strings"
)

// The source episode-group request shares TMDb's response snapshot and rate
// handling. Native recognition numbers the returned groups after this read.
func (c *Client) SourceRecognitionGroupResponse(ctx context.Context, group, locale string) (SourceRecognitionResponse, error) {
	if group == "" || strings.ContainsAny(group, "/\\?#") {
		return SourceRecognitionResponse{}, &Error{Code: "invalid_input"}
	}
	for _, r := range group {
		if r < ' ' {
			return SourceRecognitionResponse{}, &Error{Code: "invalid_input"}
		}
	}
	return c.sourceRecognitionResponse(ctx, "tv/episode_group/"+group, url.Values{"language": {locale}})
}
