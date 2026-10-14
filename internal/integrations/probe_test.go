package integrations

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"
)

const syntheticHash = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, e := os.ReadFile("../../testdata/protocol/synthetic-" + name)
	if e != nil {
		t.Fatal(e)
	}
	return b
}
func TestQBReadOnlyCookieAndRedaction(t *testing.T) {
	paths := map[string]int{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths[r.URL.Path]++
		if r.URL.Path == "/prefix/api/v2/auth/login" {
			if r.Method != "POST" {
				t.Error("not login POST")
			}
			_ = r.ParseForm()
			if r.Form.Get("password") != "secret-password" {
				t.Error("password missing")
			}
			http.SetCookie(w, &http.Cookie{Name: "SID", Value: "secret-cookie", Path: "/"})
			fmt.Fprint(w, "Ok.")
			return
		}
		if r.Method != "GET" {
			t.Errorf("unexpected mutation %s", r.Method)
		}
		c, e := r.Cookie("SID")
		if e != nil || c.Value != "secret-cookie" {
			t.Error("cookie not reused")
		}
		switch r.URL.Path {
		case "/prefix/api/v2/app/version":
			fmt.Fprint(w, "v5.0.0")
		case "/prefix/api/v2/app/webapiVersion":
			fmt.Fprint(w, "2.11.0")
		case "/prefix/api/v2/app/preferences":
			fmt.Fprint(w, `{"save_path":"/private/secret","temp_path":"/private/tmp","web_ui_password":"never-print"}`)
		case "/prefix/api/v2/torrents/info":
			if r.URL.Query().Get("limit") != "1" {
				t.Error("missing bounded sample")
			}
			fmt.Fprintf(w, `[{"hash":%q}]`, syntheticHash)
		case "/prefix/api/v2/torrents/files":
			if r.URL.Query().Get("hash") != syntheticHash {
				t.Error("wrong hash")
			}
			w.Write(fixture(t, "qb-files.json"))
		default:
			t.Error("unexpected endpoint")
			http.Error(w, "no", 404)
		}
	}))
	defer srv.Close()
	result := ProbeDownloader(context.Background(), Downloader{ID: "qb1", Kind: "qbittorrent", BaseURL: srv.URL + "/prefix", Username: "synthetic-user", Password: "secret-password"})
	if result.Status != "ok" || result.FileCount != 1 || result.Requests != 6 {
		t.Fatalf("%+v", result)
	}
	b, _ := json.Marshal(result)
	for _, secret := range []string{"secret-password", "secret-cookie", "/private", syntheticHash, srv.URL, "never-print"} {
		if strings.Contains(string(b), secret) {
			t.Fatal("leaked private field")
		}
	}
	for _, c := range result.Capabilities {
		if c.WriteVerified {
			t.Fatal("write claimed verified")
		}
	}
}
func TestQBFailures(t *testing.T) {
	for _, tc := range []struct {
		name, body, want string
		code             int
	}{{"expired", "Fails.", "authentication_failed", 200}, {"forbidden", "denied", "forbidden", 403}, {"rate", "slow", "rate_limited", 429}, {"unauth", "no", "authentication_failed", 401}, {"oversize", strings.Repeat("x", MaxBody+1), "body_limit", 200}} {
		t.Run(tc.name, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(tc.code); fmt.Fprint(w, tc.body) }))
			defer s.Close()
			r := ProbeDownloader(context.Background(), Downloader{ID: "qb", Kind: "qbittorrent", BaseURL: s.URL})
			if r.Status != tc.want {
				t.Fatalf("%+v", r)
			}
		})
	}
}
func TestCrossOriginRedirectNeverLeaks(t *testing.T) {
	contacted := false
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { contacted = true }))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer source.Close()
	r := ProbeSite(context.Background(), Site{ID: "site", Kind: "mteam", BaseURL: source.URL, APIKey: "secret"})
	if r.Status != "redirect_blocked" || contacted {
		t.Fatalf("redirect forwarded: %+v contacted=%v", r, contacted)
	}
}
func TestTransmissionDialectsAnd409(t *testing.T) {
	for _, modern := range []bool{false, true} {
		t.Run(fmt.Sprint(modern), func(t *testing.T) {
			handshake := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/proxy/rpc" || r.Method != "POST" {
					t.Error("wrong endpoint/method")
				}
				user, pass, ok := r.BasicAuth()
				if !ok || user != "user" || pass != "password" {
					t.Error("basic auth missing")
				}
				if r.Header.Get("X-Transmission-Session-Id") == "" {
					handshake++
					w.Header().Set("X-Transmission-Session-Id", "synthetic-session")
					w.WriteHeader(409)
					return
				}
				var req map[string]json.RawMessage
				_ = json.NewDecoder(r.Body).Decode(&req)
				var method string
				_ = json.Unmarshal(req["method"], &method)
				if method == "session-get" {
					if modern {
						fmt.Fprint(w, `{"result":"success","arguments":{"version":"4.1.0","rpc-version":18}}`)
					} else {
						w.Write(fixture(t, "tr-old-session.json"))
					}
					return
				}
				if method == "session_get" && modern {
					w.Write(fixture(t, "tr-new-session.json"))
					return
				}
				wantMethod := "torrent-get"
				argKey := "arguments"
				hashKey := "hashString"
				if modern {
					wantMethod = "torrent_get"
					argKey = "params"
					hashKey = "hash_string"
				}
				if method != wantMethod {
					t.Errorf("non-read/incorrect method %s", method)
					http.Error(w, "bad", 400)
					return
				}
				var args map[string]json.RawMessage
				_ = json.Unmarshal(req[argKey], &args)
				var fields []string
				_ = json.Unmarshal(args["fields"], &fields)
				data := map[string]any{"torrents": []any{map[string]any{hashKey: syntheticHash, "file_count": 1}}}
				if slices.Contains(fields, "files") {
					var ids []string
					if json.Unmarshal(args["ids"], &ids) != nil || len(ids) != 1 || ids[0] != syntheticHash {
						t.Error("files not scoped to hash")
					}
					data = map[string]any{"torrents": []any{map[string]any{hashKey: syntheticHash, "files": []any{map[string]any{"name": "synthetic.mkv", "length": 1024}}}}}
				}
				if modern {
					json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "result": data, "id": 1})
				} else {
					json.NewEncoder(w).Encode(map[string]any{"result": "success", "arguments": data, "tag": 1})
				}
			}))
			defer srv.Close()
			r := ProbeDownloader(context.Background(), Downloader{ID: "tr", Kind: "transmission", BaseURL: srv.URL + "/proxy/rpc", Username: "user", Password: "password"})
			if r.Status != "ok" || r.FileCount != 1 || handshake != 1 {
				t.Fatalf("%+v handshake=%d", r, handshake)
			}
		})
	}
}
func TestTransmissionErrors(t *testing.T) {
	for _, tc := range []struct {
		name, body, want string
		code             int
	}{{"nonjson", "not json", "protocol_changed", 200}, {"business", `{"result":"permission denied","arguments":{}}`, "business_error", 200}, {"409missing", "", "protocol_changed", 409}, {"409loop", "", "session_conflict", 409}} {
		t.Run(tc.name, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.name == "409loop" {
					w.Header().Set("X-Transmission-Session-Id", "still-invalid")
				}
				w.WriteHeader(tc.code)
				fmt.Fprint(w, tc.body)
			}))
			defer s.Close()
			r := ProbeDownloader(context.Background(), Downloader{ID: "tr", Kind: "transmission", BaseURL: s.URL})
			if r.Status != tc.want {
				t.Fatalf("%+v", r)
			}
		})
	}
}
func TestSitesSyntheticShapes(t *testing.T) {
	for _, kind := range []string{"ubits", "jiayuan", "1ptba"} {
		t.Run(kind, func(t *testing.T) {
			requests := 0
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				if r.Header.Get("Cookie") != "synthetic-cookie" {
					t.Error("cookie missing")
				}
				if r.Method != "GET" {
					t.Error("write")
				}
				w.Write(fixture(t, "nexus-search.html"))
			}))
			defer s.Close()
			r := ProbeSite(context.Background(), Site{ID: "site", Kind: kind, BaseURL: s.URL, Cookie: "synthetic-cookie", Query: "example"})
			if r.Status != "ok" || r.SampleCount != 1 || requests != 2 {
				t.Fatalf("%+v", r)
			}
		})
	}
}
func TestSiteErrorsNotEmpty(t *testing.T) {
	for _, tc := range []struct {
		name, body, want string
		code             int
	}{{"login", `<input type="password">`, "authentication_failed", 200}, {"challenge", `<html>cf-chl-test</html>`, "login_challenge", 403}, {"forbidden", "denied", "forbidden", 403}, {"rate", "slow", "rate_limited", 429}, {"template", "<html>new layout</html>", "protocol_changed", 200}} {
		t.Run(tc.name, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(tc.code); fmt.Fprint(w, tc.body) }))
			defer s.Close()
			r := ProbeSite(context.Background(), Site{ID: "s", Kind: "ubits", BaseURL: s.URL, Cookie: "secret"})
			if r.Status != tc.want {
				t.Fatalf("%+v", r)
			}
		})
	}
}
func TestMTeamAPIKeyAndEmpty(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "secret" || r.Header.Get("Cookie") != "" {
			t.Error("auth mismatch")
		}
		switch r.URL.Path {
		case "/api/member/profile":
			fmt.Fprint(w, `{"code":"0","data":{"id":"7"}}`)
		case "/api/torrent/search":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["pageSize"] != float64(10) || body["pageNumber"] != float64(1) {
				t.Error("search not bounded")
			}
			fmt.Fprint(w, `{"code":"0","data":{"data":[]}}`)
		default:
			t.Error("unexpected endpoint")
		}
	}))
	defer s.Close()
	r := ProbeSite(context.Background(), Site{ID: "mt", Kind: "mteam", BaseURL: s.URL, APIKey: "secret", Query: "example"})
	if r.Status != "empty_result" || r.Requests != 2 {
		t.Fatalf("%+v", r)
	}
	for _, b := range []string{`{"code":500,"data":{}}`, `<html>business failure</html>`, `{"message":"success"}`} {
		_, status := mteamEnvelope([]byte(b))
		if status == "" {
			t.Fatal("200 body accepted")
		}
	}
}
func TestIdentityAndInvalidConfig(t *testing.T) {
	if (TaskIdentity{"one", syntheticHash}).Fingerprint() == (TaskIdentity{"two", syntheticHash}).Fingerprint() {
		t.Fatal("identity not scoped")
	}
	if (TaskIdentity{"one", syntheticHash}).Fingerprint() != (TaskIdentity{"one", strings.ToUpper(syntheticHash)}).Fingerprint() {
		t.Fatal("hash not normalized")
	}
	for _, raw := range []string{"https://user:pass@example.invalid", "https://example.invalid?token=secret", "file:///private"} {
		r := ProbeDownloader(context.Background(), Downloader{ID: "x", Kind: "qbittorrent", BaseURL: raw})
		if r.Status != "invalid_input" || r.Requests != 0 {
			t.Fatal("unsafe configuration")
		}
	}
}

func TestTimeoutAndModernBusinessError(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"error":{"code":-32601,"message":"secret failure"}}`)
	}))
	defer s.Close()
	trn, _ := newTransport(s.URL)
	session := ""
	_, status := rpc(context.Background(), trn, Downloader{}, &session, true, "session_get", nil)
	if status != "business_error" {
		t.Fatal(status)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, status = trn.request(ctx, "GET", s.URL, nil, nil, "", "")
	if status != "transport_error" {
		t.Fatal(status)
	}
}
func TestNexusEmptyVsChanged(t *testing.T) {
	for _, tc := range []struct{ text, want string }{{"No torrents found", "empty_result"}, {"New template", "protocol_changed"}} {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, `<a href="logout.php">Exit</a><a href="userdetails.php?id=1">User</a><table class="torrents">`+tc.text+`</table>`)
		}))
		r := ProbeSite(context.Background(), Site{ID: "s", Kind: "1ptba", BaseURL: s.URL, Cookie: "synthetic", Query: "example"})
		s.Close()
		if r.Status != tc.want {
			t.Fatalf("%+v", r)
		}
	}
}
