package download

// Fixed source leaf ports. No arbitrary RPC, local torrent path, data-deletion
// option, duplicate lookup, task projection or automatic mutation replay.
import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"io"
	"math"
	"math/big"
	"mime/multipart"
	"net"
	"net/http"
	"net/textproto"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"light-media/internal/jinja/exec"
	"light-media/internal/logging"
	"light-media/internal/plugins/downloadercommands"
)

type sharedDownloaderCommands struct {
	iyuu          *IYUUClient
	trReady       bool           // Protected by the existing native client mutex.
	trSessionInfo map[string]any // Existing session-get response; same mutex, read only by the new fixed change port.
}
type sharedQBCommands struct{ owner *sharedDownloaderCommands }
type sharedTRCommands struct{ owner *sharedDownloaderCommands }

// NewSharedDownloaderCommands creates one owned fixed native client. Only this
// qB clone follows the separately approved source VERIFY=False constructor;
// environment proxy policy is unchanged. TR retains its established strict TLS,
// trust_env=False and connect+idle timeout policy.
func NewSharedDownloaderCommands(config Config, apiKey string, guard func(context.Context) error) (downloadercommands.QBC, downloadercommands.TRC, func(), error) {
	if guard == nil {
		return nil, nil, nil, commandBoundary("missing trusted native guard")
	}
	if config.Kind == "qb" {
		config.Kind = "qbittorrent"
	}
	if config.Kind == "tr" {
		config.Kind = "transmission"
	}
	c, err := NewIYUUClient(config, IYUUOptions{APIKey: apiKey, Guard: guard})
	if err != nil {
		return nil, nil, nil, err
	}
	var transport *http.Transport
	if config.Kind == "transmission" {
		transport = &http.Transport{Proxy: nil, ForceAttemptHTTP2: false, TLSHandshakeTimeout: 60 * time.Second, IdleConnTimeout: 90 * time.Second, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			conn, err := (&net.Dialer{Timeout: 60 * time.Second, KeepAlive: 30 * time.Second}).DialContext(ctx, network, address)
			if err != nil {
				return nil, err
			}
			return &transmissionQueryConnection{Conn: conn}, nil
		}}
		c.client.http.Timeout = 0
	} else {
		original, ok := http.DefaultTransport.(*http.Transport)
		if !ok {
			return nil, nil, nil, commandBoundary("unsupported default transport type")
		}
		transport = original.Clone()
		if transport.TLSClientConfig == nil {
			transport.TLSClientConfig = &tls.Config{}
		}
		// Transport.Clone owns this TLS config. Do not mutate the global
		// transport, old IYUU clients, TR, CA stores or environment settings.
		transport.TLSClientConfig.InsecureSkipVerify = true
	}
	var rt http.RoundTripper = &mutationDispatchTransport{next: logging.Transport(transport)}
	if apiKey != "" {
		rt = &iyuuBearer{next: rt, origin: c.client.base.Scheme + "://" + c.client.base.Host, token: apiKey}
	}
	c.client.http.Transport = &iyuuGuard{next: rt, guard: guard}
	owner := &sharedDownloaderCommands{iyuu: c}
	if config.Kind == "qbittorrent" {
		return &sharedQBCommands{owner}, nil, c.Close, nil
	}
	return nil, &sharedTRCommands{owner}, c.Close, nil
}

func commandBoundary(reason string) error { return &downloadercommands.BoundaryError{Reason: reason} }
func commandValueError(message string) error {
	return &downloadercommands.SourceException{Class: "ValueError", Message: message}
}

// The monotonic private dispatch proof survives all auth/session requests.
// A delegated selected request is never retried or turned into SourceException.
func (c *sharedDownloaderCommands) run(ctx context.Context, operation func(context.Context) (any, error)) (result any, err error) {
	if ctx == nil {
		return nil, commandBoundary("missing command context")
	}
	ctx, proof := beginMutation(ctx)
	outcome := ""
	defer func() { err = proof.finish(err, &outcome) }()
	if err = c.iyuu.options.Guard(ctx); err != nil {
		mutationOwnerDenied(ctx, err)
		return nil, err
	}
	if err = c.iyuu.client.nativeLock(ctx); err != nil {
		return nil, err
	}
	defer c.iyuu.client.mu.Unlock()
	result, err = operation(ctx)
	if guardErr := c.iyuu.options.Guard(ctx); guardErr != nil {
		mutationOwnerDenied(ctx, guardErr)
		return nil, guardErr
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return result, err
}

func commandScalar(value any) (string, error) {
	switch v := value.(type) {
	case nil:
		return "None", nil
	case string:
		return v, nil
	case bool:
		if v {
			return "True", nil
		}
		return "False", nil
	case int:
		return strconv.Itoa(v), nil
	case int64:
		return strconv.FormatInt(v, 10), nil
	case json.Number:
		if !strings.ContainsAny(string(v), ".eE") {
			if n, ok := new(big.Int).SetString(string(v), 10); ok {
				return n.String(), nil
			}
		}
		if n, err := strconv.ParseFloat(string(v), 64); err == nil && !math.IsNaN(n) && !math.IsInf(n, 0) {
			return exec.AsValue(n).String(), nil
		}
	case float64:
		if !math.IsNaN(v) && !math.IsInf(v, 0) {
			return exec.AsValue(v).String(), nil
		}
	}
	return "", commandBoundary("non-finite or structured scalar preparation is pending")
}
func commandJoined(value any, separator string, onlyStrings bool) (*string, error) {
	if value == nil {
		return nil, nil
	}
	if s, ok := value.(string); ok {
		return &s, nil
	}
	var values []any
	switch v := value.(type) {
	case []any:
		values = v
	case []string:
		for _, item := range v {
			values = append(values, item)
		}
	default:
		return nil, commandBoundary("non-list iterable preparation is pending")
	}
	parts := make([]string, len(values))
	for i, item := range values {
		if onlyStrings {
			s, ok := item.(string)
			if !ok {
				return nil, commandBoundary("non-string tag preparation is pending")
			}
			parts[i] = s
		} else {
			s, err := commandScalar(item)
			if err != nil {
				return nil, err
			}
			parts[i] = s
		}
	}
	s := strings.Join(parts, separator)
	return &s, nil
}
func commandOptionalString(value any) (*string, error) {
	if value == nil {
		return nil, nil
	}
	s, ok := value.(string)
	if !ok {
		return nil, commandBoundary("non-string optional field is pending")
	}
	return &s, nil
}
func commandOptionalBool(value any) (*bool, error) {
	if value == nil {
		return nil, nil
	}
	b, ok := value.(bool)
	if !ok {
		return nil, commandBoundary("non-boolean optional field is pending")
	}
	return &b, nil
}
func commandSet(form url.Values, key string, value any) error {
	if value == nil {
		return nil
	}
	s, err := commandScalar(value)
	if err != nil {
		return err
	}
	form.Set(key, s)
	return nil
}

func (q *sharedQBCommands) version(ctx context.Context) (string, error) {
	raw, err := q.owner.iyuu.client.qb(mutationReadOnly(ctx), http.MethodGet, "app/webapiVersion", "", "")
	if err != nil {
		return "", err
	}
	if _, err = iyuuVersionAtLeast(string(raw), 0, 0); err != nil {
		return "", commandValueError("Invalid Web API version")
	}
	return string(raw), nil // Deliberately fresh; do not borrow the old IYUU cache.
}
func (q *sharedQBCommands) control(ctx context.Context, operation string, ids any) (any, error) {
	return q.owner.run(ctx, func(ctx context.Context) (any, error) {
		selector, err := commandJoined(ids, "|", false)
		if err != nil {
			return nil, err
		}
		form := url.Values{}
		if selector != nil {
			form.Set("hashes", *selector)
		}
		endpoint := operation
		if operation == "start" {
			version, err := q.version(ctx)
			if err != nil {
				return nil, err
			}
			modern, err := iyuuVersionAtLeast(version, 2, 11)
			if err != nil {
				return nil, err
			}
			if !modern {
				endpoint = "resume"
			}
		}
		if operation == "delete" {
			form.Set("deleteFiles", "False")
		}
		_, err = q.owner.iyuu.client.qbSelected(ctx, http.MethodPost, "torrents/"+endpoint, form.Encode(), "application/x-www-form-urlencoded")
		return nil, err // Source controls ignore successful response text.
	})
}
func (q *sharedQBCommands) Start(ctx context.Context, ids any) (any, error) {
	return q.control(ctx, "start", ids)
}
func (q *sharedQBCommands) Recheck(ctx context.Context, ids any) (any, error) {
	return q.control(ctx, "recheck", ids)
}
func (q *sharedQBCommands) DeleteKeepFiles(ctx context.Context, ids any) (any, error) {
	return q.control(ctx, "delete", ids)
}

func (q *sharedQBCommands) Add(ctx context.Context, in downloadercommands.QBAddCall) (any, error) {
	return q.owner.run(ctx, func(ctx context.Context) (any, error) {
		return q.addInside(ctx, in)
	})
}

// addInside retains the original Add preparation and selected request exactly.
// It requires the caller to hold the existing native lock and mutation scope.
func (q *sharedQBCommands) addInside(ctx context.Context, in downloadercommands.QBAddCall) (any, error) {
	cookie, err := commandOptionalString(in.Cookie)
	if err != nil {
		return nil, err
	}
	var content []byte
	if in.TorrentFiles != nil {
		var ok bool
		content, ok = in.TorrentFiles.([]byte)
		if !ok {
			return nil, commandBoundary("file mappings, paths and file-like values are not exposed")
		}
		if len(content) > 16<<20 {
			return nil, commandBoundary("material exceeds retained 16MiB bound")
		}
	}
	form := url.Values{}
	if cookie != nil {
		form.Set("cookie", *cookie)
	}
	if in.URLs != nil {
		text, ok := in.URLs.(string)
		if !ok {
			return nil, commandBoundary("structured qB URL input is pending")
		}
		form.Set("urls", text)
	}
	for key, value := range map[string]any{"savepath": in.SavePath, "category": in.Category} {
		text, err := commandOptionalString(value)
		if err != nil {
			return nil, err
		}
		if text != nil {
			form.Set(key, *text)
		}
	}
	tags, err := commandJoined(in.Tags, ",", true)
	if err != nil {
		return nil, err
	}
	if tags != nil {
		form.Set("tags", *tags)
	}
	for key, value := range map[string]any{"autoTMM": in.AutoManagement, "sequentialDownload": in.Sequential, "firstLastPiecePrio": in.FirstLastPiece} {
		if _, err := commandOptionalBool(value); err != nil {
			return nil, err
		}
		if err := commandSet(form, key, value); err != nil {
			return nil, err
		}
	}
	paused, err := commandOptionalBool(in.Paused)
	if err != nil {
		return nil, err
	}
	stopped, err := commandOptionalBool(in.Extra["is_stopped"])
	if err != nil {
		return nil, err
	}
	if paused != nil && *paused {
		stopped = paused
	}
	if stopped != nil {
		if *stopped {
			form.Set("paused", "True")
		} else {
			form.Set("paused", "False")
		}
		form.Set("stopped", form.Get("paused"))
	}
	// Only these fixed vendor fields are admitted. Request overrides, unknown
	// kwargs, SSL/key inputs and data-removing share policies remain pending.
	fieldNames := map[string]string{"is_skip_checking": "skip_checking", "is_root_folder": "root_folder", "rename": "rename", "upload_limit": "upLimit", "download_limit": "dlLimit", "content_layout": "contentLayout", "ratio_limit": "ratioLimit", "seeding_time_limit": "seedingTimeLimit", "download_path": "downloadPath", "use_download_path": "useDownloadPath", "stop_condition": "stopCondition", "add_to_top_of_queue": "addToTopOfQueue", "inactive_seeding_time_limit": "inactiveSeedingTimeLimit", "forced": "forced"}
	for key, value := range in.Extra {
		if key == "is_stopped" {
			continue
		}
		target, ok := fieldNames[key]
		if !ok {
			return nil, commandBoundary("unsupported qB vendor keyword")
		}
		switch key {
		case "is_skip_checking", "is_root_folder", "use_download_path", "add_to_top_of_queue", "forced":
			_, err = commandOptionalBool(value)
		case "rename", "content_layout", "download_path", "stop_condition":
			_, err = commandOptionalString(value)
		}
		if err != nil {
			return nil, err
		}
		if err = commandSet(form, target, value); err != nil {
			return nil, err
		}
	}
	version, err := q.version(ctx)
	if err != nil {
		return nil, err
	}
	modern, err := iyuuVersionAtLeast(version, 2, 7)
	if err != nil {
		return nil, err
	}
	layout, root := in.Extra["content_layout"], in.Extra["is_root_folder"]
	if layout == nil && root != nil && modern {
		if root.(bool) {
			form.Set("contentLayout", "Original")
		} else {
			form.Set("contentLayout", "NoSubfolder")
		}
		form.Del("root_folder")
	}
	if layout != nil && root == nil && !modern {
		if layout == "Subfolder" || layout == "Original" {
			form.Set("root_folder", "True")
		} else {
			form.Set("root_folder", "False")
		}
		form.Del("contentLayout")
	}
	if in.Extra["use_download_path"] == nil && in.Extra["download_path"] != nil {
		form.Set("useDownloadPath", "True")
	}
	payload, contentType := form.Encode(), "application/x-www-form-urlencoded"
	if len(content) > 0 {
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		keys := make([]string, 0, len(form))
		for key := range form {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			if err = writer.WriteField(key, form.Get(key)); err != nil {
				return nil, err
			}
		}
		part, err := writer.CreatePart(textproto.MIMEHeader{"Content-Disposition": {`form-data; name="torrent__0"; filename="torrent__0"`}})
		if err != nil {
			return nil, err
		}
		if _, err = part.Write(content); err != nil {
			return nil, err
		}
		if err = writer.Close(); err != nil {
			return nil, err
		}
		payload, contentType = body.String(), writer.FormDataContentType()
	}
	raw, err := q.owner.iyuu.client.qbSelected(ctx, http.MethodPost, "torrents/add", payload, contentType)
	if err != nil {
		return nil, err
	}
	var value any
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if err = d.Decode(&value); err != nil {
		return string(raw), nil
	}
	if err = d.Decode(new(any)); err != io.EOF {
		return string(raw), nil
	}
	if object, ok := value.(map[string]any); ok {
		return object, nil
	}
	if value == nil || value == false || value == "" {
		return map[string]any{}, nil
	}
	if n, ok := value.(json.Number); ok {
		if number, err := strconv.ParseFloat(string(n), 64); err == nil && number == 0 {
			return map[string]any{}, nil
		}
	}
	if list, ok := value.([]any); ok && len(list) == 0 {
		return map[string]any{}, nil
	}
	return nil, commandBoundary("qB JSON Dictionary conversion for non-mapping value is pending")
}

func commandTRIDs(value any) (any, error) {
	parse := func(v any) (any, error) {
		switch x := v.(type) {
		case bool:
			return x, nil
		case int:
			if x >= 0 {
				return x, nil
			}
		case int64:
			if x >= 0 {
				return x, nil
			}
		case json.Number:
			if strings.ContainsAny(string(x), ".eE") {
				return nil, commandValueError("Invalid torrent id")
			}
			n, ok := new(big.Int).SetString(string(x), 10)
			if !ok || n.Sign() < 0 {
				return nil, commandValueError("Invalid torrent id")
			}
			if !n.IsInt64() {
				return nil, commandBoundary("large Transmission integer id is pending")
			}
			return json.Number(n.String()), nil
		case string:
			if len(x) == 40 && strings.Trim(x, "0123456789abcdef") == "" {
				return x, nil
			}
		}
		return nil, commandValueError("Invalid torrent id")
	}
	if value == "recently-active" {
		return value, nil
	}
	var values []any
	switch v := value.(type) {
	case nil:
		values = []any{}
	case []any:
		values = v
	case []string:
		for _, item := range v {
			values = append(values, item)
		}
	default:
		values = []any{v}
	}
	if len(values) == 0 {
		return nil, commandValueError("request require ids")
	}
	out := make([]any, len(values))
	for i, v := range values {
		var err error
		out[i], err = parse(v)
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// Source legacy envelope, deliberately no tag and no recursive key aliases.
// Session negotiation alone may repeat 409; selected mutation is a single POST.
func (c *sharedDownloaderCommands) trRequest(ctx context.Context, method string, args map[string]any, selected bool) (map[string]any, error) {
	payload, err := json.Marshal(map[string]any{"method": method, "arguments": args})
	if err != nil {
		return nil, commandBoundary("invalid fixed Transmission arguments")
	}
	if selected {
		ctx = mutationSelected(ctx)
	} else {
		ctx = mutationReadOnly(ctx)
	}
	for attempt := 0; attempt < 3; attempt++ {
		raw, header, status, err := c.iyuu.client.request(ctx, http.MethodPost, c.iyuu.client.base, payload, "application/json", c.iyuu.client.trSession)
		if err != nil {
			return nil, err
		}
		if session := header.Get("X-Transmission-Session-Id"); session != "" {
			if len(session) > 1024 || strings.ContainsAny(session, "\r\n\x00") {
				return nil, failure("rpc", "invalid_session_header")
			}
			c.iyuu.client.trSession = session
		}
		if status == http.StatusConflict && !selected {
			session := header.Get("X-Transmission-Session-Id")
			if session == "" || len(session) > 1024 || strings.ContainsAny(session, "\r\n\x00") {
				return nil, failure("rpc", "invalid_session_header")
			}
			c.iyuu.client.trSession = session
			continue
		}
		if status != http.StatusOK {
			return nil, statusError(status)
		}
		var envelope map[string]any
		d := json.NewDecoder(bytes.NewReader(raw))
		d.UseNumber()
		if d.Decode(&envelope) != nil || d.Decode(new(any)) != io.EOF {
			return nil, failure("rpc", "invalid_json")
		}
		if envelope["result"] != "success" {
			return nil, failure("rpc", "rpc_rejected")
		}
		result, ok := envelope["arguments"].(map[string]any)
		if !ok {
			return nil, failure("rpc", "invalid_arguments_response")
		}
		return result, nil
	}
	return nil, failure("rpc", "session_handshake_limit")
}
func (c *sharedDownloaderCommands) trInit(ctx context.Context) error {
	if c.trReady {
		return nil
	}
	result, err := c.trRequest(ctx, "session-get", map[string]any{}, false)
	if err != nil {
		return err
	}
	c.trSessionInfo = result
	c.trReady = true
	return nil
}
func (t *sharedTRCommands) control(ctx context.Context, method string, ids any) (any, error) {
	return t.owner.run(ctx, func(ctx context.Context) (any, error) {
		selector, err := commandTRIDs(ids)
		if err != nil {
			return nil, err
		}
		args := map[string]any{"ids": selector}
		if method == "torrent-remove" {
			args["delete-local-data"] = false
		}
		if err = t.owner.trInit(ctx); err != nil {
			return nil, err
		}
		_, err = t.owner.trRequest(ctx, method, args, true)
		return nil, err
	})
}
func (t *sharedTRCommands) Start(ctx context.Context, ids any) (any, error) {
	return t.control(ctx, "torrent-start", ids)
}
func (t *sharedTRCommands) Recheck(ctx context.Context, ids any) (any, error) {
	return t.control(ctx, "torrent-verify", ids)
}
func (t *sharedTRCommands) DeleteKeepFiles(ctx context.Context, ids any) (any, error) {
	return t.control(ctx, "torrent-remove", ids)
}
func (t *sharedTRCommands) Add(ctx context.Context, in downloadercommands.TRAddCall) (any, error) {
	return t.owner.run(ctx, func(ctx context.Context) (any, error) {
		args := map[string]any{}
		switch value := in.Content.(type) {
		case nil:
			return nil, commandValueError("add_torrent requires data or a URI.")
		case []byte:
			if len(value) == 0 {
				return nil, commandValueError("Torrent metadata is empty")
			}
			if len(value) > 16<<20 {
				return nil, commandBoundary("material exceeds retained 16MiB bound")
			}
			args["metainfo"] = base64.StdEncoding.EncodeToString(value)
		case string:
			// This layer does not parse/fetch/normalize the remote URL. The
			// selected source branch only distinguishes its explicit scheme.
			scheme := strings.ToLower(strings.SplitN(value, ":", 2)[0])
			if scheme == "file" {
				return nil, commandValueError("support for file:// URL has been removed.")
			}
			if scheme != "magnet" && scheme != "http" && scheme != "https" {
				return nil, commandBoundary("Transmission URL/path input branch is pending")
			}
			args["filename"] = value
		default:
			return nil, commandBoundary("non-bytes/string Transmission material is pending")
		}
		cookies, err := commandOptionalString(in.Cookies)
		if err != nil {
			return nil, err
		}
		if cookies != nil {
			args["cookies"] = *cookies
		}
		path, err := commandOptionalString(in.DownloadDir)
		if err != nil {
			return nil, err
		}
		if path != nil {
			args["download-dir"] = *path
		}
		paused, err := commandOptionalBool(in.Paused)
		if err != nil {
			return nil, err
		}
		if paused != nil {
			args["paused"] = *paused
		}
		if in.Labels != nil {
			var labels []string
			switch value := in.Labels.(type) {
			case string:
				labels = []string{value}
			case []string:
				labels = append([]string{}, value...)
			case []any:
				labels = []string{}
				for _, item := range value {
					s, ok := item.(string)
					if !ok {
						return nil, commandBoundary("non-string Transmission labels are pending")
					}
					labels = append(labels, s)
				}
			default:
				return nil, commandBoundary("non-list Transmission labels are pending")
			}
			args["labels"] = labels
		}
		if err = t.owner.trInit(ctx); err != nil {
			return nil, err
		}
		result, err := t.owner.trRequest(ctx, "torrent-add", args, true)
		if err != nil {
			return nil, err
		}
		item, present := result["torrent-added"]
		if !present {
			item = result["torrent-duplicate"]
		}
		fields, ok := item.(map[string]any)
		if !ok || len(fields) == 0 {
			return nil, failure("rpc", "invalid_torrent_add_response")
		}
		if _, ok := fields["id"]; !ok {
			return nil, failure("rpc", "missing_added_id")
		}
		switch fields["id"].(type) {
		case map[string]any, []any:
			return nil, failure("rpc", "unhashable_added_id")
		}
		return fields, nil
	})
}
