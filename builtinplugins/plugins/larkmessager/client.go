package larkmessager

import (
	"context"
	"errors"
	"path"
	"strings"
	"sync"

	legacyanime "light-media/builtinplugins/plugins/autosignin/pyvalue"
)

// APIError retains only a source-recognized permission fact. The raw server
// message may echo credentials and must not escape through a result or log.
type APIError struct{ ContactPermissionMissing bool }

func (e *APIError) Error() string { return "Lark API request rejected" }
func (e *APIError) Unwrap() error { return ErrRejected }

// Request operations are closed, source-defined API actions, never arbitrary RPC.
type Request struct {
	Op      string
	IDs     []string
	Query   Object
	Payload Object
	Blob    *Blob
}
type Response struct {
	Status int
	Header map[string]string
	Body   []byte
}
type Blob struct {
	Name string
	MIME string
	Data []byte
}
type Host interface {
	API(context.Context, Request) (Response, error)
	ReadFile(context.Context, string) (Blob, error)
	FetchImage(context.Context, string) (Blob, error)
}
type Client struct {
	host                            Host
	logFailure                      func(context.Context, string)
	mu                              sync.Mutex
	userChats, userTypes, chatUsers map[string]string
}

func NewClient(host Host) *Client {
	return &Client{host: host, userChats: map[string]string{}, userTypes: map[string]string{}, chatUsers: map[string]string{}}
}

func (c *Client) ignoreRejection(ctx context.Context, err error, message string) error {
	if !errors.Is(err, ErrRejected) {
		return err
	}
	if c.logFailure != nil {
		c.logFailure(ctx, message)
	}
	return nil
}
func (c *Client) RememberTarget(user, chat string) {
	user, chat = strip(user), strip(chat)
	if user == "" || chat == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.userChats[user] = chat
	c.chatUsers[chat] = user
}
func (c *Client) RememberUserIDType(open, user string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if open != "" {
		c.userTypes[strip(open)] = "open_id"
	}
	if user != "" {
		c.userTypes[strip(user)] = "user_id"
	}
}
func (c *Client) ResolveTarget(t Target) (string, string, error) {
	uid, chat, typ := strip(t.UserID), strip(t.ChatID), strip(t.ReceiveIDType)
	if uid == "" && chat == "" {
		uid, chat = t.DefaultOpenID, t.DefaultChatID
		if uid != "" && typ == "" {
			typ = "open_id"
		}
	}
	if typ == "chat_id" && chat != "" {
		return chat, "chat_id", nil
	}
	if uid != "" {
		if typ == "open_id" || typ == "user_id" {
			return uid, typ, nil
		}
		c.mu.Lock()
		remembered := c.userTypes[uid]
		c.mu.Unlock()
		if remembered == "" {
			remembered = "open_id"
		}
		return uid, remembered, nil
	}
	if chat != "" {
		return chat, "chat_id", nil
	}
	return "", "", ErrRejected
}
func (c *Client) fallbackChat(r *Result, t Target) *Result {
	if r == nil {
		return &Result{Success: false}
	}
	if !truth(r.ChatID) {
		c.mu.Lock()
		remembered := c.userChats[t.UserID]
		c.mu.Unlock()
		r.ChatID = first(t.ChatID, remembered, t.DefaultChatID)
	}
	return r
}
func (c *Client) call(ctx context.Context, r Request) (Response, Object, error) {
	if c.host == nil {
		return Response{}, nil, ErrDependency
	}
	response, err := c.host.API(ctx, r)
	if err != nil {
		return response, nil, err
	}
	var body Object
	if err = decode(response.Body, &body); err != nil {
		return response, nil, err
	}
	if body == nil {
		return response, nil, ErrUnsupported
	}
	return response, body, nil
}
func (c *Client) SendMessage(ctx context.Context, id, typ, msgtype string, content any) (_out *Result, _error error) {
	defer sourceErrors(&_error)
	_, body, err := c.call(ctx, Request{Op: "send", IDs: []string{id, typ}, Payload: Object{"receive_id": id, "msg_type": msgtype, "content": content}})
	if err != nil {
		return nil, err
	}
	if !zero(body["code"]) {
		return nil, ErrRejected
	}
	d := obj(body["data"])
	if value, present := body["data"]; present && obj(value) == nil {
		return nil, ErrUnsupported
	}
	return &Result{Success: true, MessageID: d["message_id"], ChatID: d["chat_id"], MsgType: d["msg_type"]}, nil
}
func (c *Client) ReplyMessage(ctx context.Context, id, msgtype string, content any) (_out *Result, _error error) {
	defer sourceErrors(&_error)
	if id == "" {
		return nil, ErrRejected
	}
	_, body, err := c.call(ctx, Request{Op: "reply", IDs: []string{id}, Payload: Object{"msg_type": msgtype, "content": content}})
	if err != nil {
		return nil, err
	}
	if !zero(body["code"]) {
		return nil, ErrRejected
	}
	d := obj(body["data"])
	if value, present := body["data"]; present && obj(value) == nil {
		return nil, ErrUnsupported
	}
	return &Result{Success: true, MessageID: d["message_id"], ChatID: d["chat_id"], MsgType: d["msg_type"], RootID: d["root_id"], ParentID: d["parent_id"], ThreadID: d["thread_id"]}, nil
}
func (c *Client) send(ctx context.Context, typ string, content Object, t Target) (_out *Result, _error error) {
	defer sourceErrors(&_error)
	if t.OriginalMessageID != "" {
		return c.ReplyMessage(ctx, t.OriginalMessageID, typ, content)
	}
	id, idType, e := c.ResolveTarget(t)
	if e != nil {
		return nil, e
	}
	return c.SendMessage(ctx, id, idType, typ, content)
}
func (c *Client) SendText(ctx context.Context, text string, t Target) (_out *Result, _error error) {
	defer sourceErrors(&_error)
	r, e := c.send(ctx, "text", Object{"text": text}, t)
	return c.fallbackChat(r, t), e
}
func (c *Client) UploadImage(ctx context.Context, b Blob, imageType string) (_out string, _error error) {
	defer sourceErrors(&_error)
	if imageType == "" {
		imageType = "message"
	}
	mime, _, e := GuessMIME(b.Name)
	if e != nil {
		return "", e
	}
	if !strings.HasPrefix(mime, "image/") {
		mime = "image/png"
	}
	b.MIME = mime
	_, d, e := c.call(ctx, Request{Op: "upload_image", Payload: Object{"image_type": imageType}, Blob: &b})
	if e != nil {
		return "", e
	}
	if !zero(d["code"]) {
		return "", ErrRejected
	}
	return text(obj(d["data"])["image_key"]), nil
}
func (c *Client) UploadFile(ctx context.Context, b Blob, name, fileType string) (_out string, _error error) {
	defer sourceErrors(&_error)
	if name != "" {
		b.Name = name
	}
	if fileType == "" {
		fileType = "stream"
	}
	b.MIME = "application/octet-stream"
	_, d, e := c.call(ctx, Request{Op: "upload_file", Query: Object{"file_type": fileType}, Blob: &b})
	if e != nil {
		return "", e
	}
	if !zero(d["code"]) {
		return "", ErrRejected
	}
	return text(obj(d["data"])["file_key"]), nil
}

var imageSuffix = map[string]bool{".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true, ".bmp": true, ".ico": true, ".tiff": true, ".heic": true}

func (c *Client) SendFile(ctx context.Context, ref, title, text, name string, t Target) (_out *Result, _error error) {
	defer sourceErrors(&_error)
	if c.host == nil {
		return nil, ErrDependency
	}
	b, e := c.host.ReadFile(ctx, ref)
	if e != nil {
		return &Result{}, e
	}
	image := imageSuffix[legacyanime.LowerCase(SourceSuffix(b.Name))]
	var r *Result
	if image {
		var key string
		key, e = c.UploadImage(ctx, b, "message")
		if e == nil && key != "" {
			r, e = c.send(ctx, "interactive", buildCard(CardInput{Title: title, Text: text, ImageKey: key}), t)
		}
	} else {
		var key string
		key, e = c.UploadFile(ctx, b, name, "stream")
		if e == nil && key != "" {
			r, e = c.send(ctx, "file", Object{"file_key": key}, t)
		}
		if r != nil && (title != "" || text != "") {
			_, captionErr := c.SendText(ctx, BuildMessageText(title, text, ""), t)
			if err := c.ignoreRejection(ctx, captionErr, "Lark 文本消息发送失败"); err != nil {
				return c.fallbackChat(r, t), err
			}
		}
	}
	return c.fallbackChat(r, t), c.ignoreRejection(ctx, e, "Lark 附件发送失败")
}
func (c *Client) SendVoice(ctx context.Context, ref, caption string, t Target) (_out *Result, _error error) {
	defer sourceErrors(&_error)
	if c.host == nil {
		return nil, ErrDependency
	}
	b, e := c.host.ReadFile(ctx, ref)
	if e != nil {
		return &Result{}, e
	}
	key, e := c.UploadFile(ctx, b, b.Name, "stream")
	if e != nil || key == "" {
		return &Result{}, c.ignoreRejection(ctx, e, "Lark 语音消息发送失败")
	}
	r, e := c.send(ctx, "audio", Object{"file_key": key}, t)
	if r != nil && caption != "" {
		_, captionErr := c.SendText(ctx, caption, t)
		if err := c.ignoreRejection(ctx, captionErr, "Lark 文本消息发送失败"); err != nil {
			return c.fallbackChat(r, t), err
		}
	}
	return c.fallbackChat(r, t), c.ignoreRejection(ctx, e, "Lark 语音消息发送失败")
}
func (c *Client) remoteImage(ctx context.Context, ref string) (_out string, _error error) {
	defer sourceErrors(&_error)
	ref = strip(ref)
	if ref == "" {
		return "", nil
	}
	if strings.HasPrefix(ref, "feishu://image/") {
		parts := strings.Split(strings.TrimPrefix(ref, "feishu://image/"), "/")
		return strip(parts[len(parts)-1]), nil
	}
	if c.host == nil {
		return "", ErrDependency
	}
	b, e := c.host.FetchImage(ctx, ref)
	if e != nil {
		return "", e
	}
	if len(b.Data) == 0 {
		return "", nil
	}
	b, e = remoteImageBlob(ref, b)
	if e != nil {
		return "", e
	}
	return c.UploadImage(ctx, b, "message")
}
func (c *Client) SendNotification(ctx context.Context, n Notification, t Target, items []ImageItem) (_out *Result, _error error) {
	defer sourceErrors(&_error)
	built := []ImageItem{}
	for _, item := range items {
		key, err := c.remoteImage(ctx, item.URL)
		if errors.Is(err, ErrUnknown) {
			return nil, err
		}
		built = append(built, ImageItem{Title: item.Title, ImageKey: key})
	}
	key := ""
	if len(built) == 0 {
		var err error
		key, err = c.remoteImage(ctx, n.Image)
		if errors.Is(err, ErrUnknown) {
			return nil, err
		}
	}
	r, e := c.send(ctx, "interactive", buildCard(CardInput{Title: n.Title, Text: n.Text, Link: n.Link, Buttons: n.Buttons, ImageKey: key, HeaderTemplate: HeaderTemplate(n.MType), ImageItems: built}), t)
	return c.fallbackChat(r, t), c.ignoreRejection(ctx, e, "Lark 通知发送失败")
}
func (c *Client) EditMessage(ctx context.Context, id, title, text string, buttons [][]Button) (_out bool, _error error) {
	defer sourceErrors(&_error)
	if id == "" {
		return false, nil
	}
	_, d, e := c.call(ctx, Request{Op: "edit", IDs: []string{id}, Payload: Object{"content": buildCard(CardInput{Title: title, Text: text, Buttons: buttons})}})
	if e == nil && !zero(d["code"]) {
		e = ErrRejected
	}
	return e == nil, c.ignoreRejection(ctx, e, "Lark 消息更新失败")
}
func (c *Client) AddReaction(ctx context.Context, id, emoji string) (_out string, _error error) {
	defer sourceErrors(&_error)
	if id == "" || emoji == "" {
		return "", nil
	}
	_, d, e := c.call(ctx, Request{Op: "reaction_add", IDs: []string{id}, Payload: Object{"reaction_type": Object{"emoji_type": emoji}}})
	if e == nil && !zero(d["code"]) {
		e = ErrRejected
	}
	if e != nil {
		return "", c.ignoreRejection(ctx, e, "Lark 表情添加失败")
	}
	id, _ = obj(d["data"])["reaction_id"].(string)
	if !safeSegment(id) {
		return "", ErrUnknown
	}
	return id, nil
}
func (c *Client) DeleteReaction(ctx context.Context, id, reaction string) (_out bool, _error error) {
	defer sourceErrors(&_error)
	if id == "" || reaction == "" {
		return false, nil
	}
	_, d, e := c.call(ctx, Request{Op: "reaction_delete", IDs: []string{id, reaction}})
	if e == nil && !zero(d["code"]) {
		e = ErrRejected
	}
	return e == nil, c.ignoreRejection(ctx, e, "Lark 表情删除失败")
}
func (c *Client) BatchGetID(ctx context.Context, emails, mobiles, employees []string) (map[string]string, error) {
	out := map[string]string{}
	payload := Object{}
	if len(emails) > 0 {
		payload["emails"] = emails
	}
	if len(mobiles) > 0 {
		payload["mobiles"] = mobiles
	}
	if len(employees) > 0 {
		payload["employee_ids"] = employees
	}
	if len(payload) == 0 {
		return out, nil
	}
	_, d, e := c.call(ctx, Request{Op: "user_batch", Payload: payload, Query: Object{"user_id_type": "open_id"}})
	if e != nil {
		return nil, e
	}
	if !zero(d["code"]) {
		msg, _ := d["msg"].(string)
		return nil, &APIError{ContactPermissionMissing: text(d["code"]) == "99991672" || strings.Contains(msg, "99991672") || strings.Contains(msg, "contact:user.id:readonly")}
	}
	for _, v := range arr(obj(d["data"])["user_list"]) {
		item := obj(v)
		id := text(item["user_id"])
		if id == "" {
			continue
		}
		for _, key := range []string{"email", "mobile", "employee_id", "employee_no"} {
			if s := text(item[key]); s != "" {
				out[s] = id
			}
		}
	}
	return out, nil
}

func SourceSuffix(name string) string {
	base := path.Base(name)
	i := strings.LastIndexByte(base, '.')
	if i <= 0 || i >= len(base)-1 {
		return ""
	}
	return base[i:]
}
