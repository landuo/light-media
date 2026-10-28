package catalog

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
)

// NewWithTransport keeps the fixed API origin, credential and redirect checks.
// A native owner may supply its transport; no public configuration can do so.
func NewWithTransport(credentials Credentials, transport http.RoundTripper) *Client {
	return newClient(credentials, transport, 0)
}

// SourceInformation reads the full response consumed by TmdbApi.get_info.
// It is intentionally separate from the UI's compact catalog projection.
func (c *Client) SourceInformation(ctx context.Context, kind string, id int64, fields []string) (json.RawMessage, Media, error) {
	if !kindOK(kind) || !validID(id) {
		return nil, Media{}, &Error{Code: "invalid_input"}
	}
	appendFields := "images,credits,alternative_titles,translations,release_dates,external_ids"
	if kind == "tv" {
		appendFields = "images,credits,alternative_titles,translations,content_ratings,external_ids,episode_groups"
	}
	var raw json.RawMessage
	if e := c.get(ctx, kind+"/"+strconv.FormatInt(id, 10), url.Values{"append_to_response": {appendFields}}, &raw); e != nil {
		return nil, Media{}, e
	}
	media, err := c.ProjectSourceInformation(kind, id, raw, fields)
	if err != nil {
		return nil, Media{}, err
	}
	return raw, media, nil
}

// ProjectSourceInformation reuses the normal native decoder for a full source
// response already returned by a trusted plugin. It performs no network call.
func (c *Client) ProjectSourceInformation(kind string, id int64, raw json.RawMessage, fields []string) (Media, error) {
	if !kindOK(kind) || !validID(id) {
		return Media{}, &Error{Code: "invalid_input"}
	}
	var wire wireMedia
	if decode(raw, &wire) != nil || wire.ID != id || wire.MediaType != "" && wire.MediaType != kind {
		return Media{}, &Error{Code: "invalid_response"}
	}
	// The original source append list has no recommendations. Do not apply
	// the UI detail endpoint's unrelated recommendation-presence contract.
	media, e := c.media(wire, kind, false)
	if e != nil {
		return Media{}, e
	}
	media.CategoryMetadataKnown = true
	media.IMDbID, media.Aliases, e = c.searchIdentities(wire, kind, media.Title, media.OriginalTitle)
	if e != nil {
		return Media{}, e
	}
	if wire.Credits != nil {
		media.Cast, e = c.cast(wire.Credits.Cast)
		if e != nil {
			return Media{}, e
		}
	}
	media.Seasons = append([]SeasonSummary{}, wire.Seasons...)
	media.CategoryFields, e = c.categoryFields(raw, fields)
	if e != nil {
		return Media{}, e
	}
	return media, nil
}
