package download

// Fixed selected Add encoders only. They retain immutable []byte ownership;
// neither a worker-supplied Reader/path nor a generic streaming RPC is exposed.
import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"math"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"sort"
	"sync"

	"light-media/internal/logging"
)

type commandMaterialPayload struct {
	reader      io.Reader
	length      int64
	contentType string
}

func commandMaterialLength(parts ...int64) (int64, error) {
	var total int64
	for _, part := range parts {
		if part < 0 || part > math.MaxInt64-total {
			return 0, commandBoundary("material wire length overflow")
		}
		total += part
	}
	return total, nil
}
func commandMaterialBase64Length(size int64) (int64, error) {
	if size < 0 || size/3 > math.MaxInt64/4 {
		return 0, commandBoundary("material wire length overflow")
	}
	n := (size / 3) * 4
	if size%3 != 0 {
		return commandMaterialLength(n, 4)
	}
	return n, nil
}

func commandQBMaterialPayload(form url.Values, content []byte) (commandMaterialPayload, error) {
	// This native entrypoint can receive a caller-owned slice. The former
	// multipart encoding copied it here; retain one raw snapshot, not three
	// complete multipart Buffer/string/bytes encodings.
	content = bytes.Clone(content)
	// Buffer only fixed headers and ordinary scalar fields, never content bytes.
	var framing bytes.Buffer
	writer := multipart.NewWriter(&framing)
	keys := make([]string, 0, len(form))
	for key := range form {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if err := writer.WriteField(key, form.Get(key)); err != nil {
			return commandMaterialPayload{}, err
		}
	}
	if _, err := writer.CreatePart(textproto.MIMEHeader{"Content-Disposition": {`form-data; name="torrent__0"; filename="torrent__0"`}}); err != nil {
		return commandMaterialPayload{}, err
	}
	split := framing.Len()
	if err := writer.Close(); err != nil {
		return commandMaterialPayload{}, err
	}
	raw := framing.Bytes()
	length, err := commandMaterialLength(int64(len(raw)), int64(len(content)))
	if err != nil {
		return commandMaterialPayload{}, err
	}
	return commandMaterialPayload{reader: io.MultiReader(bytes.NewReader(raw[:split]), bytes.NewReader(content), bytes.NewReader(raw[split:])), length: length, contentType: writer.FormDataContentType()}, nil
}

// Pull encoder avoids an io.Pipe, producer goroutine and a full expanded base64
// string. Each fill encodes a multiple of three except the final padded block.
type commandMaterialBase64Reader struct {
	source     []byte
	offset     int
	encoded    [32 << 10]byte
	begin, end int
}

func (r *commandMaterialBase64Reader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if r.begin == r.end {
		if r.offset == len(r.source) {
			return 0, io.EOF
		}
		n := len(r.source) - r.offset
		if n > 24<<10 {
			n = 24 << 10
		}
		base64.StdEncoding.Encode(r.encoded[:], r.source[r.offset:r.offset+n])
		r.offset += n
		r.begin = 0
		r.end = base64.StdEncoding.EncodedLen(n)
	}
	n := copy(p, r.encoded[r.begin:r.end])
	r.begin += n
	return n, nil
}
func commandTRMaterialPayload(args map[string]any, content []byte) (commandMaterialPayload, error) {
	// TR Add already froze content before session initialization, at the same
	// stage where the former base64 EncodeToString made its snapshot.
	// Match encoding/json map-key order and its exact escaping for every existing
	// argument. Only the fixed metainfo string is streamed between two quotes.
	var framing bytes.Buffer
	framing.WriteString(`{"arguments":{`)
	keys := make([]string, 0, len(args)+1)
	for key := range args {
		if key == "metainfo" {
			return commandMaterialPayload{}, commandBoundary("duplicate material metainfo")
		}
		keys = append(keys, key)
	}
	keys = append(keys, "metainfo")
	sort.Strings(keys)
	split := 0
	for i, key := range keys {
		if i != 0 {
			framing.WriteByte(',')
		}
		encodedKey, _ := json.Marshal(key)
		framing.Write(encodedKey)
		framing.WriteByte(':')
		if key == "metainfo" {
			framing.WriteByte('"')
			split = framing.Len()
			framing.WriteByte('"')
		} else {
			value, err := json.Marshal(args[key])
			if err != nil {
				return commandMaterialPayload{}, commandBoundary("invalid fixed Transmission arguments")
			}
			framing.Write(value)
		}
	}
	framing.WriteString(`},"method":"torrent-add"}`)
	encoded, err := commandMaterialBase64Length(int64(len(content)))
	if err != nil {
		return commandMaterialPayload{}, err
	}
	raw := framing.Bytes()
	length, err := commandMaterialLength(int64(len(raw)), encoded)
	if err != nil {
		return commandMaterialPayload{}, err
	}
	return commandMaterialPayload{reader: io.MultiReader(bytes.NewReader(raw[:split]), &commandMaterialBase64Reader{source: content}, bytes.NewReader(raw[split:])), length: length, contentType: "application/json"}, nil
}

// Only bytes-backed finite readers reach this body. Close joins an active Read
// without any independent encoding goroutine. Retain the first context/guard
// error even when an HTTP peer sends an early response.
type commandMaterialBody struct {
	mu     sync.Mutex
	ctx    context.Context
	guard  func(context.Context) error
	reader io.Reader
	closed bool
	err    error
}

func (b *commandMaterialBody) check() error {
	if err := b.ctx.Err(); err != nil {
		return err
	}
	if err := b.guard(b.ctx); err != nil {
		mutationOwnerDenied(b.ctx, err)
		return err
	}
	return b.ctx.Err()
}
func (b *commandMaterialBody) Read(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return 0, io.ErrClosedPipe
	}
	if b.err != nil {
		return 0, b.err
	}
	if err := b.check(); err != nil {
		b.err = err
		return 0, err
	}
	// A single transport Read cannot become one unbounded memory copy.
	if len(p) > 256<<10 {
		p = p[:256<<10]
	}
	n, err := b.reader.Read(p)
	if checkErr := b.check(); checkErr != nil {
		b.err = checkErr
		return 0, checkErr
	}
	if err != nil && err != io.EOF {
		b.err = err
	}
	return n, err
}
func (b *commandMaterialBody) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.closed = true
	return nil
}
func (b *commandMaterialBody) failure() error { b.mu.Lock(); defer b.mu.Unlock(); return b.err }

// There is no endpoint parameter: only this owner's fixed qB torrents/add or
// TR base RPC URL. Generic Client.request and every other caller stay unchanged.
func (c *sharedDownloaderCommands) selectedMaterialRequest(ctx context.Context, payload commandMaterialPayload) ([]byte, http.Header, int, error) {
	ctx = mutationSelected(ctx)
	ctx, _ = logging.Target(ctx, "downloader")
	endpoint := c.iyuu.client.base
	if c.iyuu.client.config.Kind == "qbittorrent" {
		endpoint = c.iyuu.client.qbURL("torrents/add")
	}
	body := &commandMaterialBody{ctx: ctx, guard: c.iyuu.options.Guard, reader: payload.reader}
	defer body.Close()
	req, err := http.NewRequestWithContext(logging.EndpointContext(ctx, "integration", endpoint.String()), http.MethodPost, endpoint.String(), body)
	if err != nil {
		return nil, nil, 0, failure("request", "invalid_request")
	}
	req.ContentLength = payload.length
	req.GetBody = nil // No application-level or GetBody-based selected replay.
	req.Header.Set("Content-Type", payload.contentType)
	req.Header.Set("Accept", "application/json, text/plain")
	req.Header.Set("Referer", c.iyuu.client.base.Scheme+"://"+c.iyuu.client.base.Host+"/")
	if c.iyuu.client.config.Kind == "transmission" {
		req.SetBasicAuth(c.iyuu.client.config.Username, c.iyuu.client.config.Password)
		if c.iyuu.client.trSession != "" {
			req.Header.Set("X-Transmission-Session-Id", c.iyuu.client.trSession)
		}
	}
	res, err := c.iyuu.client.http.Do(req)
	// Transport may close Body asynchronously. Join our bounded Read before
	// inspecting its captured error; no material producer is left running.
	_ = body.Close()
	if bodyErr := body.failure(); bodyErr != nil {
		if res != nil {
			res.Body.Close()
		}
		return nil, nil, 0, bodyErr
	}
	if err != nil {
		if ctx.Err() != nil {
			return nil, nil, 0, ctx.Err()
		}
		return nil, nil, 0, failure("request", "transport_failed_outcome_unknown")
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, maxResponse+1))
	if err != nil {
		return nil, nil, res.StatusCode, failure("response", "body_read_failed_outcome_unknown")
	}
	if len(data) > maxResponse {
		return nil, nil, res.StatusCode, failure("response", "body_limit")
	}
	return data, res.Header, res.StatusCode, nil
}
func (q *sharedQBCommands) addMaterial(ctx context.Context, form url.Values, content []byte) ([]byte, error) {
	payload, err := commandQBMaterialPayload(form, content)
	if err != nil {
		return nil, err
	}
	c := q.owner.iyuu.client
	if !c.loggedIn && (c.config.Username != "" || c.config.Password != "") {
		if err = c.qbLogin(ctx); err != nil {
			return nil, err
		}
	}
	raw, _, status, err := q.owner.selectedMaterialRequest(ctx, payload)
	if err != nil {
		return nil, err
	}
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		c.loggedIn = false
	}
	if status != http.StatusOK {
		return nil, statusError(status)
	}
	return raw, nil
}
func (c *sharedDownloaderCommands) addTRMaterial(ctx context.Context, args map[string]any, content []byte) (map[string]any, error) {
	payload, err := commandTRMaterialPayload(args, content)
	if err != nil {
		return nil, err
	}
	raw, header, status, err := c.selectedMaterialRequest(ctx, payload)
	if err != nil {
		return nil, err
	}
	if session := header.Get("X-Transmission-Session-Id"); session != "" {
		if len(session) > 1024 || containsInvalidMaterialSession(session) {
			return nil, failure("rpc", "invalid_session_header")
		}
		c.iyuu.client.trSession = session
	}
	// Selected 409 is terminal. Only the unmodified trInit/session-get loop retries.
	if status != http.StatusOK {
		return nil, statusError(status)
	}
	var envelope map[string]any
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if d.Decode(&envelope) != nil || d.Decode(new(any)) != io.EOF {
		return nil, failure("rpc", "invalid_json")
	}
	if envelope["result"] != "success" {
		return nil, failure("rpc", "rpc_rejected")
	}
	result, ok := envelope["arguments"].(map[string]any)
	if !ok {
		return nil, failure("rpc", "invalid_arguments_response")
	}
	return result, nil
}
func containsInvalidMaterialSession(s string) bool {
	for _, c := range s {
		if c == '\r' || c == '\n' || c == 0 {
			return true
		}
	}
	return false
}
