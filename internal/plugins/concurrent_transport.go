package plugins

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"
	"sync"
)

// concurrentTransport has one physical reader and serializes entire outgoing
// logical messages. It never calls wireSession.read/write after negotiation:
// that legacy transport has synchronous ACKs and per-epoch mutable counters.
// Independent duplex progress is required when both sides send large values.
type concurrentTransport struct {
	wire     *wireSession
	ctx      context.Context
	writeMu  sync.Mutex
	transfer uint64
}
type concurrentWireCount struct{ bytes, chunks int }

type concurrentProposal struct {
	Version      int `json:"version"`
	MaxCalls     int `json:"max_calls"`
	MaxHostCalls int `json:"max_host_calls"`
}

const maxConcurrentCalls = 32

func (p concurrentProposal) valid() bool {
	return p.Version == 1 && p.MaxCalls >= 1 && p.MaxCalls <= maxConcurrentCalls && p.MaxHostCalls >= 1 && p.MaxHostCalls <= 256
}

func (t *concurrentTransport) write(frame Frame, charge func(concurrentWireCount) error) (count concurrentWireCount, err error) {
	t.writeMu.Lock()
	defer t.writeMu.Unlock()
	if len(frame.Payload) > t.wire.limits.LogicalBytes || len(frame.Result) > t.wire.limits.LogicalBytes {
		return concurrentWireCount{}, failure("message_limit")
	}
	raw, err := frame.MarshalJSON()
	if err != nil {
		return concurrentWireCount{}, failure("invalid_frame")
	}
	if len(raw) > t.wire.limits.LogicalBytes {
		return concurrentWireCount{}, failure("message_limit")
	}
	if len(raw)+1 <= t.wire.maxFrame {
		if charge != nil {
			if err := charge(concurrentWireCount{bytes: len(raw) + 1}); err != nil {
				return count, err
			}
		}
		return t.writeLine(raw)
	}
	t.transfer++
	transfer := "t" + strconv.FormatUint(t.transfer, 10)
	kind := "call"
	if t.wire.role == "worker" {
		kind = "host_call"
	}
	var lines [][]byte
	for offset, seq := 0, 0; offset < len(raw); seq++ {
		if seq >= t.wire.limits.MessageChunks {
			return concurrentWireCount{}, failure("transport_chunk_limit")
		}
		chunk := transportChunk{TransferID: transfer, LogicalID: frame.ID, LogicalType: frame.Type, Sequence: seq, TotalBytes: len(raw), Data: []byte{}}
		payload, _ := json.Marshal(chunk)
		control := Frame{Protocol: ProtocolVersion, ID: frame.ID, Type: kind, Identity: t.wire.identity, Method: "transport.concurrent_chunk", Payload: payload}
		empty, err := control.MarshalJSON()
		if err != nil {
			return concurrentWireCount{}, failure("invalid_frame")
		}
		available := t.wire.maxFrame - len(empty) - 1
		if available < 4 {
			return concurrentWireCount{}, failure("frame_limit")
		}
		n := min(t.wire.limits.ChunkBytes, len(raw)-offset, available/4*3)
		chunk.Data, chunk.Last = raw[offset:offset+n], offset+n == len(raw)
		control.Payload, _ = json.Marshal(chunk)
		encoded, err := control.MarshalJSON()
		if err != nil {
			return concurrentWireCount{}, failure("invalid_frame")
		}
		count.bytes += len(encoded) + 1
		count.chunks++
		if count.bytes > t.wire.maxInput {
			return concurrentWireCount{}, failure("input_limit")
		}
		if count.chunks > t.wire.limits.SessionChunks {
			return concurrentWireCount{}, failure("transport_chunk_limit")
		}
		lines = append(lines, encoded)
		offset += n
	}
	if charge != nil {
		if err := charge(count); err != nil {
			return concurrentWireCount{}, err
		}
	}
	var written concurrentWireCount
	for _, line := range lines {
		part, err := t.writeLine(line)
		written.bytes += part.bytes
		written.chunks++
		if err != nil {
			return written, err
		}
	}
	return written, nil
}

func (t *concurrentTransport) writeLine(raw []byte) (count concurrentWireCount, err error) {
	if t.ctx.Err() != nil || t.wire.aborted.Load() {
		return count, failure("execution_canceled")
	}
	if len(raw)+1 > t.wire.maxFrame {
		return count, failure("frame_limit")
	}
	raw = append(raw, '\n')
	n, err := t.wire.writer.Write(raw)
	count.bytes = n
	if err != nil || n != len(raw) {
		return count, failure("worker_io_failed")
	}
	return count, nil
}

func (t *concurrentTransport) readLine() (frame Frame, count concurrentWireCount, err error) {
	var raw []byte
	for {
		if t.ctx.Err() != nil || t.wire.aborted.Load() {
			return frame, count, failure("execution_canceled")
		}
		part, e := t.wire.reader.ReadSlice('\n')
		count.bytes += len(part)
		if len(raw)+len(part) > t.wire.maxFrame {
			return frame, count, failure("frame_limit")
		}
		raw = append(raw, part...)
		if errors.Is(e, bufio.ErrBufferFull) {
			continue
		}
		if e != nil {
			if errors.Is(e, io.EOF) && len(raw) == 0 { return frame, count, io.EOF }
			return frame, count, failure("worker_io_failed")
		}
		break
	}
	if decodeTransportFrame(raw, &frame) != nil {
		return frame, count, failure("invalid_frame")
	}
	if frame.Protocol != ProtocolVersion || frame.Identity != t.wire.identity {
		return frame, count, failure("identity_mismatch")
	}
	return frame, count, nil
}

func (t *concurrentTransport) read() (frame Frame, count concurrentWireCount, err error) {
	var assembly *chunkAssembly
	for {
		current, part, err := t.readLine()
		count.bytes += part.bytes
		if err != nil {
			if assembly != nil && errors.Is(err, io.EOF) { return frame, count, failure("incomplete_transport_message") }
			return frame, count, err
		}
		if count.bytes > t.wire.maxOutput {
			return frame, count, failure("output_limit")
		}
		if current.Method != "transport.concurrent_chunk" {
			if assembly != nil {
				return frame, count, failure("incomplete_transport_message")
			}
			raw, _ := current.MarshalJSON()
			if len(raw) > t.wire.limits.LogicalBytes {
				return frame, count, failure("message_limit")
			}
			return current, count, nil
		}
		controlType := "host_call"
		if t.wire.role == "worker" {
			controlType = "call"
		}
		if current.Type != controlType {
			return frame, count, failure("invalid_transport_chunk")
		}
		var c transportChunk
		if strictJSON(current.Payload, &c) != nil || !identityPattern.MatchString(c.TransferID) || c.LogicalID != current.ID || c.Sequence < 0 || c.TotalBytes < 1 || c.TotalBytes > t.wire.limits.LogicalBytes || len(c.Data) < 1 || len(c.Data) > t.wire.limits.ChunkBytes {
			return frame, count, failure("invalid_transport_chunk")
		}
		if t.wire.role == "host" && c.LogicalType != "host_call" && c.LogicalType != "result" || t.wire.role == "worker" && c.LogicalType != "call" && c.LogicalType != "host_result" {
			return frame, count, failure("invalid_transport_chunk")
		}
		count.chunks++
		if count.chunks > t.wire.limits.MessageChunks || count.chunks > t.wire.limits.SessionChunks {
			return frame, count, failure("transport_chunk_limit")
		}
		if assembly == nil {
			if c.Sequence != 0 {
				return frame, count, failure("invalid_transport_sequence")
			}
			assembly = &chunkAssembly{transferID: c.TransferID, logicalID: c.LogicalID, logicalType: c.LogicalType, totalBytes: c.TotalBytes}
		}
		a := assembly
		if a.transferID != c.TransferID || a.logicalID != c.LogicalID || a.logicalType != c.LogicalType || a.totalBytes != c.TotalBytes || a.nextSequence != c.Sequence || len(c.Data) > a.totalBytes-len(a.raw) {
			return frame, count, failure("invalid_transport_sequence")
		}
		a.raw = append(a.raw, c.Data...)
		a.nextSequence++
		if c.Last != (len(a.raw) == a.totalBytes) {
			return frame, count, failure("invalid_transport_length")
		}
		if c.Last {
			if decodeTransportFrame(a.raw, &frame) != nil || frame.Protocol != ProtocolVersion || frame.Identity != t.wire.identity || frame.ID != a.logicalID || frame.Type != a.logicalType || strings.HasPrefix(frame.Method, "transport.concurrent_chunk") {
				return Frame{}, count, failure("invalid_transport_message")
			}
			return frame, count, nil
		}
	}
}

// Accounting belongs to one invocation, never a resettable shared epoch.
type concurrentAccount struct {
	mu         sync.Mutex
	sent, read concurrentWireCount
	hostCalls  int
	limits     transportLimits
	worker     bool
}

func (a *concurrentAccount) send(n concurrentWireCount) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	maxBytes := a.limits.HostSendBytes
	if a.worker {
		maxBytes = a.limits.HostReadBytes
	}
	if n.bytes > maxBytes-a.sent.bytes {
		return failure("input_limit")
	}
	if n.chunks > a.limits.SessionChunks-a.sent.chunks {
		return failure("transport_chunk_limit")
	}
	a.sent.bytes += n.bytes
	a.sent.chunks += n.chunks
	return nil
}
func (a *concurrentAccount) receive(n concurrentWireCount) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	maxBytes := a.limits.HostReadBytes
	if a.worker {
		maxBytes = a.limits.HostSendBytes
	}
	if n.bytes > maxBytes-a.read.bytes {
		return failure("output_limit")
	}
	if n.chunks > a.limits.SessionChunks-a.read.chunks {
		return failure("transport_chunk_limit")
	}
	a.read.bytes += n.bytes
	a.read.chunks += n.chunks
	return nil
}
func (a *concurrentAccount) host(max int) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.hostCalls >= max {
		return failure("host_call_limit")
	}
	a.hostCalls++
	return nil
}
func (a *concurrentAccount) snapshot() RunResult {
	a.mu.Lock()
	defer a.mu.Unlock()
	return RunResult{SentWireBytes: a.sent.bytes, ReadWireBytes: a.read.bytes, SentChunks: a.sent.chunks, ReadChunks: a.read.chunks, HostCalls: a.hostCalls}
}
func concurrentRoot(id string) string { root, _, _ := strings.Cut(id, "."); return root }

func concurrentReplyError(err error) string {
	var local *Error
	if !errors.As(err, &local) {
		return ""
	}
	switch local.Code {
	case "message_limit":
		return "TRANSPORT_MESSAGE_LIMIT"
	case "frame_limit":
		return "TRANSPORT_FRAME_LIMIT"
	case "input_limit":
		return "TRANSPORT_OUTPUT_LIMIT"
	case "transport_chunk_limit":
		return "TRANSPORT_CHUNK_LIMIT"
	}
	return ""
}
