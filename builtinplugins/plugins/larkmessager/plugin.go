package larkmessager

import (
	"context"
	_ "embed"
	"encoding/json"
	"strings"
	"sync"
	"time"
)

//go:embed form.json
var formJSON []byte

// KV requires per-owner private storage; unknown fields and JSON numbers survive.
type KV interface {
	Get(context.Context, string) (Object, error)
	Put(context.Context, string, Object) error
	Delete(context.Context, string) error
}
type PluginHost struct {
	API         Host
	KV          KV
	Forward     func(context.Context, Object) error
	LogFailure  func(context.Context, string)
	Sleep       func(context.Context, time.Duration) error
	Now         func() time.Time
	newChatsAPI func(appID, appSecret string) Host
}
type ConfigState struct {
	Enabled                                                                  bool
	AppID, AppSecret, ChatID, UserRaw, UserID, VerificationToken, EncryptKey string
	Admins, Direct, Emails, Mobiles, Missing                                 []string
	Switches                                                                 any
}
type Plugin struct {
	mu       sync.Mutex
	results  sync.Mutex
	host     PluginHost
	state    ConfigState
	client   *Client
	crypto   *Crypto
	resolved map[string]string
	epoch    uint64
}

func New(host PluginHost) *Plugin {
	if host.Now == nil {
		host.Now = time.Now
	}
	if host.Sleep == nil {
		host.Sleep = func(ctx context.Context, d time.Duration) error {
			t := time.NewTimer(d)
			defer t.Stop()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-t.C:
				return nil
			}
		}
	}
	return &Plugin{host: host, resolved: map[string]string{}}
}
func Form() (Object, error) { var f Object; e := decode(formJSON, &f); return f, e }
func ConfigFromTrusted(raw Object) (ConfigState, error) {
	s := ConfigState{Enabled: truth(raw["enabled"])}
	read := func(v any) (string, error) {
		if !truth(v) {
			return "", nil
		}
		vstr, ok := v.(string)
		if !ok {
			return "", ErrUnsupported
		}
		return strip(vstr), nil
	}
	fields := []struct {
		p *string
		v any
	}{{&s.AppID, raw["app_id"]}, {&s.AppSecret, raw["app_secret"]}, {&s.ChatID, raw["chat_id"]}, {&s.UserRaw, first(raw["FEISHU_OPEN_ID"], raw["user_id"])}, {&s.VerificationToken, raw["verification_token"]}, {&s.EncryptKey, raw["encrypt_key"]}}
	for _, f := range fields {
		v, e := read(f.v)
		if e != nil {
			return s, e
		}
		*f.p = v
	}
	s.UserID = s.UserRaw
	admin, e := read(first(raw["FEISHU_ADMINS"], raw["admin_users"]))
	if e != nil {
		return s, e
	}
	rawAdmins := []string{}
	for _, v := range strings.Split(admin, ",") {
		if v = strip(v); v != "" {
			rawAdmins = append(rawAdmins, v)
		}
	}
	for _, key := range []string{"admin_emails", "admin_mobiles"} {
		v, e := read(raw[key])
		if e != nil {
			return s, e
		}
		for _, v := range strings.Split(v, ",") {
			if v = strip(v); v != "" && !includes(rawAdmins, v) {
				rawAdmins = append(rawAdmins, v)
			}
		}
	}
	for _, v := range rawAdmins {
		if strings.HasPrefix(v, "ou_") {
			s.Direct = append(s.Direct, v)
		}
		if strings.Contains(v, "@") {
			s.Emails = append(s.Emails, v)
		}
		if !strings.Contains(v, "@") && !strings.HasPrefix(v, "ou_") {
			s.Mobiles = append(s.Mobiles, v)
		}
		if !includes(s.Admins, v) {
			s.Admins = append(s.Admins, v)
		}
	}
	s.Switches = raw["switchs"]
	if !truth(s.Switches) {
		s.Switches = []any{}
	}

	if s.AppID == "" {
		s.Missing = append(s.Missing, "App ID")
	}
	if s.AppSecret == "" {
		s.Missing = append(s.Missing, "App Secret")
	}
	if s.Enabled && len(s.Missing) > 0 {
		s.Enabled = false
	}
	return s, nil
}

// Init is deterministic and does not run a detached thread. The trusted owner
// must schedule SyncAdmins once for the returned epoch; pending != synchronized.
func (p *Plugin) Init(raw Object) (uint64, error) {
	s, e := ConfigFromTrusted(clone(raw))
	if e != nil {
		return 0, e
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.epoch++
	p.state = s
	p.resolved = map[string]string{}
	p.client = nil
	p.crypto = nil
	if s.Enabled {
		p.client = NewClient(p.host.API)
		p.client.logFailure = p.host.LogFailure
		if s.EncryptKey != "" {
			p.crypto = NewCrypto(s.EncryptKey)
		}
	}
	return p.epoch, nil
}
func (p *Plugin) Stop()                      { p.mu.Lock(); defer p.mu.Unlock(); p.epoch++; p.client = nil; p.crypto = nil }
func (p *Plugin) ConfigTrusted() ConfigState { p.mu.Lock(); defer p.mu.Unlock(); return clone(p.state) }
func (p *Plugin) snapshot() (ConfigState, *Client, uint64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return clone(p.state), p.client, p.epoch
}

var moduleNames = []string{"message_parser", "post_message", "post_medias_message", "post_torrents_message", "edit_message", "mark_message_processing_started", "mark_message_processing_finished"}

func (p *Plugin) Modules() []string {
	s, c, _ := p.snapshot()
	if !s.Enabled || c == nil {
		return []string{}
	}
	return append([]string{}, moduleNames...)
}
func (p *Plugin) resolveSingle(ctx context.Context, c *Client, id string) string {
	id = strip(id)
	if c == nil || id == "" {
		return ""
	}
	if strings.HasPrefix(id, "ou_") {
		return id
	}
	var m map[string]string
	var e error
	if strings.Contains(id, "@") {
		m, e = c.BatchGetID(ctx, []string{id}, nil, nil)
	} else {
		m, e = c.BatchGetID(ctx, nil, []string{id}, nil)
	}
	if e != nil {
		return ""
	}
	return m[id]
}
func (p *Plugin) ResolveAdmins(ctx context.Context) ([]string, string) {
	s, c, _ := p.snapshot()
	ids := []string{}
	for _, id := range s.Direct {
		ids = append(ids, id)
	}
	for _, list := range [][]string{s.Emails, s.Mobiles} {
		for _, id := range list {
			if v := p.resolveSingle(ctx, c, id); v != "" {
				ids = append(ids, v)
			}
		}
	}
	user := ""
	if s.UserRaw != "" {
		if strings.HasPrefix(s.UserRaw, "ou_") {
			user = s.UserRaw
		} else {
			user = p.resolveSingle(ctx, c, s.UserRaw)
			if user == "" {
				user = s.UserRaw
			}
		}
	}
	return uniqueSourceStrings(ids), user
}
func (p *Plugin) SyncAdmins(ctx context.Context, epoch uint64) (_error error) {
	defer sourceErrors(&_error)
	s, c, current := p.snapshot()
	if epoch != current {
		return ErrDenied
	}
	if !s.Enabled || c == nil {
		return nil
	}
	ids, user := p.ResolveAdmins(ctx)
	if len(s.Emails)+len(s.Mobiles)+len(s.Direct) > 0 && len(ids) == 0 {
		if e := p.host.Sleep(ctx, 5*time.Second); e != nil {
			return e
		}
		_, c, current = p.snapshot()
		if current != epoch || c == nil {
			return ErrDenied
		}
		ids, user = p.ResolveAdmins(ctx)
	}
	p.mu.Lock()
	if p.epoch != epoch || p.client == nil {
		p.mu.Unlock()
		return ErrDenied
	}
	if user != "" && user != p.state.UserRaw {
		p.state.UserID = user
	}
	p.mu.Unlock()
	return nil
}

func uniqueSourceStrings(in []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, s := range in {
		if !seen[s] {
			out = append(out, s)
			seen[s] = true
		}
	}
	return out
}
func (p *Plugin) IsAdmin(ctx context.Context, open string) (_out bool, _error error) {
	defer sourceErrors(&_error)
	s, c, epoch := p.snapshot()
	if len(s.Admins) == 0 || includes(s.Admins, open) {
		return true, nil
	}
	if c == nil {
		return false, nil
	}
	for _, id := range s.Admins {
		if strings.HasPrefix(id, "ou_") {
			continue
		}
		p.mu.Lock()
		resolved, has := p.resolved[id]
		p.mu.Unlock()
		if has {
			if resolved == open {
				return true, nil
			}
			continue
		}
		var m map[string]string
		var e error
		if strings.Contains(id, "@") {
			m, e = c.BatchGetID(ctx, []string{id}, nil, nil)
		} else {
			m, e = c.BatchGetID(ctx, nil, []string{id}, nil)
		}
		if e != nil {
			continue
		}
		resolved = m[id]
		p.mu.Lock()
		if p.epoch != epoch {
			p.mu.Unlock()
			return false, ErrDenied
		}
		p.resolved[id] = resolved
		p.mu.Unlock()
		if resolved == open {
			return true, nil
		}
	}
	return false, nil
}
func (p *Plugin) rejectCommand(ctx context.Context, open, user string) (_out bool, _error error) {
	defer sourceErrors(&_error)
	s, _, _ := p.snapshot()
	if len(s.Admins) == 0 {
		return false, nil
	}
	for _, id := range []string{open, user} {
		id = strip(id)
		if id != "" && includes(s.Admins, id) {
			return false, nil
		}
	}
	for _, id := range []string{open, user} {
		id = strip(id)
		if id == "" {
			continue
		}
		ok, e := p.IsAdmin(ctx, id)
		if e != nil {
			return true, e
		}
		if ok {
			return false, nil
		}
	}
	return true, nil
}
func MessageTarget(n Notification) Target {
	t := Target{OriginalMessageID: n.OriginalMessageID}
	if strip(n.UserID) != "" {
		t.UserID = strip(n.UserID)
		t.ReceiveIDType = "open_id"
		return t
	}
	open, user, chat := trim(n.Targets["feishu_openid"]), trim(n.Targets["feishu_userid"]), trim(n.Targets["feishu_chat_id"])
	t.ChatID = chat
	if open != "" {
		t.UserID = open
		t.ReceiveIDType = "open_id"
	} else if user != "" {
		t.UserID = user
		t.ReceiveIDType = "user_id"
	}
	return t
}
func (p *Plugin) messageContext(n Notification) (Target, *Client, bool) {
	s, c, _ := p.snapshot()
	if !s.Enabled || c == nil || (truth(s.Switches) && n.MType != "" && !containsSource(s.Switches, n.MType)) {
		return Target{}, nil, false
	}
	t := MessageTarget(n)
	if t.UserID == "" && t.ChatID == "" {
		t.UserID = s.UserID
		t.ChatID = s.ChatID
		if t.UserID != "" {
			t.ReceiveIDType = "open_id"
		}
	}
	t.DefaultOpenID = s.UserID
	t.DefaultChatID = s.ChatID
	return t, c, true
}
func (p *Plugin) Post(ctx context.Context, n Notification) (_error error) {
	defer sourceErrors(&_error)
	t, c, ok := p.messageContext(n)
	if !ok || t.UserID == "" && t.ChatID == "" {
		return nil
	}
	if n.Image != "" && n.FilePath != "" {
		copy := n
		copy.FilePath = ""
		copy.FileName = ""
		if _, err := c.SendNotification(ctx, copy, t, nil); err != nil {
			return err
		}
		_, err := c.SendFile(ctx, n.FilePath, "", "", n.FileName, t)
		return err
	}
	switch {
	case n.FilePath != "":
		_, err := c.SendFile(ctx, n.FilePath, n.Title, n.Text, n.FileName, t)
		return err
	case n.VoicePath != "":
		_, err := c.SendVoice(ctx, n.VoicePath, n.VoiceCaption, t)
		return err
	default:
		_, err := c.SendNotification(ctx, n, t, nil)
		return err
	}
}

// DispatchTrusted requires an ingress grant checked by the caller. This pure
// core receives neither an API token nor an unrestricted MessageChain URL.
func (p *Plugin) DispatchTrusted(ctx context.Context, body Object) (_error error) {
	defer sourceErrors(&_error)
	_, c, _ := p.snapshot()
	typ := text(obj(body["header"])["event_type"])
	var payload Object
	var e error
	test := false
	switch typ {
	case "im.message.receive_v1":
		payload, e = ReceivePayload(body)
	case "card.action.trigger":
		payload, test, e = CardActionPayload(body)
	default:
		return nil
	}
	if e != nil {
		return e
	}
	sender := obj(payload["sender"])
	if c != nil {
		c.RememberUserIDType(text(sender["open_id"]), text(sender["user_id"]))
		c.RememberTarget(text(first(sender["open_id"], sender["user_id"])), text(payload["chat_id"]))
	}
	if test && c != nil {
		_, err := c.ReplyMessage(ctx, text(payload["message_id"]), "text", Object{"text": "✅ 测试确认成功！LarkMessager 插件卡片交互正常工作。"})
		if err != nil && p.host.LogFailure != nil {
			p.host.LogFailure(ctx, "回复测试卡片确认消息失败")
		}
		return nil
	}
	if p.host.Forward == nil {
		return ErrDependency
	}
	if err := p.host.Forward(ctx, clone(payload)); err != nil && p.host.LogFailure != nil {
		p.host.LogFailure(ctx, "LarkMessager 转发消息到 MessageChain 失败")
	}
	return nil
}
func (p *Plugin) ParseMessage(ctx context.Context, source string, message Object) (_out Object, _error error) {
	defer sourceErrors(&_error)
	if source != "Lark" || message == nil {
		return nil, nil
	}
	sender, e := requireObject(first(message["sender"], Object{}))
	if e != nil {
		return nil, e
	}
	openValue, userValue := first(sender["open_id"], ""), first(sender["user_id"], "")
	open, user := text(openValue), text(userValue)
	rawID := first(openValue, userValue, "")
	if !truth(rawID) {
		return nil, nil
	}
	fallback := first(sender["name"], openValue, userValue)
	var username any = fallback
	id, e := sourceUnionID(rawID)
	if e != nil {
		return nil, e
	}
	username, e = sourceUnionID(username)
	if e != nil {
		return nil, e
	}
	callback := message["type"] == "cardAction"
	var callbackData any
	plain := ""
	if truth(message["text"]) {
		v, ok := message["text"].(string)
		if !ok {
			return nil, ErrUnsupported
		}
		plain = strip(v)
	}
	var images, audios, files []any
	if callback {
		callbackData = message["callback_data"]
		if !truth(callbackData) {
			return nil, nil
		}
		plain = "CALLBACK:" + str(callbackData)
		if _, ok := callbackData.(string); !ok {
			return nil, errInvalidMessage
		}
	} else {
		images, e = normalizeImages(message["images"])
		if e != nil {
			return nil, e
		}
		for _, v := range arr(message["audio_refs"]) {
			if s := strip(str(v)); s != "" {
				audios = append(audios, s)
			}
		}
		for _, v := range arr(message["files"]) {
			if f := obj(v); f != nil && truth(f["ref"]) {
				file, e := normalizeAttachment(f)
				if e != nil {
					return nil, e
				}
				files = append(files, file)
			}
		}
		if plain == "" && len(images)+len(audios)+len(files) == 0 {
			return nil, nil
		}
	}
	command := strings.HasPrefix(plain, "/")
	if callback {
		command = strings.HasPrefix(strip(str(callbackData)), "/")
	}
	if command {
		reject, e := p.rejectCommand(ctx, open, user)
		if e != nil {
			return nil, e
		}
		if reject {
			s, c, _ := p.snapshot()
			if c != nil {
				typ := "user_id"
				if open != "" {
					typ = "open_id"
				}
				_, _ = c.SendText(ctx, "只有管理员才有权限执行此命令", Target{UserID: text(rawID), ChatID: text(message["chat_id"]), ReceiveIDType: typ, DefaultOpenID: s.UserID, DefaultChatID: s.ChatID})
			}
			return nil, nil
		}
	}
	messageID, e := sourceUnionID(first(message["message_id"], ""))
	if e != nil {
		return nil, e
	}
	if messageID == nil {
		messageID = ""
	}
	chatID := ""
	if truth(message["chat_id"]) {
		v, ok := message["chat_id"].(string)
		if !ok {
			return nil, errInvalidMessage
		}
		chatID = v
	}
	return Object{"userid": id, "username": username, "channel": "飞书", "source": "Lark", "text": plain, "date": nil, "action": 0, "is_callback": callback, "callback_data": callbackData, "message_id": messageID, "chat_id": chatID, "reply_to_message_id": nil, "callback_query": nil, "images": images, "audio_refs": audios, "files": files}, nil
}
func normalizeImages(v any) ([]any, error) {
	if !truth(v) {
		return nil, nil
	}
	values, ok := v.([]any)
	if !ok {
		values = []any{v}
	}
	out := []any{}
	for _, v := range values {
		if v == nil {
			continue
		}
		if s, ok := v.(string); ok {
			out = append(out, Object{"ref": s, "name": nil, "mime_type": nil, "size": nil})
			continue
		}
		f := obj(v)
		if f == nil {
			continue
		}
		ref := first(f["ref"], f["url"], f["image_url"], f["file_url"])
		if !truth(ref) {
			continue
		}
		s, ok := ref.(string)
		if !ok {
			return nil, errInvalidMessage
		}
		size := f["size"]
		if size != nil {
			n, e := integer(size)
			if e != nil {
				if integerErrorClass(e) == "OverflowError" {
					return nil, e
				}
				size = nil
			} else {
				size = n
			}
		}
		name, mime := first(f["name"], f["filename"]), first(f["mime_type"], f["content_type"])
		for _, v := range []any{name, mime} {
			if v != nil {
				if _, ok := v.(string); !ok {
					return nil, errInvalidMessage
				}
			}
		}
		out = append(out, Object{"ref": s, "name": name, "mime_type": mime, "size": size})
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}
func normalizeAttachment(f Object) (_out Object, _error error) {
	defer sourceErrors(&_error)
	ref, ok := f["ref"].(string)
	if !ok {
		return nil, errInvalidMessage
	}
	o := Object{"ref": ref, "name": nil, "mime_type": nil, "size": nil}
	for _, k := range []string{"name", "mime_type"} {
		if f[k] != nil {
			if _, ok := f[k].(string); !ok {
				return nil, errInvalidMessage
			}
			o[k] = f[k]
		}
	}
	if f["size"] != nil {
		n, e := pydanticInteger(f["size"])
		if e != nil {
			return nil, errInvalidMessage
		}
		o["size"] = n
	}
	return o, nil
}
func integer(v any) (json.Number, error) {
	i, e := pythonInt(v)
	if e != nil {
		return "", e
	}
	return json.Number(i.String()), nil
}

func (p *Plugin) State() bool { p.mu.Lock(); defer p.mu.Unlock(); return p.state.Enabled }
