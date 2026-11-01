package plugins

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"strings"
)

// material/v1 is negotiated independently of ordinary chunk transport. The
// host opts in, the worker advertises, and both commit the same explicit ACK.
// An absent advertisement preserves ordinary limits and cannot issue tickets.
func (s *wireSession) negotiateMaterial(requested bool, advertised string) error {
	if !requested || advertised == "" {
		return nil
	}
	if advertised != MaterialContract || !s.enabled {
		return failure("material_negotiation_mismatch")
	}
	raw, err := s.exchange("transport.material_negotiate", json.RawMessage(`{"contract":"material/v1"}`))
	if err != nil {
		return err
	}
	if !validMaterialNegotiation(raw) {
		return failure("material_negotiation_mismatch")
	}
	s.materialEnabled = true
	return nil
}
func validMaterialNegotiation(raw json.RawMessage) bool {
	var v struct {
		Contract string `json:"contract"`
	}
	return materialUniqueJSON(raw) == nil && materialExactFields(raw, "contract") && strictJSON(raw, &v) == nil && v.Contract == MaterialContract
}
func materialCapability(method string) bool {
	switch method {
	case LocalTorrentBackupCapability, DownloadTorrentCapability, QBittorrentAddCapability, TransmissionAddCapability:
		return true
	}
	return false
}
func materialHostCallback(scope *materialHostScope, ctx context.Context, host HostHandler, identity Identity, method string, payload json.RawMessage) (json.RawMessage, error) {
	if scope == nil || !materialCapability(method) {
		return host(ctx, identity, method, payload)
	}
	ticket, end, err := scope.hostContext(ctx, method)
	if err != nil {
		return nil, err
	}
	defer end() // Authority to issue ends with this actual ordinary callback.
	return host(ticket, identity, method, payload)
}

// beginMaterialInvocation runs only in the serial invocation's existing gate.
// The persistent Client's immutable installed generation is host-owned; Run
// does not fabricate a generation and cannot enable this feature.
func (s *wireSession) beginMaterialInvocation(id, method string) (*materialHostScope, error) {
	if !s.materialEnabled || method == "handshake" || method == "stop" || strings.HasPrefix(method, "transport.") {
		return nil, nil
	}
	binding := s.materialBinding
	return newMaterialHostScope(s.ctx, binding, id, s.maxFrame, s.materialCapabilities, func(ctx context.Context) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if s.aborted.Load() || s.materialBinding != binding {
			return failure("material_scope_closed")
		}
		return s.ctx.Err()
	})
}

// The SDK bridge is private and comes only from the actual Handler context.
// Public HostCaller still rejects every transport.* method. No path, provider
// URL or arbitrary RPC entry point is introduced here.
type materialPeerKey struct{}
type materialPeerBridge func(context.Context, MaterialGrant, string, int64, []byte) (materialResponse, error)

func materialExchange(ctx context.Context, grant MaterialGrant, operation string, offset int64, data []byte) (materialResponse, error) {
	if ctx == nil || ctx.Err() != nil {
		return materialResponse{}, failure("execution_canceled")
	}
	bridge, ok := ctx.Value(materialPeerKey{}).(materialPeerBridge)
	if !ok || bridge == nil {
		return materialResponse{}, failure("material_not_negotiated")
	}
	return bridge(ctx, grant, operation, offset, data)
}
func materialPeerRequest(identity Identity, invocation string, grant MaterialGrant, operation string, offset int64, data []byte) (Frame, error) {
	if grant.Invocation != invocation {
		return Frame{}, failure("material_identity_mismatch")
	}
	if data == nil {
		data = []byte{}
	}
	raw, err := json.Marshal(materialRequest{Grant: grant, Operation: operation, Offset: offset, Data: data})
	if err != nil {
		return Frame{}, err
	}
	frame := Frame{Protocol: ProtocolVersion, ID: invocation + "." + grant.Token, Type: "host_call", Identity: identity, Method: materialMethod, Payload: raw}
	if _, err = decodeMaterialRequest(frame); err != nil {
		return Frame{}, err
	}
	return frame, nil
}

func (s *wireSession) serialMaterialContext(ctx context.Context, invocation string, gate chan struct{}, phase *callbackPhase) context.Context {
	if !s.materialEnabled {
		return ctx
	}
	bridge := materialPeerBridge(func(callCtx context.Context, grant MaterialGrant, operation string, offset int64, data []byte) (materialResponse, error) {
		if callCtx == nil || callCtx.Err() != nil || ctx.Err() != nil || phase.closed.Load() {
			return materialResponse{}, failure("execution_canceled")
		}
		select {
		case gate <- struct{}{}:
		case <-callCtx.Done():
			return materialResponse{}, failure("execution_canceled")
		case <-ctx.Done():
			return materialResponse{}, failure("execution_canceled")
		}
		defer func() { <-gate }()
		if phase.closed.Load() || callCtx.Err() != nil || ctx.Err() != nil {
			return materialResponse{}, failure("execution_canceled")
		}
		request, err := materialPeerRequest(s.identity, invocation, grant, operation, offset, data)
		if err != nil {
			return materialResponse{}, err
		}
		stop := context.AfterFunc(callCtx, s.abort)
		defer stop()
		value, err := s.materialPeerExchange(callCtx, invocation, request)
		if err != nil {
			s.abort()
		}
		return value, err
	})
	return context.WithValue(ctx, materialPeerKey{}, bridge)
}

// readMaterialRuntimePhysical is called by the existing serial reader only.
// At most one bounded frame is held before classification; unknown/invalid
// frames are never excluded from ordinary cumulative accounting.
func (s *wireSession) readMaterialRuntimePhysical() (Frame, error) {
	for {
		if s.ctx.Err() != nil || s.aborted.Load() {
			return Frame{}, failure("execution_canceled")
		}
		frame, n, err := readBoundedRuntimeLine(s.reader, s.maxFrame)
		var countErr error
		s.outputBytes, countErr = materialAddCount(s.outputBytes, n)
		if countErr != nil {
			return Frame{}, countErr
		}
		if err != nil {
			return Frame{}, err
		}
		if frame.Method == materialMethod {
			if !frame.materialStrict {
				return Frame{}, failure("invalid_material_frame")
			}
			reply, err := s.materialScope.handle(s.ctx, frame)
			if err != nil {
				return Frame{}, err
			}
			if err = s.materialAdmitRead(n); err != nil {
				return Frame{}, err
			}
			if err = s.materialScope.check(s.ctx); err != nil {
				return Frame{}, err
			}
			if err = s.materialWrite(s.ctx, reply); err != nil {
				return Frame{}, err
			}
			continue
		}
		if s.outputBytes-s.materialOutputBytes > s.maxOutput {
			return Frame{}, failure("output_limit")
		}
		return frame, nil
	}
}

func readBoundedRuntimeLine(reader *bufio.Reader, maxFrame int) (Frame, int, error) {
	var raw []byte
	n := 0
	for {
		part, err := reader.ReadSlice('\n')
		var countErr error
		n, countErr = materialAddCount(n, len(part))
		if countErr != nil {
			return Frame{}, n, countErr
		}
		if n > maxFrame {
			return Frame{}, n, failure("frame_limit")
		}
		raw = append(raw, part...)
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if err != nil {
			return Frame{}, n, failure("worker_io_failed")
		}
		break
	}
	var frame Frame
	if decodeTransportFrame(raw, &frame) != nil {
		return Frame{}, n, failure("invalid_frame")
	}
	frame.materialPhysical = true
	frame.materialStrict = materialUniqueJSON(raw) == nil
	return frame, n, nil
}

// CheckMaterialHost checks this actual callback's private issuance authority
// before a consumer starts owned IO. It grants no lease, path or material and
// does not replace the consumer's current guard or IssueMaterial's final check.
func CheckMaterialHost(ctx context.Context, capability string) error {
	if ctx == nil || !materialCapability(capability) {
		return failure("material_issue_denied")
	}
	ticket, ok := ctx.Value(materialIssueKey{}).(*materialIssueTicket)
	if !ok || ticket == nil || ticket.scope == nil || ticket.capability != capability {
		return failure("material_issue_denied")
	}
	if err := ticket.scope.check(ctx); err != nil {
		return err
	}
	ticket.mu.Lock()
	defer ticket.mu.Unlock()
	if !ticket.active {
		return failure("material_issue_closed")
	}
	return nil
}
func materialPeerAvailable(ctx context.Context) bool {
	if ctx == nil || ctx.Err() != nil {
		return false
	}
	bridge, ok := ctx.Value(materialPeerKey{}).(materialPeerBridge)
	return ok && bridge != nil
}
