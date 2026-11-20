package plugins

import (
	"context"
	"encoding/json"
)

// Session is the host-side surface of one live plugin worker. The wire
// process client and the in-process builtin client both satisfy it; the app
// layer programs against this interface only. A nil Session is not usable:
// every method documents its own nil behavior through the concrete types.
type Session interface {
	Call(ctx context.Context, method string, payload json.RawMessage, host HostHandler) (RunResult, error)
	CallWithOptions(ctx context.Context, method string, payload json.RawMessage, host HostHandler, options CallOptions) (RunResult, error)
	CallPrivateSchedulerStop(ctx context.Context, method string, payload json.RawMessage, host HostHandler) (RunResult, error)
	CallPublicServiceStop(ctx context.Context, method string, payload json.RawMessage, host HostHandler) (RunResult, error)
	CallCleanup(ctx context.Context, method string, payload json.RawMessage, host HostHandler) (RunResult, error)
	BeginShutdown(ctx context.Context) error
	CloseTransport(ctx context.Context) error
	CleanupContext(parentHostCtx context.Context) (context.Context, error)
	StartCleanupContinuationWithAdmission(parentHostCtx context.Context, method string, payload json.RawMessage, host HostHandler) (*PendingCall, error)
	StartCall(ctx context.Context, method string, payload json.RawMessage, host HostHandler, options CallOptions) (*PendingCall, error)
	StartCallWithAdmission(admissionCtx, ctx context.Context, method string, payload json.RawMessage, host HostHandler, options CallOptions) (*PendingCall, error)
	Stop(ctx context.Context) error
	StopWithHost(ctx context.Context, host HostHandler) error
	SupportsConcurrent() bool
	SupportsParallelHostCalls() bool
	SupportsTransportClose() bool
	SupportsPrivateSchedulerStop() bool
	SupportsPublicServiceStop() bool
	SupportsCleanupContinuations() bool
}
