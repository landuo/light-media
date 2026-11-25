package apprise

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/parser"
	goldmarkhtml "github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/util"
)

const (
	matrixIcon         = "https://github.com/caronc/apprise/raw/master/apprise/assets/themes/default/apprise-info-32x32.png"
	matrixWebhookPath  = "/api/v1/matrix/hook/"
	matrixSendInterval = 500 * time.Millisecond
)

var (
	matrixNative   = regexp.MustCompile(`(?i)^https?://webhooks\.t2bot\.io/api/v[0-9]+/matrix/hook/([a-z0-9_-]+)/?(\?.+)?$`)
	matrixT2Token  = regexp.MustCompile(`(?i)^[a-z0-9]{64}$`)
	matrixAlias    = regexp.MustCompile(`(?i)^(?:#|%23)?([a-z0-9._=-]+)(?:(?::|%3a)([a-z0-9.-]+))?$`)
	matrixRoomID   = regexp.MustCompile(`(?i)^(?:!|&#33;|%21)([a-z0-9._=-]+)(?:(?::|%3a)([a-z0-9.-]+))?$`)
	matrixEscape   = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", "'", "&apos;", `"`, "&quot;")
	matrixMarkdown = goldmark.New(
		goldmark.WithParser(parser.NewParser(
			parser.WithBlockParsers(slices.DeleteFunc(parser.DefaultBlockParsers(), func(p util.PrioritizedValue) bool {
				return p.Priority == 700 // Python-Markdown's default has no fenced-code extension.
			})...),
			parser.WithInlineParsers(parser.DefaultInlineParsers()...),
			parser.WithParagraphTransformers(parser.DefaultParagraphTransformers()...),
		)),
		goldmark.WithRendererOptions(goldmarkhtml.WithUnsafe(), goldmarkhtml.WithXHTML()),
	)
)

type matrixService struct {
	providerHTTPOptions
	mu                                           sync.Mutex
	mode, version, messageType                   string
	server, user, password                       string
	hasUser, image, discovery, requireHomeserver bool
	rooms                                        []string
	joined                                       map[string]string
	access, home, delegated, transactionUUID     string
	transaction                                  int
	discoveryExpiry, identityExpiry              time.Time
	retryDelay                                   time.Duration
	retryPending, retryTerminal                  bool
}

func matrixNativeURL(raw string) (string, bool) {
	match := matrixNative.FindStringSubmatch(raw)
	if match == nil {
		return "", false
	}
	query := match[2]
	if query == "" {
		query = "?"
	} else {
		query += "&"
	}
	return "matrixs://" + match[1] + "/" + query + "mode=t2bot", true
}

func parseMatrixService(raw string) (*matrixService, bool) {
	u, valid := parseSourceURLWithHostVerification(raw, false)
	if !valid || u.host == "" {
		return nil, false
	}
	s := &matrixService{
		mode: "off", version: "3", messageType: "text",
		image:             parseBool(u.query["image"], false),
		discovery:         parseBool(u.query["discovery"], true),
		requireHomeserver: parseBool(u.query["hsreq"], true),
		joined:            make(map[string]string),
	}
	if !s.configure(u) {
		return nil, false
	}
	if u.user != nil {
		s.user, s.hasUser = *u.user, true
	}
	if u.password != nil {
		s.password = *u.password
	}
	for _, value := range strings.FieldsFunc(u.path, func(r rune) bool { return strings.ContainsRune(" \t\r\n,\\/", r) }) {
		s.rooms = append(s.rooms, strings.FieldsFunc(sourceUnquote(value), sourceListDelimiter)...)
	}
	for _, value := range strings.FieldsFunc(u.query["to"], sourceListDelimiter) {
		s.rooms = append(s.rooms, strings.FieldsFunc(sourceUnquote(value), sourceListDelimiter)...)
	}
	slices.Sort(s.rooms)
	s.rooms = slices.Compact(s.rooms)
	if mode, specified := u.query["mode"]; specified {
		s.mode = strings.ToLower(mode)
	} else if s.password == "" && len(s.rooms) == 0 {
		s.mode = "t2bot"
	}
	switch s.mode {
	case "off", "matrix", "slack", "t2bot", "":
	default:
		return nil, false
	}
	if s.mode == "t2bot" {
		s.password = u.host
	}
	if token := u.query["token"]; token != "" {
		s.password = sourceUnquote(token)
	} else if s.password == "" && s.user != "" {
		s.password, s.user, s.hasUser = s.user, "", false
	}
	if v := u.query["version"]; v != "" {
		s.version = sourceUnquote(v)
	} else if v, ok := u.query["v"]; ok {
		s.version = sourceUnquote(v)
	}
	if s.version != "2" && s.version != "3" {
		return nil, false
	}
	if mt := u.query["msgtype"]; mt != "" {
		s.messageType = strings.ToLower(sourceUnquote(mt))
	}
	if s.messageType != "text" && s.messageType != "notice" {
		return nil, false
	}
	if s.mode == "t2bot" {
		if !matrixT2Token.MatchString(strings.TrimSuffix(s.password, "\n")) {
			return nil, false
		}
		s.access = strings.TrimFunc(s.password, sourceSpace)
		return s, true
	}
	host, ok := sourceHostname(u.host)
	if !ok {
		return nil, false
	}
	if u.port != "" {
		port, err := strconv.Atoi(strings.TrimPrefix(u.port, ":"))
		if err != nil || port < 1 || port > 65535 {
			return nil, false
		}
	}
	scheme := "http"
	if u.scheme == "matrixs" {
		scheme = "https"
	}
	s.server = scheme + "://" + host + u.port
	s.discovery = s.discovery && s.mode == "off" && scheme == "https"
	return s, true
}

func (s *matrixService) sendTextInOrder()          {}
func (s *matrixService) truncateAttachments() bool { return s.overflow == "truncate" }
func (s *matrixService) attachmentChunks(title, body string) ([]notificationChunk, error) {
	return notificationChunksWithBodyLimit(title, body, s.emojis, s.overflow, s.format, 65000)
}
func (s *matrixService) waitForRequest(ctx context.Context, _ Request) error {
	if s.retryPending {
		terminal := s.retryTerminal
		if err := s.waitForRetry(ctx); err != nil {
			return err
		}
		if !terminal {
			return nil
		}
	}
	return s.throttle.wait(ctx, matrixSendInterval)
}

func (s *matrixService) waitForRetry(ctx context.Context) error {
	s.retryPending = false
	timer := time.NewTimer(s.retryDelay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return s.throttle.wait(ctx, 0)
	}
}

type matrixDeliver = func(Request) (Response, bool)

func (s *matrixService) sendAttachments(title, body string, sources []*attachmentSource, resolve func(*attachmentSource) (attachment, error), deliver func(Request) (Response, bool)) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.mode != "off" {
		return s.webhook(title, body, deliver)
	}
	if s.access == "" && s.password != "" && s.user == "" {
		s.access = s.password
		var id [16]byte
		if _, err := rand.Read(id[:]); err != nil {
			return false, err
		}
		id[6] = id[6]&0x0f | 0x40
		id[8] = id[8]&0x3f | 0x80
		s.transactionUUID = fmt.Sprintf("%x-%x-%x-%x-%x", id[0:4], id[4:6], id[6:8], id[8:10], id[10:16])
	}
	if s.access == "" && !s.login(deliver) && !s.register(deliver) {
		return false, nil
	}
	if len(s.rooms) == 0 {
		result, ok, _ := s.fetch(http.MethodGet, "/joined_rooms", nil, nil, nil, deliver)
		if !ok {
			return false, nil
		}
		for _, room := range matrixList(matrixObject(result)["joined_rooms"]) {
			if value, ok := room.(string); ok {
				s.rooms = append(s.rooms, value)
			}
		}
		if len(s.rooms) == 0 {
			return false, nil
		}
	}
	var files []object
	for _, source := range sources {
		file, err := resolve(source)
		if err != nil {
			return false, err
		}
		image := strings.HasPrefix(strings.ToLower(file.mediaType), "image/")
		if !image && s.version == "2" {
			continue
		}
		result, ok, _ := s.fetch(http.MethodPost, "/upload", nil, nil, &file, deliver)
		response, isObject := result.(map[string]any)
		if !ok || !isObject {
			return false, nil
		}
		info := object{{"mimetype", file.mediaType}}
		if s.version == "2" {
			files = append(files, object{{"info", info}, {"msgtype", "m.image"}, {"body", "tta.webp"}, {"url", response["content_uri"]}})
		} else {
			info = append(info, field{"size", len(file.data)})
			kind := "m.image"
			if !image {
				kind = "m.file"
			}
			payload := object{{"body", file.name}, {"info", info}, {"msgtype", kind}, {"url", response["content_uri"]}}
			if !image {
				payload = append(payload, field{"filename", file.name})
			}
			files = append(files, payload)
		}
	}
	message, err := s.messagePayload(title, body)
	if err != nil {
		return false, err
	}
	success := true
	for _, room := range s.rooms {
		id := s.join(room, deliver)
		if id == "" {
			success = false
			continue
		}
		if s.version == "2" && s.image {
			imageTitle := title
			if imageTitle == "" {
				imageTitle = "info"
			}
			if !s.sendRoom(id, object{{"msgtype", "m.image"}, {"url", matrixIcon}, {"body", imageTitle}}, deliver, false) {
				success = false
				continue
			}
		}
		for _, file := range files {
			payload := append(slices.Clone(file), field{"room_id", id}, field{"type", "m.room.message"})
			if !s.sendRoom(id, payload, deliver, true) {
				success = false
			}
		}
		if !s.sendRoom(id, message, deliver, true) {
			success = false
		}
	}
	return success, nil
}

func matrixTitleHTML(title string, escape, whitespace bool) string {
	if title == "" {
		return ""
	}
	if escape {
		title = matrixEscape.Replace(title)
	}
	if whitespace {
		title = strings.NewReplacer("\t", "&emsp;", " ", "&nbsp;").Replace(title)
	}
	return "<h1>" + title + "</h1>"
}

func matrixRenderMarkdown(body string) (string, error) {
	var out bytes.Buffer
	if err := matrixMarkdown.Convert([]byte(body), &out); err != nil {
		return "", err
	}
	return strings.TrimSpace(out.String()), nil
}

func (s *matrixService) messagePayload(title, body string) (object, error) {
	plain := body
	if title != "" {
		plain = "# " + title + "\r\n" + body
	}
	payload := object{{"msgtype", "m." + s.messageType}, {"body", plain}}
	if s.format == "text" {
		return payload, nil
	}
	formatted := matrixTitleHTML(title, s.format == "markdown", false)
	if s.format == "markdown" {
		var err error
		body, err = matrixRenderMarkdown(body)
		if err != nil {
			return nil, err
		}
	}
	return append(payload, field{"format", "org.matrix.custom.html"}, field{"formatted_body", formatted + body}), nil
}

func (s *matrixService) webhook(title, body string, deliver matrixDeliver) (bool, error) {
	username := s.user
	if username == "" {
		username = "Apprise"
	}
	endpoint := s.server + matrixWebhookPath
	token := s.password
	if token == "" {
		token = s.user
		if !s.hasUser {
			token = "None"
		}
	}
	endpoint += token
	if s.mode == "t2bot" {
		endpoint = "https://webhooks.t2bot.io" + matrixWebhookPath + s.access
	}
	var payload object
	switch s.mode {
	case "matrix", "t2bot":
		format := "plain"
		if s.format != "text" {
			format = "html"
			if s.format == "markdown" {
				var err error
				body, err = matrixRenderMarkdown(body)
				if err != nil {
					return false, err
				}
			}
			body = matrixTitleHTML(title, true, true) + body
		} else if title != "" {
			body = title + "\r\n" + body
		}
		payload = object{{"displayName", username}, {"format", format}, {"text", body}}
		if s.mode == "t2bot" && s.image {
			payload = append(payload, field{"avatarUrl", matrixIcon})
		}
	case "slack":
		if strings.Contains(title, "\r*\n") || strings.Contains(body, "\r*\n") {
			return false, nil
		}
		escape := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
		payload = object{{"username", username}, {"mrkdwn", s.format == "markdown"}, {"attachments", []any{object{
			{"title", escape.Replace(title)}, {"text", escape.Replace(body)}, {"color", "#3AA3E3"}, {"ts", float64(time.Now().UnixNano()) / 1e9}, {"footer", "Apprise"},
		}}}}
	default:
		return false, nil
	}
	data, err := pythonJSON(payload)
	if err != nil {
		return false, err
	}
	_, sent := deliver(s.post(endpoint, "application/json", data))
	return sent, nil
}

func (s *matrixService) login(deliver matrixDeliver) bool {
	if s.user == "" || s.password == "" {
		return false
	}
	payload := object{{"type", "m.login.password"}}
	if s.version == "3" {
		payload = append(payload, field{"identifier", object{{"type", "m.id.user"}, {"user", s.user}}})
	} else {
		payload = append(payload, field{"user", s.user})
	}
	payload = append(payload, field{"password", s.password})
	result, ok, _ := s.fetch(http.MethodPost, "/login", payload, nil, nil, deliver)
	if !ok {
		return false
	}
	s.setAuth(result)
	return s.access != ""
}

func (s *matrixService) register(deliver matrixDeliver) bool {
	payload := object{{"kind", "user"}, {"auth", object{{"type", "m.login.dummy"}}}}
	if s.user != "" {
		payload = append(payload, field{"username", s.user})
	}
	if s.password != "" {
		payload = append(payload, field{"password", s.password})
	}
	result, ok, _ := s.fetch(http.MethodPost, "/register", payload, [][2]string{{"kind", "user"}}, nil, deliver)
	if !ok {
		return false
	}
	s.setAuth(result)
	return s.access != ""
}

func (s *matrixService) setAuth(result any) {
	value := matrixObject(result)
	s.access = matrixString(value["access_token"])
	s.home = matrixString(value["home_server"])
}

func (s *matrixService) join(room string, deliver matrixDeliver) string {
	room = strings.TrimFunc(room, sourceSpace)
	match := matrixRoomID.FindStringSubmatch(room)
	identifier := match != nil
	if match == nil {
		match = matrixAlias.FindStringSubmatch(room)
	}
	if match == nil || s.access == "" {
		return ""
	}
	home := match[2]
	if home == "" {
		home = s.home
	}
	if home == "" {
		home = "None"
	}
	prefix := "#"
	if identifier {
		prefix = "!"
	}
	key := prefix + match[1] + ":" + home
	if id, ok := s.joined[key]; ok {
		return id
	}
	lookup := key
	if identifier && match[2] == "" && !s.requireHomeserver {
		lookup = prefix + match[1]
	}
	result, ok, status := s.fetch(http.MethodPost, "/join/"+sourceQuote(lookup, "/"), object{}, nil, nil, deliver)
	value := matrixObject(result)
	if ok {
		id := matrixString(value["room_id"])
		if id == "" && identifier {
			id = lookup
		}
		s.joined[key] = id
		return id
	}
	if identifier || (status != http.StatusNotFound && value["errcode"] != "M_NOT_FOUND") {
		return ""
	}
	payload := object{{"room_alias_name", match[1]}, {"name", "#" + match[1] + " - Apprise Notifications"}, {"visibility", "private"}, {"preset", "trusted_private_chat"}}
	result, ok, _ = s.fetch(http.MethodPost, "/createRoom", payload, nil, nil, deliver)
	value = matrixObject(result)
	if !ok {
		if value["errcode"] != "M_ROOM_IN_USE" {
			return ""
		}
		result, ok, _ = s.fetch(http.MethodGet, "/directory/room/"+sourceQuote(key, "/"), nil, nil, nil, deliver)
		if !ok {
			return ""
		}
		return matrixString(matrixObject(result)["room_id"])
	}
	id := matrixString(value["room_id"])
	if alias := matrixString(value["room_alias"]); alias != "" {
		s.joined[alias] = id
	}
	return id
}

func (s *matrixService) sendRoom(id string, payload object, deliver matrixDeliver, incrementOnFailure bool) bool {
	transaction := s.transactionUUID
	if transaction == "" {
		transaction = strconv.Itoa(s.transaction)
	}
	_, ok, _ := s.fetch(http.MethodPut, "/rooms/"+sourceQuote(id, "/")+"/send/m.room.message/"+transaction, payload, nil, nil, deliver)
	if s.access != s.password && (ok || incrementOnFailure) {
		s.transaction++
	}
	return ok
}

func (s *matrixService) fetch(method, path string, payload any, params [][2]string, file *attachment, deliver matrixDeliver) (any, bool, int) {
	base, ok := s.baseURL(deliver)
	if !ok {
		return nil, false, http.StatusInternalServerError
	}
	prefix := "/_matrix/client/v3"
	if s.version == "2" {
		prefix = "/_matrix/client/r0"
	}
	if file != nil {
		prefix = "/_matrix/media/v3"
		if s.version == "2" {
			prefix = "/_matrix/media/r0"
		}
	}
	return s.fetchURL(method, base+prefix+path, payload, params, file, deliver)
}

func (s *matrixService) fetchURL(method, endpoint string, payload any, params [][2]string, file *attachment, deliver matrixDeliver) (any, bool, int) {
	var data []byte
	contentType := "application/json"
	if file != nil {
		data, contentType = file.data, file.mediaType
		params = append(params, [2]string{"filename", file.name})
	} else {
		var err error
		data, err = pythonJSON(payload)
		if err != nil {
			return nil, false, http.StatusInternalServerError
		}
	}
	call := s.post(endpoint, contentType, data)
	call.Method, call.ParamOrder = method, params
	call.Headers["Accept"] = "application/json"
	call.HeaderOrder = append(call.HeaderOrder, [2]string{"Accept", "application/json"})
	if s.access != "" {
		auth := "Bearer " + s.access
		call.Headers["Authorization"] = auth
		call.HeaderOrder = append(call.HeaderOrder, [2]string{"Authorization", auth})
	}
	status := http.StatusInternalServerError
	for attempt := 0; attempt < 2; attempt++ {
		reply, sent := deliver(call)
		if reply.Status == 0 {
			return nil, false, status
		}
		status = reply.Status
		var result any
		decoder := json.NewDecoder(bytes.NewReader(reply.Content))
		decoder.UseNumber()
		if !json.Valid(reply.Content) || decoder.Decode(&result) != nil {
			return nil, false, status
		}
		if status == http.StatusTooManyRequests {
			delay, ok := matrixRetryDelay(result)
			if !ok {
				return nil, false, status
			}
			s.retryDelay, s.retryPending, s.retryTerminal = delay, true, attempt == 1
			continue
		}
		return result, sent, status
	}
	return nil, false, status
}

func matrixRetryDelay(result any) (time.Duration, bool) {
	obj, ok := result.(map[string]any)
	if !ok {
		return 0, false
	}
	value, found := obj["retry_after_ms"]
	if !found {
		if nested, exists := obj["error"]; exists {
			errObject, ok := nested.(map[string]any)
			if !ok {
				return 0, false
			}
			value, found = errObject["retry_after_ms"]
		}
	}
	if !found {
		return time.Second, true
	}
	var ms float64
	switch n := value.(type) {
	case bool:
		if n {
			ms = 1
		}
	case json.Number:
		var err error
		ms, err = n.Float64()
		if err != nil {
			return 0, false
		}
	default:
		return 0, false
	}
	if math.IsNaN(ms) || math.IsInf(ms, 0) || ms < 0 || ms >= float64(math.MaxInt64)/float64(time.Millisecond) {
		return 0, false
	}
	return time.Duration(ms * float64(time.Millisecond)), true
}

func (s *matrixService) baseURL(deliver matrixDeliver) (string, bool) {
	if !s.discovery {
		return s.server, true
	}
	now := time.Now()
	if now.Before(s.identityExpiry) {
		if now.Before(s.discoveryExpiry) && s.delegated != "" {
			return s.delegated, true
		}
		return s.server, true
	}
	result, _, status := s.fetchURL(http.MethodGet, s.server+"/.well-known/matrix/client", nil, nil, nil, deliver)
	switch {
	case status == http.StatusNotFound || status == http.StatusOK && len(matrixObject(result)) == 0:
		s.delegated = ""
		s.discoveryExpiry, s.identityExpiry = now.Add(24*time.Hour), now.Add(24*time.Hour)
		return s.server, true
	case status != http.StatusOK:
		return "", false
	}
	value := matrixObject(result)
	base, ok := matrixDiscoveryEndpoint(value["m.homeserver"])
	if !ok {
		return "", false
	}
	_, _, status = s.fetchURL(http.MethodGet, base+"/_matrix/client/versions", nil, nil, nil, deliver)
	if status != http.StatusOK {
		return "", false
	}
	if id, exists := value["m.identity_server"]; exists {
		identity, ok := matrixDiscoveryEndpoint(id)
		if !ok {
			return "", false
		}
		_, _, status = s.fetchURL(http.MethodGet, identity+"/_matrix/identity/v2", nil, nil, nil, deliver)
		if status != http.StatusOK {
			return "", false
		}
	}
	s.delegated = base
	now = time.Now()
	s.discoveryExpiry, s.identityExpiry = now.Add(24*time.Hour), now.Add(24*time.Hour+2*time.Second)
	return base, true
}

func matrixDiscoveryEndpoint(value any) (string, bool) {
	endpoint := strings.TrimRight(matrixString(matrixObject(value)["base_url"]), "/")
	validation := endpoint
	if !strings.Contains(validation, "://") {
		// URLBase uses an "unknown" default for validation, then Requests
		// rejects the original unqualified URL after Matrix throttles it.
		validation = "unknown://" + validation
	}
	_, ok := parseSourceURL(validation)
	return endpoint, ok
}

func (s *matrixService) closeNotification(ctx context.Context, transport Transport) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.retryPending && s.waitForRetry(ctx) != nil {
		return
	}
	if s.mode != "off" || s.access == "" || s.access == s.password && s.user == "" {
		return
	}
	deliver := func(call Request) (Response, bool) {
		if s.waitForRequest(ctx, call) != nil {
			return Response{}, false
		}
		reply, err := transport(ctx, call)
		return reply, err == nil && s.successful(reply.Status)
	}
	value, ok, _ := s.fetch(http.MethodPost, "/logout", object{}, nil, nil, deliver)
	if s.retryPending {
		_ = s.waitForRetry(ctx)
	}
	if ok || matrixObject(value)["errcode"] == "M_UNKNOWN_TOKEN" {
		s.access, s.home, s.transaction, s.transactionUUID = "", "", 0, ""
	}
}

func matrixObject(v any) map[string]any { obj, _ := v.(map[string]any); return obj }
func matrixList(v any) []any            { list, _ := v.([]any); return list }
func matrixString(v any) string         { str, _ := v.(string); return str }
