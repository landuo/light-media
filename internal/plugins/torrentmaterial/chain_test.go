package torrentmaterial

import (
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"light-media/internal/ocr"
	"light-media/internal/plugins"
	"light-media/internal/plugins/iyuuautoseed"
	"reflect"
	"strings"
	"testing"
)

func ptr(s string) *string { return &s }
func descriptor(raw string) string {
	return "[" + base64.StdEncoding.EncodeToString([]byte(raw)) + "]https://owned.invalid/ticket"
}
func classOf(err error) string {
	var own *SourceError
	if errors.As(err, &own) {
		return own.Class
	}
	var original *ocr.SourceError
	if errors.As(err, &original) {
		return original.Class
	}
	return ""
}

func TestDownloadTorrentWrapperAndNotification(t *testing.T) {
	ctx := context.Background()
	c := Chain{UserAgent: "owned-default"}
	for _, enclosure := range []*string{nil, ptr("")} {
		got, err := c.DownloadTorrent(ctx, Torrent{Enclosure: enclosure}, MessageContext{})
		if err != nil || got.Content != nil || got.Folder != "" || got.Files == nil || len(got.Files) != 0 {
			t.Fatalf("none: %#v %v", got, err)
		}
	}
	got, err := c.DownloadTorrent(ctx, Torrent{Enclosure: ptr("magnet:owned")}, MessageContext{})
	if err != nil || got.Content == nil || got.Content.Kind != "string" || got.Content.String != "magnet:owned" {
		t.Fatalf("magnet: %#v %v", got, err)
	}
	requests := []MaterialRequest{}
	content := []byte("owned-invalid-bencode-is-still-content")
	c.Material = func(_ context.Context, r MaterialRequest) (MaterialResult, error) {
		requests = append(requests, r)
		return MaterialResult{Content: &plugins.TorrentMaterialContent{Kind: "bytes", Bytes: content}, Folder: "Root", Files: []string{"z", "a"}}, nil
	}
	got, err = c.DownloadTorrent(ctx, Torrent{Enclosure: ptr("https://owned.invalid/direct"), SiteCookie: ptr("owned=1"), SiteUA: ptr(""), SiteProxy: true}, MessageContext{})
	if err != nil || len(requests) != 1 || requests[0].Cookie == nil || *requests[0].Cookie != "owned=1" || requests[0].UserAgent != "owned-default" || !requests[0].CacheInvalid || !requests[0].Proxy || !bytes.Equal(got.Content.Bytes, content) || !reflect.DeepEqual(got.Files, []string{"z", "a"}) {
		t.Fatalf("direct: %#v %#v %v", got, requests, err)
	}
	got.Content.Bytes[0] = 'X'
	if content[0] == 'X' {
		t.Fatal("result aliases helper buffer")
	}
	got, err = c.DownloadTorrent(ctx, Torrent{Enclosure: ptr("[]https://owned.invalid/final"), SiteCookie: ptr("owned=1"), SiteUA: ptr("ua")}, MessageContext{})
	if err != nil || requests[1].Cookie != nil || requests[1].CacheInvalid || requests[1].UserAgent != "ua" {
		t.Fatalf("empty descriptor: %#v %v", requests, err)
	}
	notices := []Notification{}
	c.Notify = func(_ context.Context, n Notification) error { notices = append(notices, n); return nil }
	c.Material = func(context.Context, MaterialRequest) (MaterialResult, error) {
		return MaterialResult{ErrorMessage: "owned failure"}, nil
	}
	got, err = c.DownloadTorrent(ctx, Torrent{Enclosure: ptr("owned")}, MessageContext{Source: ptr("must-not-pass"), UserID: json.RawMessage(`9007199254740993`)})
	if err != nil || got.Content != nil || len(notices) != 1 || notices[0].Source != nil || notices[0].Type != "手动处理" || notices[0].Title != "None 种子下载失败！" || notices[0].Text != "错误信息：owned failure\n站点：None" || string(notices[0].UserID) != "9007199254740993" {
		t.Fatalf("failure: %#v %v", notices, err)
	}
	_, err = c.DownloadTorrent(ctx, Torrent{Enclosure: ptr("owned"), Title: ptr("name"), SiteName: ptr("site")}, MessageContext{Channel: ptr("Telegram"), Source: ptr("owned-source")})
	if err != nil || notices[1].Source == nil || *notices[1].Source != "owned-source" {
		t.Fatalf("channel: %#v %v", notices, err)
	}
	c.Material = func(context.Context, MaterialRequest) (MaterialResult, error) {
		return MaterialResult{Content: &plugins.TorrentMaterialContent{Kind: "string", String: ""}}, nil
	}
	got, err = c.DownloadTorrent(ctx, Torrent{Enclosure: ptr("owned")}, MessageContext{})
	if err != nil || got.Content == nil || got.Content.Kind != "string" || got.Content.String != "" || len(notices) != 2 {
		t.Fatalf("empty string confused with failure: %#v %v", got, err)
	}
	cancelCtx, cancel := context.WithCancel(ctx)
	cancel()
	_, err = (Chain{}).DownloadTorrent(cancelCtx, Torrent{Enclosure: ptr("owned")}, MessageContext{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel: %v", err)
	}
}

func TestDownloadTorrentDescriptorSemantics(t *testing.T) {
	ctx := context.Background()
	var calls []DescriptorRequest
	var materials []MaterialRequest
	response := &DescriptorResponse{Status: 200, Text: "https://owned.invalid/final", Body: []byte(`{"ok":true,"data":{"url":"https://untrusted.invalid/final;x?x=1#f"}}`), ContentType: "application/json"}
	c := Chain{UserAgent: "default", Request: func(_ context.Context, r DescriptorRequest) (*DescriptorResponse, error) {
		calls = append(calls, r)
		return response, nil
	}, Material: func(_ context.Context, r MaterialRequest) (MaterialResult, error) {
		materials = append(materials, r)
		return MaterialResult{Content: &plugins.TorrentMaterialContent{Kind: "string", String: "magnet:result"}}, nil
	}}
	get := `{"method":"get","cookie":true,"proxy":1,"header":{"X-Owned":"yes"},"params":{"id":9007199254740993},"result":"data.url","success":"ok","result_base_url":"https://trusted.invalid/api/"}`
	got, err := c.DownloadTorrent(ctx, Torrent{Enclosure: ptr(descriptor(get)), SiteCookie: ptr("owned=1"), SiteUA: ptr("ua"), SiteProxy: false}, MessageContext{})
	if err != nil || got.Content.String != "magnet:result" || len(calls) != 1 || calls[0].Method != "GET" || calls[0].Cookie == nil || !calls[0].Proxy || calls[0].Headers.Kind != "object" || field(calls[0].Params, "id").Number != "9007199254740993" || materials[0].URL != "https://trusted.invalid/final;x?x=1#f" || materials[0].Cookie != nil || materials[0].Proxy || materials[0].CacheInvalid {
		t.Fatalf("get/result: %#v %#v %v", calls, materials, err)
	}
	_, err = c.DownloadTorrent(ctx, Torrent{Enclosure: ptr(descriptor(`{"method":"GET","header":[],"params":[["x",1]]}`)), SiteCookie: ptr("owned=1")}, MessageContext{})
	if err != nil || calls[1].Method != "POST" || calls[1].Cookie != nil || calls[1].Headers.Kind != "null" || calls[1].Params.Kind != "list" || materials[1].URL != response.Text {
		t.Fatalf("POST/text: %#v %v", calls, err)
	}
	response.Body = []byte(`{"data":{"url":["a b",2]}}`)
	_, err = c.DownloadTorrent(ctx, Torrent{Enclosure: ptr(descriptor(`{"result":"data.url","result_path":"//download","result_query_param":"token","result_base_url":"https://trusted.invalid/api/"}`))}, MessageContext{})
	if err != nil || materials[2].URL != "https://trusted.invalid/api/download?token=%5B%27a+b%27%2C+2%5D" {
		t.Fatalf("query no doseq: %#v %v", materials, err)
	}
	// requests.Response truthiness only rejects 400..599, not status 600.
	response.Status = 600
	response.Text = "https://owned.invalid/600"
	_, err = c.DownloadTorrent(ctx, Torrent{Enclosure: ptr(descriptor(`{}`))}, MessageContext{})
	if err != nil || materials[3].URL != response.Text {
		t.Fatalf("600 truth: %#v %v", materials, err)
	}
	response.Status = 503
	before := len(materials)
	got, err = c.DownloadTorrent(ctx, Torrent{Enclosure: ptr(descriptor(`{}`))}, MessageContext{})
	if err != nil || got.Content != nil || len(materials) != before {
		t.Fatalf("503 false: %#v %v", got, err)
	}
	response.Status = 200
	for _, tc := range []struct{ name, params, body, class string }{
		{"nonobject", "[]", `{}`, "AttributeError"},
		{"invalidJSON", "{", `{}`, "JSONDecodeError"},
		{"nestedNonobject", `{"result":"data.url"}`, `{"data":true}`, "AttributeError"},
		{"urlNotString", `{"result":"data"}`, `{"data":123}`, "AttributeError"},
		{"unhashableSuccess", `{"result":"data","success":[1]}`, `{"data":"x"}`, "TypeError"},
		{"unhashableQuery", `{"result":"data","result_path":"x","result_query_param":[1]}`, `{"data":"x"}`, "TypeError"},
		{"rawJSONError", `{"result":"data"}`, `not-json`, "JSONDecodeError"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response.Body = []byte(tc.body)
			_, err := c.DownloadTorrent(ctx, Torrent{Enclosure: ptr(descriptor(tc.params))}, MessageContext{})
			if classOf(err) != tc.class {
				t.Fatalf("class %q error %v", classOf(err), err)
			}
		})
	}
	for _, encoded := range []string{"/w==", "ew"} {
		_, err = c.DownloadTorrent(ctx, Torrent{Enclosure: ptr("[" + encoded + "]owned")}, MessageContext{})
		want := "Error"
		if encoded == "/w==" {
			want = "UnicodeDecodeError"
		}
		if classOf(err) != want {
			t.Fatalf("descriptor encoding %s: %v", encoded, err)
		}
	}
	// The descriptor decoder is lenient about nonalphabet bytes/excess padding,
	// but strictly decodes its resulting UTF-8. Response JSON has its own codec.
	decoded, err := decodeDescriptorBase64([]byte("!e3!0====ignored"))
	if err != nil || string(decoded) != "{}" {
		t.Fatalf("base64: %q %v", decoded, err)
	}
	response.Body = []byte{0xff, 0xfe, '{', 0, '"', 0, 'x', 0, '"', 0, ':', 0, '"', 0, '/', 0, 'o', 0, 'k', 0, '"', 0, '}', 0}
	response.ContentType = ""
	_, err = c.DownloadTorrent(ctx, Torrent{Enclosure: ptr(descriptor(`{"result":"x","result_base_url":"https://owned.invalid/base"}`))}, MessageContext{})
	if err != nil || materials[len(materials)-1].URL != "https://owned.invalid/ok" {
		t.Fatalf("response UTF16 separate: %#v %v", materials, err)
	}
	for _, tc := range []struct{ address, base, want string }{
		{"../d;p?x=1#f", "https://owned.invalid/a/b/", "https://owned.invalid/a/d;p?x=1#f"},
		{"//other.invalid/d", "https://owned.invalid/api", "https://owned.invalid/d"},
		{"https://other.invalid/d", "relative", "https://other.invalid/d"},
	} {
		got, err := iyuuautoseed.NormalizeTorrentMaterialURL(tc.address, tc.base)
		if err != nil || got != tc.want {
			t.Fatalf("normalize: %q %v", got, err)
		}
	}
}

type ownedHelperPorts struct {
	rows    []*iyuuautoseed.TorrentResponse
	gets    int
	cache   map[string][]byte
	invalid []string
}

func (h *ownedHelperPorts) Get(_ context.Context, _ string) (*iyuuautoseed.TorrentResponse, error) {
	h.gets++
	r := h.rows[0]
	h.rows = h.rows[1:]
	return r, nil
}
func (h *ownedHelperPorts) Post(context.Context, string, map[string]string) (*iyuuautoseed.TorrentResponse, error) {
	return nil, errors.New("unexpected owned POST")
}
func (h *ownedHelperPorts) GetTorrent(_ context.Context, key string) ([]byte, error) {
	return bytes.Clone(h.cache[key]), nil
}
func (h *ownedHelperPorts) PutTorrent(_ context.Context, key string, b []byte) error {
	h.cache[key] = bytes.Clone(b)
	return nil
}
func (h *ownedHelperPorts) AddInvalid(_ context.Context, address string) error {
	h.invalid = append(h.invalid, address)
	return nil
}

func TestDownloadTorrentExistingHelperCacheAndRecovery(t *testing.T) {
	// Actual existing Go helper+parser is exercised; its HTTP/cache ports are
	// owned in-memory components, not a claim about real HTTP or cold disk cache.
	raw := []byte("d4:infod6:lengthi1e4:name9:owned.mkvee")
	ports := &ownedHelperPorts{rows: []*iyuuautoseed.TorrentResponse{{Status: 503}, {Status: 200, Content: raw}, {Status: 200, Content: []byte("bad-bencode")}}, cache: map[string][]byte{}}
	notices := 0
	c := Chain{Material: func(ctx context.Context, r MaterialRequest) (MaterialResult, error) {
		out, err := (iyuuautoseed.TorrentHelper{HTTP: ports, Cache: ports}).Download(ctx, r.URL, r.CacheInvalid)
		return FromHelper(out), err
	}, Notify: func(context.Context, Notification) error { notices++; return nil }}
	ctx := context.Background()
	torrent := Torrent{Enclosure: ptr("https://owned.invalid/torrent")}
	got, err := c.DownloadTorrent(ctx, torrent, MessageContext{})
	if err != nil || got.Content != nil || notices != 1 || len(ports.invalid) != 1 {
		t.Fatalf("failure: %#v %v", got, err)
	}
	got, err = c.DownloadTorrent(ctx, torrent, MessageContext{})
	if err != nil || got.Content == nil || !bytes.Equal(got.Content.Bytes, raw) || !reflect.DeepEqual(got.Files, []string{"owned.mkv"}) || ports.gets != 2 || len(ports.cache) != 1 {
		t.Fatalf("recovery: %#v %v gets=%d cache=%d", got, err, ports.gets, len(ports.cache))
	}
	got, err = c.DownloadTorrent(ctx, torrent, MessageContext{})
	if err != nil || ports.gets != 2 || !bytes.Equal(got.Content.Bytes, raw) {
		t.Fatalf("cache: %#v %v gets=%d", got, err, ports.gets)
	}
	got, err = c.DownloadTorrent(ctx, Torrent{Enclosure: ptr("https://owned.invalid/invalid")}, MessageContext{})
	if err != nil || got.Content == nil || string(got.Content.Bytes) != "bad-bencode" || got.Folder != "" || len(got.Files) != 0 || len(ports.cache) != 1 {
		t.Fatalf("not strict material rejection: %#v %v", got, err)
	}
	ports.rows = []*iyuuautoseed.TorrentResponse{{Status: 503}}
	c.Request = func(context.Context, DescriptorRequest) (*DescriptorResponse, error) {
		return &DescriptorResponse{Status: 200, Text: "https://owned.invalid/indirect"}, nil
	}
	_, err = c.DownloadTorrent(ctx, Torrent{Enclosure: ptr(descriptor(`{}`))}, MessageContext{})
	if err != nil || len(ports.invalid) != 1 || notices != 2 {
		t.Fatalf("indirect no invalid cache: %v %#v", err, ports.invalid)
	}
	if strings.Contains(ports.invalid[0], "indirect") {
		t.Fatal("indirect invalid inserted")
	}
}

//go:embed testdata/source-wrapper.json
var sourceWrapperJSON []byte

type sourceWrapperObservation struct {
	Case   string `json:"case"`
	Inputs struct {
		Torrent struct {
			Enclosure *string `json:"enclosure"`
			Title     *string `json:"title"`
			SiteName  *string `json:"site_name"`
			Cookie    *string `json:"site_cookie"`
			UA        *string `json:"site_ua"`
			Proxy     bool    `json:"site_proxy"`
		} `json:"torrent"`
		Channel  *string         `json:"channel"`
		Source   *string         `json:"source"`
		UserID   json.RawMessage `json:"userid"`
		Response struct {
			Truthy bool            `json:"truthy"`
			Text   string          `json:"text"`
			JSON   json.RawMessage `json:"json"`
		} `json:"response"`
		Helper struct {
			Content json.RawMessage `json:"content"`
			Folder  string          `json:"folder"`
			Files   []string        `json:"files"`
			Error   string          `json:"error"`
		} `json:"helper_result"`
	} `json:"inputs"`
	Outcome struct {
		Value     json.RawMessage `json:"value"`
		Exception *struct {
			Type string `json:"type"`
		} `json:"exception"`
	} `json:"outcome"`
	Trace   []map[string]any `json:"trace"`
	Notices []any            `json:"notices"`
}

func sourceJSONAny(t *testing.T, raw []byte) any {
	t.Helper()
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var value any
	if err := d.Decode(&value); err != nil {
		t.Fatal(err)
	}
	return value
}
func sourceComparable(t *testing.T, value any) any {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return sourceJSONAny(t, raw)
}
func sourcePlainValue(t *testing.T, value Value) any {
	t.Helper()
	switch value.Kind {
	case "null":
		return nil
	case "bool":
		return value.Bool
	case "string":
		for _, p := range value.Points {
			if p >= 0xd800 && p <= 0xdfff {
				t.Fatal("19-case oracle has no surrogate adapter")
			}
		}
		return string(value.Points)
	case "int", "float":
		return json.Number(value.Number)
	case "list":
		out := []any{}
		for _, v := range value.Items {
			out = append(out, sourcePlainValue(t, v))
		}
		return out
	case "object":
		out := map[string]any{}
		for _, v := range value.Entries {
			out[string(v.Key)] = sourcePlainValue(t, v.Value)
		}
		return out
	default:
		t.Fatalf("unexpected envelope %q", value.Kind)
		return nil
	}
}
func sourceUnwrapEnums(value any) any {
	switch value := value.(type) {
	case map[string]any:
		if _, ok := value["$enum"]; ok {
			return value["value"]
		}
		out := map[string]any{}
		for k, v := range value {
			out[k] = sourceUnwrapEnums(v)
		}
		return out
	case []any:
		out := []any{}
		for _, v := range value {
			out = append(out, sourceUnwrapEnums(v))
		}
		return out
	default:
		return value
	}
}
func sourcePortProjection(t *testing.T, trace []map[string]any) []any {
	t.Helper()
	out := []any{}
	var init map[string]any
	for _, event := range trace {
		switch event["op"] {
		case "request.init":
			if init != nil {
				t.Fatal("unconsumed request init")
			}
			init = event
		case "request.get_res", "request.post_res":
			if init == nil {
				t.Fatal("request without source init")
			}
			method := "GET"
			if event["op"] == "request.post_res" {
				method = "POST"
			}
			out = append(out, map[string]any{"op": "descriptor", "method": method, "url": event["url"], "ua": init["ua"], "cookie": init["cookies"], "headers": init["headers"], "proxies": init["proxies"], "params": event["params"]})
			init = nil
		case "torrent_helper.download_torrent":
			out = append(out, map[string]any{"op": "material", "url": event["url"], "cookie": event["cookie"], "ua": event["ua"], "proxy": event["proxy"], "cache_invalid": event["cache_invalid"]})
		case "notice.post_message":
			fields := map[string]any{}
			for k, v := range event {
				if k != "op" {
					fields[k] = sourceUnwrapEnums(v)
				}
			}
			out = append(out, map[string]any{"op": "notice", "fields": fields})
		case "response.bool", "response.text", "response.json", "torrent_helper.init", "notification.init", "log.info", "log.error":
			// These are retained in the full oracle but are not target port calls.
		default:
			t.Fatalf("unknown source event %v", event["op"])
		}
	}
	if init != nil {
		t.Fatal("unconsumed request init")
	}
	return out
}

func TestDownloadTorrentActualSourceWrapper19(t *testing.T) {
	// Selected original methods really ran. Their RequestUtils, helper, model
	// and notification dependencies were recording components, not real HTTP.
	const fixtureSHA = "133bf97c1df6958a62f341b036936519d78fda928cb836eb8e23139f1fffc6bc"
	if fmt.Sprintf("%x", sha256.Sum256(sourceWrapperJSON)) != fixtureSHA {
		t.Fatal("source fixture binding changed")
	}
	var document struct {
		RawSHA   string `json:"raw_sha256"`
		Source   string `json:"source_commit"`
		Settings struct {
			UA    string            `json:"USER_AGENT"`
			Proxy map[string]string `json:"PROXY"`
		} `json:"settings_input"`
		Cases []sourceWrapperObservation `json:"cases"`
	}
	d := json.NewDecoder(bytes.NewReader(sourceWrapperJSON))
	d.UseNumber()
	if err := d.Decode(&document); err != nil {
		t.Fatal(err)
	}
	if document.RawSHA != "6e654f82a7e3099eadcde59b38ab9b12c7a8a6b2cc5e1e3909a4a7e1d528e5c6" || document.Source != "8a83efeacd3e26b82be495e17d50146e31c96a60" || len(document.Cases) != 19 {
		t.Fatal("original observation identity/count")
	}
	for _, observed := range document.Cases {
		t.Run(observed.Case, func(t *testing.T) {
			input := observed.Inputs
			events := []any{}
			notices := []any{}
			c := Chain{UserAgent: document.Settings.UA}
			c.Request = func(_ context.Context, req DescriptorRequest) (*DescriptorResponse, error) {
				var proxy any
				if req.Proxy {
					proxy = document.Settings.Proxy
				}
				events = append(events, map[string]any{"op": "descriptor", "method": req.Method, "url": req.URL, "ua": req.UserAgent, "cookie": req.Cookie, "headers": sourcePlainValue(t, req.Headers), "proxies": proxy, "params": sourcePlainValue(t, req.Params)})
				status := 200
				if !input.Response.Truthy {
					status = 503
				} // Only owned response bool is projected, not an observed HTTP status.
				return &DescriptorResponse{Status: status, Text: input.Response.Text, Body: bytes.Clone(input.Response.JSON), ContentType: "application/json"}, nil
			}
			c.Material = func(_ context.Context, req MaterialRequest) (MaterialResult, error) {
				events = append(events, map[string]any{"op": "material", "url": req.URL, "cookie": req.Cookie, "ua": req.UserAgent, "proxy": req.Proxy, "cache_invalid": req.CacheInvalid})
				out := MaterialResult{Folder: input.Helper.Folder, Files: append([]string{}, input.Helper.Files...), ErrorMessage: input.Helper.Error}
				switch v := sourceJSONAny(t, input.Helper.Content).(type) {
				case nil:
				case string:
					out.Content = &plugins.TorrentMaterialContent{Kind: "string", String: v}
				case map[string]any:
					if len(v) != 1 {
						t.Fatal("non-byte helper marker")
					}
					encoded, ok := v["$bytes"].(string)
					if !ok {
						t.Fatal("missing byte marker")
					}
					raw, err := base64.StdEncoding.DecodeString(encoded)
					if err != nil {
						t.Fatal(err)
					}
					out.Content = &plugins.TorrentMaterialContent{Kind: "bytes", Bytes: raw}
				default:
					t.Fatalf("unhandled helper input %T", v)
				}
				return out, nil
			}
			c.Notify = func(_ context.Context, n Notification) error {
				fields := map[string]any{"channel": n.Channel, "source": n.Source, "userid": sourceJSONAny(t, n.UserID), "mtype": n.Type, "title": n.Title, "text": n.Text}
				notices = append(notices, fields)
				events = append(events, map[string]any{"op": "notice", "fields": fields})
				return nil
			}
			v := input.Torrent
			got, err := c.DownloadTorrent(context.Background(), Torrent{Enclosure: v.Enclosure, Title: v.Title, SiteName: v.SiteName, SiteCookie: v.Cookie, SiteUA: v.UA, SiteProxy: v.Proxy}, MessageContext{Channel: input.Channel, Source: input.Source, UserID: input.UserID})
			if observed.Outcome.Exception != nil {
				if classOf(err) != observed.Outcome.Exception.Type {
					t.Fatalf("source error %s target %v", observed.Outcome.Exception.Type, err)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				var content any
				if got.Content != nil {
					switch got.Content.Kind {
					case "string":
						content = got.Content.String
					case "bytes":
						content = map[string]any{"$bytes": base64.StdEncoding.EncodeToString(got.Content.Bytes)}
					default:
						t.Fatal("unknown content kind")
					}
				}
				actual := sourceComparable(t, []any{content, got.Folder, got.Files})
				expected := sourceJSONAny(t, observed.Outcome.Value)
				if !reflect.DeepEqual(actual, expected) {
					t.Fatalf("source tuple %#v target %#v", expected, actual)
				}
			}
			if actual, expected := sourceComparable(t, events), sourceComparable(t, sourcePortProjection(t, observed.Trace)); !reflect.DeepEqual(actual, expected) {
				t.Fatalf("ordered source ports %#v target %#v", expected, actual)
			}
			if actual, expected := sourceComparable(t, notices), sourceComparable(t, sourceUnwrapEnums(observed.Notices)); !reflect.DeepEqual(actual, expected) {
				t.Fatalf("source notices %#v target %#v", expected, actual)
			}
		})
	}
}

func TestDownloadTorrentActualSourceLogProjection(t *testing.T) {
	var doc struct {
		Cases []sourceWrapperObservation `json:"cases"`
	}
	if err := json.Unmarshal(sourceWrapperJSON, &doc); err != nil {
		t.Fatal(err)
	}
	selected := map[string]bool{"unmatched_bracket_stops_before_helper": true, "empty_bytes_notice_with_channel": true, "nested_relative_rewrite_ignores_lone_result_path": true}
	for _, observation := range doc.Cases {
		if !selected[observation.Case] {
			continue
		}
		t.Run(observation.Case, func(t *testing.T) {
			actual := []any{}
			expected := []any{}
			for _, event := range observation.Trace {
				if event["op"] == "log.info" || event["op"] == "log.error" {
					expected = append(expected, map[string]any{"level": strings.ToUpper(strings.TrimPrefix(event["op"].(string), "log.")), "message": event["message"]})
				}
			}
			input := observation.Inputs
			c := Chain{Request: func(context.Context, DescriptorRequest) (*DescriptorResponse, error) {
				return &DescriptorResponse{Status: 200, Text: input.Response.Text, Body: input.Response.JSON, ContentType: "application/json"}, nil
			}, Material: func(context.Context, MaterialRequest) (MaterialResult, error) {
				if observation.Case == "empty_bytes_notice_with_channel" {
					return MaterialResult{Content: &plugins.TorrentMaterialContent{Kind: "bytes", Bytes: []byte{}}, ErrorMessage: input.Helper.Error}, nil
				}
				return MaterialResult{Content: &plugins.TorrentMaterialContent{Kind: "string", String: "owned"}}, nil
			}, Notify: func(context.Context, Notification) error { return nil }, Log: func(_ context.Context, level, message string) {
				actual = append(actual, map[string]any{"level": level, "message": message})
			}}
			v := input.Torrent
			_, err := c.DownloadTorrent(context.Background(), Torrent{Enclosure: v.Enclosure, Title: v.Title, SiteName: v.SiteName, SiteCookie: v.Cookie, SiteUA: v.UA, SiteProxy: v.Proxy}, MessageContext{Channel: input.Channel, Source: input.Source, UserID: input.UserID})
			if err != nil || !reflect.DeepEqual(actual, expected) {
				t.Fatalf("source logs %#v target %#v error %v", expected, actual, err)
			}
		})
	}
}

func TestDownloadTorrentLazyResponseTextBoundary(t *testing.T) {
	unavailable := errors.New("owned explicit text decoder boundary")
	for _, tc := range []struct {
		name, params  string
		status        int
		wantError     bool
		materialCalls int
	}{
		{"false-response", `{}`, 503, false, 0},
		{"json-does-not-read-text", `{"result":"url"}`, 200, false, 1},
		{"text-propagates-boundary", `{}`, 200, true, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			c := Chain{Request: func(context.Context, DescriptorRequest) (*DescriptorResponse, error) {
				return &DescriptorResponse{Status: tc.status, TextError: unavailable, Body: []byte(`{"url":"https://owned.invalid/material"}`), ContentType: "application/json"}, nil
			}, Material: func(context.Context, MaterialRequest) (MaterialResult, error) {
				calls++
				return MaterialResult{Content: &plugins.TorrentMaterialContent{Kind: "bytes", Bytes: []byte("owned")}}, nil
			}}
			_, err := c.DownloadTorrent(context.Background(), Torrent{Enclosure: ptr(descriptor(tc.params))}, MessageContext{})
			if tc.wantError {
				if !errors.Is(err, unavailable) {
					t.Fatal(err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if calls != tc.materialCalls {
				t.Fatalf("material calls %d want%d", calls, tc.materialCalls)
			}
			if classOf(err) != "" {
				t.Fatal("target decoder boundary must not masquerade as a source exception")
			}
		})
	}
}

//go:embed testdata/source-nonstring-log.json
var sourceNonstringLogJSON []byte

func TestDownloadTorrentActualSourceNonstringLogOrder(t *testing.T) {
	if fmt.Sprintf("%x", sha256.Sum256(sourceNonstringLogJSON)) != "e1ec49d67d042fc45e960b60645f12ffc3e32d1f451e69ceb9fcc8a55b048991" {
		t.Fatal("original non-string log observation bytes changed")
	}
	var doc struct {
		Source string                     `json:"source_commit"`
		Cases  []sourceWrapperObservation `json:"cases"`
	}
	if err := json.Unmarshal(sourceNonstringLogJSON, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Source != "8a83efeacd3e26b82be495e17d50146e31c96a60" || len(doc.Cases) != 3 {
		t.Fatal("source observation identity/count")
	}
	for _, observation := range doc.Cases {
		t.Run(observation.Case, func(t *testing.T) {
			expected, actual := []any{}, []any{}
			for _, event := range observation.Trace {
				if event["op"] == "log.info" || event["op"] == "log.error" {
					expected = append(expected, map[string]any{"level": strings.ToUpper(strings.TrimPrefix(event["op"].(string), "log.")), "message": event["message"]})
				}
			}
			input := observation.Inputs
			requests, material, notices := 0, 0, 0
			c := Chain{Request: func(_ context.Context, request DescriptorRequest) (*DescriptorResponse, error) {
				requests++
				if request.Method != "GET" || request.URL != "https://owned-descriptor.invalid/token" || request.Cookie != nil {
					t.Fatalf("unexpected descriptor preparation %#v", request)
				}
				return &DescriptorResponse{Status: 200, Text: input.Response.Text, Body: input.Response.JSON, ContentType: "application/json"}, nil
			}, Material: func(context.Context, MaterialRequest) (MaterialResult, error) {
				material++
				return MaterialResult{}, errors.New("non-string URL must not reach the typed network/cache port")
			}, Notify: func(context.Context, Notification) error {
				notices++
				return nil
			}, Log: func(_ context.Context, level, message string) {
				actual = append(actual, map[string]any{"level": level, "message": message})
			}}
			v := input.Torrent
			_, err := c.DownloadTorrent(context.Background(), Torrent{Enclosure: v.Enclosure, Title: v.Title, SiteName: v.SiteName, SiteCookie: v.Cookie, SiteUA: v.UA, SiteProxy: v.Proxy}, MessageContext{Channel: input.Channel, Source: input.Source, UserID: input.UserID})
			if observation.Outcome.Exception == nil || classOf(err) != observation.Outcome.Exception.Type || !reflect.DeepEqual(actual, expected) {
				t.Fatalf("source exception/logs %#v/%#v target %v/%#v", observation.Outcome.Exception, expected, err, actual)
			}
			// This is the original exception class and log sequence projection.
			// Source enters helper.startswith for two cases; target classifies
			// before its string-only port. It is not identical helper-call tracing.
			if requests != 1 || material != 0 || notices != len(observation.Notices) {
				t.Fatalf("unexpected ports request=%d material=%d notice=%d", requests, material, notices)
			}
		})
	}
}
