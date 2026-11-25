package autosignin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	sdk "light-media/builtinplugins/internal/sdk"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"
)

const refinementRoot = "testdata/source-oracles/autosignin-source-edge-refinement-20261106/"

func refinementPoints(s string) []int {
	out := []int{}
	for _, r := range sourceCodepoints(s) {
		out = append(out, int(r))
	}
	return out
}
func TestRefinementActualSourceDoAndJSONPersistence(t *testing.T) {
	raw, e := os.ReadFile(refinementRoot + "source-chain-r2.json")
	if e != nil {
		t.Fatal(e)
	}
	var f struct {
		Rows []struct {
			Text          string
			DoError       *string `json:"do_error"`
			MessagePoints [][]int `json:"message_points"`
			DBRows        []struct {
				Key, StorageText string `json:"-"`
			} `json:"db_rows"`
		}
	}
	if e = Decode(raw, &f); e != nil {
		t.Fatal(e)
	}
	for i, c := range f.Rows {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			if c.DoError != nil {
				t.Fatal(*c.DoError)
			}
			now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
			site := Object{"id": 1, "name": "Owned", "url": "https://yemapt.org/", "public": false, "cookie": "owned", "ua": "Owned/1"}
			engine := New(Inventory{Indexers: []Object{site}}, func() time.Time { return now })
			engine.SchemaLoaded = true
			engine.RetryKeyword = "never-match"
			adapter, e := FixedSiteAdapter(AdapterYemaPT)
			if e != nil {
				t.Fatal(e)
			}
			calls := 0
			visitor := SiteVisitor{Adapters: []SiteAdapter{adapter}, Hooks: SiteVisitHooks{HTTP: edgeHTTPFunction(func(context.Context, PageRequest) (*PageResponse, error) {
				calls++
				return &PageResponse{Status: 200, Text: c.Text}, nil
			}), Now: func() time.Time { return now }, Stats: func(context.Context, string, bool, int) error { return nil }}}
			writes := map[string][]byte{}
			engine.Effects = func(effect Effect) error {
				if effect.Kind == "history.put" {
					raw, e := sourceMarshalBusinessJSON(effect.Value)
					if e != nil {
						return e
					}
					writes[effect.Key] = raw
				}
				return nil
			}
			if e = engine.Do(context.Background(), now, "签到", []any{"all"}, nil, nil, RunHooks{Visit: visitor.Visit}); e != nil {
				t.Fatal(e)
			}
			if calls != 2 || len(writes) != 2 {
				t.Fatalf("source2HTTP/2JSON rows got%d/%d", calls, len(writes))
			}
			detail := engine.Data["11月6日"].([]any)[0].(Object)
			if got := refinementPoints(detail["status"].(string)); !reflect.DeepEqual(got, c.MessagePoints[0]) {
				t.Fatalf("current message=%v source=%v", got, c.MessagePoints[0])
			}
			saved, e := sourceRestoreSavedJSON(writes["11月6日"])
			if e != nil {
				t.Fatal(e)
			}
			message := saved.([]any)[0].(Object)["status"].(string)
			if !reflect.DeepEqual(refinementPoints(message), c.MessagePoints[0]) {
				t.Fatalf("strict saved string codepoints=%v source=%v", refinementPoints(message), c.MessagePoints[0])
			}
			if !json.Valid(writes["11月6日"]) || !utf8.Valid(writes["11月6日"]) {
				t.Fatal("illegal JSON/UTF8 crossed persistence boundary")
			}
		})
	}
}
func TestRefinementActualSourceHistoryLazyBranchAndLateFailure(t *testing.T) {
	raw, e := os.ReadFile(refinementRoot + "source-lazy-history-r1.json")
	if e != nil {
		t.Fatal(e)
	}
	var f struct {
		Rows []struct {
			History Object
			Error   *struct{ Kind, Message string }
			Effects []string
			Keys    []string
		}
	}
	if e = Decode(raw, &f); e != nil {
		t.Fatal(e)
	}
	for i, c := range f.Rows {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
			site := Object{"id": 1, "name": "Owned", "url": "https://yemapt.org/", "public": false, "cookie": "owned", "ua": "Owned/1"}
			engine := New(Inventory{Indexers: []Object{site}}, func() time.Time { return now })
			engine.SchemaLoaded = true
			engine.RetryKeyword = "never-match"
			engine.Data["签到-2026-11-06"] = c.History
			calls := 0
			adapter, _ := FixedSiteAdapter(AdapterYemaPT)
			visitor := SiteVisitor{Adapters: []SiteAdapter{adapter}, Hooks: SiteVisitHooks{HTTP: edgeHTTPFunction(func(context.Context, PageRequest) (*PageResponse, error) {
				calls++
				return &PageResponse{Status: 200, Text: `{"success":false,"errorMessage":"owned-failure"}`}, nil
			}), Now: func() time.Time { return now }, Stats: func(context.Context, string, bool, int) error { return nil }}}
			err := engine.Do(context.Background(), now, "签到", []any{"all"}, nil, nil, RunHooks{Visit: visitor.Visit})
			if c.Error != nil {
				if err == nil || err.Error() != c.Error.Message {
					t.Fatalf("got%v want%s:%s", err, c.Error.Kind, c.Error.Message)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			wantCalls := 0
			for _, s := range c.Effects {
				if s == "HTTP" {
					wantCalls++
				}
			}
			if calls != wantCalls {
				t.Fatalf("source short circuit HTTP=%d got%d", wantCalls, calls)
			}
			if len(engine.Data) != len(c.Keys) {
				t.Fatalf("source keys=%v target=%v", c.Keys, engine.Data)
			}
		})
	}
}
func TestRefinementActualSDKRawKVSurrogateRoundTrip(t *testing.T) {
	// Actual production handler/independent worker/SDK framing. Resource peer is
	// synthetic; this is not App SQLite/Main or a real website.
	for _, code := range []string{"d800", "dfff", "fffd"} {
		t.Run(code, func(t *testing.T) {
			p := startRuntimePeer(t, 1)
			p.siteURL = "https://yemapt.org/"
			config := runtimeConfig()
			config["retry_keyword"] = "never-match"
			config["enabled"] = true
			p.must("init", config)
			var mu sync.Mutex
			network := 0
			var host sdk.HostHandler
			host = func(ctx context.Context, id sdk.Identity, method string, raw json.RawMessage) (json.RawMessage, error) {
				if method == sdk.BackgroundSubmitCapability {
					var in struct {
						Method  string
						Payload json.RawMessage
					}
					if e := json.Unmarshal(raw, &in); e != nil {
						return nil, e
					}
					pending, e := p.client.StartCallWithAdmission(ctx, p.ctx, in.Method, in.Payload, host, sdk.CallOptions{CallerDeadlineOnly: true})
					if e != nil {
						return nil, e
					}
					go func() {
						out, e := pending.Wait()
						p.mu.Lock()
						p.completed = append(p.completed, out)
						p.failures = append(p.failures, e)
						p.mu.Unlock()
					}()
					return json.RawMessage(`{"accepted":true}`), nil
				}
				if method == sdk.SitesSourceRequestCapability {
					mu.Lock()
					network++
					mu.Unlock()
					return json.Marshal(sdk.SiteHTTPResultV1{SchemaVersion: 1, RequestState: "completed", Response: &sdk.SiteHTTPResponseV1{StatusCode: 200, Content: []byte(`{"success":false,"errorMessage":"\u` + code + `"}`), ContentType: "application/json; charset=utf-8", Cookies: []sdk.SiteHTTPPairV1{}}})
				}
				return p.host(ctx, id, method, raw)
			}
			out, e := p.client.CallWithOptions(p.ctx, "sign_in", json.RawMessage(`null`), host, sdk.CallOptions{CallerDeadlineOnly: true})
			if e != nil {
				t.Fatal(e)
			}
			if out.HostCalls > 64 {
				t.Fatal("raised perincoming budget")
			}
			p.mu.Lock()
			saved := map[string]json.RawMessage{}
			for k, v := range p.data {
				saved[k] = append(json.RawMessage(nil), v...)
			}
			p.mu.Unlock()
			if len(saved) != 2 {
				t.Fatalf("not2saved rows: %v", saved)
			}
			for k, v := range saved {
				if !json.Valid(v) || !utf8.Valid(v) || code != "fffd" && !bytes.Contains(v, []byte(`\u`+code)) {
					t.Fatalf("rawKV lost original codepoint key=%s JSON=%s", k, v)
				}
				if code != "fffd" && (bytes.Contains(v, []byte(`\ufffd`)) || bytes.Contains(v, []byte("�"))) {
					t.Fatalf("replacement persisted: %s", v)
				}
			}
			// The raw method returns source data successfully; the App final HTTP
			// serializer, independently owned by Root, decides the web response.
			pageResult, pageErr := p.client.CallWithOptions(p.ctx, "get_page", json.RawMessage(`null`), host, sdk.CallOptions{CallerDeadlineOnly: true})
			if pageErr != nil {
				t.Fatalf("raw get_page must succeed before final HTTP serialization: %v", pageErr)
			}
			if !json.Valid(pageResult.Result) || !utf8.Valid(pageResult.Result) {
				t.Fatal("illegal raw page IPC JSON")
			}
			if code != "fffd" && (!bytes.Contains(pageResult.Result, []byte(`\u`+code)) || bytes.Contains(pageResult.Result, []byte("�"))) {
				t.Fatalf("raw page lost original codepoint: %s", pageResult.Result)
			}

			mu.Lock()
			gotNetwork := network
			mu.Unlock()
			if gotNetwork != 2 {
				t.Fatalf("selected ordinary HTTP calls=%d want2/no replay", gotNetwork)
			}
			t.Logf("classification=actual independent SDK worker/rawKV peer;2HTTP/2escaped rows;parentcalls=%d;raw page method success=%v; HTTP not exercised", out.HostCalls, pageErr == nil)
		})
	}
}
func TestRefinementStrictBoundariesAndSourceLogDrop(t *testing.T) {
	if pythonScalar(Object(nil)) != "None" || pythonScalar([]any(nil)) != "None" {
		t.Fatal("legacy nil containers must retain JSON-null/None projection")
	}
	for _, raw := range []string{`NaN`, `Infinity`, `{"x":NaN}`} {
		if _, e := sourceRestoreSavedJSON([]byte(raw)); e == nil {
			t.Fatalf("relaxed IPC JSON %s", raw)
		}
	}
	v, e := parseSiteJSON(`{"z":NaN,"a":[Infinity,-Infinity],"z":1}`)
	if e != nil {
		t.Fatal(e)
	}
	if got := pythonScalar(v); got != "{'z': 1, 'a': [inf, -inf]}" {
		t.Fatal(got)
	}
	if _, e = json.Marshal(v); e == nil {
		t.Fatal("nonfinite HTTP container entered strict JSON")
	}
	bad := string([]byte{0xed, 0xa0, 0x80})
	seen := []sdk.LogEntryV1{}
	b := sdkBackend{ctx: context.Background(), host: func(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
		if method != sdk.LogWriteCapability {
			t.Fatal(method)
		}
		var row sdk.LogEntryV1
		if e := json.Unmarshal(raw, &row); e != nil {
			t.Fatal(e)
		}
		if e := row.Validate(); e != nil {
			t.Fatal(e)
		}
		seen = append(seen, row)
		return json.RawMessage(`null`), nil
	}}
	if e := b.log("INFO", bad); e != nil {
		t.Fatal(e)
	}
	if len(seen) != 1 || seen[0].Level != sdk.LogWarning || !strings.HasPrefix(seen[0].Message, "source_utf8_log_dropped:") {
		t.Fatalf("source drop did not continue with finite diagnostic: %v", seen)
	}
	revoked := errors.New("owned revoke")
	b.host = func(context.Context, string, json.RawMessage) (json.RawMessage, error) { return nil, revoked }
	if !errors.Is(b.log("INFO", bad), revoked) {
		t.Fatal("source log adaptation swallowed host guard")
	}
}

func TestRefinementActualSourceResponseSerialization(t *testing.T) {
	raw, e := os.ReadFile("testdata/source-oracles/source-asgi-response-only-20261106/source-response.json")
	if e != nil {
		t.Fatal(e)
	}
	var f struct {
		Rows []struct {
			Text string `json:"input_http_text"`
			Web  map[string]struct {
				Status    int
				Exception *struct{ Kind, Message string }
			}
		}
	}
	if e = Decode(raw, &f); e != nil {
		t.Fatal(e)
	}
	for i, c := range f.Rows {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
			site := Object{"id": 1, "name": "Owned", "url": "https://yemapt.org/", "public": false, "cookie": "owned", "ua": "Owned/1"}
			engine := New(Inventory{Indexers: []Object{site}, Configured: []Object{site}}, func() time.Time { return now })
			engine.SchemaLoaded = true
			engine.RetryKeyword = "never-match"
			adapter, _ := FixedSiteAdapter(AdapterYemaPT)
			visitor := SiteVisitor{Adapters: []SiteAdapter{adapter}, Hooks: SiteVisitHooks{HTTP: edgeHTTPFunction(func(context.Context, PageRequest) (*PageResponse, error) {
				return &PageResponse{Status: 200, Text: c.Text}, nil
			}), Now: func() time.Time { return now }, Stats: func(context.Context, string, bool, int) error { return nil }}}
			if e := engine.Do(context.Background(), now, "签到", []any{"all"}, nil, nil, RunHooks{Visit: visitor.Visit}); e != nil {
				t.Fatal(e)
			}
			page, e := engine.Page()
			if e != nil {
				t.Fatal(e)
			}
			api, e := SigninByDomain(context.Background(), "https://yemapt.org/", DomainAPIHooks{KeyAccepted: true, Lookup: func(context.Context, string) (Object, error) { return site, nil }, Visit: func(ctx context.Context, site Object) (Result, error) {
				return visitor.Visit(ctx, site, "签到", true)
			}})
			if e != nil {
				t.Fatal(e)
			}
			for path, value := range map[string]any{"/page/AutoSignIn": page, "/source-api": api} {
				want := c.Web[path]
				got := sourceWebSerializationFailure(value)
				if want.Status == 200 {
					if got != nil {
						t.Fatalf("%s source200 got%v", path, got)
					}
					continue
				}
				var typed *ValueError
				if !errors.As(got, &typed) || typed.Kind != want.Exception.Kind || typed.Message != want.Exception.Message {
					t.Fatalf("%s source=%+v got%v/%v", path, want.Exception, typed, got)
				}
			}
		})
	}
}

func TestRefinementSourceSavedTimeConversionOrder(t *testing.T) {
	raw, e := os.ReadFile(refinementRoot + "source-time-order-r1.json")
	if e != nil {
		t.Fatal(e)
	}
	var f struct {
		Rows []struct {
			Raw    string
			Result []string
			Error  *struct{ Kind, Message string }
		}
	}
	if e = Decode(raw, &f); e != nil {
		t.Fatal(e)
	}
	for i, c := range f.Rows {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			var times any
			if e = Decode([]byte(c.Raw), &times); e != nil {
				t.Fatal(e)
			}
			out, e := ParseRandomSchedule("2026-11-06", times)
			if c.Error == nil {
				if e != nil || len(out) != len(c.Result) {
					t.Fatalf("source empty result got%v/%v", out, e)
				}
				return
			}
			var typed *ValueError
			if !errors.As(e, &typed) || typed.Kind != c.Error.Kind || typed.Message != c.Error.Message {
				t.Fatalf("source%+v got%v/%v", c.Error, typed, e)
			}
			now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
			engine := New(Inventory{}, func() time.Time { return now })
			engine.Enabled = true
			engine.Cron = ""
			effects := []string{}
			engine.HistoryLive = func(string) (any, error) {
				effects = append(effects, "get_data")
				return Object{"date": "2026-11-06", "times": times}, nil
			}
			engine.Effects = func(Effect) error { effects = append(effects, "put"); return nil }
			_, e = engine.Services(func(int, int) (int, error) { effects = append(effects, "random"); return 120, nil }, "UTC")
			if e == nil || e.Error() != c.Error.Message || !reflect.DeepEqual(effects, []string{"get_data"}) {
				t.Fatalf("public getter overflow stage got%v effects%v", e, effects)
			}
		})
	}
}

func TestRefinementSourceMetadataFieldPresence(t *testing.T) {
	id := "owned-site"
	configured := []Object{{"_host_site_id": id, "public": false, "proxy": true, "render": true}}
	for name, fields := range map[string]string{"catalog-public": `{"public":true,"proxy":false,"render":false,"timeout":17}`, "explicit-null": `{"public":null,"proxy":null,"render":null}`, "explicit-false": `{"public":false,"proxy":false,"render":false}`, "absent-legacy-fallback": `{}`} {
		t.Run(name, func(t *testing.T) {
			backend := sdkBackend{ctx: context.Background()}
			out := backend.project(sdk.SiteSourceIndexerV1{SiteID: &id, Fields: json.RawMessage(fields)}, configured, false)
			var original Object
			if e := Decode([]byte(fields), &original); e != nil {
				t.Fatal(e)
			}
			for _, key := range []string{"public", "proxy", "render"} {
				want, exists := original[key]
				if !exists {
					want = configured[0][key]
				}
				if !reflect.DeepEqual(out[key], want) {
					t.Fatalf("source field presence %s got%#v want%#v", key, out[key], want)
				}
			}
			if out["_host_site_id"] != id {
				t.Fatal("lost canonical identity")
			}
		})
	}
	calls := 0
	b := sdkBackend{ctx: context.Background(), host: func(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
		calls++
		if method != sdk.SitesSourceCredentialsCapability {
			t.Fatal(method)
		}
		var req sdk.SiteSourceReferenceRequestV1
		if e := json.Unmarshal(raw, &req); e != nil {
			t.Fatal(e)
		}
		if req.Reference.SiteID != id {
			t.Fatal("credentials borrowed another id")
		}
		req.Reference.Snapshot = strings.Repeat("b", 64)
		return json.Marshal(sdk.SiteSourceCredentialsV1{SchemaVersion: 1, Reference: req.Reference, Fields: json.RawMessage(`{"cookie":"owned-fixture"}`)})
	}}
	metadata := b.project(sdk.SiteSourceIndexerV1{Fields: json.RawMessage(`{"public":true}`)}, configured, true)
	if calls != 0 || metadata["_host_site_id"] != nil || metadata["public"] != true {
		t.Fatalf("catalog-only metadata borrowed credentials or config: %v/%d", metadata, calls)
	}
	active := b.project(sdk.SiteSourceIndexerV1{SiteID: &id, Fields: json.RawMessage(`{"public":true}`)}, configured, true)
	if calls != 1 || active["cookie"] != "owned-fixture" || active["public"] != true {
		t.Fatalf("current identity credential path failed: %v/%d", active, calls)
	}
}

func TestRefinementNotificationPrivateBoundaryPosition(t *testing.T) {
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	site := Object{"id": 1, "name": "Owned", "public": false}
	engine := New(Inventory{Indexers: []Object{site}}, func() time.Time { return now })
	engine.Notify = true
	engine.RetryKeyword = "never-match"
	effects := []string{}
	engine.Effects = func(effect Effect) error { effects = append(effects, effect.Kind); return nil }
	sent := 0
	var first json.RawMessage
	sourceFailure := errors.New("owned UnicodeEncodeError at original notification history")
	backend := sdkBackend{ctx: context.Background(), host: func(_ context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
		sent++
		if method != sdk.PostMessageCapability {
			t.Fatalf("unexpected host method %s", method)
		}
		if sent == 1 {
			first = append(json.RawMessage(nil), raw...)
			return nil, sourceFailure
		}
		return json.RawMessage(`{"queued":1,"attempted":1,"failed":0,"errors":[]}`), nil
	}}
	private := string([]byte{0xed, 0xa0, 0x80})
	err := engine.Do(context.Background(), now, "签到", []any{"all"}, nil, nil, RunHooks{Visit: func(context.Context, Object, string, bool) (Result, error) {
		return Result{SiteName: "Owned", Message: "签到失败" + private}, nil
	}, Notify: backend.notify})
	if !errors.Is(err, sourceFailure) {
		t.Fatalf("original post_message exception must stop source tail: %v", err)
	}
	if !reflect.DeepEqual(effects, []string{"history.put", "history.put"}) || len(engine.Data) != 2 || sent != 1 {
		t.Fatalf("post_message stage/prefix moved: effects%v rows%d host%d", effects, len(engine.Data), sent)
	}
	if !bytes.Contains(first, []byte(`\ud800`)) || bytes.Contains(first, []byte("�")) {
		t.Fatalf("source notification normalized before realhost boundary: %s", first)
	}
	if err := backend.notify(context.Background(), Object{"mtype": "NotificationType.SiteMessage", "text": "owned ordinary notification"}); err != nil || sent != 2 {
		t.Fatalf("ordinary notification changed %v host%d", err, sent)
	}
}
