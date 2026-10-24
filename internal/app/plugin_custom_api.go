package app

// Direct registered API transport and request authority. The retired queued
// API executor and its persisted invocation scopes are not part of this module.
import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"light-media/internal/plugins"
	"net/http"
	"net/url"
	"path"
	"slices"
	"strings"
	"time"
)

type pluginAPIAuthority struct {
	Kind  string `json:"kind"`
	Proof string `json:"proof,omitempty"`
}

type pluginHTTPFailure struct {
	Status int
	Detail any
}

func apiKeyEqual(actual, expected string) bool {
	if actual == "" || expected == "" || len(actual) > 4096 {
		return false
	}
	a, b := sha256.Sum256([]byte(actual)), sha256.Sum256([]byte(expected))
	return subtle.ConstantTimeCompare(a[:], b[:]) == 1
}

func (a *App) pluginAPIKeyProof(token string) string {
	mac := hmac.New(sha256.New, a.Store.key)
	mac.Write([]byte("light-media/plugin-api-key/v1\x00"))
	mac.Write([]byte(token))
	return hex.EncodeToString(mac.Sum(nil))
}

func (a *App) pluginHTTPAuthority(r *http.Request, auth string, native bool) (pluginAPIAuthority, *pluginHTTPFailure) {
	fail := func(status int, detail string) (pluginAPIAuthority, *pluginHTTPFailure) {
		return pluginAPIAuthority{}, &pluginHTTPFailure{status, detail}
	}
	if auth == "anonymous" {
		return pluginAPIAuthority{Kind: "anonymous"}, nil
	}
	query := r.URL.Query()
	key := r.Header.Get("X-API-KEY")
	if key == "" {
		key = plugins.LastQuery(query, "apikey")
	}
	checkKey := func(value, label string) (pluginAPIAuthority, *pluginHTTPFailure) {
		if !apiKeyEqual(value, a.Settings().APIToken) {
			return fail(401, label+" 校验不通过")
		}
		return pluginAPIAuthority{Kind: "key", Proof: a.pluginAPIKeyProof(value)}, nil
	}
	if auth == "apikey" {
		return checkKey(key, "apikey")
	}
	authorization := strings.Fields(r.Header.Get("Authorization"))
	if len(authorization) > 0 && strings.EqualFold(authorization[0], "Bearer") {
		if len(authorization) != 2 || !a.validPluginBearer(r.Context(), tokenHash(authorization[1])) {
			return fail(403, "token校验不通过")
		}
		return pluginAPIAuthority{Kind: "session", Proof: tokenHash(authorization[1])}, nil
	}
	if key != "" {
		return checkKey(key, "apikey")
	}
	if token := plugins.LastQuery(query, "token"); token != "" {
		return checkKey(token, "token")
	}
	// Native same-origin plugin pages use the existing session. Compatibility
	// aliases retain the original explicit bearer/key requirements.
	if native && a.authenticated(r) {
		if !sameRequestOrigin(r) || r.Header.Get("Sec-Fetch-Site") == "cross-site" {
			return fail(403, "请求来源不匹配")
		}
		cookie, _ := r.Cookie(sessionCookie(r, "", 0).Name)
		return pluginAPIAuthority{Kind: "session", Proof: tokenHash(cookie.Value)}, nil
	}
	return fail(401, "Not authenticated")
}

func (a *App) validPluginBearer(ctx context.Context, hash string) bool {
	var expires int64
	err := a.Store.DB.QueryRowContext(ctx, `SELECT expires_at FROM sessions WHERE token_hash=?`, hash).Scan(&expires)
	return err == nil && expires > time.Now().Unix()
}

func pluginCustomPath(value string) (instance, endpoint string, native, matched bool) {
	for _, prefix := range []string{"/api/v1/plugin/", "/api/v2/plugin/"} {
		if strings.HasPrefix(value, prefix) {
			parts := strings.SplitN(strings.TrimPrefix(value, prefix), "/", 2)
			if len(parts) != 2 {
				return "", "", false, true
			}
			return parts[0], "/" + parts[1], false, true
		}
	}
	if strings.HasPrefix(value, "/api/plugins/") {
		parts := strings.SplitN(strings.TrimPrefix(value, "/api/plugins/"), "/", 3)
		if len(parts) >= 2 && parts[1] == "api" {
			if len(parts) != 3 {
				return "", "", true, true
			}
			return parts[0], "/" + parts[2], true, true
		}
	}
	return "", "", false, false
}

func pluginHTTPJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(body)
}

func pluginHTTPError(w http.ResponseWriter, failure *pluginHTTPFailure) {
	pluginHTTPJSON(w, failure.Status, map[string]any{"detail": failure.Detail})
}

func (a *App) servePluginCustomAPI(w http.ResponseWriter, r *http.Request) bool {
	instance, endpoint, native, matched := pluginCustomPath(r.URL.Path)
	if !matched {
		return false
	}
	if path.Clean(r.URL.Path) != r.URL.Path || strings.ContainsAny(r.URL.Path, "\\\x00") || strings.Contains(strings.ToLower(r.URL.RawPath), "%2f") || strings.Contains(strings.ToLower(r.URL.RawPath), "%5c") {
		pluginHTTPError(w, &pluginHTTPFailure{400, "Invalid path"})
		return true
	}
	if !pluginStorageID.MatchString(instance) {
		pluginHTTPError(w, &pluginHTTPFailure{404, "Not Found"})
		return true
	}
	installation, err := a.pluginStore.Installation(r.Context(), instance)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		pluginHTTPError(w, &pluginHTTPFailure{503, "Plugin storage unavailable"})
		return true
	}
	implementation, implementationErr := a.resolvePluginInvocation(r.Context(), installation)
	manifest := implementation.Manifest
	if err != nil || implementationErr != nil {
		pluginHTTPError(w, &pluginHTTPFailure{404, "Not Found"})
		return true
	}
	// Original get_plugin_apis exposes routes registered from loaded objects,
	// irrespective of the plugin business-enabled setting.
	registered, registeredOK := a.pluginRegisteredAPIs.Load(instance)
	if !registeredOK {
		pluginHTTPError(w, &pluginHTTPFailure{404, "Not Found"})
		return true
	}
	manifest.APIs = registered.([]plugins.HTTPAPI)
	var api *plugins.HTTPAPI
	var params map[string]string
	allowed := []string{}
	for _, definition := range manifest.APIs {
		values, ok := definition.Match(endpoint)
		if !ok {
			continue
		}
		allowed = append(allowed, definition.Methods...)
		if slices.Contains(definition.Methods, r.Method) {
			copy := definition
			api = &copy
			params = values
			break
		}
	}
	if api == nil {
		status, detail := 404, "Not Found"
		if len(allowed) > 0 {
			status, detail = 405, "Method Not Allowed"
			w.Header().Set("Allow", strings.Join(allowed, ", "))
		}
		pluginHTTPError(w, &pluginHTTPFailure{status, detail})
		return true
	}
	authority, failure := a.pluginHTTPAuthority(r, api.Auth, native)
	if failure != nil {
		pluginHTTPError(w, failure)
		return true
	}
	if len(r.URL.RawQuery) > 64<<10 {
		pluginHTTPError(w, &pluginHTTPFailure{413, "Query too large"})
		return true
	}
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		pluginHTTPError(w, &pluginHTTPFailure{400, "Invalid query"})
		return true
	}
	missing := []map[string]any{}
	input := plugins.HTTPRequest{RequestID: w.Header().Get("X-Request-ID"), Key: api.Key, Method: r.Method, Path: endpoint, Params: params, Query: map[string][]string{}, CredentialMatches: map[string]bool{}, Headers: map[string]string{}}
	for key, values := range query {
		input.Query[key] = slices.Clone(values)
	}
	for _, parameter := range api.Query {
		values, present := query[parameter.Name]
		if parameter.Required && !present {
			missing = append(missing, map[string]any{"type": "missing", "loc": []string{"query", parameter.Name}, "msg": "Field required", "input": nil})
		}
		if parameter.Kind == "api_key" && present {
			value := ""
			if len(values) > 0 {
				value = values[len(values)-1]
			}
			input.CredentialMatches[parameter.Name] = apiKeyEqual(value, a.Settings().APIToken)
			delete(input.Query, parameter.Name)
		}
	}
	if len(missing) > 0 {
		pluginHTTPError(w, &pluginHTTPFailure{422, missing})
		return true
	}
	if api.Auth != "anonymous" {
		delete(input.Query, "apikey")
	}
	if api.Auth == "bear" {
		delete(input.Query, "token")
	}
	for _, header := range api.Headers {
		if value := r.Header.Get(header); value != "" {
			input.Headers[header] = value
		}
	}
	if api.Body != "" && r.Body != nil {
		bodyReader := io.Reader(http.MaxBytesReader(w, r.Body, 512<<10))
		body, err := io.ReadAll(bodyReader)
		if err != nil {
			pluginHTTPError(w, &pluginHTTPFailure{413, "Request body too large"})
			return true
		}
		if len(body) > 0 {
			if api.Body == "json" && !json.Valid(body) {
				pluginHTTPError(w, &pluginHTTPFailure{422, "Invalid JSON body"})
				return true
			}
			input.Body = body
		}
	}
	if api.BodyRequired && len(input.Body) == 0 {
		pluginHTTPError(w, &pluginHTTPFailure{422, []map[string]any{{"type": "missing", "loc": []string{"body"}, "msg": "Field required", "input": nil}}})
		return true
	}
	// A registered original API is already bound to a loaded instance. Its
	// own implementation performs configuration edits. No first-write load,
	// alternate configuration prepare/CAS, or extra framework init is inserted.
	if implementation.External {
		response, err := a.callOriginalExternalHTTP(r.Context(), installation, *api, input, authority)
		if err != nil {
			status := 502
			var failure *APIError
			if errors.As(err, &failure) && failure.Code == "PLUGIN_API_AUTH_REVOKED" {
				status = 401
			}
			pluginHTTPError(w, &pluginHTTPFailure{status, "Plugin execution failed"})
			return true
		}
		for key, value := range response.Headers {
			w.Header().Set(key, value)
		}
		if w.Header().Get("Content-Type") == "" {
			w.Header().Set("Content-Type", "application/json")
		}
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(response.Status)
		w.Write(response.Body)
		return true
	}
	pluginHTTPError(w, &pluginHTTPFailure{404, "Plugin adapter unavailable"})
	return true
}

func (a *App) validatePluginHTTPAuthorityTx(ctx context.Context, tx *sql.Tx, authority pluginAPIAuthority) error {
	switch authority.Kind {
	case "anonymous":
		return nil
	case "key":
		if a.Settings().APIToken != "" && hmac.Equal([]byte(authority.Proof), []byte(a.pluginAPIKeyProof(a.Settings().APIToken))) {
			return nil
		}
	case "session":
		var expires int64
		if err := tx.QueryRowContext(ctx, `SELECT expires_at FROM sessions WHERE token_hash=?`, authority.Proof).Scan(&expires); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		} else if err == nil && expires > time.Now().Unix() {
			return nil
		}
	}
	return bad("PLUGIN_API_AUTH_REVOKED", "插件API凭据已变化或失效")
}
