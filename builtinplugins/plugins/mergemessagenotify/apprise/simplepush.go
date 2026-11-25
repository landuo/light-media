package apprise

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/url"
	"strings"
	"sync"
)

const simplePushEndpoint = "https://api.simplepush.io/send"

type simplePushService struct {
	providerHTTPOptions
	apiKey, event string
	cipherBlock   cipher.Block
	random        io.Reader
	ivOnce        sync.Once
	iv            [aes.BlockSize]byte
	ivError       error
}

func parseSimplePushService(raw string) (*simplePushService, bool) {
	u, valid := parseSourceURLWithHostVerification(raw, false)
	if !valid || u.host == "" || strings.TrimLeftFunc(u.host, sourceSpace) != u.host {
		return nil, false
	}
	event := sourceUnquote(u.query["event"])
	if strings.TrimLeftFunc(event, sourceSpace) != event {
		return nil, false
	}
	s := &simplePushService{
		apiKey: strings.TrimRightFunc(u.host, sourceSpace), event: strings.TrimRightFunc(event, sourceSpace),
		random: rand.Reader,
	}
	if !s.configure(u) {
		return nil, false
	}
	if u.user != nil && *u.user != "" && u.password != nil && *u.password != "" {
		// The SimplePush protocol uses the first 16 bytes of SHA-1(password+salt).
		digest := sha1.Sum([]byte(*u.password + *u.user))
		block, err := aes.NewCipher(digest[:aes.BlockSize])
		if err != nil {
			return nil, false
		}
		s.cipherBlock = block
	}
	return s, true
}

func (s *simplePushService) successfulResponse(reply Response) bool {
	var payload map[string]json.RawMessage
	if json.Unmarshal(bytes.TrimPrefix(reply.Content, []byte("\xef\xbb\xbf")), &payload) != nil {
		return false
	}
	var status string
	return json.Unmarshal(payload["status"], &status) == nil && status == "OK"
}

func (s *simplePushService) requests(title, body string) ([]Request, error) {
	chunks, err := notificationChunksWithLimits(title, body, s.emojis, s.overflow, s.format, 1024, 10000)
	if err != nil {
		return nil, err
	}
	calls := make([]Request, 0, len(chunks))
	for _, chunk := range chunks {
		data := "key=" + url.QueryEscape(s.apiKey)
		if s.cipherBlock != nil {
			s.ivOnce.Do(func() { _, s.ivError = io.ReadFull(s.random, s.iv[:]) })
			if s.ivError != nil {
				return nil, s.ivError
			}
			data += "&encrypted=true&iv=" + strings.ToUpper(hex.EncodeToString(s.iv[:]))
			chunk.body, chunk.title = s.encrypt(chunk.body), s.encrypt(chunk.title)
		}
		data += "&msg=" + url.QueryEscape(chunk.body) + "&title=" + url.QueryEscape(chunk.title)
		if s.event != "" {
			data += "&event=" + url.QueryEscape(s.event)
		}
		calls = append(calls, s.postForm(simplePushEndpoint, data))
	}
	return calls, nil
}

func (s *simplePushService) encrypt(text string) string {
	padding := aes.BlockSize - len(text)%aes.BlockSize
	content := make([]byte, len(text)+padding)
	copy(content, text)
	for i := len(text); i < len(content); i++ {
		content[i] = byte(padding)
	}
	cipher.NewCBCEncrypter(s.cipherBlock, s.iv[:]).CryptBlocks(content, content)
	return base64.URLEncoding.EncodeToString(content)
}
