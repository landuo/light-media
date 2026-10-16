package download

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"light-media/internal/logging"
	cis "light-media/internal/plugins/cleaninvalidseed"
)

const nativeHash = "0123456789abcdef0123456789abcdef01234567"

func nativeSourceFields(t *testing.T, modern bool) []string {
	t.Helper()
	raw, e := os.ReadFile("../../testdata/plugins/clean-invalid-seed-native/transmission-trarg.json")
	if e != nil {
		t.Fatal(e)
	}
	var c struct {
		Legacy []string `json:"legacy_fields"`
		Modern []string `json:"modern_fields"`
	}
	if e = json.Unmarshal(raw, &c); e != nil {
		t.Fatal(e)
	}
	if modern {
		return c.Modern
	}
	return c.Legacy
}
func TestNativeInstalledFieldContract(t *testing.T) {
	if !reflect.DeepEqual(nativeTRFields, nativeSourceFields(t, false)) {
		t.Fatal("installed _trarg changed")
	}
}

func nativeJSON(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var v map[string]any
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if err := d.Decode(&v); err != nil {
		t.Fatal(err)
	}
	return v
}
func nativeJSONList(t *testing.T, raw []byte) []map[string]any {
	t.Helper()
	v, e := nativeObjectList(raw, "test")
	if e != nil {
		t.Fatal(e)
	}
	return v
}
func nativeLoggedContext(t *testing.T) (context.Context, *bytes.Buffer) {
	t.Helper()
	out := new(bytes.Buffer)
	run, err := logging.NewRun(out)
	if err != nil {
		t.Fatal(err)
	}
	return run.Context(context.Background()), out
}
func TestNativeQBFullRawLazyTrackersAndAppend(t *testing.T) {
	raw := []byte(` [ {"hash":"` + nativeHash + `","name":"合成影片🎬","state":"stoppedUP","category":"类别","content_path":"/合成/影片","tags":"旧标签","tracker":"https://tracker.invalid/announce?passkey=synthetic-key","all_time_dl":900719925474099312345,"unknown":{"hashString":"stayCamel","x":[null,false,900719925474099312345]},"optional":null} ] `)
	var logins, info, trackerCalls, tagsCalls atomic.Int32
	tags := make([]string, 40)
	for i := range tags {
		tags[i] = fmt.Sprintf("标签%d", i)
	}
	tags[0] = "  保留空白  "
	tags[1] = "逗号,标签"
	tags[2] = "标签2"
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Referer() != sOrigin(r)+"/" {
			t.Error("origin referer")
		}
		if r.URL.Path == "/proxy/api/v2/auth/login" {
			_ = r.ParseForm()
			if r.Form.Get("username") != "test-user" || r.Form.Get("password") != "synthetic-password" {
				t.Error("credentials")
			}
			logins.Add(1)
			http.SetCookie(w, &http.Cookie{Name: "SID", Value: "synthetic-session", Path: "/proxy"})
			io.WriteString(w, "Ok.")
			return
		}
		c, e := r.Cookie("SID")
		if e != nil || c.Value != "synthetic-session" {
			http.Error(w, "secret-cookie", 403)
			return
		}
		switch r.URL.Path {
		case "/proxy/api/v2/torrents/info":
			if r.URL.RawQuery != "" {
				t.Error("native scope was filtered")
			}
			info.Add(1)
			w.Write(raw)
		case "/proxy/api/v2/torrents/trackers":
			if r.Method != "GET" || r.URL.Query().Get("hash") != nativeHash {
				t.Error("tracker selector")
			}
			n := trackerCalls.Add(1)
			fmt.Fprintf(w, `[{"url":"https://synthetic.invalid/a?passkey=synthetic-key","tier":-1,"status":4,"msg":"错误第%d轮","num_peers":900719925474099312345,"unknown":false}]`, n)
		case "/proxy/api/v2/torrents/addTags":
			_ = r.ParseForm()
			if r.Method != "POST" || r.Form.Get("hashes") != nativeHash || r.Form.Get("tags") != strings.Join(tags, ",") {
				t.Error("append wire differs")
			}
			tagsCalls.Add(1)
		default:
			t.Errorf("unexpected endpoint %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer s.Close()
	c := mustClient(t, "qb", s.URL+"/proxy")
	ctx, logs := nativeLoggedContext(t)
	got, err := c.NativeTorrents(ctx)
	if err != nil || !bytes.Equal(got, raw) {
		t.Fatalf("native raw changed: %v", err)
	}
	rows := nativeJSONList(t, got)
	if rows[0]["all_time_dl"].(json.Number).String() != "900719925474099312345" {
		t.Fatal("large number rounded")
	}
	got[0] = 'x'
	again, err := c.NativeTorrents(ctx)
	if err != nil || !bytes.Equal(again, raw) {
		t.Fatal("response aliases previous caller")
	}
	for i := 1; i <= 4; i++ {
		got, err := c.NativeTrackers(ctx, "living-room:"+strings.ToUpper(nativeHash))
		if err != nil {
			t.Fatal(err)
		}
		row := nativeJSONList(t, got)[0]
		if row["msg"] != fmt.Sprintf("错误第%d轮", i) {
			t.Fatal("lazy tracker cached")
		}
	}
	if err = c.SetTorrentTags(ctx, nativeHash, tags); err != nil {
		t.Fatal(err)
	}
	if logins.Load() != 1 || info.Load() != 2 || trackerCalls.Load() != 4 || tagsCalls.Load() != 1 {
		t.Fatal("unexpected call counts")
	}
	for _, secret := range []string{"synthetic-password", "synthetic-session", "synthetic-key", "tracker.invalid", nativeHash, "合成影片"} {
		if strings.Contains(logs.String(), secret) {
			t.Fatalf("secret leaked in logs: %s", secret)
		}
	}
}
func nativeTRPayload(modern bool) []byte {
	row := map[string]any{"id": json.Number("900719925474099312345"), "name": "合成TR🎬", "status": 6, "labels": []string{"旧标签"}, "hashString": nativeHash, "totalSize": json.Number("900719925474099312345"), "percentDone": json.Number("1.000"), "addedDate": 100, "trackerList": "https://synthetic.invalid/a?passkey=synthetic-key", "leftUntilDone": 0, "rateDownload": 0, "rateUpload": 123, "recheckProgress": 0, "peersGettingFromUs": 1, "peersSendingToUs": 2, "uploadRatio": json.Number("1.234567890123456789"), "uploadedEver": 10, "downloadedEver": 20, "downloadDir": "/合成", "error": 0, "errorString": "", "doneDate": 100, "queuePosition": 0, "activityDate": 200, "trackers": []any{map[string]any{"announce": "https://synthetic.invalid/a?passkey=synthetic-key", "tier": 0, "id": 1}}, "trackerStats": []any{map[string]any{"announce": "https://synthetic.invalid/a?passkey=synthetic-key", "lastAnnounceResult": "torrent not registered", "lastAnnounceSucceeded": false, "lastAnnounceTime": 123, "unknown": map[string]any{"hashString": "opaque", "rateDownload": json.Number("900719925474099312345")}}}, "unknown": map[string]any{"hashString": "opaque", "rateDownload": json.Number("900719925474099312345")}, "optional": nil}
	if modern {
		for new, old := range nativeTRKeys {
			if v, ok := row[old]; ok {
				delete(row, old)
				row[new] = v
			}
		}
		for _, v := range row["tracker_stats"].([]any) {
			r := v.(map[string]any)
			for new, old := range nativeTRTrackerKeys {
				if v, ok := r[old]; ok {
					delete(r, old)
					r[new] = v
				}
			}
		}
	}
	raw, _ := json.Marshal([]any{row})
	return raw
}
func TestNativeTransmissionDialectsFullFieldsAndReplace(t *testing.T) {
	for _, modern := range []bool{false, true} {
		t.Run(fmt.Sprint(modern), func(t *testing.T) {
			var mu sync.Mutex
			var calls []string
			var requestIDs []string
			var reads int
			var mutations int
			var handshake string
			tags := []string{"旧标签", "无效做种", "逗号,标签", "无效做种"}
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				user, pass, ok := r.BasicAuth()
				if !ok || user != "test-user" || pass != "synthetic-password" {
					t.Error("basic authentication")
				}
				if r.URL.Path != "/rpc" || r.Method != "POST" {
					t.Error("configured endpoint")
				}
				body, _ := io.ReadAll(r.Body)
				q := nativeJSON(t, body)
				method, _ := q["method"].(string)
				id := q["id"]
				args := q["params"]
				if id == nil {
					id = q["tag"]
					args = q["arguments"]
				}
				mu.Lock()
				defer mu.Unlock()
				calls = append(calls, method)
				if handshake == "" {
					handshake = "synthetic-csrf"
					w.Header().Set("X-Transmission-Session-Id", handshake)
					w.WriteHeader(409)
					return
				}
				if r.Header.Get("X-Transmission-Session-Id") != handshake {
					t.Error("CSRF handshake")
				}
				send := func(values any) {
					if modern {
						reply(w, map[string]any{"jsonrpc": "2.0", "id": id, "result": values})
					} else {
						reply(w, map[string]any{"tag": id, "result": "success", "arguments": values})
					}
				}
				if !modern && method == "session_get" {
					reply(w, map[string]any{"tag": id, "result": "unknown method", "arguments": map[string]any{}})
					return
				}
				if method == "session_get" || method == "session-get" {
					send(map[string]any{"version": "synthetic-version"})
					return
				}
				if (modern && method == "torrent_get") || (!modern && method == "torrent-get") {
					fields := args.(map[string]any)["fields"].([]any)
					want := nativeSourceFields(t, modern)
					got := make([]string, len(fields))
					for i, f := range fields {
						got[i] = f.(string)
					}
					if !reflect.DeepEqual(got, want) {
						t.Errorf("native fields differ: %v", got)
					}
					reads++
					requestIDs = append(requestIDs, id.(json.Number).String())
					if reads == 1 {
						handshake = "synthetic-csrf-refresh"
						w.Header().Set("X-Transmission-Session-Id", handshake)
						w.WriteHeader(409)
						return
					}
					send(map[string]any{"torrents": json.RawMessage(nativeTRPayload(modern))})
					return
				}
				if (modern && method == "torrent_set") || (!modern && method == "torrent-set") {
					a := args.(map[string]any)
					if !reflect.DeepEqual(a["ids"], []any{nativeHash}) || !reflect.DeepEqual(a["labels"], []any{tags[0], tags[1], tags[2], tags[3]}) {
						t.Error("replace labels changed")
					}
					mutations++
					send(map[string]any{})
					return
				}
				t.Errorf("unexpected native method %s", method)
				http.Error(w, "bad", 500)
			}))
			defer s.Close()
			c := mustClient(t, "tr", s.URL+"/rpc")
			ctx, logs := nativeLoggedContext(t)
			got, err := c.NativeTorrents(ctx)
			if err != nil {
				t.Fatal(err)
			}
			want := nativeTRPayload(false)
			if !reflect.DeepEqual(nativeJSONList(t, got), nativeJSONList(t, want)) {
				t.Fatalf("native fields not preserved: %s", got)
			}
			rows := nativeJSONList(t, got)
			if _, ok := rows[0]["optional"]; !ok || rows[0]["optional"] != nil {
				t.Fatal("presence lost")
			}
			if err = c.SetTorrentTags(ctx, nativeHash, tags); err != nil {
				t.Fatal(err)
			}
			mu.Lock()
			defer mu.Unlock()
			if reads != 2 || mutations != 1 || len(requestIDs) != 2 || requestIDs[0] != requestIDs[1] {
				t.Fatal("mutation retried or 409 changed id")
			}
			if modern && c.trDialect != "jsonrpc" || !modern && c.trDialect != "legacy" {
				t.Fatal("dialect not negotiated")
			}
			for _, secret := range []string{"synthetic-password", "synthetic-csrf", nativeHash, "synthetic-key", "合成TR"} {
				if strings.Contains(logs.String(), secret) {
					t.Fatalf("secret leaked: %s", secret)
				}
			}
		})
	}
}
func TestNativeFailureAndSelectionBoundaries(t *testing.T) {
	for _, kind := range []string{"qb", "tr"} {
		t.Run(kind, func(t *testing.T) {
			var requests atomic.Int32
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); http.Error(w, "must not reach", 500) }))
			defer s.Close()
			c := mustClient(t, kind, s.URL)
			for _, hash := range []string{"all", "1", "", nativeHash + "|" + nativeHash, "other:" + nativeHash} {
				if _, e := c.NativeTrackers(context.Background(), hash); e == nil {
					t.Error("unsafe tracker selector")
				}
				if e := c.SetTorrentTags(context.Background(), hash, []string{"tag"}); e == nil {
					t.Error("unsafe mutation selector")
				}
			}
			if kind == "tr" {
				if _, e := c.NativeTrackers(context.Background(), nativeHash); e == nil {
					t.Error("TR lazy trackers method unsupported")
				}
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if _, e := c.NativeTorrents(ctx); !errors.Is(e, context.Canceled) {
				t.Error(e)
			}
			if e := c.SetTorrentTags(ctx, nativeHash, []string{"tag"}); !errors.Is(e, context.Canceled) {
				t.Error(e)
			}
			if _, e := c.NativeTorrents(nil); e == nil {
				t.Error("nil context accepted")
			}
			c.mu.Lock()
			deadline, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
			defer cancel()
			_, e := c.NativeTorrents(deadline)
			c.mu.Unlock()
			if !errors.Is(e, context.DeadlineExceeded) {
				t.Error("mutex wait ignores cancellation", e)
			}
			if e := c.SetTorrentTags(context.Background(), nativeHash, []string{string([]byte{0xff})}); e == nil {
				t.Error("invalid UTF8 tags accepted")
			}
			if requests.Load() != 0 {
				t.Error("rejected operation accessed daemon")
			}
		})
	}
}
func TestNativeQBResponsesRedirectRateLimitAndCancel(t *testing.T) {
	for _, action := range []string{"torrents", "trackers", "tags"} {
		for _, body := range []string{"false", "null", "{}", "[null]", "[] trailing", "synthetic-secret-body"} {
			t.Run(action+"-"+body, func(t *testing.T) {
				s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, body) }))
				defer s.Close()
				c, err := New(Config{ID: "synthetic", Kind: "qb", BaseURL: s.URL})
				if err != nil {
					t.Fatal(err)
				}
				var e error
				switch action {
				case "torrents":
					_, e = c.NativeTorrents(context.Background())
				case "trackers":
					_, e = c.NativeTrackers(context.Background(), nativeHash)
				case "tags":
					e = c.SetTorrentTags(context.Background(), nativeHash, []string{"tag"})
				}
				if e == nil || strings.Contains(e.Error(), body) {
					t.Fatal("invalid/secret response became success or leaked", e)
				}
			})
		}
	}
	for _, code := range []int{302, 429} {
		for _, action := range []string{"torrents", "trackers", "tags"} {
			t.Run(fmt.Sprintf("http%d-%s", code, action), func(t *testing.T) {
				var leaked, calls atomic.Int32
				other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked.Add(1) }))
				defer other.Close()
				s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls.Add(1)
					w.Header().Set("Location", other.URL+"/synthetic-secret")
					w.Header().Set("Retry-After", "100")
					w.WriteHeader(code)
					io.WriteString(w, "synthetic-secret")
				}))
				defer s.Close()
				c, _ := New(Config{ID: "synthetic", Kind: "qb", BaseURL: s.URL})
				var e error
				switch action {
				case "torrents":
					_, e = c.NativeTorrents(context.Background())
				case "trackers":
					_, e = c.NativeTrackers(context.Background(), nativeHash)
				case "tags":
					e = c.SetTorrentTags(context.Background(), nativeHash, []string{"tag"})
				}
				if e == nil || strings.Contains(e.Error(), "synthetic-secret") || leaked.Load() != 0 || calls.Load() != 1 {
					t.Fatal("redirect/rate limit behavior", e, leaked.Load(), calls.Load())
				}
			})
		}
	}
	started := make(chan struct{})
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(started); <-r.Context().Done() }))
	defer s.Close()
	c, _ := New(Config{ID: "synthetic", Kind: "qb", BaseURL: s.URL})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, e := c.NativeTorrents(ctx); done <- e }()
	<-started
	cancel()
	select {
	case e := <-done:
		if !errors.Is(e, context.Canceled) {
			t.Fatal(e)
		}
	case <-time.After(time.Second):
		t.Fatal("HTTP cancellation blocked")
	}
}

func TestNativeTransmissionProtocolFailures(t *testing.T) {
	for _, dialect := range []string{"legacy", "jsonrpc"} {
		for _, failureCase := range []string{"false_result", "rpc_error", "wrong_id", "missing_torrents", "null_torrents", "bad_torrents", "alias_conflict", "handshake_limit", "bad_session", "rate_limit", "redirect"} {
			t.Run(dialect+"-"+failureCase, func(t *testing.T) {
				var calls atomic.Int32
				s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls.Add(1)
					if failureCase == "handshake_limit" || failureCase == "bad_session" {
						if failureCase == "handshake_limit" {
							w.Header().Set("X-Transmission-Session-Id", "synthetic-csrf")
						}
						w.WriteHeader(409)
						return
					}
					if failureCase == "rate_limit" {
						w.WriteHeader(429)
						io.WriteString(w, "synthetic-key")
						return
					}
					if failureCase == "redirect" {
						w.Header().Set("Location", "http://127.0.0.1:1/synthetic-key")
						w.WriteHeader(302)
						return
					}
					raw, _ := io.ReadAll(r.Body)
					q := nativeJSON(t, raw)
					id := q["id"]
					if id == nil {
						id = q["tag"]
					}
					if failureCase == "wrong_id" {
						id = json.Number("9999999")
					}
					value := any(map[string]any{"torrents": []any{}})
					switch failureCase {
					case "false_result":
						value = false
					case "missing_torrents":
						value = map[string]any{}
					case "null_torrents":
						value = map[string]any{"torrents": nil}
					case "bad_torrents":
						value = map[string]any{"torrents": []any{nil}}
					case "alias_conflict":
						value = map[string]any{"torrents": []any{map[string]any{"hash_string": "one", "hashString": "different"}}}
					}
					if dialect == "legacy" {
						result := any("success")
						if failureCase == "rpc_error" || failureCase == "false_result" {
							result = false
						}
						reply(w, map[string]any{"tag": id, "result": result, "arguments": value})
					} else {
						out := map[string]any{"jsonrpc": "2.0", "id": id, "result": value}
						if failureCase == "rpc_error" {
							out["error"] = map[string]any{"code": 1, "message": "synthetic-key"}
						}
						reply(w, out)
					}
				}))
				defer s.Close()
				c := mustClient(t, "tr", s.URL)
				c.trDialect = dialect
				_, err := c.NativeTorrents(context.Background())
				if err == nil || strings.Contains(err.Error(), "synthetic-key") {
					t.Fatalf("protocol failure accepted/leaked: %v", err)
				}
				want := int32(1)
				if failureCase == "handshake_limit" {
					want = 3
				}
				if calls.Load() != want {
					t.Fatal("unexpected retry", calls.Load())
				}
			})
		}
	}
}
func TestNativeConcurrentClientsPreserveSessionAndRequestIDs(t *testing.T) {
	var mu sync.Mutex
	seen := map[string]bool{}
	count := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		q := nativeJSON(t, raw)
		id := q["id"].(json.Number).String()
		mu.Lock()
		defer mu.Unlock()
		if seen[id] {
			t.Error("concurrent duplicate request id")
		}
		seen[id] = true
		count++
		reply(w, map[string]any{"jsonrpc": "2.0", "id": q["id"], "result": map[string]any{"torrents": []any{}}})
	}))
	defer s.Close()
	c := mustClient(t, "tr", s.URL)
	c.trDialect = "jsonrpc"
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 3; j++ {
				if _, e := c.NativeTorrents(context.Background()); e != nil {
					t.Error(e)
				}
			}
		}()
	}
	wg.Wait()
	mu.Lock()
	defer mu.Unlock()
	if count != 36 {
		t.Fatal(count)
	}
}

// Test-only frozen seven-operation binding. The production App must bind this
// allowlist to its own stable instance/revision and mutation receipt; it must
// never expose an arbitrary endpoint/method proxy to a worker.
func nativeSeven(c *Client) cis.NativeCall {
	return func(ctx context.Context, op string, args map[string]any) (json.RawMessage, error) {
		qb := c.config.Kind == "qbittorrent"
		switch op {
		case "qb.torrents_info":
			if !qb || len(args) != 0 {
				return nil, errors.New("ungranted native operation")
			}
			return c.NativeTorrents(ctx)
		case "tr.torrent_get":
			if qb {
				return nil, errors.New("ungranted native operation")
			}
			fields, ok := args["arguments"].([]string)
			if !ok || !reflect.DeepEqual(fields, nativeTRFields) {
				return nil, errors.New("ungranted field list")
			}
			return c.NativeTorrents(ctx)
		case "qb.torrents_trackers":
			if !qb {
				return nil, errors.New("wrong kind")
			}
			hash, ok := args["torrent_hash"].(string)
			if !ok {
				return nil, errors.New("invalid hash type")
			}
			return c.NativeTrackers(ctx, hash)
		case "qb.torrents_add_tags", "tr.change_torrent":
			hashKey, tagKey := "torrent_hashes", "tags"
			if op == "tr.change_torrent" {
				if qb {
					return nil, errors.New("wrong kind")
				}
				hashKey, tagKey = "ids", "labels"
			} else if !qb {
				return nil, errors.New("wrong kind")
			}
			hash, ok := args[hashKey].(string)
			if !ok {
				return nil, errors.New("invalid hash type")
			}
			values, ok := args[tagKey].([]any)
			if !ok {
				return nil, errors.New("invalid tag type")
			}
			tags := []string{}
			for _, v := range values {
				tag, ok := v.(string)
				if !ok {
					return nil, errors.New("invalid tag element")
				}
				tags = append(tags, tag)
			}
			if e := c.SetTorrentTags(ctx, hash, tags); e != nil {
				return nil, e
			}
			return json.RawMessage(`null`), nil
		case "qb.torrents_delete", "tr.remove_torrent":
			hashKey, dataKey := "torrent_hashes", "delete_files"
			if op == "tr.remove_torrent" {
				if qb {
					return nil, errors.New("wrong kind")
				}
				hashKey, dataKey = "ids", "delete_data"
			} else if !qb {
				return nil, errors.New("wrong kind")
			}
			hash, ok := args[hashKey].(string)
			if !ok || args[dataKey] != false {
				return nil, errors.New("ungranted deletion")
			}
			if e := c.Control(ctx, hash, "remove_keep_data"); e != nil {
				return nil, e
			}
			return json.RawMessage(`null`), nil
		default:
			return nil, errors.New("ungranted native operation")
		}
	}
}
func TestNativeOriginalWrapperSevenOpsLoopback(t *testing.T) {
	for _, kind := range []string{"qbittorrent", "transmission"} {
		t.Run(kind, func(t *testing.T) {
			var trackers, tagged, removed atomic.Int32
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if kind == "qbittorrent" {
					switch r.URL.Path {
					case "/api/v2/auth/login":
						http.SetCookie(w, &http.Cookie{Name: "SID", Value: "synthetic", Path: "/"})
						io.WriteString(w, "Ok.")
					case "/api/v2/torrents/info":
						row := qbRow(nativeHash)
						row["category"] = "原生类别"
						row["content_path"] = "/downloads/原生文件"
						reply(w, []any{row})
					case "/api/v2/torrents/trackers":
						trackers.Add(1)
						reply(w, []any{map[string]any{"url": "https://synthetic.invalid/a", "status": 4, "msg": "torrent not registered", "tier": 0}})
					case "/api/v2/torrents/addTags":
						_ = r.ParseForm()
						if r.Form.Get("hashes") != nativeHash || r.Form.Get("tags") != "无效做种" {
							t.Error("qB append differs")
						}
						tagged.Add(1)
					case "/api/v2/torrents/delete":
						_ = r.ParseForm()
						if r.Form.Get("hashes") != nativeHash || r.Form.Get("deleteFiles") != "false" {
							t.Error("remove data requested")
						}
						removed.Add(1)
					default:
						t.Error("ungranted HTTP endpoint", r.URL.Path)
						http.NotFound(w, r)
					}
					return
				}
				b, _ := io.ReadAll(r.Body)
				q := nativeJSON(t, b)
				method := q["method"].(string)
				params := q["params"].(map[string]any)
				var result any = map[string]any{}
				switch method {
				case "session_get":
					result = map[string]any{"version": "4.1.0"}
				case "torrent_get":
					rows := nativeJSONList(t, nativeTRPayload(true))
					rows[0]["total_size"] = 1024
					result = map[string]any{"torrents": rows}
				case "torrent_set":
					if !reflect.DeepEqual(params["ids"], []any{nativeHash}) || !reflect.DeepEqual(params["labels"], []any{"旧标签", "无效做种"}) {
						t.Error("TR label replace differs")
					}
					tagged.Add(1)
				case "torrent_remove":
					if !reflect.DeepEqual(params["ids"], []any{nativeHash}) || params["delete_local_data"] != false {
						t.Error("TR delete data requested")
					}
					removed.Add(1)
				default:
					t.Error("ungranted RPC method", method)
				}
				reply(w, map[string]any{"jsonrpc": "2.0", "id": q["id"], "result": result})
			}))
			defer s.Close()
			c := mustClient(t, kind, s.URL)
			d := cis.NativeDownloader{Service: cis.Service{Name: "synthetic pinned", Kind: kind}, Call: nativeSeven(c)}
			ctx := context.Background()
			rows, flag, e := d.List(ctx)
			if e != nil || flag || len(rows) != 1 {
				t.Fatal("source wrapper list failed", flag, e)
			}
			hash, e := cis.Hash(rows[0], kind)
			if e != nil || hash != nativeHash {
				t.Fatal("source native identity changed", hash, e)
			}
			if kind == "qbittorrent" {
				m := cis.New(nil)
				for i := 0; i < 2; i++ {
					if got := m.TrackerInfo(rows[0], kind); len(got) != 1 {
						t.Fatal("source lazy tracker")
					}
				}
				if trackers.Load() != 6 {
					t.Fatal("source repeated getter count", trackers.Load())
				}
			}
			tags := []any{"无效做种"}
			if kind == "transmission" {
				tags = []any{"旧标签", "无效做种", "旧标签"}
			}
			result, e := d.SetTags(ctx, hash, tags)
			if e != nil || kind == "qbittorrent" && result != nil || kind == "transmission" && result != true {
				t.Fatal("source tag return", result, e)
			}
			result, e = d.Delete(ctx, hash, false)
			if e != nil || result != true {
				t.Fatal("source delete return", result, e)
			}
			if tagged.Load() != 1 || removed.Load() != 1 {
				t.Fatal("native effects missing", tagged.Load(), removed.Load())
			}
			if _, e = d.Call(ctx, "arbitrary.rpc", map[string]any{}); e == nil {
				t.Fatal("generic RPC escaped")
			}
			if _, e = d.Call(ctx, map[bool]string{true: "qb.torrents_delete", false: "tr.remove_torrent"}[kind == "qbittorrent"], map[string]any{"torrent_hashes": nativeHash, "delete_files": true, "ids": nativeHash, "delete_data": true}); e == nil {
				t.Fatal("data deletion escaped grant")
			}
		})
	}
}

func TestNativeTagFailureRetainsOriginalWrapperReturn(t *testing.T) {
	for _, kind := range []string{"qbittorrent", "transmission"} {
		t.Run(kind, func(t *testing.T) {
			var attempts atomic.Int32
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if kind == "qbittorrent" {
					if strings.HasSuffix(r.URL.Path, "/auth/login") {
						http.SetCookie(w, &http.Cookie{Name: "SID", Value: "synthetic", Path: "/"})
						io.WriteString(w, "Ok.")
						return
					}
					attempts.Add(1)
					io.WriteString(w, "false")
					return
				}
				raw, _ := io.ReadAll(r.Body)
				q := nativeJSON(t, raw)
				if q["method"] == "session_get" {
					reply(w, map[string]any{"jsonrpc": "2.0", "id": q["id"], "result": map[string]any{"version": "4.1.0"}})
					return
				}
				attempts.Add(1)
				reply(w, map[string]any{"jsonrpc": "2.0", "id": q["id"], "result": false})
			}))
			defer s.Close()
			c := mustClient(t, kind, s.URL)
			if e := c.SetTorrentTags(context.Background(), nativeHash, []string{"无效做种"}); e == nil {
				t.Fatal("client accepted explicit false")
			}
			d := cis.NativeDownloader{Service: cis.Service{Name: "synthetic", Kind: kind}, Call: nativeSeven(c)}
			result, e := d.SetTags(context.Background(), nativeHash, []any{"无效做种"})
			if e != nil || kind == "qbittorrent" && result != nil || kind == "transmission" && result != false {
				t.Fatal("source swallowing/false semantics changed", result, e)
			}
			if attempts.Load() != 2 {
				t.Fatal("uncertain/false mutation was retried", attempts.Load())
			}
		})
	}
}
func TestNativeTransmissionInFlightCancellation(t *testing.T) {
	started := make(chan struct{})
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		close(started)
		<-r.Context().Done()
	}))
	defer s.Close()
	c := mustClient(t, "tr", s.URL)
	c.trDialect = "jsonrpc"
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, e := c.NativeTorrents(ctx); done <- e }()
	<-started
	cancel()
	select {
	case e := <-done:
		if !errors.Is(e, context.Canceled) {
			t.Fatal(e)
		}
	case <-time.After(time.Second):
		t.Fatal("TR cancellation blocked")
	}
}

func TestNativeSnapshotDoesNotInheritTaskRowCap(t *testing.T) {
	rows := strings.Repeat(`{"hash":"`+nativeHash+`"},`, 10000) + `{"hash":"` + nativeHash + `"}`
	// Native transport preserves daemon objects, including duplicates; plugin
	// semantics, not the download-page Task projection, decide their meaning.
	raw := []byte("[" + rows + "]")
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(raw) }))
	defer s.Close()
	c, _ := New(Config{ID: "synthetic", Kind: "qb", BaseURL: s.URL})
	out, err := c.NativeTorrents(context.Background())
	if err != nil || !bytes.Equal(out, raw) {
		t.Fatal("native list was projected/capped", err)
	}
}
