package catalog

import (
	"context"
	"encoding/json"
	"strconv"
)

// SourcePersonInformation retains the ordinary provider's whole person record
// for the plugin Chain's MediaPerson contract. It shares the existing fixed
// provider origin, credentials, rate limits and redirect policy.
func (c *Client) SourcePersonInformation(ctx context.Context, id int64) (json.RawMessage, error) {
	if !validID(id) {
		return nil, &Error{Code: "invalid_input"}
	}
	var raw json.RawMessage
	err := c.get(ctx, "person/"+strconv.FormatInt(id, 10), nil, &raw)
	return raw, err
}
