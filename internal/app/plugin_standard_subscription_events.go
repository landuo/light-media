package app

import (
	"context"
	"encoding/json"
	"light-media/internal/plugins"
	"slices"
)

// Pinned SubscribeModifiedEventData._diff_fields compares the key union,
// treats absent keys as None, uses Python JSON value equality (True == 1),
// and returns sorted names. Snapshot decoding here does not coerce integers.
func standardSubscriptionModifiedPayload(id string, before, after json.RawMessage, scene string) (json.RawMessage, error) {
	var oldFields, newFields map[string]json.RawMessage
	if json.Unmarshal(before, &oldFields) != nil || oldFields == nil || json.Unmarshal(after, &newFields) != nil || newFields == nil {
		return nil, bad("PLUGIN_EVENT_INVALID", "订阅事件快照必须为对象")
	}
	keys := make(map[string]bool, len(oldFields)+len(newFields))
	for key := range oldFields {
		keys[key] = true
	}
	for key := range newFields {
		keys[key] = true
	}
	fields := []string{}
	for key := range keys {
		oldValue, newValue := oldFields[key], newFields[key]
		if len(oldValue) == 0 {
			oldValue = json.RawMessage(`null`)
		}
		if len(newValue) == 0 {
			newValue = json.RawMessage(`null`)
		}
		if !originalBaseJSONEqual(oldValue, newValue) {
			fields = append(fields, key)
		}
	}
	slices.Sort(fields)
	return json.Marshal(struct {
		ID     string          `json:"subscribe_id"`
		Before json.RawMessage `json:"old_subscribe_info"`
		After  json.RawMessage `json:"subscribe_info"`
		Scene  string          `json:"scene"`
		Fields []string        `json:"fields"`
	}{id, before, after, scene, fields})
}

type standardSubscriptionSceneKey struct{}

func standardSubscriptionEventScene(ctx context.Context) string {
	if ctx.Value(standardSubscriptionSceneKey{}) == "status" {
		return "status"
	}
	return "update"
}

// Bind the existing pause/resume operation to the source status-event scene.
// Only state/revision are accepted as changes; the existing host save logic
// continues to validate activation and preserve all unrelated editor fields.
func (a *App) saveSubscriptionStatus(ctx context.Context, key, state string, revision int) (Subscription, error) {
	if state != "active" && state != "paused" {
		return Subscription{}, bad("INVALID_SUBSCRIPTION_STATE", "订阅状态须为active或paused")
	}
	var current Subscription
	if err := a.Store.Get(ctx, "subscription", key, &current); err != nil {
		return current, err
	}
	var input SubscriptionRequest
	raw, err := json.Marshal(current)
	if err != nil {
		return current, err
	}
	if err = json.Unmarshal(raw, &input); err != nil {
		return current, err
	}
	input.State = state
	if revision > 0 {
		input.Revision = revision
	}
	return a.saveSubscription(context.WithValue(ctx, standardSubscriptionSceneKey{}, "status"), key, input)
}

// The source sends this synchronous chain before history/delete. Only its
// documented output fields determine the completion veto; JSON snapshots
// preserve full source inputs across the Go process boundary.
func (a *App) sendStandardSubscriptionCompletionCheck(ctx context.Context, input subscriptionCompletionCheckEvent) (subscriptionCompletionCheckResult, error) {
	out := subscriptionCompletionCheckResult{Event: "subscribe.completion.check", Source: "未知来源"}
	raw, err := json.Marshal(map[string]any{"subscribe": input.Subscribe, "mediainfo": input.MediaInfo, "meta": input.Meta, "cancel": false, "source": "未知来源", "reason": ""})
	if err != nil {
		return out, err
	}
	event := plugins.MutableEvent{Type: out.Event, Data: raw}
	if !a.sendOriginalChainEvent(ctx, &event) || !originalBaseJSONTruthy(event.Data) {
		return out, nil
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(event.Data, &fields) != nil || fields == nil {
		return out, bad("PLUGIN_EVENT_INVALID", "订阅完成判定事件不是对象")
	}
	out.Cancel = originalBaseJSONTruthy(fields["cancel"])
	if value, ok := fields["source"]; ok {
		if err = json.Unmarshal(value, &out.Source); err != nil {
			return out, bad("PLUGIN_EVENT_INVALID", "订阅完成判定来源无效")
		}
	}
	if value, ok := fields["reason"]; ok {
		if err = json.Unmarshal(value, &out.Reason); err != nil {
			return out, bad("PLUGIN_EVENT_INVALID", "订阅完成判定原因无效")
		}
	}
	return out, nil
}
