package app

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"light-media/internal/plugins"
	"light-media/internal/plugins/builtin"
)

// All diagnostics are request-local and contain only fixed classes and counters.
// No error string, callback method, identity, payload or result is retained.
type transferFormObservation struct {
	mu                              sync.Mutex
	started                         time.Time
	queueMS                         int64
	phases                          map[string]int64
	runnerContext                   string
	hostStarted, hostEnded          int
	hostMS                          int64
	hostFirstStartMS, hostLastEndMS int64
	hostErrors                      map[string]int
}

func newTransferFormObservation(pluginID string) *transferFormObservation {
	if !isTorrentTransfer(pluginID) {
		return nil
	}
	return &transferFormObservation{started: time.Now(), queueMS: -1, phases: map[string]int64{}, hostErrors: map[string]int{}, runnerContext: "not_started", hostFirstStartMS: -1, hostLastEndMS: -1}
}
func transferFormErrorClass(err error) string {
	if err == nil {
		return "none"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "deadline"
	}
	if errors.Is(err, context.Canceled) {
		return "canceled"
	}
	var pe *plugins.Error
	if errors.As(err, &pe) && pe != nil {
		switch pe.Code {
		case "execution_canceled":
			return "execution_canceled"
		case "frame_limit":
			return "frame_limit"
		case "handshake_mismatch":
			return "handshake_mismatch"
		case "host_call_limit":
			return "host_call_limit"
		case "host_operation_failed":
			return "host_operation_failed"
		case "identity_mismatch":
			return "identity_mismatch"
		case "incomplete_transport_message":
			return "incomplete_transport_message"
		case "input_limit":
			return "input_limit"
		case "invalid_frame":
			return "invalid_frame"
		case "invalid_host_call":
			return "invalid_host_call"
		case "invalid_invocation":
			return "invalid_invocation"
		case "invalid_limits":
			return "invalid_limits"
		case "invalid_manifest":
			return "invalid_manifest"
		case "invalid_reply":
			return "invalid_reply"
		case "invalid_transport_ack":
			return "invalid_transport_ack"
		case "invalid_transport_chunk":
			return "invalid_transport_chunk"
		case "invalid_transport_length":
			return "invalid_transport_length"
		case "invalid_transport_limits":
			return "invalid_transport_limits"
		case "invalid_transport_message":
			return "invalid_transport_message"
		case "invalid_transport_sequence":
			return "invalid_transport_sequence"
		case "invalid_worker":
			return "invalid_worker"
		case "message_limit":
			return "message_limit"
		case "output_limit":
			return "output_limit"
		case "plugin_operation_failed":
			return "plugin_operation_failed"
		case "request_failed":
			return "request_failed"
		case "stderr_limit":
			return "stderr_limit"
		case "transport_chunk_limit":
			return "transport_chunk_limit"
		case "transport_mismatch":
			return "transport_mismatch"
		case "transport_not_negotiated":
			return "transport_not_negotiated"
		case "unsupported_api":
			return "unsupported_api"
		case "worker_failed":
			return "worker_failed"
		case "worker_io_failed":
			return "worker_io_failed"
		case "worker_start_failed":
			return "worker_start_failed"

		default:
			return "runner_other"
		}
	}
	var ae *APIError
	if errors.As(err, &ae) && ae != nil {
		return "host_api_error"
	}
	return "other"
}

// A bounded host taxonomy prevents an error histogram from exceeding log limits.
func transferFormHostErrorClass(err error) string {
	if err == nil {
		return "none"
	}
	if errors.Is(err, context.Canceled) {
		return "canceled"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "deadline"
	}
	var ae *APIError
	if errors.As(err, &ae) && ae != nil {
		switch ae.Code {
		case "TORRENT_TRANSFER_OWNER_DENIED":
			return "owner_denied"
		case "TORRENT_TRANSFER_REFERENCES_UNCLAIMED":
			return "references_unclaimed"
		case "TORRENT_TRANSFER_NAME_CONFLICT":
			return "name_conflict"
		default:
			return "host_api_error"
		}
	}
	var pe *plugins.Error
	if errors.As(err, &pe) && pe != nil {
		return "runner_error"
	}
	return "other"
}
func transferFormContextClass(ctx context.Context) string {
	if ctx == nil {
		return "unavailable"
	}
	return transferFormErrorClass(ctx.Err())
}
func (d *transferFormObservation) acquired() {
	if d == nil {
		return
	}
	d.queueMS = time.Since(d.started).Milliseconds()
}
func (d *transferFormObservation) observe(v plugins.RunObservation) {
	d.mu.Lock()
	defer d.mu.Unlock()
	// Unknown numeric enum values cannot introduce a new log key.
	if v.Phase.String() == "unknown" {
		return
	}
	d.phases[v.Phase.String()] = time.Since(d.started).Milliseconds()
	d.runnerContext = v.ContextState.String()
}
func (d *transferFormObservation) wrapHost(next plugins.HostHandler) plugins.HostHandler {
	if d == nil {
		return next
	}
	return func(ctx context.Context, identity plugins.Identity, method string, raw json.RawMessage) (json.RawMessage, error) {
		start := time.Now()
		d.mu.Lock()
		if d.hostStarted == 0 {
			d.hostFirstStartMS = start.Sub(d.started).Milliseconds()
		}
		d.hostStarted++
		d.mu.Unlock()
		result, err := next(ctx, identity, method, raw)
		class := transferFormHostErrorClass(err)
		d.mu.Lock()
		d.hostEnded++
		d.hostMS += time.Since(start).Milliseconds()
		d.hostLastEndMS = time.Since(d.started).Milliseconds()
		if err != nil {
			d.hostErrors[class]++
		}
		d.mu.Unlock()
		return result, err
	}
}
func (d *transferFormObservation) failureMessage(ctx context.Context, stage string, result plugins.RunResult, err error) string {
	if d == nil {
		return ""
	}
	switch stage {
	case "queue", "runner", "form_decode", "render_mode":
	default:
		stage = "unknown"
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	queueMS := d.queueMS
	if stage == "queue" {
		queueMS = time.Since(d.started).Milliseconds()
	}
	// JSON is encoded while holding the lock; no mutable maps escape the snapshot.
	data, _ := json.Marshal(map[string]any{
		"stage": stage, "error_class": transferFormErrorClass(err), "context": transferFormContextClass(ctx), "runner_context": d.runnerContext,
		"elapsed_ms": time.Since(d.started).Milliseconds(), "queue_ms": queueMS,
		"phases_ms": d.phases, "host_started": d.hostStarted, "host_ended": d.hostEnded,
		"host_total_ms": d.hostMS, "host_first_start_ms": d.hostFirstStartMS, "host_last_end_ms": d.hostLastEndMS,
		"host_error_classes": d.hostErrors, "host_calls": result.HostCalls,
		"sent_wire_bytes": result.SentWireBytes, "read_wire_bytes": result.ReadWireBytes,
	})
	return string(data)
}
func (a *App) logTransferFormFailure(d *transferFormObservation, installation PluginInstallation, ctx context.Context, stage string, result plugins.RunResult, err error) {
	if d == nil {
		return
	}
	// Existing bounded/redacting logger remains the only sink. No API changes.
	// Only the host's validated installation identity supplies attribution.
	// Callback identity and worker-controlled values never enter this record.
	manifest, _, exists := builtin.Lookup(installation.PluginID)
	if !exists || !isTorrentTransfer(installation.PluginID) || manifest.Version != installation.Version || !pluginStorageID.MatchString(installation.InstanceID) {
		return
	}
	a.writeLog(map[string]any{
		"time": now(), "id": id("log_"), "level": "WARN", "module": "plugins",
		"event": "torrent_transfer_form_failed", "message": d.failureMessage(ctx, stage, result, err),
		"app_version": appVersion, "plugin_id": installation.PluginID,
		"plugin_instance_id": installation.InstanceID, "plugin_version": installation.Version,
	})
}
