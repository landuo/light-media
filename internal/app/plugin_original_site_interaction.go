package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"light-media/internal/commandrouter"
)

type originalLarkIncomingKey struct{}

func originalLarkSlashIngress(ctx context.Context, info originalIncomingMessage) (accepted, verified bool) {
	grant, ok := ctx.Value(originalLarkIncomingKey{}).(originalLarkWebhookGrant)
	accepted = ok && info.Channel != nil && *info.Channel == "飞书" && info.Source != nil && *info.Source == "Lark"
	return accepted, accepted && grant.verified
}

type originalSlashSession struct {
	id       string
	created  time.Time
	page     int
	awaiting string
}

type originalSlashInteractions struct {
	mu            sync.Mutex
	byUser        map[string]originalSlashSession
	cookiePrompts map[string]time.Time
}

func (s *originalSlashInteractions) currentLocked(user string) originalSlashSession {
	cutoff := time.Now().Add(-24 * time.Hour)
	for key, session := range s.byUser {
		if session.created.Before(cutoff) {
			delete(s.byUser, key)
		}
	}
	for key, prompted := range s.cookiePrompts {
		if prompted.Before(cutoff.Add(-24 * time.Hour)) {
			delete(s.cookiePrompts, key)
		}
	}
	return s.byUser[user]
}

func (s *originalSlashInteractions) current(user string) originalSlashSession {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.currentLocked(user)
}

func (s *originalSlashInteractions) incoming(user, text string, other originalSlashSession) (originalSlashSession, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	session := s.currentLocked(user)
	_, recentlyPrompted := s.cookiePrompts[user]
	privacy := session
	if other.created.After(session.created) {
		privacy.awaiting = ""
	}
	return session, originalSiteSensitiveInput(privacy, recentlyPrompted, text)
}

func (s *originalSlashInteractions) start(user string) (originalSlashSession, error) {
	var token [6]byte
	if _, err := rand.Read(token[:]); err != nil {
		return originalSlashSession{}, err
	}
	next := originalSlashSession{id: hex.EncodeToString(token[:]), created: time.Now()}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.currentLocked(user)
	if s.byUser == nil {
		s.byUser = make(map[string]originalSlashSession)
	}
	s.byUser[user] = next
	return next, nil
}

func (s *originalSlashInteractions) step(user, id, action string, pages int) (originalSlashSession, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	current := s.currentLocked(user)
	if id == "" || current.id != id {
		return current, false
	}
	switch action {
	case "close":
		delete(s.byUser, user)
		return current, true
	case "page-prev":
		current.page = max(0, current.page-1)
		current.awaiting = ""
	case "page-next":
		current.page++
		current.awaiting = ""
	case "cookie", "enable", "disable", "search", "delete":
		current.awaiting = action
		if action == "cookie" {
			if s.cookiePrompts == nil {
				s.cookiePrompts = make(map[string]time.Time)
			}
			s.cookiePrompts[user] = time.Now()
		}
	case "refresh":
		current.awaiting = ""
	case "clamp":
		// External actions have no trusted page count. An unknown action
		// only re-renders, so it cannot call this internal clamp.
		if pages > 0 {
			current.page = max(0, min(current.page, pages-1))
		}
	}
	s.byUser[user] = current
	return current, true
}

// Bind one pending submission to the exact mode the caller observed, even
// when concurrent callbacks change the prompt before the login starts.
func (s *originalSlashInteractions) consume(user string, expected originalSlashSession) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	current := s.currentLocked(user)
	if current.id != expected.id || current.awaiting != expected.awaiting {
		return false
	}
	current.awaiting = ""
	s.byUser[user] = current
	return true
}

type originalSlashReply struct {
	info     originalIncomingMessage
	user     string
	username string
}

type originalMessageButton struct {
	Text         string `json:"text"`
	CallbackData string `json:"callback_data"`
}

func (r originalSlashReply) post(ctx context.Context, a *App, title string, save bool) error {
	_, err := a.postOriginalPluginMessage(ctx, map[string]any{"channel": r.info.Channel, "source": r.info.Source, "userid": r.info.UserID, "username": r.username, "title": title, "save_history": save})
	return err
}

func (r originalSlashReply) show(ctx context.Context, a *App, title, text string, buttons [][]originalMessageButton, edit bool) error {
	if edit && r.info.MessageID != nil && r.info.ChatID != nil {
		messageID, messageErr := originalInputID(r.info.MessageID, false)
		chatID, chatErr := originalInputID(r.info.ChatID, false)
		if messageErr == nil && chatErr == nil && messageID != nil && *messageID != "" && chatID != nil && *chatID != "" {
			kwargs, err := json.Marshal(map[string]any{"channel": r.info.Channel, "source": r.info.Source,
				"message_id": r.info.MessageID, "chat_id": r.info.ChatID, "title": title, "text": text, "buttons": buttons})
			if err != nil {
				return err
			}
			updated, err := a.invokeOriginalPluginModules(ctx, "edit_message", nil, nil, kwargs)
			if err != nil {
				return err
			}
			if originalViewJSONTruth(updated) {
				return nil
			}
		}
	}
	_, err := a.postOriginalPluginMessage(ctx, map[string]any{"channel": r.info.Channel, "source": r.info.Source, "userid": r.info.UserID, "username": r.username,
		"title": title, "text": text, "buttons": buttons, "save_history": false})
	return err
}

func (a *App) openOriginalSiteInteraction(ctx context.Context, reply originalSlashReply, args string) error {
	session, err := a.pluginOriginalSiteInteractions.start(reply.user)
	if err != nil {
		return err
	}
	if args != "" {
		_, err = a.originalSiteInteractionText(ctx, reply, session, args)
		return err
	}
	return a.renderOriginalSiteInteraction(ctx, reply, session.id, false)
}

func (a *App) originalSiteInteractionCallback(ctx context.Context, reply originalSlashReply, data string) (bool, error) {
	parts := strings.SplitN(data, ":", 4)
	if len(parts) < 3 || parts[0] != "sites" {
		return false, nil
	}
	action := parts[2]
	switch action {
	case "close", "page-prev", "page-next", "cookie", "enable", "disable", "refresh":
	default:
		action = ""
	}
	_, ok := a.pluginOriginalSiteInteractions.step(reply.user, parts[1], action, 0)
	if !ok {
		return true, reply.post(ctx, a, "站点交互已失效，请重新发送 /sites", true)
	}
	if action == "close" {
		return true, reply.show(ctx, a, "站点管理", "站点交互已结束", nil, true)
	}
	return true, a.renderOriginalSiteInteraction(ctx, reply, parts[1], true)
}

// Only exact input prefixes enter a site operation. Pending input is consumed
// before an explicit prefix, as in the original SiteChain.
func originalSiteTextAction(text string) (string, string) {
	fields := strings.FieldsFunc(text, originalSiteSpace)
	if len(fields) < 2 {
		return "", ""
	}
	prefix := strings.ToLower(fields[0])
	if prefix == "更新" && len(fields) > 2 && strings.EqualFold(fields[1], "cookie") {
		return "cookie", strings.Join(fields[2:], " ")
	}
	var action string
	switch prefix {
	case "cookie", "更新cookie":
		action = "cookie"
	case "启用", "enable":
		action = "enable"
	case "禁用", "disable":
		action = "disable"
	}
	return action, strings.Join(fields[1:], " ")
}

func originalSiteSpace(r rune) bool {
	return unicode.IsSpace(r) || r >= 0x1c && r <= 0x1f
}

func originalSiteSensitiveInput(session originalSlashSession, recentlyPrompted bool, text string) bool {
	if strings.HasPrefix(text, "CALLBACK:") {
		return false
	}
	name, args := commandrouter.Split(text)
	if strings.HasPrefix(name, "/") {
		action, _ := originalSiteTextAction(args)
		return name == "/sites" && action == "cookie"
	}
	action, _ := originalSiteTextAction(text)
	if action == "cookie" {
		return true
	}
	if originalSiteTextControl(originalIncomingStrip(text)) != "" {
		return false
	}
	if session.awaiting == "cookie" {
		return true
	}
	// A user may answer a replaced or expired card. For recently prompted
	// senders, keep credential-shaped replies out of other consumers too.
	fields := strings.FieldsFunc(text, originalSiteSpace)
	return recentlyPrompted && (len(fields) == 3 || len(fields) == 4) && originalSiteDigits.FindString(fields[0]) == fields[0]
}

func originalSiteTextControl(text string) string {
	switch strings.ToLower(text) {
	case "退出", "关闭", "q", "quit", "exit":
		return "close"
	case "取消", "cancel", "返回", "back", "刷新", "refresh", "列表", "list":
		return "refresh"
	case "p", "prev", "上一页":
		return "page-prev"
	case "n", "next", "下一页":
		return "page-next"
	}
	return ""
}

func (a *App) originalSiteInteractionText(ctx context.Context, reply originalSlashReply, session originalSlashSession, text string) (bool, error) {
	if session.id == "" {
		return false, nil
	}
	normalized := originalIncomingStrip(text)
	change := originalSiteTextControl(normalized)
	if change != "" {
		if _, ok := a.pluginOriginalSiteInteractions.step(reply.user, session.id, change, 0); !ok {
			return false, nil
		}
		if change == "close" {
			return true, reply.post(ctx, a, "站点交互已结束", false)
		}
		return true, a.renderOriginalSiteInteraction(ctx, reply, session.id, false)
	}

	action, args := originalSiteTextAction(normalized)
	if session.awaiting != "" {
		action, args = session.awaiting, normalized
		if !a.pluginOriginalSiteInteractions.consume(reply.user, session) {
			return false, nil
		}
	} else if _, ok := a.pluginOriginalSiteInteractions.step(reply.user, session.id, "", 0); !ok {
		return false, nil
	}
	if action == "" {
		_, hint := originalSiteInteractionPrompt(session.awaiting)
		return true, reply.post(ctx, a, hint, true)
	}
	var message string
	var err error
	if action == "cookie" {
		message, err = a.updateOriginalSiteInteractionCookie(ctx, args)
	} else {
		message, err = a.setOriginalSiteInteractionEnabled(ctx, args, action == "enable")
	}
	if err != nil {
		return true, err
	}
	if err := reply.post(ctx, a, message, true); err != nil {
		return true, err
	}
	return true, a.renderOriginalSiteInteraction(ctx, reply, session.id, false)
}

func originalSiteInteractionPrompt(mode string) (string, string) {
	switch mode {
	case "cookie":
		return "当前操作：更新站点 Cookie，请输入：<id> <username> <password> [2fa_code/secret]", "输入站点 ID、用户名、密码和可选 2FA；输入 `取消` 返回列表，输入 `退出` 结束交互。"
	case "enable":
		return "当前操作：启用站点，请输入站点 ID，多个 ID 用空格分隔。", "输入一个或多个站点 ID；输入 `取消` 返回列表，输入 `退出` 结束交互。"
	case "disable":
		return "当前操作：禁用站点，请输入站点 ID，多个 ID 用空格分隔。", "输入一个或多个站点 ID；输入 `取消` 返回列表，输入 `退出` 结束交互。"
	default:
		return "", "可输入：`cookie <id> <username> <password> [2fa]`、`启用 <id...>`、`禁用 <id...>`、`n`、`p`、`刷新`、`退出`。"
	}
}

func (a *App) renderOriginalSiteInteraction(ctx context.Context, reply originalSlashReply, id string, edit bool) error {
	a.settingsMu.Lock()
	rows, err := a.originalSourceSiteRows(ctx)
	a.settingsMu.Unlock()
	if err != nil {
		return err
	}
	const pageSize = 6
	pages := max(1, (len(rows)+pageSize-1)/pageSize)
	session, ok := a.pluginOriginalSiteInteractions.step(reply.user, id, "clamp", pages)
	if !ok {
		return nil
	}
	text := "当前没有任何站点。\n\n输入 `退出` 结束交互。"
	if len(rows) != 0 {
		lines := []string{"| ID | 站点 | 状态 | Cookie | 渲染 | 域名 |", "| --- | --- | --- | --- | --- | --- |"}
		start := session.page * pageSize
		escape := strings.NewReplacer("\n", "<br>", "|", "\\|")
		for _, row := range rows[start:min(start+pageSize, len(rows))] {
			status, cookie, render := "禁用", "未配置", "否"
			if sourceNativeActive(row.fields["is_active"]) {
				status = "启用"
			}
			if row.site.Cookie != "" {
				cookie = "已配置"
			}
			if sourceSubscriptionTruth(row.fields["render"]) {
				render = "是"
			}
			domain := sourceText(row.fields["domain"])
			if domain == "" {
				domain = standardSiteEventDomain(sourceText(row.fields["url"]))
			}
			lines = append(lines, fmt.Sprintf("| %d | %s | %s | %s | %s | %s |", row.sourceID, escape.Replace(sourceText(row.fields["name"])), status, cookie, render, escape.Replace(domain)))
		}
		prompt, hint := originalSiteInteractionPrompt(session.awaiting)
		sections := []string{strings.Join(lines, "\n"), fmt.Sprintf("第 %d/%d 页，共 %d 个站点", session.page+1, pages, len(rows))}
		if prompt != "" {
			sections = append(sections, prompt)
		}
		text = strings.Join(append(sections, hint), "\n\n")
	}
	button := func(text, action string) originalMessageButton {
		return originalMessageButton{text, "sites:" + id + ":" + action}
	}
	buttons := [][]originalMessageButton{}
	var navigation []originalMessageButton
	if session.page > 0 {
		navigation = append(navigation, button("⬅️ 上一页", "page-prev"))
	}
	if session.page+1 < pages {
		navigation = append(navigation, button("下一页 ➡️", "page-next"))
	}
	if len(navigation) > 0 {
		buttons = append(buttons, navigation)
	}
	buttons = append(buttons,
		[]originalMessageButton{button("更新 Cookie", "cookie"), button("禁用站点", "disable"), button("启用站点", "enable")},
		[]originalMessageButton{button("刷新列表", "refresh"), button("关闭", "close")})
	return reply.show(ctx, a, "站点管理", text, buttons, edit)
}

var originalSiteDigits = regexp.MustCompile(`\p{Nd}+`)

func originalSiteDecimal(value string) string {
	number, ok := categoryDecimalInteger(value)
	if !ok {
		return ""
	}
	return number.String()
}

func originalSiteInteractionRow(rows []originalSourceSiteRow, number string) (originalSourceSiteRow, bool) {
	id, err := strconv.ParseInt(number, 10, 64)
	if err == nil {
		for _, row := range rows {
			if row.sourceID == id {
				return row, true
			}
		}
	}
	return originalSourceSiteRow{}, false
}

func (a *App) setOriginalSiteInteractionEnabled(ctx context.Context, text string, enabled bool) (string, error) {
	ids := originalSiteDigits.FindAllString(text, -1)
	if len(ids) == 0 {
		return "请输入至少一个有效的站点 ID", nil
	}
	a.settingsMu.Lock()
	defer a.settingsMu.Unlock()
	rows, err := a.originalSourceSiteRows(ctx)
	if err != nil {
		return "", err
	}
	var changed, missing []string
	for _, rawID := range ids {
		number := originalSiteDecimal(rawID)
		row, found := originalSiteInteractionRow(rows, number)
		if !found {
			missing = append(missing, number)
			continue
		}
		c := a.Settings()
		for i := range c.Sites {
			if c.Sites[i].ID == row.site.ID && c.Sites[i].Enabled != enabled {
				c.Sites[i].Enabled = enabled
				if err := a.saveSettings(ctx, c); err != nil {
					return "", err
				}
				break
			}
		}
		changed = append(changed, sourceText(row.fields["name"]))
	}
	action := "禁用"
	if enabled {
		action = "启用"
	}
	if len(changed) == 0 {
		return "未找到站点：" + strings.Join(missing, ", "), nil
	}
	message := fmt.Sprintf("已%s %d 个站点：%s", action, len(changed), strings.Join(changed, ", "))
	if len(missing) > 0 {
		message += "；未找到：" + strings.Join(missing, ", ")
	}
	return message, nil
}

func (a *App) updateOriginalSiteInteractionCookie(ctx context.Context, text string) (string, error) {
	parts := strings.FieldsFunc(text, originalSiteSpace)
	if len(parts) < 3 || len(parts) > 4 || originalSiteDigits.FindString(parts[0]) != parts[0] {
		return "格式错误，请输入：cookie <id> <username> <password> [2fa_code/secret]", nil
	}
	number := originalSiteDecimal(parts[0])
	a.settingsMu.Lock()
	rows, err := a.originalSourceSiteRows(ctx)
	a.settingsMu.Unlock()
	if err != nil {
		return "", err
	}
	row, found := originalSiteInteractionRow(rows, number)
	if !found {
		return "站点编号 " + number + " 不存在", nil
	}
	twoFA := ""
	if len(parts) == 4 {
		twoFA = parts[3]
	}
	prefix := "【" + sourceText(row.fields["name"]) + "】Cookie&UA 更新"
	cookie, ua, err := a.originalSiteCookieLogin(ctx, row.site, parts[1], parts[2], twoFA)
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return prefix + "失败：" + err.Error(), nil
	}
	if cookie == "" || ua == "" || invalidSiteImportText(cookie, 64<<10) || invalidSiteImportText(ua, 8192) {
		return prefix + "失败：未取得有效的登录会话", nil
	}

	// Browser I/O runs without the settings lock. A later edit, deletion or
	// identity replacement must win over this earlier credential attempt.
	a.settingsMu.Lock()
	defer a.settingsMu.Unlock()
	rows, err = a.originalSourceSiteRows(ctx)
	if err != nil {
		return "", err
	}
	current, found := originalSiteInteractionRow(rows, number)
	if !found || current.site != row.site || current.provenance != row.provenance {
		return prefix + "失败：站点设置已变化", nil
	}
	c := a.Settings()
	for i := range c.Sites {
		if c.Sites[i].ID == current.site.ID {
			c.Sites[i].Cookie, c.Sites[i].UserAgent = cookie, ua
			if err := a.saveSettings(ctx, c); err != nil {
				return "", err
			}
			return prefix + "成功", nil
		}
	}
	return prefix + "失败：站点设置已变化", nil
}
