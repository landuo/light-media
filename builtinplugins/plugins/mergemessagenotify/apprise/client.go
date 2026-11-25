// Package apprise incrementally ports the installed Apprise 1.9.9 dependency.
// The native library is not yet the complete provider set and has no implicit
// network transport. Valid unported providers are explicit dependencies.
package apprise

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"reflect"
	"regexp"
	"runtime"
	"strings"
	"sync"
)

var ErrUnsupported = errors.New("Apprise provider/dependency requires native implementation")

type Request struct {
	Method, URL             string
	Data                    []byte
	Headers, Params         map[string]string
	HeaderOrder, ParamOrder [][2]string
	RemoveHeaders           []string // Optional requests headers with None values; other clients retain defaults.
	Auth                    []any
	Verify                  bool
	Timeout                 [2]float64
	MaxResponseBytes        int64 // zero preserves the ordinary provider response contract.
}
type Response struct {
	Status  int
	Headers map[string]string
	Content []byte
}
type Transport func(context.Context, Request) (Response, error)

type notificationService interface {
	successful(status int) bool
}

type plannedNotificationService interface {
	notificationService
	requests(title, body string) ([]Request, error)
}

type orderedNotificationService interface {
	notificationService
	sendText(title, body string, deliver func(Request) (Response, bool)) (bool, error)
}

type attachmentChunkService interface {
	notificationService
	attachmentChunks(title, body string) ([]notificationChunk, error)
	truncateAttachments() bool
}

type attachmentService interface {
	attachmentChunkService
	request(title, body string, attachments []attachment) (Request, error)
}

type orderedAttachmentService interface {
	attachmentChunkService
	sendAttachments(title, body string, sources []*attachmentSource, resolve func(*attachmentSource) (attachment, error), deliver func(Request) (Response, bool)) (bool, error)
}

type Client struct {
	transport           Transport
	services            []notificationService
	ReadLocalAttachment LocalAttachmentReader
}

func New(transport Transport) *Client { return &Client{transport: transport} }

// Close releases sessions created by this notification client.
func (c *Client) Close(ctx context.Context) {
	if c.transport == nil {
		return
	}
	for _, service := range c.services {
		if closer, ok := service.(interface {
			closeNotification(context.Context, Transport)
		}); ok {
			closer.closeNotification(ctx, c.transport)
		}
	}
}

//go:embed testdata/catalog.json
var catalogJSON []byte
var knownSchemas = func() map[string]bool {
	var catalog []struct{ Schema string }
	if err := json.Unmarshal(catalogJSON, &catalog); err != nil {
		panic(err)
	}
	out := map[string]bool{}
	for _, p := range catalog {
		out[p.Schema] = true
	}
	return out
}()

var sourceURLStart = regexp.MustCompile(`[A-Za-z0-9İıſK]+://`)

// Apprise.add accepts multiple URLs in one configuration line. A separator
// only terminates a URL when a new scheme follows, so URL values can contain
// whitespace and commas without starting another service.
func sourceURLs(line string) []string {
	starts := sourceURLStart.FindAllStringIndex(line, -1)
	if len(starts) == 0 {
		return nil
	}
	var urls []string
	start := starts[0][0]
	for _, next := range starts[1:] {
		if len([]rune(line[next[0]:next[1]-3])) > 32 {
			continue
		}
		previous := strings.TrimRightFunc(line[start:next[0]], func(r rune) bool {
			return r == ',' || sourceSpace(r)
		})
		if len(previous) != next[0]-start {
			urls = append(urls, previous)
			start = next[0]
		}
	}
	return append(urls, line[start:])
}

func (c *Client) Add(line string) (bool, error) {
	urls := sourceURLs(line)
	if len(urls) == 0 {
		return false, nil
	}
	accepted := true
	for _, raw := range urls {
		added, err := c.addService(strings.ReplaceAll(raw, "/#", "/%23"))
		if err != nil {
			return false, err
		}
		accepted = added && accepted
	}
	return accepted, nil
}

func (c *Client) addService(raw string) (bool, error) {
	sep := strings.Index(raw, "://")
	if sep <= 0 {
		return false, nil
	}
	scheme := strings.ToLower(strings.TrimSpace(raw[:sep]))
	var service notificationService
	var valid bool
	var err error
	switch scheme {
	case "json", "jsons":
		service, valid, err = parseJSONService(raw)
	case "apprise", "apprises":
		service, valid = parseGatewayService(raw)
	case "bark", "barks":
		service, valid = parseBarkService(raw)
	case "gotify", "gotifys":
		service, valid = parseGotifyService(raw)
	case "ntfy", "ntfys":
		service, valid = parseNtfyService(raw)
	case "pushdeer", "pushdeers":
		service, valid = parsePushDeerService(raw)
	case "pbul":
		service, valid = parsePushBulletService(raw)
	case "schan":
		service, valid = parseServerChanService(raw)
	case "psafer", "psafers":
		service, valid = parsePushSaferService(raw)
	case "join":
		service, valid = parseJoinService(raw)
	case "ifttt":
		service, valid = parseIFTTTService(raw)
	case "pover":
		service, valid = parsePushoverService(raw)
	case "discord":
		service, valid = parseDiscordService(raw)
	case "pjet", "pjets":
		service, valid = parsePushjetService(raw)
	case "mmost", "mmosts":
		service, valid = parseMattermostService(raw)
	case "gchat":
		service, valid = parseGoogleChatService(raw)
	case "slack":
		service, valid = parseSlackService(raw)
	case "tgram":
		service, valid = parseTelegramService(raw)
	case "zulip":
		service, valid = parseZulipService(raw)
	case "flock":
		service, valid = parseFlockService(raw)
	case "prowl":
		service, valid = parseProwlService(raw)
	case "spush":
		service, valid = parseSimplePushService(raw)
	case "rocket", "rockets":
		service, valid = parseRocketChatService(raw)
	case "matrix", "matrixs":
		service, valid = parseMatrixService(raw)
	case "pushplus":
		service, valid = parsePushPlusService(raw)
	case "feishu":
		service, valid = parseFeishuService(raw)
	case "dingtalk":
		service, valid = parseDingTalkService(raw)
	case "pagerduty":
		service, valid = parsePagerDutyService(raw)
	case "twist":
		service, valid = parseTwistService(raw)
	case "chanify":
		service, valid = parseChanifyService(raw)
	case "pushme":
		service, valid = parsePushMeService(raw)
	case "wxpusher":
		service, valid = parseWxPusherService(raw)
	case "lark":
		service, valid = parseLarkService(raw)
	case "wecombot":
		service, valid = parseWeComBotService(raw)
	case "pushy":
		service, valid = parsePushyService(raw)
	case "line":
		service, valid = parseLineService(raw)
	case "qq":
		service, valid = parseQQService(raw)
	case "wxteams", "webex":
		service, valid = parseWebexTeamsService(raw)
	case "form", "forms":
		service, valid = parseFormService(raw)
	case "xml", "xmls":
		service, valid = parseCustomXMLService(raw)
	case "hassio", "hassios":
		service, valid = parseHomeAssistantService(raw)
	case "http", "https":
		if native, ok := gatewayNativeURL(raw); ok {
			service, valid = parseGatewayService(native)
		} else if native, ok := ntfyNativeURL(raw); ok {
			service, valid = parseNtfyService(native)
		} else if native, ok := iftttNativeURL(raw); ok {
			service, valid = parseIFTTTService(native)
		} else if native, ok := discordNativeURL(raw); ok {
			service, valid = parseDiscordService(native)
		} else if native, ok := mattermostNativeURL(raw); ok {
			service, valid = parseMattermostService(native)
		} else if native, ok := googleChatNativeURL(raw); ok {
			service, valid = parseGoogleChatService(native)
		} else if native, ok := slackNativeURL(raw); ok {
			service, valid = parseSlackService(native)
		} else if native, ok := flockNativeURL(raw); ok {
			service, valid = parseFlockService(native)
		} else if native, ok := matrixNativeURL(raw); ok {
			service, valid = parseMatrixService(native)
		} else if native, ok := pushPlusNativeURL(raw); ok {
			service, valid = parsePushPlusService(native)
		} else if native, ok := larkNativeURL(raw); ok {
			service, valid = parseLarkService(native)
		} else if native, ok := weComBotNativeURL(raw); ok {
			service, valid = parseWeComBotService(native)
		} else if native, ok := qqNativeURL(raw); ok {
			service, valid = parseQQService(native)
		} else if native, ok := webexTeamsNativeURL(raw); ok {
			service, valid = parseWebexTeamsService(native)
		} else {
			return false, ErrUnsupported
		}
	default:
		if knownSchemas[scheme] {
			return false, ErrUnsupported
		}
		return false, nil
	}
	if err != nil || !valid {
		return false, err
	}
	c.services = append(c.services, service)
	return true, nil
}
func (c *Client) Notify(ctx context.Context, title, body, attach any) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(c.services) == 0 {
		return false, nil
	}
	attachments, validAttach := attachmentSources(attach)
	if !validAttach {
		return false, nil
	}
	titleText, ok := sourceText(title)
	if !ok {
		return false, nil
	}
	bodyText, ok := sourceText(body)
	if !ok || bodyText == "" && len(attachments) == 0 {
		return false, nil
	}
	type requestGroup struct {
		service  notificationService
		calls    []Request
		attached attachmentChunkService
		ordered  orderedNotificationService
	}
	requests := make([]requestGroup, 0, len(c.services))
	hasCalls := false
	for _, service := range c.services {
		_, orderedText := service.(interface{ sendTextInOrder() })
		if provider, ok := service.(attachmentChunkService); ok && (len(attachments) != 0 || orderedText) {
			hasCalls = true
			requests = append(requests, requestGroup{service: service, attached: provider})
			continue
		}
		if len(attachments) != 0 && bodyText == "" {
			requests = append(requests, requestGroup{service: service})
			continue
		}
		if provider, ok := service.(orderedNotificationService); ok {
			hasCalls = true
			requests = append(requests, requestGroup{service: service, ordered: provider})
			continue
		}
		provider, ok := service.(plannedNotificationService)
		if !ok {
			return nil, ErrUnsupported
		}
		calls, err := provider.requests(titleText, bodyText)
		if err != nil {
			return nil, err
		}
		hasCalls = hasCalls || len(calls) != 0
		requests = append(requests, requestGroup{service: service, calls: calls})
	}
	if hasCalls && c.transport == nil {
		return nil, ErrUnsupported
	}
	type sendResult struct {
		success bool
		err     error
	}
	send := func(group requestGroup) sendResult {
		throttle, _ := group.service.(interface{ waitToSend(context.Context) error })
		requestThrottle, _ := group.service.(interface {
			waitForRequest(context.Context, Request) error
		})
		responseCheck, _ := group.service.(interface{ successfulResponse(Response) bool })
		deliver := func(request Request) (Response, bool) {
			var waitErr error
			if requestThrottle != nil {
				waitErr = requestThrottle.waitForRequest(ctx, request)
			} else if throttle != nil {
				waitErr = throttle.waitToSend(ctx)
			}
			if waitErr != nil {
				return Response{}, false
			}
			reply, err := c.transport(ctx, request)
			if err != nil || !group.service.successful(reply.Status) {
				return reply, false
			}
			return reply, responseCheck == nil || responseCheck.successfulResponse(reply)
		}
		if group.ordered != nil {
			sent, err := group.ordered.sendText(titleText, bodyText, deliver)
			return sendResult{success: sent, err: err}
		}
		if group.attached == nil {
			if len(group.calls) == 0 {
				empty, _ := group.service.(interface{ emptyNotificationSuccessful() bool })
				return sendResult{success: empty != nil && empty.emptyNotificationSuccessful()}
			}
			success := true
			for _, request := range group.calls {
				_, sent := deliver(request)
				success = sent && success
			}
			return sendResult{success: success}
		}
		chunks, err := group.attached.attachmentChunks(titleText, bodyText)
		if err != nil {
			return sendResult{err: err}
		}
		sources := attachments
		if len(sources) != 0 && group.attached.truncateAttachments() {
			sources = sources[:1]
		}
		success := len(chunks) != 0
		for _, chunk := range chunks {
			var sent bool
			var err error
			if provider, ok := group.attached.(orderedAttachmentService); ok {
				resolve := func(source *attachmentSource) (attachment, error) {
					return source.resolve(ctx, c.transport, c.ReadLocalAttachment)
				}
				sent, err = provider.sendAttachments(chunk.title, chunk.body, sources, resolve, deliver)
			} else {
				var files []attachment
				files, err = resolveAttachments(ctx, sources, c.transport, c.ReadLocalAttachment)
				if err == nil {
					var request Request
					request, err = group.attached.(attachmentService).request(chunk.title, chunk.body, files)
					if err == nil {
						_, sent = deliver(request)
					}
				}
			}
			if errors.Is(err, errAttachmentUnavailable) {
				// Failed downloads are never cached. NotifyBase still tries later chunks.
				success = false
				continue
			}
			if err != nil {
				return sendResult{err: err}
			}
			success = sent && success
		}
		return sendResult{success: success}
	}
	if len(requests) == 1 {
		result := send(requests[0])
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if result.err != nil {
			return nil, result.err
		}
		return result.success, nil
	}
	// The original default asset uses ThreadPoolExecutor's CPython 3.12 bound.
	// Reserve workers before sending; no unbounded waiting goroutine per URL.
	workers := runtime.NumCPU() + 4
	if workers > 32 {
		workers = 32
	}
	if workers > len(requests) {
		workers = len(requests)
	}
	jobs := make(chan requestGroup)
	results := make(chan sendResult, len(requests))
	var group sync.WaitGroup
	for i := 0; i < workers; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			for request := range jobs {
				results <- send(request)
			}
		}()
	}
	for _, request := range requests {
		jobs <- request
	}
	close(jobs)
	group.Wait()
	close(results)
	success := true
	var sendErr error
	for result := range results {
		success = success && result.success
		if sendErr == nil {
			sendErr = result.err
		}
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if sendErr != nil {
		return nil, sendErr
	}
	return success, nil
}

func sourceText(v any) (string, bool) {
	if v == nil {
		return "", true
	}
	if s, ok := v.(string); ok {
		return s, true
	}
	value := reflect.ValueOf(v)
	falseValue := false
	switch value.Kind() {
	case reflect.Bool:
		falseValue = !value.Bool()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		falseValue = value.Int() == 0
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		falseValue = value.Uint() == 0
	case reflect.Float32, reflect.Float64:
		falseValue = value.Float() == 0
	case reflect.Array, reflect.Slice, reflect.Map:
		falseValue = value.Len() == 0
	}
	if falseValue {
		return "", true
	}
	return "", false
}
