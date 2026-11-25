package mergemessagenotify

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
)

const (
	qqBotTokenURL = "https://bots.qq.com/app/getAppAccessToken"
	qqBotAPIURL   = "https://api.sgroup.qq.com"
	qqBotSandbox  = "https://sandbox.api.sgroup.qq.com"
	qqBotTimeout  = 10
)

var qqBotOpenID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

type qqBot struct {
	mu          sync.Mutex
	credential  [sha256.Size]byte
	accessToken string
	refreshAt   time.Time
}

func (b *qqBot) send(ctx context.Context, http func(context.Context, Request) (Response, error), get func(string) any, m Message) (any, error) {
	appID, _ := get("app_id").(string)
	secret, _ := get("app_secret").(string)
	appID, secret = strings.TrimSpace(appID), strings.TrimSpace(secret)
	if appID == "" || secret == "" {
		return false, errors.New("QQ Bot 需要填写 AppID 和 AppSecret")
	}
	openid, _ := get("target_openid").(string)
	openid = strings.TrimSpace(openid)
	if !qqBotOpenID.MatchString(openid) {
		return false, errors.New("QQ Bot 需要填写当前机器人的用户或群 OpenID")
	}
	targetPath := "/v2/users/"
	switch get("target_type") {
	case nil, "", "c2c":
	case "group":
		targetPath = "/v2/groups/"
	default:
		return false, errors.New("QQ Bot 接收类型必须是私聊或群聊")
	}
	baseURL := qqBotAPIURL
	switch get("sandbox") {
	case nil, false:
	case true:
		baseURL = qqBotSandbox
	default:
		return false, errors.New("QQ Bot 沙箱设置无效")
	}
	parts := make([]string, 0, 2)
	for _, value := range []any{m.Title, m.Text} {
		if truth(value) {
			if s := str(value); strings.TrimSpace(s) != "" {
				parts = append(parts, s)
			}
		}
	}
	if len(parts) == 0 {
		return false, errors.New("QQ Bot 通知内容为空")
	}
	if http == nil {
		return false, errChannelHTTP
	}
	// Encode both fields so their boundary is preserved without retaining the
	// AppSecret in the cache. The access token stays in memory for this instance.
	credentialJSON, _ := json.Marshal([2]string{appID, secret})
	credential := sha256.Sum256(credentialJSON)
	token, err := b.token(ctx, http, credential, appID, secret)
	if err != nil {
		return false, err
	}
	response, err := http(ctx, Request{"post", baseURL + targetPath + openid + "/messages", map[string]any{
		"json":    obj("content", strings.Join(parts, "\n\n"), "msg_type", 0),
		"headers": map[string]any{"Authorization": "QQBot " + token},
		"timeout": qqBotTimeout,
	}})
	if err != nil {
		// The platform may have received the message before the connection failed.
		// Never retry this send or include a raw transport error in the log.
		return false, errors.New("QQ Bot 未能确认发送结果")
	}
	if response.Status == 401 {
		b.mu.Lock()
		if b.credential == credential && b.accessToken == token {
			b.accessToken = ""
			b.refreshAt = time.Time{}
		}
		b.mu.Unlock()
	}
	fields, _ := response.JSON.(map[string]any)
	messageID, _ := fields["id"].(string)
	if !qqBotAccepted(response, fields) || strings.TrimSpace(messageID) == "" {
		return false, qqBotResponseError("发送", response.Status, fields)
	}
	return true, nil
}

func (b *qqBot) token(ctx context.Context, http func(context.Context, Request) (Response, error), credential [sha256.Size]byte, appID, secret string) (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if b.credential == credential && b.accessToken != "" && time.Now().Before(b.refreshAt) {
		return b.accessToken, nil
	}
	response, err := http(ctx, Request{"post", qqBotTokenURL, map[string]any{
		"json":    obj("appId", appID, "clientSecret", secret),
		"timeout": qqBotTimeout,
	}})
	if err != nil {
		return "", errors.New("QQ Bot 无法获取 Access Token")
	}
	fields, _ := response.JSON.(map[string]any)
	token, _ := fields["access_token"].(string)
	if !qqBotAccepted(response, fields) || token == "" || strings.ContainsFunc(token, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r)
	}) {
		return "", qqBotResponseError("获取 Access Token", response.Status, fields)
	}
	seconds := int64(7200)
	if value, found := fields["expires_in"]; found {
		seconds = qqBotInt(value)
	}
	if seconds <= 0 {
		return "", errors.New("QQ Bot Access Token 有效期无效")
	}
	lifetime := time.Duration(min(seconds, 86400)) * time.Second
	b.credential, b.accessToken = credential, token
	b.refreshAt = time.Now().Add(lifetime - min(30*time.Second, lifetime/3))
	return token, nil
}

func qqBotAccepted(response Response, fields map[string]any) bool {
	return response.Status >= 200 && response.Status < 300 && !response.JSONError && qqBotCode(fields) == 0
}

func qqBotResponseError(stage string, status int, fields map[string]any) error {
	if code := qqBotCode(fields); code != 0 {
		return fmt.Errorf("QQ Bot %s失败（HTTP %d，错误码 %d）", stage, status, code)
	}
	return fmt.Errorf("QQ Bot %s失败（HTTP %d，平台未返回有效确认）", stage, status)
}

func qqBotCode(fields map[string]any) int64 {
	if code := qqBotInt(fields["code"]); code != 0 {
		return code
	}
	return qqBotInt(fields["err_code"])
}

func qqBotInt(value any) int64 {
	var text string
	switch v := value.(type) {
	case int:
		return int64(v)
	case json.Number:
		text = string(v)
	case string:
		text = v
	}
	n, err := strconv.ParseInt(text, 10, 64)
	if err != nil {
		return 0
	}
	return n
}
