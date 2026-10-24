package plugins

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"strings"
	"testing"
)

func codecFrame() Frame {
	return Frame{Protocol: ProtocolVersion, ID: "logical", Type: "call", Identity: Identity{"Fixture", "Fixture", "1.2.3"}, Method: "invoke", Payload: json.RawMessage(`{"value":"<>&\u2028\u2029","nested":[1,true,null]}`)}
}

// Compare both acceptance and receiver contents, including rejected input.
func TestTransportCodecDecodeEquivalence(t *testing.T) {
	f := codecFrame()
	good, e := json.Marshal(f)
	if e != nil {
		t.Fatal(e)
	}
	cases := map[string][]byte{
		"valid": good, "whitespace": append([]byte(" \r\n\t"), append(bytes.Clone(good), []byte(" \r\n\t")...)...),
		"empty_object": []byte("{}"), "empty": nil, "null": []byte("null"), "array": []byte("[]"), "string": []byte(`"x"`), "boolean": []byte("true"), "number": []byte("1"),
		"truncated": good[:len(good)-1], "trailing_object": append(bytes.Clone(good), []byte(` {}`)...),
		"trailing_null": append(bytes.Clone(good), []byte(` null`)...), "trailing_garbage": append(bytes.Clone(good), '!'),
		"unknown_outer":        bytes.Replace(good, []byte(`"jsonrpc":`), []byte(`"unknown":1,"jsonrpc":`), 1),
		"unknown_value":        bytes.Replace(good, []byte(`"protocol":`), []byte(`"unknown":1,"protocol":`), 1),
		"bad_version":          bytes.Replace(good, []byte(`"2.0"`), []byte(`"1.0"`), 1),
		"null_params":          bytes.Replace(good, good[bytes.Index(good, []byte(`"params":`))+9:len(good)-1], []byte("null"), 1),
		"wrong_protocol":       bytes.Replace(good, []byte(`"protocol":1`), []byte(`"protocol":2`), 1),
		"wrong_identity":       bytes.Replace(good, []byte(`"instance_id":"Fixture"`), []byte(`"instance_id":"Other"`), 1),
		"empty_id":             bytes.Replace(good, []byte(`"logical"`), []byte(`""`), 1),
		"long_id":              bytes.Replace(good, []byte(`"logical"`), []byte(`"`+strings.Repeat("x", 129)+`"`), 1),
		"number_id":            bytes.Replace(good, []byte(`"logical"`), []byte(`123`), 1),
		"bad_method":           bytes.Replace(good, []byte(`plugin.invoke`), []byte(`alien.invoke`), 1),
		"duplicate_id":         bytes.Replace(good, []byte(`"id":"logical"`), []byte(`"id":"old","id":"logical"`), 1),
		"case_fold":            bytes.Replace(good, []byte(`"jsonrpc"`), []byte(`"JSONRPC"`), 1),
		"invalid_utf8_payload": bytes.Replace(good, []byte(`nested`), []byte{'n', 255}, 1),
		"nested_depth":         bytes.Replace(good, []byte(`[1,true,null]`), []byte(strings.Repeat("[", 10001)+"0"+strings.Repeat("]", 10001)), 1),
	}
	for _, kind := range []string{"call", "host_call", "result", "host_result"} {
		v := f
		v.Type = kind
		if strings.Contains(kind, "result") {
			v.Method = ""
			v.Result = v.Payload
			v.Payload = nil
		}
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		cases[kind] = raw
		if strings.Contains(kind, "result") {
			v.Result = nil
			v.Error = &ReplyError{Code: "FAILED"}
			raw, err = json.Marshal(v)
			if err != nil {
				t.Fatal(err)
			}
			cases[kind+"_error"] = raw
			cases[kind+"_error_unknown"] = bytes.Replace(raw, []byte(`"code":-32000`), []byte(`"unknown":0,"code":-32000`), 1)
			cases[kind+"_error_code"] = bytes.Replace(raw, []byte(`-32000`), []byte(`-1`), 1)
		}
	}
	// Identity/protocol checks belong to the transport/business boundary, not
	// this codec. Null params and duplicate/case-fold keys preserve old behavior.
	accepted := map[string]bool{}
	for _, name := range []string{"valid", "whitespace", "null_params", "wrong_protocol", "wrong_identity", "duplicate_id", "case_fold", "invalid_utf8_payload", "call", "host_call", "result", "host_result", "result_error", "host_result_error"} {
		accepted[name] = true
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			for _, seed := range []Frame{{}, f} {
				old, new := seed, seed
				oe := strictJSON(raw, &old)
				ne := decodeTransportFrame(raw, &new)
				if (oe == nil) != accepted[name] || (ne == nil) != accepted[name] {
					t.Fatalf("acceptance changed: expected %v, old %v, new %v", accepted[name], oe, ne)
				}
				if (oe == nil) != (ne == nil) || codeOf(oe) != codeOf(ne) || !reflect.DeepEqual(old, new) {
					t.Fatalf("old=%+v/%v new=%+v/%v", old, oe, new, ne)
				}
			}
		})
	}
	// This is the non-equivalence that requires the cold fallback, not a relaxed assertion.
	tail := append(bytes.Clone(good), []byte(" null")...)
	old, direct := Frame{}, Frame{}
	if strictJSON(tail, &old) == nil || direct.UnmarshalJSON(tail) == nil || old.ID != "logical" || direct.ID != "" {
		t.Fatal("receiver mutation regression premise changed")
	}
}

func TestTransportCodecMarshalEquivalence(t *testing.T) {
	base := codecFrame()
	cases := []struct {
		name   string
		mutate func(*Frame)
	}{
		{"call", func(*Frame) {}}, {"host_call", func(f *Frame) { f.Type = "host_call" }},
		{"nil_payload", func(f *Frame) { f.Payload = nil }}, {"null_payload", func(f *Frame) { f.Payload = []byte("null") }},
		{"whitespace_payload", func(f *Frame) { f.Payload = []byte(" \n { \"x\" : \"<>&\" } \t") }},
		{"literal_unicode", func(f *Frame) { f.Payload = []byte("{\"value\":\"\u2028\u2029\"}") }},
		{"invalid_payload", func(f *Frame) { f.Payload = []byte("{") }},
		{"trailing_payload", func(f *Frame) { f.Payload = []byte("{} {}") }},
		{"invalid_type", func(f *Frame) { f.Type = "other" }}, {"invalid_method", func(f *Frame) { f.Method = "!" }},
		{"empty_id", func(f *Frame) { f.ID = "" }}, {"long_id", func(f *Frame) { f.ID = strings.Repeat("x", 129) }},
		{"mixed_call", func(f *Frame) { f.Result = []byte("{}") }},
		{"result", func(f *Frame) { f.Type = "result"; f.Method = ""; f.Result = f.Payload; f.Payload = nil }},
		{"host_result", func(f *Frame) { f.Type = "host_result"; f.Method = ""; f.Result = []byte("null"); f.Payload = nil }},
		{"nil_result", func(f *Frame) { f.Type = "result"; f.Method = ""; f.Payload = nil }},
		{"error", func(f *Frame) {
			f.Type = "result"
			f.Method = ""
			f.Payload = nil
			f.Error = &ReplyError{Code: "FAILED"}
		}},
		{"error_result", func(f *Frame) {
			f.Type = "result"
			f.Method = ""
			f.Payload = nil
			f.Result = []byte("{}")
			f.Error = &ReplyError{Code: "FAILED"}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := base
			c.mutate(&f)
			old, oe := json.Marshal(f)
			new, ne := f.MarshalJSON()
			if (oe == nil) != (ne == nil) || !bytes.Equal(old, new) {
				t.Fatalf("marshal bytes/success differ: %v / %v", oe, ne)
			}
			if oe != nil {
				var wrapped *json.MarshalerError
				if !errors.As(oe, &wrapped) || codeOf(wrapped.Err) != codeOf(ne) {
					t.Fatalf("unexpected error mapping: %v / %v", oe, ne)
				}
			}
		})
	}
}

type codecShortWriter struct {
	n   int
	err error
}

func (w codecShortWriter) Write(p []byte) (int, error) { return min(w.n, len(p)), w.err }

func TestTransportCodecPhysicalEquivalence(t *testing.T) {
	f := codecFrame()
	raw, e := json.Marshal(f)
	if e != nil {
		t.Fatal(e)
	}
	for _, mode := range []string{"small", "exact_frame", "frame_over", "exact_budget", "budget_over", "prior_bytes", "default_budget", "canceled", "aborted", "short_write", "io_error", "invalid"} {
		t.Run(mode, func(t *testing.T) {
			var oldWire, newWire bytes.Buffer
			makeSession := func(w io.Writer) *wireSession {
				s := &wireSession{ctx: context.Background(), writer: w, maxFrame: 4096, maxInput: 8192}
				switch mode {
				case "exact_frame":
					s.maxFrame = len(raw) + 1
				case "frame_over":
					s.maxFrame = len(raw)
				case "exact_budget":
					s.maxInput = len(raw) + 1
				case "budget_over":
					s.maxInput = len(raw)
				case "prior_bytes":
					s.maxInput = len(raw) + 1
					s.inputBytes = 1
				case "default_budget":
					s.maxInput = 0
				case "canceled":
					ctx, cancel := context.WithCancel(context.Background())
					cancel()
					s.ctx = ctx
				case "aborted":
					s.aborted.Store(true)
				case "short_write":
					s.writer = codecShortWriter{3, nil}
				case "io_error":
					s.writer = codecShortWriter{2, io.ErrClosedPipe}
				}
				return s
			}
			frame := f
			if mode == "invalid" {
				frame.ID = ""
			}
			old, new := makeSession(&oldWire), makeSession(&newWire)
			oe := (*codecLegacySession)(old).writePhysical(frame)
			ne := new.writePhysical(frame)
			want := map[string]string{"small": "", "exact_frame": "", "exact_budget": "", "default_budget": "", "frame_over": "frame_limit", "budget_over": "input_limit", "prior_bytes": "input_limit", "canceled": "execution_canceled", "aborted": "execution_canceled", "short_write": "worker_io_failed", "io_error": "worker_io_failed", "invalid": "frame_limit"}
			codecAssertExpectedErrors(t, mode, want, oe, ne)
			if (oe == nil) != (ne == nil) || codeOf(oe) != codeOf(ne) || old.inputBytes != new.inputBytes || old.maxInput != new.maxInput || !bytes.Equal(oldWire.Bytes(), newWire.Bytes()) {
				t.Fatalf("physical mismatch old=%v/%d new=%v/%d", oe, old.inputBytes, ne, new.inputBytes)
			}
		})
	}
	for _, mode := range []string{"valid", "trailing", "unknown", "null", "eof", "exact_frame", "exact_output_budget", "frame_over", "budget_over", "prior_bytes", "canceled", "aborted"} {
		t.Run("read_"+mode, func(t *testing.T) {
			wire := append(bytes.Clone(raw), '\n')
			if mode == "trailing" {
				wire = append(bytes.Clone(raw), []byte(" null\n")...)
			}
			if mode == "unknown" {
				wire = []byte("{\"extra\":1}\n")
			}
			if mode == "null" {
				wire = []byte("null\n")
			}
			if mode == "eof" {
				wire = bytes.Clone(raw)
			}
			makeSession := func() *wireSession {
				s := &wireSession{ctx: context.Background(), reader: bufio.NewReaderSize(bytes.NewReader(wire), 16), maxFrame: 4096, maxOutput: 8192}
				switch mode {
				case "exact_output_budget":
					s.maxOutput = len(wire)
				case "exact_frame":
					s.maxFrame = len(wire)
				case "frame_over":
					s.maxFrame = len(wire) - 1
				case "budget_over":
					s.maxOutput = len(wire) - 1
				case "prior_bytes":
					s.maxOutput = len(wire)
					s.outputBytes = 1
				case "canceled":
					ctx, cancel := context.WithCancel(context.Background())
					cancel()
					s.ctx = ctx
				case "aborted":
					s.aborted.Store(true)
				}
				return s
			}
			old, new := makeSession(), makeSession()
			of, oe := (*codecLegacySession)(old).readPhysical()
			nf, ne := new.readPhysical()
			want := map[string]string{"valid": "", "exact_frame": "", "exact_output_budget": "", "trailing": "invalid_frame", "unknown": "invalid_frame", "null": "invalid_frame", "eof": "worker_io_failed", "frame_over": "output_limit", "budget_over": "output_limit", "prior_bytes": "output_limit", "canceled": "execution_canceled", "aborted": "execution_canceled"}
			codecAssertExpectedErrors(t, mode, want, oe, ne)
			if (oe == nil) != (ne == nil) || codeOf(oe) != codeOf(ne) || old.outputBytes != new.outputBytes || !reflect.DeepEqual(of, nf) {
				t.Fatalf("read mismatch: %v / %v", oe, ne)
			}
		})
	}
}

// Synchronous peer generates matching ACKs from each actual control ID. Each
// physical write must match the frozen encoder byte-for-byte before normalizing
// only the random transfer/request IDs for cross-run comparison.
type codecPeer struct {
	t       *testing.T
	replies bytes.Buffer
	wire    bytes.Buffer
	frames  []Frame
	ackMode string
}

func (p *codecPeer) Write(raw []byte) (int, error) {
	p.t.Helper()
	var f Frame
	if err := strictJSON(raw, &f); err != nil {
		p.t.Fatal(err)
	}
	expected, err := json.Marshal(f)
	if err != nil || !bytes.Equal(raw, append(expected, '\n')) {
		p.t.Fatal("physical bytes differ from frozen encoder", err)
	}
	p.wire.Write(raw)
	if f.Method == "transport.chunk" {
		var c transportChunk
		if strictJSON(f.Payload, &c) != nil {
			p.t.Fatal("bad chunk")
		}
		a := transportACK{c.TransferID, c.Sequence}
		if p.ackMode == "sequence" {
			a.Sequence++
		}
		result, _ := json.Marshal(a)
		typ := "result"
		if f.Type == "host_call" {
			typ = "host_result"
		}
		ack := Frame{Protocol: ProtocolVersion, ID: f.ID, Type: typ, Identity: f.Identity, Result: result}
		if p.ackMode == "identity" {
			ack.InstanceID = "Other"
		}
		if p.ackMode == "protocol" {
			ack.Protocol++
		}
		if p.ackMode == "id" {
			ack.ID = "wrong"
		}
		if p.ackMode == "type" {
			ack.Type = "result"
			if typ == "result" {
				ack.Type = "host_result"
			}
		}
		encoded, _ := json.Marshal(ack)
		p.replies.Write(encoded)
		p.replies.WriteByte('\n')
		f.ID = "CONTROL"
		c.TransferID = "TRANSFER"
		f.Payload, _ = json.Marshal(c)
	}
	p.frames = append(p.frames, f)
	return len(raw), nil
}

func TestTransportCodecWriteAndChunkEquivalence(t *testing.T) {
	for _, role := range []string{"host", "worker"} {
		for _, mode := range []string{"small", "small_canceled", "small_aborted", "small_canceled_invalid", "small_aborted_invalid", "large", "legacy_large", "logical_limit", "message_chunks", "session_chunks", "wire_limit", "canceled", "ack_sequence", "ack_identity", "ack_protocol", "ack_id", "ack_type"} {
			t.Run(role+"_"+mode, func(t *testing.T) {
				f := codecFrame()
				if role == "worker" {
					f.Type = "host_call"
				}
				if mode != "small" && !strings.HasPrefix(mode, "small_") {
					f.Payload = blobJSON(9000)
				}
				if strings.HasSuffix(mode, "_invalid") {
					f.ID = ""
				}
				makeSession := func() (*wireSession, *codecPeer) {
					p := &codecPeer{t: t, ackMode: strings.TrimPrefix(mode, "ack_")}
					s := &wireSession{ctx: context.Background(), role: role, identity: f.Identity, writer: p, reader: bufio.NewReaderSize(&p.replies, 16)}
					s.enable(transportLimits{1, 4096, 32 << 10, 1 << 20, 1 << 20, 1024, 100, 100, 8})
					switch mode {
					case "legacy_large":
						s.enabled = false
					case "logical_limit":
						s.limits.LogicalBytes = 4096
					case "message_chunks":
						s.limits.MessageChunks = 1
					case "session_chunks":
						s.limits.SessionChunks = 1
					case "wire_limit":
						s.maxInput = 4096
					case "small_aborted", "small_aborted_invalid":
						s.aborted.Store(true)
					case "canceled", "small_canceled", "small_canceled_invalid":
						ctx, cancel := context.WithCancel(context.Background())
						cancel()
						s.ctx = ctx
					}
					return s, p
				}
				old, op := makeSession()
				new, np := makeSession()
				oe := (*codecLegacySession)(old).write(f)
				ne := new.write(f)
				want := map[string]string{"small": "", "large": "", "small_canceled": "execution_canceled", "small_aborted": "execution_canceled", "small_canceled_invalid": "invalid_frame", "small_aborted_invalid": "invalid_frame", "legacy_large": "frame_limit", "logical_limit": "message_limit", "message_chunks": "transport_chunk_limit", "session_chunks": "transport_chunk_limit", "wire_limit": "input_limit", "canceled": "execution_canceled", "ack_sequence": "invalid_transport_ack", "ack_identity": "invalid_transport_ack", "ack_protocol": "invalid_transport_ack", "ack_id": "invalid_transport_ack", "ack_type": "invalid_transport_ack"}
				codecAssertExpectedErrors(t, mode, want, oe, ne)
				if (oe == nil) != (ne == nil) || codeOf(oe) != codeOf(ne) || old.inputBytes != new.inputBytes || old.outputBytes != new.outputBytes || old.sentChunks != new.sentChunks || !reflect.DeepEqual(op.frames, np.frames) {
					t.Fatalf("write mismatch %v / %v counts %d/%d", oe, ne, old.sentChunks, new.sentChunks)
				}
				if mode == "small" && !bytes.Equal(op.wire.Bytes(), np.wire.Bytes()) {
					t.Fatal("small physical wire changed")
				}
				if (mode == "large" || mode == "small") && ne != nil {
					t.Fatal(ne)
				}
			})
		}
	}
}

func TestTransportCodecAssemblyEquivalence(t *testing.T) {
	for _, mode := range []string{"valid", "trailing", "unknown", "null", "identity", "protocol", "id", "type", "reserved_method", "sequence", "length", "chunk_limit", "session_limit", "transfer_limit", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			f := Frame{Protocol: 1, ID: "logical", Type: "result", Identity: receiverForTest().identity, Result: blobJSON(1700)}
			switch mode {
			case "identity":
				f.InstanceID = "Other"
			case "protocol":
				f.Protocol = 2
			case "id":
				f.ID = "different"
			case "type":
				f.Type = "host_result"
			case "reserved_method":
				f.Type = "host_call"
				f.Method = "transport.other"
				f.Payload = f.Result
				f.Result = nil
			}
			raw, _ := json.Marshal(f)
			switch mode {
			case "trailing":
				raw = append(raw, []byte(" null")...)
			case "unknown":
				raw = bytes.Replace(raw, []byte(`"jsonrpc":`), []byte(`"unknown":1,"jsonrpc":`), 1)
			case "null":
				raw = []byte("null")
			}
			makeSession := func() *wireSession {
				s := receiverForTest()
				s.writer = io.Discard
				switch mode {
				case "chunk_limit":
					s.limits.MessageChunks = 1
				case "session_limit":
					s.limits.SessionChunks = 1
				case "transfer_limit":
					s.limits.Transfers = 0
				case "cancel":
					ctx, cancel := context.WithCancel(context.Background())
					cancel()
					s.ctx = ctx
				}
				return s
			}
			old, new := makeSession(), makeSession()
			var stream bytes.Buffer
			for offset, seq := 0, 0; offset < len(raw); seq++ {
				n := min(512, len(raw)-offset)
				c := chunkForTest(new, seq, raw[offset:offset+n], offset+n == len(raw), len(raw))
				if mode == "reserved_method" {
					var v transportChunk
					json.Unmarshal(c.Payload, &v)
					v.LogicalType = f.Type
					c.Payload, _ = json.Marshal(v)
				}
				if mode == "sequence" && seq == 1 {
					var v transportChunk
					json.Unmarshal(c.Payload, &v)
					v.Sequence++
					c.Payload, _ = json.Marshal(v)
				}
				if mode == "length" {
					var v transportChunk
					json.Unmarshal(c.Payload, &v)
					v.TotalBytes++
					c.Payload, _ = json.Marshal(v)
				}
				b, _ := json.Marshal(c)
				stream.Write(b)
				stream.WriteByte('\n')
				offset += n
			}
			old.reader = bufio.NewReader(bytes.NewReader(stream.Bytes()))
			new.reader = bufio.NewReader(bytes.NewReader(stream.Bytes()))
			of, oe := (*codecLegacySession)(old).read()
			nf, ne := new.read()
			want := map[string]string{"valid": "", "trailing": "invalid_transport_message", "unknown": "invalid_transport_message", "null": "invalid_transport_message", "identity": "invalid_transport_message", "protocol": "invalid_transport_message", "id": "invalid_transport_message", "type": "invalid_transport_message", "reserved_method": "invalid_transport_message", "sequence": "invalid_transport_sequence", "length": "invalid_transport_length", "chunk_limit": "transport_chunk_limit", "session_limit": "transport_chunk_limit", "transfer_limit": "invalid_transport_sequence", "cancel": "execution_canceled"}
			codecAssertExpectedErrors(t, mode, want, oe, ne)
			if (oe == nil) != (ne == nil) || codeOf(oe) != codeOf(ne) || !reflect.DeepEqual(of, nf) || old.readChunks != new.readChunks || old.inputBytes != new.inputBytes || old.outputBytes != new.outputBytes || !reflect.DeepEqual(old.assembly, new.assembly) || !reflect.DeepEqual(old.seenTransfers, new.seenTransfers) {
				t.Fatalf("assembly mismatch %v/%v chunks=%d/%d", oe, ne, old.readChunks, new.readChunks)
			}
			if mode == "valid" && ne != nil {
				t.Fatal(fmt.Sprint(ne))
			}
		})
	}
}

func TestTransportCodecFourKindsSmallAndChunked(t *testing.T) {
	for _, kind := range []string{"call", "host_result", "host_call", "result"} {
		for _, size := range []int{32, 9000} {
			t.Run(fmt.Sprintf("%s_%d", kind, size), func(t *testing.T) {
				f := codecFrame()
				f.Type = kind
				f.Payload = blobJSON(size)
				role, receiverRole := "host", "worker"
				if kind == "host_call" || kind == "result" {
					role, receiverRole = "worker", "host"
				}
				if strings.Contains(kind, "result") {
					f.Result = f.Payload
					f.Payload = nil
					f.Method = ""
				}
				makeSender := func() (*wireSession, *codecPeer) {
					p := &codecPeer{t: t}
					s := &wireSession{ctx: context.Background(), role: role, identity: f.Identity, writer: p, reader: bufio.NewReader(&p.replies)}
					s.enable(transportLimits{1, 4096, 32 << 10, 1 << 20, 1 << 20, 1024, 100, 100, 8})
					return s, p
				}
				os, op := makeSender()
				ns, np := makeSender()
				if err := (*codecLegacySession)(os).write(f); err != nil {
					t.Fatal(err)
				}
				if err := ns.write(f); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(op.frames, np.frames) || os.sentChunks != ns.sentChunks || os.inputBytes != ns.inputBytes || os.outputBytes != ns.outputBytes {
					t.Fatal("four-kind sender mismatch")
				}
				makeReceiver := func() (*wireSession, *bytes.Buffer) {
					var ack bytes.Buffer
					s := &wireSession{ctx: context.Background(), role: receiverRole, identity: f.Identity, writer: &ack, reader: bufio.NewReader(bytes.NewReader(np.wire.Bytes()))}
					s.enable(ns.limits)
					return s, &ack
				}
				old, oa := makeReceiver()
				next, na := makeReceiver()
				of, oe := (*codecLegacySession)(old).read()
				nf, ne := next.read()
				if oe != nil || ne != nil || !reflect.DeepEqual(of, nf) || !reflect.DeepEqual(f, nf) || !bytes.Equal(oa.Bytes(), na.Bytes()) || old.readChunks != next.readChunks || old.inputBytes != next.inputBytes || old.outputBytes != next.outputBytes {
					t.Fatalf("four-kind receiver mismatch %v/%v", oe, ne)
				}
				if size > 4096 && (ns.sentChunks == 0 || next.readChunks != ns.sentChunks) {
					t.Fatal("large message did not traverse chunks")
				}
				if size < 4096 && (ns.sentChunks != 0 || next.readChunks != 0) {
					t.Fatal("small message unexpectedly chunked")
				}
			})
		}
	}
}

func FuzzTransportCodecDecodeEquivalence(f *testing.F) {
	valid, _ := json.Marshal(codecFrame())
	for _, raw := range [][]byte{valid, append(bytes.Clone(valid), []byte(" null")...), []byte("null"), []byte("{}"), []byte(""), []byte("{\"unknown\":0}")} {
		f.Add(raw)
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		// Keep generated cases bounded; the production frame/logical limits are unchanged.
		if len(raw) > 1<<20 {
			t.Skip()
		}
		for _, seed := range []Frame{{}, codecFrame()} {
			old, next := seed, seed
			oe := strictJSON(raw, &old)
			ne := decodeTransportFrame(raw, &next)
			if (oe == nil) != (ne == nil) || codeOf(oe) != codeOf(ne) || !reflect.DeepEqual(old, next) {
				t.Fatal("decode acceptance, error, or receiver changed")
			}
		}
	})
}

// Assert nilness independently: codeOf intentionally returns an empty string
// for non-local errors and must never make an unexpected error look successful.
func codecAssertExpectedErrors(t *testing.T, name string, expected map[string]string, oldErr, newErr error) {
	t.Helper()
	want, ok := expected[name]
	if !ok {
		t.Fatalf("missing explicit expected outcome for %s", name)
	}
	for _, e := range []error{oldErr, newErr} {
		if want == "" {
			if e != nil {
				t.Fatalf("%s: expected success, got %v", name, e)
			}
			continue
		}
		var local *Error
		if e == nil || !errors.As(e, &local) || local.Code != want {
			t.Fatalf("%s: expected local %s, got %v", name, want, e)
		}
	}
	if (oldErr == nil) != (newErr == nil) {
		t.Fatalf("%s: old/new success mismatch", name)
	}
}

// Both paths intentionally share Frame's codec. Freeze that dependency so a
// future codec change cannot silently change both sides of the oracle together.
func TestTransportCodecOracleDependency(t *testing.T) {
	expected := map[string]string{
		"jsonrpc.go":              "ca173d11652bbf2ce186e134a3770ff54987a5631be09043c845d2c35545dc8b",
		"protocol_test.go":        "d9b98c64c75b33a7f42f3957920bd2ef50cb6407a8950190b614e66e8d66df70",
		"large_transport_test.go": "a8dd40b28fec000ba49c877ea051be134351c7fdf0d37b0284dfbd35ac566f94",
	}
	for name, want := range expected {
		raw, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if got := fmt.Sprintf("%x", sha256.Sum256(raw)); got != want {
			t.Fatalf("shared oracle dependency %s changed: sha256=%s; independently re-review and re-freeze, do not merely update this assertion", name, got)
		}
	}
	// protocol.go can carry the independent cancellation fix; freeze only these
	// oracle-relevant definitions at runtime and record its full preimage separately.
	if ProtocolVersion != 1 || identityPattern.String() != `^[A-Za-z][A-Za-z0-9_.-]{0,127}$` || methodPattern.String() != `^[a-z][a-z0-9_.]{0,95}$` {
		t.Fatal("shared protocol identity/method/version oracle changed")
	}
	e := failure("codec_oracle_probe")
	local, ok := e.(*Error)
	if !ok || local.Code != "codec_oracle_probe" || codeOf(e) != local.Code || codeOf(nil) != "" {
		t.Fatal("shared failure/error-code oracle changed")
	}
}
