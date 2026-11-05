package catalog

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
)

func (e *Explorer) SourceBangumiSearch(ctx context.Context, name string) (json.RawMessage, error) {
	var raw json.RawMessage
	err := e.requestProvider(ctx, "bangumi", http.MethodGet, "/search/subject/"+name, nil, nil, &raw, true)
	return raw, err
}

func (e *Explorer) SourceBangumiInformation(ctx context.Context, id, stamp string, characters bool) (json.RawMessage, error) {
	if !ValidExploreIdentity("bangumi", "tv", id) {
		return nil, &ExploreError{Source: "bangumi", Code: "invalid_input"}
	}
	path := "/v0/subjects/" + id
	if characters {
		path += "/characters"
	}
	var raw json.RawMessage
	err := e.requestProvider(ctx, "bangumi", http.MethodGet, path, url.Values{"_ts": {stamp}}, nil, &raw, true)
	return raw, err
}
