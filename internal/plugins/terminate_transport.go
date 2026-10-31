package plugins

import (
	"context"
	"errors"
)

const intentionalTransportTermination = "transport_terminated"

// TerminateTransport is a host-owned physical termination, not a plugin
// lifecycle operation or negotiated graceful retirement. It sends no frames.
// The App must first release its exact-object owners and observe actual drain.
// A successful result means the existing child was killed/closed and reaped;
// it does not mean that child exited zero or completed business cleanup.
//
// A claimant always observes the actual reaper, even if ctx then expires.
// A canceled caller before admission or a nonclaimant whose wait expires has
// no such receipt: transport_termination_pending must not mark App retirement
// complete. Admission/observer waits use only the caller context; the runner
// timeout bounds claimant host/write cleanup, never a hidden observer cutoff.
// Existing terminal failures remain failures. Later Stop observes
// the intentional terminal marker rather than pretending a clean worker exit.
func (c *Client) TerminateTransport(ctx context.Context) error {
	if c == nil || ctx == nil || c.session == nil || c.cancel == nil || c.done == nil {
		return failure("invalid_invocation")
	}
	if ctx.Err() != nil {
		return failure("transport_termination_pending")
	}
	c.stopRequested.Store(true)
	if c.concurrent != nil {
		return c.concurrent.terminateTransport(ctx)
	}

	// The existing serial gate also serializes Stop and terminal state. No
	// ordinary handler or host callback is invoked while this operation owns it.
	select {
	case c.gate <- struct{}{}:
	default:
		select {
		case c.gate <- struct{}{}:
		case <-ctx.Done():
			return failure("transport_termination_pending")
		}
	}
	defer func() { <-c.gate }()
	if c.stopped {
		// Every existing serial Stop/fail releases this gate after actual Wait.
		<-c.done
		return physicalTerminationResult(c.terminal)
	}
	c.stopped = true
	if c.terminal == nil {
		c.terminal = c.physicalTerminationReason()
	}
	c.cancel()
	c.session.abort()
	<-c.done
	return physicalTerminationResult(c.terminal)
}

// Called under the serial gate or concurrent state mutex, before this
// operation cancels the child. Never replace a known earlier failure with our
// intentional marker; a crash racing the later cancel is not health evidence.
func (c *Client) physicalTerminationReason() error {
	select {
	case <-c.done:
		if c.waitErr != nil {
			return failure("worker_failed")
		}
	default:
	}
	if c.life.Err() != nil {
		return failure("execution_canceled")
	}
	return failure(intentionalTransportTermination)
}

func physicalTerminationResult(err error) error {
	var local *Error
	if errors.As(err, &local) && local.Code == intentionalTransportTermination {
		return nil
	}
	return err
}

func (m *concurrentClient) terminateTransport(ctx context.Context) error {
	m.mu.Lock()
	if m.stopDone != nil {
		done := m.stopDone
		m.mu.Unlock()
		select {
		case <-done:
		default:
			select {
			case <-done:
			case <-ctx.Done():
				return failure("transport_termination_pending")
			}
		}
		m.mu.Lock()
		err := m.stopErr
		m.mu.Unlock()
		return physicalTerminationResult(err)
	}
	m.stopDone = make(chan struct{})
	m.stopping, m.draining = true, true
	if m.terminal == nil {
		m.terminal = m.client.physicalTerminationReason()
	}
	err := m.terminal
	for _, call := range m.calls {
		call.revoke()
	}
	m.mu.Unlock()

	bounded, cancel := context.WithTimeout(ctx, m.client.runner.Timeout)
	defer cancel()
	m.client.cancel()
	m.client.session.abort()
	<-m.client.done
	<-m.readerDone
	// The reader is gone and ordinary admission is sealed before Wait, so
	// these groups cannot gain another writer/host callback after this point.
	joined := make(chan struct{})
	go func() { m.writes.Wait(); m.hosts.Wait(); close(joined) }()
	select {
	case <-joined:
	default:
		select {
		case <-joined:
		case <-bounded.Done():
			if physicalTerminationResult(err) == nil {
				err = failure("execution_canceled")
			}
		}
	}
	m.mu.Lock()
	m.stopErr = err
	close(m.stopDone)
	m.mu.Unlock()
	return physicalTerminationResult(err)
}
