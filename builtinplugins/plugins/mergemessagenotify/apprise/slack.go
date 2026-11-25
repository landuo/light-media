package apprise

import (
	"bytes"
	"context"
	"encoding/json"
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
	slackBot       = "bot"
	slackHook      = "hook"
	slackGovHook   = "gov-hook"
	slackAPI       = "https://slack.com/api/"
	slackHookURL   = "https://hooks.slack.com/services/"
	slackGovURL    = "https://hooks.slack-gov.com/services/"
	slackIcon      = "https://github.com/caronc/apprise/raw/master/apprise/assets/themes/default/apprise-info-72x72.png"
	slackJSON      = "application/json; charset=utf-8"
	slackBodyLimit = 35000
)

var (
	slackNative   = regexp.MustCompile(`(?i)^https?://hooks\.slack(-gov)?\.com/services/([a-z0-9]+)/([a-z0-9]+)/([a-z0-9]+)/?(\?.+)?$`)
	slackBotToken = regexp.MustCompile(`(?i)^(?:xoxe\.)?xox[abp]-[a-z0-9-]+$`)
	slackHookPart = regexp.MustCompile(`(?i)^[a-z0-9]+$`)
	slackChannel  = regexp.MustCompile(`(?i)^([+#@]?[a-z0-9_-]{1,32})(?::([0-9.]+))?$`)
	slackTag      = regexp.MustCompile(`(?i)(?:<|&lt;)?[ \t]*!([^| \n]+)(?:[ \t]*\|[ \t]*(?:([^\n]+?)[ \t]*)?(?:>|&gt;)|(?:>|&gt;))`)
	slackMention  = regexp.MustCompile(`(?i)(?:<|&lt;)?[ \t]*@([^| \n]+)(?:[ \t]*\|[ \t]*(?:([^\n]+?)[ \t]*)?(?:>|&gt;)|(?:>|&gt;))`)
	slackLink     = regexp.MustCompile(`(?i)(?:<|&lt;)?[ \t]*((?:https?|mailto)://[^| \n]+)(?:[ \t]*\|[ \t]*(?:([^\n]+?)[ \t]*)?(?:>|&gt;)|(?:>|&gt;))`)
	slackEscape   = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
)

type slackService struct {
	providerHTTPOptions
	mode, endpoint, token, username  string
	channels                         []string
	image, footer, timestamp, blocks bool
	emailIDs                         sync.Map
}

func slackNativeURL(raw string) (string, bool) {
	match := slackNative.FindStringSubmatch(raw)
	if match == nil {
		return "", false
	}
	params := match[5]
	if match[1] != "" {
		if params == "" {
			params = "?mode=gov-hook"
		} else {
			// The original replaces, rather than appends to, the query here.
			params = "&mode=gov-hook"
		}
	}
	return "slack://" + strings.Join(match[2:5], "/") + "/" + params, true
}

func parseSlackService(raw string) (*slackService, bool) {
	u, valid := parseSourceURLWithHostVerification(raw, false)
	if !valid {
		return nil, false
	}
	parts := strings.FieldsFunc(u.path, func(r rune) bool {
		return strings.ContainsRune(" \t\r\n,\\/", r)
	})
	for i := range parts {
		parts[i] = sourceUnquote(parts[i])
	}
	var credentials []string
	if strings.HasPrefix(u.host, "xo") {
		credentials = []string{u.host}
	} else {
		end := min(len(parts), 2)
		credentials = append([]string{u.host}, parts[:end]...)
		parts = parts[end:]
	}
	splitChannels := func(r rune) bool {
		return strings.ContainsRune(" \t\r\n,#\\/", r)
	}
	if override := u.query["token"]; override != "" {
		credentials = strings.FieldsFunc(sourceUnquote(override), splitChannels)
	}
	botToken := len(credentials) != 0 && strings.HasPrefix(credentials[0], "xo")
	mode := slackHook
	if botToken {
		mode = slackBot
	}
	if requested := u.query["mode"]; requested != "" {
		requested = sourceUnquote(requested)
		mode = ""
		for _, candidate := range []string{slackHook, slackGovHook, slackBot} {
			if strings.HasPrefix(candidate, requested) {
				mode = candidate
				break
			}
		}
	}
	s := &slackService{mode: mode, username: "Apprise"}
	if !s.configure(u) {
		return nil, false
	}
	switch strings.ToLower(u.query["format"]) {
	case "text", "html", "markdown":
	default:
		s.format = "markdown"
	}
	switch mode {
	case slackBot:
		if !botToken || !slackBotToken.MatchString(strings.TrimSuffix(credentials[0], "\n")) {
			return nil, false
		}
		s.token = strings.TrimFunc(credentials[0], sourceSpace)
		s.endpoint = slackAPI + "chat.postMessage"
	case slackHook, slackGovHook:
		if botToken || len(credentials) < 3 {
			return nil, false
		}
		for _, part := range credentials[:3] {
			if !slackHookPart.MatchString(strings.TrimSuffix(part, "\n")) {
				return nil, false
			}
		}
		s.endpoint = slackHookURL
		if mode == slackGovHook {
			s.endpoint = slackGovURL
		}
		s.endpoint += strings.Join(credentials[:3], "/")
	default:
		return nil, false
	}
	if u.user != nil && *u.user != "" {
		s.username = *u.user
	}
	s.image, s.footer, s.timestamp = true, true, true
	for key, setting := range map[string]*bool{"image": &s.image, "footer": &s.footer, "timestamp": &s.timestamp, "blocks": &s.blocks} {
		if value, present := u.query[key]; present {
			*setting = parseBool(value, false)
		}
	}
	parts = append(parts, strings.FieldsFunc(sourceUnquote(u.query["to"]), splitChannels)...)
	for _, part := range parts {
		s.channels = append(s.channels, strings.FieldsFunc(part, sourceListDelimiter)...)
	}
	slices.Sort(s.channels)
	s.channels = slices.Compact(s.channels)
	if len(s.channels) == 0 {
		channel := ""
		if mode == slackBot {
			channel = "#general"
		}
		s.channels = []string{channel}
	}
	return s, true
}

func (s *slackService) sendTextInOrder() {}

func (s *slackService) waitToSend(ctx context.Context) error {
	return s.throttle.wait(ctx, 1200*time.Millisecond)
}

func (s *slackService) truncateAttachments() bool { return s.overflow == "truncate" }

func (s *slackService) attachmentChunks(title, body string) ([]notificationChunk, error) {
	return notificationChunksWithBodyLimit(title, body, s.emojis, s.overflow, s.format, slackBodyLimit)
}

func (s *slackService) sendAttachments(title, body string, sources []*attachmentSource, resolve func(*attachmentSource) (attachment, error), deliver func(Request) (Response, bool)) (bool, error) {
	payload, valid := s.messagePayload(title, body)
	if !valid {
		return false, nil
	}
	allSent := true
	var acceptedChannels []any
	for _, channel := range s.channels {
		if match := pushBulletEmail.FindStringSubmatch(channel); match != nil {
			user := s.lookupEmail(match[1], deliver)
			if !slackTruthy(user) {
				allSent = false
				continue
			}
			// An email lookup leaves the current thread in the source payload.
			payload = payload.set("channel", user)
		} else if channel != "" {
			match := slackChannel.FindStringSubmatch(strings.TrimSuffix(channel, "\n"))
			if match == nil {
				allSent = false
				continue
			}
			if match[2] != "" {
				payload = payload.set("thread_ts", match[2])
			} else {
				payload = payload.del("thread_ts")
			}
			channel = match[1]
			switch channel[0] {
			case '+':
				channel = channel[1:]
			case '@', '#':
			default:
				channel = "#" + channel
			}
			payload = payload.set("channel", channel)
		}
		reply, sent, err := s.sendJSON(http.MethodPost, s.endpoint, payload, nil, deliver)
		if err != nil {
			return false, err
		}
		allSent = allSent && sent
		if sent && slackTruthy(reply["channel"]) {
			acceptedChannels = append(acceptedChannels, reply["channel"])
		}
	}
	if s.mode != slackBot || len(acceptedChannels) == 0 {
		return allSent, nil
	}
	for i, source := range sources {
		file, err := resolve(source)
		if err != nil {
			return false, err
		}
		sent, err := s.upload(file, i+1, acceptedChannels, deliver)
		if err != nil || !sent {
			return false, err
		}
	}
	return allSent, nil
}

func (s *slackService) messagePayload(title, body string) (object, bool) {
	if s.blocks {
		format := "plain_text"
		if s.format == "markdown" {
			format = "mrkdwn"
		}
		blocks := []any{}
		if title != "" {
			blocks = append(blocks, object{
				{"type", "header"}, {"text", object{{"type", "plain_text"}, {"text", title}, {"emoji", true}}},
			})
		}
		blocks = append(blocks, object{{"type", "section"}, {"text", object{{"type", format}, {"text", body}}}})
		if s.footer {
			var elements []any
			if s.image {
				elements = append(elements, object{{"type", "image"}, {"image_url", slackIcon}, {"alt_text", "info"}})
			}
			elements = append(elements, object{{"type", format}, {"text", "Apprise"}})
			blocks = append(blocks, object{{"type", "context"}, {"elements", elements}})
		}
		payload := object{{"username", s.username}, {"attachments", []any{object{{"blocks", blocks}, {"color", "#3AA3E3"}}}}}
		if s.footer && s.image {
			payload = append(payload, field{"icon_url", slackIcon})
		}
		return payload, true
	}
	// In the original regex map this exact newline match raises KeyError
	// because the pattern, rather than the matched text, is the dictionary key.
	if strings.Contains(title, "\r*\n") || s.format == "markdown" && strings.Contains(body, "\r*\n") {
		return nil, false
	}
	if s.format == "markdown" {
		body = slackEscape.Replace(body)
		body = slackRestoreMarkdown(body, slackTag, "!")
		body = slackRestoreMarkdown(body, slackMention, "@")
		body = slackRestoreMarkdown(body, slackLink, "")
	}
	item := object{{"title", slackEscape.Replace(title)}, {"text", body}, {"color", "#3AA3E3"}}
	if s.footer {
		if s.image {
			item = append(item, field{"footer_icon", slackIcon})
		}
		item = append(item, field{"footer", "Apprise"})
		if s.timestamp {
			item = append(item, field{"ts", float64(time.Now().UnixNano()) / 1e9})
		}
	}
	payload := object{{"username", s.username}, {"mrkdwn", s.format == "markdown"}, {"attachments", []any{item}}}
	if s.image {
		payload = append(payload, field{"icon_url", slackIcon})
	}
	return payload, true
}

func slackRestoreMarkdown(body string, pattern *regexp.Regexp, prefix string) string {
	for _, match := range pattern.FindAllStringSubmatch(body, -1) {
		target := match[1]
		if prefix == "" {
			target = strings.ReplaceAll(target, "&amp;", "&")
		} else {
			target = strings.TrimFunc(target, sourceSpace)
		}
		value := "<" + prefix + target
		if label := strings.TrimFunc(match[2], sourceSpace); label != "" {
			value += "|" + label
		}
		value += ">"
		// The source replaces all copies of the first case-insensitive match.
		find := regexp.MustCompile("(?i)" + regexp.QuoteMeta(match[0]))
		body = find.ReplaceAllStringFunc(body, func(string) string { return value })
	}
	return body
}

func (s *slackService) lookupEmail(email string, deliver func(Request) (Response, bool)) any {
	if id, known := s.emailIDs.Load(email); known {
		return id
	}
	if s.mode != slackBot {
		return nil
	}
	call := s.post(slackAPI+"users.lookupByEmail", "application/x-www-form-urlencoded", nil)
	call.Method = http.MethodGet
	token := "Bearer " + s.token
	call.Headers["Authorization"] = token
	call.HeaderOrder = append(call.HeaderOrder, [2]string{"Authorization", token})
	call.ParamOrder = [][2]string{{"email", email}}
	reply, okay := deliver(call)
	data := slackJSONReply(reply)
	if !okay || !slackTruthy(data["ok"]) {
		return nil
	}
	user, _ := data["user"].(map[string]any)
	id, valid := user["id"]
	if valid {
		s.emailIDs.Store(email, id)
	}
	return id
}

func (s *slackService) upload(file attachment, index int, channels []any, deliver func(Request) (Response, bool)) (bool, error) {
	name := file.name
	if name == "" {
		name = fmt.Sprintf("file%03d.dat", index)
	}
	params := [][2]string{{"filename", name}, {"length", strconv.Itoa(len(file.data))}}
	reply, sent, err := s.sendJSON(http.MethodGet, slackAPI+"files.getUploadURLExternal", object{}, params, deliver)
	if err != nil || !sent || !slackTruthy(reply["file_id"]) {
		return false, err
	}
	uploadURL, _ := reply["upload_url"].(string)
	if uploadURL == "" {
		return false, nil
	}
	data, contentType, err := pushBulletMultipart(file)
	if err != nil {
		return false, err
	}
	// The source attempts completion even if the upload response failed.
	_, _ = deliver(s.slackRequest(http.MethodPost, uploadURL, contentType, data, nil))
	for _, channel := range channels {
		payload := object{
			{"files", []any{object{{"id", reply["file_id"]}, {"title", file.name}}}},
			{"channel_id", channel},
		}
		completed, okay, err := s.sendJSON(http.MethodPost, slackAPI+"files.completeUploadExternal", payload, nil, deliver)
		if err != nil || !okay || !slackTruthy(completed["files"]) {
			return false, err
		}
	}
	return true, nil
}

func (s *slackService) sendJSON(method, endpoint string, payload object, params [][2]string, deliver func(Request) (Response, bool)) (map[string]any, bool, error) {
	data, err := pythonJSON(payload)
	if err != nil {
		return nil, false, err
	}
	reply, sent := deliver(s.slackRequest(method, endpoint, slackJSON, data, params))
	if !sent {
		return nil, false, nil
	}
	result := slackJSONReply(reply)
	if s.mode == slackBot {
		return result, slackTruthy(result["ok"]) || bytes.Contains(reply.Content, []byte("OK")), nil
	}
	return result, bytes.Equal(reply.Content, []byte("ok")), nil
}

func (s *slackService) slackRequest(method, endpoint, contentType string, data []byte, params [][2]string) Request {
	call := s.post(endpoint, contentType, data)
	call.Method = method
	call.ParamOrder = params
	call.Headers["Accept"] = "application/json"
	call.HeaderOrder = [][2]string{{"User-Agent", "Apprise"}, {"Accept", "application/json"}, {"Content-Type", contentType}}
	if s.mode == slackBot {
		token := "Bearer " + s.token
		call.Headers["Authorization"] = token
		call.HeaderOrder = append(call.HeaderOrder, [2]string{"Authorization", token})
	}
	return call
}

func slackJSONReply(reply Response) map[string]any {
	var data map[string]any
	_ = json.Unmarshal(reply.Content, &data)
	return data
}

func slackTruthy(value any) bool {
	switch v := value.(type) {
	case nil:
		return false
	case bool:
		return v
	case string:
		return v != ""
	case float64:
		return v != 0
	case []any:
		return len(v) != 0
	case map[string]any:
		return len(v) != 0
	default:
		return true
	}
}
