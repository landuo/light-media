package larkmessager

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
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
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const APIBase = "https://open.larksuite.com/open-apis"

// Binding is created exclusively from trusted host config and frozen events.
// It must never be deserialized from a worker's invocation payload.
type Binding struct {
	Owner                string
	Generation           int64
	Revision             int64
	InvocationID         string
	AppID                string
	AppSecret            string
	Targets              map[string][]string
	Messages             []string
	Cards                []string
	Resources            map[string][]string
	ContactIdentifiers   []string
	RecipientIdentifiers []string
	ProfileUsers         []string
	Files                map[string]Blob
	Images               map[string]Blob
}
type Scope struct {
	Owner                string
	Generation, Revision int64
	InvocationID         string
}

type Options struct {
	Guard         func(context.Context, Scope) error
	Client        *http.Client
	Now           func() time.Time
	UUID          func() string
	TestAPIOrigin string
}
type Intent struct {
	ID                   string
	Owner                string
	Generation, Revision int64
	Digest               string
	Op                   string
}
type Receipt struct {
	Intent   Intent
	State    string
	Response Response
}

// Begin must durably reserve before IO. Existing unresolved intents must return
// ErrUnknown, never permission to resend. Finish atomically stores the receipt.
type Journal interface {
	Begin(context.Context, Intent) (*Receipt, error)
	Finish(context.Context, Receipt) error
}
type MemoryJournal struct {
	mu   sync.Mutex
	rows map[string]Receipt
}

func NewMemoryJournal() *MemoryJournal { return &MemoryJournal{rows: map[string]Receipt{}} }
func (j *MemoryJournal) Begin(ctx context.Context, i Intent) (*Receipt, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if r, ok := j.rows[i.ID]; ok {
		if r.Intent != i {
			return nil, ErrDenied
		}
		if r.State == "intent" || r.State == "unknown" {
			return nil, ErrUnknown
		}
		o := clone(r)
		return &o, nil
	}
	j.rows[i.ID] = Receipt{Intent: i, State: "intent"}
	return nil, nil
}
func (j *MemoryJournal) Finish(ctx context.Context, r Receipt) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	old, ok := j.rows[r.Intent.ID]
	if !ok || old.Intent != r.Intent || old.State != "intent" {
		return ErrDenied
	}
	j.rows[r.Intent.ID] = clone(r)
	return nil
}
func (j *MemoryJournal) Snapshot() map[string]Receipt {
	j.mu.Lock()
	defer j.mu.Unlock()
	return clone(j.rows)
}

// MemoryJournal is for isolated tests only; it is not crash durable.
type Adapter struct {
	binding   Binding
	client    *http.Client
	origin    string
	now       func() time.Time
	uuid      func() string
	journal   Journal
	sequence  atomic.Uint64
	uncertain atomic.Bool
	gate      chan struct{}
	tokenMu   sync.Mutex
	token     string
	expires   int64
	guard     func(context.Context, Scope) error
	grantMu   sync.RWMutex
	derived   map[string]map[string]bool
}

func NewAdapter(b Binding, j Journal, o Options) (*Adapter, error) {
	if b.Owner == "" || b.Generation <= 0 || b.Revision <= 0 || b.InvocationID == "" || b.AppID == "" || b.AppSecret == "" || j == nil {
		return nil, ErrDenied
	}
	if o.TestAPIOrigin == "" && o.Guard == nil {
		return nil, ErrDependency
	}
	origin := APIBase
	if o.TestAPIOrigin != "" {
		u, e := url.Parse(o.TestAPIOrigin)
		if e != nil || u == nil {
			return nil, ErrDenied
		}
		ip := net.ParseIP(u.Hostname())
		if e != nil || u.Scheme != "http" || ip == nil || !ip.IsLoopback() || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" {
			return nil, ErrDenied
		}
		origin = strings.TrimSuffix(o.TestAPIOrigin, "/") + "/open-apis"
	}
	client := http.Client{}
	if o.Client != nil {
		client = *o.Client
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if client.Transport == nil {
		client.Transport = http.DefaultTransport.(*http.Transport).Clone()
	}
	if t, ok := client.Transport.(*http.Transport); ok {
		if t.TLSClientConfig != nil && t.TLSClientConfig.InsecureSkipVerify {
			return nil, ErrDenied
		}
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.UUID == nil {
		o.UUID = func() string {
			b := make([]byte, 16)
			if _, e := rand.Read(b); e != nil {
				panic(e)
			}
			b[6] = (b[6] & 15) | 64
			b[8] = (b[8] & 63) | 128
			h := hex.EncodeToString(b)
			return h[:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
		}
	}
	return &Adapter{binding: clone(b), journal: j, client: &client, origin: origin, now: o.Now, uuid: o.UUID, guard: o.Guard, derived: map[string]map[string]bool{}, gate: make(chan struct{}, 1)}, nil
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
func (a *Adapter) authorize(r Request) error {
	b := a.binding
	a.grantMu.RLock()
	defer a.grantMu.RUnlock()
	has := func(kind, id string, base []string) bool { return includes(base, id) || a.derived[kind][id] }
	ids := r.IDs
	checkN := func(n int) bool { return len(ids) == n }
	message := func() bool { return len(ids) > 0 && has("message", ids[0], b.Messages) }
	card := func() bool { return len(ids) > 0 && has("card", ids[0], b.Cards) }
	switch r.Op {
	case "send":
		if !checkN(2) || !has("target:"+ids[1], ids[0], b.Targets[ids[1]]) || r.Payload["receive_id"] != ids[0] {
			return ErrDenied
		}
	case "reply", "edit", "reaction_add":
		if !checkN(1) || !message() {
			return ErrDenied
		}
	case "reaction_delete":
		if !checkN(2) || !message() || !has("reaction:"+ids[0], ids[1], b.Resources["reaction:"+ids[0]]) {
			return ErrDenied
		}
	case "card_create":
		if !checkN(0) {
			return ErrDenied
		}
	case "card_content":
		if !checkN(2) || !card() || (ids[1] != StreamBodyID && ids[1] != StreamTitleID) {
			return ErrDenied
		}
	case "card_close":
		if !checkN(1) || !card() {
			return ErrDenied
		}
	case "upload_image", "upload_file":
		if !checkN(0) || r.Blob == nil || !a.ownsBlob(*r.Blob) {
			return ErrDenied
		}
		wantMIME := "application/octet-stream"
		if r.Op == "upload_image" {
			mime, _, e := GuessMIME(r.Blob.Name)
			if e != nil {
				return ErrDenied
			}
			wantMIME = mime
			if !strings.HasPrefix(wantMIME, "image/") {
				wantMIME = "image/png"
			}
		}
		if r.Blob.MIME != wantMIME {
			return ErrDenied
		}
	case "image_get", "file_get":
		kind := "image"
		if r.Op == "file_get" {
			kind = "file"
		}
		if !checkN(1) || !has(kind, ids[0], b.Resources[kind]) {
			return ErrDenied
		}
	case "resource_get":
		if !checkN(2) || !message() || !includes(b.Resources["message:"+ids[0]+":"+text(r.Query["type"])], ids[1]) {
			return ErrDenied
		}
	case "user_get":
		if !checkN(1) || !includes(b.ProfileUsers, ids[0]) {
			return ErrDenied
		}
	case "user_batch":
		if !checkN(0) {
			return ErrDenied
		}
		for _, key := range []string{"emails", "mobiles", "employee_ids"} {
			for _, v := range toStrings(r.Payload[key]) {
				if !includes(b.ContactIdentifiers, v) {
					return ErrDenied
				}
			}
		}
	case "chats":
		if !checkN(0) {
			return ErrDenied
		}
	default:
		return ErrDenied
	}
	if r.Op == "send" || r.Op == "reply" || r.Op == "edit" {
		if e := checkContentResources(r.Payload["content"], func(kind, id string) bool {
			base := b.Resources[kind]
			if kind == "card" {
				base = b.Cards
			}
			return has(kind, id, base)
		}); e != nil {
			return e
		}
	}
	if r.Op == "card_create" {
		if e := checkContentResources(r.Payload["card"], func(kind, id string) bool {
			base := b.Resources[kind]
			if kind == "card" {
				base = b.Cards
			}
			return has(kind, id, base)
		}); e != nil {
			return e
		}
	}
	if r.Op != "send" {
		for _, s := range ids {
			if !safeSegment(s) {
				return ErrDenied
			}
		}
	}
	return nil
}
func toStrings(v any) []string {
	if s, ok := v.([]string); ok {
		return s
	}
	s := []string{}
	for _, v := range arr(v) {
		s = append(s, str(v))
	}
	return s
}
func (a *Adapter) ownsBlob(b Blob) bool {
	check := func(v Blob) bool { return b.Name == v.Name && bytes.Equal(b.Data, v.Data) }
	for _, v := range a.binding.Files {
		// upload_file's explicit file_name is a display filename, not another
		// filesystem read. The exact bytes were already frozen in this grant.
		if bytes.Equal(b.Data, v.Data) {
			return true
		}
	}
	for ref, v := range a.binding.Images {
		if check(v) {
			return true
		}
		if normalized, e := remoteImageBlob(ref, v); e == nil && check(normalized) {
			return true
		}
	}
	return false
}
func (a *Adapter) ReadFile(ctx context.Context, ref string) (Blob, error) {
	if e := a.check(ctx); e != nil {
		return Blob{}, e
	}
	if ctx.Err() != nil {
		return Blob{}, ctx.Err()
	}
	b, ok := a.binding.Files[ref]
	if !ok {
		return Blob{}, ErrDenied
	}
	return clone(b), nil
}
func (a *Adapter) FetchImage(ctx context.Context, ref string) (Blob, error) {
	if e := a.check(ctx); e != nil {
		return Blob{}, e
	}
	if ctx.Err() != nil {
		return Blob{}, ctx.Err()
	}
	b, ok := a.binding.Images[ref]
	if !ok {
		return Blob{}, ErrDenied
	}
	return clone(b), nil
}
func uuidJSON(v any) []byte {
	var b bytes.Buffer
	e := json.NewEncoder(&b)
	e.SetEscapeHTML(false)
	_ = e.Encode(v)
	return bytes.TrimSuffix(b.Bytes(), []byte{'\n'})
}
func (a *Adapter) tokenFor(ctx context.Context, force bool) (string, error) {
	if e := a.check(ctx); e != nil {
		return "", e
	}
	a.tokenMu.Lock()
	defer a.tokenMu.Unlock()
	now := a.now().Unix()
	if a.token != "" && !force && a.expires-now > 60 {
		return a.token, nil
	}
	r, e := a.request(ctx, "POST", "/auth/v3/tenant_access_token/internal", nil, Object{"app_id": a.binding.AppID, "app_secret": a.binding.AppSecret}, nil, "", 10*time.Second)
	if e != nil {
		return "", e
	}
	if r.Status < 200 || r.Status >= 400 {
		return "", ErrRejected
	}
	var d Object
	if decode(r.Body, &d) != nil || !zero(d["code"]) {
		return "", ErrRejected
	}
	token, ok := d["tenant_access_token"].(string)
	if !ok || token == "" {
		return "", ErrRejected
	}
	expire := int64(7200)
	if n, ok := d["expire"].(json.Number); ok {
		var err error
		expire, err = n.Int64()
		if err != nil {
			return "", ErrUnsupported
		}
	} else if _, ok := d["expire"]; ok {
		return "", ErrUnsupported
	}
	a.token = token
	a.expires = now + expire
	return token, nil
}
func (a *Adapter) ForceToken(ctx context.Context) (string, error) { return a.tokenFor(ctx, true) }
func (a *Adapter) check(ctx context.Context) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if a.guard != nil {
		return a.guard(ctx, Scope{a.binding.Owner, a.binding.Generation, a.binding.Revision, a.binding.InvocationID})
	}
	return nil
}
func (a *Adapter) API(ctx context.Context, r Request) (_out Response, _error error) {
	defer sourceErrors(&_error)
	select {
	case a.gate <- struct{}{}:
		defer func() { <-a.gate }()
	case <-ctx.Done():
		return Response{}, ctx.Err()
	}
	if a.uncertain.Load() {
		return Response{}, ErrUnknown
	}
	if e := a.check(ctx); e != nil {
		return Response{}, e
	}
	r = clone(r)
	if e := a.authorize(r); e != nil {
		return Response{}, e
	}
	method, path, timeout, write, e := route(r)
	if e != nil {
		return Response{}, e
	}
	payload := clone(r.Payload)
	switch r.Op {
	case "send", "reply":
		payload["content"] = string(uuidJSON(payload["content"]))
		payload["uuid"] = a.uuid()
	case "edit":
		payload["content"] = string(uuidJSON(payload["content"]))
	case "card_create":
		payload = Object{"type": "card_json", "data": string(uuidJSON(payload["card"]))}
	case "card_content":
		payload["uuid"] = a.uuid()
	case "card_close":
		payload["uuid"] = a.uuid()
		payload["settings"] = string(uuidJSON(Object{"config": Object{"streaming_mode": false}}))
	}
	query := clone(r.Query)
	if query == nil {
		query = Object{}
	}
	if r.Op == "send" {
		query["receive_id_type"] = r.IDs[1]
	}
	if r.Op == "chats" {
		query = Object{"user_id_type": "open_id", "page_size": 100}
	}
	var intent Intent
	if write {
		seq := a.sequence.Add(1)
		digest := sha256.Sum256(uuidJSON(Object{"op": r.Op, "ids": r.IDs, "query": query, "payload": r.Payload, "blob": r.Blob}))
		intent = Intent{ID: fmt.Sprintf("%s/%d/%d/%s/%d", a.binding.Owner, a.binding.Generation, a.binding.Revision, a.binding.InvocationID, seq), Owner: a.binding.Owner, Generation: a.binding.Generation, Revision: a.binding.Revision, Digest: hex.EncodeToString(digest[:]), Op: r.Op}
		existing, e := a.journal.Begin(ctx, intent)
		if e != nil {
			if errors.Is(e, ErrUnknown) {
				a.uncertain.Store(true)
			}
			return Response{}, e
		}
		if existing != nil {
			if existing.State == "confirmed" {
				a.confirmGrants(r, existing.Response)
				return clone(existing.Response), nil
			}
			return clone(existing.Response), ErrRejected
		}
	}
	token, e := a.tokenFor(ctx, r.Op == "chats")
	if e != nil {
		if write {
			if a.finish(ctx, Receipt{Intent: intent, State: "rejected"}) != nil {
				a.uncertain.Store(true)
				return Response{}, ErrUnknown
			}
		}
		return Response{}, e
	}
	if e := a.check(ctx); e != nil {
		if write && a.finish(ctx, Receipt{Intent: intent, State: "rejected"}) != nil {
			a.uncertain.Store(true)
			return Response{}, ErrUnknown
		}
		return Response{}, e
	}
	response, e := a.request(ctx, method, path, query, payload, r.Blob, token, timeout)
	if write {
		state := "confirmed"
		if e != nil {
			state = "unknown"
		} else {
			var d Object
			if r.Op == "card_content" || r.Op == "card_close" {
				parsed, parseError := SafeCardKit(response.Body)
				if parseError != nil {
					state = "unknown"
				} else if !CardKitOK(response.Status, parsed) {
					state = "rejected"
				}
			} else if decode(response.Body, &d) != nil {
				state = "unknown"
			} else if !zero(d["code"]) {
				state = "rejected"
			}
		}
		if a.finish(ctx, Receipt{Intent: intent, State: state, Response: response}) != nil {
			a.uncertain.Store(true)
			return Response{}, ErrUnknown
		}
		if state == "confirmed" {
			a.confirmGrants(r, response)
		}
		if state == "unknown" {
			a.uncertain.Store(true)
			return Response{}, ErrUnknown
		}
	}
	if e != nil {
		return Response{}, errors.New("Lark transport failed")
	}
	if !write {
		a.confirmGrants(r, response)
	}
	return response, nil
}
func (a *Adapter) finish(ctx context.Context, r Receipt) error {
	c, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	return a.journal.Finish(c, r)
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
	case "card_create":
		return "POST", "/cardkit/v1/cards", timeout, true, nil
	case "card_content":
		return "PUT", "/cardkit/v1/cards/" + id(0) + "/elements/" + id(1) + "/content", timeout, true, nil
	case "card_close":
		return "PATCH", "/cardkit/v1/cards/" + id(0) + "/settings", timeout, true, nil
	case "upload_image":
		return "POST", "/im/v1/images", 30 * time.Second, true, nil
	case "upload_file":
		return "POST", "/im/v1/files", 60 * time.Second, true, nil
	case "image_get":
		return "GET", "/im/v1/images/" + id(0), timeout, false, nil
	case "file_get":
		return "GET", "/im/v1/files/" + id(0), 60 * time.Second, false, nil
	case "resource_get":
		return "GET", "/im/v1/messages/" + id(0) + "/resources/" + id(1), 60 * time.Second, false, nil
	case "user_get":
		return "GET", "/contact/v3/users/" + id(0), 10 * time.Second, false, nil
	case "user_batch":
		return "POST", "/contact/v3/users/batch_get_id", timeout, false, nil
	case "chats":
		return "GET", "/im/v1/chats", 10 * time.Second, false, nil
	}
	return "", "", 0, false, ErrDenied
}
func (a *Adapter) request(ctx context.Context, method, path string, query, payload Object, blob *Blob, token string, timeout time.Duration) (Response, error) {
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

func checkContentResources(v any, allowed func(string, string) bool) error {
	if o := obj(v); o != nil {
		for k, x := range o {
			kind := ""
			switch k {
			case "img_key", "image_key":
				kind = "image"
			case "file_key":
				kind = "file"
			case "card_id":
				kind = "card"
			}
			if kind != "" {
				id, ok := x.(string)
				if !ok || !allowed(kind, id) {
					return ErrDenied
				}
			}
			if e := checkContentResources(x, allowed); e != nil {
				return e
			}
		}
	}
	for _, x := range arr(v) {
		if e := checkContentResources(x, allowed); e != nil {
			return e
		}
	}
	return nil
}

// Only a persisted successful receipt may introduce a new resource identity.
// The parent must reconstruct grants from its durable receipt for later calls.
func (a *Adapter) confirmGrants(r Request, response Response) {
	var body Object
	if decode(response.Body, &body) != nil || !zero(body["code"]) {
		return
	}
	data := obj(body["data"])
	if data == nil {
		return
	}
	a.grantMu.Lock()
	defer a.grantMu.Unlock()
	add := func(kind string, v any) {
		id, ok := v.(string)
		if !ok || !safeSegment(id) {
			return
		}
		if a.derived[kind] == nil {
			a.derived[kind] = map[string]bool{}
		}
		a.derived[kind][id] = true
	}
	switch r.Op {
	case "send", "reply":
		add("message", data["message_id"])
	case "card_create":
		add("card", data["card_id"])
	case "reaction_add":
		if len(r.IDs) == 1 {
			add("reaction:"+r.IDs[0], data["reaction_id"])
		}
	case "upload_image":
		add("image", data["image_key"])
	case "upload_file":
		add("file", data["file_key"])
	case "user_batch":
		for _, item := range arr(data["user_list"]) {
			u := obj(item)
			for _, key := range []string{"email", "mobile", "employee_id", "employee_no"} {
				identifier, ok := u[key].(string)
				requestKey := map[string]string{"email": "emails", "mobile": "mobiles", "employee_id": "employee_ids", "employee_no": "employee_ids"}[key]
				if ok && includes(a.binding.RecipientIdentifiers, identifier) && includes(toStrings(r.Payload[requestKey]), identifier) {
					add("target:open_id", u["user_id"])
				}
			}
		}
	}
}
