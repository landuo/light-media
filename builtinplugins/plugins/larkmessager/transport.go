package larkmessager

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/textproto"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const apiBase = "https://open.larksuite.com/open-apis"
const endpointCapability = "network.lark.endpoint"

type provider struct {
	origin, appID, secret string
	client                *http.Client
	tokenMu               sync.Mutex
	token                 string
	expires               int64
}

func newProvider(origin, id, secret string) (*provider, error) {
	fixture := origin != apiBase
	if fixture {
		u, err := url.Parse(origin)
		if err != nil {
			return nil, ErrDenied
		}
		port, err := strconv.Atoi(u.Port())
		if err != nil || port < 1 || port > 65535 || origin != "http://127.0.0.1:"+strconv.Itoa(port)+"/open-apis" {
			return nil, ErrDenied
		}
	}
	transport := &http.Transport{Proxy: http.ProxyFromEnvironment,
		DialContext:         (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		TLSHandshakeTimeout: 10 * time.Second, IdleConnTimeout: 90 * time.Second}
	if fixture {
		transport.Proxy = nil
	}
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return &provider{origin: origin, appID: id, secret: secret, client: client}, nil
}

// The list action uses the saved credentials even while messaging is disabled.
// Its temporary provider shares the guarded HTTP transport and owns its token.
func (a *provider) withChatCredentials(id, secret string) Host {
	return &provider{origin: a.origin, appID: id, secret: secret, client: a.client}
}

func (a *provider) tokenFor(ctx context.Context) (string, error) {
	a.tokenMu.Lock()
	defer a.tokenMu.Unlock()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	now := time.Now().Unix()
	if a.token != "" && a.expires-now > 60 {
		return a.token, nil
	}
	r, err := a.request(ctx, "POST", "/auth/v3/tenant_access_token/internal", nil, Object{"app_id": a.appID, "app_secret": a.secret}, nil, "", 10*time.Second)
	if err != nil {
		return "", err
	}
	var d Object
	if r.Status < 200 || r.Status >= 400 || decode(r.Body, &d) != nil || !zero(d["code"]) {
		return "", ErrRejected
	}
	token, ok := d["tenant_access_token"].(string)
	if !ok || token == "" {
		return "", ErrRejected
	}
	expires := int64(7200)
	if number, ok := d["expire"].(json.Number); ok {
		expires, err = number.Int64()
		if err != nil {
			return "", ErrUnsupported
		}
	} else if _, ok := d["expire"]; ok {
		return "", ErrUnsupported
	}
	a.token, a.expires = token, now+expires
	return token, nil
}

func requestUUID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = (b[6] & 15) | 64
	b[8] = (b[8] & 63) | 128
	h := hex.EncodeToString(b[:])
	return h[:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:], nil
}

func (a *provider) API(ctx context.Context, r Request) (_out Response, _error error) {
	defer sourceErrors(&_error)
	call := currentInvocation(ctx)
	if call == nil {
		return Response{}, ErrDependency
	}
	if call.uncertain {
		return Response{}, ErrUnknown
	}
	switch r.Op {
	case "send":
		if len(r.IDs) != 2 || !includes([]string{"chat_id", "open_id", "user_id"}, r.IDs[1]) || r.IDs[0] == "" {
			return Response{}, ErrDenied
		}
	case "reply", "edit", "reaction_add":
		if len(r.IDs) != 1 || !safeSegment(r.IDs[0]) {
			return Response{}, ErrDenied
		}
	case "reaction_delete":
		if len(r.IDs) != 2 || !safeSegment(r.IDs[0]) || !safeSegment(r.IDs[1]) {
			return Response{}, ErrDenied
		}
	}
	method, path, timeout, write, err := route(r)
	if err != nil {
		return Response{}, err
	}
	payload, query := clone(r.Payload), clone(r.Query)
	if query == nil {
		query = Object{}
	}
	switch r.Op {
	case "send", "reply":
		payload["content"] = string(uuidJSON(payload["content"]))
		uuid, err := requestUUID()
		if err != nil {
			return Response{}, err
		}
		payload["uuid"] = uuid
	case "edit":
		payload["content"] = string(uuidJSON(payload["content"]))
	}
	if r.Op == "send" {
		query["receive_id_type"] = r.IDs[1]
	}
	if r.Op == "chats" {
		query = Object{"user_id_type": "open_id", "page_size": 100}
	}
	token, err := a.tokenFor(ctx)
	if err != nil {
		return Response{}, err
	}
	result, err := a.request(ctx, method, path, query, payload, r.Blob, token, timeout)
	if write && (err != nil || !acknowledgedWrite(r.Op, result)) {
		call.uncertain = true
		return Response{}, ErrUnknown
	}
	return result, err
}

func acknowledgedWrite(operation string, response Response) bool {
	status := response.Status
	if status < 200 || status >= 500 || status >= 300 && status < 400 || status == http.StatusRequestTimeout {
		return false
	}
	var envelope Object
	if decode(response.Body, &envelope) != nil {
		return false
	}
	number, ok := envelope["code"].(json.Number)
	if !ok {
		return false
	}
	code, err := number.Int64()
	if err != nil {
		return false
	}
	if code != 0 {
		return true
	}
	if status >= 400 {
		return false
	}
	data := obj(envelope["data"])
	switch operation {
	case "send", "reply":
		// The source permits a missing data member but requires an object
		// when present. A malformed receipt may follow a committed send.
		_, present := envelope["data"]
		return !present || data != nil
	case "reaction_add":
		id, _ := data["reaction_id"].(string)
		return safeSegment(id)
	case "upload_image":
		key, _ := data["image_key"].(string)
		return key != ""
	case "upload_file":
		key, _ := data["file_key"].(string)
		return key != ""
	}
	return true
}

func includes(v []string, s string) bool {
	for _, x := range v {
		if x == s {
			return true
		}
	}
	return false
}
func safeSegment(s string) bool {
	if s == "" || s == "." || s == ".." {
		return false
	}
	for _, r := range s {
		if !(r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
			return false
		}
	}
	return true
}
func uuidJSON(v any) []byte {
	var b bytes.Buffer
	e := json.NewEncoder(&b)
	e.SetEscapeHTML(false)
	_ = e.Encode(v)
	return bytes.TrimSuffix(b.Bytes(), []byte{'\n'})
}
func route(r Request) (method, path string, timeout time.Duration, write bool, err error) {
	timeout = 15 * time.Second
	id := func(n int) string {
		if n < len(r.IDs) {
			return r.IDs[n]
		}
		return ""
	}
	switch r.Op {
	case "send":
		return "POST", "/im/v1/messages", timeout, true, nil
	case "reply":
		return "POST", "/im/v1/messages/" + id(0) + "/reply", timeout, true, nil
	case "edit":
		return "PATCH", "/im/v1/messages/" + id(0), timeout, true, nil
	case "reaction_add":
		return "POST", "/im/v1/messages/" + id(0) + "/reactions", 10 * time.Second, true, nil
	case "reaction_delete":
		return "DELETE", "/im/v1/messages/" + id(0) + "/reactions/" + id(1), 10 * time.Second, true, nil
	case "upload_image":
		return "POST", "/im/v1/images", 30 * time.Second, true, nil
	case "upload_file":
		return "POST", "/im/v1/files", 60 * time.Second, true, nil
	case "user_batch":
		return "POST", "/contact/v3/users/batch_get_id", timeout, false, nil
	case "chats":
		return "GET", "/im/v1/chats", 10 * time.Second, false, nil
	}
	return "", "", 0, false, ErrDenied
}
func (a *provider) request(ctx context.Context, method, path string, query, payload Object, blob *Blob, token string, timeout time.Duration) (Response, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	u, e := url.Parse(a.origin + path)
	if e != nil {
		return Response{}, ErrDenied
	}
	q := u.Query()
	for k, v := range query {
		q.Set(k, str(v))
	}
	u.RawQuery = q.Encode()
	var body io.Reader
	contentType := "application/json"
	if blob != nil {
		var b bytes.Buffer
		w := multipart.NewWriter(&b)
		if path == "/im/v1/images" {
			keys := []string{}
			for k := range payload {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				if e = w.WriteField(k, str(payload[k])); e != nil {
					return Response{}, ErrUnsupported
				}
			}
		}
		name := "file"
		if path == "/im/v1/images" {
			name = "image"
		}
		h := make(textproto.MIMEHeader)
		filename := strings.NewReplacer("\\", "\\\\", "\"", "\\\"").Replace(blob.Name)
		if strings.ContainsAny(filename, "\r\n") {
			return Response{}, ErrUnsupported
		}
		h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="%s"; filename="%s"`, name, filename))
		h.Set("Content-Type", blob.MIME)
		part, e := w.CreatePart(h)
		if e != nil {
			return Response{}, e
		}
		_, _ = part.Write(blob.Data)
		_ = w.Close()
		body = &b
		contentType = w.FormDataContentType()
	} else if payload != nil {
		body = bytes.NewReader(uuidJSON(payload))
	}
	req, e := http.NewRequestWithContext(ctx, method, u.String(), body)
	if e != nil {
		return Response{}, ErrDenied
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if blob == nil || body != nil {
		req.Header.Set("Content-Type", contentType)
	}
	resp, e := a.client.Do(req)
	if e != nil {
		return Response{}, errors.New("Lark transport failed")
	}
	defer resp.Body.Close()
	data, e := io.ReadAll(io.LimitReader(resp.Body, 16*1024*1024+1))
	if e != nil || len(data) > 16*1024*1024 {
		return Response{}, ErrUnknown
	}
	headers := map[string]string{}
	if s := resp.Header.Get("Content-Type"); s != "" {
		headers["Content-Type"] = s
	}
	return Response{Status: resp.StatusCode, Header: headers, Body: data}, nil
}
