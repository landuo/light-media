package seedrescue

import (
	"context"
)

// NewReadProjection is a canonical-host adapter, not a source initialization.
// The caller supplies a verified immutable config/status snapshot and read-only
// Host, allowing the original status/job views while a native scan holds its
// mutation lease. It cannot schedule, clear once, write KV or mark running scans
// interrupted. The returned object must only consume GetStatus/GetJob/Form.
func NewReadProjection(ctx context.Context, host Host, config Object, status Object, busy bool) *Machine {
	m := &Machine{host: host, life: ctx, config: ParseConfig(config), gate: make(chan struct{}, 1), status: clone(status)}
	if len(m.status) == 0 {
		m.status = Object{"state": "idle"}
	}
	if busy {
		m.gate <- struct{}{}
	}
	return m
}
