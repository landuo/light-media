package plugins

import "context"

// HostCallBudgetSnapshot is observational metadata, never permission or an
// atomic reservation. A consumer must reserve unknown in-flight results itself.
// All Charged fields below are per incoming invocation, including attempted
// writes charged before I/O; cancellation/error does not refund them.
type HostCallBudgetSnapshot struct {
	MaxHostCalls         int
	HostCallsCharged     int
	MaxParallelHostCalls int

	// PhysicalFrameBytes is the encoded physical JSON line ceiling including
	// its newline. LogicalMessageBytes includes the
	// complete identity/RPC envelope. RawChunkBytes is before chunk base64.
	PhysicalFrameBytes           int
	LogicalMessageBytes          int
	RawChunkBytes                int
	ChunksPerLogicalMessage      int
	CumulativeChunksPerDirection int

	// Directions are from the worker's perspective. These are cumulative per
	// incoming call in the concurrent protocol, not process/pass-wide quotas.
	MaxWorkerToHostWireBytes     int
	WorkerToHostWireBytesCharged int
	WorkerToHostChunksCharged    int
	MaxHostToWorkerWireBytes     int
	HostToWorkerWireBytesCharged int
	HostToWorkerChunksCharged    int

	// Session state is not reset by a new phase. The ordinary admission limit
	// excludes the separately reserved stop/cleanup rules; Entries includes them.
	SessionOrdinaryIncomingLimit int
	SessionIncomingEntries       int
}

type hostCallBudgetKey struct{}
type hostCallBudgetView struct {
	worker *concurrentWorker
	call   *concurrentWorkerCall
}

// CurrentHostCallBudget returns a real, live opted-in concurrent SDK phase's
// negotiated limits and actual charged counters. Closed, non-SDK and legacy
// contexts return false. It contains no identity, credentials or HostCaller.
// A derived context retains its phase; it cannot turn another phase's snapshot
// into authority. Frame/chunk/byte limits remain enforced by the transport.
func CurrentHostCallBudget(ctx context.Context) (HostCallBudgetSnapshot, bool) {
	var out HostCallBudgetSnapshot
	if ctx == nil || ctx.Err() != nil {
		return out, false
	}
	view, ok := ctx.Value(hostCallBudgetKey{}).(*hostCallBudgetView)
	if !ok || view == nil || view.worker == nil || view.call == nil {
		return out, false
	}
	w, call := view.worker, view.call
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.calls[call.request.ID] != call || w.proposal.MaxParallelHostCalls == 0 {
		return out, false
	}
	call.mu.Lock()
	defer call.mu.Unlock()
	if call.reverseClosed || call.phase.closed.Load() || call.ctx.Err() != nil {
		return out, false
	}
	usage := call.account.snapshot()
	limits := w.transport.wire.limits
	return HostCallBudgetSnapshot{
		MaxHostCalls: w.proposal.MaxHostCalls, HostCallsCharged: usage.HostCalls, MaxParallelHostCalls: w.proposal.MaxParallelHostCalls,
		PhysicalFrameBytes: limits.FrameBytes, LogicalMessageBytes: limits.LogicalBytes, RawChunkBytes: limits.ChunkBytes,
		ChunksPerLogicalMessage: limits.MessageChunks, CumulativeChunksPerDirection: limits.SessionChunks,
		MaxWorkerToHostWireBytes: limits.HostReadBytes, WorkerToHostWireBytesCharged: usage.SentWireBytes, WorkerToHostChunksCharged: usage.SentChunks,
		MaxHostToWorkerWireBytes: limits.HostSendBytes, HostToWorkerWireBytesCharged: usage.ReadWireBytes, HostToWorkerChunksCharged: usage.ReadChunks,
		SessionOrdinaryIncomingLimit: w.proposal.MaxCalls, SessionIncomingEntries: len(w.calls),
	}, true
}
