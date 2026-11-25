package apprise

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	discordWebhookAPI = "https://discord.com/api/webhooks"
	discordIcon       = "https://github.com/caronc/apprise/raw/master/apprise/assets/themes/default/apprise-info-256x256.png"
	discordLogo       = "https://github.com/caronc/apprise/raw/master/apprise/assets/themes/default/apprise-logo.png"
	discordBodyLimit  = 2000
	discordTitleLimit = 250
	discordFieldLimit = 10
)

var (
	discordNativePattern = regexp.MustCompile(`(?i)^https?://discord(?:app)?\.com/api/webhooks/([0-9]+)/([A-Za-z0-9İıſK_-]+)/?(\?.+)?$`)
	discordMention       = regexp.MustCompile(`(?i)(?:<?@(&?)([0-9]+)>?|@([a-z0-9İıſK]+))`)
	discordFlagNumber    = regexp.MustCompile(`^[+-]?[0-9]+(?:_[0-9]+)*$`)
	discordHeadingBreak  = regexp.MustCompile(`[\r\n][\t\n\v\f\r \x{001c}-\x{001f}\x{0085}\p{Z}]*#`)
)

type discordService struct {
	endpoint, username, avatarURL, href, thread, overflow, format string
	flags                                                         json.Number
	ping                                                          []string
	tts, avatar, footer, footerLogo, thumbnail, fields            bool
	verify, emojis                                                bool
	timeout                                                       [2]float64

	rateMu        sync.Mutex
	rateRemaining float64
	rateReset     time.Time
}

func discordNativeURL(raw string) (string, bool) {
	match := discordNativePattern.FindStringSubmatch(raw)
	if match == nil {
		return "", false
	}
	return "discord://" + match[1] + "/" + match[2] + "/" + match[3], true
}

func parseDiscordService(raw string) (*discordService, bool) {
	u, valid := parseSourceURLWithHostVerification(raw, false)
	if !valid {
		return nil, false
	}
	segments := strings.FieldsFunc(u.path, func(r rune) bool {
		return strings.ContainsRune(" \t\r\n,\\/", r)
	})
	if len(segments) == 0 {
		return nil, false
	}
	id, token := u.host, sourceUnquote(segments[0])
	for _, value := range []string{id, token} {
		first, _ := utf8.DecodeRuneInString(value)
		if value == "" || sourceSpace(first) {
			return nil, false
		}
	}
	id, token = strings.TrimFunc(id, sourceSpace), strings.TrimFunc(token, sourceSpace)
	s := &discordService{
		endpoint: discordWebhookAPI + "/" + id + "/" + token,
		overflow: "upstream", format: "text", verify: true, avatar: true,
		fields: true, footerLogo: true, rateRemaining: 1, timeout: [2]float64{4, 4},
	}
	if u.user != nil {
		s.username = *u.user
	}
	if value, ok := u.query["botname"]; ok {
		s.username = sourceUnquote(value)
	}
	if value := sourceUnquote(u.query["flags"]); value != "" {
		value = strings.TrimFunc(value, sourceSpace)
		if !discordFlagNumber.MatchString(value) {
			return nil, false
		}
		number, valid := new(big.Int).SetString(strings.ReplaceAll(value, "_", ""), 10)
		if !valid || number.Sign() < 0 {
			return nil, false
		}
		if number.Sign() != 0 {
			s.flags = json.Number(number.String())
		}
	}
	s.avatarURL = sourceUnquote(u.query["avatar_url"])
	s.thread = sourceUnquote(u.query["thread"])
	s.ping = strings.FieldsFunc(sourceUnquote(u.query["ping"]), sourceListDelimiter)

	for key, value := range u.query {
		switch key {
		case "tts":
			s.tts = parseBool(value, false)
		case "avatar":
			s.avatar = parseBool(value, false)
		case "footer":
			s.footer = parseBool(value, false)
		case "footer_logo":
			s.footerLogo = parseBool(value, false)
		case "image":
			s.thumbnail = parseBool(value, false)
		case "fields":
			s.fields = parseBool(value, false)
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
	if href, ok := u.query["href"]; ok {
		s.href = sourceUnquote(href)
	} else if href, ok := u.query["url"]; ok {
		s.href = sourceUnquote(href)
		s.format = "markdown"
	}
	if _, ok := u.query["thread"]; ok {
		s.format = "markdown"
	}
	return s, true
}

func (s *discordService) sendTextInOrder() {}

func (s *discordService) successful(status int) bool {
	return status == http.StatusOK || status == http.StatusNoContent
}

func (s *discordService) waitToSend(ctx context.Context) error {
	s.rateMu.Lock()
	remaining, reset := s.rateRemaining, s.rateReset
	s.rateMu.Unlock()
	if now := time.Now(); remaining <= 0 && now.Before(reset) {
		timer := time.NewTimer(reset.Sub(now) + 10*time.Second)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
		}
	}
	return ctx.Err()
}

func (s *discordService) observeRateLimit(reply Response) {
	remaining, err := strconv.ParseFloat(strings.TrimFunc(attachmentHeader(reply.Headers, "X-RateLimit-Remaining"), sourceSpace), 64)
	if err != nil {
		return
	}
	s.rateMu.Lock()
	defer s.rateMu.Unlock()
	s.rateRemaining = remaining
	if reset, err := strconv.ParseInt(strings.TrimFunc(attachmentHeader(reply.Headers, "X-RateLimit-Reset"), sourceSpace), 10, 64); err == nil {
		s.rateReset = time.Unix(reset, 0)
	}
}

func (s *discordService) send(request Request, deliver func(Request) (Response, bool)) bool {
	reply, sent := deliver(request)
	s.observeRateLimit(reply)
	if !sent && reply.Status == http.StatusTooManyRequests {
		reply, sent = deliver(request)
		s.observeRateLimit(reply)
	}
	return sent
}

func (s *discordService) truncateAttachments() bool { return s.overflow == "truncate" }

func (s *discordService) attachmentChunks(title, body string) ([]notificationChunk, error) {
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
	const reserveCounter = 12
	titleRunes := []rune(title)
	titleMax := min(len(titleRunes)+reserveCounter, discordTitleLimit)
	if len(titleRunes) > titleMax {
		title = strings.TrimRightFunc(string(titleRunes[:titleMax]), sourceSpace)
	}
	bodyMax := discordBodyLimit
	if title != "" {
		bodyMax -= titleMax
	}
	bodyRunes := []rune(body)
	if len(bodyRunes) <= bodyMax {
		return []notificationChunk{{title: title, body: body}}, nil
	}
	trimChunk := func(value string) string {
		return strings.TrimRightFunc(strings.TrimLeft(value, "\r\n\v\f"), sourceSpace)
	}
	if s.overflow == "truncate" {
		return []notificationChunk{{title: title, body: trimChunk(string(bodyRunes[:bodyMax]))}}, nil
	}
	parts := smartSplit(body, bodyMax, s.format)
	digits := len(strconv.Itoa(len(parts)))
	counterWidth := 4 + digits*2
	showCounter := title != "" && titleMax > reserveCounter && counterWidth <= reserveCounter
	if showCounter && len([]rune(title)) > titleMax-counterWidth {
		title = string([]rune(title)[:titleMax-counterWidth])
	}
	chunks := make([]notificationChunk, 0, len(parts))
	for i, part := range parts {
		chunkTitle := title
		if showCounter {
			chunkTitle += " [" + zeroPad(i+1, digits) + "/" + zeroPad(len(parts), digits) + "]"
		}
		chunks = append(chunks, notificationChunk{title: chunkTitle, body: trimChunk(part)})
	}
	return chunks, nil
}

func (s *discordService) requests(title, body string) ([]Request, error) {
	chunks, err := s.attachmentChunks(title, body)
	if err != nil {
		return nil, err
	}
	var requests []Request
	for _, chunk := range chunks {
		calls, err := s.messageRequests(chunk.title, chunk.body)
		if err != nil {
			return nil, err
		}
		requests = append(requests, calls...)
	}
	return requests, nil
}

func (s *discordService) sendAttachments(title, body string, sources []*attachmentSource, resolve func(*attachmentSource) (attachment, error), deliver func(Request) (Response, bool)) (bool, error) {
	calls, err := s.messageRequests(title, body)
	if err != nil {
		return false, err
	}
	for _, call := range calls {
		if !s.send(call, deliver) {
			return false, nil
		}
	}
	for _, source := range sources {
		file, err := resolve(source)
		if err == errAttachmentUnavailable && source.name == "" {
			// A computed attachment name probes the file before Discord's
			// send checks accessibility. A failed probe is not cached.
			file, err = resolve(source)
		}
		if err != nil {
			return false, err
		}
		payload, contentType, err := discordMultipart(s.basePayload(false), file)
		if err != nil {
			return false, err
		}
		if !s.send(s.post(payload, contentType, true), deliver) {
			return false, nil
		}
	}
	return true, nil
}

func (s *discordService) basePayload(tts bool) object {
	payload := object{{"tts", tts}, {"wait", !tts}}
	if s.flags != "" {
		payload = append(payload, field{"flags", s.flags})
	}
	if s.avatar {
		avatar := s.avatarURL
		if avatar == "" {
			avatar = discordIcon
		}
		payload = append(payload, field{"avatar_url", avatar})
	}
	if s.username != "" {
		payload = append(payload, field{"username", s.username})
	}
	return payload
}

func (s *discordService) messageRequests(title, body string) ([]Request, error) {
	if body == "" {
		return nil, nil
	}
	payload := s.basePayload(s.tts)
	mentionText := strings.Join(s.ping, " ")
	if s.format == "markdown" {
		mentionText = body + " " + mentionText
	}
	mentions, mentionContent := discordPing(mentionText)
	if mentions != nil {
		payload = append(payload, field{"allow_mentions", mentions}, field{"content", mentionContent})
	}
	if s.format != "markdown" {
		if title != "" {
			body = title + "\r\n" + body
		}
		post, err := s.jsonPost(payload.set("content", body+mentionContent), true)
		return []Request{post}, err
	}

	description := body
	var sections []any
	if s.fields {
		description, sections = discordSections(body)
	}
	embed := object{
		{"author", object{{"name", "Apprise"}, {"url", "https://github.com/caronc/apprise"}}},
		{"title", title},
		{"description", description},
		{"color", 0x3aa3e3},
	}
	if s.href != "" {
		embed = append(embed, field{"url", s.href})
	}
	if s.footer {
		footer := object{{"text", "Apprise Notifications"}}
		if s.footerLogo {
			footer = append(footer, field{"icon_url", discordLogo})
		}
		embed = append(embed, field{"footer", footer})
	}
	if s.thumbnail {
		embed = append(embed, field{"thumbnail", object{{"url", discordIcon}, {"height", 256}, {"width", 256}}})
	}
	if len(sections) != 0 {
		embed = append(embed, field{"fields", sections[:min(discordFieldLimit, len(sections))]})
	}
	payload = append(payload, field{"embeds", []any{embed}})
	post, err := s.jsonPost(payload, true)
	if err != nil {
		return nil, err
	}
	requests := []Request{post}
	// The original subsequent embeds deliberately omit thread_id.
	for i := discordFieldLimit; i < len(sections); i += discordFieldLimit {
		embed.set("description", "").set("fields", sections[i:min(i+discordFieldLimit, len(sections))])
		post, err := s.jsonPost(payload, false)
		if err != nil {
			return nil, err
		}
		requests = append(requests, post)
	}
	return requests, nil
}

func (s *discordService) jsonPost(payload object, threaded bool) (Request, error) {
	data, err := pythonJSON(payload)
	return s.post(data, "application/json; charset=utf-8", threaded), err
}

func (s *discordService) post(data []byte, contentType string, threaded bool) Request {
	request := Request{
		Method: http.MethodPost, URL: s.endpoint, Data: data,
		Headers:     map[string]string{"User-Agent": "Apprise", "Content-Type": contentType},
		HeaderOrder: [][2]string{{"User-Agent", "Apprise"}, {"Content-Type", contentType}},
		Verify:      s.verify, Timeout: s.timeout,
	}
	if threaded && s.thread != "" {
		request.Params = map[string]string{"thread_id": s.thread}
		request.ParamOrder = [][2]string{{"thread_id", s.thread}}
	}
	return request
}

func discordPing(text string) (object, string) {
	parse, users, roles := []string{}, []string{}, []string{}
	for _, match := range discordMention.FindAllStringSubmatch(text, -1) {
		switch {
		case match[3] != "":
			parse = append(parse, match[3])
		case match[1] == "&":
			roles = append(roles, match[2])
		default:
			users = append(users, match[2])
		}
	}
	if len(parse)+len(users)+len(roles) == 0 {
		return nil, ""
	}
	// Source builds three Python sets. Use stable order within each set while
	// preserving the original parse, role, user grouping in the message.
	slices.Sort(parse)
	slices.Sort(users)
	slices.Sort(roles)
	parse, users, roles = slices.Compact(parse), slices.Compact(users), slices.Compact(roles)
	pings := make([]string, 0, len(parse)+len(roles)+len(users))
	for _, token := range parse {
		pings = append(pings, "@"+token)
	}
	for _, token := range roles {
		pings = append(pings, "<@&"+token+">")
	}
	for _, token := range users {
		pings = append(pings, "<@"+token+">")
	}
	return object{{"parse", parse}, {"users", users}, {"roles", roles}}, "👉 " + strings.Join(pings, " ")
}

func discordSections(markdown string) (string, []any) {
	remainder := strings.TrimLeftFunc(markdown, sourceSpace)
	var description string
	if remainder != "" && !strings.HasPrefix(remainder, "#") {
		_, first := utf8.DecodeRuneInString(remainder)
		end := discordSectionEnd(remainder, first)
		description = strings.TrimFunc(remainder[:end], sourceSpace)
		remainder = remainder[end:]
	}
	var fields []any
	for {
		hash := strings.IndexByte(remainder, '#')
		if hash < 0 {
			break
		}
		header, rest, _ := strings.Cut(remainder[hash+1:], "\n")
		remainder = strings.TrimLeftFunc(rest, sourceSpace)
		if header == "" {
			continue
		}
		name := strings.Trim(header, "#`* \r\n\t\v")
		content := "```\n```"
		if remainder != "" && !strings.HasPrefix(remainder, "#") {
			_, first := utf8.DecodeRuneInString(remainder)
			if first < len(remainder) {
				_, second := utf8.DecodeRuneInString(remainder[first:])
				end := discordSectionEnd(remainder, first+second)
				content = "```md\n" + strings.TrimFunc(remainder[:end], sourceSpace) + "\n```"
				remainder = remainder[end:]
			}
		}
		fields = append(fields, object{{"name", name}, {"value", content}})
	}
	return description, fields
}

func discordSectionEnd(text string, start int) int {
	if heading := discordHeadingBreak.FindStringIndex(text[start:]); heading != nil {
		return start + heading[0]
	}
	return max(start, len(strings.TrimRightFunc(text, sourceSpace)))
}

func discordMultipart(fields object, file attachment) ([]byte, string, error) {
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
			return nil, "", err
		}
	}
	name := strings.NewReplacer("\r", "%0D", "\n", "%0A", "\"", "%22").Replace(file.name)
	part, err := writer.CreatePart(textproto.MIMEHeader{
		"Content-Disposition": {fmt.Sprintf(`form-data; name="file"; filename="%s"`, name)},
	})
	if err != nil {
		return nil, "", err
	}
	if _, err := part.Write(file.data); err != nil {
		return nil, "", err
	}
	if err := writer.Close(); err != nil {
		return nil, "", err
	}
	return data.Bytes(), writer.FormDataContentType(), nil
}
