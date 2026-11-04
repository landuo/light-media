package app

import (
	"io"
	"net/http"
)

// The sole route is POST /api/system/setting/MediaServers, below the existing
// administrator session, same-origin and Sec-Fetch-Site write checks. No raw
// GET or arbitrary-key setter is exposed. Saving a new fact is the existing
// source operation, not an additional required initialization step.
func (a *App) originalMediaServerConfigSettingAPI(w http.ResponseWriter, r *http.Request) (any, error) {
	if r.Method != http.MethodPost || r.URL.Path != "/api/system/setting/MediaServers" {
		return nil, &APIError{Code: "NOT_FOUND", Message: "接口不存在", Status: http.StatusNotFound}
	}
	// This is exactly the current decodeBody global HTTP budget, not a new
	// MediaServers quota or an expansion of the global parser. Read once so an
	// actually absent optional body is None, but whitespace/invalid JSON is not.
	var raw []byte
	if r.Body != nil {
		var err error
		raw, err = io.ReadAll(http.MaxBytesReader(w, r.Body, 2<<20))
		if err != nil {
			return nil, bad("INVALID_JSON", "请求内容无效或过大")
		}
	}
	value, err := originalMediaServerSettingBody(raw)
	if err != nil {
		return nil, err
	}
	state := a.originalMediaServerConfig
	if state == nil {
		return nil, bad("MEDIA_SERVER_CONFIG_NOT_ESTABLISHED", "原媒体服务器配置尚未装配")
	}
	changed, err := state.set(r.Context(), value)
	if err != nil {
		return nil, err
	}
	if changed != nil && *changed {
		// Credential-safe existing config event policy. This intentionally is not
		// the original full raw value; an ordinary listener cannot read credentials.
		// Current authorized SDK reads the committed fact through its exact grant.
		a.emitStandardBusinessEvent(r.Context(), "config.updated", map[string]any{
			"key": []string{originalMediaServerConfigKey}, "value": nil, "change_type": "update",
		})
	}
	return map[string]bool{"success": true}, nil
}
