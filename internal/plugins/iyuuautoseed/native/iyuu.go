package download

// IYUU uses fixed native operations and per-call dispatch proofs. It does not
// add generic RPC, file deletion, relocation or an application HTTP endpoint.
import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"light-media/internal/releasemeta/legacyanime"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

type IYUUOptions struct {
	AutoCategory bool
	Sequential   bool
	FirstLast    bool
	APIKey       string
	Sleep        func(context.Context, time.Duration) error
	// Guard is supplied by the trusted owner and runs before every HTTP dispatch,
	// including session negotiation. It is not serialized or worker supplied.
	Guard func(context.Context) error
}
type IYUUClient struct {
	client     *Client
	options    IYUUOptions
	webVersion string
}
type IYUUAddSpec struct {
	Torrent             []byte
	Magnet              string
	SavePath            *string
	Category            *string
	Tags                []string
	SkipChecking        *bool
	IgnoreCategoryCheck bool
}
type IYUUAddResult struct {
	Accepted     bool
	SourceTruthy bool
	AddedIDs     []string
	Hash         string
	Outcome      string
}
type IYUUReceipt struct {
	Accepted bool
	Outcome  string
}

func NewIYUUClient(config Config, options IYUUOptions) (*IYUUClient, error) {
	if strings.ContainsAny(options.APIKey, "\r\n") {
		return nil, failure("iyuu_binding", "invalid_api_key")
	}
	if options.APIKey != "" && config.Kind != "qbittorrent" {
		return nil, failure("iyuu_binding", "api_key_wrong_kind")
	}
	if options.APIKey != "" {
		config.Username = ""
		config.Password = ""
	}
	c, e := New(config)
	if e != nil {
		return nil, e
	}
	c.http.Transport = &mutationDispatchTransport{next: c.http.Transport}
	if options.APIKey != "" {
		c.http.Transport = &iyuuBearer{next: c.http.Transport, origin: c.base.Scheme + "://" + c.base.Host, token: options.APIKey}
	}
	if options.Guard != nil {
		c.http.Transport = &iyuuGuard{next: c.http.Transport, guard: options.Guard}
	}
	if options.Sleep == nil {
		options.Sleep = func(ctx context.Context, d time.Duration) error {
			timer := time.NewTimer(d)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-timer.C:
				return nil
			}
		}
	}
	return &IYUUClient{client: c, options: options}, nil
}

type iyuuGuard struct {
	next  http.RoundTripper
	guard func(context.Context) error
}

func (g *iyuuGuard) RoundTrip(r *http.Request) (*http.Response, error) {
	if err := g.guard(r.Context()); err != nil {
		mutationOwnerDenied(r.Context(), err)
		return nil, failure("iyuu_owner", "revoked")
	}
	return g.next.RoundTrip(r)
}
func (g *iyuuGuard) CloseIdleConnections() {
	if c, ok := g.next.(interface{ CloseIdleConnections() }); ok {
		c.CloseIdleConnections()
	}
}

type iyuuBearer struct {
	next          http.RoundTripper
	origin, token string
}

func (t *iyuuBearer) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Scheme+"://"+r.URL.Host != t.origin {
		mutationOwnerDenied(r.Context(), nil)
		return nil, failure("iyuu_auth", "wrong_origin")
	}
	clone := r.Clone(r.Context())
	clone.Header = r.Header.Clone()
	clone.Header.Set("Authorization", "Bearer "+t.token)
	return t.next.RoundTrip(clone)
}
func (t *iyuuBearer) CloseIdleConnections() {
	if c, ok := t.next.(interface{ CloseIdleConnections() }); ok {
		c.CloseIdleConnections()
	}
}
func (c *IYUUClient) Close() { c.client.http.CloseIdleConnections() }

// Torrents preserves native JSON numbers and fields; only a validated hash list
// or the fixed completed filter can select daemon objects. qB's installed
// library uses POST torrents/info, unlike the main download page's GET reader.
func (c *IYUUClient) Torrents(ctx context.Context, hashes []string, completed bool) (json.RawMessage, error) {
	ctx = mutationReadOnly(ctx)
	for i, h := range hashes {
		v, e := c.client.validHash(h)
		if e != nil {
			return nil, e
		}
		hashes = append([]string{}, hashes...)
		hashes[i] = v
	}
	if e := c.client.nativeLock(ctx); e != nil {
		return nil, e
	}
	defer c.client.mu.Unlock()
	if c.client.config.Kind == "qbittorrent" {
		form := url.Values{}
		if hashes != nil {
			form.Set("hashes", strings.Join(hashes, "|"))
		}
		if completed {
			form.Set("filter", "completed")
		}
		raw, e := c.client.qb(ctx, http.MethodPost, "torrents/info", form.Encode(), "application/x-www-form-urlencoded")
		if e != nil {
			return nil, e
		}
		rows, e := nativeObjectList(raw, "iyuu_torrents")
		if e != nil {
			return nil, e
		}
		if !completed {
			return raw, nil
		}
		return iyuuCompletedQB(rows)
	}
	if e := c.client.trConnect(ctx); e != nil {
		return nil, e
	}
	raw, e := c.client.iyuuTRTorrents(ctx, hashes)
	if e != nil {
		return nil, e
	}
	rows, e := nativeObjectList(raw, "iyuu_torrents")
	if e != nil {
		return nil, e
	}
	out := []map[string]any{}
	for _, row := range rows {
		if e := nativeAliasKeys(row, nativeTRKeys); e != nil {
			return nil, e
		}
		if completed {
			status, ok := row["status"].(json.Number)
			if !ok || status != "6" {
				continue
			}
		}
		if e := iyuuTRProperties(row); e != nil {
			return nil, e
		}
		out = append(out, row)
	}
	return json.Marshal(out)
}

func (c *IYUUClient) AddPaused(ctx context.Context, spec IYUUAddSpec) (out IYUUAddResult, err error) {
	ctx, phase := beginMutation(ctx)
	defer func() { err = phase.finish(err, &out.Outcome) }()
	out = IYUUAddResult{AddedIDs: []string{}, Outcome: "rejected", SourceTruthy: c.client.config.Kind == "qbittorrent"}
	if len(spec.Torrent) == 0 && spec.Magnet == "" {
		return out, nil
	}
	if len(spec.Torrent) > maxResponse || len(spec.Magnet) > maxResponse {
		return out, failure("iyuu_add", "payload_limit")
	}
	if spec.Magnet != "" && !strings.HasPrefix(spec.Magnet, "magnet:") {
		return out, failure("iyuu_add", "unsupported_content_uri")
	}
	if spec.Magnet != "" && len(spec.Torrent) > 0 {
		return out, failure("iyuu_add", "ambiguous_content")
	}
	for _, s := range spec.Tags {
		if !utf8.ValidString(s) {
			return out, failure("iyuu_add", "invalid_utf8_tag")
		}
	}
	if e := c.client.nativeLock(ctx); e != nil {
		return out, e
	}
	defer c.client.mu.Unlock()
	if c.client.config.Kind == "qbittorrent" {
		category := spec.Category
		if _, e := c.webVersionLocked(ctx); e != nil {
			return out, e
		}
		auto := c.options.AutoCategory
		if !spec.IgnoreCategoryCheck {
			if category != nil && *category != "" && auto {
				auto = true
			} else {
				auto = false
				category = nil
			}
		}
		var body bytes.Buffer
		w := multipart.NewWriter(&body)
		field := func(name, value string) error { return w.WriteField(name, value) }
		if spec.Magnet != "" {
			if e := field("urls", spec.Magnet); e != nil {
				return out, e
			}
		}
		if category != nil {
			if e := field("category", *category); e != nil {
				return out, e
			}
		}
		if len(spec.Tags) > 0 {
			if e := field("tags", strings.Join(spec.Tags, ",")); e != nil {
				return out, e
			}
		}
		if spec.SavePath != nil && *spec.SavePath != "" {
			if e := field("savepath", *spec.SavePath); e != nil {
				return out, e
			}
		}
		for _, p := range [][2]string{{"sequentialDownload", iyuuBool(c.options.Sequential)}, {"firstLastPiecePrio", iyuuBool(c.options.FirstLast)}, {"paused", "True"}, {"stopped", "True"}} {
			if e := field(p[0], p[1]); e != nil {
				return out, e
			}
		}
		if spec.SkipChecking != nil {
			if e := field("skip_checking", iyuuBool(*spec.SkipChecking)); e != nil {
				return out, e
			}
		}
		if e := field("autoTMM", iyuuBool(auto)); e != nil {
			return out, e
		}
		if len(spec.Torrent) > 0 {
			header := make(map[string][]string)
			header["Content-Disposition"] = []string{`form-data; name="torrent__0"; filename="torrent__0"`}
			part, e := w.CreatePart(header)
			if e != nil {
				return out, e
			}
			if _, e = part.Write(spec.Torrent); e != nil {
				return out, e
			}
		}
		if e := w.Close(); e != nil {
			return out, e
		}
		raw, e := c.client.qbSelected(ctx, http.MethodPost, "torrents/add", body.String(), w.FormDataContentType())
		if e != nil {
			out.Outcome = "unknown"
			return out, e
		}
		accepted, ids, e := iyuuAddResponse(raw)
		if e != nil {
			out.Outcome = "unknown"
			return out, e
		}
		out.Accepted = accepted
		out.AddedIDs = ids
		if accepted {
			out.Outcome = "confirmed"
		}
		return out, nil
	}
	if e := c.client.trConnect(ctx); e != nil {
		return out, e
	}
	args := map[string]any{"paused": true, "labels": append([]string{}, spec.Tags...)}
	if spec.SavePath != nil {
		args["download_dir"] = *spec.SavePath
	}
	if spec.Magnet != "" {
		args["filename"] = spec.Magnet
	} else {
		args["metainfo"] = base64.StdEncoding.EncodeToString(spec.Torrent)
	}
	values, e := c.client.trSelectedRPC(ctx, "torrent_add", args, c.client.trDialect)
	if e != nil {
		out.Outcome = "unknown"
		return out, e
	}
	var raw json.RawMessage
	if v, ok := values["torrent_added"]; ok {
		raw = v
	} else if v, ok := values["torrent_duplicate"]; ok {
		raw = v
	} else {
		out.Outcome = "unknown"
		return out, failure("iyuu_add", "missing_added_object")
	}
	var row map[string]any
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if d.Decode(&row) != nil || row == nil {
		out.Outcome = "unknown"
		return out, failure("iyuu_add", "invalid_added_object")
	}
	hash, _ := row["hash_string"].(string)
	if hash == "" {
		hash, _ = row["hashString"].(string)
	}
	if _, e := c.client.validHash(hash); e != nil {
		out.Outcome = "unknown"
		return out, failure("iyuu_add", "invalid_added_identity")
	}
	out.Hash = hash
	out.SourceTruthy = true
	out.Accepted = true
	out.Outcome = "confirmed"
	return out, nil
}
func iyuuBool(v bool) string {
	if v {
		return "True"
	}
	return "False"
}
func iyuuAddResponse(raw []byte) (bool, []string, error) {
	ids := []string{}
	v, e := iyuuJSON(raw)
	if e != nil {
		return strings.Contains(string(raw), "Ok"), ids, nil
	}
	if s, ok := v.(string); ok {
		return strings.Contains(s, "Ok"), ids, nil
	}
	if !iyuuTruth(v) {
		return false, ids, nil
	}
	m, ok := v.(map[string]any)
	if !ok {
		return strings.Contains(string(raw), "Ok"), ids, nil
	}
	if rawIDs := m["added_torrent_ids"]; iyuuTruth(rawIDs) {
		switch x := rawIDs.(type) {
		case []any:
			for _, v := range x {
				if iyuuTruth(v) {
					ids = append(ids, iyuuScalarString(v))
				}
			}
		case string:
			for _, r := range x {
				ids = append(ids, string(r))
			}
		case map[string]any:
			keys, e := iyuuAddedIDKeys(raw)
			if e != nil {
				return false, ids, e
			}
			for _, key := range keys {
				if key != "" {
					ids = append(ids, key)
				}
			}
		default:
			return false, ids, failure("iyuu_add", "unsupported_added_ids")
		}
	}
	if len(ids) > 0 || iyuuTruth(m["success_count"]) || iyuuTruth(m["pending_count"]) {
		return true, ids, nil
	}
	b, _ := json.Marshal(v)
	return strings.Contains(string(b), "Ok"), ids, nil
}
func iyuuJSON(raw []byte) (any, error) {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var v any
	if e := d.Decode(&v); e != nil {
		return nil, e
	}
	if d.Decode(new(any)) != io.EOF {
		return nil, errors.New("trailing JSON")
	}
	return v, nil
}
func iyuuTruth(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case string:
		return x != ""
	case json.Number:
		n, e := x.Float64()
		return e == nil && n != 0
	case []any:
		return len(x) > 0
	case map[string]any:
		return len(x) > 0
	default:
		return true
	}
}
func iyuuScalarString(v any) string {
	switch x := v.(type) {
	case nil:
		return "None"
	case bool:
		if x {
			return "True"
		}
		return "False"
	case string:
		return x
	case json.Number:
		if !strings.ContainsAny(string(x), ".eE") {
			return string(x)
		}
		f, e := strconv.ParseFloat(string(x), 64)
		if e != nil {
			return string(x)
		}
		scientific := strconv.FormatFloat(f, 'e', -1, 64)
		i := strings.LastIndexByte(scientific, 'e')
		exponent, _ := strconv.Atoi(scientific[i+1:])
		if exponent >= -4 && exponent < 16 {
			fixed := strconv.FormatFloat(f, 'f', -1, 64)
			if !strings.Contains(fixed, ".") {
				fixed += ".0"
			}
			return fixed
		}
		return scientific
	default:
		b, _ := json.Marshal(v)
		return string(b)
	}
}

// FindByTag preserves the source's nine attempts, each preceded by three
// seconds. The temporary tag is removed only after observing a matching hash.
func (c *IYUUClient) FindByTag(ctx context.Context, tag string) (string, error) {
	hash, err := c.PollByTag(ctx, tag)
	if err == nil && hash != "" {
		_, _ = c.RemoveTag(ctx, hash, tag)
	}
	return hash, err
}

// PollByTag performs reads only. Durable hosts must journal RemoveTag separately
// after recording this observed identity; an unknown cleanup must never resend.
func (c *IYUUClient) PollByTag(ctx context.Context, tag string) (string, error) {
	if c.client.config.Kind != "qbittorrent" || tag == "" || strings.Contains(tag, ",") {
		return "", failure("iyuu_tag", "invalid_selector")
	}
	for i := 1; i < 10; i++ {
		if e := c.options.Sleep(ctx, 3*time.Second); e != nil {
			return "", e
		}
		raw, e := c.Torrents(ctx, nil, false)
		if e != nil {
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			continue
		}
		rows, _ := nativeObjectList(raw, "iyuu_tag")
		for _, row := range rows {
			tags, ok := row["tags"].(string)
			if !ok {
				continue
			}
			found := false
			for _, s := range strings.Split(tags, ",") {
				if legacyanime.Strip(s) == tag {
					found = true
				}
			}
			if !found {
				continue
			}
			hash, ok := row["hash"].(string)
			if !ok {
				return "", nil
			}
			if _, e := c.client.validHash(hash); e != nil {
				return "", e
			}
			return hash, nil
		}
	}
	return "", nil
}
func (c *IYUUClient) RemoveTag(ctx context.Context, hash, tag string) (out IYUUReceipt, err error) {
	ctx, phase := beginMutation(ctx)
	defer func() { err = phase.finish(err, &out.Outcome) }()
	if _, e := c.client.validHash(hash); e != nil {
		return IYUUReceipt{Outcome: "rejected"}, e
	}
	if tag == "" || strings.Contains(tag, ",") {
		return IYUUReceipt{Outcome: "rejected"}, failure("iyuu_tag", "invalid_selector")
	}
	return c.qbMutation(ctx, "removeTags", url.Values{"hashes": {hash}, "tags": {tag}})
}
func (c *IYUUClient) SetCategory(ctx context.Context, hash, category string) (out IYUUReceipt, err error) {
	ctx, phase := beginMutation(ctx)
	defer func() { err = phase.finish(err, &out.Outcome) }()
	hash, e := c.client.validHash(hash)
	if e != nil {
		return IYUUReceipt{Outcome: "rejected"}, e
	}
	return c.qbMutation(ctx, "setCategory", url.Values{"hashes": {hash}, "category": {category}})
}
func (c *IYUUClient) Recheck(ctx context.Context, hashes []string) (out IYUUReceipt, err error) {
	ctx, phase := beginMutation(ctx)
	defer func() { err = phase.finish(err, &out.Outcome) }()
	return c.control(ctx, hashes, false)
}
func (c *IYUUClient) Start(ctx context.Context, hashes []string) (out IYUUReceipt, err error) {
	ctx, phase := beginMutation(ctx)
	defer func() { err = phase.finish(err, &out.Outcome) }()
	return c.control(ctx, hashes, true)
}
func (c *IYUUClient) control(ctx context.Context, hashes []string, start bool) (IYUUReceipt, error) {
	ids := make([]string, len(hashes))
	if len(hashes) == 0 {
		return IYUUReceipt{Outcome: "rejected"}, failure("iyuu_control", "empty_selector")
	}
	for i, h := range hashes {
		v, e := c.client.validHash(h)
		if e != nil {
			return IYUUReceipt{Outcome: "rejected"}, e
		}
		ids[i] = v
	}
	if c.client.config.Kind == "qbittorrent" {
		endpoint := "recheck"
		if start {
			if e := c.client.nativeLock(ctx); e != nil {
				return IYUUReceipt{Outcome: "unknown"}, e
			}
			version, e := c.webVersionLocked(ctx)
			c.client.mu.Unlock()
			if e != nil {
				return IYUUReceipt{Outcome: "rejected"}, e
			}
			modern, e := iyuuVersionAtLeast(version, 2, 11)
			if e != nil {
				return IYUUReceipt{Outcome: "rejected"}, e
			}
			endpoint = "resume"
			if modern {
				endpoint = "start"
			}
		}
		return c.qbMutation(ctx, endpoint, url.Values{"hashes": {strings.Join(ids, "|")}})
	}
	if e := c.client.nativeLock(ctx); e != nil {
		return IYUUReceipt{Outcome: "unknown"}, e
	}
	defer c.client.mu.Unlock()
	if e := c.client.trConnect(ctx); e != nil {
		return IYUUReceipt{Outcome: "rejected"}, e
	}
	method := "torrent_verify"
	if start {
		method = "torrent_start"
	}
	_, e := c.client.trSelectedRPC(ctx, method, map[string]any{"ids": ids}, c.client.trDialect)
	if e != nil {
		return IYUUReceipt{Outcome: "unknown"}, e
	}
	return IYUUReceipt{Accepted: true, Outcome: "confirmed"}, nil
}
func (c *IYUUClient) qbMutation(ctx context.Context, endpoint string, values url.Values) (IYUUReceipt, error) {
	if c.client.config.Kind != "qbittorrent" {
		return IYUUReceipt{Outcome: "rejected"}, failure("iyuu_control", "wrong_kind")
	}
	switch endpoint {
	case "removeTags", "setCategory", "recheck", "start", "resume":
	default:
		return IYUUReceipt{Outcome: "rejected"}, failure("iyuu_control", "unsupported_action")
	}
	if e := c.client.nativeLock(ctx); e != nil {
		return IYUUReceipt{Outcome: "unknown"}, e
	}
	defer c.client.mu.Unlock()
	_, e := c.client.qbSelected(ctx, http.MethodPost, "torrents/"+endpoint, values.Encode(), "application/x-www-form-urlencoded")
	if e != nil {
		return IYUUReceipt{Outcome: "unknown"}, e
	}
	return IYUUReceipt{Accepted: true, Outcome: "confirmed"}, nil
}
