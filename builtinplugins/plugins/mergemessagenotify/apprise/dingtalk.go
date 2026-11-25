package apprise

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

const dingTalkEndpoint = "https://oapi.dingtalk.com/robot/send?access_token="

var dingTalkToken = regexp.MustCompile(`(?i)^[a-z0-9]+$`)

type dingTalkService struct {
	providerHTTPOptions
	token, secret string
	phones        []any
	clock         func() time.Time
}

func parseDingTalkService(raw string) (*dingTalkService, bool) {
	u, valid := parseSourceURLWithHostVerification(raw, false)
	token := queryToken(u)
	if !valid || !dingTalkToken.MatchString(strings.TrimSuffix(token, "\n")) {
		return nil, false
	}
	s := &dingTalkService{token: strings.TrimFunc(token, sourceSpace), phones: []any{}, clock: time.Now}
	if !s.configure(u) {
		return nil, false
	}
	if u.rawUser != nil {
		s.secret = *u.rawUser
	}
	if secret := u.query["secret"]; secret != "" {
		s.secret = sourceUnquote(secret)
	}
	if s.secret != "" {
		if !dingTalkToken.MatchString(strings.TrimSuffix(s.secret, "\n")) {
			return nil, false
		}
		s.secret = strings.TrimFunc(s.secret, sourceSpace)
	}
	var targets []string
	for _, part := range strings.FieldsFunc(u.path, func(r rune) bool {
		return strings.ContainsRune(" \t\r\n,\\/", r)
	}) {
		targets = append(targets, strings.FieldsFunc(sourceUnquote(part), sourceListDelimiter)...)
	}
	for _, part := range strings.FieldsFunc(u.query["to"], sourceListDelimiter) {
		targets = append(targets, strings.FieldsFunc(sourceUnquote(part), sourceListDelimiter)...)
	}
	slices.Sort(targets)
	for _, target := range slices.Compact(targets) {
		if strings.IndexFunc(target, func(r rune) bool {
			return !sourceSpace(r) && !(r >= '0' && r <= '9') && !strings.ContainsRune(")(+-", r)
		}) >= 0 {
			continue
		}
		phone := strings.Map(func(r rune) rune {
			if r >= '0' && r <= '9' {
				return r
			}
			return -1
		}, target)
		if len(phone) >= 11 && len(phone) <= 14 {
			s.phones = append(s.phones, phone)
		}
	}
	return s, true
}

func (s *dingTalkService) sendText(title, body string, deliver func(Request) (Response, bool)) (bool, error) {
	markdown := s.format == "markdown"
	var chunks []notificationChunk
	var err error
	if markdown {
		chunks, err = notificationChunks(title, body, s.emojis, s.overflow, s.format)
	} else {
		chunks, err = titlelessNotificationChunks(title, body, &s.providerHTTPOptions, 32768)
	}
	if err != nil {
		return false, err
	}
	success := true
	for _, chunk := range chunks {
		payload := object{{"msgtype", "text"}, {"at", object{{"atMobiles", s.phones}, {"isAtAll", false}}}}
		if markdown {
			payload = append(payload, field{"markdown", object{{"title", chunk.title}, {"text", chunk.body}}})
		} else {
			payload = append(payload, field{"text", object{{"content", chunk.body}}})
		}
		data, err := pythonJSON(payload)
		if err != nil {
			return false, err
		}
		call := s.post(dingTalkEndpoint+s.token, "application/json", data)
		if s.secret != "" {
			timestamp, signature := s.signature()
			call.ParamOrder = [][2]string{{"timestamp", timestamp}, {"sign", signature}}
			call.Params = map[string]string{"timestamp": timestamp, "sign": signature}
		}
		_, sent := deliver(call)
		success = sent && success
	}
	return success, nil
}

func (s *dingTalkService) signature() (timestamp, signature string) {
	now := s.clock()
	seconds := float64(now.Unix()) + float64(now.Nanosecond())/1e9
	timestamp = strconv.FormatInt(int64(math.RoundToEven(seconds*1000)), 10)
	digest := hmac.New(sha256.New, []byte(s.secret))
	_, _ = digest.Write([]byte(timestamp + "\n" + s.secret))
	// The original quotes the Base64 result before Requests encodes params.
	signature = sourceQuote(base64.StdEncoding.EncodeToString(digest.Sum(nil)), "")
	return timestamp, signature
}
