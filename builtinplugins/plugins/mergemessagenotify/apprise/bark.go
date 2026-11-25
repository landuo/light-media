package apprise

import (
	"context"
	"math/big"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	barkRequestInterval = 5500 * time.Millisecond
	barkDefaultIcon     = "https://github.com/caronc/apprise/raw/master/apprise/assets/themes/default/apprise-info-128x128.png"
)

var barkSounds = strings.Fields(`
alarm.caf anticipate.caf bell.caf birdsong.caf bloom.caf calypso.caf
chime.caf choo.caf descent.caf electronic.caf fanfare.caf glass.caf
gotosleep.caf healthnotification.caf horn.caf ladder.caf mailsent.caf
minuet.caf multiwayinvitation.caf newmail.caf newsflash.caf noir.caf
paymentsuccess.caf shake.caf sherwoodforest.caf silence.caf spell.caf
suspense.caf telegraph.caf tiptoes.caf typewriters.caf update.caf
`)

type barkService struct {
	endpoint, overflow, format string
	icon, sound, click, level  string
	category, group            string
	badge, volume              *big.Int
	targets                    []string
	auth                       []any
	verify, emojis, call       bool
	timeout                    [2]float64

	mu       sync.Mutex
	lastSend time.Time
}

func parseBarkService(raw string) (*barkService, bool) {
	u, valid := parseSourceURL(raw)
	if !valid {
		return nil, false
	}
	s := &barkService{overflow: "upstream", format: "text", verify: true, timeout: [2]float64{4, 4}}
	scheme := "http"
	if u.scheme == "barks" {
		scheme = "https"
	}
	port := u.port
	if port == ":0" {
		port = ""
	}
	s.endpoint = scheme + "://" + u.host + port + "/push"
	if u.user != nil && *u.user != "" {
		var password any
		if u.password != nil {
			password = *u.password
		}
		s.auth = []any{*u.user, password}
	}
	for _, segment := range strings.FieldsFunc(u.path, func(r rune) bool {
		return strings.ContainsRune(" \t\r\n,\\/", r)
	}) {
		s.targets = append(s.targets, strings.FieldsFunc(sourceUnquote(segment), sourceListDelimiter)...)
	}
	for _, target := range strings.FieldsFunc(u.query["to"], sourceListDelimiter) {
		s.targets = append(s.targets, strings.FieldsFunc(sourceUnquote(target), sourceListDelimiter)...)
	}
	slices.Sort(s.targets)
	s.targets = slices.Compact(s.targets)

	get := func(key string) string { return sourceUnquote(u.query[key]) }
	s.icon = get("icon")
	if s.icon == "" && parseBool(u.query["image"], true) {
		s.icon = barkDefaultIcon
	}
	if sound := strings.ToLower(get("sound")); sound != "" {
		for _, candidate := range barkSounds {
			if strings.HasPrefix(candidate, sound) {
				s.sound = candidate
				break
			}
		}
	}
	if level := get("level"); level != "" {
		for _, candidate := range []string{"active", "timeSensitive", "passive", "critical"} {
			if level[0] == candidate[0] {
				s.level = candidate
				break
			}
		}
	}
	s.click = get("click")
	s.category = get("category")
	s.group = get("group")
	if value, ok := new(big.Int).SetString(strings.TrimFunc(get("badge"), sourceSpace), 10); ok && value.Sign() > 0 {
		s.badge = value
	}
	// Source volume validation logs an invalid range but leaves the parsed
	// integer assigned. The payload omits only missing and zero values.
	if value, ok := new(big.Int).SetString(strings.TrimFunc(get("volume"), sourceSpace), 10); ok && value.Sign() != 0 {
		s.volume = value
	}
	s.call = parseBool(u.query["call"], false)
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
			switch strings.ToLower(value) {
			case "text", "html", "markdown":
				s.format = strings.ToLower(value)
			}
		case "overflow":
			switch strings.ToLower(value) {
			case "upstream", "truncate", "split":
				s.overflow = strings.ToLower(value)
			}
		case "emojis":
			s.emojis = parseBool(value, false)
		case "tz":
			if !validSourceTimezone(value) {
				return nil, false
			}
		}
	}
	return s, true
}

func (s *barkService) successful(status int) bool { return status == http.StatusOK }

func (s *barkService) waitToSend(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if delay := time.Until(s.lastSend.Add(barkRequestInterval)); delay > 0 {
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

func (s *barkService) requests(title, body string) ([]Request, error) {
	if len(s.targets) == 0 {
		return nil, nil
	}
	chunks, err := notificationChunks(title, body, s.emojis, s.overflow, s.format)
	if err != nil {
		return nil, err
	}
	headers := map[string]string{"User-Agent": "Apprise", "Content-Type": "application/json; charset=utf-8"}
	headerOrder := [][2]string{{"User-Agent", "Apprise"}, {"Content-Type", "application/json; charset=utf-8"}}
	calls := make([]Request, 0, len(chunks)*len(s.targets))
	for _, chunk := range chunks {
		payload := s.payload(chunk)
		// Source iterates the sorted target list via pop(), last first.
		for _, target := range slices.Backward(s.targets) {
			payload = payload.set("device_key", target)
			data, err := pythonJSON(payload)
			if err != nil {
				return nil, err
			}
			calls = append(calls, Request{
				Method: http.MethodPost, URL: s.endpoint, Data: data,
				Headers: headers, HeaderOrder: headerOrder,
				Auth: s.auth, Verify: s.verify, Timeout: s.timeout,
			})
		}
	}
	return calls, nil
}

func (s *barkService) payload(chunk notificationChunk) object {
	if chunk.title == "" {
		chunk.title = "Apprise Notifications"
	}
	bodyKey := "body"
	if s.format == "markdown" {
		bodyKey = "markdown"
	}
	out := object{{"title", chunk.title}, {bodyKey, chunk.body}}
	for _, pair := range [][2]string{{"icon", s.icon}, {"sound", s.sound}, {"url", s.click}} {
		if pair[1] != "" {
			out = append(out, field{pair[0], pair[1]})
		}
	}
	if s.badge != nil {
		out = append(out, field{"badge", s.badge})
	}
	for _, pair := range [][2]string{{"level", s.level}, {"category", s.category}, {"group", s.group}} {
		if pair[1] != "" {
			out = append(out, field{pair[0], pair[1]})
		}
	}
	if s.volume != nil {
		out = append(out, field{"volume", s.volume})
	}
	if s.call {
		out = append(out, field{"call", 1})
	}
	return out
}
