package subscriptionstats

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"
)

type asyncWire struct {
	Method  string            `json:"method"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
	Body    string            `json:"body"`
	Timeout map[string]int    `json:"timeout,omitempty"`
}

type asyncOriginalCase struct {
	Name       string                     `json:"name"`
	Options    map[string]json.RawMessage `json:"options"`
	Returned   bool                       `json:"source_returned"`
	Value      bool                       `json:"source_value"`
	Error      string                     `json:"source_error"`
	InputJSON  string                     `json:"input_json"`
	FieldsJSON string                     `json:"fields_json"`
	Requests   []asyncWire                `json:"requests"`
	Transports []struct {
		Proxy *string `json:"proxy"`
	} `json:"transports"`
}

func asyncSettings() SettingsSnapshot {
	share, uid := true, "owned-uid"
	return SettingsSnapshot{Share: &share, Known: true, ServerHost: "http://owned.invalid/base///", UserUID: &uid, UserAgent: "Owned-Source-UA", Environment: []string{}}
}

func asyncOraclePayload(test asyncOriginalCase) ([]byte, error) {
	fields := []sourceField{{"name", json.RawMessage(`"电影 <&>\u2028\ud83d\ude00"`)}, {"id", json.RawMessage(`123`)}, {"none", json.RawMessage(`null`)}, {"float", json.RawMessage(`1.0`)}}
	if test.InputJSON != "" {
		var input map[string]json.RawMessage
		if json.Unmarshal([]byte(test.InputJSON), &input) != nil {
			return nil, ErrInvalidJSON
		}
		fields = []sourceField{{"value", input["value"]}}
	}
	raw, err := marshalSourceFields(fields)
	if err != nil {
		return nil, err
	}
	return asyncJSON(raw)
}

func TestAsyncOriginalHelperHTTPXWireOracle(t *testing.T) {
	raw, err := os.ReadFile("testdata/async-original.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Cases []asyncOriginalCase `json:"cases"`
	}
	if err = json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	if len(fixture.Cases) != 35 {
		t.Fatal("incomplete installed oracle")
	}
	for _, test := range fixture.Cases {
		t.Run(test.Name, func(t *testing.T) {
			var captured []asyncWire
			var mu sync.Mutex
			var calls int
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				headers := map[string]string{"host": r.Host}
				for k, v := range r.Header {
					headers[strings.ToLower(k)] = strings.Join(v, ", ")
				}
				if r.ContentLength > 0 {
					headers["content-length"] = strconv.FormatInt(r.ContentLength, 10)
				}
				logical := r.URL.String()
				if r.URL.Scheme == "" {
					logical = "http://" + r.Host + r.URL.RequestURI()
				}
				mu.Lock()
				calls++
				call := calls
				captured = append(captured, asyncWire{Method: r.Method, URL: logical, Headers: headers, Body: string(body)})
				mu.Unlock()
				if string(test.Options["request_error"]) == "true" {
					conn, _, e := w.(http.Hijacker).Hijack()
					if e == nil {
						conn.Close()
					}
					return
				}
				var redirect, status int
				json.Unmarshal(test.Options["redirect"], &redirect)
				json.Unmarshal(test.Options["status"], &status)
				if redirect != 0 && call == 1 {
					w.Header().Set("Location", "/next")
					w.Header().Set("Set-Cookie", "owned=yes; Path=/")
					w.WriteHeader(redirect)
					w.Write([]byte("redirect"))
					return
				}
				if status == 0 {
					status = 200
				}
				w.WriteHeader(status)
				w.Write([]byte("owned"))
			}))
			defer server.Close()
			address := strings.TrimPrefix(server.URL, "http://")
			base := http.DefaultTransport.(*http.Transport).Clone()
			base.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, network, address)
			}
			client := AsyncClient{Base: base, Guard: func(context.Context, *url.URL) error { return nil }}
			defer client.Close()
			setting := asyncSettings()
			if raw, ok := test.Options["share"]; ok {
				json.Unmarshal(raw, setting.Share)
			}
			if raw, ok := test.Options["uid"]; ok {
				if string(raw) == "null" {
					setting.UserUID = nil
				} else {
					var v string
					json.Unmarshal(raw, &v)
					setting.UserUID = &v
				}
			}
			if raw, ok := test.Options["ua"]; ok {
				json.Unmarshal(raw, &setting.UserAgent)
			}
			if raw, ok := test.Options["host"]; ok {
				json.Unmarshal(raw, &setting.ServerHost)
			}
			if raw, ok := test.Options["proxies"]; ok {
				json.Unmarshal(raw, &setting.Proxies)
			}
			var result Result
			if !*setting.Share {
				result, err = client.SubReg(context.Background(), setting, nil)
			} else {
				body, encodingErr := asyncOraclePayload(test)
				if encodingErr != nil {
					var caught bool
					result, caught = AsyncJSONResult(encodingErr)
					if !caught {
						t.Fatal(encodingErr)
					}
				} else {
					encoded, _ := json.Marshal(setting)
					var sourceSettings map[string]json.RawMessage
					json.Unmarshal(encoded, &sourceSettings)
					if raw, ok := test.Options["host"]; ok {
						sourceSettings["server_host"] = raw
					}
					encoded, _ = json.Marshal(sourceSettings)
					result, err = client.SubRegSourceSettings(context.Background(), encoded, body)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			if result.SourceReturned != test.Returned || result.SourceValue != test.Value || result.SourceError != test.Error {
				t.Fatalf("result got=%+v expected returned=%v value=%v error=%s", result, test.Returned, test.Value, test.Error)
			}
			mu.Lock()
			actual := append([]asyncWire{}, captured...)
			mu.Unlock()
			if len(actual) != len(test.Requests) {
				t.Fatalf("request count=%d expected=%d", len(actual), len(test.Requests))
			}
			for i, want := range test.Requests {
				want.Timeout = nil
				if !reflect.DeepEqual(actual[i], want) {
					t.Fatalf("wire %d got=%+v expected=%+v", i, actual[i], want)
				}
			}
			if len(actual) > 0 {
				proxy := ""
				if test.Transports[0].Proxy != nil {
					proxy = *test.Transports[0].Proxy
				}
				if client.pools[proxy] == nil {
					t.Fatal("wrong source proxy selected")
				}
			}
		})
	}
}

func TestAsyncClientSharedHTTP2PoolFreshCookiesAndScopedTLS(t *testing.T) {
	var connections atomic.Int32
	var requests atomic.Int32
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ProtoMajor != 2 {
			t.Error("HTTP/2 was not negotiated")
		}
		if r.Header.Get("Cookie") != "" {
			t.Error("per-call cookie jar leaked")
		}
		requests.Add(1)
		w.Header().Set("Set-Cookie", "isolated=yes; Path=/")
		w.Write([]byte("owned"))
	}))
	server.EnableHTTP2 = true
	server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			connections.Add(1)
		}
	}
	server.StartTLS()
	defer server.Close()
	base := http.DefaultTransport.(*http.Transport).Clone()
	client := AsyncClient{Base: base, Guard: func(context.Context, *url.URL) error { return nil }}
	defer client.Close()
	setting := asyncSettings()
	setting.ServerHost = server.URL
	for i := 0; i < 2; i++ {
		result, err := client.SubReg(context.Background(), setting, []byte(`{"owned":true}`))
		if err != nil || !result.SourceValue {
			t.Fatalf("%+v %v", result, err)
		}
	}
	if requests.Load() != 2 || connections.Load() != 1 {
		t.Fatalf("requests=%d connections=%d", requests.Load(), connections.Load())
	}
	if base.TLSClientConfig != nil && base.TLSClientConfig.InsecureSkipVerify {
		t.Fatal("caller TLS policy mutated")
	}
}

func TestAsyncClientIgnoresEnvironmentProxyAndHonorsExplicitProxy(t *testing.T) {
	for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "all_proxy"} {
		t.Setenv(key, "http://127.0.0.1:1")
	}
	var directCalls, proxyCalls atomic.Int32
	direct := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { directCalls.Add(1); w.Write([]byte("owned")) }))
	defer direct.Close()
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxyCalls.Add(1)
		if !r.URL.IsAbs() {
			t.Error("proxy did not get absolute URL")
		}
		w.Write([]byte("owned"))
	}))
	defer proxy.Close()
	client := AsyncClient{Guard: func(context.Context, *url.URL) error { return nil }}
	defer client.Close()
	setting := asyncSettings()
	setting.ServerHost = direct.URL
	result, err := client.SubReg(context.Background(), setting, []byte(`{}`))
	if err != nil || !result.SourceValue {
		t.Fatalf("%+v %v", result, err)
	}
	setting.Proxies = map[string]string{"http": "http://127.0.0.1:1", "https": proxy.URL}
	result, err = client.SubReg(context.Background(), setting, []byte(`{}`))
	if err != nil || !result.SourceValue {
		t.Fatalf("%+v %v", result, err)
	}
	if directCalls.Load() != 1 || proxyCalls.Load() != 1 {
		t.Fatalf("direct=%d proxy=%d", directCalls.Load(), proxyCalls.Load())
	}
}

func TestAsyncClientSourceDecodersAndCorruptBody(t *testing.T) {
	for _, encoding := range []string{"gzip", "zlib", "raw-deflate", "br", "zstd", "broken-gzip"} {
		t.Run(encoding, func(t *testing.T) {
			var body bytes.Buffer
			var writer io.WriteCloser
			wireEncoding := encoding
			switch encoding {
			case "gzip":
				writer = gzip.NewWriter(&body)
			case "zlib":
				writer = zlib.NewWriter(&body)
				wireEncoding = "deflate"
			case "raw-deflate":
				writer, _ = flate.NewWriter(&body, flate.DefaultCompression)
				wireEncoding = "deflate"
			case "br":
				writer = brotli.NewWriter(&body)
			case "zstd":
				writer, _ = zstd.NewWriter(&body, zstd.WithEncoderConcurrency(1))
			case "broken-gzip":
				body.WriteString("invalid")
				wireEncoding = "gzip"
			}
			if writer != nil {
				writer.Write([]byte(strings.Repeat("owned", 200)))
				writer.Close()
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Encoding", wireEncoding)
				w.Write(body.Bytes())
			}))
			defer server.Close()
			client := AsyncClient{Guard: func(context.Context, *url.URL) error { return nil }}
			defer client.Close()
			setting := asyncSettings()
			setting.ServerHost = server.URL
			result, err := client.SubReg(context.Background(), setting, []byte(`{}`))
			if err != nil || !result.SourceReturned || result.SourceValue != (encoding != "broken-gzip") {
				t.Fatalf("%+v %v", result, err)
			}
		})
	}
}

func TestAsyncClientLazyDisabledNativeBindingAndLostReply(t *testing.T) {
	client := AsyncClient{}
	setting := asyncSettings()
	*setting.Share = false
	setting.Known = false
	result, err := client.SubRegFields(context.Background(), setting, []byte(`invalid`))
	if err != nil || !result.SourceReturned || result.SourceValue || result.SourceError != "" {
		t.Fatalf("%+v %v", result, err)
	}
	*setting.Share = true
	if _, err = client.SubRegFields(context.Background(), setting, []byte(`{}`)); !errors.Is(err, ErrDependency) {
		t.Fatal(err)
	}
	var first, foreign atomic.Int32
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { foreign.Add(1) }))
	defer other.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		first.Add(1)
		w.Header().Set("Location", other.URL+"/foreign")
		w.WriteHeader(307)
	}))
	defer server.Close()
	client = AsyncClient{Guard: func(context.Context, *url.URL) error { return nil }}
	defer client.Close()
	setting = asyncSettings()
	setting.ServerHost = server.URL
	if _, err = client.SubReg(context.Background(), setting, []byte(`{}`)); !errors.Is(err, ErrRedirect) {
		t.Fatal(err)
	}
	if first.Load() != 1 || foreign.Load() != 0 {
		t.Fatal("unbound redirected request")
	}
}

func TestAsyncClientReadTimeoutIsInactivityRatherThanTotal(t *testing.T) {
	for _, slow := range []bool{false, true} {
		t.Run(strconv.FormatBool(slow), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(200)
				w.(http.Flusher).Flush()
				pause := 2 * time.Second
				chunks := 3
				if slow {
					pause = 6 * time.Second
					chunks = 1
				}
				for i := 0; i < chunks; i++ {
					select {
					case <-time.After(pause):
					case <-r.Context().Done():
						return
					}
					w.Write([]byte("part"))
					w.(http.Flusher).Flush()
				}
			}))
			defer server.Close()
			client := AsyncClient{Guard: func(context.Context, *url.URL) error { return nil }}
			defer client.Close()
			setting := asyncSettings()
			setting.ServerHost = server.URL
			result, err := client.SubReg(context.Background(), setting, []byte(`{}`))
			if err != nil || !result.SourceReturned || result.SourceValue == slow {
				t.Fatalf("%+v %v", result, err)
			}
		})
	}
}

func asyncRawSetting(test asyncOriginalCase) json.RawMessage {
	raw, _ := json.Marshal(asyncSettings())
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(raw, &fields)
	for source, target := range map[string]string{"share": "share", "host": "server_host", "ua": "user_agent", "uid": "user_uid", "proxies": "proxies"} {
		if value, ok := test.Options[source]; ok {
			fields[target] = value
		}
	}
	raw, _ = json.Marshal(fields)
	return raw
}

func asyncOwnedWireClient(t *testing.T) (*AsyncClient, *[]asyncWire, *sync.Mutex) {
	t.Helper()
	var captures []asyncWire
	var mu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		headers := map[string]string{"host": r.Host}
		for k, v := range r.Header {
			headers[strings.ToLower(k)] = strings.Join(v, ", ")
		}
		if r.ContentLength > 0 {
			headers["content-length"] = strconv.FormatInt(r.ContentLength, 10)
		}
		logical := r.URL.String()
		if r.URL.Scheme == "" {
			logical = "http://" + r.Host + r.URL.RequestURI()
		}
		mu.Lock()
		captures = append(captures, asyncWire{Method: r.Method, URL: logical, Headers: headers, Body: string(body)})
		mu.Unlock()
		w.WriteHeader(200)
	}))
	t.Cleanup(server.Close)
	address := strings.TrimPrefix(server.URL, "http://")
	base := http.DefaultTransport.(*http.Transport).Clone()
	base.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, address)
	}
	client := &AsyncClient{Base: base, Guard: func(context.Context, *url.URL) error { return nil }}
	t.Cleanup(client.Close)
	return client, &captures, &mu
}

func TestAsyncSourceCrossValidationOrderAndRawProxyBoundary(t *testing.T) {
	raw, err := os.ReadFile("testdata/async-original.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Cases []asyncOriginalCase `json:"cross_cases"`
	}
	if json.Unmarshal(raw, &fixture) != nil || len(fixture.Cases) != 15 {
		t.Fatal("missing source cross oracle")
	}
	for _, test := range fixture.Cases {
		t.Run(test.Name, func(t *testing.T) {
			client, captures, mu := asyncOwnedWireClient(t)
			payload, encodingErr := asyncOraclePayload(test)
			if encodingErr != nil {
				payload = []byte(`{"value":1e309}`)
			}
			result, err := client.SubRegSourceSettings(context.Background(), asyncRawSetting(test), payload)
			nativeBoundary := strings.HasPrefix(test.Name, "proxy-") && strings.Contains(test.Name, "surrogate") && !strings.Contains(test.Name, "unselected")
			if nativeBoundary {
				if !errors.Is(err, ErrDependency) || result.SourceReturned || result.SourceError != "" {
					t.Fatalf("selected raw surrogate is an explicit native dependency, never a replacement URL: %+v %v", result, err)
				}
			} else if err != nil || result.SourceReturned != test.Returned || result.SourceValue != test.Value || result.SourceError != test.Error {
				t.Fatalf("source exception order got=%+v %v want returned=%v value=%v error=%s", result, err, test.Returned, test.Value, test.Error)
			}
			mu.Lock()
			actual := append([]asyncWire{}, (*captures)...)
			mu.Unlock()
			if nativeBoundary {
				if len(actual) != 0 {
					t.Fatal("replacement proxy was requested")
				}
				return
			}
			if len(actual) != len(test.Requests) {
				t.Fatal("source wire count", len(actual), len(test.Requests))
			}
			for i, want := range test.Requests {
				want.Timeout = nil
				if !reflect.DeepEqual(actual[i], want) {
					t.Fatalf("owned wire got=%+v expected=%+v", actual[i], want)
				}
			}
		})
	}
}

func TestAsyncSubRegFieldsActualSixteenFieldsSourceAPI(t *testing.T) {
	raw, err := os.ReadFile("testdata/async-original.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Cases []asyncOriginalCase `json:"registration_cases"`
	}
	if json.Unmarshal(raw, &fixture) != nil || len(fixture.Cases) != 10 {
		t.Fatal("missing 16-field source oracle")
	}
	for _, test := range fixture.Cases {
		for _, useRaw := range []bool{false, true} {
			t.Run(test.Name+"/raw="+strconv.FormatBool(useRaw), func(t *testing.T) {
				client, captures, mu := asyncOwnedWireClient(t)
				rawSettings := asyncRawSetting(test)
				var result Result
				var err error
				if useRaw {
					result, err = client.SubRegSourceFields(context.Background(), rawSettings, []byte(test.FieldsJSON))
				} else {
					var setting SettingsSnapshot
					if json.Unmarshal(rawSettings, &setting) != nil {
						t.Fatal("bad typed snapshot")
					}
					result, err = client.SubRegFields(context.Background(), setting, []byte(test.FieldsJSON))
				}
				if err != nil || result.SourceReturned != test.Returned || result.SourceValue != test.Value || result.SourceError != test.Error {
					t.Fatalf("actual fields API got=%+v %v want returned=%v value=%v error=%s", result, err, test.Returned, test.Value, test.Error)
				}
				mu.Lock()
				actual := append([]asyncWire{}, (*captures)...)
				mu.Unlock()
				if len(actual) != len(test.Requests) {
					t.Fatal("source wire count", len(actual), len(test.Requests))
				}
				for i, want := range test.Requests {
					want.Timeout = nil
					if !reflect.DeepEqual(actual[i], want) {
						t.Fatalf("wire exact precision/compact UTF8 got=%+v want=%+v", actual[i], want)
					}
				}
			})
		}
	}
}

func TestAsyncSubRegSourceSettingsDisabledIsLazyAndCancelIsNative(t *testing.T) {
	client := AsyncClient{}
	disabled := json.RawMessage(`{"share":false,"known":"invalid","server_host":null,"proxies":{"https":"http://\ud800.invalid"},"user_agent":[]}`)
	result, err := client.SubRegSourceFields(context.Background(), disabled, []byte(`invalid`))
	if err != nil || !result.SourceReturned || result.SourceValue {
		t.Fatalf("disabled raw API evaluated unused fields: %+v %v", result, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result, err = client.SubRegSourceFields(ctx, json.RawMessage(`invalid`), nil)
	if !errors.Is(err, context.Canceled) || result.SourceReturned || result.SourceError != "" {
		t.Fatalf("native cancel became source result %+v %v", result, err)
	}
}

func TestAsyncClientPoolLRUBoundAndFortySlotTimeoutAndCancel(t *testing.T) {
	client := AsyncClient{}
	t.Cleanup(client.Close)
	first, err := client.pool("http://owned-0.invalid:3")
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i < 32; i++ {
		if _, err = client.pool("http://owned-" + strconv.Itoa(i) + ".invalid:3"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = client.pool("http://owned-0.invalid:3"); err != nil {
		t.Fatal(err)
	}
	if _, err = client.pool("http://owned-32.invalid:3"); err != nil {
		t.Fatal(err)
	}
	if len(client.pools) != 32 || client.pools["http://owned-1.invalid:3"] != nil || client.pools["http://owned-0.invalid:3"] != first || cap(first.slots) != 40 {
		t.Fatal("shared LRU32/call slots40")
	}
	var guards atomic.Int32
	client.Guard = func(context.Context, *url.URL) error { guards.Add(1); return nil }
	setting := asyncSettings()
	setting.Proxies = map[string]string{"https": "http://owned-0.invalid:3"}
	for i := 0; i < 40; i++ {
		first.slots <- struct{}{}
	}
	defer func() {
		for i := 0; i < 40; i++ {
			<-first.slots
		}
	}()
	result, err := client.SubReg(context.Background(), setting, []byte(`{}`))
	if err != nil || !result.SourceReturned || result.SourceValue || result.Outcome != "unconfirmed" || guards.Load() != 0 {
		t.Fatalf("5s pool wait result %+v %v", result, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		r, e := client.SubReg(ctx, setting, []byte(`{}`))
		if r.SourceReturned || r.SourceError != "" {
			done <- errors.New("native cancellation became source outcome")
			return
		}
		done <- e
	}()
	cancel()
	if err = <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestAsyncClientGuardAndCloseLifecycleRemainNative(t *testing.T) {
	client, captured, mu := asyncOwnedWireClient(t)
	guardErr := errors.New("owned native origin guard")
	client.Guard = func(context.Context, *url.URL) error { return guardErr }
	result, err := client.SubReg(context.Background(), asyncSettings(), []byte(`{}`))
	if !errors.Is(err, guardErr) || result.SourceReturned || result.SourceError != "" {
		t.Fatalf("guard became source false: %+v %v", result, err)
	}
	mu.Lock()
	count := len(*captured)
	mu.Unlock()
	if count != 0 {
		t.Fatal("guarded call sent")
	}
	client.Close()
	if _, err = client.SubReg(context.Background(), asyncSettings(), []byte(`{}`)); !errors.Is(err, ErrDependency) {
		t.Fatal("closed client accepted new enabled request", err)
	}
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		close(started)
		<-r.Context().Done()
	}))
	defer server.Close()
	active := &AsyncClient{Guard: func(context.Context, *url.URL) error { return nil }}
	defer active.Close()
	setting := asyncSettings()
	setting.ServerHost = server.URL
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		r, e := active.SubReg(ctx, setting, []byte(`{}`))
		if r.SourceReturned || r.SourceError != "" {
			done <- errors.New("native active cancellation became source outcome")
			return
		}
		done <- e
	}()
	<-started
	active.Close()
	cancel()
	if err = <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
