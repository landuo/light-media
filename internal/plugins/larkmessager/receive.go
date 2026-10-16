package larkmessager

import (
	"encoding/json"
	"errors"
	"strings"
)

func requireObject(v any) (_out Object, _error error) {
	defer sourceErrors(&_error)
	if v == nil {
		return Object{}, nil
	}
	o := obj(v)
	if o == nil {
		return nil, ErrUnsupported
	}
	return o, nil
}

// ParsePostContent consumes a source-shaped post. Unknown-locale fallback needs
// its original JSON key order; pass localeOrder from the trusted raw decoder.
func ParsePostContent(content Object, messageID string, localeOrder []string) (_text string, _images []any, _error error) {
	defer sourceErrors(&_error)
	var postBody Object
	if _, ok := content["content"].([]any); ok {
		postBody = content
	}
	post := obj(content["post"])
	if post != nil && !truth(postBody) {
		for _, locale := range []string{"zh_cn", "en_us", "ja_jp"} {
			if body := obj(post[locale]); body != nil {
				postBody = body
				break
			}
		}
		if !truth(postBody) {
			if ordered, ok := content["post"].(*SourceObject); ok {
				localeOrder = ordered.Keys
			}
			if localeOrder == nil {
				if len(post) > 1 {
					return "", nil, ErrUnsupported
				}
				for _, body := range post {
					if body := obj(body); body != nil {
						postBody = body
					}
				}
			} else {
				for _, locale := range localeOrder {
					if body := obj(post[locale]); body != nil {
						postBody = body
						break
					}
				}
			}
		}
	}
	if !truth(postBody) {
		return "", nil, nil
	}
	lines := []string{}
	if title := trim(postBody["title"]); title != "" {
		lines = append(lines, title)
	}
	images := []any{}
	for _, rv := range arr(postBody["content"]) {
		row, ok := rv.([]any)
		if !ok {
			continue
		}
		parts := []string{}
		for _, ev := range row {
			el := obj(ev)
			if el == nil {
				continue
			}
			tag := trim(el["tag"])
			key := trim(el["image_key"])
			if tag == "img" && key != "" {
				path := key
				if messageID != "" {
					path = messageID + "/" + key
				}
				images = append(images, Object{"ref": "feishu://image/" + path})
			}
			switch tag {
			case "text", "plain_text":
				parts = append(parts, text(first(el["text"], el["content"])))
			case "a":
				s, url := trim(el["text"]), trim(first(el["href"], el["url"]))
				if s != "" && url != "" && s != url {
					parts = append(parts, s+" "+url)
				} else {
					parts = append(parts, text(first(s, url)))
				}
			case "at":
				s := trim(first(el["user_name"], el["name"], el["user_id"]))
				if s != "" {
					parts = append(parts, " @"+s)
				}
			case "code_block", "pre":
				code, lang := trim(first(el["text"], el["content"])), trim(el["language"])
				if code != "" {
					parts = append(parts, "```"+lang+"\n"+code+"\n```")
				}
			default:
				parts = append(parts, text(first(el["text"], el["content"])))
			}
		}
		if rowText := strip(strings.Join(parts, "")); rowText != "" {
			lines = append(lines, rowText)
		}
	}
	if len(images) == 0 {
		images = nil
	}
	return strip(strings.Join(lines, "\n")), images, nil
}
func ReceivePayload(body Object) (_out Object, _error error) {
	defer sourceErrors(&_error)
	evt, e := requireObject(body["event"])
	if e != nil {
		return nil, e
	}
	message, e := requireObject(evt["message"])
	if e != nil {
		return nil, e
	}
	sender, e := requireObject(evt["sender"])
	if e != nil {
		return nil, e
	}
	id := sender["sender_id"]
	var open, user string
	if v := obj(id); v != nil {
		open, user = text(v["open_id"]), text(v["user_id"])
	} else if truth(id) {
		open = str(id)
	}
	mid, chat, chatType := text(message["message_id"]), text(message["chat_id"]), text(message["chat_type"])
	typ := text(first(message["message_type"], message["msg_type"]))
	var content Object
	var localeOrder []string
	if s, ok := message["content"].(string); ok {
		if parsed, parseError := DecodeSourceObject([]byte(s)); parseError != nil {
			content = Object{"raw": s}
		} else {
			content = parsed
			localeOrder = objectKeysAt([]byte(s), "post")
		}
	} else {
		content = obj(message["content"])
	}
	plain := ""
	var images, audio, files []any
	switch typ {
	case "text":
		if s, ok := content["text"].(string); ok {
			plain = strip(s)
		} else if content["text"] != nil {
			return nil, ErrUnsupported
		}
	case "image":
		if key := trim(content["image_key"]); key != "" {
			path := key
			if mid != "" {
				path = mid + "/" + key
			}
			images = []any{Object{"ref": "feishu://image/" + path}}
		}
	case "audio", "media", "file":
		if key := trim(content["file_key"]); key != "" {
			name := trim(content["file_name"])
			path := key
			if mid != "" {
				path = mid + "/" + key
			}
			if typ == "audio" {
				if name == "" {
					name = "audio.opus"
				}
				audio = []any{"feishu://file/" + path + "/" + name}
			} else {
				refName := name
				if refName == "" {
					refName = "attachment"
				}
				files = []any{Object{"ref": "feishu://file/" + path + "/" + refName, "name": nullString(name)}}
			}
		}
	case "post":
		var err error
		plain, images, err = ParsePostContent(content, mid, localeOrder)
		if err != nil {
			return nil, err
		}
	}
	return Object{"type": "message", "source": "Lark", "message_id": mid, "chat_id": chat, "chat_type": chatType, "message_type": typ, "text": plain, "images": images, "audio_refs": audio, "files": files, "sender": Object{"open_id": open, "user_id": user, "name": text(first(open, user))}}, nil
}

// objectKeysAt preserves original JSON insertion order for source post.values().
func objectKeysAt(raw []byte, path string) []string {
	d := json.NewDecoder(strings.NewReader(string(raw)))
	t, e := d.Token()
	if e != nil || t != json.Delim('{') {
		return nil
	}
	for d.More() {
		k, e := d.Token()
		if e != nil {
			return nil
		}
		var value json.RawMessage
		if d.Decode(&value) != nil {
			return nil
		}
		if k == path {
			q := json.NewDecoder(strings.NewReader(string(value)))
			t, e := q.Token()
			if e != nil || t != json.Delim('{') {
				return nil
			}
			out := []string{}
			for q.More() {
				k, e := q.Token()
				if e != nil {
					return nil
				}
				s, ok := k.(string)
				if !ok {
					return nil
				}
				out = append(out, s)
				var skip json.RawMessage
				if q.Decode(&skip) != nil {
					return nil
				}
			}
			return out
		}
	}
	return nil
}
func CardActionPayload(body Object) (_out Object, _test bool, _error error) {
	defer sourceErrors(&_error)
	evt, e := requireObject(body["event"])
	if e != nil {
		return nil, false, e
	}
	action, e := requireObject(first(body["action"], evt["action"]))
	if e != nil {
		return nil, false, e
	}
	operator, e := requireObject(first(body["operator"], evt["operator"]))
	if e != nil {
		return nil, false, e
	}
	mid := text(obj(body["message"])["message_id"])
	if mid == "" {
		mid = text(obj(evt["context"])["open_message_id"])
		if mid == "" {
			mid = text(obj(evt["message"])["message_id"])
		}
	}
	chat := text(first(obj(evt["context"])["open_chat_id"], evt["chat_id"]))
	callback := extractCallback(action["value"], text(action["name"]))
	value := ""
	if callback != nil {
		value = *callback
	}
	if value == "" {
		value = text(action["tag"])
	}
	operatorIDs := obj(operator["operator_id"])
	open := text(first(operatorIDs["open_id"], operator["open_id"]))
	user := text(operatorIDs["user_id"])
	return Object{"type": "cardAction", "source": "Lark", "message_id": mid, "chat_id": chat, "callback_data": value, "sender": Object{"open_id": open, "user_id": user, "name": text(first(open, user))}}, value == "test_ok", nil
}

// SourceWebhook records source validation facts; Accepted is not an ingress
// authorization grant. The source exempts challenge/encrypted/card callbacks.
type SourceWebhook struct {
	Status            int
	Reply             Object
	Body              Object
	EventType         string
	Accepted          bool
	Encrypted         bool
	SignatureVerified bool
	TokenVerified     bool
	SignatureExempt   string
}

func DecodeSourceWebhook(raw []byte, headers map[string]string, queryToken, encryptKey, verificationToken string) (_out SourceWebhook, _error error) {
	defer sourceErrors(&_error)
	out := SourceWebhook{Status: 200}
	crypto := NewCrypto(encryptKey)
	value, initialError := DecodeSource(raw)
	if e := initialError; e != nil {
		if encryptKey != "" && len(raw) > 0 {
			plain, e := crypto.Decrypt(string(raw))
			if e != nil {
				out.Status = 400
				out.Reply = Object{"error": "decrypt failed"}
				return out, nil
			}
			if value, e = DecodeSource([]byte(plain)); e != nil {
				out.Status = 400
				out.Reply = Object{"error": "decrypt failed"}
				return out, nil
			}
			out.Encrypted = true
		} else {
			out.Status = 400
			out.Reply = Object{"error": "invalid body"}
			return out, nil
		}
	}
	body := obj(value)
	if truth(body["encrypt"]) {
		if encryptKey == "" {
			out.Status = 400
			out.Reply = Object{"error": "encrypt_key not configured", "hint": "请在插件配置中填写 Encrypt Key"}
			return out, nil
		}
		plain, e := crypto.Decrypt(text(body["encrypt"]))
		if e == nil {
			value, e = DecodeSource([]byte(plain))
		}
		if e != nil {
			out.Status = 400
			out.Reply = Object{"error": "decrypt failed"}
			return out, nil
		}
		body = obj(value)
		out.Encrypted = true
	}
	if body != nil {
		_, hasChallenge := body["challenge"]
		if hasChallenge || body["type"] == "url_verification" {
			challenge, ok := body["challenge"]
			if !ok {
				challenge = ""
			}
			out.Reply = Object{"challenge": challenge}
			out.SignatureExempt = "challenge"
			return out, nil
		}
	}
	header, e := requireObject(body["header"])
	if e != nil {
		return out, e
	}
	if encryptKey != "" {
		rawType := text(first(body["event_type"], header["event_type"]))
		if out.Encrypted {
			out.SignatureExempt = "encrypted"
		} else if rawType == "card.action.trigger" {
			out.SignatureExempt = "card.action.trigger"
		} else {
			signature := headerValue(headers, "X-Lark-Signature")
			if signature == "" {
				out.Status = 403
				out.Reply = Object{"error": "missing signature"}
				return out, nil
			}
			if !crypto.VerifySignature(signature, raw, headerValue(headers, "X-Lark-Request-Timestamp"), headerValue(headers, "X-Lark-Request-Nonce")) {
				out.Status = 403
				out.Reply = Object{"error": "signature verification failed"}
				return out, nil
			}
			out.SignatureVerified = true
		}
	}
	if verificationToken != "" {
		token := text(first(body["token"], header["token"], queryToken))
		if token != verificationToken {
			out.Status = 403
			out.Reply = Object{"error": "token verification failed"}
			return out, nil
		}
		out.TokenVerified = true
	}
	for _, field := range []string{"header", "event", "action", "operator", "message"} {
		if body[field] != nil && obj(body[field]) == nil {
			return out, ErrUnsupported
		}
	}
	if body["schema"] != nil {
		if _, ok := body["schema"].(string); !ok {
			return out, ErrUnsupported
		}
	}
	out.Body = body
	out.EventType = text(header["event_type"])
	out.Accepted = true
	out.Reply = Object{"success": true}
	return out, nil
}
func headerValue(h map[string]string, k string) string {
	for key, v := range h {
		if strings.EqualFold(key, k) {
			return v
		}
	}
	return ""
}

var errInvalidMessage = errors.New("invalid Lark message model")
