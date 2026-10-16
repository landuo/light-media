package autosignin

import (
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"crypto/sha256"
	_ "embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"light-media/internal/releasemeta/legacyanime"
)

// Public frontend protocol constants from the installed source, not any user's
// credentials. Deliberately separate from the host-only Credentials input.
//
//go:embed mteam-protocol.json
var mteamProtocolJSON []byte
var mteamProtocol = func() struct {
	Path           string   `json:"_api_path"`
	Secret         string   `json:"_sign_secret"`
	Version        string   `json:"_web_version"`
	SpecialDomains []string `json:"special_domains"`
} {
	var p struct {
		Path           string   `json:"_api_path"`
		Secret         string   `json:"_sign_secret"`
		Version        string   `json:"_web_version"`
		SpecialDomains []string `json:"special_domains"`
	}
	if json.Unmarshal(mteamProtocolJSON, &p) != nil {
		panic("invalid pinned mteam protocol")
	}
	return p
}()

// Credentials and RequestPlan are host-internal types, never worker RPC values.
// A real host must bind the current registered site and authorize its compiled
// adapter targets before passing these values to any transport. Match is not an
// authorization decision and must never grant access to arbitrary subdomains.
type Credentials struct {
	URL, Token, UserAgent string
	Proxy, Timeout        any
}
type RequestPlan struct {
	Method, URL   string
	Headers, Data map[string]string
	UseProxy      bool
	Timeout       any
}
type AdapterResponse struct {
	Status    int
	Payload   any
	JSONError error
}
type SiteTransport interface {
	Post(context.Context, RequestPlan) (*AdapterResponse, error)
}

func MTeamMatch(address string) bool {
	for _, part := range strings.Split(address, ".") {
		if part == "m-team" {
			return true
		}
	}
	return false
}
func MTeamSign(timestamp int64) string {
	message := "POST&" + mteamProtocol.Path + "&" + strconv.FormatInt(timestamp, 10)
	hash := hmac.New(sha1.New, []byte(mteamProtocol.Secret))
	hash.Write([]byte(message))
	return base64.StdEncoding.EncodeToString(hash.Sum(nil))
}
func MTeamHeaders(address, token, agent string, timestamp int64) map[string]string {
	fingerprint := sha256.Sum256([]byte(token))
	text := hex.EncodeToString(fingerprint[:])
	seconds := timestamp / 1000
	if timestamp < 0 && timestamp%1000 != 0 {
		seconds--
	}
	headers := map[string]string{"Content-Type": "application/x-www-form-urlencoded; charset=UTF-8", "Accept": "application/json, text/plain, */*", "Authorization": token, "Origin": address, "Referer": address + "/index", "Ts": strconv.FormatInt(seconds, 10), "webversion": mteamProtocol.Version, "Did": text[:32], "visitorid": text[32:]}
	if agent != "" {
		headers["User-Agent"] = agent
	}
	return headers
}
func sourceDomain(address string) string {
	if address == "" {
		return ""
	}
	for _, special := range mteamProtocol.SpecialDomains {
		if strings.Contains(address, special) {
			return special
		}
	}
	host := address
	if strings.HasPrefix(address, "http") {
		parsed, err := url.Parse(address)
		if err != nil {
			return ""
		}
		host = parsed.Host
		if parsed.User != nil {
			host = parsed.User.String() + "@" + host
		}
	}
	if host == "" {
		return ""
	}
	parts := strings.Split(host, ".")
	if len(parts) > 3 {
		return host
	}
	if len(parts) > 2 {
		parts = parts[len(parts)-2:]
	}
	return strings.Join(parts, ".")
}
func MTeamPlan(credentials Credentials, timestamp int64) (RequestPlan, bool) {
	address := strings.TrimRight(credentials.URL, "/")
	token := legacyanime.Strip(credentials.Token)
	if address == "" || token == "" {
		return RequestPlan{}, false
	}
	return RequestPlan{Method: "POST", URL: "https://api." + sourceDomain(address) + mteamProtocol.Path, Headers: MTeamHeaders(address, token, credentials.UserAgent, timestamp), Data: map[string]string{"_timestamp": strconv.FormatInt(timestamp, 10), "_sgin": MTeamSign(timestamp)}, UseProxy: Truth(credentials.Proxy), Timeout: credentials.Timeout}, true
}
func pythonScalar(v any) string {
	switch x := v.(type) {
	case nil:
		return "None"
	case bool:
		if x {
			return "True"
		}
		return "False"
	case string:
		return x
	default:
		return fmt.Sprint(x)
	}
}

// These are exactly the non-ASCII upper mappings whose result is all ASCII in
// installed Python15. Only equality against SUCCESS/FAIL uses them; this is not
// a replacement for Python upper on arbitrary Unicode output.
func sourceASCIIUpper(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case 'ß':
			b.WriteString("SS")
		case 'ı':
			b.WriteByte('I')
		case 'ſ':
			b.WriteByte('S')
		case 'ﬀ':
			b.WriteString("FF")
		case 'ﬁ':
			b.WriteString("FI")
		case 'ﬂ':
			b.WriteString("FL")
		case 'ﬃ':
			b.WriteString("FFI")
		case 'ﬄ':
			b.WriteString("FFL")
		case 'ﬅ', 'ﬆ':
			b.WriteString("ST")
		default:
			if r >= 'a' && r <= 'z' {
				r -= 'a' - 'A'
			}
			b.WriteRune(r)
		}
	}
	return b.String()
}
func MTeamPayload(payload any) (bool, string) {
	object, ok := payload.(Object)
	if !ok {
		return false, "模拟登录失败，接口返回数据异常"
	}
	code := pythonScalar(object["code"])
	message := ""
	if Truth(object["message"]) {
		message = legacyanime.Strip(pythonScalar(object["message"]))
	}
	upper := sourceASCIIUpper(message)
	success, _ := object["success"].(bool)
	if success || code == "0" || upper == "SUCCESS" {
		return true, "模拟登录成功"
	}
	if code == "1" && upper == "FAIL" {
		return true, "模拟登录成功"
	}
	if code == "401" || strings.Contains(legacyanime.LowerCase(message), "full authentication") {
		return false, "模拟登录失败，Token登录态已失效，请重新登录站点后更新Token"
	}
	return false, "模拟登录失败，接口未确认访问时间更新"
}
func MTeamVisit(ctx context.Context, credentials Credentials, timestamp int64, transport SiteTransport) (bool, string, error) {
	plan, ok := MTeamPlan(credentials, timestamp)
	if !ok {
		return false, "模拟登录失败，未配置站点Token（需填写浏览器登录态Authorization）", nil
	}
	if transport == nil {
		return false, "", errors.New("site transport not ready")
	}
	response, err := transport.Post(ctx, plan)
	if err != nil {
		return false, "", err
	}
	if response == nil {
		return false, "模拟登录失败，无法打开网站", nil
	}
	if response.Status != 200 {
		return false, fmt.Sprintf("模拟登录失败，状态码：%d", response.Status), nil
	}
	if response.JSONError != nil {
		return false, "模拟登录失败，接口返回数据异常", nil
	}
	state, message := MTeamPayload(response.Payload)
	return state, message, nil
}
