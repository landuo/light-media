package mediaservervendors

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"math/big"
	"net/url"
	"strconv"
	"strings"

	"light-media/internal/releasemeta/legacyanime"
)

type UgreenAPIResult struct {
	Code             *big.Int
	Message          string
	Data, Debug, Raw json.RawMessage
	Success          bool
}

func ugreenAPIBool(v bool) json.RawMessage {
	if v {
		return json.RawMessage(`true`)
	}
	return json.RawMessage(`false`)
}
func ugreenAPIObject(pairs ...any) json.RawMessage {
	var b bytes.Buffer
	b.WriteByte('{')
	for i := 0; i < len(pairs); i += 2 {
		if i > 0 {
			b.WriteByte(',')
		}
		b.Write(ugreenKey(pairs[i].(string)))
		b.WriteByte(':')
		b.Write(pairs[i+1].(json.RawMessage))
	}
	b.WriteByte('}')
	return b.Bytes()
}
func ugreenAPICode(raw json.RawMessage) *big.Int {
	raw = bytes.TrimSpace(raw)
	if !json.Valid(raw) {
		return big.NewInt(-1)
	}
	switch raw[0] {
	case 'n', '[', '{':
		return big.NewInt(-1)
	case 't':
		return big.NewInt(1)
	case 'f':
		return big.NewInt(0)
	case '"':
		text, err := ejText(raw)
		if err != nil {
			return big.NewInt(-1)
		}
		n, err := legacyanime.Integer(text)
		if err != nil {
			return big.NewInt(-1)
		}
		return n
	default:
		if bytes.ContainsAny(raw, ".eE") {
			value, err := strconv.ParseFloat(string(raw), 64)
			if err != nil || math.IsInf(value, 0) || math.IsNaN(value) {
				return big.NewInt(-1)
			}
			n, _ := new(big.Float).SetFloat64(value).Int(nil)
			return n
		}
		n, ok := new(big.Int).SetString(string(raw), 10)
		if !ok {
			return big.NewInt(-1)
		}
		return n
	}
}
func ugreenBuildAPIResult(raw json.RawMessage) (UgreenAPIResult, error) {
	out := UgreenAPIResult{Code: big.NewInt(-1), Message: "响应格式错误", Data: json.RawMessage(`null`), Debug: json.RawMessage(`null`), Raw: json.RawMessage(`null`)}
	fields, err := ejObject(raw)
	if err != nil {
		return out, nil
	}
	out.Code = ugreenAPICode(fields.get("code"))
	out.Success = out.Code.Cmp(big.NewInt(200)) == 0
	message := fields.get("msg")
	if !ejTruth(message) {
		message = json.RawMessage(`""`)
	}
	out.Message, err = ejPythonText(message, false)
	if err != nil {
		return out, err
	}
	out.Data = ejCopy(fields.get("data"))
	out.Debug = ejCopy(fields.get("debug"))
	out.Raw = ugreenObjectRaw(fields)
	return out, nil
}
func (a *UgreenConcreteAPI) encryptedRequest(path, method string, params, data json.RawMessage) (UgreenSessionRequest, string, error) {
	var zero UgreenSessionRequest
	parsed, err := url.Parse(a.host + "/ugreen/" + strings.Trim(path, "/"))
	if err != nil {
		return zero, "", ejSource("ValueError")
	}
	if !ejTruth(params) {
		params = json.RawMessage(`{}`)
	}
	query, err := ugreenEncodeQuery(params)
	if err != nil {
		return zero, "", err
	}
	parts := []string{}
	if parsed.RawQuery != "" {
		parts = append(parts, parsed.RawQuery)
	}
	if query != "" {
		parts = append(parts, query)
	}
	parsed.RawQuery = ""
	parsed.ForceQuery = false
	key, err := a.crypto.generateAESKey()
	if err != nil {
		return zero, "", err
	}
	encrypted, err := a.crypto.encryptAES(strings.Join(parts, "&"), key)
	if err != nil {
		return zero, "", err
	}
	req := UgreenSessionRequest{URL: parsed.String(), Method: strings.ToUpper(method), Params: ugreenAPIObject("encrypt_query", ugreenKey(encrypted))}
	if !ejNull(data) {
		req.JSON = ejCopy(data)
		if req.Method == "POST" || req.Method == "PUT" || req.Method == "PATCH" {
			req.JSON, err = a.crypto.encryptBodyJSON(data, key)
			if err != nil {
				return zero, "", err
			}
		}
	}
	headers := []UgreenHTTPField{}
	if a.clientID != "" {
		headers = append(headers, UgreenHTTPField{"Client-Id", a.clientID})
	}
	if a.clientVersion != "" {
		headers = append(headers, UgreenHTTPField{"Client-Version", a.clientVersion})
	}
	if a.agent != "" {
		headers = append(headers, UgreenHTTPField{"UG-Agent", a.agent})
	}
	headers = append(headers, UgreenHTTPField{"X-Specify-Language", a.language}, UgreenHTTPField{"Accept", "application/json, text/plain, */*"})
	if ejTruth(a.token) {
		token, err := ejText(a.token)
		if err != nil {
			return zero, "", err
		}
		security, err := ugreenSecurityKey(token)
		if err != nil {
			return zero, "", err
		}
		code, err := a.crypto.rsaEncryptLong(key)
		if err != nil {
			return zero, "", err
		}
		tokenCipher, err := a.crypto.rsaEncryptLong(token)
		if err != nil {
			return zero, "", err
		}
		headers = append(headers, UgreenHTTPField{"X-Ugreen-Security-Key", security}, UgreenHTTPField{"X-Ugreen-Security-Code", code}, UgreenHTTPField{"X-Ugreen-Token", tokenCipher})
	}
	if req.JSON != nil {
		headers = append(headers, UgreenHTTPField{"Content-Type", "application/json"})
	}
	req.Headers = headers
	return req, key, nil
}
func ugreenAPIDecryptResponse(raw json.RawMessage, key string) (json.RawMessage, error) {
	fields, err := ejObject(raw)
	if err != nil {
		return ejCopy(raw), nil
	}
	encrypted := fields.get("encrypt_resp_body")
	if !ejTruth(encrypted) {
		return ejCopy(raw), nil
	}
	text, err := ejPythonText(encrypted, false)
	if err != nil {
		return nil, err
	}
	plain, err := ugreenDecryptAES(text, key)
	if err != nil {
		return nil, err
	}
	if json.Valid([]byte(plain)) {
		return json.RawMessage(plain), nil
	}
	return ugreenKey(plain), nil
}
func (a *UgreenConcreteAPI) request(ctx context.Context, path, method string, params, data json.RawMessage) (UgreenAPIResult, error) {
	if err := a.current(ctx); err != nil {
		return UgreenAPIResult{}, err
	}
	if a.crypto == nil {
		return UgreenAPIResult{Code: big.NewInt(-1), Message: "未登录"}, nil
	}
	req, key, err := a.encryptedRequest(path, method, params, data)
	if err != nil {
		return UgreenAPIResult{}, err
	}
	payload, err := a.requestJSON(ctx, req)
	if err != nil {
		return UgreenAPIResult{}, err
	}
	if ejNull(payload) {
		return UgreenAPIResult{Code: big.NewInt(-1), Message: "接口请求失败"}, nil
	}
	raw, err := ugreenAPIDecryptResponse(payload, key)
	if err != nil {
		return UgreenAPIResult{}, err
	}
	return ugreenBuildAPIResult(raw)
}
func (a *UgreenConcreteAPI) CurrentUser(ctx context.Context) (json.RawMessage, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	result, err := a.request(ctx, "v1/user/current/user", "GET", nil, nil)
	if err != nil {
		return nil, err
	}
	if !result.Success {
		return json.RawMessage(`null`), nil
	}
	fields, err := ejObject(result.Data)
	if err != nil {
		return json.RawMessage(`null`), nil
	}
	return ugreenObjectRaw(fields), nil
}
func (a *UgreenConcreteAPI) MediaList(ctx context.Context) (json.RawMessage, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	result, err := a.request(ctx, "v1/video/homepage/media_list", "GET", nil, nil)
	if err != nil {
		return nil, err
	}
	if !result.Success {
		return json.RawMessage(`null`), nil
	}
	fields, err := ejObject(result.Data)
	if err != nil {
		return json.RawMessage(`null`), nil
	}
	value := bytes.TrimSpace(fields.get("media_lib_info_list"))
	if len(value) == 0 || value[0] != '[' {
		return json.RawMessage(`null`), nil
	}
	return ejCopy(value), nil
}
func (a *UgreenConcreteAPI) FolderPage(ctx context.Context, page, pageSize int) (json.RawMessage, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	params := ugreenAPIObject("page", json.RawMessage(strconv.Itoa(page)), "page_size", json.RawMessage(strconv.Itoa(pageSize)), "sort_type", json.RawMessage(`1`), "order_type", json.RawMessage(`1`))
	result, err := a.request(ctx, "v1/video/poster_wall/media_lib/get_folder", "GET", params, nil)
	if err != nil {
		return nil, err
	}
	if !result.Success {
		return json.RawMessage(`null`), nil
	}
	if _, err := ejObject(result.Data); err != nil {
		return json.RawMessage(`null`), nil
	}
	return ejCopy(result.Data), nil
}
func (a *UgreenConcreteAPI) Scan(ctx context.Context, id json.RawMessage, scanType int) (bool, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	params := ugreenAPIObject("op_type", json.RawMessage(`2`), "media_lib_set_id", id, "media_lib_scan_type", json.RawMessage(strconv.Itoa(scanType)))
	result, err := a.request(ctx, "v1/video/media_lib/scan", "GET", params, nil)
	return result.Success, err
}
