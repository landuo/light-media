package apprise

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const pushBulletAPI = "https://api.pushbullet.com/v2/"

// Apprise recognizes a labeled address before treating a recipient as a
// channel or device. The original regex intentionally accepts a prefix.
var pushBulletEmail = regexp.MustCompile(
	`(?i)^(?:[\s"']*[^:<'"]*[:<\s'"]+)?` +
		`((?:[^+]+\+)?[a-z0-9_!#$%&*/=?%\x60{|}~^-]+(?:\.[a-z0-9_!#$%&'*/=?%\x60{|}~^-]+)*@` +
		`(?:(?:[a-z0-9](?:[a-z0-9_-]*[a-z0-9])?\.)+[a-z0-9][a-z0-9_-]*[a-z0-9]|[a-z0-9][a-z0-9_-]{5,}))`)

type pushBulletService struct {
	token, overflow, format string
	targets                 []string
	verify, emojis          bool
	timeout                 [2]float64
	throttle                providerThrottle
}

func parsePushBulletService(raw string) (*pushBulletService, bool) {
	u, valid := parseSourceURLWithHostVerification(strings.ReplaceAll(raw, "/#", "/%23"), false)
	first, _ := utf8.DecodeRuneInString(u.host)
	if !valid || u.host == "" || sourceSpace(first) {
		return nil, false
	}
	s := &pushBulletService{
		token: strings.TrimFunc(u.host, sourceSpace), overflow: "upstream", format: "text",
		verify: true, timeout: [2]float64{4, 4},
	}
	for _, path := range strings.FieldsFunc(u.path, func(r rune) bool {
		return strings.ContainsRune(" \t\r\n,\\/", r)
	}) {
		s.targets = append(s.targets, strings.FieldsFunc(sourceUnquote(path), sourceListDelimiter)...)
	}
	for _, value := range strings.FieldsFunc(u.query["to"], sourceListDelimiter) {
		s.targets = append(s.targets, strings.FieldsFunc(sourceUnquote(value), sourceListDelimiter)...)
	}
	slices.Sort(s.targets)
	s.targets = slices.Compact(s.targets)
	if len(s.targets) == 0 {
		s.targets = []string{""}
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

func (s *pushBulletService) waitToSend(ctx context.Context) error {
	return s.throttle.wait(ctx, 1200*time.Millisecond)
}

func (s *pushBulletService) successful(status int) bool {
	return status == http.StatusOK || status == http.StatusNoContent
}

// Pushbullet accepts a nonempty input that trims to an empty note body.
func (s *pushBulletService) emptyNotificationSuccessful() bool { return true }

func (s *pushBulletService) requests(title, body string) ([]Request, error) {
	chunks, err := s.attachmentChunks(title, body)
	if err != nil {
		return nil, err
	}
	var calls []Request
	for _, chunk := range chunks {
		if chunk.body == "" {
			continue
		}
		for _, target := range s.targets {
			call, err := s.note(chunk.title, chunk.body, target)
			if err != nil {
				return nil, err
			}
			calls = append(calls, call)
		}
	}
	return calls, nil
}

func (s *pushBulletService) attachmentChunks(title, body string) ([]notificationChunk, error) {
	return notificationChunks(title, body, s.emojis, s.overflow, s.format)
}

func (s *pushBulletService) truncateAttachments() bool { return s.overflow == "truncate" }

func (s *pushBulletService) note(title, body, target string) (Request, error) {
	payload := object{{"type", "note"}, {"title", title}, {"body", body}}
	email := pushBulletEmail.FindStringSubmatch(target)
	switch {
	case target == "":
	case email != nil:
		payload = append(payload, field{"email", email[1]})
	case strings.HasPrefix(target, "#"):
		payload = append(payload, field{"channel_tag", target[1:]})
	default:
		payload = append(payload, field{"device_iden", target})
	}
	return s.jsonCall("pushes", payload)
}

func (s *pushBulletService) sendAttachments(title, body string, sources []*attachmentSource, resolve func(*attachmentSource) (attachment, error), deliver func(Request) (Response, bool)) (bool, error) {
	files := make([]Request, 0, len(sources))
	for i, source := range sources {
		file, err := resolve(source)
		if err != nil {
			return false, err
		}
		posted, sent, err := s.upload(file, i+1, deliver)
		if err != nil || !sent {
			return false, err
		}
		files = append(files, posted)
	}
	success := true
	for _, target := range s.targets {
		if body != "" {
			call, err := s.note(title, body, target)
			if err != nil {
				return false, err
			}
			if _, sent := deliver(call); !sent {
				success = false
				continue
			}
		}
		// The original sends the same file payload for each target and does
		// not include the recipient fields used in the preceding note.
		for _, call := range files {
			_, sent := deliver(call)
			success = sent && success
		}
	}
	return success, nil
}

func (s *pushBulletService) upload(file attachment, index int, deliver func(Request) (Response, bool)) (Request, bool, error) {
	name := file.name
	if name == "" {
		name = fmt.Sprintf("file%03d.dat", index)
	}
	call, err := s.jsonCall("upload-request", object{{"file_name", name}, {"file_type", file.mediaType}})
	if err != nil {
		return Request{}, false, err
	}
	reply, sent := deliver(call)
	if !sent {
		return Request{}, false, nil
	}
	var upload struct {
		FileName  json.RawMessage `json:"file_name"`
		FileType  *string         `json:"file_type"`
		FileURL   json.RawMessage `json:"file_url"`
		UploadURL *string         `json:"upload_url"`
	}
	if json.Unmarshal(reply.Content, &upload) != nil || upload.FileName == nil || upload.FileType == nil || upload.FileURL == nil || upload.UploadURL == nil {
		return Request{}, false, nil
	}
	var fileName, fileURL any
	for _, field := range []struct {
		raw    json.RawMessage
		target *any
	}{{upload.FileName, &fileName}, {upload.FileURL, &fileURL}} {
		decoder := json.NewDecoder(bytes.NewReader(field.raw))
		decoder.UseNumber()
		if err := decoder.Decode(field.target); err != nil {
			return Request{}, false, nil
		}
	}
	multipartBody, contentType, err := pushBulletMultipart(file)
	if err != nil {
		return Request{}, false, err
	}
	if _, sent := deliver(s.post(*upload.UploadURL, contentType, multipartBody)); !sent {
		return Request{}, false, nil
	}
	payload := object{{"type", "file"}, {"file_name", fileName}, {"file_type", *upload.FileType}, {"file_url", fileURL}}
	if strings.HasPrefix(*upload.FileType, "image/") {
		payload = append(payload, field{"image_url", fileURL})
	}
	posted, err := s.jsonCall("pushes", payload)
	return posted, err == nil, err
}

func (s *pushBulletService) jsonCall(endpoint string, payload object) (Request, error) {
	data, err := pythonJSON(payload)
	if err != nil {
		return Request{}, err
	}
	return s.post(pushBulletAPI+endpoint, "application/json", data), nil
}

func (s *pushBulletService) post(url, contentType string, data []byte) Request {
	return Request{
		Method: http.MethodPost, URL: url, Data: data,
		Headers:     map[string]string{"User-Agent": "Apprise", "Content-Type": contentType},
		HeaderOrder: [][2]string{{"User-Agent", "Apprise"}, {"Content-Type", contentType}},
		Auth:        []any{s.token, ""},
		Verify:      s.verify,
		Timeout:     s.timeout,
	}
}

func pushBulletMultipart(file attachment) ([]byte, string, error) {
	var data bytes.Buffer
	writer := multipart.NewWriter(&data)
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
