package mergemessagenotify

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode/utf16"
)

// pyJSONString matches json.dumps ensure_ascii=True, including spaces and
// insertion order. It is used before AES; a reordered map changes ciphertext.
func pyJSONString(v any) (string, error) {
	quoteString := func(s string) string {
		var b strings.Builder
		b.WriteByte('"')
		for _, r := range s {
			switch r {
			case '\\', '"':
				b.WriteByte('\\')
				b.WriteRune(r)
			case '\b':
				b.WriteString(`\b`)
			case '\f':
				b.WriteString(`\f`)
			case '\n':
				b.WriteString(`\n`)
			case '\r':
				b.WriteString(`\r`)
			case '\t':
				b.WriteString(`\t`)
			default:
				if r < 32 || r >= 127 {
					if r > 0xffff {
						a, c := utf16.EncodeRune(r)
						fmt.Fprintf(&b, "\\u%04x\\u%04x", a, c)
					} else {
						fmt.Fprintf(&b, "\\u%04x", r)
					}
				} else {
					b.WriteRune(r)
				}
			}
		}
		b.WriteByte('"')
		return b.String()
	}
	switch x := v.(type) {
	case nil:
		return "null", nil
	case string:
		return quoteString(x), nil
	case bool:
		if x {
			return "true", nil
		}
		return "false", nil
	case int:
		return strconv.Itoa(x), nil
	case json.Number:
		return pythonNumber(x), nil
	case object:
		parts := []string{}
		for _, p := range x {
			v, e := pyJSONString(p.v)
			if e != nil {
				return "", e
			}
			parts = append(parts, quoteString(jsonKey(p.k))+": "+v)
		}
		return "{" + strings.Join(parts, ", ") + "}", nil
	case []any:
		parts := []string{}
		for _, p := range x {
			v, e := pyJSONString(p)
			if e != nil {
				return "", e
			}
			parts = append(parts, v)
		}
		return "[" + strings.Join(parts, ", ") + "]", nil
	default:
		return "", errors.New("ordered Python JSON input required")
	}
}
func decodeOrdered(raw string) (any, error) {
	if extendedJSON(raw) {
		return nil, ErrDependency
	}
	d := json.NewDecoder(strings.NewReader(raw))
	d.UseNumber()
	var read func() (any, error)
	read = func() (any, error) {
		t, e := d.Token()
		if e != nil {
			return nil, e
		}
		switch t {
		case json.Delim('{'):
			o := obj()
			for d.More() {
				k, e := d.Token()
				if e != nil {
					return nil, e
				}
				v, e := read()
				if e != nil {
					return nil, e
				}
				o.put(k.(string), v)
			}
			_, e = d.Token()
			return o, e
		case json.Delim('['):
			a := []any{}
			for d.More() {
				v, e := read()
				if e != nil {
					return nil, e
				}
				a = append(a, v)
			}
			_, e = d.Token()
			return a, e
		default:
			return t, nil
		}
	}
	v, e := read()
	if e != nil {
		return nil, e
	}
	if _, e = d.Token(); e != io.EOF {
		return nil, errors.New("invalid trailing JSON")
	}
	return v, nil
}
func (p *Plugin) bark(ctx context.Context, get func(string) any, m Message, ext map[string]any) (any, error) {
	s := get("server_url")
	if !truth(s) {
		return false, nil
	}
	server, e := stringValue(s)
	if e != nil {
		return nil, e
	}
	server = strings.TrimRight(server, "/")
	if server == "" || !truth(get("push_key")) {
		return false, nil
	}
	enabled := truth(get("cipher_enable"))
	if enabled && !validCipher(get) {
		return false, nil
	}
	j := obj("title", m.Title, "body", or(m.Text, m.Title))
	for _, pair := range []struct{ config, json string }{{"sound", "sound"}, {"group", "group"}, {"level", "level"}} {
		if v := get(pair.config); truth(v) {
			j.put(pair.json, v)
		}
	}
	// Original order is title,body,sound,icon,group,url,level,isArchive,autoCopy.
	j = obj("title", m.Title, "body", or(m.Text, m.Title))
	if v := get("sound"); truth(v) {
		j.put("sound", v)
	}
	if v := ext["image"]; truth(v) {
		j.put("icon", v)
	}
	if v := get("group"); truth(v) {
		j.put("group", v)
	}
	if v := ext["link"]; truth(v) {
		j.put("url", v)
	}
	if v := get("level"); truth(v) {
		j.put("level", v)
	}
	if truth(get("isArchive")) {
		j.put("isArchive", 1)
	}
	if truth(get("autoCopy")) {
		j.put("autoCopy", 1)
	}
	if more := get("more_json"); truth(more) {
		raw, e := stringValue(more)
		if e != nil {
			return nil, e
		}
		if v, e := decodeOrdered(raw); e == nil {
			updateMore(&j, v)
		} else if errors.Is(e, ErrDependency) {
			return nil, e
		}
	}
	var payload any = j
	if enabled {
		plain, e := pyJSONString(j)
		if e == nil {
			var encrypted, iv any
			encrypted, iv, e = p.encrypt(plain, str(get("cipher_mode")), str(get("cipher_key")))
			if e == nil {
				payload = obj("ciphertext", encrypted, "iv", iv)
			}
		} /* Original encryption exception downgrades to plaintext. */
	}
	var proxy any
	if truth(get("enable_proxy")) {
		proxy = p.host.Proxy
	}
	if p.host.HTTP == nil {
		return nil, ErrDependency
	}
	r, e := p.host.HTTP(ctx, Request{"post", server + "/" + str(get("push_key")), map[string]any{"json": payload, "proxies": proxy}})
	if e != nil {
		return nil, e
	}
	v, e := r.json()
	if e != nil {
		return nil, e
	}
	if !r.OK() && len(v) == 0 {
		return false, nil
	}
	return numberEq(v["code"], 200), nil
}

// dict.update accepts both objects and iterable pairs. On an invalid later
// entry the source catch preserves earlier mutations; do not roll them back.
func updateMore(dst *object, v any) {
	if o, ok := v.(object); ok {
		for _, p := range o {
			dst.putAny(p.k, p.v)
		}
		return
	}
	var entries []any
	switch x := v.(type) {
	case []any:
		entries = x
	case string:
		for _, r := range x {
			entries = append(entries, string(r))
		}
	default:
		return
	}
	for _, entry := range entries {
		var pair []any
		switch x := entry.(type) {
		case []any:
			pair = x
		case string:
			for _, r := range x {
				pair = append(pair, string(r))
			}
		case object:
			for _, p := range x {
				pair = append(pair, p.k)
			}
		default:
			return
		}
		if len(pair) != 2 {
			return
		}
		switch pair[0].(type) {
		case object, []any:
			return
		}
		dst.putAny(pair[0], pair[1])
	}
}
func validCipher(get func(string) any) bool {
	alg := get("cipher_algorithm")
	mode := get("cipher_mode")
	padding := get("cipher_padding")
	key := get("cipher_key")
	sizes := map[string]int{"AES128": 16, "AES192": 24, "AES256": 32}
	n, ok := sizes[str(alg)]
	if !ok || !(mode == "CBC" || mode == "ECB" || mode == "GCM") || !(padding == "noPadding" || padding == "pkcs7") || !truth(key) {
		return false
	}
	s, e := stringValue(key)
	return e == nil && len([]byte(s)) == n
}
func (p *Plugin) encrypt(plain, mode, key string) (any, any, error) {
	block, e := aes.NewCipher([]byte(key))
	if e != nil {
		return nil, nil, e
	}
	data := []byte(plain)
	if mode == "CBC" || mode == "ECB" {
		n := 16 - len(data)%16
		data = append(data, bytes.Repeat([]byte{byte(n)}, n)...)
	}
	if mode == "ECB" {
		out := make([]byte, len(data))
		for i := 0; i < len(data); i += 16 {
			block.Encrypt(out[i:i+16], data[i:i+16])
		}
		return encode64(out), nil, nil
	}
	uuid := ""
	if p.host.UUID != nil {
		uuid = p.host.UUID()
	} else {
		b := make([]byte, 16)
		if _, e = rand.Read(b); e != nil {
			return nil, nil, e
		}
		b[6] = (b[6] & 15) | 0x40
		b[8] = (b[8] & 63) | 0x80
		uuid = hex.EncodeToString(b)
	}
	uuid = strings.ReplaceAll(uuid, "-", "")
	if len(uuid) < 16 {
		return nil, nil, errors.New("invalid UUID source")
	}
	if mode == "CBC" {
		iv := uuid[:16]
		out := make([]byte, len(data))
		cipher.NewCBCEncrypter(block, []byte(iv)).CryptBlocks(out, data)
		return encode64(out), iv, nil
	}
	if mode == "GCM" {
		iv := uuid[:12]
		a, e := cipher.NewGCM(block)
		if e != nil {
			return nil, nil, e
		}
		return encode64(a.Seal(nil, []byte(iv), data, nil)), iv, nil
	}
	return nil, nil, nil
}
