// Fixed torrent_get route adapted from copied native.go; only ids selection added.
package download

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
)

func (c *Client) iyuuTRTorrents(ctx context.Context, hashes []string) (json.RawMessage, error) {
	fields := append([]string(nil), nativeTRFields...)
	if c.trDialect == "jsonrpc" {
		for i, f := range fields {
			for modern, original := range nativeTRKeys {
				if f == original {
					fields[i] = modern
					break
				}
			}
		}
	}
	c.requestID++
	id := c.requestID
	args := map[string]any{"fields": fields}
	if hashes != nil {
		args["ids"] = hashes
	}
	var request any
	if c.trDialect == "legacy" {
		request = map[string]any{"method": "torrent-get", "arguments": args, "tag": id}
	} else {
		request = map[string]any{"jsonrpc": "2.0", "method": "torrent_get", "params": args, "id": id}
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return nil, failure("native_torrents", "invalid_arguments")
	}
	for attempt := 0; attempt < 3; attempt++ {
		body, header, status, err := c.request(ctx, http.MethodPost, c.base, payload, "application/json", c.trSession)
		if err != nil {
			return nil, err
		}
		if status == http.StatusConflict {
			session := header.Get("X-Transmission-Session-Id")
			if session == "" || len(session) > 1024 || strings.ContainsAny(session, "\r\n\x00") {
				return nil, failure("rpc", "invalid_session_header")
			}
			c.trSession = session
			continue
		}
		if status != http.StatusOK {
			return nil, statusError(status)
		}
		var envelope map[string]json.RawMessage
		if err = decode(body, &envelope); err != nil {
			return nil, err
		}
		var values map[string]json.RawMessage
		if c.trDialect == "legacy" {
			var tag uint64
			var result string
			if json.Unmarshal(envelope["tag"], &tag) != nil || tag != id {
				return nil, failure("rpc", "response_id_mismatch")
			}
			if json.Unmarshal(envelope["result"], &result) != nil || result != "success" {
				return nil, failure("rpc", "rpc_rejected")
			}
			if json.Unmarshal(envelope["arguments"], &values) != nil || values == nil {
				return nil, failure("rpc", "invalid_arguments_response")
			}
		} else {
			var protocol string
			var responseID uint64
			if json.Unmarshal(envelope["jsonrpc"], &protocol) != nil || protocol != "2.0" || json.Unmarshal(envelope["id"], &responseID) != nil || responseID != id {
				return nil, failure("rpc", "response_id_mismatch")
			}
			if e, ok := envelope["error"]; ok && string(e) != "null" {
				return nil, failure("rpc", "rpc_rejected")
			}
			if json.Unmarshal(envelope["result"], &values) != nil || values == nil {
				return nil, failure("rpc", "invalid_result")
			}
		}
		raw, ok := values["torrents"]
		if !ok {
			return nil, failure("native_torrents", "missing_torrents")
		}
		return append(json.RawMessage(nil), raw...), nil
	}
	return nil, failure("rpc", "session_handshake_limit")
}
