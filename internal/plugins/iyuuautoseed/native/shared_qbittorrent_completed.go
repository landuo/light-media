package download

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"light-media/internal/logging"
)

// QBittorrentCompletedClient exposes only the source completed info request.
// This qB-only private client follows the approved original constructor's
// VERIFY_WEBUI_CERTIFICATE=False. Authentication, timeout, redirect policy,
// response budget, lock and per-dispatch guard remain the existing behavior.
// The global transport, old IYUU/Transfer clients and TR are not changed.
type QBittorrentCompletedClient struct{ native *IYUUClient }

func NewQBittorrentCompletedClient(config Config, apiKey string, guard func(context.Context) error) (*QBittorrentCompletedClient, error) {
	if config.Kind != "qb" && config.Kind != "qbittorrent" {
		return nil, failure("qb_completed", "wrong_kind")
	}
	if guard == nil {
		return nil, failure("qb_completed", "missing_guard")
	}
	config.Kind = "qbittorrent"
	client, err := NewIYUUClient(config, IYUUOptions{APIKey: apiKey, Guard: guard})
	if err != nil {
		return nil, err
	}
	original, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return nil, failure("qb_completed", "unsupported_default_transport")
	}
	transport := original.Clone()
	if transport.TLSClientConfig == nil {
		transport.TLSClientConfig = &tls.Config{}
	}
	// Clone owns its TLS config and connection pool. Retain its existing proxy,
	// dialer and all other defaults; no direct fallback or global CA mutation.
	transport.TLSClientConfig.InsecureSkipVerify = true
	var rt http.RoundTripper = &mutationDispatchTransport{next: logging.Transport(transport)}
	if apiKey != "" {
		rt = &iyuuBearer{next: rt, origin: client.client.base.Scheme + "://" + client.client.base.Host, token: apiKey}
	}
	client.client.http.Transport = &iyuuGuard{next: rt, guard: guard}
	return &QBittorrentCompletedClient{native: client}, nil
}

func (q *QBittorrentCompletedClient) Close() { q.native.Close() }

// hashes is source SDK _list2string(ids, "|"): nil omits the form key, while
// pointer-to-empty sends hashes=. It is deliberately not an exact-hash grant.
// Tags and the completion predicate must run, in that order, in the host.
func (q *QBittorrentCompletedClient) Rows(ctx context.Context, hashes *string) (json.RawMessage, error) {
	if ctx == nil {
		return nil, failure("qb_completed", "invalid_context")
	}
	if hashes != nil && !utf8.ValidString(*hashes) {
		return nil, failure("qb_completed", "invalid_unicode")
	}
	c := q.native.client
	ctx = mutationReadOnly(ctx)
	if err := c.nativeLock(ctx); err != nil {
		return nil, err
	}
	defer c.mu.Unlock()
	form := url.Values{"filter": {"completed"}}
	if hashes != nil {
		form.Set("hashes", *hashes)
	}
	raw, err := c.qb(ctx, http.MethodPost, "torrents/info", form.Encode(), "application/x-www-form-urlencoded")
	if err != nil {
		return nil, err
	}
	return qbittorrentCompletedVendorRows(raw)
}

// qbittorrent-api 2026.6.0 _cast -> TorrentInfoList turns JSON null into
// a successful empty list (as do other falsey JSON values). Non-mapping
// ARRAY entries remain values until the
// source tag/completion phase accesses .get; do not reject them prematurely.
// Mapping entries become TorrentDictionary, whose sole relevant key adaptation
// renames reannounce to reannounce_in, overwriting an existing destination key.
func qbittorrentCompletedVendorRows(raw json.RawMessage) (json.RawMessage, error) {
	var rows []json.RawMessage
	if json.Unmarshal(raw, &rows) != nil {
		var ok bool
		rows, ok = qbittorrentCompletedJSONRoot(raw)
		if !ok {
			return nil, failure("qb_completed", "invalid_json_list")
		}
	}
	if rows == nil {
		rows = []json.RawMessage{}
	}
	for i, row := range rows {
		value := bytes.TrimSpace(row)
		if len(value) == 0 || value[0] != '{' {
			continue
		}
		var fields map[string]json.RawMessage
		if json.Unmarshal(value, &fields) != nil {
			return nil, failure("qb_completed", "invalid_json_object")
		}
		if reannounce, exists := fields["reannounce"]; exists {
			fields["reannounce_in"] = reannounce
			delete(fields, "reannounce")
			encoded, err := json.Marshal(fields)
			if err != nil {
				return nil, failure("qb_completed", "invalid_json_object")
			}
			rows[i] = encoded
		}
	}
	return json.Marshal(rows)
}

// The vendor iterates `list_entries or []`. A nonempty JSON string/dict
// yields non-Mapping values, so its very first .get fails in the later source
// phase. One non-Mapping sentinel suffices; never fabricate successful tasks.
// This is bounded JSON conversion only, not arbitrary Python construction.
func qbittorrentCompletedJSONRoot(raw json.RawMessage) ([]json.RawMessage, bool) {
	if !json.Valid(raw) {
		return nil, false
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var value any
	if d.Decode(&value) != nil {
		return nil, false
	}
	empty := []json.RawMessage{}
	nonMapping := []json.RawMessage{json.RawMessage(`null`)}
	switch value := value.(type) {
	case nil:
		return empty, true
	case bool:
		return empty, !value
	case string:
		if value == "" {
			return empty, true
		}
		return nonMapping, true
	case map[string]any:
		if len(value) == 0 {
			return empty, true
		}
		return nonMapping, true
	case json.Number:
		if !strings.ContainsAny(string(value), ".eE") {
			return empty, value == "0" || value == "-0"
		}
		number, _ := strconv.ParseFloat(string(value), 64)
		return empty, number == 0 // binary64 underflow matches source json.loads.
	}
	return nil, false
}
