package plugins

import (
	"bufio"
	"encoding/json"
	"errors"
	"strings"
)

// Frozen baseline transport methods: do not update alongside the candidate.
type codecLegacySession wireSession

func (s *codecLegacySession) writePhysical(frame Frame) error {
	if s.aborted.Load() || s.ctx.Err() != nil {
		return failure("execution_canceled")
	}
	raw, err := json.Marshal(frame)
	if err != nil || len(raw)+1 > s.maxFrame {
		return failure("frame_limit")
	}
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
func (s *codecLegacySession) readPhysical() (Frame, error) {
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
	if strictJSON(raw, &frame) != nil {
		return frame, failure("invalid_frame")
	}
	return frame, nil
}
func (s *codecLegacySession) write(frame Frame) error {
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
	raw, err := json.Marshal(frame)
	if err != nil {
		return failure("invalid_frame")
	}
	if s.enabled && len(raw) > s.limits.LogicalBytes {
		return failure("message_limit")
	}
	if len(raw)+1 <= s.maxFrame {
		return s.writePhysical(frame)
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
		empty, err := json.Marshal(control)
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
func (s *codecLegacySession) read() (Frame, error) {
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
func (s *codecLegacySession) acceptChunk(control Frame) (Frame, *Frame, error) {
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
		if strictJSON(a.raw, &original) != nil || original.Protocol != ProtocolVersion || original.Identity != s.identity || original.ID != a.logicalID || original.Type != a.logicalType || strings.HasPrefix(original.Method, "transport.") {
			return empty, nil, failure("invalid_transport_message")
		}
		result = &original
		s.assembly = nil
	}
	value, _ := json.Marshal(transportACK{c.TransferID, c.Sequence})
	return Frame{Protocol: ProtocolVersion, ID: control.ID, Type: ackType, Identity: s.identity, Result: value}, result, nil
}
