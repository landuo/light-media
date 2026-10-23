package scrapehost

import (
	"context"
	"light-media/internal/plugins/scrapefileclean"
)

// OriginalMachine exposes identity only to the trusted App owner. The caller
// holds its owner gate; mutations continue through this host's serialized calls.
func (h *Host) OriginalMachine(ctx context.Context) (*scrapefileclean.Machine, error) {
	out := make(chan *scrapefileclean.Machine, 1)
	err := h.call(ctx, func() error {
		if h.closing {
			return ErrClosed
		}
		out <- h.machine
		return nil
	})
	if err != nil {
		return nil, err
	}
	return <-out, nil
}

// AdoptOriginalIdleMachine preserves logical members when a verified inert
// owner acquires real authority roots. Open/newLocal validation is unchanged.
// This runs only before the newly opened host's first actual Init call.
func (h *Host) AdoptOriginalIdleMachine(ctx context.Context, m *scrapefileclean.Machine) error {
	return h.call(ctx, func() error {
		if h.closing {
			return ErrClosed
		}
		if m == nil || h.running || h.watcher != nil || len(m.Watches) != 0 || len(m.Queue) != 0 || m.TimerDue != nil {
			return ErrUnsafe
		}
		m.Ops = h.ops
		m.Now = h.opts.Now
		h.machine = m
		return nil
	})
}
