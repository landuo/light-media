// Package integrations implements bounded, read-only M0 protocol probes.
package integrations

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const MaxBody = 1 << 20

type Downloader struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	BaseURL  string `json:"base_url"`
	Username string `json:"username"`
	Password string `json:"password"`
}
type Site struct {
	ID      string `json:"id"`
	Kind    string `json:"kind"`
	BaseURL string `json:"base_url"`
	Cookie  string `json:"cookie"`
	APIKey  string `json:"api_key"`
	Query   string `json:"query"`
}
type Input struct {
	Downloaders []Downloader `json:"downloaders"`
	Sites       []Site       `json:"sites"`
}
type Capability struct {
	Name          string `json:"name"`
	ProtocolKnown bool   `json:"protocol_known"`
	ReadObserved  bool   `json:"read_observed"`
	WriteVerified bool   `json:"write_verified"`
}
type Result struct {
	ID                string       `json:"id"`
	Kind              string       `json:"kind"`
	Status            string       `json:"status"`
	Stage             string       `json:"stage"`
	Evidence          string       `json:"evidence"`
	Version           string       `json:"version,omitempty"`
	Protocol          string       `json:"protocol,omitempty"`
	Requests          int          `json:"requests"`
	SampleIdentity    string       `json:"sample_identity,omitempty"`
	SampleCount       int          `json:"sample_count"`
	FileCount         int          `json:"file_count"`
	PathFieldsPresent int          `json:"path_fields_present"`
	Capabilities      []Capability `json:"capabilities,omitempty"`
}
type Report struct {
	Results []Result `json:"results"`
	Status  string   `json:"status"`
}

var safeToken = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)
var hashPattern = regexp.MustCompile(`^[a-fA-F0-9]{40}([a-fA-F0-9]{24})?$`)
var versionPattern = regexp.MustCompile(`^v?[0-9]+(\.[0-9]+){1,3}([ -][A-Za-z0-9().-]{1,40})?$`)

func safe(s string) string {
	if safeToken.MatchString(s) {
		return s
	}
	return "invalid_id"
}
func version(s string) string {
	if versionPattern.MatchString(s) {
		return s
	}
	return "unrecognized"
}

// TaskIdentity is instance scoped; numeric session IDs must never be persisted as identity.
type TaskIdentity struct {
	InstanceID string
	Hash       string
}

func (t TaskIdentity) Fingerprint() string {
	sum := sha256.Sum256([]byte(t.InstanceID + "\x00" + strings.ToLower(t.Hash)))
	return hex.EncodeToString(sum[:16])
}

func Run(ctx context.Context, in Input) Report {
	out := Report{Status: "ok", Results: []Result{}}
	if len(in.Downloaders)+len(in.Sites) > 10 {
		return Report{Status: "invalid_input", Results: []Result{}}
	}
	seen := map[string]bool{}
	for _, d := range in.Downloaders {
		if seen[d.ID] || !safeToken.MatchString(d.ID) {
			return Report{Status: "invalid_input", Results: []Result{}}
		}
		seen[d.ID] = true
	}
	for _, d := range in.Downloaders {
		out.Results = append(out.Results, ProbeDownloader(ctx, d))
	}
	for _, s := range in.Sites {
		out.Results = append(out.Results, ProbeSite(ctx, s))
	}
	if len(out.Results) == 0 {
		out.Status = "unverified"
	}
	for _, r := range out.Results {
		if r.Status != "ok" && r.Status != "empty_result" {
			out.Status = "incomplete"
		}
	}
	return out
}

type transport struct {
	base   *url.URL
	client *http.Client
	count  int
}

func newTransport(raw string) (*transport, string) {
	u, e := url.Parse(raw)
	if e != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, "invalid_input"
	}
	jar, _ := cookiejar.New(nil)
	return &transport{base: u, client: &http.Client{Timeout: 10 * time.Second, Jar: jar, CheckRedirect: func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }}}, ""
}
func (t *transport) endpoint(path string) string {
	u := *t.base
	u.Path = strings.TrimRight(u.Path, "/") + "/" + strings.TrimLeft(path, "/")
	return u.String()
}
func (t *transport) request(ctx context.Context, method, endpoint string, data []byte, headers map[string]string, user, pass string) ([]byte, http.Header, string) {
	if t.count >= 10 {
		return nil, nil, "request_limit"
	}
	t.count++
	req, e := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(data))
	if e != nil {
		return nil, nil, "invalid_input"
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	req.Header.Set("User-Agent", "light-media-M0/0.1")
	if user != "" || pass != "" {
		req.SetBasicAuth(user, pass)
	}
	resp, e := t.client.Do(req)
	if e != nil {
		return nil, nil, "transport_error"
	}
	defer resp.Body.Close()
	body, e := io.ReadAll(io.LimitReader(resp.Body, MaxBody+1))
	if e != nil {
		return nil, resp.Header, "transport_error"
	}
	if len(body) > MaxBody {
		return nil, resp.Header, "body_limit"
	}
	status := ""
	switch {
	case resp.StatusCode == 401:
		status = "authentication_failed"
	case resp.StatusCode == 403:
		status = "forbidden"
	case resp.StatusCode == 429:
		status = "rate_limited"
	case resp.StatusCode == 409:
		status = "session_conflict"
	case resp.StatusCode >= 300 && resp.StatusCode < 400:
		status = "redirect_blocked"
	case resp.StatusCode < 200 || resp.StatusCode >= 300:
		status = "http_error"
	}
	return body, resp.Header, status
}
func decode(b []byte, v any) bool {
	d := json.NewDecoder(bytes.NewReader(b))
	if d.Decode(v) != nil {
		return false
	}
	return errors.Is(d.Decode(new(any)), io.EOF)
}
func capRead(name string) Capability {
	return Capability{Name: name, ProtocolKnown: true, ReadObserved: true}
}
func writes(kind string) []Capability {
	var c []Capability
	for _, n := range []string{"add_stopped", "pause", "resume", "verify", "relocate_existing", "move_data", "remove_keep_data"} {
		c = append(c, Capability{Name: n, ProtocolKnown: n != "relocate_existing" || kind == "transmission"})
	}
	return c
}
func ProbeDownloader(ctx context.Context, d Downloader) (r Result) {
	r = Result{ID: safe(d.ID), Kind: safe(d.Kind), Status: "unverified", Stage: "configuration", Evidence: "live_request"}
	if !safeToken.MatchString(d.ID) {
		r.Status = "invalid_input"
		return
	}
	t, s := newTransport(d.BaseURL)
	if s != "" {
		r.Status = s
		return
	}
	defer func() { r.Requests = t.count }()
	switch d.Kind {
	case "qbittorrent":
		qb(ctx, t, d, &r)
	case "transmission":
		tr(ctx, t, d, &r)
	default:
		r.Status = "unsupported_kind"
	}
	return
}
func qb(ctx context.Context, t *transport, d Downloader, r *Result) {
	r.Stage = "login"
	body, _, s := t.request(ctx, "POST", t.endpoint("api/v2/auth/login"), []byte(url.Values{"username": {d.Username}, "password": {d.Password}}.Encode()), map[string]string{"Content-Type": "application/x-www-form-urlencoded", "Referer": t.base.String()}, "", "")
	if s != "" {
		r.Status = s
		return
	}
	if strings.TrimSpace(string(body)) != "Ok." {
		r.Status = "authentication_failed"
		return
	}
	loginURL, _ := url.Parse(t.endpoint("api/v2/app/version"))
	hasSession := false
	for _, cookie := range t.client.Jar.Cookies(loginURL) {
		if cookie.Name == "SID" && cookie.Value != "" {
			hasSession = true
		}
	}
	if !hasSession {
		r.Status = "authentication_failed"
		return
	}
	for _, item := range []struct{ path, stage string }{{"app/version", "version"}, {"app/webapiVersion", "webapi_version"}} {
		r.Stage = item.stage
		body, _, s = t.request(ctx, "GET", t.endpoint("api/v2/"+item.path), nil, nil, "", "")
		if s != "" {
			r.Status = s
			return
		}
		v := version(strings.TrimSpace(string(body)))
		if v == "unrecognized" {
			r.Status = "protocol_changed"
			return
		}
		if item.stage == "version" {
			r.Version = v
		} else {
			r.Protocol = v
		}
	}
	r.Capabilities = writes("qbittorrent")
	r.Capabilities = append(r.Capabilities, capRead("probe"))
	r.Stage = "preferences"
	body, _, s = t.request(ctx, "GET", t.endpoint("api/v2/app/preferences"), nil, nil, "", "")
	if s != "" {
		r.Status = s
		return
	}
	var prefs map[string]json.RawMessage
	if !decode(body, &prefs) || prefs == nil {
		r.Status = "protocol_changed"
		return
	}
	for _, k := range []string{"save_path", "temp_path"} {
		var p string
		if v, ok := prefs[k]; ok && json.Unmarshal(v, &p) == nil && p != "" {
			r.PathFieldsPresent++
		}
	}
	if r.PathFieldsPresent == 0 {
		r.Status = "protocol_changed"
		return
	}
	r.Stage = "torrent_sample"
	body, _, s = t.request(ctx, "GET", t.endpoint("api/v2/torrents/info")+"?limit=1", nil, nil, "", "")
	if s != "" {
		r.Status = s
		return
	}
	var torrents []struct {
		Hash string `json:"hash"`
	}
	if !decode(body, &torrents) || torrents == nil || len(torrents) > 1 {
		r.Status = "protocol_changed"
		return
	}
	r.Capabilities = append(r.Capabilities, capRead("list"))
	r.SampleCount = len(torrents)
	if len(torrents) == 0 {
		r.Status = "ok"
		r.Stage = "no_sample_files_unverified"
		return
	}
	h := torrents[0].Hash
	if !hashPattern.MatchString(h) {
		r.Status = "protocol_changed"
		return
	}
	r.SampleIdentity = (TaskIdentity{d.ID, h}).Fingerprint()
	r.Stage = "files"
	body, _, s = t.request(ctx, "GET", t.endpoint("api/v2/torrents/files")+"?hash="+url.QueryEscape(h), nil, nil, "", "")
	if s != "" {
		r.Status = s
		return
	}
	var files []struct {
		Name     string   `json:"name"`
		Size     *int64   `json:"size"`
		Progress *float64 `json:"progress"`
	}
	if !decode(body, &files) || files == nil {
		r.Status = "protocol_changed"
		return
	}
	for _, f := range files {
		if f.Name == "" || f.Size == nil || f.Progress == nil {
			r.Status = "protocol_changed"
			return
		}
	}
	r.FileCount = len(files)
	r.Capabilities = append(r.Capabilities, capRead("files"))
	r.Status = "ok"
	r.Stage = "complete"
}

func rpc(ctx context.Context, t *transport, d Downloader, session *string, modern bool, method string, args map[string]any) (map[string]json.RawMessage, string) {
	req := map[string]any{"method": method, "arguments": args, "tag": 1}
	if modern {
		req = map[string]any{"jsonrpc": "2.0", "method": method, "params": args, "id": 1}
	}
	payload, _ := json.Marshal(req)
	for attempt := 0; attempt < 2; attempt++ {
		body, h, s := t.request(ctx, "POST", t.base.String(), payload, map[string]string{"Content-Type": "application/json", "X-Transmission-Session-Id": *session}, d.Username, d.Password)
		if s == "session_conflict" && attempt == 0 {
			*session = h.Get("X-Transmission-Session-Id")
			if *session == "" || len(*session) > 1024 {
				return nil, "protocol_changed"
			}
			continue
		}
		if s != "" {
			return nil, s
		}
		var env map[string]json.RawMessage
		if !decode(body, &env) || env == nil {
			return nil, "protocol_changed"
		}
		var result map[string]json.RawMessage
		if modern {
			var v string
			_ = json.Unmarshal(env["jsonrpc"], &v)
			if v != "2.0" {
				return nil, "protocol_changed"
			}
			var id int
			if json.Unmarshal(env["id"], &id) != nil || id != 1 {
				return nil, "protocol_changed"
			}
			if _, ok := env["error"]; ok {
				return nil, "business_error"
			}
			if json.Unmarshal(env["result"], &result) != nil || result == nil {
				return nil, "protocol_changed"
			}
		} else {
			var outcome string
			if json.Unmarshal(env["result"], &outcome) != nil {
				return nil, "protocol_changed"
			}
			if outcome != "success" {
				return nil, "business_error"
			}
			if json.Unmarshal(env["arguments"], &result) != nil || result == nil {
				return nil, "protocol_changed"
			}
		}
		return result, ""
	}
	return nil, "session_conflict"
}
func tr(ctx context.Context, t *transport, d Downloader, r *Result) {
	// A root URL uses Transmission's documented default path; supplied proxy/RPC paths stay exact.
	if t.base.Path == "" || t.base.Path == "/" {
		t.base.Path = "/transmission/rpc"
	}
	session := ""
	r.Stage = "session_get"
	result, s := rpc(ctx, t, d, &session, false, "session-get", map[string]any{"fields": []string{"version", "rpc-version", "download-dir"}})
	if s != "" {
		r.Status = s
		return
	}
	var v string
	var rpcVersion int
	_ = json.Unmarshal(result["version"], &v)
	_ = json.Unmarshal(result["rpc-version"], &rpcVersion)
	if version(v) == "unrecognized" || rpcVersion <= 0 {
		r.Status = "protocol_changed"
		return
	}
	r.Version = version(v)
	r.Protocol = "legacy"
	modern := strings.HasPrefix(v, "4.1.") || rpcVersion >= 18
	if modern {
		r.Protocol = "jsonrpc2"
		result, s = rpc(ctx, t, d, &session, true, "session_get", map[string]any{"fields": []string{"version", "rpc_version_semver", "download_dir"}})
		if s != "" {
			r.Status = s
			return
		}
		var verified string
		_ = json.Unmarshal(result["version"], &verified)
		if version(verified) == "unrecognized" {
			r.Status = "protocol_changed"
			return
		}
	}
	pathKey := "download-dir"
	hashKey := "hashString"
	method := "torrent-get"
	recent := "recently-active"
	if modern {
		pathKey = "download_dir"
		hashKey = "hash_string"
		method = "torrent_get"
		recent = "recently_active"
	}
	var p string
	_ = json.Unmarshal(result[pathKey], &p)
	if p != "" {
		r.PathFieldsPresent = 1
	}
	r.Capabilities = writes("transmission")
	r.Capabilities = append(r.Capabilities, capRead("probe"))
	r.Stage = "torrent_sample"
	fields := []string{hashKey}
	if modern {
		fields = append(fields, "file_count")
	}
	result, s = rpc(ctx, t, d, &session, modern, method, map[string]any{"fields": fields, "ids": recent})
	if s != "" {
		r.Status = s
		return
	}
	var torrents []map[string]json.RawMessage
	if json.Unmarshal(result["torrents"], &torrents) != nil || torrents == nil {
		r.Status = "protocol_changed"
		return
	}
	r.Capabilities = append(r.Capabilities, capRead("recent_list"))
	if len(torrents) == 0 {
		r.Status = "ok"
		r.Stage = "no_recent_sample_files_unverified"
		return
	}
	var h string
	if modern {
		// Request only bounded file-count metadata first: a first torrent can
		// contain tens of thousands of files and exceed the response budget.
		best := 1001
		for _, tor := range torrents {
			var count int
			var candidate string
			if json.Unmarshal(tor["file_count"], &count) == nil && count > 0 && count < best && json.Unmarshal(tor[hashKey], &candidate) == nil && hashPattern.MatchString(candidate) {
				best = count
				h = candidate
			}
		}
		if h == "" {
			r.Status = "unverified"
			r.Stage = "no_bounded_sample_files_unverified"
			return
		}
	} else {
		_ = json.Unmarshal(torrents[0][hashKey], &h)
	}
	if !hashPattern.MatchString(h) {
		r.Status = "protocol_changed"
		return
	}
	r.SampleCount = 1
	r.SampleIdentity = (TaskIdentity{d.ID, h}).Fingerprint()
	r.Stage = "files"
	result, s = rpc(ctx, t, d, &session, modern, method, map[string]any{"fields": []string{hashKey, "files"}, "ids": []string{h}})
	if s != "" {
		r.Status = s
		return
	}
	if json.Unmarshal(result["torrents"], &torrents) != nil || len(torrents) != 1 {
		r.Status = "protocol_changed"
		return
	}
	var actual string
	_ = json.Unmarshal(torrents[0][hashKey], &actual)
	if !strings.EqualFold(actual, h) {
		r.Status = "protocol_changed"
		return
	}
	var files []map[string]json.RawMessage
	if json.Unmarshal(torrents[0]["files"], &files) != nil || files == nil {
		r.Status = "protocol_changed"
		return
	}
	for _, f := range files {
		var name string
		var length int64
		if json.Unmarshal(f["name"], &name) != nil || name == "" || json.Unmarshal(f["length"], &length) != nil || length < 0 {
			r.Status = "protocol_changed"
			return
		}
	}
	r.FileCount = len(files)
	r.Capabilities = append(r.Capabilities, capRead("files"))
	r.Status = "ok"
	r.Stage = "complete"
}
