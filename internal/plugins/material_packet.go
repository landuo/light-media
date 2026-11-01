package plugins

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"unicode/utf8"
)

const materialMethod = "transport.material"

type materialRequest struct {
	Grant     MaterialGrant `json:"grant"`
	Operation string        `json:"operation"`
	Offset    int64         `json:"offset"`
	Data      []byte        `json:"data"`
}

type materialResponse struct {
	Token    string `json:"token"`
	Offset   int64  `json:"offset"`
	Next     int64  `json:"next"`
	Data     []byte `json:"data"`
	Finished bool   `json:"finished"`
}

// Material frames have a finite, fixed schema. Reject duplicate keys before
// decoding typed fields, including in the JSON-RPC/identity envelope. This is
// not a new decoder for arbitrary plugin values or an additional depth policy.
func materialUniqueJSON(raw []byte) error {
	if !utf8.Valid(raw) {
		return failure("invalid_material_frame")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var visit func(int) error
	visit = func(depth int) error {
		if depth > 8 {
			return failure("invalid_material_frame")
		}
		token, err := d.Token()
		if err != nil {
			return err
		}
		delim, ok := token.(json.Delim)
		if !ok {
			return nil
		}
		switch delim {
		case '{':
			seen := map[string]bool{}
			for d.More() {
				key, err := d.Token()
				if err != nil {
					return err
				}
				name, ok := key.(string)
				if !ok || seen[name] {
					return failure("invalid_material_frame")
				}
				seen[name] = true
				if err := visit(depth + 1); err != nil {
					return err
				}
			}
		case '[':
			return failure("invalid_material_frame") // Every byte field is base64, never an array.
		default:
			return failure("invalid_material_frame")
		}
		end, err := d.Token()
		if err != nil || end != json.Delim('}') {
			return failure("invalid_material_frame")
		}
		return nil
	}
	if err := visit(0); err != nil {
		return failure("invalid_material_frame")
	}
	if _, err := d.Token(); err != io.EOF {
		return failure("invalid_material_frame")
	}
	return nil
}

func materialExactFields(raw []byte, names ...string) bool {
	var fields map[string]json.RawMessage
	return json.Unmarshal(raw, &fields) == nil && materialExactFieldMap(fields, names...)
}

// Reuse the one locally decoded map; this does not validate duplicate keys.
// Every material packet still performs materialUniqueJSON before typed decode.
func materialExactFieldMap(fields map[string]json.RawMessage, names ...string) bool {
	if len(fields) != len(names) {
		return false
	}
	for _, name := range names {
		if len(fields[name]) == 0 || bytes.Equal(bytes.TrimSpace(fields[name]), []byte("null")) {
			return false
		}
	}
	return true
}

func decodeMaterialRequest(frame Frame) (materialRequest, error) {
	var r materialRequest
	var fields map[string]json.RawMessage
	if frame.Protocol != ProtocolVersion || frame.Type != "host_call" || frame.Method != materialMethod || frame.Result != nil || frame.Error != nil || len(frame.Payload) > 4*((MaterialFrameBytes+2)/3)+2048 || materialUniqueJSON(frame.Payload) != nil || json.Unmarshal(frame.Payload, &fields) != nil || !materialExactFieldMap(fields, "grant", "operation", "offset", "data") || strictJSON(frame.Payload, &r) != nil {
		return r, failure("invalid_material_frame")
	}
	if !materialExactFields(fields["grant"], "contract", "token", "invocation", "chunk_bytes", "capability", "direction", "kind", "total", "sha256") || !validMaterialSpec(r.Grant.MaterialSpec) || r.Grant.Contract != MaterialContract || r.Grant.ChunkBytes < 1 || r.Grant.ChunkBytes > MaterialFrameBytes || len(r.Grant.Token) != 64 || frame.ID != r.Grant.Invocation+"."+r.Grant.Token || r.Offset < 0 || r.Data == nil || len(r.Data) > r.Grant.ChunkBytes || !materialCanonicalData(fields["data"], r.Data) {
		return r, failure("invalid_material_frame")
	}
	if raw, err := hex.DecodeString(r.Grant.Token); err != nil || hex.EncodeToString(raw) != r.Grant.Token {
		return r, failure("invalid_material_frame")
	}
	return r, nil
}

func materialRequestFrame(identity Identity, grant MaterialGrant, op string, offset int64, data []byte) (Frame, error) {
	if data == nil {
		data = []byte{}
	}
	payload, err := json.Marshal(materialRequest{grant, op, offset, data})
	if err != nil {
		return Frame{}, err
	}
	f := Frame{Protocol: ProtocolVersion, ID: grant.Invocation + "." + grant.Token, Type: "host_call", Identity: identity, Method: materialMethod, Payload: payload}
	if _, err = decodeMaterialRequest(f); err != nil {
		return Frame{}, err
	}
	return f, nil
}

// handle authenticates the complete binding before any port call. The mutex
// serializes the original offset and one terminal Finish, not business calls.
// A failed port/post-guard is terminal and never makes a partial write replayable.
func (s *materialHostScope) handle(ctx context.Context, frame Frame) (Frame, error) {
	r, err := decodeMaterialRequest(frame)
	if err != nil {
		return Frame{}, err
	}
	g, err := s.lookup(ctx, frame.Identity, r.Grant)
	if err != nil {
		return Frame{}, err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if err := g.check(ctx, s); err != nil {
		return Frame{}, err
	}
	if r.Offset != g.next {
		return Frame{}, failure("material_offset_mismatch")
	}
	opctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(s.ctx, cancel)
	defer func() { stop(); cancel() }()
	response := materialResponse{Token: g.grant.Token, Offset: g.next, Next: g.next, Data: []byte{}}
	remaining := g.grant.Total - g.next // Both are nonnegative and next never exceeds Total.
	switch r.Operation {
	case "read":
		if g.grant.Direction != MaterialToWorker || len(r.Data) != 0 || remaining == 0 {
			return Frame{}, failure("invalid_material_operation")
		}
		n := int(min(int64(g.grant.ChunkBytes), remaining))
		response.Data, err = materialReadExact(opctx, g.port.Read, g.next, n)
		if err == nil {
			response.Data = bytes.Clone(response.Data)
		}
	case "write":
		if g.grant.Direction != MaterialToHost || remaining == 0 || int64(len(r.Data)) != min(int64(g.grant.ChunkBytes), remaining) {
			return Frame{}, failure("invalid_material_operation")
		}
		err = g.port.Write(opctx, g.next, bytes.Clone(r.Data))
	case "finish":
		if remaining != 0 || len(r.Data) != 0 {
			return Frame{}, failure("invalid_material_operation")
		}
		if hex.EncodeToString(g.digest.Sum(nil)) != g.grant.SHA256 {
			g.failed = true
			return Frame{}, failure("material_digest_mismatch")
		}
		response.Finished = true
	default:
		return Frame{}, failure("invalid_material_operation")
	}
	if err != nil {
		g.failed = true
		return Frame{}, err
	}
	if err := g.check(opctx, s); err != nil {
		g.failed = true
		return Frame{}, err
	}
	if r.Operation == "read" {
		_, _ = g.digest.Write(response.Data)
		g.next += int64(len(response.Data))
	}
	if r.Operation == "write" {
		_, _ = g.digest.Write(r.Data)
		g.next += int64(len(r.Data))
	}
	response.Next = g.next
	if response.Finished {
		g.finished = true
	}
	payload, err := json.Marshal(response)
	if err != nil {
		g.failed = true
		return Frame{}, err
	}
	return Frame{Protocol: ProtocolVersion, ID: frame.ID, Type: "host_result", Identity: frame.Identity, Result: payload}, nil
}

// A peer accepts only the matching outstanding bounded request. This is not a
// worker-side authority grant: the host still looks up its independently issued
// token and checks its current owner on every frame.
func materialAcceptReply(request Frame, reply Frame) (materialResponse, error) {
	r, err := decodeMaterialRequest(request)
	if err != nil {
		return materialResponse{}, err
	}
	return materialAcceptReplyDecoded(request, r, reply)
}

// Only a private locally generated request may reuse its decoded metadata.
// Incoming replies still undergo every original external-boundary check.
func materialAcceptPreparedReply(request materialPreparedRequest, reply Frame) (materialResponse, error) {
	return materialAcceptReplyDecoded(request.frame, request.checked, reply)
}

func materialAcceptReplyDecoded(request Frame, r materialRequest, reply Frame) (materialResponse, error) {
	var response materialResponse
	var fields map[string]json.RawMessage
	if reply.Protocol != ProtocolVersion || reply.Identity != request.Identity || reply.Type != "host_result" || reply.ID != request.ID || reply.Method != "" || reply.Payload != nil || reply.Error != nil || materialUniqueJSON(reply.Result) != nil || json.Unmarshal(reply.Result, &fields) != nil || !materialExactFieldMap(fields, "token", "offset", "next", "data", "finished") || strictJSON(reply.Result, &response) != nil || response.Token != r.Grant.Token || response.Offset != r.Offset || response.Next < r.Offset || response.Next > r.Grant.Total || response.Data == nil {
		return response, failure("invalid_material_reply")
	}
	if !materialCanonicalData(fields["data"], response.Data) {
		return response, failure("invalid_material_reply")
	}
	n := min(int64(r.Grant.ChunkBytes), r.Grant.Total-r.Offset)
	switch r.Operation {
	case "read":
		if r.Grant.Direction != MaterialToWorker || n <= 0 || int64(len(response.Data)) != n || response.Next != r.Offset+n || response.Finished {
			return response, failure("invalid_material_reply")
		}
	case "write":
		if r.Grant.Direction != MaterialToHost || n <= 0 || len(response.Data) != 0 || response.Next != r.Offset+n || response.Finished {
			return response, failure("invalid_material_reply")
		}
	case "finish":
		if r.Offset != r.Grant.Total || response.Next != r.Offset || len(response.Data) != 0 || !response.Finished {
			return response, failure("invalid_material_reply")
		}
	default:
		return response, failure("invalid_material_reply")
	}
	return response, nil
}

func materialCanonicalData(raw json.RawMessage, data []byte) bool {
	var encoded string
	if json.Unmarshal(raw, &encoded) != nil {
		return false
	}
	// Equality with the canonical encoder implies successful strict decoding
	// back to data. Keep exact string equality (including padding/no CRLF);
	// decoding the same bytes a second time adds no acceptance constraint.
	return base64.StdEncoding.EncodeToString(data) == encoded
}

// Size the exact fixed-schema worst-case empty controls using the real physical
// bound. Offset/Next use Total, and ChunkBytes initially has its largest decimal
// width. Base64 data is the only growing field; smaller final widths only help.
func materialGrantPageBytes(identity Identity, grant MaterialGrant, maxFrame int) (int, error) {
	request, _ := json.Marshal(materialRequest{Grant: grant, Operation: "finish", Offset: grant.Total, Data: []byte{}})
	reply, _ := json.Marshal(materialResponse{Token: grant.Token, Offset: grant.Total, Next: grant.Total, Data: []byte{}, Finished: false})
	id := grant.Invocation + "." + grant.Token
	in, err := (Frame{Protocol: ProtocolVersion, ID: id, Type: "host_call", Identity: identity, Method: materialMethod, Payload: request}).MarshalJSON()
	if err != nil {
		return 0, err
	}
	out, err := (Frame{Protocol: ProtocolVersion, ID: id, Type: "host_result", Identity: identity, Result: reply}).MarshalJSON()
	if err != nil {
		return 0, err
	}
	available := maxFrame - max(len(in), len(out)) - 1
	if available < 4 {
		return 0, failure("frame_limit")
	}
	return min(MaterialFrameBytes, (available/4)*3), nil
}
