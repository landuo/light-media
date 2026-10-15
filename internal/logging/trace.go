package logging

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const RunByteBudget = 256 << 10

type runKey struct{}
type targetKey struct{}
type siteKey struct{}
type operationKey struct{}
type target struct{ id, kind string }

func SiteContext(ctx context.Context, id string) context.Context {
	if len(id) > 64 {
		return ctx
	}
	for _, r := range id {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
			return ctx
		}
	}
	return context.WithValue(ctx, siteKey{}, id)
}

// EndpointContext converts only recognized endpoint categories to fixed labels.
// The original endpoint/URL is never attached to a record or context value.
func EndpointContext(ctx context.Context, service, endpoint string) context.Context {
	p := endpoint
	if u, err := url.Parse(endpoint); err == nil {
		p = u.Path
	}
	operation := "http_request"
	if service == "fnos" {
		switch {
		case p == "/login":
			operation = "fnos_login"
		case p == "/sys/version":
			operation = "fnos_version"
		case p == "/user/info":
			operation = "fnos_user"
		case p == "/mdb/list" || p == "/mdb/list_all":
			operation = "fnos_libraries"
		case p == "/mdb/create":
			operation = "fnos_library_create"
		case strings.HasPrefix(p, "/mdb/scan/"):
			operation = "fnos_library_scan"
		case strings.HasPrefix(p, "/mdb/"):
			operation = "fnos_library_detail"
		case p == "/item/list":
			operation = "fnos_item_list"
		case strings.HasPrefix(p, "/item/"):
			operation = "fnos_item_detail"
		case strings.HasPrefix(p, "/person/"):
			operation = "fnos_persons"
		case p == "/search/list":
			operation = "fnos_search"
		case p == "/scrap/search":
			operation = "fnos_provider_search"
		case strings.Contains(p, "/sys/img/"):
			operation = "fnos_image"
		}
	} else if service == "metadata" {
		switch {
		case strings.Contains(p, "/episode/"):
			operation = "metadata_episode"
		case strings.HasPrefix(p, "/3/movie/"):
			operation = "metadata_movie"
		case strings.HasPrefix(p, "/3/tv/"):
			operation = "metadata_tv"
		}
	} else if service == "integration" || service == "material" {
		for suffix, label := range map[string]string{"/api/v2/auth/login": "qb_login", "/api/v2/app/version": "qb_version", "/api/v2/app/webapiVersion": "qb_api_version", "/api/v2/app/preferences": "qb_preferences", "/api/v2/torrents/info": "qb_torrents", "/api/v2/torrents/files": "qb_files", "/transmission/rpc": "transmission_rpc", "/api/member/profile": "mteam_profile", "/api/torrent/search": "site_search", "/api/torrent/genDlToken": "material_token", "/torrents.php": "site_search", "/index.php": "site_check", "/download.php": "material_fetch"} {
			if strings.HasSuffix(p, suffix) {
				operation = label
				break
			}
		}
	}
	return context.WithValue(ctx, operationKey{}, operation)
}

// Run owns one independent bounded sink; it never changes slog's default logger.
type Run struct {
	ID       string
	handler  *Handler
	sequence atomic.Uint64
}

func NewRun(writer io.Writer) (*Run, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return nil, errors.New("trace ID generation failed")
	}
	h, err := New(writer, Options{MaxBytes: RunByteBudget})
	if err != nil {
		return nil, err
	}
	r := &Run{ID: "run-" + hex.EncodeToString(b), handler: h}
	r.record(context.Background(), slog.LevelInfo, "run_started", nil)
	return r, nil
}

func (r *Run) Context(ctx context.Context) context.Context {
	return context.WithValue(ctx, runKey{}, r)
}

// Target generates a target identity. No caller label, host or credential is used.
func Target(ctx context.Context, kind string) (context.Context, string) {
	r, _ := ctx.Value(runKey{}).(*Run)
	if r == nil {
		return ctx, ""
	}
	switch kind {
	case "downloader", "site", "material", "fnos", "metadata", "fnos_experiment":
	default:
		kind = "probe"
	}
	id := "target-" + strings.TrimPrefix(r.ID, "run-") + "-" + strconv.FormatUint(r.sequence.Add(1), 10)
	return context.WithValue(ctx, targetKey{}, target{id, kind}), id
}

func (r *Run) record(ctx context.Context, level slog.Level, event string, attrs []slog.Attr) {
	record := slog.NewRecord(time.Now(), level, "M0 probe trace", 0)
	record.AddAttrs(slog.String("module", "m0_trace"), slog.String("event", event), slog.String("run_id", r.ID))
	if t, ok := ctx.Value(targetKey{}).(target); ok {
		record.AddAttrs(slog.String("target_id", t.id), slog.String("target_kind", t.kind))
	}
	if site, ok := ctx.Value(siteKey{}).(string); ok && site != "" {
		record.AddAttrs(slog.String("site_id", site))
	}
	record.AddAttrs(attrs...)
	_ = r.handler.Handle(ctx, record) // Stats below makes sink failure visible in stdout.
}

// Transport leaves HTTP semantics unchanged and never inspects URL, headers or
// payload. A response completion is emitted on Body.Close, after bounded readers.
func Transport(base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return traceTransport{base}
}

type traceTransport struct{ base http.RoundTripper }

func (t traceTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	r, _ := req.Context().Value(runKey{}).(*Run)
	if r == nil {
		return t.base.RoundTrip(req)
	}
	requestID := "request-" + strings.TrimPrefix(r.ID, "run-") + "-" + strconv.FormatUint(r.sequence.Add(1), 10)
	ctx := WithIDs(req.Context(), IDs{RequestID: requestID})
	method := req.Method
	switch method {
	case "GET", "POST", "HEAD", "PUT", "PATCH", "DELETE", "OPTIONS":
	default:
		method = "OTHER"
	}
	operation, _ := req.Context().Value(operationKey{}).(string)
	if operation == "" {
		operation = "http_request"
	}
	attrs := []slog.Attr{slog.String("operation", operation), slog.String("method", method)}
	r.record(ctx, slog.LevelInfo, "http_started", attrs)
	start := time.Now()
	finish := func(status int, transportFailed bool) {
		state, code, level := "ok", "none", slog.LevelInfo
		switch {
		case transportFailed:
			state, code, level = "failed", "transport_error", slog.LevelError
		case status >= 400:
			state, code, level = "failed", "http_"+strconv.Itoa(status), slog.LevelError
		case status >= 300:
			state, code = "redirect_response", "http_"+strconv.Itoa(status)
		}
		r.record(ctx, level, "http_completed", append(append([]slog.Attr{}, attrs...), slog.String("status", state), slog.String("error_code", code), slog.Int("http_status", status), slog.Int64("duration_ms", time.Since(start).Milliseconds())))
	}
	resp, err := t.base.RoundTrip(req)
	if err != nil {
		finish(0, true)
		return resp, err
	}
	if resp == nil {
		finish(0, true)
		return resp, err
	}
	if resp.Body == nil {
		finish(resp.StatusCode, false)
	} else {
		resp.Body = &traceBody{body: resp.Body, status: resp.StatusCode, finish: finish}
	}
	return resp, err
}

type traceBody struct {
	body   io.ReadCloser
	status int
	finish func(int, bool)
	mu     sync.Mutex
	failed bool
	once   sync.Once
}

func (b *traceBody) Read(p []byte) (int, error) {
	n, err := b.body.Read(p)
	if err != nil && err != io.EOF {
		b.mu.Lock()
		b.failed = true
		b.mu.Unlock()
	}
	return n, err
}
func (b *traceBody) Close() error {
	err := b.body.Close()
	b.mu.Lock()
	failed := b.failed || err != nil
	b.mu.Unlock()
	b.once.Do(func() { b.finish(b.status, failed) })
	return err
}

// Report preserves the existing summary shape and adds generated run identity and
// sink health. Only caller id/kind aliases are filtered against supplied secrets.
func (r *Run) Report(writer io.Writer, value any, status string, secrets ...string) error {
	state := "failed"
	switch status {
	case "ok", "passed", "observed", "cleaned", "planned", "dry_run":
		state = "ok"
	case "prepared":
		state = "prepared"
	case "scan_accepted":
		state = "accepted"
	case "diagnosed":
		state = "diagnosed"
	case "unverified":
		state = "unverified"
	}
	level := slog.LevelInfo
	if state == "failed" {
		level = slog.LevelError
	}
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}
	var obj map[string]any
	decoder := json.NewDecoder(strings.NewReader(string(b)))
	decoder.UseNumber()
	if err = decoder.Decode(&obj); err != nil || obj == nil {
		return errors.New("trace report must be an object")
	}
	redactAliases(obj, secrets)
	code := "none"
	if state == "failed" {
		code = "probe_incomplete"
	}
	if state == "unverified" {
		code = "probe_unverified"
	}
	if supplied, ok := obj["code"].(string); ok {
		if fixed := fixedSummaryCode(supplied); fixed != "" {
			code = fixed
		}
	}
	if results, ok := obj["results"].([]any); ok {
		for _, item := range results {
			result, ok := item.(map[string]any)
			if !ok {
				continue
			}
			tid, _ := result["trace_target_id"].(string)
			if !strings.HasPrefix(tid, "target-"+strings.TrimPrefix(r.ID, "run-")+"-") {
				continue
			}
			resultCode := "probe_incomplete"
			for _, key := range []string{"code", "status"} {
				if v, ok := result[key].(string); ok {
					if fixed := fixedSummaryCode(v); fixed != "" {
						resultCode = fixed
						break
					}
				}
			}
			if v, ok := result["status"].(string); ok && (v == "ok" || v == "passed" || v == "empty_result") {
				resultCode = "none"
			}
			r.record(context.WithValue(context.Background(), targetKey{}, target{tid, "probe"}), slog.LevelInfo, "target_completed", []slog.Attr{slog.String("error_code", resultCode)})
		}
	}
	r.record(context.Background(), level, "run_completed", []slog.Attr{slog.String("status", state), slog.String("error_code", code)})
	s := r.handler.Stats()
	logState := "ok"
	if s.Failed > 0 || s.Dropped > 0 {
		logState = "failed"
	}
	obj["run_id"] = r.ID
	obj["logging"] = map[string]any{"status": logState, "written": s.Written, "failed": s.Failed, "dropped": s.Dropped, "attempted_bytes": s.Bytes, "budget_bytes": RunByteBudget}
	if err = json.NewEncoder(writer).Encode(obj); err != nil {
		return err
	}
	if logState != "ok" {
		return errors.New("trace sink failed or exhausted")
	}
	return nil
}

func fixedSummaryCode(code string) string {
	for _, known := range []string{"ITEM_PARENT_MISMATCH", "OWNED_DETAIL_SCOPE_MISMATCH", "BASELINE_CHANGED", "INVALID_ITEM_DETAIL", "INVALID_ITEM_GUID", "INVALID_ITEMS", "INVALID_ITEM", "INVALID_SEARCH_DATA", "INVALID_SEARCH_ITEM", "INVALID_DATA", "INVALID_ENVELOPE", "INVALID_USER", "INVALID_VERSION", "INVALID_HOST", "INVALID_INPUT", "CREDENTIALS_REQUIRED", "ACCESS_CODE_SESSION_REQUIRED", "EMPTY_TOKEN", "INVALID_TOKEN", "EXPERIMENT_LOCKED", "EXCLUSIVE_SYNTHETIC_DIRECTORY_REQUIRED", "INVALID_EXPERIMENT_ID", "INVALID_ACTION", "ADMIN_REQUIRED", "ALREADY_CLEANED", "DELETE_NOT_CONFIRMED", "SCAN_ALREADY_REQUESTED", "INVALID_OR_EXCESS_ITEMS", "INVALID_OR_EXCESS_CHILDREN", "INVALID_OR_EXCESS_PERSONS", "CHILD_TYPE_MISMATCH", "BODY_LIMIT", "BODY_FAILED", "REQUEST_LIMIT", "TRANSPORT_FAILED", "authentication_failed", "forbidden", "login_challenge", "rate_limited", "redirect_blocked", "business_error", "protocol_changed", "body_limit", "request_limit", "transport_error", "invalid_input", "unsupported_kind", "unverified", "not_found", "invalid_metainfo", "http_error", "session_conflict"} {
		if code == known {
			return known
		}
	}
	if strings.HasPrefix(code, "BUSINESS_") {
		return "business_error"
	}
	if len(code) == 8 && strings.HasPrefix(code, "HTTP_") {
		if n, e := strconv.Atoi(code[5:]); e == nil && n >= 100 && n <= 599 {
			return code
		}
	}
	return ""
}

func redactAliases(value any, secrets []string) {
	switch v := value.(type) {
	case map[string]any:
		for key, item := range v {
			if key == "id" || key == "kind" {
				if label, ok := item.(string); ok {
					for _, secret := range secrets {
						if secret != "" && strings.Contains(label, secret) {
							v[key] = "redacted_target"
							break
						}
					}
				}
			}
			redactAliases(item, secrets)
		}
	case []any:
		for _, item := range v {
			redactAliases(item, secrets)
		}
	}
}
