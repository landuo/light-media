package catalog

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
)

// SourceDoubanSearch returns the original Frodo search envelope, rather than
// the reduced Explore card model. Host recognition applies the fixed matcher.
func (e *Explorer) SourceDoubanSearch(ctx context.Context, name, stamp string) (json.RawMessage, error) {
	var raw json.RawMessage
	err := e.requestProvider(ctx, "douban", http.MethodGet, "/api/v2/search/weixin", url.Values{"q": {name}, "start": {"0"}, "count": {"20"}, "_ts": {stamp}}, nil, &raw, true)
	return raw, err
}

func (e *Explorer) SourceDoubanInformation(ctx context.Context, kind, id string, people bool) (json.RawMessage, error) {
	if !ValidExploreIdentity("douban", kind, id) {
		return nil, &ExploreError{Source: "douban", Code: "invalid_input"}
	}
	path := "/api/v2/" + kind + "/" + id
	if people {
		path += "/celebrities"
	}
	var raw json.RawMessage
	err := e.requestProvider(ctx, "douban", http.MethodGet, path, nil, nil, &raw, true)
	return raw, err
}
