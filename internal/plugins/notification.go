package plugins

import (
	"context"
	"encoding/json"
	"errors"
)

const PostMessageCapability = "message.post"

// NotificationDispatch describes routing/attempts, never receipt by a person.
// Like MoviePilot, channel exceptions do not undo already saved history/events.
type NotificationDispatch struct {
	Queued    int      `json:"queued"`
	Attempted int      `json:"attempted"`
	Failed    int      `json:"failed"`
	Errors    []string `json:"errors"`
}

// PostMessage accepts the original JSON-shaped Notification fields. A concurrent
// worker is required because routing can synchronously invoke plugin modules.
func (b Base) PostMessage(ctx context.Context, fields map[string]any) (NotificationDispatch, error) {
	var result NotificationDispatch
	if b.Host == nil {
		return result, errors.New("post_message requires an active host caller")
	}
	raw, err := json.Marshal(fields)
	if err != nil {
		return result, err
	}
	raw, err = b.Host(ctx, PostMessageCapability, raw)
	if err != nil {
		return result, err
	}
	err = json.Unmarshal(raw, &result)
	return result, err
}
