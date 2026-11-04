package mediaservervendors

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"time"

	"light-media/internal/plugins/mediaserverruntime"
)

// This is one REQUIRED persistent private HTTP session, not a new fake Api
// success port. The original wrapper's API interface is implemented below by
// real login/session/encryption algorithms. Concrete HTTP wiring stays separate.
type UgreenHTTPField struct{ Name, Value string }
type UgreenSessionRequest struct {
	Method, URL     string
	Headers         []UgreenHTTPField
	Params          json.RawMessage
	JSON            json.RawMessage // nil means no JSON argument, not a body containing null.
	Timeout         time.Duration
	SourceVerifySSL bool // State only. Private actual TLS always uses user-approved noverify.
}
type UgreenSessionResponse struct {
	Headers []UgreenHTTPField
	JSON    func(context.Context) (json.RawMessage, error)
}
type UgreenPrivateSession interface {
	Request(context.Context, UgreenSessionRequest) (*UgreenSessionResponse, error)
	Cookies(context.Context) (json.RawMessage, error)
	UpdateCookies(context.Context, []UgreenHTTPField) error
	Close(context.Context) error
}
type UgreenAPIDependencies struct {
	Current    func(context.Context) error
	NewSession func(context.Context, string) (UgreenPrivateSession, error)
	Entropy    io.Reader
	Report     func(UgreenDiagnostic)
}
type UgreenConcreteAPI struct {
	mu                                       sync.Mutex
	deps                                     UgreenAPIDependencies
	host                                     string
	session                                  UgreenPrivateSession
	token, staticToken, publicKey, username  json.RawMessage
	isUGK                                    bool
	clientID, clientVersion, language, agent string
	timeout                                  time.Duration
	sourceVerify                             bool
	crypto                                   *ugreenCipher
}

var _ UgreenAPI = (*UgreenConcreteAPI)(nil)

func ugreenAPINil(v any) bool {
	if v == nil {
		return true
	}
	r := reflect.ValueOf(v)
	switch r.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return r.IsNil()
	}
	return false
}
func (a *UgreenConcreteAPI) current(ctx context.Context) error {
	if ctx == nil {
		return &EmbyJellyfinBoundaryError{Kind: "Ugreen API requires context"}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := a.deps.Current(ctx); err != nil {
		return &EmbyJellyfinBoundaryError{Kind: "Ugreen API current authority", cause: err}
	}
	return ctx.Err()
}
func ugreenAPICall[T any](ctx context.Context, a *UgreenConcreteAPI, fn func() (T, error)) (T, error) {
	var zero T
	if err := a.current(ctx); err != nil {
		return zero, err
	}
	value, err := fn()
	if boundary := a.current(ctx); boundary != nil {
		return zero, boundary
	}
	return value, err
}
func ugreenAPIOrdinary(err error) bool {
	// Only a direct source exception classified at the owned source boundary;
	// joined/context/permission/unknown/platform errors do not get swallowed.
	e, ok := err.(*mediaserverruntime.SourceError)
	return ok && e != nil && e.Class != "" && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded)
}
func (a *UgreenConcreteAPI) report(operation, class string) {
	a.deps.Report(UgreenDiagnostic{Operation: operation, Severity: "error", Class: class})
}
func NewUgreenAPI(ctx context.Context, options UgreenAPIOptions, deps UgreenAPIDependencies) (*UgreenConcreteAPI, error) {
	if deps.Current == nil || deps.NewSession == nil || ugreenAPINil(deps.Entropy) || deps.Report == nil {
		return nil, errors.New("Ugreen concrete API requires current/persistent session/entropy/report")
	}
	a := &UgreenConcreteAPI{deps: deps, token: json.RawMessage(`null`), staticToken: json.RawMessage(`null`), publicKey: json.RawMessage(`null`), username: json.RawMessage(`null`), clientVersion: "76363", language: "zh-CN", agent: "PC/WEB", timeout: 20 * time.Second, sourceVerify: options.SourceVerifySSL}
	if err := a.current(ctx); err != nil {
		return nil, err
	}
	normalized, err := ugreenStandardize(options.Host)
	if err != nil {
		return nil, err
	}
	text, err := ejText(normalized)
	if err != nil {
		return nil, err
	}
	parsed, err := url.Parse(text)
	if err != nil {
		return nil, ejSource("ValueError")
	}
	if text != "" {
		a.host = parsed.Scheme + "://" + parsed.Host
		if parsed.User != nil {
			a.host = parsed.Scheme + "://" + parsed.User.String() + "@" + parsed.Host
		}
	}
	session, err := ugreenAPICall(ctx, a, func() (UgreenPrivateSession, error) { return deps.NewSession(ctx, a.host) })
	if err != nil {
		return nil, err
	}
	if ugreenAPINil(session) {
		return nil, &EmbyJellyfinBoundaryError{Kind: "nil actual Ugreen private session"}
	}
	a.session = session
	cipher := &ugreenCipher{entropy: deps.Entropy}
	key, err := cipher.generateAESKey()
	if err != nil {
		_ = session.Close(ctx)
		return nil, err
	}
	a.clientID = key[:8] + "-" + key[8:12] + "-" + key[12:16] + "-" + key[16:20] + "-" + key[20:] + "-WEB"
	return a, nil
}
func (a *UgreenConcreteAPI) Token(ctx context.Context) (json.RawMessage, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.current(ctx); err != nil {
		return nil, err
	}
	return ejCopy(a.token), nil
}
func (a *UgreenConcreteAPI) Close(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	_, err := ugreenAPICall(ctx, a, func() (bool, error) { return true, a.session.Close(ctx) })
	return err
}
func (a *UgreenConcreteAPI) commonHeaders() []UgreenHTTPField {
	return []UgreenHTTPField{
		{"Accept", "application/json, text/plain, */*"}, {"Client-Id", a.clientID}, {"Client-Version", a.clientVersion}, {"UG-Agent", a.agent}, {"UG-Client-Id", a.clientID}, {"X-Specify-Language", a.language},
	}
}
func (a *UgreenConcreteAPI) requestRaw(ctx context.Context, req UgreenSessionRequest, readJSON bool) (*UgreenSessionResponse, json.RawMessage, error) {
	req.Timeout = a.timeout
	req.SourceVerifySSL = a.sourceVerify
	response, err := ugreenAPICall(ctx, a, func() (*UgreenSessionResponse, error) { return a.session.Request(ctx, req) })
	if err != nil {
		return nil, nil, err
	}
	if response == nil {
		return nil, nil, &EmbyJellyfinBoundaryError{Kind: "nil Ugreen HTTP response"}
	}
	if !readJSON {
		return response, nil, nil
	}
	if response.JSON == nil {
		return nil, nil, &EmbyJellyfinBoundaryError{Kind: "Ugreen response JSON reader absent"}
	}
	payload, err := ugreenAPICall(ctx, a, func() (json.RawMessage, error) { return response.JSON(ctx) })
	return response, payload, err
}
func (a *UgreenConcreteAPI) requestJSON(ctx context.Context, req UgreenSessionRequest) (json.RawMessage, error) {
	req.Method = strings.ToUpper(req.Method)
	if req.Method != "POST" {
		req.Method = "GET"
		req.JSON = nil
	} // Exact original dispatch, even PUT/PATCH.
	_, raw, err := a.requestRaw(ctx, req, true)
	if err != nil {
		if ugreenAPIOrdinary(err) {
			a.report("request", "RequestFailed")
			return json.RawMessage(`null`), nil
		}
		return nil, err
	}
	return raw, nil
}
