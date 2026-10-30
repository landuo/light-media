package app

import (
	"bytes"
	"encoding/json"
	"io"
	"math/big"
	"unicode/utf8"
)

type sharedTransmissionQueryRequest struct {
	downloaderID string
	ids          json.RawMessage
	status, tags []string
	invalidIDs   bool
}

func decodeSharedTransmissionQuery(raw json.RawMessage) (out sharedTransmissionQueryRequest, err error) {
	invalid := func() (sharedTransmissionQueryRequest, error) {
		return out, bad("INVALID_PLUGIN_VALUE", "Transmission 查询必须为唯一字段的有效 Unicode 有界对象")
	}
	if len(raw) == 0 || len(raw) > 128<<10 || !utf8.Valid(raw) || !json.Valid(raw) || !sharedHistoryScalarEscapes(raw) {
		return invalid()
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	start, e := d.Token()
	if e != nil || start != json.Delim('{') {
		return invalid()
	}
	values := map[string]json.RawMessage{}
	for d.More() {
		token, e := d.Token()
		key, ok := token.(string)
		if e != nil || !ok || key != "downloader_id" && key != "ids" && key != "status" && key != "tags" {
			return invalid()
		}
		if _, ok := values[key]; ok {
			return invalid()
		}
		var value json.RawMessage
		if d.Decode(&value) != nil {
			return invalid()
		}
		values[key] = value
	}
	end, e := d.Token()
	if e != nil || end != json.Delim('}') {
		return invalid()
	}
	if _, e = d.Token(); e != io.EOF {
		return invalid()
	}
	if json.Unmarshal(values["downloader_id"], &out.downloaderID) != nil || len(out.downloaderID) == 0 || len(out.downloaderID) > 64 {
		return invalid()
	}
	for _, c := range out.downloaderID {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '.' || c == '-') {
			return invalid()
		}
	}
	if out.status, err = sharedTransmissionFilter(values["status"], false); err != nil {
		return out, err
	}
	if out.tags, err = sharedTransmissionFilter(values["tags"], true); err != nil {
		return out, err
	}
	out.ids, out.invalidIDs = sharedTransmissionIDs(values["ids"])
	return out, nil
}

// Actual transmission-rpc 7.0.11 _parse_torrent_ids: None and [] mean all;
// integer ID, lower-case 40-character SHA1, or scalar recently-active.
// Validation errors are caught by the original wrapper, not an empty success.
func sharedTransmissionIDs(raw json.RawMessage) (json.RawMessage, bool) {
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil, false
	}
	var value any
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if d.Decode(&value) != nil {
		return nil, true
	}
	if text, ok := value.(string); ok && text == "recently-active" {
		return raw, false
	}
	list, ok := value.([]any)
	if !ok {
		list = []any{value}
	}
	if len(list) == 0 {
		return nil, false
	}
	for _, item := range list {
		switch x := item.(type) {
		case bool: // Python bool is an int; retain native JSON behavior.
		case json.Number:
			n, ok := new(big.Int).SetString(string(x), 10)
			if !ok || n.Sign() < 0 {
				return nil, true
			}
		case string:
			if len(x) != 40 {
				return nil, true
			}
			for _, c := range x {
				if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
					return nil, true
				}
			}
		default:
			return nil, true
		}
	}
	encoded, e := json.Marshal(list)
	return encoded, e != nil
}
