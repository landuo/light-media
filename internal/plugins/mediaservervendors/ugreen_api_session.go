package mediaservervendors

import (
	"context"
	"encoding/json"
	"strings"
	"unicode/utf8"

	"light-media/internal/releasemeta/legacyanime"
)

func ugreenAPIText(raw json.RawMessage) (string, error) {
	if !ejTruth(raw) {
		return "", nil
	}
	text, err := ejPythonText(raw, false)
	if err != nil {
		return "", err
	}
	return legacyanime.Strip(text), nil
}
func ugreenPublicKey(raw json.RawMessage) (string, error) {
	if !ejTruth(raw) {
		return "", nil
	}
	text, err := ejPythonText(raw, false)
	if err != nil {
		return "", err
	}
	text = legacyanime.Strip(text)
	if text == "" || strings.Contains(text, "BEGIN") {
		return text, nil
	}
	decoded, err := ugreenBase64Decode(text)
	if err != nil {
		if ugreenAPIOrdinary(err) {
			return "", nil
		}
		return "", err
	}
	if !utf8.Valid(decoded) {
		return "", nil
	}
	return string(decoded), nil
}
func ugreenFirstTruthy(fields ejMembers, keys ...string) json.RawMessage {
	for _, key := range keys {
		v := fields.get(key)
		if ejTruth(v) {
			return v
		}
	}
	return json.RawMessage(`""`)
}
func ugreenAPIRSAValue(payload json.RawMessage, headers []UgreenHTTPField) (json.RawMessage, error) {
	// Source headers are case-insensitive Requests headers; preserve the first
	// effective x-rsa-token lookup before root/data fallbacks.
	for _, field := range headers {
		if strings.EqualFold(field.Name, "x-rsa-token") && field.Value != "" {
			return ugreenKey(field.Value), nil
		}
	}
	fields, err := ejObject(payload)
	if err != nil {
		return nil, ejSource("AttributeError")
	}
	value := ugreenFirstTruthy(fields, "xRsaToken", "x-rsa-token")
	if ejTruth(value) {
		return value, nil
	}
	data, err := ejObject(fields.get("data"))
	if err != nil {
		return json.RawMessage(`null`), nil
	}
	return ugreenFirstTruthy(data, "xRsaToken", "x-rsa-token"), nil
}
func (a *UgreenConcreteAPI) Login(ctx context.Context, username, password json.RawMessage) (json.RawMessage, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.current(ctx); err != nil {
		return nil, err
	}
	if !ejTruth(username) || !ejTruth(password) {
		return json.RawMessage(`null`), nil
	}
	headers := a.commonHeaders()
	response, checkJSON, err := a.requestRaw(ctx, UgreenSessionRequest{Method: "POST", URL: a.host + "/ugreen/v1/verify/check", Headers: headers, JSON: ugreenAPIObject("username", username)}, true)
	if err != nil {
		if ugreenAPIOrdinary(err) {
			a.report("login_check", "RequestFailed")
			return json.RawMessage(`null`), nil
		}
		return nil, err
	}
	check, err := ugreenBuildAPIResult(checkJSON)
	if err != nil {
		return nil, err
	}
	if !check.Success {
		a.report("login_check", "APIRejected")
		return json.RawMessage(`null`), nil
	}
	rsaToken, err := ugreenAPIRSAValue(checkJSON, response.Headers)
	if err != nil {
		return nil, err
	}
	loginKey, err := ugreenPublicKey(rsaToken)
	if err != nil {
		return nil, err
	}
	if loginKey == "" {
		a.report("login_check", "MissingPublicKey")
		return json.RawMessage(`null`), nil
	}
	loginCipher, err := newUgreenCipher(loginKey, a.deps.Entropy)
	if err != nil {
		return nil, err
	}
	passwordText, err := ugreenUTF8(password)
	if err != nil {
		return nil, err
	}
	encryptedPassword, err := loginCipher.rsaEncryptLong(passwordText)
	if err != nil {
		return nil, err
	}
	payload, err := a.requestJSON(ctx, UgreenSessionRequest{Method: "POST", URL: a.host + "/ugreen/v1/verify/login", Headers: headers, JSON: ugreenAPIObject(
		"username", username, "password", ugreenKey(encryptedPassword), "keepalive", json.RawMessage(`true`), "otp", json.RawMessage(`true`), "is_simple", json.RawMessage(`true`))})
	if err != nil {
		return nil, err
	}
	if !ejTruth(payload) {
		return json.RawMessage(`null`), nil
	}
	result, err := ugreenBuildAPIResult(payload)
	if err != nil {
		return nil, err
	}
	data, mappingErr := ejObject(result.Data)
	if !result.Success || mappingErr != nil {
		a.report("login", "APIRejected")
		return json.RawMessage(`null`), nil
	}
	token, err := ugreenAPIText(ugreenFirstTruthy(data, "token", "token_id", "tokenId"))
	if err != nil {
		return nil, err
	}
	publicKey, err := ugreenPublicKey(ugreenFirstTruthy(data, "public_key", "publicKey"))
	if err != nil {
		return nil, err
	}
	if publicKey == "" {
		publicKey = loginKey
	}
	if token == "" || publicKey == "" {
		a.report("login", "MissingTokenOrKey")
		return json.RawMessage(`null`), nil
	}
	// Keep original partial-publication order if a later crypto constructor fails.
	a.token = ugreenKey(token)
	static, err := ugreenAPIText(ugreenFirstTruthy(data, "static_token", "staticToken"))
	if err != nil {
		return nil, err
	}
	if static == "" {
		static = token
	}
	a.staticToken = ugreenKey(static)
	a.isUGK = ejTruth(data.get("is_ugk"))
	a.publicKey = ugreenKey(publicKey)
	newCrypto, err := newUgreenCipher(publicKey, a.deps.Entropy)
	if err != nil {
		return nil, err
	}
	a.crypto = newCrypto
	a.username = ejCopy(username)
	return ejCopy(a.token), nil
}
func (a *UgreenConcreteAPI) ExportSession(ctx context.Context) (json.RawMessage, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.current(ctx); err != nil {
		return nil, err
	}
	if !ejTruth(a.token) || !ejTruth(a.publicKey) {
		return json.RawMessage(`null`), nil
	}
	cookies, err := ugreenAPICall(ctx, a, func() (json.RawMessage, error) { return a.session.Cookies(ctx) })
	if err != nil {
		return nil, err
	}
	return ugreenAPIObject("token", a.token, "static_token", a.staticToken, "is_ugk", ugreenAPIBool(a.isUGK), "public_key", a.publicKey, "username", a.username, "client_id", ugreenKey(a.clientID), "client_version", ugreenKey(a.clientVersion), "language", ugreenKey(a.language), "ug_agent", ugreenKey(a.agent), "cookies", cookies), nil
}
func (a *UgreenConcreteAPI) ImportSession(ctx context.Context, raw json.RawMessage) (bool, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.current(ctx); err != nil {
		return false, err
	}
	fields, err := ejObject(raw)
	if err != nil {
		return false, nil
	}
	token, err := ugreenAPIText(fields.get("token"))
	if err != nil {
		return false, err
	}
	publicKey, err := ugreenPublicKey(fields.get("public_key"))
	if err != nil {
		return false, err
	}
	if token == "" || publicKey == "" {
		return false, nil
	}
	static, err := ugreenAPIText(fields.get("static_token"))
	if err != nil {
		return false, err
	}
	isUGK := ejTruth(fields.get("is_ugk"))
	for _, item := range []struct {
		name  string
		value *string
	}{{"client_id", &a.clientID}, {"client_version", &a.clientVersion}, {"language", &a.language}, {"ug_agent", &a.agent}} {
		value, err := ugreenAPIText(fields.get(item.name))
		if err != nil {
			return false, err
		}
		if value != "" {
			*item.value = value
		}
	}
	username, err := ugreenAPIText(fields.get("username"))
	if err != nil {
		return false, err
	}
	a.username = json.RawMessage(`null`)
	if username != "" {
		a.username = ugreenKey(username)
	}
	if cookies, e := ejObject(fields.get("cookies")); e == nil {
		updates := []UgreenHTTPField{}
		for _, key := range cookies.keys {
			value := cookies.values[string(key)]
			if ejNull(value) {
				continue
			}
			k, e := ejText(key)
			if e != nil {
				return false, e
			}
			v, e := ejPythonText(value, false)
			if e != nil {
				return false, e
			}
			updates = append(updates, UgreenHTTPField{k, v})
		}
		_, e = ugreenAPICall(ctx, a, func() (bool, error) { return true, a.session.UpdateCookies(ctx, updates) })
		if e != nil && !ugreenAPIOrdinary(e) {
			return false, e
		}
	}
	a.token = ugreenKey(token)
	if static == "" {
		static = token
	}
	a.staticToken = ugreenKey(static)
	a.isUGK = isUGK
	a.publicKey = ugreenKey(publicKey)
	newCrypto, err := newUgreenCipher(publicKey, a.deps.Entropy)
	if err != nil {
		return false, err
	}
	a.crypto = newCrypto
	return true, nil
}
func (a *UgreenConcreteAPI) Logout(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.current(ctx); err != nil {
		return err
	}
	if !ejTruth(a.token) || a.crypto == nil {
		return nil
	}
	req, _, err := a.encryptedRequest("v1/verify/logout", "GET", json.RawMessage(`{}`), nil)
	if err == nil {
		_, _, err = a.requestRaw(ctx, req, false)
	} // No response JSON callback.
	if err != nil && !ugreenAPIOrdinary(err) {
		return err
	}
	a.token = json.RawMessage(`null`)
	a.staticToken = json.RawMessage(`null`)
	a.isUGK = false
	a.publicKey = json.RawMessage(`null`)
	a.crypto = nil
	a.username = json.RawMessage(`null`)
	return nil
}
