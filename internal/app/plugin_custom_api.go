package app

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
	"maps"
	"net/http"
	"net/url"
	"path"
	"slices"
	"strings"
	"time"

	"light-media/internal/plugins"
	"light-media/internal/plugins/builtin"
)

type pluginAPIAuthority struct {
	Kind  string `json:"kind"`
	Proof string `json:"proof,omitempty"`
}
type pluginAPIScope struct {
	Key       string             `json:"key"`
	Method    string             `json:"method"`
	RequestID string             `json:"request_id"`
	Authority pluginAPIAuthority `json:"authority"`
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

// Custom endpoints bypass the management API envelope, not its installation,
// identity or origin boundaries. No request may register a route or capability.
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
	manifest, _, exists := builtin.Lookup(installation.PluginID)
	if err != nil || !installation.Installed || !installation.DesiredEnabled || !exists || manifest.Version != installation.Version {
		pluginHTTPError(w, &pluginHTTPFailure{404, "Not Found"})
		return true
	}
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
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 512<<10))
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
	a.pluginMu.Lock()
	run, err := a.enqueuePluginHTTP(r.Context(), installation, input, pluginAPIScope{Key: api.Key, Method: r.Method, RequestID: w.Header().Get("X-Request-ID"), Authority: authority})
	a.pluginMu.Unlock()
	if err == nil {
		a.wakePlugins()
		response, err := a.waitPluginHTTP(r.Context(), run)
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
	status, detail := 409, "Plugin changed"
	var apiError *APIError
	if errors.As(err, &apiError) {
		switch apiError.Code {
		case "PLUGIN_API_AUTH_REVOKED":
			status, detail = 401, "API credential revoked"
		case "PLUGIN_QUEUE_FULL":
			status, detail = 503, "Plugin busy"
		case "INVALID_PLUGIN_VALUE":
			status, detail = 413, "Request too large"
		}
	}
	pluginHTTPError(w, &pluginHTTPFailure{status, detail})
	return true
}

func (a *App) enqueuePluginHTTP(ctx context.Context, installation PluginInstallation, input plugins.HTTPRequest, scope pluginAPIScope) (PluginRun, error) {
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return PluginRun{}, err
	}
	defer tx.Rollback()
	var current int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM plugin_installations WHERE instance_id=? AND plugin_id=? AND version=? AND generation=? AND installed=1 AND desired_enabled=1`, installation.InstanceID, installation.PluginID, installation.Version, installation.Generation).Scan(&current); err != nil {
		return PluginRun{}, err
	}
	if current != 1 {
		return PluginRun{}, bad("PLUGIN_SNAPSHOT_STALE", "插件API实例已变化")
	}
	var total, local int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(SUM(c.instance_id=?),0) FROM plugin_api_calls c JOIN plugin_runs r ON r.id=c.run_id WHERE r.state IN ('pending','running')`, installation.InstanceID).Scan(&total, &local); err != nil {
		return PluginRun{}, err
	}
	if total >= 64 || local >= 16 {
		return PluginRun{}, bad("PLUGIN_QUEUE_FULL", "插件API执行队列已满")
	}
	if err = a.validatePluginHTTPAuthorityTx(ctx, tx, scope.Authority); err != nil {
		return PluginRun{}, err
	}
	raw, err := json.Marshal(input)
	if err != nil {
		return PluginRun{}, err
	}
	run, err := a.pluginStore.enqueueRunTx(ctx, tx, installation.InstanceID, "api", raw)
	if err != nil {
		return run, err
	}
	clear, _ := json.Marshal(scope)
	sealed, err := a.pluginStore.seal("api_scope", run.InstanceID, run.ID, clear)
	if err != nil {
		return run, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO plugin_api_calls VALUES(?,?,?,?,?)`, run.ID, run.InstanceID, run.Generation, run.ConfigRevision, sealed); err != nil {
		return run, err
	}
	return run, tx.Commit()
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
func (a *App) validatePluginHTTPRunTx(ctx context.Context, tx *sql.Tx, run PluginRun) (plugins.HTTPRequest, error) {
	var input plugins.Invocation
	var request plugins.HTTPRequest
	var scope pluginAPIScope
	var sealed []byte
	if run.Action != "api" || json.Unmarshal(run.Input, &input) != nil || !pluginStrictJSON(input.Input, &request) {
		return request, bad("PLUGIN_API_SCOPE_DENIED", "插件API执行输入无效")
	}
	var pluginID string
	err := tx.QueryRowContext(ctx, `SELECT c.scope,i.plugin_id FROM plugin_api_calls c JOIN plugin_runs r ON r.id=c.run_id JOIN plugin_installations i ON i.instance_id=c.instance_id JOIN plugin_values v ON v.instance_id=i.instance_id AND v.kind='config' AND v.item_key='main' WHERE c.run_id=? AND c.instance_id=? AND c.generation=? AND c.config_revision=? AND i.version=? AND r.version=i.version AND r.state IN ('pending','running','succeeded') AND i.installed=1 AND i.desired_enabled=1 AND i.generation=c.generation AND r.generation=c.generation AND r.config_revision=c.config_revision AND v.revision=c.config_revision`, run.ID, run.InstanceID, run.Generation, run.ConfigRevision, run.Version).Scan(&sealed, &pluginID)
	if err != nil {
		return request, bad("PLUGIN_API_SCOPE_DENIED", "插件API执行未获得宿主授权")
	}
	raw, err := a.pluginStore.unseal("api_scope", run.InstanceID, run.ID, sealed)
	if err != nil {
		return request, err
	}
	if !pluginStrictJSON(raw, &scope) || scope.Key != request.Key || scope.Method != request.Method || scope.RequestID != request.RequestID {
		return request, bad("PLUGIN_API_SCOPE_DENIED", "插件API授权与执行不一致")
	}
	manifest, _, available := builtin.Lookup(pluginID)
	authorized := false
	if available && manifest.Version == run.Version {
		for _, api := range manifest.APIs {
			if api.Key != scope.Key || !slices.Contains(api.Methods, scope.Method) {
				continue
			}
			params, matched := api.Match(request.Path)
			if !matched || !maps.Equal(params, request.Params) {
				continue
			}
			authorized = api.Auth == "anonymous" && scope.Authority.Kind == "anonymous" || api.Auth == "apikey" && scope.Authority.Kind == "key" || api.Auth == "bear" && slices.Contains([]string{"key", "session"}, scope.Authority.Kind)
		}
	}
	if !authorized {
		return request, bad("PLUGIN_API_SCOPE_DENIED", "插件API未声明当前路由、方法或鉴权方式")
	}
	return request, a.validatePluginHTTPAuthorityTx(ctx, tx, scope.Authority)
}
func (a *App) validatePluginHTTPRun(ctx context.Context, run PluginRun) error {
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = a.validatePluginHTTPRunTx(ctx, tx, run)
	return err
}
func (a *App) waitPluginHTTP(ctx context.Context, run PluginRun) (plugins.HTTPResponse, error) {
	timer := time.NewTicker(25 * time.Millisecond)
	defer timer.Stop()
	for {
		current, err := a.pluginStore.Run(ctx, run.ID)
		if err != nil {
			return plugins.HTTPResponse{}, err
		}
		switch current.State {
		case "succeeded":
			if err := a.validatePluginHTTPRun(ctx, current); err != nil {
				return plugins.HTTPResponse{}, err
			}
			var response plugins.HTTPResponse
			if !pluginStrictJSON(current.Result, &response) || response.Validate() != nil {
				return response, bad("PLUGIN_API_RESPONSE_INVALID", "插件API响应无效")
			}
			return response, nil
		case "failed", "cancelled", "reconcile_pending":
			if current.ErrorCode == "PLUGIN_API_AUTH_REVOKED" {
				return plugins.HTTPResponse{}, bad(current.ErrorCode, "插件API凭据已失效")
			}
			return plugins.HTTPResponse{}, bad("PLUGIN_API_EXECUTION_FAILED", "插件API执行失败或等待恢复")
		}
		select {
		case <-ctx.Done():
			return plugins.HTTPResponse{}, ctx.Err()
		case <-timer.C:
		}
	}
}
