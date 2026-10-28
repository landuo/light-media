package plugins

import "slices"

// BroadcastHandler binds a declared SDK method to a broadcast event. Multiple
// methods may subscribe to the same event; each receives an isolated Event.
// Explicit declarations replace Events' legacy event-method shorthand per type.
type BroadcastHandler struct {
	Event  string `json:"event"`
	Method string `json:"method"`
}

func (m Manifest) validateBroadcastHandlers() error {
	if len(m.BroadcastHandlers) > 64 {
		return failure("invalid_manifest")
	}
	seen := map[string]bool{}
	for _, h := range m.BroadcastHandlers {
		key := h.Event + "/" + h.Method
		if !(EventQuery{Kind: BroadcastEventKind, Type: h.Event}).Valid() || !methodPattern.MatchString(h.Method) || !slices.Contains(m.Methods, h.Method) || seen[key] {
			return failure("invalid_manifest")
		}
		seen[key] = true
	}
	return nil
}
