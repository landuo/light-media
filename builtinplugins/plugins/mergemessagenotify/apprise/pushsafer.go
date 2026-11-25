package apprise

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
)

type pushSaferOption struct {
	name  string
	value int
}

var pushSaferPriorities = []pushSaferOption{
	{"low", -2}, {"medium", -1}, {"normal", 0}, {"high", 1}, {"emergency", 2},
}

// The first matching prefix wins, so keep the original option order.
var pushSaferSounds = []pushSaferOption{
	{"silent", 0},
	{"ahem", 1},
	{"applause", 2},
	{"arrow", 3},
	{"baby", 4},
	{"bell", 5},
	{"bicycle", 6},
	{"bike", 6},
	{"boing", 7},
	{"buzzer", 8},
	{"camera", 9},
	{"carhorn", 10},
	{"horn", 10},
	{"cashregister", 11},
	{"chime", 12},
	{"creakydoor", 13},
	{"cuckooclock", 14},
	{"cuckoo", 14},
	{"disconnect", 15},
	{"dog", 16},
	{"doorbell", 17},
	{"fanfare", 18},
	{"gunshot", 19},
	{"honk", 20},
	{"jawharp", 21},
	{"morse", 22},
	{"electric", 23},
	{"radiotuner", 24},
	{"sirens", 25},
	{"militarytrumpets", 26},
	{"military", 26},
	{"trumpets", 26},
	{"ufo", 27},
	{"whahwhah", 28},
	{"whah", 42},
	{"goodye", 29},
	{"hello", 30},
	{"no", 31},
	{"okay", 32},
	{"ok", 32},
	{"ooohhhweee", 33},
	{"warn", 34},
	{"warning", 34},
	{"welcome", 35},
	{"yeah", 36},
	{"yes", 37},
	{"beep", 38},
	{"beep1", 38},
	{"weee", 39},
	{"wee", 39},
	{"cutinout", 40},
	{"flickglass", 41},
	{"laser", 43},
	{"windchime", 44},
	{"echo", 45},
	{"zipper", 46},
	{"hihat", 47},
	{"beep2", 48},
	{"beep3", 49},
	{"beep4", 50},
	{"alarmarmed", 51},
	{"armed", 51},
	{"alarmdisarmed", 52},
	{"disarmed", 52},
	{"backupready", 53},
	{"dooropen", 55},
	{"dopen", 55},
	{"doorclosed", 54},
	{"dclosed", 54},
	{"windowopen", 57},
	{"wopen", 57},
	{"windowclosed", 56},
	{"wclosed", 56},
	{"lighton", 58},
	{"lon", 58},
	{"lightoff", 59},
	{"loff", 59},
	{"doorbellrang", 60},
}

type pushSaferService struct {
	endpoint, key, overflow, format, sound, vibration string
	targets                                           []string
	timeout                                           [2]float64
	verify, emojis                                    bool
	throttle                                          providerThrottle
}

func parsePushSaferService(raw string) (*pushSaferService, bool) {
	u, valid := parseSourceURLWithHostVerification(raw, false)
	if !valid || u.host == "" || strings.TrimLeftFunc(u.host, sourceSpace) != u.host {
		return nil, false
	}
	scheme := "http"
	if u.scheme == "psafers" {
		scheme = "https"
	}
	s := &pushSaferService{
		endpoint: scheme + "://www.pushsafer.com/api",
		key:      strings.TrimRightFunc(u.host, sourceSpace), overflow: "upstream",
		format: "text", verify: true, timeout: [2]float64{4, 4},
	}

	// Path elements and 'to' values are split and decoded independently.
	// The constructor splits the decoded values again, then sorts and deduplicates.
	for _, path := range strings.FieldsFunc(u.path, func(r rune) bool {
		return strings.ContainsRune(" \t\r\n,\\/", r)
	}) {
		s.targets = append(s.targets, strings.FieldsFunc(sourceUnquote(path), sourceListDelimiter)...)
	}
	for _, target := range strings.FieldsFunc(u.query["to"], sourceListDelimiter) {
		s.targets = append(s.targets, strings.FieldsFunc(sourceUnquote(target), sourceListDelimiter)...)
	}
	slices.Sort(s.targets)
	s.targets = slices.Compact(s.targets)
	if len(s.targets) == 0 {
		s.targets = []string{"a"}
	}

	// Apprise validates priority at construction but never sends it in the form.
	if value := u.query["priority"]; value != "" {
		if _, ok := pushSaferChoice(sourceUnquote(value), -2, 2, pushSaferPriorities); !ok {
			return nil, false
		}
	}
	if value := u.query["sound"]; value != "" {
		sound, ok := pushSaferChoice(sourceUnquote(value), 0, 60, pushSaferSounds)
		if !ok {
			return nil, false
		}
		s.sound = strconv.Itoa(sound)
	}
	if value := u.query["vibration"]; value != "" {
		vibration, ok := pushSaferChoice(sourceUnquote(value), 0, 3, nil)
		if !ok {
			return nil, false
		}
		s.vibration = strconv.Itoa(vibration)
	}
	for key, value := range u.query {
		switch key {
		case "verify":
			s.verify = parseBool(value, false)
		case "cto", "rto":
			if timeout, err := strconv.ParseFloat(value, 64); err == nil {
				if key == "cto" {
					s.timeout[0] = timeout
				} else {
					s.timeout[1] = timeout
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
			// The original constructor attempts to assign its read-only
			// url_identifier property when storage is disabled.
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

func pushSaferChoice(raw string, minValue, maxValue int, options []pushSaferOption) (int, bool) {
	value := strings.TrimFunc(raw, sourceSpace)
	if n, ok := pushSaferInt(value); ok {
		return n, n >= minValue && n <= maxValue
	}
	if value != "" {
		value = strings.ToLower(value)
		for _, option := range options {
			if strings.HasPrefix(option.name, value) {
				return option.value, true
			}
		}
	}
	return 0, false
}

func pushSaferInt(value string) (int, bool) {
	if n, err := strconv.Atoi(value); err == nil {
		return n, true
	}
	// Python int() also accepts Unicode decimal digits and underscores between
	// digits. Normalize just those characters before applying the option range.
	var numeric strings.Builder
	previousDigit := false
	for index, r := range value {
		switch {
		case index == 0 && (r == '+' || r == '-'):
			numeric.WriteRune(r)
		case r == '_' && previousDigit:
			previousDigit = false
		case unicode.IsDigit(r):
			numeric.WriteByte(pushSaferASCIIDigit(r))
			previousDigit = true
		default:
			return 0, false
		}
	}
	if !previousDigit {
		return 0, false
	}
	n, err := strconv.Atoi(numeric.String())
	return n, err == nil
}

func pushSaferASCIIDigit(r rune) byte {
	if r <= 0xffff {
		for _, block := range unicode.Digit.R16 {
			if uint16(r) >= block.Lo && uint16(r) <= block.Hi && (uint16(r)-block.Lo)%block.Stride == 0 {
				return '0' + byte(((uint16(r)-block.Lo)/block.Stride)%10)
			}
		}
	}
	for _, block := range unicode.Digit.R32 {
		if uint32(r) >= block.Lo && uint32(r) <= block.Hi && (uint32(r)-block.Lo)%block.Stride == 0 {
			return '0' + byte(((uint32(r)-block.Lo)/block.Stride)%10)
		}
	}
	return '0' // Only called after unicode.IsDigit.
}

func (s *pushSaferService) waitToSend(ctx context.Context) error {
	return s.throttle.wait(ctx, 1200*time.Millisecond)
}

func (s *pushSaferService) successful(status int) bool {
	return status == http.StatusOK || status == http.StatusNoContent
}

func (s *pushSaferService) successfulResponse(response Response) bool {
	if !s.successful(response.Status) {
		return false
	}
	var payload map[string]json.RawMessage
	if json.Unmarshal(bytes.TrimPrefix(response.Content, []byte("\xef\xbb\xbf")), &payload) != nil {
		return false
	}
	// Python's True compares equal to the numeric status 1.
	status := string(payload["status"])
	number, err := strconv.ParseFloat(status, 64)
	return status == "true" || err == nil && number == 1
}

func (s *pushSaferService) truncateAttachments() bool { return s.overflow == "truncate" }

func (s *pushSaferService) attachmentChunks(title, body string) ([]notificationChunk, error) {
	return notificationChunks(title, body, s.emojis, s.overflow, s.format)
}

func (s *pushSaferService) requests(title, body string) ([]Request, error) {
	chunks, err := s.attachmentChunks(title, body)
	if err != nil {
		return nil, err
	}
	calls := make([]Request, 0, len(chunks)*len(s.targets))
	for _, chunk := range chunks {
		for _, target := range s.targets {
			payload := s.basePayload(chunk.title, chunk.body, target)
			calls = append(calls, s.request(append(payload, [2]string{"k", s.key})))
		}
	}
	return calls, nil
}

func (s *pushSaferService) sendAttachments(title, body string, sources []*attachmentSource, resolve func(*attachmentSource) (attachment, error), deliver func(Request) (Response, bool)) (bool, error) {
	images := make([]string, 0, len(sources))
	for _, source := range sources {
		file, err := resolve(source)
		if err != nil {
			return false, err
		}
		if strings.HasPrefix(file.mediaType, "image/") {
			images = append(images, "data:"+file.mediaType+";base64,"+base64.StdEncoding.EncodeToString(file.data))
		}
	}

	success := true
	pictureKeys := [3]string{"p", "p2", "p3"}
	for _, target := range s.targets {
		base := s.basePayload(title, body, target)
		if len(images) == 0 {
			_, sent := deliver(s.request(append(base, [2]string{"k", s.key})))
			success = sent && success
			continue
		}
		payload := slices.Clone(base)
		for batch := range slices.Chunk(images, len(pictureKeys)) {
			for i, picture := range batch {
				payload = orderedPairSet(payload, pictureKeys[i], picture)
			}
			payload = orderedPairSet(payload, "k", s.key)
			_, sent := deliver(s.request(payload))
			if !sent {
				// A failed batch leaves the original mutable payload intact.
				success = false
				continue
			}
			payload = slices.Clone(base)
			payload[0][1], payload[1][1] = "", "..."
		}
	}
	return success, nil
}

func (s *pushSaferService) basePayload(title, body, target string) [][2]string {
	payload := [][2]string{{"t", title}, {"m", body}, {"i", "25"}, {"c", "#3AA3E3"}, {"d", target}}
	if s.sound != "" {
		payload = append(payload, [2]string{"s", s.sound})
	}
	if s.vibration != "" {
		payload = append(payload, [2]string{"v", s.vibration})
	}
	return payload
}

func (s *pushSaferService) request(payload [][2]string) Request {
	var form strings.Builder
	for i, pair := range payload {
		if i != 0 {
			form.WriteByte('&')
		}
		form.WriteString(pair[0])
		form.WriteByte('=')
		form.WriteString(url.QueryEscape(pair[1]))
	}
	const contentType = "application/x-www-form-urlencoded"
	return Request{
		Method: http.MethodPost, URL: s.endpoint, Data: []byte(form.String()),
		Headers:     map[string]string{"User-Agent": "Apprise", "Content-Type": contentType},
		HeaderOrder: [][2]string{{"User-Agent", "Apprise"}, {"Content-Type", contentType}},
		Verify:      s.verify, Timeout: s.timeout,
	}
}
