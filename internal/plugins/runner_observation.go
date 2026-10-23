package plugins

import "context"

// RunPhase identifies host-side boundaries, not child process internals.
// An End event means the operation returned, not that it succeeded.
type RunPhase uint8

const (
	PhaseRunEntered RunPhase = iota + 1
	PhaseStartBegin
	PhaseStartEnd
	PhaseHandshakeBegin
	PhaseHandshakeEnd
	PhaseNegotiateBegin
	PhaseNegotiateEnd
	PhaseInvokeBegin
	PhaseInvokeEnd
	PhaseStopBegin
	PhaseStopEnd
	PhaseWaitBegin
	PhaseWaitEnd
	PhaseCleanupWaitBegin
	PhaseCleanupWaitEnd
	PhaseRunReturned
)

func (p RunPhase) String() string {
	switch p {
	case PhaseRunEntered:
		return "run_entered"
	case PhaseStartBegin:
		return "start_begin"
	case PhaseStartEnd:
		return "start_end"
	case PhaseHandshakeBegin:
		return "handshake_begin"
	case PhaseHandshakeEnd:
		return "handshake_end"
	case PhaseNegotiateBegin:
		return "negotiate_begin"
	case PhaseNegotiateEnd:
		return "negotiate_end"
	case PhaseInvokeBegin:
		return "invoke_begin"
	case PhaseInvokeEnd:
		return "invoke_end"
	case PhaseStopBegin:
		return "stop_begin"
	case PhaseStopEnd:
		return "stop_end"
	case PhaseWaitBegin:
		return "wait_begin"
	case PhaseWaitEnd:
		return "wait_end"
	case PhaseCleanupWaitBegin:
		return "cleanup_wait_begin"
	case PhaseCleanupWaitEnd:
		return "cleanup_wait_end"
	case PhaseRunReturned:
		return "run_returned"

	default:
		return "unknown"
	}
}

type RunContextState uint8

const (
	RunContextActive RunContextState = iota
	RunContextCanceled
	RunContextDeadline
	RunContextUnavailable
)

func (s RunContextState) String() string {
	switch s {
	case RunContextActive:
		return "active"
	case RunContextCanceled:
		return "canceled"
	case RunContextDeadline:
		return "deadline"
	default:
		return "unavailable"
	}
}

type RunObservation struct {
	Phase        RunPhase
	ContextState RunContextState
}

// observe is synchronous on the Run goroutine. It supplies no command, payload,
// identity, result or error. Trusted observers must be bounded and nonblocking;
// their execution remains charged to the existing wall-clock budget.
func (r Runner) observe(p RunPhase, ctx context.Context) {
	if r.ObservePhase == nil {
		return
	}
	state := RunContextUnavailable
	if ctx != nil {
		switch ctx.Err() {
		case nil:
			state = RunContextActive
		case context.Canceled:
			state = RunContextCanceled
		case context.DeadlineExceeded:
			state = RunContextDeadline
		}
	}
	r.ObservePhase(RunObservation{Phase: p, ContextState: state})
}
