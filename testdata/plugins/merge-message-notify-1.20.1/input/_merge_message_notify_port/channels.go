package mergemessagenotify

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"math"
	"net/url"
	"strconv"
	"strings"
	"time"
)

func (p *Plugin) send(ctx context.Context, c Channel, m Message, config map[string]any) (any, error) {
	if truth(m.RawType) {
		return nil, errors.New("AttributeError")
	}
	get := func(k string) any {
		v := config[k]
		if v == nil {
			v = c.Defaults[k]
		}
		return v
	}
	if m.Type != nil && truth(get("enable_notify_types")) {
		yes, e := contains(get("enable_notify_types"), m.Type.Name)
		if e != nil {
			return nil, e
		}
		if !yes {
			return false, nil
		}
	}
	required := func(keys ...string) bool {
		for _, k := range keys {
			if !truth(get(k)) {
				return false
			}
		}
		return true
	}
	server := func() (string, error) {
		v := get("server_url")
		if !truth(v) {
			return "", nil
		}
		s, e := stringValue(v)
		return strings.TrimRight(s, "/"), e
	}
	ext := m.Ext
	if ext == nil {
		ext = map[string]any{}
	}
	withProxy := func(kwargs map[string]any) map[string]any {
		var proxy any
		if truth(get("enable_proxy")) {
			proxy = p.host.Proxy
		}
		kwargs["proxies"] = proxy
		return kwargs
	}
	http := func(method, url string, kwargs map[string]any) (Response, error) {
		if p.host.HTTP == nil {
			return Response{}, ErrDependency
		}
		return p.host.HTTP(ctx, Request{method, url, kwargs})
	}
	post := func(url string, payload any, proxy bool) (Response, error) {
		kwargs := map[string]any{"json": payload}
		if proxy {
			kwargs = withProxy(kwargs)
		}
		return http("post", url, kwargs)
	}
	response := func(r Response, err error, key string, accept func(any) bool) (any, error) {
		if err != nil {
			return nil, err
		}
		j, e := r.json()
		if e != nil {
			return nil, e
		}
		if !r.OK() && len(j) == 0 {
			return false, nil
		}
		return accept(j[key]), nil
	}
	zero := func(v any) bool { return numberEq(v, 0) }
	falsey := func(v any) bool { return !truth(v) }
	contentImage := func(text any) (string, error) {
		v, e := stringValue(text)
		if e != nil {
			return "", e
		}
		if truth(ext["image"]) {
			v += "\n\n![](" + str(ext["image"]) + ")"
		}
		return v, nil
	}
	key := strings.TrimPrefix(c.Key, "custom.")
	switch key {
	case "iyuu":
		if !truth(m.Title) || !required("token") {
			return false, nil
		}
		r, e := http("post", "https://iyuu.cn/"+str(get("token"))+".send", map[string]any{"params": obj("text", m.Title, "desp", or(m.Text, m.Title))})
		return response(r, e, "errcode", zero)
	case "chanify":
		if !required("token") {
			return false, nil
		}
		j := obj("title", m.Title, "text", m.Text)
		if truth(get("sound")) {
			j.put("sound", 1)
		}
		r, e := post("https://api.chanify.net/v1/sender/"+str(get("token")), j, false)
		return response(r, e, "res", falsey)
	case "pushplus":
		if !required("token") {
			return false, nil
		}
		text, e := contentImage(or(m.Text, m.Title))
		if e != nil {
			return nil, e
		}
		j := obj("token", get("token"), "title", m.Title, "topic", get("topic"), "template", "markdown", "channel", get("channel"), "content", text)
		r, e := post("http://www.pushplus.plus/send", j, false)
		return response(r, e, "code", func(v any) bool { return numberEq(v, 200) })
	case "pushdeer":
		if !truth(m.Title) {
			return false, nil
		}
		s, e := server()
		if e != nil {
			return nil, e
		}
		if s == "" || !required("push_key") {
			return false, nil
		}
		text, e := contentImage(or(m.Text, m.Title))
		if e != nil {
			return nil, e
		}
		j := obj("pushkey", get("push_key"), "text", m.Title, "type", "markdown", "desp", text)
		r, e := post(s+"/message/push", j, true)
		return response(r, e, "code", zero)
	case "serverchan":
		if !truth(m.Title) {
			return false, nil
		}
		if !required("send_key") {
			return nil, nil
		}
		text, e := contentImage(or(m.Text, m.Title))
		if e != nil {
			return nil, e
		}
		j := obj("title", m.Title, "noip", or(get("noip"), false), "desp", text)
		if values := get("channel"); truth(values) {
			items, e := iterable(values)
			if e != nil {
				return nil, e
			}
			if len(items) > 2 {
				items = items[:2]
			}
			parts := []string{}
			for _, v := range items {
				parts = append(parts, str(v))
			}
			j.put("channel", strings.Join(parts, "|"))
		}
		r, e := post("https://sctapi.ftqq.com/"+str(get("send_key"))+".send", j, false)
		return response(r, e, "code", zero)
	case "gotify":
		s, e := server()
		if e != nil {
			return nil, e
		}
		if s == "" || !required("token") {
			return false, nil
		}
		// Original send_message omits ext_info when calling __build_json. Preserve
		// that omission: image/link are deliberately absent from this payload.
		j := obj("title", or(m.Title, ""), "message", or(m.Text, ""), "priority", 2, "extras", obj("client::display", obj("contentType", "text/plain"), "client::notification", obj()))
		r, e := post(s+"/message?token="+str(get("token")), j, true)
		return response(r, e, "errorCode", falsey)
	case "pushme":
		s, e := server()
		if e != nil {
			return nil, e
		}
		if s == "" || !required("key_type", "key") {
			return false, nil
		}
		j := obj()
		if kt := get("key_type"); kt == "push_key" || kt == "temp_key" {
			j.put(str(kt), get("key"))
		}
		if truth(m.Title) {
			title := m.Title
			if topic := get("message_topic"); truth(topic) {
				symbols := map[string]string{"info": "[i]", "success": "[s]", "warning": "[w]", "failure": "[f]"}
				if symbol := symbols[str(topic)]; symbol != "" {
					title = symbol + " " + str(title)
				}
			}
			if mc := get("message_channel"); truth(mc) {
				title = str(title) + " [~" + str(mc) + "]"
			}
			j.put("title", title)
		}
		data := m.Text
		typ := "text"
		if truth(ext["image"]) {
			t, e := stringValue(data)
			if e != nil {
				return nil, e
			}
			data = t + "\n\n![](" + str(ext["image"]) + ")"
			typ = "markdown"
		}
		j.put("type", typ)
		j.put("content", data)
		r, e := http("post", str(get("server_url")), withProxy(map[string]any{"data": j}))
		if e != nil {
			return nil, e
		}
		return r.Text == "success", nil
	case "ntfy":
		s, e := server()
		if e != nil {
			return nil, e
		}
		if s == "" || !required("topic") || (!truth(get("token")) && (!truth(get("username")) || !truth(get("password")))) {
			return false, nil
		}
		topic, e := stringValue(get("topic"))
		if e != nil {
			return nil, e
		}
		headers := obj()
		if truth(get("enable_md")) {
			headers.put("X-Markdown", "true")
		}
		if truth(m.Title) {
			title, e := stringValue(m.Title)
			if e != nil {
				return nil, e
			}
			headers.put("X-Title", []byte(title))
		}
		if m.Type != nil {
			headers.put("X-Tags", []byte(m.Type.Value))
		}
		if truth(ext["image"]) && !truth(get("enable_md")) {
			headers.put("X-Attach", ext["image"])
		}
		if truth(get("token")) {
			headers.put("Authorization", "Bearer "+str(get("token")))
		} else {
			headers.put("Authorization", "Basic "+encode64([]byte(str(get("username"))+":"+str(get("password")))))
		}
		data, e := stringValue(or(m.Text, m.Title))
		if e != nil {
			return nil, e
		}
		if truth(ext["image"]) && truth(get("enable_md")) {
			data += "\n\n![](" + str(ext["image"]) + ")"
		}
		r, e := http("post", s+"/"+quote(topic), withProxy(map[string]any{"headers": headers, "data": []byte(data)}))
		return response(r, e, "code", falsey)
	case "pushover":
		s, e := server()
		if e != nil {
			return nil, e
		}
		if s == "" || !required("user_key", "api_token") {
			return false, nil
		}
		j := obj("user", get("user_key"), "token", get("api_token"), "title", m.Title, "message", or(m.Text, m.Title))
		if v := get("sound"); truth(v) {
			j.put("sound", v)
		}
		if v := get("priority"); v != nil {
			j.put("priority", v)
			if numberEq(v, 2) {
				j.put("retry", 60)
				j.put("expire", 1800)
			}
		}
		if truth(ext["image"]) {
			var attachment any
			r, e := http("get", str(ext["image"]), map[string]any{"stream": true, "timeout": 5})
			if e == nil && r.OK() {
				attachment = encode64(r.Content)
			}
			j.put("attachment_base64", attachment)
		}
		if truth(ext["link"]) {
			j.put("url", ext["link"])
			j.put("url_title", ext["link"])
		}
		r, e := post(s+"/1/messages.json", j, true)
		if e != nil {
			return nil, e
		}
		data, e := r.json()
		if e != nil {
			return nil, e
		}
		return numberEq(data["status"], 1), nil
	case "dingtalkrobot", "qiyeweixinbot", "feishubot":
		if !truth(m.Text) {
			return false, nil
		}
		if key == "qiyeweixinbot" && !required("key") {
			return nil, nil
		}
		if key != "qiyeweixinbot" && !required("access_token") {
			return false, nil
		}
		return p.bot(ctx, key, get, m, ext)
	case "bark":
		return p.bark(ctx, get, m, ext)
	case "onebot-11":
		return p.onebot(ctx, get, m, ext)
	case "http":
		return p.httpChannel(ctx, get, m, ext)
	case "email":
		return p.email(ctx, get, m, ext)
	case "apprise":
		raw := or(get("service_urls"), "")
		s, e := stringValue(raw)
		if e != nil {
			return nil, e
		}
		urls := []string{}
		for _, line := range splitLines(s) {
			if line = strip(line); line != "" {
				urls = append(urls, line)
			}
		}
		if len(urls) == 0 {
			return false, nil
		}
		if p.host.Apprise == nil {
			return nil, ErrDependency
		}
		v, e := p.host.Apprise(ctx, AppriseRequest{urls, m.Title, or(m.Text, m.Title), ext["image"]})
		if e != nil {
			return nil, e
		}
		return truth(v), nil
	}
	return nil, errors.New("unknown channel")
}
func quote(s string) string {
	const hex = "0123456789ABCDEF"
	var b strings.Builder
	for _, c := range []byte(s) {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("_.-~/", rune(c)) {
			b.WriteByte(c)
		} else {
			b.WriteByte('%')
			b.WriteByte(hex[c>>4])
			b.WriteByte(hex[c&15])
		}
	}
	return b.String()
}
func splitLines(s string) []string {
	return strings.FieldsFunc(s, func(c rune) bool {
		return c == '\n' || c == '\r' || c == '\v' || c == '\f' || c == 0x1c || c == 0x1d || c == 0x1e || c == 0x85 || c == 0x2028 || c == 0x2029
	})
}
func (p *Plugin) now() time.Time {
	if p.host.Now != nil {
		return p.host.Now()
	}
	return time.Now()
}
func (p *Plugin) bot(ctx context.Context, key string, get func(string) any, m Message, ext map[string]any) (any, error) {
	var endpoint string
	var j object
	markdown := func(trailing bool) string {
		out := ""
		if truth(m.Title) {
			out += "# " + str(m.Title) + "\n\n"
		}
		if truth(m.Text) {
			out += "> " + str(m.Text) + "\n\n"
		}
		if truth(ext["image"]) {
			out += "![](" + str(ext["image"]) + ")"
			if trailing {
				out += "\n\n"
			}
		}
		return out
	}
	switch key {
	case "dingtalkrobot":
		// __sign first quote_plus encodes Base64, then urlencode encodes that
		// string a second time. This intentionally retains the original behavior.
		query := "access_token=" + url.QueryEscape(str(get("access_token")))
		if secret := get("secret"); truth(secret) {
			stamp := strconv.FormatInt(int64(math.RoundToEven(float64(p.now().UnixNano())/1e6)), 10)
			h := hmac.New(sha256.New, []byte(str(secret)))
			h.Write([]byte(stamp + "\n" + str(secret)))
			sign := url.QueryEscape(encode64(h.Sum(nil)))
			query += "&timestamp=" + stamp + "&sign=" + url.QueryEscape(sign)
		}
		endpoint = "https://oapi.dingtalk.com/robot/send?" + query
		typ := "text"
		body := obj("content", m.Text)
		if truth(ext["link"]) && truth(ext["image"]) {
			typ = "link"
			body = obj("title", m.Title, "text", m.Text, "picUrl", ext["image"], "messageUrl", ext["link"])
		} else if truth(ext["image"]) || truth(m.Title) {
			typ = "markdown"
			body = obj("title", m.Title, "text", markdown(false))
		}
		mob, e := splitMulti(get("atMobiles"))
		if e != nil {
			return nil, e
		}
		ids, e := splitMulti(get("atUserIds"))
		if e != nil {
			return nil, e
		}
		j = obj("msgtype", typ, typ, body, "at", obj("atMobiles", mob, "atUserIds", ids, "isAtAll", truth(get("isAtAll"))))
	case "qiyeweixinbot":
		endpoint = "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=" + str(get("key"))
		typ := "markdown"
		content := any(markdown(true))
		if !truth(m.Title) && truth(m.Text) && !truth(ext["image"]) {
			typ = "text"
			content = m.Text
		}
		mob, e := splitMulti(get("mentioned_mobile_list"))
		if e != nil {
			return nil, e
		}
		ids, e := splitMulti(get("mentioned_list"))
		if e != nil {
			return nil, e
		}
		if truth(get("isAtAll")) {
			mob = append(mob, "@all")
		}
		j = obj("msgtype", typ, typ, obj("content", content, "mentioned_mobile_list", mob, "mentioned_list", ids))
	case "feishubot":
		endpoint = "https://open.feishu.cn/open-apis/bot/v2/hook/" + str(get("access_token"))
		ids, e := splitMulti(get("at_user_ids"))
		if e != nil {
			return nil, e
		}
		typ := "post"
		var body any
		if !truth(m.Title) && truth(m.Text) && !truth(ext["link"]) {
			typ = "text"
			text, e := stringValue(m.Text)
			if e != nil {
				return nil, e
			}
			at := []string{}
			for _, id := range ids {
				at = append(at, "<at user_id=\""+id+"\"></at>")
			}
			if len(at) > 0 {
				text += "\n" + strings.Join(at, " ")
			}
			if truth(get("at_all")) {
				text += "\n<at user_id=\"all\">所有人</at>"
			}
			body = obj("text", text)
		} else {
			content := []any{[]any{obj("tag", "text", "text", m.Text)}}
			if truth(ext["link"]) {
				content = append(content, []any{obj("tag", "a", "text", ">>点此查看详情<<", "href", ext["link"])})
			}
			at := []any{}
			for _, id := range ids {
				at = append(at, obj("tag", "at", "user_id", id))
			}
			if truth(get("at_all")) {
				at = append(at, obj("tag", "at", "user_id", "all", "user_name", "所有人"))
			}
			if len(at) > 0 {
				content = append(content, at)
			}
			body = obj("post", obj("zh_cn", obj("title", m.Title, "content", content)))
		}
		j = obj("msg_type", typ, "content", body)
		if secret := get("secret"); truth(secret) {
			stamp := strconv.FormatInt(int64(math.RoundToEven(float64(p.now().UnixNano())/1e9)), 10)
			h := hmac.New(sha256.New, []byte(stamp+"\n"+str(secret)))
			j.put("timestamp", stamp)
			j.put("sign", encode64(h.Sum(nil)))
		}
	}
	if p.host.HTTP == nil {
		return nil, ErrDependency
	}
	r, e := p.host.HTTP(ctx, Request{"post", endpoint, map[string]any{"json": j}})
	if e != nil {
		return nil, e
	}
	data, e := r.json()
	if e != nil {
		return nil, e
	}
	if !r.OK() && len(data) == 0 {
		return false, nil
	}
	code := data["errcode"]
	if key == "feishubot" {
		code = data["code"]
	}
	return numberEq(code, 0) || (key == "dingtalkrobot" && code == "0"), nil
}
