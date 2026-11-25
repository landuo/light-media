package apprise

import (
	"context"
	"crypto/rand"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	ntfyCloudURL        = "https://ntfy.sh"
	ntfyDefaultIcon     = "https://github.com/caronc/apprise/raw/master/apprise/assets/themes/default/apprise-info-256x256.png"
	ntfyTitleLimit      = 200
	ntfyBodyLimit       = 7800
	ntfyJSONLimit       = 8000
	ntfyCounterWidth    = 12
	ntfyRequestInterval = 5500 * time.Millisecond
)

var ntfyTopic = regexp.MustCompile(`^[A-Za-z0-9_İıſK-]{1,64}$`)
var ntfyNative = regexp.MustCompile(`(?i)^(?:http|ntfy)s?://ntfy\.sh(/[^?]+)?(\?.+)?$`)
var ntfyToken = regexp.MustCompile(`(?i)^tk_[^ \t]+`)

type ntfyService struct {
	endpoint, token, overflow, format string
	icon, priority, delay, click      string
	email, tags, actions              string
	remoteAttachment, remoteFilename  string
	topics                            []string
	auth                              []any
	verify, emojis, missingToken      bool
	timeout                           [2]float64

	mu       sync.Mutex
	lastSend time.Time
}

func ntfyNativeURL(raw string) (string, bool) {
	match := ntfyNative.FindStringSubmatch(strings.ReplaceAll(raw, "/#", "/%23"))
	if match == nil {
		return "", false
	}
	query := match[2]
	if query == "" {
		query = "?"
	} else {
		query += "&"
	}
	return "ntfys://" + match[1] + query + "mode=cloud", true
}

func parseNtfyService(raw string) (*ntfyService, bool) {
	u, valid := parseSourceURLWithHostVerification(strings.ReplaceAll(raw, "/#", "/%23"), false)
	if !valid {
		return nil, false
	}
	s := &ntfyService{overflow: "upstream", format: "text", verify: true, timeout: [2]float64{4, 4}}
	get := func(key string) string { return sourceUnquote(u.query[key]) }

	var topics []string
	for _, segment := range strings.FieldsFunc(u.path, func(r rune) bool {
		return strings.ContainsRune(" \t\r\n,\\/", r)
	}) {
		topics = append(topics, sourceUnquote(segment))
	}
	for _, entry := range strings.FieldsFunc(u.query["to"], sourceListDelimiter) {
		topics = append(topics, sourceUnquote(entry))
	}
	_, validHost := sourceHostname(u.rawHost)
	mode := sourceUnquote(strings.ToLower(strings.TrimFunc(u.query["mode"], sourceSpace)))
	if u.query["mode"] == "" {
		mode = "cloud"
		if validHost && len(topics) > 0 {
			mode = "private"
		}
	}
	// URL parsing uses the decoded value before the constructor normalizes it.
	if mode == "cloud" && !strings.Contains(strings.ToLower(u.rawHost), "ntfy.sh") {
		topics = append(topics, u.rawHost)
	}
	if mode == "private" && !validHost {
		return nil, false
	}
	mode = strings.ToLower(strings.TrimFunc(mode, sourceSpace))
	switch mode {
	case "cloud":
		s.endpoint = ntfyCloudURL
	case "private":
		scheme := "http"
		if u.scheme == "ntfys" {
			scheme = "https"
		}
		port := u.port
		if port == ":0" {
			port = ""
		}
		s.endpoint = scheme + "://" + u.host + port
	default:
		return nil, false
	}
	for _, entry := range topics {
		for _, topic := range strings.FieldsFunc(entry, sourceListDelimiter) {
			if ntfyTopic.MatchString(topic) {
				s.topics = append(s.topics, topic)
			}
		}
	}
	slices.Sort(s.topics)
	s.topics = slices.Compact(s.topics)

	if !s.setAuth(u, mode) {
		return nil, false
	}
	if image, specified := u.query["image"]; !specified || parseBool(image, false) {
		s.icon = get("avatar_url")
		if s.icon == "" {
			s.icon = ntfyDefaultIcon
		}
	}
	s.priority = ntfyPriority(get("priority"))
	s.delay = get("delay")
	s.click = get("click")
	if strings.IndexFunc(s.click, func(r rune) bool { return r > 127 }) != -1 {
		s.click = sourceQuote(s.click, ":/?&=[]")
	}
	s.click = sourceQuote(s.click, ":/?@&=#")
	s.email = get("email")
	tags := strings.FieldsFunc(get("tags"), sourceListDelimiter)
	slices.Sort(tags)
	s.tags = strings.Join(slices.Compact(tags), ",")
	s.actions = get("actions")
	s.remoteAttachment = get("attach")
	if s.remoteAttachment != "" {
		filename := get("filename")
		s.remoteFilename = filename[strings.LastIndexByte(filename, '/')+1:]
	}

	for key, value := range u.query {
		switch key {
		case "verify":
			s.verify = parseBool(value, false)
		case "cto", "rto":
			if seconds, err := strconv.ParseFloat(value, 64); err == nil {
				if key == "cto" {
					s.timeout[0] = seconds
				} else {
					s.timeout[1] = seconds
				}
			}
		case "format":
			switch value = strings.ToLower(value); value {
			case "text", "html", "markdown":
				s.format = value
			}
		case "overflow":
			switch value = strings.ToLower(value); value {
			case "upstream", "truncate", "split":
				s.overflow = value
			}
		case "emojis":
			s.emojis = parseBool(value, false)
		case "store":
			if !parseBool(value, false) {
				return nil, false
			}
		case "tz":
			if !validSourceTimezone(value) {
				return nil, false
			}
		}
	}
	return s, true
}

func (s *ntfyService) setAuth(u sourceURL, mode string) bool {
	var rawUser, rawPassword string
	if u.rawUser != nil {
		rawUser = *u.rawUser
	}
	if u.rawPassword != nil {
		rawPassword = *u.rawPassword
	}
	token := sourceUnquote(u.query["token"])
	auth := "basic"
	switch {
	case u.query["auth"] != "":
		auth = sourceUnquote(strings.ToLower(strings.TrimFunc(u.query["auth"], sourceSpace)))
	case token != "", rawPassword == "" && ntfyToken.MatchString(rawUser):
		auth = "token"
	}
	if auth == "token" && token == "" {
		if rawPassword != "" {
			token = sourceUnquote(rawPassword)
		} else {
			token = sourceUnquote(rawUser)
		}
	}
	switch strings.ToLower(strings.TrimFunc(auth, sourceSpace)) {
	case "basic":
		if mode == "private" && u.user != nil && *u.user != "" {
			var password any
			if u.password != nil {
				password = *u.password
			}
			s.auth = []any{*u.user, password}
		}
	case "token":
		if mode == "private" {
			s.token = token
			s.missingToken = token == ""
		}
	default:
		return false
	}
	return true
}

func ntfyPriority(value string) string {
	value = strings.ToLower(value)
	for _, pair := range [][2]string{{"l", "low"}, {"mo", "low"}, {"n", ""}, {"h", "high"}, {"e", "max"}, {"mi", "min"}, {"ma", "max"}, {"d", ""}, {"1", "min"}, {"2", "low"}, {"3", ""}, {"4", "high"}, {"5", "max"}} {
		if strings.HasPrefix(value, pair[0]) {
			return pair[1]
		}
	}
	return ""
}

func (s *ntfyService) successful(status int) bool { return status == http.StatusOK }

func (s *ntfyService) waitToSend(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if delay := time.Until(s.lastSend.Add(ntfyRequestInterval)); delay > 0 {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.lastSend = time.Now()
	return nil
}

func (s *ntfyService) truncateAttachments() bool { return s.overflow == "truncate" }

func (s *ntfyService) attachmentChunks(title, body string) ([]notificationChunk, error) {
	if s.emojis {
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
	if s.overflow == "upstream" {
		return []notificationChunk{{title: title, body: body}}, nil
	}
	titleRunes := []rune(title)
	titleMax := min(len(titleRunes)+ntfyCounterWidth, ntfyTitleLimit)
	if len(titleRunes) > titleMax {
		title = strings.TrimRightFunc(string(titleRunes[:titleMax]), sourceSpace)
	}
	bodyMax := ntfyBodyLimit
	if title != "" {
		bodyMax -= titleMax
	}
	bodyRunes := []rune(body)
	if len(bodyRunes) <= bodyMax {
		return []notificationChunk{{title: title, body: body}}, nil
	}
	trimBody := func(value string) string {
		return strings.TrimRightFunc(strings.TrimLeft(value, "\r\n\v\f"), sourceSpace)
	}
	if s.overflow == "truncate" {
		return []notificationChunk{{title: title, body: trimBody(string(bodyRunes[:bodyMax]))}}, nil
	}
	parts := smartSplit(body, bodyMax, s.format)
	digits := len(strconv.Itoa(len(parts)))
	counterWidth := 4 + digits*2
	showCounter := title != "" && titleMax > ntfyCounterWidth && counterWidth <= ntfyCounterWidth
	if showCounter && len([]rune(title)) > titleMax-counterWidth {
		title = string([]rune(title)[:titleMax-counterWidth])
	}
	chunks := make([]notificationChunk, 0, len(parts))
	for i, part := range parts {
		chunkTitle := title
		if showCounter {
			chunkTitle += " [" + zeroPad(i+1, digits) + "/" + zeroPad(len(parts), digits) + "]"
		}
		chunks = append(chunks, notificationChunk{title: chunkTitle, body: trimBody(part)})
	}
	return chunks, nil
}

func (s *ntfyService) requests(title, body string) ([]Request, error) {
	chunks, err := s.attachmentChunks(title, body)
	if err != nil || s.missingToken {
		return nil, err
	}
	calls := make([]Request, 0, len(chunks)*len(s.topics))
	for _, chunk := range chunks {
		for _, topic := range slices.Backward(s.topics) {
			call, err := s.topicRequest(topic, chunk.title, chunk.body, nil)
			if err != nil {
				return nil, err
			}
			calls = append(calls, call)
		}
	}
	return calls, nil
}

func (s *ntfyService) sendAttachments(title, body string, sources []*attachmentSource, resolve func(*attachmentSource) (attachment, error), deliver func(Request) (Response, bool)) (bool, error) {
	if len(s.topics) == 0 {
		return false, nil
	}
	for _, topic := range slices.Backward(s.topics) {
		for i, source := range sources {
			file, err := resolve(source)
			if err != nil || s.missingToken {
				return false, err
			}
			fileTitle, fileBody := "", ""
			if i == 0 {
				fileTitle, fileBody = title, body
			}
			call, err := s.topicRequest(topic, fileTitle, fileBody, &file)
			if err != nil {
				return false, err
			}
			if _, sent := deliver(call); !sent {
				return false, nil
			}
		}
	}
	return true, nil
}

func (s *ntfyService) topicRequest(topic, title, body string, file *attachment) (Request, error) {
	call := Request{
		Method: http.MethodPost, URL: s.endpoint, Auth: s.auth, Verify: s.verify, Timeout: s.timeout,
		Headers: make(map[string]string),
	}
	addHeader := func(key, value string) {
		if value != "" {
			call.Headers[key] = value
			call.HeaderOrder = append(call.HeaderOrder, [2]string{key, value})
		}
	}
	addHeader("User-Agent", "Apprise")
	if s.token != "" {
		addHeader("Authorization", "Bearer "+s.token)
	}
	fields := object{}
	if file == nil {
		addHeader("Content-Type", "application/json")
		fields = append(fields, field{"topic", topic})
		if s.remoteAttachment != "" {
			fields = append(fields, field{"attach", s.remoteAttachment})
			if s.remoteFilename != "" {
				fields = append(fields, field{"filename", s.remoteFilename})
			}
		}
	} else {
		call.URL += "/" + sourceQuote(topic, "")
		call.Data = file.data
		fields = append(fields, field{"filename", file.name})
	}
	addHeader("X-Icon", s.icon)
	if title != "" {
		fields = append(fields, field{"title", title})
	}
	if body != "" {
		fields = append(fields, field{"message", body})
	}
	if s.format == "markdown" {
		addHeader("X-Markdown", "yes")
	}
	for _, pair := range [][2]string{{"X-Priority", s.priority}, {"X-Delay", s.delay}, {"X-Click", s.click}, {"X-Email", s.email}, {"X-Tags", s.tags}, {"X-Actions", s.actions}} {
		addHeader(pair[0], pair[1])
	}
	if file != nil {
		call.Params = make(map[string]string, len(fields))
		for _, field := range fields {
			value := field.value.(string)
			call.Params[field.key] = value
			call.ParamOrder = append(call.ParamOrder, [2]string{field.key, value})
		}
		return call, nil
	}
	data, err := pythonJSON(fields)
	if err != nil {
		return Request{}, err
	}
	if len(data) > ntfyJSONLimit {
		if title != "" {
			body = title + "\n" + body
		}
		return s.topicRequest(topic, "", "", &attachment{name: ntfyMemoryFilename(), data: []byte(body)})
	}
	call.Data = data
	return call, nil
}

func ntfyMemoryFilename() string {
	var id [16]byte
	_, _ = rand.Read(id[:])
	id[6] = id[6]&0x0f | 0x40
	id[8] = id[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x.txt", id[:4], id[4:6], id[6:8], id[8:10], id[10:])
}
