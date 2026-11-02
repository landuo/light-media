package download

// Finite source Transmission.get_torrents transport. This reuses the existing
// configured endpoint, sanitized errors, session header and dispatch guard, not
// an old business-plugin owner. No caller can choose an RPC method or fields.
import (
	"context"
	"crypto/tls"
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"time"

	"light-media/internal/logging"
)

type TransmissionQueryClient struct {
	client      *Client
	initialized bool
}

func NewTransmissionQueryClient(config Config, guard func(context.Context) error) (*TransmissionQueryClient, error) {
	if config.Kind != "tr" && config.Kind != "transmission" {
		return nil, failure("transmission_query", "wrong_kind")
	}
	if guard == nil {
		return nil, failure("transmission_query", "missing_guard")
	}
	c, err := New(config)
	if err != nil {
		return nil, err
	}
	// requests' timeout=60 is connect/read-idle, not a 60-second total request.
	// Do not change the timeout or environment-proxy policy of old consumers.
	transport := &http.Transport{Proxy: nil, TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, ForceAttemptHTTP2: false, TLSHandshakeTimeout: 60 * time.Second, IdleConnTimeout: 90 * time.Second,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			conn, err := (&net.Dialer{Timeout: 60 * time.Second, KeepAlive: 30 * time.Second}).DialContext(ctx, network, address)
			if err != nil {
				return nil, err
			}
			return &transmissionQueryConnection{Conn: conn}, nil
		},
	}
	c.http = &http.Client{Transport: &iyuuGuard{next: logging.Transport(transport), guard: guard}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	c.trDialect = "legacy" // actual transmission-rpc 7.0.11 protocol, not arbitrary dialect/RPC
	return &TransmissionQueryClient{client: c}, nil
}

type transmissionQueryConnection struct{ net.Conn }

func (c *transmissionQueryConnection) Read(p []byte) (int, error) {
	if err := c.SetReadDeadline(time.Now().Add(60 * time.Second)); err != nil {
		return 0, err
	}
	return c.Conn.Read(p)
}
func (c *transmissionQueryConnection) Write(p []byte) (int, error) {
	if err := c.SetWriteDeadline(time.Now().Add(60 * time.Second)); err != nil {
		return 0, err
	}
	return c.Conn.Write(p)
}
func (c *TransmissionQueryClient) Close() { c.client.http.CloseIdleConnections() }

// ids is the already source-validated numeric/hash/recently-active selector.
// nil omits it. Empty source lists must be canonicalized to nil by the host.
func (q *TransmissionQueryClient) Torrents(ctx context.Context, ids json.RawMessage) (json.RawMessage, error) {
	c := q.client
	ctx = mutationReadOnly(ctx)
	if err := c.nativeLock(ctx); err != nil {
		return nil, err
	}
	defer c.mu.Unlock()
	if !q.initialized {
		if _, err := c.trRPC(ctx, "session_get", map[string]any{}, "legacy"); err != nil {
			return nil, err
		}
		q.initialized = true
	}
	args := map[string]any{"fields": nativeTRFields}
	if len(ids) > 0 {
		args["ids"] = ids
	}
	c.requestID++
	id := c.requestID
	payload, err := json.Marshal(map[string]any{"method": "torrent-get", "arguments": args, "tag": id})
	if err != nil {
		return nil, failure("transmission_query", "invalid_arguments")
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
		var response struct {
			Result    string                     `json:"result"`
			Tag       uint64                     `json:"tag"`
			Arguments map[string]json.RawMessage `json:"arguments"`
		}
		if err = decode(body, &response); err != nil {
			return nil, err
		}
		if response.Tag != id {
			return nil, failure("rpc", "response_id_mismatch")
		}
		if response.Result != "success" {
			return nil, failure("rpc", "rpc_rejected")
		}
		raw, ok := response.Arguments["torrents"]
		if !ok {
			return nil, failure("transmission_query", "missing_torrents")
		}
		if _, err = nativeObjectList(raw, "transmission_query"); err != nil {
			return nil, err
		}
		return raw, nil
	}
	return nil, failure("rpc", "session_handshake_limit")
}
