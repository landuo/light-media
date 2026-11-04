package app

import (
	"bytes"
	"encoding/json"
	"net/http"

	"light-media/internal/notice"
)

// Admit the source's optional JSON Body Union[list, dict, bool, int, str].
// This private helper does not change global decodeBody, authorization, the
// existing HTTP/store budgets, or native APIError rendering. Source v1 detailed
// 422 payloads echo request input; the native response intentionally does not.
func originalMediaServerSettingBody(raw []byte) (json.RawMessage, error) {
	if len(raw) == 0 {
		raw = []byte(`null`)
	}
	if !json.Valid(raw) {
		return nil, &APIError{Code: "INVALID_JSON", Message: "请求JSON内容无效", Status: http.StatusUnprocessableEntity}
	}
	value := bytes.TrimSpace(raw)
	if value[0] == '-' || value[0] >= '0' && value[0] <= '9' {
		// Reuse the existing Pydantic integer conversion, not Python int(value),
		// truncation, or a new numeric range. Original JSON integers remain ints.
		// For integral JSON floats, bool occurs before int in the original Union;
		// The actual original three-number annex selects false/true for 0.0/1.0
		// and integer 2 for 2.0. Reuse the existing conversion for the remaining
		// integral numeric domain; do not claim the annex exhausts that domain.
		integer, ok := notice.CoerceSourceInteger(json.Number(value))
		if !ok {
			return nil, &APIError{Code: "INVALID_MEDIA_SERVER_CONFIG", Message: "请求内容不符合媒体服务器设置类型", Status: http.StatusUnprocessableEntity}
		}
		if bytes.ContainsAny(value, ".eE") && (integer == "0" || integer == "1") {
			if integer == "0" {
				value = json.RawMessage(`false`)
			} else {
				value = json.RawMessage(`true`)
			}
		} else {
			value = json.RawMessage(integer)
		}
	}
	// The original list filter and all existing store JSON/codepoint protections
	// remain in their existing, already verified owner implementation.
	return originalMediaServerSettingValue(value)
}
