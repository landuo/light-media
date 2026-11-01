package plugins

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

// Frozen pre-optimization production helpers. These are the old implementation,
// not expectations computed with the new decoder. Only the five helper names
// and their references are renamed; shared typed schemas/strictJSON are unchanged.
func materialBeforeMaterialUniqueJSON(raw []byte) error {
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

func materialBeforeMaterialExactFields(raw []byte, names ...string) bool {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || len(fields) != len(names) {
		return false
	}
	for _, name := range names {
		if len(fields[name]) == 0 || bytes.Equal(bytes.TrimSpace(fields[name]), []byte("null")) {
			return false
		}
	}
	return true
}

func materialBeforeDecodeMaterialRequest(frame Frame) (materialRequest, error) {
	var r materialRequest
	if frame.Protocol != ProtocolVersion || frame.Type != "host_call" || frame.Method != materialMethod || frame.Result != nil || frame.Error != nil || len(frame.Payload) > 4*((MaterialFrameBytes+2)/3)+2048 || materialBeforeMaterialUniqueJSON(frame.Payload) != nil || !materialBeforeMaterialExactFields(frame.Payload, "grant", "operation", "offset", "data") || strictJSON(frame.Payload, &r) != nil {
		return r, failure("invalid_material_frame")
	}
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(frame.Payload, &fields)
	if !materialBeforeMaterialExactFields(fields["grant"], "contract", "token", "invocation", "chunk_bytes", "capability", "direction", "kind", "total", "sha256") || !validMaterialSpec(r.Grant.MaterialSpec) || r.Grant.Contract != MaterialContract || r.Grant.ChunkBytes < 1 || r.Grant.ChunkBytes > MaterialFrameBytes || len(r.Grant.Token) != 64 || frame.ID != r.Grant.Invocation+"."+r.Grant.Token || r.Offset < 0 || r.Data == nil || len(r.Data) > r.Grant.ChunkBytes || !materialBeforeMaterialCanonicalData(fields["data"], r.Data) {
		return r, failure("invalid_material_frame")
	}
	if raw, err := hex.DecodeString(r.Grant.Token); err != nil || hex.EncodeToString(raw) != r.Grant.Token {
		return r, failure("invalid_material_frame")
	}
	return r, nil
}

func materialBeforeMaterialAcceptReply(request Frame, reply Frame) (materialResponse, error) {
	var response materialResponse
	r, err := materialBeforeDecodeMaterialRequest(request)
	if err != nil {
		return response, err
	}
	if reply.Protocol != ProtocolVersion || reply.Identity != request.Identity || reply.Type != "host_result" || reply.ID != request.ID || reply.Method != "" || reply.Payload != nil || reply.Error != nil || materialBeforeMaterialUniqueJSON(reply.Result) != nil || !materialBeforeMaterialExactFields(reply.Result, "token", "offset", "next", "data", "finished") || strictJSON(reply.Result, &response) != nil || response.Token != r.Grant.Token || response.Offset != r.Offset || response.Next < r.Offset || response.Next > r.Grant.Total || response.Data == nil {
		return response, failure("invalid_material_reply")
	}
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(reply.Result, &fields)
	if !materialBeforeMaterialCanonicalData(fields["data"], response.Data) {
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

func materialBeforeMaterialCanonicalData(raw json.RawMessage, data []byte) bool {
	var encoded string
	if json.Unmarshal(raw, &encoded) != nil {
		return false
	}
	decoded, err := base64.StdEncoding.Strict().DecodeString(encoded)
	return err == nil && bytes.Equal(decoded, data) && base64.StdEncoding.EncodeToString(data) == encoded
}

// Size the exact fixed-schema worst-case empty controls using the real physical
// bound. Offset/Next use Total, and ChunkBytes initially has its largest decimal
// width. Base64 data is the only growing field; smaller final widths only help.

func materialCodecFixture(t *testing.T, direction MaterialDirection, data []byte) (Frame, Frame) {
	t.Helper()
	capability := QBittorrentAddCapability
	operation := "write"
	requestData, replyData := data, []byte{}
	if direction == MaterialToWorker {
		capability = LocalTorrentBackupCapability
		operation = "read"
		requestData, replyData = []byte{}, data
	}
	grant := MaterialGrant{Contract: MaterialContract, Token: strings.Repeat("a", 64), Invocation: "callCodec", ChunkBytes: MaterialFrameBytes, MaterialSpec: MaterialSpec{Capability: capability, Direction: direction, Kind: "bytes", Total: int64(len(data)), SHA256: materialTestSHA(data)}}
	payload, err := json.Marshal(materialRequest{grant, operation, 0, requestData})
	if err != nil {
		t.Fatal(err)
	}
	frame := Frame{Protocol: ProtocolVersion, ID: grant.Invocation + "." + grant.Token, Type: "host_call", Identity: materialTestBinding().Identity, Method: materialMethod, Payload: payload}
	raw, err := json.Marshal(materialResponse{Token: grant.Token, Offset: 0, Next: int64(len(data)), Data: replyData})
	if err != nil {
		t.Fatal(err)
	}
	reply := Frame{Protocol: ProtocolVersion, ID: frame.ID, Type: "host_result", Identity: frame.Identity, Result: raw}
	return frame, reply
}
func materialCodecReplace(t *testing.T, raw []byte, from, to string) []byte {
	t.Helper()
	out := bytes.Replace(raw, []byte(from), []byte(to), 1)
	if bytes.Equal(out, raw) {
		t.Fatal("ineffective negative fixture", from)
	}
	return out
}
func materialCodecSameError(a, b error) bool {
	return a == nil && b == nil || a != nil && b != nil && reflect.TypeOf(a) == reflect.TypeOf(b) && a.Error() == b.Error()
}

func TestMaterialPacketOldDecoderEquivalence(t *testing.T) {
	request, reply := materialCodecFixture(t, MaterialToHost, []byte("AB"))
	for _, tc := range []struct{ name, from, to string }{
		{"unchanged", "", ""},
		{"duplicate_offset", `"offset":0`, `"offset":0,"offset":0`},
		{"escaped_duplicate_offset", `"offset":0`, `"offset":0,"off\u0073et":0`},
		{"duplicate_grant", `"token":"` + strings.Repeat("a", 64) + `"`, `"token":"` + strings.Repeat("a", 64) + `","token":"` + strings.Repeat("a", 64) + `"`},
		{"escaped_duplicate_grant", `"token":"` + strings.Repeat("a", 64) + `"`, `"token":"` + strings.Repeat("a", 64) + `","\u0074oken":"` + strings.Repeat("a", 64) + `"`},
		{"case_field", `"offset":0`, `"Offset":0`},
		{"escaped_field_same_semantics", `"offset":0`, `"off\u0073et":0`},
		{"null_offset", `"offset":0`, `"offset":null`},
		{"fraction_offset", `"offset":0`, `"offset":0.0`},
		{"exponent_offset", `"offset":0`, `"offset":0e0`},
		{"negative_offset", `"offset":0`, `"offset":-1`},
		{"max_int64_offset", `"offset":0`, `"offset":9223372036854775807`},
		{"overflow_int64_offset", `"offset":0`, `"offset":9223372036854775808`},
		{"unknown_field", `"offset":0`, `"offset":0,"extra":1`},
		{"unknown_operation_decode_not_dispatch", `"operation":"write"`, `"operation":"other"`},
		{"null_data", `"data":"QUI="`, `"data":null`},
		{"array_data", `"data":"QUI="`, `"data":[]`},
		{"noncanonical_padbits", `"data":"QUI="`, `"data":"QUJ="`},
		{"newline_base64", `"data":"QUI="`, `"data":"QU\nI="`},
		{"escaped_base64_same_semantics", `"data":"QUI="`, `"data":"\u0051UI="`},
		{"invalid_utf8", `"data":"QUI="`, string([]byte{'"', 'd', 'a', 't', 'a', '"', ':', '"', 255, '"'})},
		{"null_grant_field", `"total":2`, `"total":null`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := request
			if tc.from != "" {
				f.Payload = materialCodecReplace(t, request.Payload, tc.from, tc.to)
			}
			want, we := materialBeforeDecodeMaterialRequest(f)
			got, ge := decodeMaterialRequest(f)
			if !materialCodecSameError(we, ge) || we == nil && !reflect.DeepEqual(want, got) {
				t.Fatal("request acceptance/value changed", we, ge)
			}
			_, oldReplyErr := materialBeforeMaterialAcceptReply(f, reply)
			_, newReplyErr := materialAcceptReply(f, reply)
			if !materialCodecSameError(oldReplyErr, newReplyErr) {
				t.Fatal("invalid request reply gate changed", oldReplyErr, newReplyErr)
			}
		})
	}
	trailing := request
	trailing.Payload = append(bytes.Clone(request.Payload), []byte(`{}`)...)
	if _, a := materialBeforeDecodeMaterialRequest(trailing); a == nil {
		t.Fatal("old trailing fixture accepted")
	} else if _, b := decodeMaterialRequest(trailing); !materialCodecSameError(a, b) {
		t.Fatal("trailing changed", a, b)
	}
	for _, direction := range []MaterialDirection{MaterialToHost, MaterialToWorker} {
		f, r := materialCodecFixture(t, direction, bytes.Repeat([]byte("x"), MaterialFrameBytes))
		want, we := materialBeforeDecodeMaterialRequest(f)
		got, ge := decodeMaterialRequest(f)
		if !materialCodecSameError(we, ge) || !reflect.DeepEqual(want, got) {
			t.Fatal("bounded full page", we, ge)
		}
		wr, we := materialBeforeMaterialAcceptReply(f, r)
		gr, ge := materialAcceptReply(f, r)
		if !materialCodecSameError(we, ge) || !reflect.DeepEqual(wr, gr) {
			t.Fatal("read/write full reply", we, ge)
		}
	}
	read, readReply := materialCodecFixture(t, MaterialToWorker, []byte("AB"))
	for _, tc := range []struct{ name, from, to string }{
		{"duplicate_next", `"next":2`, `"next":2,"next":2`},
		{"case_next", `"next":2`, `"Next":2`},
		{"null_next", `"next":2`, `"next":null`},
		{"exponent_next", `"next":2`, `"next":2e0`},
		{"past_total", `"next":2`, `"next":3`},
		{"null_data", `"data":"QUI="`, `"data":null`},
		{"array_data", `"data":"QUI="`, `"data":[]`},
		{"noncanonical_data", `"data":"QUI="`, `"data":"QUJ="`},
		{"finish_on_read", `"finished":false`, `"finished":true`},
		{"unknown_field", `"next":2`, `"next":2,"extra":0`},
	} {
		t.Run("reply_"+tc.name, func(t *testing.T) {
			r := readReply
			r.Result = materialCodecReplace(t, r.Result, tc.from, tc.to)
			want, we := materialBeforeMaterialAcceptReply(read, r)
			got, ge := materialAcceptReply(read, r)
			if !materialCodecSameError(we, ge) || we == nil && !reflect.DeepEqual(want, got) {
				t.Fatal("reply acceptance changed", we, ge)
			}
		})
	}
	// Canonical helper checks remain equivalent independently of typed decoding.
	for _, raw := range []string{`null`, `""`, `"YQ=="`, `"YR=="`, `"YQ=\n="`, `"\u0059Q=="`, `[]`, `"é"`, `"YQ=="{}`} {
		for _, data := range [][]byte{nil, {}, []byte("a"), []byte("AB")} {
			if materialBeforeMaterialCanonicalData([]byte(raw), data) != materialCanonicalData([]byte(raw), data) {
				t.Fatal("canonical predicate changed", raw)
			}
		}
	}
}

func TestMaterialPreparedRequestOwnsImmutableMetadata(t *testing.T) {
	original, reply := materialCodecFixture(t, MaterialToHost, []byte("AB"))
	decoded, err := materialBeforeDecodeMaterialRequest(original)
	if err != nil {
		t.Fatal(err)
	}
	caller := bytes.Clone(decoded.Data)
	prepared, err := materialPreparePeerRequest(original.Identity, decoded.Grant.Invocation, decoded.Grant, decoded.Operation, decoded.Offset, caller)
	if err != nil {
		t.Fatal(err)
	}
	caller[0] = 'X'
	decoded.Grant.Token = strings.Repeat("b", 64)
	decoded.Operation = "other"
	if prepared.checked.Data != nil {
		t.Fatal("retained extra decoded material page")
	}
	actual, err := materialBeforeDecodeMaterialRequest(prepared.frame)
	if err != nil || string(actual.Data) != "AB" || actual.Grant.Token != strings.Repeat("a", 64) || actual.Operation != "write" {
		t.Fatal("caller mutation changed private request", err)
	}
	want, we := materialBeforeMaterialAcceptReply(original, reply)
	got, ge := materialAcceptPreparedReply(prepared, reply)
	if !materialCodecSameError(we, ge) || !reflect.DeepEqual(want, got) {
		t.Fatal("prepared reply validation changed", we, ge)
	}
	wrong := reply
	wrong.InstanceID = "OtherClone"
	_, we = materialBeforeMaterialAcceptReply(original, wrong)
	_, ge = materialAcceptPreparedReply(prepared, wrong)
	if we == nil || !materialCodecSameError(we, ge) {
		t.Fatal("prepared identity check relaxed", we, ge)
	}
}
