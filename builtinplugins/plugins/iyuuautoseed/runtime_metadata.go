package iyuuautoseed

import (
	"encoding/json"
	"errors"
	"sync/atomic"
)

// Public declarations depend only on configuration, not the active network
// pass. Publish immutable results at configuration transitions so marketplace
// state/service queries never wait on core.runMu or inspect its mutable caches.
type runtimeMetadata struct {
	state        bool
	services     json.RawMessage
	serviceError error
}
type runtimeMetadataSlot struct {
	value atomic.Pointer[runtimeMetadata]
}

func newPluginRuntime(core *Core) *pluginRuntime {
	runtime := &pluginRuntime{core: core}
	runtime.publishMetadata()
	return runtime
}

// Caller owns core.runMu, or the runtime has not yet been published.
func (r *pluginRuntime) publishMetadata() {
	services, err := publicServices(r.core.config)
	r.metadata.value.Store(&runtimeMetadata{state: r.core.config.State(), services: append(json.RawMessage(nil), services...), serviceError: err})
}
func (r *pluginRuntime) metadataResponse(method string) (json.RawMessage, error) {
	m := r.metadata.value.Load()
	if m == nil {
		return nil, errors.New("IYUU public metadata unavailable")
	}
	if method == "get_state" {
		return json.Marshal(m.state)
	}
	if m.serviceError != nil {
		return nil, m.serviceError
	}
	return append(json.RawMessage(nil), m.services...), nil
}
