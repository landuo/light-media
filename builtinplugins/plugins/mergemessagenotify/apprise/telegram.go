package apprise

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"math/big"
	"mime/multipart"
	"net/textproto"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	telegramBotAPI       = "https://api.telegram.org/bot"
	telegramBodyLimit    = 4096
	telegramCaptionLimit = 1024
	telegramIconName     = "apprise-info-256x256.png"
)

//go:embed telegram-info.png
var telegramInfoImage []byte

var (
	telegramTokenURL = regexp.MustCompile(`(?i)^tgram://(?:bot)?((?:[a-z0-9_-]+)(?::[a-z0-9_-]+)?@)?([0-9]+)(?::|%3a)+(.*)$`)
	telegramToken    = regexp.MustCompile(`(?i)^[a-z0-9_-]+$`)
	telegramTargetID = regexp.MustCompile(`(?i)^(?:(-?[0-9]{1,32})|(?:@|%40)?([a-z_-][a-z0-9_-]+))(?:(?::|%3a)([0-9]+))?$`)
	telegramInteger  = regexp.MustCompile(`^[+-]?\p{Nd}(?:_?\p{Nd})*$`)
)

type telegramTarget struct {
	id    any // Numeric chat IDs remain JSON numbers. Detected owner IDs are strings.
	topic json.Number
}

type telegramService struct {
	providerHTTPOptions
	token, markdown, content string
	topic                    json.Number
	silent, preview, image   bool
	detect                   bool
	ownerMu                  sync.Mutex
	targets                  []telegramTarget
}

func parseTelegramService(raw string) (*telegramService, bool) {
	match := telegramTokenURL.FindStringSubmatch(raw)
	if match == nil {
		return nil, false
	}
	u, valid := parseSourceURLWithHostVerification("tgram://"+match[1]+match[2]+"/"+match[3], false)
	if !valid {
		return nil, false
	}
	path := strings.FieldsFunc(u.path, func(r rune) bool {
		return strings.ContainsRune(" \t\r\n,\\/", r)
	})
	if len(path) == 0 {
		return nil, false
	}
	token := sourceUnquote(path[0])
	if !telegramToken.MatchString(strings.TrimSuffix(token, "\n")) {
		return nil, false
	}
	s := &telegramService{token: match[2] + ":" + strings.TrimFunc(token, sourceSpace), content: "before", markdown: "MARKDOWN"}
	if !s.configure(u) {
		return nil, false
	}
	if rawFormat := strings.ToLower(u.query["format"]); rawFormat != "text" && rawFormat != "markdown" {
		// NotifyTelegram's provider default is HTML; ordinary Apprise defaults
		// to text for providers that do not declare a default.
		s.format = "html"
	}
	if mdv := strings.ToLower(u.query["mdv"]); mdv != "" {
		if !strings.HasPrefix(mdv, "v1") && !strings.HasPrefix(mdv, "1") {
			if strings.HasPrefix(mdv, "v2") || strings.HasPrefix(mdv, "2") || strings.HasPrefix(mdv, "default") {
				s.markdown = "MarkdownV2"
			}
		}
	}
	if placement := strings.ToLower(u.query["content"]); placement != "" {
		if placement != "before" && placement != "after" {
			return nil, false
		}
		s.content = placement
	}
	topic := u.query["topic"]
	if topic == "" {
		topic = u.query["thread"]
	}
	if topic != "" {
		s.topic, valid = telegramNumber(topic)
		if !valid {
			return nil, false
		}
	}
	s.silent = parseBool(u.query["silent"], false)
	s.preview = parseBool(u.query["preview"], false)
	s.image = parseBool(u.query["image"], false)

	var rawTargets []string
	for _, raw := range path[1:] {
		rawTargets = append(rawTargets, strings.FieldsFunc(sourceUnquote(raw), sourceListDelimiter)...)
	}
	for _, raw := range strings.FieldsFunc(u.query["to"], sourceListDelimiter) {
		rawTargets = append(rawTargets, strings.FieldsFunc(sourceUnquote(raw), sourceListDelimiter)...)
	}
	s.detect = parseBool(u.query["detect"], len(rawTargets) == 0)
	slices.Sort(rawTargets)
	for _, raw := range slices.Compact(rawTargets) {
		target := telegramTargetID.FindStringSubmatch(raw)
		if target == nil {
			s.detect = false
			continue
		}
		item := telegramTarget{topic: s.topic}
		if target[3] != "" {
			item.topic, valid = telegramNumber(target[3])
			if !valid {
				return nil, false
			}
		}
		if target[2] != "" {
			item.id = "@" + target[2]
		} else {
			item.id, _ = telegramNumber(target[1])
		}
		s.targets = append(s.targets, item)
	}
	return s, true
}

// Python int accepts Unicode decimal digits and underscores, and both chat
// IDs and topic IDs must retain integers that do not fit in int64.
func telegramNumber(raw string) (json.Number, bool) {
	if !telegramInteger.MatchString(raw) {
		return "", false
	}
	var value strings.Builder
	for _, r := range raw {
		switch r {
		case '+', '-':
			value.WriteRune(r)
		case '_':
		default:
			value.WriteByte(pushSaferASCIIDigit(r))
		}
	}
	// Python 3's default decimal conversion guard is 4300 digits.
	digits := strings.TrimLeft(value.String(), "+-")
	if len(digits) > 4300 {
		return "", false
	}
	n, valid := new(big.Int).SetString(value.String(), 10)
	if !valid {
		return "", false
	}
	return json.Number(n.String()), true
}

func (s *telegramService) sendTextInOrder() {}

func (s *telegramService) waitForRequest(ctx context.Context, request Request) error {
	if strings.HasSuffix(request.URL, "/getUpdates") {
		return ctx.Err()
	}
	return s.throttle.wait(ctx, time.Millisecond)
}

func (s *telegramService) truncateAttachments() bool { return s.overflow == "truncate" }

func (s *telegramService) attachmentChunks(title, body string) ([]notificationChunk, error) {
	if s.emojis {
		var err error
		title, err = applyEmojis(title)
		if err != nil {
			return nil, err
		}
		body, err = applyEmojis(body)
		if err != nil {
			return nil, err
		}
	}
	title, body = strings.TrimFunc(title, sourceSpace), strings.TrimRightFunc(body, sourceSpace)
	if title != "" {
		if s.format == "html" {
			body = "<b>" + title + "</b><br />\r\n" + body
		} else {
			body = title + "\r\n" + body
		}
	}
	if s.overflow == "upstream" || utf8.RuneCountInString(body) <= telegramBodyLimit {
		return []notificationChunk{{body: body}}, nil
	}
	trimChunk := func(value string) string {
		return strings.TrimRightFunc(strings.TrimLeft(value, "\r\n\v\f"), sourceSpace)
	}
	if s.overflow == "truncate" {
		return []notificationChunk{{body: trimChunk(string([]rune(body)[:telegramBodyLimit]))}}, nil
	}
	var chunks []notificationChunk
	for _, part := range smartSplit(body, telegramBodyLimit, s.format) {
		chunks = append(chunks, notificationChunk{body: trimChunk(part)})
	}
	return chunks, nil
}

func (s *telegramService) recipients(deliver func(Request) (Response, bool)) []telegramTarget {
	s.ownerMu.Lock()
	defer s.ownerMu.Unlock()
	if len(s.targets) == 0 && s.detect {
		reply, sent := deliver(s.post(telegramBotAPI+s.token+"/getUpdates", "application/json", nil))
		if !sent {
			return nil
		}
		if id := telegramBotOwner(reply.Content); id != "" {
			s.targets = append(s.targets, telegramTarget{id: id, topic: s.topic})
		}
	}
	return s.targets
}

func telegramBotOwner(data []byte) string {
	var result struct {
		OK     bool
		Result []struct {
			Message *struct {
				From *struct {
					ID json.RawMessage
				}
			}
		}
	}
	if json.Unmarshal(data, &result) != nil || !result.OK {
		return ""
	}
	for _, entry := range result.Result {
		if entry.Message == nil || entry.Message.From == nil {
			continue
		}
		id := entry.Message.From.ID
		var text string
		if json.Unmarshal(id, &text) == nil {
			return text
		}
		if numeric, valid := telegramNumber(string(id)); valid && numeric != "0" {
			return string(numeric)
		}
		// The first matching entry terminates the source lookup even at zero.
		return ""
	}
	return ""
}

func (s *telegramService) sendAttachments(_ string, body string, sources []*attachmentSource, resolve func(*attachmentSource) (attachment, error), deliver func(Request) (Response, bool)) (bool, error) {
	targets := s.recipients(deliver)
	if len(targets) == 0 {
		return false, nil
	}
	mode := s.markdown
	if s.format != "markdown" {
		mode = "HTML"
		for _, entry := range telegramHTMLEntries {
			body = entry.pattern.ReplaceAllString(body, entry.replacement)
		}
	}
	var caption object
	if len(sources) != 0 && body != "" && utf8.RuneCountInString(body) < telegramCaptionLimit {
		caption = object{{"caption", body}, {"show_caption_above_media", s.content == "before"}, {"parse_mode", mode}}
	}
	attachmentsFirst := s.content == "after" || body == "" || len(caption) != 0
	success := true
	for _, target := range targets {
		if s.image {
			icon := attachment{name: telegramIconName, data: telegramInfoImage}
			call, err := s.mediaCall("SendPhoto", "photo", telegramTargetPayload(nil, target), icon)
			if err != nil {
				return false, err
			}
			// The source sends text even if the optional icon upload fails.
			deliver(call)
		}
		if len(sources) != 0 && attachmentsFirst {
			var sent bool
			var err error
			caption, sent, err = s.mediaAttachments(target, caption, sources, resolve, deliver)
			if err != nil {
				return false, err
			}
			if !sent {
				success = false
				continue
			}
			if body == "" || len(caption) != 0 {
				continue
			}
		}
		payload := telegramTargetPayload(object{
			{"disable_notification", s.silent}, {"disable_web_page_preview", !s.preview},
			{"parse_mode", mode}, {"text", body},
		}, target)
		data, err := pythonJSON(payload)
		if err != nil {
			return false, err
		}
		if _, sent := deliver(s.post(telegramBotAPI+s.token+"/sendMessage", "application/json", data)); !sent {
			success = false
			continue
		}
		if len(sources) != 0 && !attachmentsFirst {
			_, sent, err := s.mediaAttachments(target, nil, sources, resolve, deliver)
			if err != nil {
				return false, err
			}
			success = sent && success
		}
	}
	return success, nil
}

func telegramTargetPayload(payload object, target telegramTarget) object {
	payload = payload.set("chat_id", target.id)
	if target.topic != "" && target.topic != "0" {
		payload = payload.set("message_thread_id", target.topic)
	}
	return payload
}

func (s *telegramService) mediaAttachments(target telegramTarget, caption object, sources []*attachmentSource, resolve func(*attachmentSource) (attachment, error), deliver func(Request) (Response, bool)) (object, bool, error) {
	for i, source := range sources {
		file, err := resolve(source)
		if err == errAttachmentUnavailable && source.name == "" {
			// _send_attachments asks for the computed name first. A failed
			// probe is not cached, and send_media checks availability again.
			file, err = resolve(source)
		}
		if err != nil {
			if err == errAttachmentUnavailable {
				return caption, false, nil
			}
			return caption, false, err
		}
		name := file.name
		if name == "" {
			name = fmt.Sprintf("file%03d.dat", i+1)
		}
		var payload object
		if i == 0 && len(caption) != 0 {
			// Python reuses the first attachment's caption dictionary across
			// targets, including a prior message_thread_id if the next lacks it.
			caption = telegramTargetPayload(caption.set("title", name), target)
			payload = caption
		} else {
			payload = telegramTargetPayload(object{{"title", name}}, target)
		}
		method, key := telegramMediaType(file.mediaType)
		call, err := s.mediaCall(method, key, payload, file)
		if err != nil {
			return caption, false, err
		}
		if _, sent := deliver(call); !sent {
			return caption, false, nil
		}
	}
	return caption, true, nil
}

func telegramMediaType(mediaType string) (method, field string) {
	mime := strings.ToLower(mediaType)
	switch {
	case strings.HasPrefix(mime, "image/gif"), strings.HasPrefix(mime, "video/h264"):
		return "sendAnimation", "animation"
	case strings.HasPrefix(mime, "image/"):
		return "sendPhoto", "photo"
	case strings.HasPrefix(mime, "video/mp4"):
		return "sendVideo", "video"
	case strings.HasPrefix(mime, "application/ogg"), strings.HasPrefix(mime, "audio/ogg"):
		return "sendVoice", "voice"
	case strings.HasPrefix(mime, "audio/mpeg"), strings.HasPrefix(mime, "audio/mp4a-latm"):
		return "sendAudio", "audio"
	default:
		return "sendDocument", "document"
	}
}

func (s *telegramService) mediaCall(method, key string, fields object, file attachment) (Request, error) {
	var data bytes.Buffer
	writer := multipart.NewWriter(&data)
	for _, entry := range fields {
		value := fmt.Sprint(entry.value)
		if flag, ok := entry.value.(bool); ok {
			if flag {
				value = "True"
			} else {
				value = "False"
			}
		}
		if err := writer.WriteField(entry.key, value); err != nil {
			return Request{}, err
		}
	}
	name := strings.NewReplacer("\r", "%0D", "\n", "%0A", "\"", "%22").Replace(file.name)
	part, err := writer.CreatePart(textproto.MIMEHeader{
		"Content-Disposition": {fmt.Sprintf(`form-data; name="%s"; filename="%s"`, key, name)},
	})
	if err != nil {
		return Request{}, err
	}
	if _, err := part.Write(file.data); err != nil {
		return Request{}, err
	}
	if err := writer.Close(); err != nil {
		return Request{}, err
	}
	return s.post(telegramBotAPI+s.token+"/"+method, writer.FormDataContentType(), data.Bytes()), nil
}

// NotifyTelegram applies these substitutions in order. Ordinary Merge
// notifications leave Apprise's body_format unset, so heading markup adds
// neither of the HTML/Markdown-specific line separators.
var telegramHTMLEntries = func() []struct {
	pattern     *regexp.Regexp
	replacement string
} {
	const ws = `[\t\n\v\f\r \x{001c}-\x{001f}\x{0085}\p{Z}]`
	entries := [][2]string{
		{`\s*<!.+?-->\s*`, ""},
		{`\s*<\s*(!?DOCTYPE|p|div|span|body|script|link|meta|html|font|head|label|form|input|textarea|select|iframe|source)([^a-z0-9>][^>]*)?>\s*`, ""},
		{`\s*<\s*/(span|body|script|meta|html|font|head|label|form|input|textarea|select|ol|ul|link|iframe|source)([^a-z0-9>][^>]*)?>\s*`, ""},
		{`<\s*strong([^a-z0-9>][^>]*)?>`, "<b>"},
		{`<\s*/\s*strong([^a-z0-9>][^>]*)?>`, "</b>"},
		{`\s*<\s*(h[1-6]|title)([^a-z0-9>][^>]*)?>\s*`, "<b>"},
		{`\s*<\s*/\s*(h[1-6]|title)([^a-z0-9>][^>]*)?>\s*`, "</b>"},
		{`<\s*(caption|em)([^a-z0-9>][^>]*)?>`, "<i>"},
		{`<\s*/\s*(caption|em)([^a-z0-9>][^>]*)?>`, "</i>"},
		{`<\s*li([^a-z0-9>][^>]*)?>\s*`, " -"},
		{`\s*<\s*/?\s*(ol|ul|br|hr)\s*/?>\s*`, "\r\n"},
		{`\s*<\s*/\s*(br|p|hr|li|div)([^a-z0-9>][^>]*)?>\s*`, "\r\n"},
		{`&nbsp;?`, " "},
		{`&emsp;?`, "   "},
		{`&apos;?`, "'"},
		{`&quot;?`, "\""},
		{`\r*\n[\r\n]+`, "\r\n"},
	}
	out := make([]struct {
		pattern     *regexp.Regexp
		replacement string
	}, 0, len(entries))
	for _, entry := range entries {
		out = append(out, struct {
			pattern     *regexp.Regexp
			replacement string
		}{regexp.MustCompile("(?is)" + strings.ReplaceAll(entry[0], `\s`, ws)), entry[1]})
	}
	return out
}()

var _ orderedAttachmentService = (*telegramService)(nil)
