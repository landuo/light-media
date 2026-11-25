package plugins

import "slices"

// ChainHandler declares a synchronous mutable-event subscriber, not an RPC
// capability or an event producer. The host must connect the named chain at
// its real core boundary. Missing priority is the original decorator's 10.
type ChainHandler struct {
	Event    string `json:"event"`
	Method   string `json:"method"`
	Priority *int   `json:"priority,omitempty"`
}

func (h ChainHandler) Order() int {
	if h.Priority == nil {
		return 10
	}
	return *h.Priority
}

func (m Manifest) validateChainHandlers() error {
	if len(m.ChainHandlers) > 64 {
		return failure("invalid_manifest")
	}
	seen := map[string]bool{}
	for _, h := range m.ChainHandlers {
		key := h.Event + "/" + h.Method
		if !(EventQuery{Kind: ChainEventKind, Type: h.Event}).Valid() || !methodPattern.MatchString(h.Method) || !slices.Contains(m.Methods, h.Method) || seen[key] {
			return failure("invalid_manifest")
		}
		seen[key] = true
	}
	if slices.Contains(m.Events, "command.register") {
		explicit := false
		for _, h := range m.ChainHandlers {
			if h.Event == "command.register" {
				explicit = true
			}
		}
		if !explicit && !slices.Contains(m.Methods, "event") {
			return failure("invalid_manifest")
		}
	}
	return nil
}
