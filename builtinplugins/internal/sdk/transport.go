package plugins

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
)

const defaultWireBytes = 64 << 20

// MaxPayloadBytes is the shared storage/result value ceiling. Transport allows
// the explicit logical envelope allowance for JSON-RPC and scoped KV wrappers.
const MaxPayloadBytes = 16 << 20

// MaxLogicalMessageBytes includes the complete JSON-RPC identity/envelope.
const MaxLogicalMessageBytes = MaxPayloadBytes + (4 << 10)
const defaultLogicalBytes = MaxLogicalMessageBytes
const defaultChunkBytes = 256 << 10

type transportAdvertisement struct {
	Version int `json:"version"`
}
type transportLimits struct {
	Version       int `json:"version"`
	FrameBytes    int `json:"frame_bytes"`
	LogicalBytes  int `json:"logical_bytes"`
	HostSendBytes int `json:"host_send_bytes"`
	HostReadBytes int `json:"host_read_bytes"`
	ChunkBytes    int `json:"chunk_bytes"`
	MessageChunks int `json:"message_chunks"`
	SessionChunks int `json:"session_chunks"`
	Transfers     int `json:"transfers"`
}

func (v transportLimits) valid() error {
	if v.Version != 1 || v.FrameBytes < 256 || v.FrameBytes > 1<<20 || v.LogicalBytes < v.FrameBytes || v.LogicalBytes > defaultLogicalBytes || v.HostSendBytes < v.FrameBytes || v.HostSendBytes > defaultWireBytes || v.HostReadBytes < v.FrameBytes || v.HostReadBytes > defaultWireBytes || v.ChunkBytes < 1 || v.ChunkBytes > defaultChunkBytes || v.MessageChunks < 1 || v.MessageChunks > 32768 || v.SessionChunks < 1 || v.SessionChunks > 65536 || v.Transfers < 1 || v.Transfers > 260 {
		return failure("invalid_transport_limits")
	}
	return nil
}

type transportChunk struct {
	TransferID  string `json:"transfer_id"`
	LogicalID   string `json:"logical_id"`
	LogicalType string `json:"logical_type"`
	Sequence    int    `json:"sequence"`
	TotalBytes  int    `json:"total_bytes"`
	Last        bool   `json:"last"`
	Data        []byte `json:"data"`
}
type transportACK struct {
	TransferID string `json:"transfer_id"`
	Sequence   int    `json:"sequence"`
}
type chunkAssembly struct {
	transferID, logicalID, logicalType string
	totalBytes, nextSequence           int
	raw                                []byte
}

func randomID() (string, error) {
	var n [16]byte
	if _, err := rand.Read(n[:]); err != nil {
		return "", failure("request_failed")
	}
	return "t" + hex.EncodeToString(n[:]), nil
}
func (s *wireSession) abort() {
	s.abortOnce.Do(func() {
		s.aborted.Store(true)
		if s.closeIO != nil {
			s.closeIO()
		}
	})
}
func (s *wireSession) enable(limits transportLimits) {
	s.enabled, s.limits = true, limits
	s.maxFrame = limits.FrameBytes
	s.maxInput, s.maxOutput = limits.HostSendBytes, limits.HostReadBytes
	if s.role == "worker" {
		s.maxInput, s.maxOutput = limits.HostReadBytes, limits.HostSendBytes
	}
	s.seenTransfers = map[string]bool{}
}
func (s *wireSession) writePhysical(frame Frame) error {
	if s.aborted.Load() || s.ctx.Err() != nil {
		return failure("execution_canceled")
	}
	raw, err := frame.MarshalJSON()
	if err != nil || len(raw)+1 > s.maxFrame {
		return failure("frame_limit")
	}
	return s.writeEncodedPhysical(raw)
}

// writeEncodedPhysical accepts only output from Frame.MarshalJSON after the
// caller has checked cancellation and the physical frame ceiling.
func (s *wireSession) writeEncodedPhysical(raw []byte) error {
	if s.maxInput == 0 {
		s.maxInput = defaultWireBytes
	}
	if len(raw)+1 > s.maxInput-s.inputBytes {
		return failure("input_limit")
	}
	raw = append(raw, '\n')
	n, err := s.writer.Write(raw)
	s.inputBytes += n
	if err != nil || n != len(raw) {
		return failure("worker_io_failed")
	}
	return nil
}
func (s *wireSession) readPhysical() (Frame, error) {
	var frame Frame
	var raw []byte
	if s.aborted.Load() || s.ctx.Err() != nil {
		return frame, failure("execution_canceled")
	}
	for {
		part, err := s.reader.ReadSlice('\n')
		s.outputBytes += len(part)
		if len(raw)+len(part) > s.maxFrame || s.outputBytes > s.maxOutput {
			return frame, failure("output_limit")
		}
		raw = append(raw, part...)
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if err != nil {
			return frame, failure("worker_io_failed")
		}
		break
	}
	if s.aborted.Load() || s.ctx.Err() != nil {
		return frame, failure("execution_canceled")
	}
	if decodeTransportFrame(raw, &frame) != nil {
		return frame, failure("invalid_frame")
	}
	return frame, nil
}
func (s *wireSession) write(frame Frame) error {
	valueLimit := s.maxFrame
	if s.enabled {
		valueLimit = s.limits.LogicalBytes
	}
	if len(frame.Payload) > valueLimit || len(frame.Result) > valueLimit {
		if s.enabled {
			return failure("message_limit")
		}
		return failure("frame_limit")
	}
	raw, err := frame.MarshalJSON()
	if err != nil {
		return failure("invalid_frame")
	}
	if s.enabled && len(raw) > s.limits.LogicalBytes {
		return failure("message_limit")
	}
	if len(raw)+1 <= s.maxFrame {
		if s.aborted.Load() || s.ctx.Err() != nil {
			return failure("execution_canceled")
		}
		return s.writeEncodedPhysical(raw)
	}
	if !s.enabled {
		return failure("frame_limit")
	}
	transfer, err := randomID()
	if err != nil {
		return err
	}
	controlType, ackType := "call", "result"
	if s.role == "worker" {
		controlType, ackType = "host_call", "host_result"
	}
	for offset, seq := 0, 0; offset < len(raw); seq++ {
		if seq >= s.limits.MessageChunks || s.sentChunks >= s.limits.SessionChunks {
			return failure("transport_chunk_limit")
		}
		id, err := randomID()
		if err != nil {
			return err
		}
		// Base64 is the only data-dependent expansion. Size a tiny empty
		// control first, rather than allocating a 256 KiB candidate for every
		// small negotiated frame. false is one byte larger than true.
		chunk := transportChunk{transfer, frame.ID, frame.Type, seq, len(raw), false, []byte{}}
		payload, _ := json.Marshal(chunk)
		control := Frame{Protocol: ProtocolVersion, ID: id, Type: controlType, Identity: s.identity, Method: "transport.chunk", Payload: payload}
		empty, err := control.MarshalJSON()
		if err != nil {
			return failure("invalid_frame")
		}
		available := s.maxFrame - len(empty) - 1
		if available < 4 {
			return failure("frame_limit")
		}
		n := min(s.limits.ChunkBytes, len(raw)-offset, (available/4)*3)
		chunk.Data, chunk.Last = raw[offset:offset+n], offset+n == len(raw)
		control.Payload, _ = json.Marshal(chunk)
		if err = s.writePhysical(control); err != nil {
			return err
		}
		s.sentChunks++
		ack, err := s.readPhysical()
		if err != nil {
			return err
		}
		var value transportACK
		if ack.Protocol != ProtocolVersion || ack.Identity != s.identity || ack.ID != id || ack.Type != ackType || ack.Error != nil || ack.Method != "" || ack.Payload != nil || strictJSON(ack.Result, &value) != nil || value.TransferID != transfer || value.Sequence != seq {
			return failure("invalid_transport_ack")
		}
		offset += n
	}
	return nil
}
func (s *wireSession) read() (Frame, error) {
	for {
		frame, err := s.readPhysical()
		if err != nil {
			var local *Error
			if s.assembly != nil && errors.As(err, &local) && local.Code == "worker_io_failed" {
				return Frame{}, failure("incomplete_transport_message")
			}
			return frame, err
		}
		if frame.Method != "transport.chunk" {
			if s.assembly != nil {
				return Frame{}, failure("incomplete_transport_message")
			}
			return frame, nil
		}
		if !s.enabled {
			return Frame{}, failure("transport_not_negotiated")
		}
		ack, complete, err := s.acceptChunk(frame)
		if err != nil {
			return Frame{}, err
		}
		if err = s.writePhysical(ack); err != nil {
			return Frame{}, err
		}
		if complete != nil {
			return *complete, nil
		}
	}
}
func (s *wireSession) acceptChunk(control Frame) (Frame, *Frame, error) {
	var empty Frame
	controlType, ackType := "host_call", "host_result"
	if s.role == "worker" {
		controlType, ackType = "call", "result"
	}
	if control.Protocol != ProtocolVersion || control.Identity != s.identity || control.Type != controlType || control.Result != nil || control.Error != nil {
		return empty, nil, failure("identity_mismatch")
	}
	var c transportChunk
	if strictJSON(control.Payload, &c) != nil || !identityPattern.MatchString(c.TransferID) || c.LogicalID == "" || len(c.LogicalID) > 128 || c.LogicalID == control.ID || c.Sequence < 0 || c.TotalBytes < 1 || c.TotalBytes > s.limits.LogicalBytes || len(c.Data) < 1 || len(c.Data) > s.limits.ChunkBytes {
		return empty, nil, failure("invalid_transport_chunk")
	}
	if s.role == "host" && c.LogicalType != "host_call" && c.LogicalType != "result" || s.role == "worker" && c.LogicalType != "call" && c.LogicalType != "host_result" {
		return empty, nil, failure("invalid_transport_chunk")
	}
	s.readChunks++
	if s.readChunks > s.limits.SessionChunks || c.Sequence >= s.limits.MessageChunks {
		return empty, nil, failure("transport_chunk_limit")
	}
	if s.assembly == nil {
		if c.Sequence != 0 || s.seenTransfers[c.TransferID] || len(s.seenTransfers) >= s.limits.Transfers {
			return empty, nil, failure("invalid_transport_sequence")
		}
		s.seenTransfers[c.TransferID] = true
		s.assembly = &chunkAssembly{transferID: c.TransferID, logicalID: c.LogicalID, logicalType: c.LogicalType, totalBytes: c.TotalBytes, raw: make([]byte, 0, min(c.TotalBytes, len(c.Data)))}
	}
	a := s.assembly
	if a.transferID != c.TransferID || a.logicalID != c.LogicalID || a.logicalType != c.LogicalType || a.totalBytes != c.TotalBytes || a.nextSequence != c.Sequence || len(c.Data) > a.totalBytes-len(a.raw) {
		return empty, nil, failure("invalid_transport_sequence")
	}
	a.raw = append(a.raw, c.Data...)
	a.nextSequence++
	if c.Last != (len(a.raw) == a.totalBytes) {
		return empty, nil, failure("invalid_transport_length")
	}
	var result *Frame
	if c.Last {
		var original Frame
		if decodeTransportFrame(a.raw, &original) != nil || original.Protocol != ProtocolVersion || original.Identity != s.identity || original.ID != a.logicalID || original.Type != a.logicalType || strings.HasPrefix(original.Method, "transport.") {
			return empty, nil, failure("invalid_transport_message")
		}
		result = &original
		s.assembly = nil
	}
	value, _ := json.Marshal(transportACK{c.TransferID, c.Sequence})
	return Frame{Protocol: ProtocolVersion, ID: control.ID, Type: ackType, Identity: s.identity, Result: value}, result, nil
}

// A rejected whole reply can report a fixed transport code only before any of
// its bytes were written. Once a transfer starts, an ordinary frame would be an
// invalid interruption; the receiver must retain the ambiguous failure.
func (s *wireSession) writeWorkerReply(frame Frame) error {
	before := s.inputBytes
	err := s.write(frame)
	var local *Error
	if err == nil || s.inputBytes != before || !errors.As(err, &local) {
		return err
	}
	var code string
	switch local.Code {
	case "message_limit":
		code = "TRANSPORT_MESSAGE_LIMIT"
	case "frame_limit":
		code = "TRANSPORT_FRAME_LIMIT"
	case "input_limit":
		// SDK outgoing bytes are Runner output bytes; use the host-relative code.
		code = "TRANSPORT_OUTPUT_LIMIT"
	case "transport_chunk_limit":
		code = "TRANSPORT_CHUNK_LIMIT"
	default:
		return err
	}
	frame.Result, frame.Payload, frame.Method = nil, nil, ""
	frame.Error = &ReplyError{Code: code}
	// If the small error cannot fit the remaining physical/wire budget,
	// terminate; it is not safe to invent a successful delivery.
	if fallbackErr := s.writePhysical(frame); fallbackErr != nil {
		return fallbackErr
	}
	return err
}

func workerReplyFailure(code string) error {
	switch code {
	case "MODULE_RATE_LIMITED":
		return ErrModuleRateLimited
	case "TRANSPORT_MESSAGE_LIMIT":
		return failure("message_limit")
	case "TRANSPORT_FRAME_LIMIT":
		return failure("frame_limit")
	case "TRANSPORT_OUTPUT_LIMIT":
		return failure("output_limit")
	case "TRANSPORT_CHUNK_LIMIT":
		return failure("transport_chunk_limit")
	default:
		return failure("plugin_operation_failed")
	}
}

// Decode once on the successful hot path. The old outer Decoder can update
// frame before rejecting trailing data, so retain its exact failure behavior.
func decodeTransportFrame(raw []byte, frame *Frame) error {
	if err := frame.UnmarshalJSON(raw); err != nil {
		return strictJSON(raw, frame)
	}
	return nil
}
