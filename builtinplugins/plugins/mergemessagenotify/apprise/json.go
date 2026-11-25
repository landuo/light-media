package apprise

import (
	"encoding/json"
	"strconv"
	"strings"
	"unicode"
)

type field struct {
	key   string
	value any
}
type object []field
type jsonService struct {
	method, endpoint, overflow, format string
	headers, params                    map[string]string
	headerOrder, paramOrder            [][2]string
	extras                             [][2]string
	auth                               []any
	verify                             bool
	timeout                            [2]float64
	emojis                             bool
}

func parseJSONService(raw string) (jsonService, bool, error) {
	s := jsonService{method: "POST", headers: map[string]string{"User-Agent": "Apprise", "Content-Type": "application/json"}, params: map[string]string{}, verify: true, timeout: [2]float64{4, 4}, overflow: "upstream", format: "text"}
	s.headerOrder = [][2]string{{"User-Agent", "Apprise"}, {"Content-Type", "application/json"}}
	u, valid := parseSourceURL(raw)
	if !valid {
		return s, false, nil
	}
	scheme := "http"
	if u.scheme == "jsons" {
		scheme = "https"
	}
	s.endpoint = scheme + "://" + u.host + u.port + u.path
	if u.user != nil && *u.user != "" {
		var pass any
		if u.password != nil {
			pass = *u.password
		}
		s.auth = []any{*u.user, pass}
	}
	for _, extra := range u.headers {
		s.headers[extra[0]] = extra[1]
		s.headerOrder = orderedPairSet(s.headerOrder, extra[0], extra[1])
	}
	for _, extra := range u.params {
		s.params[extra[0]] = extra[1]
	}
	s.extras = u.extras
	s.paramOrder = u.params
	for key, value := range u.query {
		switch key {
		case "method":
			if value != "" {
				s.method = strings.ToUpper(sourceUnquote(value))
			}
		case "verify":
			s.verify = parseBool(value, false)
		case "cto", "rto":
			if v, e := strconv.ParseFloat(value, 64); e == nil {
				if key == "cto" {
					s.timeout[0] = v
				} else {
					s.timeout[1] = v
				}
			}
		case "overflow":
			value = strings.ToLower(value)
			if value == "truncate" || value == "split" || value == "upstream" {
				s.overflow = value
			}
		case "format":
			value = strings.ToLower(value)
			if value == "text" || value == "html" || value == "markdown" {
				s.format = value
			}
		case "emojis":
			s.emojis = parseBool(value, false)
		case "tz":
			if !validSourceTimezone(value) {
				return s, false, nil
			}
		}
	}
	switch s.method {
	case "POST", "GET", "DELETE", "PUT", "HEAD", "PATCH", "UPDATE", "OPTIONS":
		return s, true, nil
	}
	return s, false, nil
}
func parseBool(s string, defaultValue bool) bool {
	s = strings.ToLower(s)
	r := []rune(s)
	if len(r) > 2 {
		s = string(r[:2])
	}
	switch s {
	case "de", "di", "ne", "f", "n", "no", "of", "0", "fa":
		return false
	case "en", "al", "t", "y", "ye", "on", "1", "tr":
		return true
	}
	return defaultValue
}
func sourceSpace(r rune) bool { return unicode.IsSpace(r) || r >= 0x1c && r <= 0x1f }
func (s jsonService) successful(status int) bool {
	return status >= 200 && status < 300
}
func (s jsonService) requests(title, body string) ([]Request, error) {
	return notificationRequests(title, body, s.emojis, s.overflow, s.format, func(title, body string) (Request, error) {
		return s.request(title, body, nil)
	})
}

func (s jsonService) truncateAttachments() bool { return s.overflow == "truncate" }

func (s jsonService) attachmentChunks(title, body string) ([]notificationChunk, error) {
	return notificationChunks(title, body, s.emojis, s.overflow, s.format)
}

func notificationRequests(title, body string, emojis bool, overflow, format string, request func(string, string) (Request, error)) ([]Request, error) {
	chunks, err := notificationChunks(title, body, emojis, overflow, format)
	if err != nil {
		return nil, err
	}
	requests := make([]Request, 0, len(chunks))
	for _, chunk := range chunks {
		call, err := request(chunk.title, chunk.body)
		if err != nil {
			return nil, err
		}
		requests = append(requests, call)
	}
	return requests, nil
}

type notificationChunk struct{ title, body string }

func notificationChunks(title, body string, emojis bool, overflow, format string) ([]notificationChunk, error) {
	return notificationChunksWithBodyLimit(title, body, emojis, overflow, format, 32768)
}

func notificationChunksWithBodyLimit(title, body string, emojis bool, overflow, format string, bodyLimit int) ([]notificationChunk, error) {
	return notificationChunksWithLimits(title, body, emojis, overflow, format, 250, bodyLimit)
}

func notificationChunksWithLimits(title, body string, emojis bool, overflow, format string, titleLimit, bodyLimit int) ([]notificationChunk, error) {
	if emojis {
		var err error
		body, err = applyEmojis(body)
		if err != nil {
			return nil, err
		}
		title, err = applyEmojis(title)
		if err != nil {
			return nil, err
		}
	}
	title = strings.TrimFunc(title, sourceSpace)
	body = strings.TrimRightFunc(body, sourceSpace)
	return splitNotificationChunks(title, body, overflow, format, titleLimit, bodyLimit), nil
}

func splitNotificationChunks(title, body, overflow, format string, titleLimit, bodyLimit int) []notificationChunk {
	chunks := []string{body}
	if overflow != "upstream" {
		r := []rune(title)
		if len(r) > titleLimit {
			title = strings.TrimRightFunc(string(r[:titleLimit]), sourceSpace)
		}
		r = []rune(body)
		if len(r) > bodyLimit {
			if overflow == "split" {
				chunks = smartSplit(body, bodyLimit, format)
			} else {
				chunks = []string{strings.TrimRightFunc(strings.TrimLeft(string(r[:bodyLimit]), "\r\n\v\f"), sourceSpace)}
			}
		}
	}
	parts := make([]notificationChunk, 0, len(chunks))
	count := len(chunks)
	for i, chunk := range chunks {
		chunkTitle := title
		if overflow == "split" && len([]rune(body)) > bodyLimit {
			chunk = strings.TrimRightFunc(strings.TrimLeft(chunk, "\r\n\v\f"), sourceSpace)
			if title != "" && titleLimit > 130 {
				digits := len(strconv.Itoa(count))
				width := 4 + digits*2
				if width <= 12 {
					v := []rune(chunkTitle)
					if len(v) > titleLimit-width {
						chunkTitle = string(v[:titleLimit-width])
					}
					chunkTitle += " [" + zeroPad(i+1, digits) + "/" + zeroPad(count, digits) + "]"
				}
			}
		}
		parts = append(parts, notificationChunk{title: chunkTitle, body: chunk})
	}
	return parts
}
func zeroPad(n, width int) string { v := strconv.Itoa(n); return strings.Repeat("0", width-len(v)) + v }
func (s jsonService) request(title, body string, attachments []attachment) (Request, error) {
	payload := object{{"version", "1.0"}, {"title", title}, {"message", body}, {"attachments", attachmentPayload(attachments)}, {"type", "info"}}
	for _, extra := range s.extras {
		at := -1
		for i, p := range payload {
			if p.key == extra[0] {
				at = i
				break
			}
		}
		if at >= 0 {
			value := payload[at].value
			if extra[1] != "" {
				payload = payload.set(extra[1], value)
			}
			payload = payload.del(extra[0])
		} else {
			payload = payload.set(extra[0], extra[1])
		}
	}
	data, err := pythonJSON(payload)
	if err != nil {
		return Request{}, err
	}
	return Request{Method: s.method, URL: s.endpoint, Data: data, Headers: s.headers, Params: s.params, HeaderOrder: s.headerOrder, ParamOrder: s.paramOrder, Auth: s.auth, Verify: s.verify, Timeout: s.timeout}, nil
}
func pythonJSON(v any) ([]byte, error) {
	var b strings.Builder
	var write func(any) error
	write = func(v any) error {
		switch x := v.(type) {
		case string:
			b.WriteByte('"')
			for _, r := range x {
				switch r {
				case '"', '\\':
					b.WriteByte('\\')
					b.WriteRune(r)
				case '\b':
					b.WriteString("\\b")
				case '\f':
					b.WriteString("\\f")
				case '\n':
					b.WriteString("\\n")
				case '\r':
					b.WriteString("\\r")
				case '\t':
					b.WriteString("\\t")
				default:
					if r < 32 || r > 126 {
						if r > 0xffff {
							r -= 0x10000
							appendEscape(&b, 0xd800+(r>>10))
							appendEscape(&b, 0xdc00+(r&1023))
						} else {
							appendEscape(&b, r)
						}
					} else {
						b.WriteRune(r)
					}
				}
			}
			b.WriteByte('"')
		case object:
			b.WriteByte('{')
			for i, p := range x {
				if i > 0 {
					b.WriteString(", ")
				}
				if err := write(p.key); err != nil {
					return err
				}
				b.WriteString(": ")
				if err := write(p.value); err != nil {
					return err
				}
			}
			b.WriteByte('}')
		case []any:
			b.WriteByte('[')
			for i, p := range x {
				if i > 0 {
					b.WriteString(", ")
				}
				if err := write(p); err != nil {
					return err
				}
			}
			b.WriteByte(']')
		default:
			data, err := json.Marshal(v)
			if err != nil {
				return err
			}
			b.Write(data)
		}
		return nil
	}
	if err := write(v); err != nil {
		return nil, err
	}
	return []byte(b.String()), nil
}
func appendEscape(b *strings.Builder, r rune) {
	b.WriteString("\\u")
	s := strconv.FormatInt(int64(r), 16)
	b.WriteString(strings.Repeat("0", 4-len(s)))
	b.WriteString(s)
}
