package download

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
)

// SourceConstructorConnected reproduces only the native wrapper's constructor
// authentication. It does not sync daemon preferences or retain a worker scope.
// Later SDK operations keep their existing stateless per-call transports.
func (c *IYUUClient) SourceConstructorConnected(ctx context.Context) error {
	if err := c.client.nativeLock(ctx); err != nil {
		return err
	}
	defer c.client.mu.Unlock()
	ctx = mutationReadOnly(ctx)
	if c.client.config.Kind == "qbittorrent" {
		method, endpoint, contentType := http.MethodGet, "app/version", ""
		var payload []byte
		if c.options.APIKey == "" {
			method, endpoint, contentType = http.MethodPost, "auth/login", "application/x-www-form-urlencoded"
			payload = []byte(url.Values{"username": {c.client.config.Username}, "password": {c.client.config.Password}}.Encode())
		}
		body, _, status, err := c.client.sourceQBReadRequest(ctx, method, c.client.qbURL(endpoint), payload, contentType, false)
		if err != nil {
			return err
		}
		if status >= http.StatusBadRequest {
			return statusError(status)
		}
		// Fixed qbittorrent-api accepts exact empty/Ok. bodies, with no SID check.
		if c.options.APIKey == "" && len(body) != 0 && string(body) != "Ok." {
			return failure("source_constructor", "authentication_failed")
		}
		return nil
	}
	// transmission-rpc7.0.11 constructor calls get_session with no fields or tag.
	payload := []byte(`{"method":"session-get","arguments":{}}`)
	c.client.trSession = "0"
	for attempt := 0; attempt < 3; attempt++ {
		body, headers, status, err := c.client.request(ctx, http.MethodPost, c.client.base, payload, "application/json", c.client.trSession)
		if err != nil {
			return err
		}
		if status == http.StatusConflict {
			session := headers.Get("X-Transmission-Session-Id")
			if session == "" {
				return failure("source_constructor", "invalid_session_header")
			}
			c.client.trSession = session
			continue
		}
		if status == http.StatusUnauthorized || status == http.StatusForbidden {
			return statusError(status)
		}
		var response struct {
			Result    string
			Arguments map[string]json.RawMessage
		}
		if err := decode(body, &response); err != nil {
			return err
		}
		if response.Result != "success" || response.Arguments == nil {
			return failure("source_constructor", "rpc_rejected")
		}
		// get_session sets these fields before get_torrent_arguments(rpc-version).
		if _, present := response.Arguments["version"]; !present {
			return failure("source_constructor", "invalid_session")
		}
		rpc := bytes.TrimSpace(response.Arguments["rpc-version"])
		// The source compares the RPC version numerically (Python bool is also
		// numeric); it does not require a nonempty/string server version.
		if len(rpc) == 0 || !(rpc[0] == '-' || rpc[0] >= '0' && rpc[0] <= '9' || bytes.Equal(rpc, []byte("true")) || bytes.Equal(rpc, []byte("false"))) {
			return failure("source_constructor", "invalid_session")
		}
		return nil
	}
	return failure("source_constructor", "session_handshake_limit")
}
