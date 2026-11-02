package app

import (
	"encoding/json"
	"errors"
	"io"
	"math"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Scoped source HTTP errors: never put input or upstream diagnostics in Error(),
// logs, or unrelated application's error envelopes.
type originalPluginServerHTTPError struct {
	status int
	body   any
	text   bool
}

func (e *originalPluginServerHTTPError) Error() string {
	return "plugin statistics/rating request failed"
}
func originalPluginServerFailure() error {
	return &originalPluginServerHTTPError{status: 500, text: true}
}
func writeOriginalPluginServerError(w http.ResponseWriter, err error) bool {
	var e *originalPluginServerHTTPError
	if !errors.As(err, &e) {
		return false
	}
	if e.text {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(e.status)
		_, _ = io.WriteString(w, "Internal Server Error")
		return true
	}
	raw, encodeErr := json.Marshal(e.body)
	if encodeErr != nil {
		return writeOriginalPluginServerError(w, originalPluginServerFailure())
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(e.status)
	_, _ = w.Write(raw)
	return true
}
func originalRatingValidation(kind, message string, loc []any, input any, context map[string]any) error {
	if originalCenterInvalidDiagnostic(input) {
		return originalPluginServerFailure()
	}
	detail := map[string]any{"type": kind, "loc": loc, "msg": message, "input": input}
	if context != nil {
		detail["ctx"] = context
	}
	return &originalPluginServerHTTPError{422, map[string]any{"detail": []any{detail}}, false}
}
func originalCenterInvalidDiagnostic(value any) bool {
	switch value := value.(type) {
	case string:
		return !utf8.ValidString(value)
	case originalCenterUnpairedString:
		return true
	case float64:
		return math.IsNaN(value) || math.IsInf(value, 0)
	case []any:
		for _, v := range value {
			if originalCenterInvalidDiagnostic(v) {
				return true
			}
		}
	case map[string]any:
		for key, v := range value {
			if !utf8.ValidString(key) {
				return true
			}
			if originalCenterInvalidDiagnostic(v) {
				return true
			}
		}
	}
	return false
}

// Pydantic allows non-finite float values before applying field bounds. This
// must not relax originalRatingFloat, which also serves service/browser APIs.
func originalCenterFloat(value any) (float64, string) {
	var text string
	switch value := value.(type) {
	case bool:
		if value {
			return 1, ""
		}
		return 0, ""
	case float64:
		return value, ""
	case json.Number:
		text = string(value)
	case string:
		text = strings.TrimSpace(value)
		if strings.HasPrefix(text, "_") || strings.HasSuffix(text, "_") || strings.Contains(text, "__") {
			return 0, "float_parsing"
		}
		text = strings.ReplaceAll(text, "_", "")
	default:
		return 0, "float_type"
	}
	switch strings.ToLower(text) {
	case "nan", "+nan", "-nan":
		return math.NaN(), ""
	case "inf", "+inf", "infinity", "+infinity":
		return math.Inf(1), ""
	case "-inf", "-infinity":
		return math.Inf(-1), ""
	}
	if !originalRatingDecimal.MatchString(text) {
		return 0, "float_parsing"
	}
	v, e := strconv.ParseFloat(text, 64)
	if e != nil && !math.IsInf(v, 0) && v != 0 {
		return 0, "float_parsing"
	}
	return v, ""
}

func originalPluginRatingRequest(w http.ResponseWriter, r *http.Request) (float64, error) {
	raw, e := readOriginalPluginCenterBody(r.Context(), r.Body)
	if e != nil {
		return 0, &APIError{"PLUGIN_RATING_BODY_LIMIT", "插件评分请求无效或过大", http.StatusRequestEntityTooLarge}
	}
	bodyLoc := []any{"body"}
	if len(raw) == 0 {
		return 0, originalRatingValidation("missing", "Field required", bodyLoc, nil, nil)
	}
	var value any
	contentType := r.Header.Get("Content-Type")
	mediaType, _, mediaErr := mime.ParseMediaType(contentType)
	_, contentTypePresent := r.Header["Content-Type"]
	if contentTypePresent && (mediaErr != nil || !strings.HasPrefix(strings.ToLower(mediaType), "application/") || (strings.ToLower(mediaType) != "application/json" && !strings.HasSuffix(strings.ToLower(mediaType), "+json"))) {
		value = string(raw)
	} else {
		value, e = parseOriginalCenterJSON(raw)
		if e != nil {
			if errors.Is(e, errOriginalCenterEncoding) {
				return 0, &originalPluginServerHTTPError{400, map[string]any{"detail": "There was an error parsing the body"}, false}
			}
			var syntax *originalCenterJSONError
			if !errors.As(e, &syntax) {
				return 0, &originalPluginServerHTTPError{400, map[string]any{"detail": "There was an error parsing the body"}, false}
			}
			return 0, originalRatingValidation("json_invalid", "JSON decode error", []any{"body", syntax.position}, map[string]any{}, map[string]any{"error": syntax.message})
		}
	}
	if value == nil {
		return 0, originalRatingValidation("missing", "Field required", bodyLoc, nil, nil)
	}
	fields, ok := value.(map[string]any)
	if !ok {
		return 0, originalRatingValidation("model_attributes_type", "Input should be a valid dictionary or object to extract fields from", bodyLoc, value, nil)
	}
	input, exists := fields["rating"]
	loc := []any{"body", "rating"}
	if !exists {
		return 0, originalRatingValidation("missing", "Field required", loc, fields, nil)
	}
	rating, kind := originalCenterFloat(input)
	if kind != "" {
		message := "Input should be a valid number"
		if kind == "float_parsing" {
			message = "Input should be a valid number, unable to parse string as a number"
		}
		return 0, originalRatingValidation(kind, message, loc, input, nil)
	}
	// Pydantic checks multiple_of before bounds. NaN/Inf produce NaN here,
	// so the subsequent bounds check reports le/ge, not multiple_of.
	if math.Abs(rating-math.Round(rating/0.1)*0.1) > 1e-9 {
		return 0, originalRatingValidation("multiple_of", "Input should be a multiple of 0.1", loc, input, map[string]any{"multiple_of": 0.1})
	}
	if !(rating <= 5) {
		return 0, originalRatingValidation("less_than_equal", "Input should be less than or equal to 5", loc, input, map[string]any{"le": 5.0})
	}
	if !(rating >= 0.1) {
		return 0, originalRatingValidation("greater_than_equal", "Input should be greater than or equal to 0.1", loc, input, map[string]any{"ge": 0.1})
	}
	return rating, nil
}

func originalPluginCenterResponse(success bool, message string, data any) map[string]any {
	var text any
	if message != "" {
		text = message
	}
	return map[string]any{"success": success, "message": text, "message_i18n": text, "data": data}
}

// urlsplit/quote keep local repository IDs encoded as text, so percent signs
// are encoded again. This reports identity/version only, never the local path.
func originalPluginLocalReportRepository(raw string) string {
	raw = strings.NewReplacer("\t", "", "\r", "", "\n", "").Replace(raw)
	rest := strings.TrimPrefix(raw, "local://")
	noFragment, _, _ := strings.Cut(rest, "#")
	location, query, _ := strings.Cut(noFragment, "?")
	authority, path, hasPath := strings.Cut(location, "/")
	id := authority
	if id == "" && hasPath {
		id = strings.Trim(path, "/")
	}
	malformed := strings.Contains(authority, "[") != strings.Contains(authority, "]")
	if malformed {
		id = strings.Trim(strings.SplitN(rest, "?", 2)[0], "/")
		query = ""
	}
	// A local identifier is not a credential transport. Ordinary URL credentials
	// remain redacted even for this unusual synthetic repository representation.
	if at := strings.LastIndex(id, "@"); at >= 0 {
		id = id[at+1:]
	}
	if id == "" {
		return "local:"
	}
	quote := func(v string) string { return strings.ReplaceAll(url.QueryEscape(v), "+", "%20") }
	result := "local://" + quote(id)
	for _, part := range strings.Split(query, "&") {
		key, value, _ := strings.Cut(part, "=")
		key, _ = url.QueryUnescape(key)
		value, _ = url.QueryUnescape(value)
		if key == "version" && value != "" {
			result += "?version=" + quote(strings.ToValidUTF8(value, "\ufffd"))
			break
		}
	}
	return result
}
