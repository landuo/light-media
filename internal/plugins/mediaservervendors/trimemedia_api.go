package mediaservervendors

import (
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"light-media/internal/plugins/mediaserverruntime"
)

// These are resolved runtime dependencies, not fixed oracle constants. Signing
// uses real time/random observations supplied by the trusted owner; an omitted
// dependency is an error, never a healthy fake. AccessHeaders represent the
// separately resolved Requests/urllib3 default-header path for access-code GET.
type TrimeAPIProtocol struct {
	UserAgent     string
	AccessHeaders []TrimeHTTPHeader
	Nonce         func(context.Context) (int, error)
	Seconds       func(context.Context) (float64, error)
	Report        func(TrimeAPIDiagnostic)
}
type TrimeAPIDiagnostic struct{ Level, Stage, Class string }

// TrimeNativeAPI is the actual retained source Api object. It keeps the same
// private HTTP client/cookie jar across logout, close and subsequent operations.
// Short state locks never span callbacks/HTTP. Request failures do not roll back
// already published token/version; each original method determines assignment.
type TrimeNativeAPI struct {
	mu            sync.RWMutex
	http          *TrimeHTTP
	protocol      TrimeAPIProtocol
	host, apiKey  string
	accessCode    *string
	accessCodeRaw json.RawMessage
	token         *string
	version       *TrimeVersion
}

var _ TrimeAPI = (*TrimeNativeAPI)(nil)

func NewTrimeNativeAPI(ctx context.Context, transport *http.Transport, host, apiKey string, accessCode *string, current TrimeHTTPCurrent, decode TrimeHTTPJSON, protocol TrimeAPIProtocol) (*TrimeNativeAPI, error) {
	if e := trimeContext(ctx); e != nil {
		return nil, e
	}
	if protocol.Nonce == nil || protocol.Seconds == nil || protocol.Report == nil {
		return nil, errors.New("Trime Api requires resolved clock/random/diagnostic dependencies")
	}
	if accessCode != nil && *accessCode != "" && protocol.AccessHeaders == nil {
		return nil, errors.New("Trime access-code requires resolved source default header profile")
	}
	host = strings.TrimRight(host, "/")
	client, e := NewTrimeHTTP(transport, host, current, decode)
	if e != nil {
		return nil, e
	}
	protocol.AccessHeaders = append([]TrimeHTTPHeader(nil), protocol.AccessHeaders...)
	var code *string
	if accessCode != nil {
		copy := *accessCode
		code = &copy
	}
	return &TrimeNativeAPI{http: client, protocol: protocol, host: host, apiKey: apiKey, accessCode: code}, nil
}
func (a *TrimeNativeAPI) check(ctx context.Context) error {
	if a == nil || a.http == nil {
		return errors.New("Trime Api has no retained HTTP session")
	}
	return a.http.check(ctx)
}
func (a *TrimeNativeAPI) Token(ctx context.Context) (*string, error) {
	if e := a.check(ctx); e != nil {
		return nil, e
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.token == nil {
		return nil, nil
	}
	copy := *a.token
	return &copy, nil
}
func (a *TrimeNativeAPI) Host(ctx context.Context) (string, error) {
	if e := a.check(ctx); e != nil {
		return "", e
	}
	return a.host, nil
}
func (a *TrimeNativeAPI) Version(ctx context.Context) (*TrimeVersion, error) {
	if e := a.check(ctx); e != nil {
		return nil, e
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.version, nil
}
func (a *TrimeNativeAPI) report(level, stage, class string) {
	a.protocol.Report(TrimeAPIDiagnostic{level, stage, class})
}
func trimeAPIOrdinary(err error) (*mediaserverruntime.SourceError, bool) {
	source, ok := err.(*mediaserverruntime.SourceError)
	return source, ok && source != nil && source.Class != "" && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded)
}

// Only an explicitly classified source error may cross the HTTP url.Error
// wrapper. Unknown network errors/current-policy failures are not swallowed or
// retried. Full Requests network classification remains a distinct adapter gate.
func trimeAPIHTTPError(err error) error {
	if u, ok := err.(*url.Error); ok {
		if source, ordinary := trimeAPIOrdinary(u.Err); ordinary {
			return source
		}
	}
	return err
}
func (a *TrimeNativeAPI) requestUtils(ctx context.Context, method, address string, headers []TrimeHTTPHeader, body *string) (*TrimeHTTPResult, error) {
	response, err := a.http.exchange(ctx, method, address, headers, body)
	if err == nil {
		return response, nil
	}
	if e := a.check(ctx); e != nil {
		return nil, e
	}
	err = trimeAPIHTTPError(err)
	source, ordinary := trimeAPIOrdinary(err)
	if !ordinary {
		return nil, err
	}
	retryable := source.Class == "ConnectionError" || source.Class == "ConnectTimeout" || source.Class == "SSLError" || source.Class == "ProxyError" || source.Class == "ChunkedEncodingError" || source.Class == "ReadTimeout"
	verb := strings.ToUpper(method)
	if retryable && (verb == "GET" || verb == "HEAD" || verb == "OPTIONS") {
		a.report("DEBUG", "request-retry", source.Class)
		if e := a.http.Close(ctx); e != nil {
			return nil, e
		}
		response, err = a.http.exchange(ctx, method, address, headers, body)
		if err == nil {
			return response, nil
		}
		if e := a.check(ctx); e != nil {
			return nil, e
		}
		err = trimeAPIHTTPError(err)
		source, ordinary = trimeAPIOrdinary(err)
		if !ordinary {
			return nil, err
		}
		// Only actual RequestException classes are caught by RequestUtils. Other
		// explicitly ordinary source errors escape to Api.request's own catch.
		if !trimeRequestException(source.Class) {
			return nil, err
		}
		a.report("DEBUG", "request-retry-failed", source.Class)
		return nil, nil
	}
	if !trimeRequestException(source.Class) {
		return nil, err
	}
	stage := "request-failed"
	if retryable {
		stage = "request-no-retry"
	}
	a.report("DEBUG", stage, source.Class)
	return nil, nil
}
func trimeRequestException(class string) bool {
	switch class {
	case "RequestException", "ConnectionError", "ConnectTimeout", "ReadTimeout", "Timeout", "ChunkedEncodingError", "HTTPError", "TooManyRedirects", "SSLError", "ProxyError", "InvalidURL", "MissingSchema", "InvalidSchema", "InvalidHeader", "ContentDecodingError":
		return true
	}
	return false
}

type trimeAPIResult struct {
	code    *big.Int
	message any
	data    any
}

func (r *trimeAPIResult) success() bool { return r != nil && r.code.Sign() == 0 }

// Original six-operation callers supply no query params. Keep that seam absent
// rather than invent an arbitrary query serializer or public endpoint RPC.
func (a *TrimeNativeAPI) request(ctx context.Context, path, method string, data trimeJSONObject) (*trimeAPIResult, error) {
	if e := a.check(ctx); e != nil {
		return nil, e
	}
	if a.host == "" || path == "" {
		return nil, nil
	}
	apiPath := "/api/v1"
	if !strings.HasPrefix(path, "/") {
		apiPath += "/"
	}
	apiPath += path
	if method == "" {
		method = "get"
		if data != nil {
			method = "post"
		}
	}
	var body *string
	if method != "get" {
		value := ""
		if len(data) > 0 {
			var e error
			value, e = trimeJSONBody(data)
			if e != nil {
				return nil, e
			}
		}
		body = &value
	}
	nonce, e := a.protocol.Nonce(ctx)
	if e != nil {
		return nil, e
	}
	seconds, e := a.protocol.Seconds(ctx)
	if e != nil {
		return nil, e
	}
	payload := ""
	if body != nil {
		payload = *body
	}
	auth, e := trimeAuthX(apiPath, payload, a.apiKey, nonce, seconds)
	if e != nil {
		return nil, e
	}
	token, e := a.Token(ctx)
	if e != nil {
		return nil, e
	}
	headers := []TrimeHTTPHeader{{"User-Agent", a.protocol.UserAgent}, {"Accept", "application/json"}, {"Referer", a.host}}
	if token != nil {
		headers = append(headers, TrimeHTTPHeader{"Authorization", *token})
	}
	headers = append(headers, TrimeHTTPHeader{"authx", auth})
	if body != nil {
		headers = append(headers, TrimeHTTPHeader{"Content-Type", "application/json"})
	}
	// Python's try starts after body/signature construction, so failures above
	// must escape. Only explicitly source ordinary failures below become None.
	fail := func(err error) (*trimeAPIResult, error) {
		if current := a.check(ctx); current != nil {
			return nil, current
		}
		if source, ordinary := trimeAPIOrdinary(err); ordinary {
			a.report("ERROR", "api-exception", source.Class)
			return nil, nil
		}
		return nil, err
	}
	response, e := a.requestUtils(ctx, method, a.host+apiPath, headers, body)
	if e != nil {
		return fail(e)
	}
	if response == nil || response.Status >= 400 {
		a.report("ERROR", "api-response-failed", "")
		return nil, nil
	}
	value, e := a.http.json(ctx, response)
	if e != nil {
		return fail(e)
	}
	object, ok := value.(map[string]any)
	if !ok {
		return fail(trimeAPISource("AttributeError", "response JSON has no get method"))
	}
	rawCode, present := object["code"]
	if !present {
		rawCode = int64(-1)
	}
	code, e := trimeAPIInteger(rawCode)
	if e != nil {
		return fail(e)
	}
	result := &trimeAPIResult{code: code, message: object["msg"]}
	if code.Sign() != 0 {
		a.report("ERROR", "api-code-failed", "")
		return result, nil
	}
	result.data = object["data"]
	return result, nil
}
func (a *TrimeNativeAPI) Close(ctx context.Context) error { return a.http.Close(ctx) }

func (a *TrimeNativeAPI) SetConstructorAccessCode(ctx context.Context, raw json.RawMessage) error {
	if err := a.check(ctx); err != nil {
		return err
	}
	if !json.Valid(raw) {
		return errors.New("invalid owned Trime access code JSON")
	}
	a.mu.Lock()
	a.accessCodeRaw = constructorJSON(raw)
	a.accessCode = constructorStringProjection(raw)
	a.mu.Unlock()
	return nil
}
