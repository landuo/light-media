package mergemessagenotify

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
)

func (p *Plugin) render(ctx context.Context, text any, variables map[string]any, encode bool) (any, error) {
	if !truth(text) || len(variables) == 0 {
		return text, nil
	}
	if encode {
		v := map[string]any{}
		for k, val := range variables {
			if k == "" {
				continue
			}
			if val != nil {
				s, err := templateString(val)
				if err != nil {
					return nil, err
				}
				val = quote(s)
			}
			v[k] = val
		}
		variables = v
	}
	if p.host.Render == nil {
		return nil, ErrDependency
	}
	return p.host.Render(ctx, text, variables)
}
func variables(m Message, ext map[string]any) map[string]any {
	v := cloneMap(ext)
	if v == nil {
		v = map[string]any{}
	}
	var typ any
	if m.Type != nil {
		typ = m.Type.Value
	}
	v["title"] = m.Title
	v["text"] = m.Text
	v["type"] = typ
	return v
}
func (p *Plugin) onebot(ctx context.Context, get func(string) any, m Message, ext map[string]any) (any, error) {
	s := get("server_url")
	if !truth(s) {
		return false, nil
	}
	server, e := stringValue(s)
	if e != nil {
		return nil, e
	}
	server = strings.TrimRight(server, "/")
	if server == "" || (!truth(get("user_ids")) && !truth(get("group_ids"))) {
		return false, nil
	}
	message, e := p.render(ctx, get("message_template"), variables(m, ext), false)
	if e != nil {
		return nil, e
	}
	var proxy any
	if truth(get("enable_proxy")) {
		proxy = p.host.Proxy
	}
	headers := map[string]any{}
	if token := get("access_token"); truth(token) {
		headers["Authorization"] = "Bearer " + str(token)
	}
	failed := 0
	for _, ids := range []struct{ key, field string }{{"user_ids", "user_id"}, {"group_ids", "group_id"}} {
		values, e := splitMulti(get(ids.key))
		if e != nil {
			return nil, e
		}
		for _, id := range values {
			if p.host.HTTP == nil {
				return nil, ErrDependency
			}
			r, e := p.host.HTTP(ctx, Request{"post", server + "/send_msg", map[string]any{"json": obj("message", message, ids.field, id), "headers": headers, "proxies": proxy}})
			if e != nil {
				return nil, e
			}
			j, e := r.json()
			if e != nil {
				return nil, e
			}
			if !r.OK() || (!numberEq(j["retcode"], 0) && !numberEq(j["retcode"], 1)) {
				failed++
			}
		}
	}
	return failed == 0, nil
}
func strToDict(v any) (any, error) {
	if !truth(v) {
		return map[string]any{}, nil
	}
	s, e := stringValue(v)
	if e != nil {
		return nil, e
	}
	if d, e := Decode([]byte(s)); e == nil {
		return d, nil
	} else if errors.Is(e, ErrDependency) {
		return nil, e
	}
	out := map[string]any{}
	for _, line := range splitLines(s) {
		if line == "" || !strings.Contains(line, ":") {
			continue
		}
		parts := strings.Split(line, ":")
		if len(parts) != 2 {
			return nil, errors.New("ValueError")
		}
		if parts[0] != "" {
			out[parts[0]] = parts[1]
		}
	}
	return out, nil
}
func (p *Plugin) httpChannel(ctx context.Context, get func(string) any, m Message, ext map[string]any) (any, error) {
	if !truth(get("method")) || !truth(get("url")) {
		return false, nil
	}
	vars := variables(m, ext)
	custom, e := strToDict(get("template_variables"))
	if e != nil {
		return nil, e
	}
	if truth(custom) {
		cm, ok := custom.(map[string]any)
		if !ok {
			return nil, errors.New("TypeError")
		}
		for k, v := range cm {
			vars[k] = v
		}
	}
	rawURL, e := p.render(ctx, get("url"), vars, true)
	if e != nil {
		return nil, e
	}
	url, e := stringValue(rawURL)
	if e != nil {
		return nil, e
	}
	rawHeaders, e := p.render(ctx, get("headers"), vars, false)
	if e != nil {
		return nil, e
	}
	headers, e := strToDict(rawHeaders)
	if e != nil {
		return nil, e
	}
	rawParams, e := p.render(ctx, get("params"), vars, false)
	if e != nil {
		return nil, e
	}
	params, e := strToDict(rawParams)
	if e != nil {
		return nil, e
	}
	var contentType any
	if truth(headers) {
		hm, ok := headers.(map[string]any)
		if !ok {
			return nil, errors.New("AttributeError")
		}
		for k, v := range hm {
			if strings.EqualFold(k, "Content-Type") {
				contentType = v
				break
			}
		}
	}
	body := get("body")
	if contentType == "application/json" && truth(body) {
		escaped := map[string]any{}
		for k, v := range vars {
			switch x := v.(type) {
			case string:
				b, _ := json.Marshal(x)
				escaped[k] = string(b[1 : len(b)-1])
			case nil:
				escaped[k] = "null"
			case bool:
				if x {
					escaped[k] = "true"
				} else {
					escaped[k] = "false"
				}
			case json.Number, int, float64:
				escaped[k] = x
			default:
				b, _ := json.Marshal(str(x))
				escaped[k] = string(b[1 : len(b)-1])
			}
		}
		body, e = p.render(ctx, body, escaped, false)
	} else {
		body, e = p.render(ctx, body, vars, false)
	}
	if e != nil {
		return nil, e
	}
	if bs, ok := body.(string); ok {
		if decoded, e := decodeOrdered(bs); e == nil {
			if contentType == "application/json" {
				body, e = pyJSONString(decoded)
				if e != nil {
					return nil, e
				}
			} else if contentType == "application/x-www-form-urlencoded" {
				body = decoded
			}
		} else if errors.Is(e, ErrDependency) {
			return nil, e
		} else if bs == "" {
			body = nil
		}
	} else {
		return nil, errors.New("TypeError")
	}
	var proxy any
	if truth(get("enable_proxy")) {
		proxy = p.host.Proxy
	}
	method, e := stringValue(get("method"))
	if e != nil {
		return nil, e
	}
	if p.host.HTTP == nil {
		return nil, ErrDependency
	}
	r, e := p.host.HTTP(ctx, Request{method, url, map[string]any{"headers": headers, "params": params, "data": body, "proxies": proxy}})
	if e != nil {
		return nil, e
	}
	return r.OK(), nil
}
