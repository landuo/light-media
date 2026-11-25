package apprise

import (
	"bytes"
	"context"
	"fmt"
	"math/big"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	tableast "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer"
	goldmarkhtml "github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/util"
)

const (
	pushoverEndpoint = "https://api.pushover.net/1/messages.json"
	pushoverMaxImage = 5 << 20
)

var (
	pushoverDevice  = regexp.MustCompile(`^[A-Za-z0-9İıſK_-]{1,25}$`)
	pushoverDecimal = regexp.MustCompile(`^[+-]?\p{Nd}(?:_?\p{Nd})*$`)
)

type pushoverService struct {
	userKey, token, device, sound string
	supplementalURL, urlTitle     string
	format, overflow              string
	retry, expire                 string
	priority                      int
	verify, emojis                bool
	timeout                       [2]float64
	throttle                      providerThrottle
}

func parsePushoverService(raw string) (*pushoverService, bool) {
	u, valid := parseSourceURLWithHostVerification(raw, false)
	if !valid || u.user == nil || *u.user == "" || u.host == "" ||
		strings.TrimLeftFunc(*u.user, sourceSpace) != *u.user ||
		strings.TrimLeftFunc(u.host, sourceSpace) != u.host {
		return nil, false
	}
	s := &pushoverService{
		userKey: strings.TrimRightFunc(*u.user, sourceSpace),
		token:   strings.TrimRightFunc(u.host, sourceSpace),
		sound:   "pushover", format: "text", overflow: "upstream",
		verify: true, timeout: [2]float64{4, 4},
	}

	// Source first splits path and query separately, then splits, sorts and
	// deduplicates decoded targets before validating device names.
	var targets []string
	for _, path := range strings.FieldsFunc(u.path, func(r rune) bool {
		return strings.ContainsRune(" \t\r\n,\\/", r)
	}) {
		targets = append(targets, strings.FieldsFunc(sourceUnquote(path), sourceListDelimiter)...)
	}
	for _, target := range strings.FieldsFunc(u.query["to"], sourceListDelimiter) {
		targets = append(targets, strings.FieldsFunc(sourceUnquote(target), sourceListDelimiter)...)
	}
	if len(targets) == 0 {
		s.device = "ALL_DEVICES"
	} else {
		slices.Sort(targets)
		validTargets := slices.DeleteFunc(slices.Compact(targets), func(target string) bool {
			return !pushoverDevice.MatchString(target)
		})
		s.device = strings.Join(validTargets, ",")
	}

	s.priority = pushoverPriority(sourceUnquote(u.query["priority"]))
	if sound := u.query["sound"]; sound != "" {
		s.sound = strings.ToLower(sourceUnquote(sound))
	}
	s.supplementalURL = sourceUnquote(u.query["url"])
	s.urlTitle = u.query["url_title"]
	if s.priority == 2 {
		retry := pushoverInteger(u.query["retry"], 900)
		expire := pushoverInteger(u.query["expire"], 3600)
		if retry.Cmp(big.NewInt(30)) < 0 || expire.Sign() < 0 || expire.Cmp(big.NewInt(10800)) > 0 {
			return nil, false
		}
		s.retry, s.expire = retry.String(), expire.String()
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
			// NotifyBase cannot assign the Pushover URL identity property.
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

func pushoverPriority(value string) int {
	value = strings.ToLower(value)
	switch {
	case strings.HasPrefix(value, "l"), strings.HasPrefix(value, "-2"):
		return -2
	case strings.HasPrefix(value, "m"), strings.HasPrefix(value, "-1"):
		return -1
	case strings.HasPrefix(value, "h"), strings.HasPrefix(value, "1"):
		return 1
	case strings.HasPrefix(value, "e"), strings.HasPrefix(value, "2"):
		return 2
	default:
		return 0
	}
}

func pushoverInteger(value string, fallback int64) *big.Int {
	if !pushoverDecimal.MatchString(value) {
		return big.NewInt(fallback)
	}
	var digits strings.Builder
	count := 0
	for _, r := range value {
		switch r {
		case '+', '-':
			digits.WriteRune(r)
		case '_':
		default:
			count++
			// The source Python runtime rejects decimal conversions over
			// its default 4,300-digit limit.
			if count > 4300 {
				return big.NewInt(fallback)
			}
			digits.WriteByte(pushSaferASCIIDigit(r))
		}
	}
	n, _ := new(big.Int).SetString(digits.String(), 10)
	return n
}

func (s *pushoverService) waitToSend(ctx context.Context) error {
	return s.throttle.wait(ctx, 5500*time.Millisecond)
}

// Apprise reports delivery solely from HTTP 200, including emergency replies.
// It neither interprets the returned receipt nor polls for acknowledgement.
func (s *pushoverService) successful(status int) bool { return status == http.StatusOK }

func (s *pushoverService) truncateAttachments() bool { return s.overflow == "truncate" }

func (s *pushoverService) attachmentChunks(title, body string) ([]notificationChunk, error) {
	return notificationChunksWithBodyLimit(title, body, s.emojis, s.overflow, s.format, 1024)
}

func (s *pushoverService) requests(title, body string) ([]Request, error) {
	if s.device == "" {
		return nil, nil
	}
	chunks, err := s.attachmentChunks(title, body)
	if err != nil {
		return nil, err
	}
	calls := make([]Request, 0, len(chunks))
	for _, chunk := range chunks {
		payload, err := s.payload(chunk.title, chunk.body)
		if err != nil {
			return nil, err
		}
		call, err := s.request(payload, nil)
		if err != nil {
			return nil, err
		}
		calls = append(calls, call)
	}
	return calls, nil
}

func (s *pushoverService) sendAttachments(title, body string, sources []*attachmentSource, resolve func(*attachmentSource) (attachment, error), deliver func(Request) (Response, bool)) (bool, error) {
	if s.device == "" {
		return false, nil
	}
	payload, err := s.payload(title, body)
	if err != nil {
		return false, err
	}
	for i, source := range sources {
		file, err := resolve(source)
		if err != nil {
			return false, err
		}
		if i != 0 || body == "" {
			payload = orderedPairSet(payload, "message", file.name)
		}
		var image *attachment
		if strings.HasPrefix(strings.ToLower(file.mediaType), "image/") {
			if len(file.data) == 0 || len(file.data) > pushoverMaxImage {
				return false, nil
			}
			image = &file
		}
		call, err := s.request(payload, image)
		if err != nil {
			return false, err
		}
		if _, sent := deliver(call); !sent {
			return false, nil
		}
		payload = orderedPairSet(payload, "title", "")
		payload = orderedPairSet(payload, "sound", "none")
	}
	return true, nil
}

func (s *pushoverService) payload(title, body string) ([][2]string, error) {
	if title == "" {
		title = "Apprise Notifications"
	}
	if s.format == "markdown" {
		var rendered bytes.Buffer
		if err := pushoverMarkdown.Convert([]byte(body), &rendered); err != nil {
			return nil, err
		}
		body = strings.TrimSpace(rendered.String())
	}
	payload := [][2]string{
		{"token", s.token}, {"user", s.userKey}, {"priority", strconv.Itoa(s.priority)},
		{"title", title}, {"message", body}, {"device", s.device}, {"sound", s.sound},
	}
	if s.supplementalURL != "" {
		payload = append(payload, [2]string{"url", s.supplementalURL})
	}
	if s.urlTitle != "" {
		payload = append(payload, [2]string{"url_title", s.urlTitle})
	}
	if s.format == "html" || s.format == "markdown" {
		payload = append(payload, [2]string{"html", "1"})
	}
	if s.priority == 2 {
		payload = append(payload, [2]string{"retry", s.retry}, [2]string{"expire", s.expire})
	}
	return payload, nil
}

func (s *pushoverService) request(payload [][2]string, image *attachment) (Request, error) {
	var contentType string
	var data []byte
	if image == nil {
		contentType = "application/x-www-form-urlencoded"
		var form strings.Builder
		for i, field := range payload {
			if i > 0 {
				form.WriteByte('&')
			}
			form.WriteString(field[0])
			form.WriteByte('=')
			form.WriteString(url.QueryEscape(field[1]))
		}
		data = []byte(form.String())
	} else {
		var err error
		data, contentType, err = pushoverMultipart(payload, *image)
		if err != nil {
			return Request{}, err
		}
	}
	return Request{
		Method: http.MethodPost, URL: pushoverEndpoint, Data: data,
		Headers:     map[string]string{"User-Agent": "Apprise", "Content-Type": contentType},
		HeaderOrder: [][2]string{{"User-Agent", "Apprise"}, {"Content-Type", contentType}},
		Auth:        []any{s.token, ""},
		Verify:      s.verify,
		Timeout:     s.timeout,
	}, nil
}

func pushoverMultipart(payload [][2]string, image attachment) ([]byte, string, error) {
	var data bytes.Buffer
	writer := multipart.NewWriter(&data)
	for _, field := range payload {
		if err := writer.WriteField(field[0], field[1]); err != nil {
			return nil, "", err
		}
	}
	name := strings.NewReplacer("\r", "%0D", "\n", "%0A", "\"", "%22").Replace(image.name)
	part, err := writer.CreatePart(textproto.MIMEHeader{
		"Content-Disposition": {fmt.Sprintf(`form-data; name="attachment"; filename="%s"`, name)},
	})
	if err != nil {
		return nil, "", err
	}
	if _, err := part.Write(image.data); err != nil {
		return nil, "", err
	}
	if err := writer.Close(); err != nil {
		return nil, "", err
	}
	return data.Bytes(), writer.FormDataContentType(), nil
}

var pushoverMarkdown = goldmark.New(
	goldmark.WithParser(parser.NewParser(
		// Apprise enables only Python-Markdown's nl2br and tables extensions.
		// Backtick fences are therefore inline code spans, not fenced blocks.
		parser.WithBlockParsers(slices.DeleteFunc(parser.DefaultBlockParsers(), func(p util.PrioritizedValue) bool {
			return p.Priority == 700 // Default fenced-code block parser.
		})...),
		parser.WithInlineParsers(parser.DefaultInlineParsers()...),
		parser.WithParagraphTransformers(parser.DefaultParagraphTransformers()...),
	)),
	goldmark.WithExtensions(extension.Table),
	goldmark.WithRendererOptions(
		goldmarkhtml.WithHardWraps(), goldmarkhtml.WithUnsafe(), goldmarkhtml.WithXHTML(),
		renderer.WithNodeRenderers(util.Prioritized(pushoverMarkdownRenderer{}, 0)),
	),
)

type pushoverMarkdownRenderer struct{}

func (pushoverMarkdownRenderer) RegisterFuncs(r renderer.NodeRendererFuncRegisterer) {
	r.Register(ast.KindCodeSpan, renderPushoverCodeSpan)
	r.Register(tableast.KindTableCell, renderPushoverTableCell)
}

func renderPushoverCodeSpan(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		_, err := w.WriteString("</code>")
		return ast.WalkContinue, err
	}
	var code strings.Builder
	for child := node.FirstChild(); child != nil; child = child.NextSibling() {
		segment := child.(*ast.Text).Segment
		code.Write(segment.Value(source))
	}
	escaped := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(strings.TrimFunc(code.String(), sourceSpace))
	_, err := w.WriteString("<code>" + escaped)
	return ast.WalkSkipChildren, err
}

func renderPushoverTableCell(w util.BufWriter, _ []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	tag := "td"
	if node.Parent().Kind() == tableast.KindTableHeader {
		tag = "th"
	}
	if !entering {
		_, err := w.WriteString("</" + tag + ">\n")
		return ast.WalkContinue, err
	}
	align := node.(*tableast.TableCell).Alignment
	start := "<" + tag
	if align != tableast.AlignNone {
		start += ` style="text-align: ` + align.String() + `;"`
	}
	_, err := w.WriteString(start + ">")
	return ast.WalkContinue, err
}
