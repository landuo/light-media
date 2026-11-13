package catalog

import (
	"context"
	"encoding/json"
	"strconv"
)

// SourceEpisodeRows uses the existing authenticated TMDB transport, retaining
// the source list order and group episode-number reassignment before its DTO.
func (c *Client) SourceEpisodeRows(ctx context.Context, id int64, season int, group string) (json.RawMessage, error) {
	info, err := c.SourceSeasonInformation(ctx, id, season, group)
	if err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(info, &fields) != nil {
		return nil, &Error{Code: "invalid_response"}
	}
	var value any
	if len(fields["episodes"]) > 0 {
		if json.Unmarshal(fields["episodes"], &value) != nil {
			return nil, &Error{Code: "invalid_response"}
		}
	}
	empty := value == nil
	switch v := value.(type) {
	case bool:
		empty = !v
	case float64:
		empty = v == 0
	case string:
		empty = v == ""
	case []any:
		empty = len(v) == 0
	case map[string]any:
		empty = len(v) == 0
	}
	if empty {
		return json.RawMessage(`[]`), nil
	}
	return fields["episodes"], nil
}

func (c *Client) SourceSeasonInformation(ctx context.Context, id int64, season int, group string) (json.RawMessage, error) {
	if !validID(id) || season < 0 || season > 1000 {
		return nil, &Error{Code: "invalid_input"}
	}
	if group == "" {
		var info json.RawMessage
		if err := c.get(ctx, "tv/"+strconv.FormatInt(id, 10)+"/season/"+strconv.Itoa(season), nil, &info); err != nil {
			return nil, err
		}
		return info, nil
	}
	// The HTTP client receives a path segment, never a browser-supplied URL.
	for _, r := range group {
		if r == '/' || r == '\\' || r == '?' || r == '#' || r < ' ' {
			return nil, &Error{Code: "invalid_input"}
		}
	}
	var body struct {
		Groups []struct {
			Order    int
			Episodes []map[string]json.RawMessage
		}
	}
	if err := c.get(ctx, "tv/episode_group/"+group, nil, &body); err != nil {
		return nil, err
	}
	for _, item := range body.Groups {
		if item.Order == season {
			for index, episode := range item.Episodes {
				episode["episode_number"] = json.RawMessage(strconv.Itoa(index + 1))
			}
			return json.Marshal(map[string]any{"episodes": item.Episodes, "order": item.Order})
		}
	}
	return json.RawMessage(`{}`), nil
}

// SourceSeasonSummaries reads the TV header without appended credits, images,
// translations or recommendations. The season picker needs only this array.
func (c *Client) SourceSeasonSummaries(ctx context.Context, id int64) (json.RawMessage, error) {
	if !validID(id) {
		return nil, &Error{Code: "invalid_input"}
	}
	var response struct {
		ID      int64           `json:"id"`
		Seasons json.RawMessage `json:"seasons"`
	}
	if err := c.get(ctx, "tv/"+strconv.FormatInt(id, 10), nil, &response); err != nil {
		return nil, err
	}
	if response.ID != id {
		return nil, &Error{Code: "invalid_response"}
	}
	if len(response.Seasons) == 0 || string(response.Seasons) == "null" {
		return json.RawMessage(`[]`), nil
	}
	var seasons []json.RawMessage
	if json.Unmarshal(response.Seasons, &seasons) != nil {
		return nil, &Error{Code: "invalid_response"}
	}
	return response.Seasons, nil
}
