package cleanuphost

import (
	"context"
	"light-media/internal/plugins/removelink"
)

// OriginalState reads the retained machine on the host command loop. It does
// not open an owner, restore a snapshot, acquire an execution lease or initialize
// the machine. A closed host remains closed, including while shutdown races it.
func (h *Host) OriginalState(ctx context.Context) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if h == nil {
		return false, ErrClosed
	}
	// A channel avoids writing a caller-owned variable after cancellation returns.
	result := make(chan bool, 1)
	err := h.call(ctx, func() error {
		if h.closing {
			return ErrClosed
		}
		if h.machine == nil {
			return ErrUncertain
		}
		result <- removelink.Truth(h.machine.StateValue())
		return nil
	})
	if err != nil {
		return false, err
	}
	return <-result, nil
}
