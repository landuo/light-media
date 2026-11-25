package plugins

import (
	"context"
	"encoding/json"
	"errors"
)

const (
	EventSendCapability   = "event.send"
	EventCheckCapability  = "event.check"
	EventManageCapability = "event.manage"
	BroadcastEventKind    = "broadcast"
	ChainEventKind        = "chain"
)

// EventQuery explicitly identifies the original EventType/ChainEventType kind.
// Equal string values do not infer the kind from current registrations.
type EventQuery struct {
	Kind string `json:"kind"`
	Type string `json:"type"`
}

func (q EventQuery) Valid() bool {
	return (q.Kind == BroadcastEventKind || q.Kind == ChainEventKind) && methodPattern.MatchString(q.Type) && !excludedPluginExtensionEvent(q.Type)
}

// These four fixed-source events are dedicated workflow/AI extension points,
// explicitly excluded by the user. Ordinary and author-defined event names
// remain available; do not reject unrelated events containing similar words.
func excludedPluginExtensionEvent(kind string) bool {
	switch kind {
	case "workflow.execute", "workflow.execution", "agent.tokens.usage", "agent.llm.provider":
		return true
	}
	return false
}

type EventSendRequest struct {
	Kind     string          `json:"kind"`
	Type     string          `json:"type"`
	Data     json.RawMessage `json:"data"`
	Priority *int            `json:"priority,omitempty"`
}

// SendEvent returns nil for broadcasts and for chains without enabled handlers.
// A chain result is the shared event after mutations, not a handler's return.
// The active HostCaller must negotiate concurrency for synchronous chain calls.
func SendEvent(ctx context.Context, host HostCaller, input EventSendRequest) (*MutableEvent, error) {
	if host == nil || !(EventQuery{input.Kind, input.Type}).Valid() {
		return nil, errors.New("invalid plugin event")
	}
	if len(input.Data) == 0 {
		input.Data = json.RawMessage(`null`)
	}
	raw, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	result, err := host(ctx, EventSendCapability, raw)
	if err != nil {
		return nil, err
	}
	var event *MutableEvent
	err = json.Unmarshal(result, &event)
	return event, err
}

// CheckEvent asks the host whether an enabled handler currently exists. It does
// not invoke get_state or grant permission to call a method directly.
func CheckEvent(ctx context.Context, host HostCaller, query EventQuery) (bool, error) {
	if host == nil || !query.Valid() {
		return false, errors.New("invalid plugin event query")
	}
	raw, _ := json.Marshal(query)
	result, err := host(ctx, EventCheckCapability, raw)
	if err != nil {
		return false, err
	}
	var enabled bool
	err = json.Unmarshal(result, &enabled)
	return enabled, err
}

// EventControl changes only the calling object's declared methods. It never
// accepts a plugin ID, class name, arbitrary function or external code path.
type EventControl struct {
	Action   string `json:"action"`
	Kind     string `json:"kind,omitempty"`
	Type     string `json:"type,omitempty"`
	Method   string `json:"method,omitempty"`
	Priority *int   `json:"priority,omitempty"`
}

func (c EventControl) Valid() bool {
	switch c.Action {
	case "add", "remove":
		return (EventQuery{c.Kind, c.Type}).Valid() && methodPattern.MatchString(c.Method)
	case "enable_handler", "disable_handler":
		return c.Kind == "" && c.Type == "" && c.Priority == nil && methodPattern.MatchString(c.Method)
	case "enable_class", "disable_class", "visualize":
		return c.Kind == "" && c.Type == "" && c.Method == "" && c.Priority == nil
	}
	return false
}

type EventHandlerInfo struct {
	EventType         string `json:"event_type"`
	HandlerIdentifier string `json:"handler_identifier"`
	Status            string `json:"status"`
	Priority          *int   `json:"priority,omitempty"`
}

// ManageEvent returns the handler inventory for visualize and nil for mutation.
// Mutations affect the current registry immediately, just like EventManager.
func ManageEvent(ctx context.Context, host HostCaller, control EventControl) ([]EventHandlerInfo, error) {
	if host == nil || !control.Valid() {
		return nil, errors.New("invalid plugin event control")
	}
	raw, err := json.Marshal(control)
	if err != nil {
		return nil, err
	}
	result, err := host(ctx, EventManageCapability, raw)
	if err != nil {
		return nil, err
	}
	var handlers []EventHandlerInfo
	err = json.Unmarshal(result, &handlers)
	return handlers, err
}
