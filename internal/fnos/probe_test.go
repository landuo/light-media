package fnos

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestReadOnlyProbeAndExactSignature(t *testing.T) {
	var requests []string
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		a, _ := url.ParseQuery(r.Header.Get("authx"))
		want := Signature(r.URL.Path, string(b), a.Get("nonce"), a.Get("timestamp"))
		if want != r.Header.Get("authx") {
			t.Error("signature differs from exact request bytes")
		}
		requests = append(requests, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v/api/v1/sys/version":
			fmt.Fprint(w, `{"code":0,"data":{"version":"1.0-synthetic","mediasrvVersion":"1.0-synthetic"}}`)
		case "/v/api/v1/login":
			fmt.Fprint(w, `{"code":0,"data":{"token":"secret-test-token"}}`)
		case "/v/api/v1/user/info":
			if r.Header.Get("Authorization") != "secret-test-token" {
				t.Error("missing token")
			}
			fmt.Fprint(w, `{"code":0,"data":{"is_admin":1}}`)
		case "/v/api/v1/mdb/list":
			fmt.Fprint(w, `{"code":0,"data":[{"guid":"synthetic-id","name":"Test","category":"TV","dir_list":["/synthetic"]}]}`)
		case "/v/api/v1/item/list":
			fmt.Fprint(w, `{"code":0,"data":{"list":[{"type":"TV","title":"Synthetic","trim_id":"tt123"}]}}`)
		default:
			t.Errorf("unexpected operation: %s", r.URL.Path)
			http.Error(w, "unexpected", 404)
		}
	}))
	defer s.Close()
	r := Probe(context.Background(), Config{Host: s.URL + "/v", Username: "secret-user", Password: "secret-password"})
	if r.Status != "passed" || r.Requests != 5 || !r.Authenticated || !r.Admin || r.WriteOperationsTested || r.NFOImportTested {
		t.Fatalf("unexpected summary: %+v", r)
	}
	b, _ := json.Marshal(r)
	for _, secret := range []string{"secret-user", "secret-password", "secret-test-token"} {
		if strings.Contains(string(b), secret) {
			t.Error("credential leak")
		}
	}
	for _, v := range requests {
		if strings.Contains(v, "scan") || strings.Contains(v, "DELETE") {
			t.Error("write operation called")
		}
	}
}

func TestFailureIsSanitizedAndNeverPassed(t *testing.T) {
	for _, body := range []string{`{"code":401,"msg":"secret-cookie"}`, `{"data":{}}`, `secret-password`} {
		t.Run(body, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, body) }))
			defer s.Close()
			r := Probe(context.Background(), Config{Host: s.URL, Username: "secret-user", Password: "secret-password"})
			b, _ := json.Marshal(r)
			if r.Status == "passed" || strings.Contains(string(b), "secret-") {
				t.Fatalf("bad result: %s", b)
			}
		})
	}
}

func TestRedirectDoesNotForwardCredentials(t *testing.T) {
	hit := false
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hit = true }))
	defer target.Close()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 302) }))
	defer s.Close()
	r := Probe(context.Background(), Config{Host: s.URL, Username: "user", Password: "password"})
	if hit || r.Code != "HTTP_302" {
		t.Fatalf("redirect followed: %+v", r)
	}
}

// All responses below are invented synthetic protocol samples.
func syntheticServer(t *testing.T, overrides map[string]string) *httptest.Server {
	t.Helper()
	defaults := map[string]string{
		"/sys/version": `{"code":0,"data":{"version":"1.0-synthetic","mediasrvVersion":"1.0-synthetic"}}`,
		"/login":       `{"code":0,"data":{"token":"synthetic-private-token"}}`,
		"/user/info":   `{"code":0,"data":{"is_admin":1}}`,
		"/mdb/list":    `{"code":0,"data":[{"guid":"lib-1","name":"Synthetic","category":"TV","dir_list":["/synthetic"]}]}`,
		"/item/list":   `{"code":0,"data":{"list":[{"type":"TV","title":"Synthetic","season_number":1,"episode_number":2}]}}`,
	}
	for k, v := range overrides {
		defaults[k] = v
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/v/api/v1")
		b, ok := defaults[p]
		if !ok {
			t.Errorf("unexpected endpoint %s", p)
			http.Error(w, "bad", 400)
			return
		}
		fmt.Fprint(w, b)
	}))
}
func TestNullMissingAndWrongTypeNeverPass(t *testing.T) {
	cases := []struct{ path, body string }{
		{"/sys/version", `{"code":0,"data":null}`}, {"/sys/version", `{"code":0,"data":{}}`}, {"/sys/version", `{"code":0,"data":{"version":"secret-cookie","mediasrvVersion":"1.0"}}`},
		{"/login", `{"code":0,"data":null}`}, {"/login", `{"code":0,"data":{}}`},
		{"/user/info", `{"code":0,"data":null}`}, {"/user/info", `{"code":0,"data":{}}`}, {"/user/info", `{"code":0,"data":{"is_admin":3}}`},
		{"/mdb/list", `{"code":0,"data":null}`}, {"/mdb/list", `{"code":0,"data":[{}]}`},
		{"/item/list", `{"code":0,"data":{}}`}, {"/item/list", `{"code":0,"data":{"list":null}}`}, {"/item/list", `{"code":0,"data":{"list":[{"type":"TV","title":"Synthetic","season_number":{"token":"hidden"}}]}}`},
	}
	for _, tc := range cases {
		t.Run(tc.path+tc.body, func(t *testing.T) {
			s := syntheticServer(t, map[string]string{tc.path: tc.body})
			defer s.Close()
			r := Probe(context.Background(), Config{Host: s.URL, Username: "user-secret", Password: "password-secret"})
			if r.Status == "passed" || r.Code == "" {
				t.Fatalf("invalid accepted %+v", r)
			}
		})
	}
}
func TestSampleLimitsAndCredentialRedaction(t *testing.T) {
	libs := []map[string]any{}
	for i := 0; i < 9; i++ {
		libs = append(libs, map[string]any{"guid": fmt.Sprint(i), "name": "Library 用户 user-secret", "category": "TV", "dir_list": []string{"/synthetic/password-secret/影视", strings.Repeat("x", 1500)}})
	}
	b, _ := json.Marshal(map[string]any{"code": 0, "data": libs})
	items := []map[string]any{{"type": "TV", "title": "你好 synthetic-private-token password-secret user-secret", "season_number": 1}, {"type": "TV", "title": "must-not-emit-second-sample"}}
	p, _ := json.Marshal(map[string]any{"code": 0, "data": map[string]any{"list": items}})
	s := syntheticServer(t, map[string]string{"/mdb/list": string(b), "/item/list": string(p)})
	defer s.Close()
	r := Probe(context.Background(), Config{Host: s.URL, Username: "user-secret", Password: "password-secret"})
	if r.Status != "passed" || !r.Truncated || len(r.Libraries) != 8 || r.Requests != 12 {
		t.Fatalf("%+v", r)
	}
	output, _ := json.Marshal(r)
	for _, secret := range []string{"user-secret", "password-secret", "synthetic-private-token", "must-not-emit-second-sample"} {
		if strings.Contains(string(output), secret) {
			t.Fatal("remote secret/sample leaked")
		}
	}
	if !strings.Contains(string(output), "你好") || !strings.Contains(string(output), "影视") {
		t.Fatal("readable content lost")
	}
	for _, l := range r.Libraries {
		if len(l.Samples) != 1 || len([]rune(l.Directories[1])) > 1025 {
			t.Fatal("local bounds missing")
		}
	}
}
func TestRequestLimit(t *testing.T) {
	called := false
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }))
	defer s.Close()
	c := client{host: s.URL, http: s.Client(), requests: 20}
	_, e := c.request(context.Background(), "/sys/version", nil)
	if e == nil || e.Error() != "REQUEST_LIMIT" || called || c.requests != 20 {
		t.Fatal("request limit bypass")
	}
}

func TestSearchDecodedQuerySignatureAndLocalLimit(t *testing.T) {
	titles := []string{"夜王", "生活 大爆炸+&=", "诛仙"}
	searchCalls := 0
	base := syntheticServer(t, nil)
	defer base.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v/api/v1/search/list" {
			req, _ := http.NewRequestWithContext(r.Context(), r.Method, base.URL+r.URL.RequestURI(), r.Body)
			req.Header = r.Header.Clone()
			resp, e := base.Client().Do(req)
			if e != nil {
				t.Error(e)
				return
			}
			defer resp.Body.Close()
			w.WriteHeader(resp.StatusCode)
			io.Copy(w, resp.Body)
			return
		}
		searchCalls++
		if r.Method != "GET" {
			t.Error("search not GET")
		}
		if r.Header.Get("Authorization") != "synthetic-private-token" {
			t.Error("missing auth")
		}
		q := r.URL.Query().Get("q")
		if q != titles[searchCalls-1] {
			t.Error("query changed")
		}
		auth, _ := url.ParseQuery(r.Header.Get("authx"))
		want := Signature("/v/api/v1/search/list", "q="+q, auth.Get("nonce"), auth.Get("timestamp"))
		if r.Header.Get("authx") != want {
			t.Error("incorrect decoded query signature")
		}
		if strings.Contains(r.URL.RawQuery, "夜") {
			t.Error("wire query not escaped")
		}
		body, _ := io.ReadAll(r.Body)
		if len(body) != 0 {
			t.Error("GET body present")
		}
		if searchCalls == 3 {
			fmt.Fprint(w, `{"code":0,"data":[]}`)
			return
		}
		rows := []map[string]any{}
		for i := 0; i < 4; i++ {
			rows = append(rows, map[string]any{"type": "Episode", "title": q + " user-secret password-secret synthetic-private-token", "trim_id": "tm123", "season_number": 2, "episode_number": i + 1})
		}
		json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": rows})
	}))
	defer server.Close()
	r := Probe(context.Background(), Config{Host: server.URL, Username: "user-secret", Password: "password-secret", SampleTitles: titles})
	if r.Status != "passed" || searchCalls != 3 || r.Requests != 8 || len(r.SearchSamples) != 3 || !r.Truncated {
		t.Fatalf("%+v", r)
	}
	if len(r.SearchSamples[0].Samples) != 3 || *r.SearchSamples[0].Samples[0].Season != 2 || *r.SearchSamples[0].Samples[0].Episode != 1 || r.SearchSamples[0].Samples[0].SourceID != "tm123" {
		t.Fatal("identity/known numbers altered")
	}
	if r.SearchSamples[2].Status != "empty_result" || len(r.SearchSamples[2].Samples) != 0 {
		t.Fatal("empty misclassified")
	}
	b, _ := json.Marshal(r)
	for _, secret := range []string{"user-secret", "password-secret", "synthetic-private-token"} {
		if strings.Contains(string(b), secret) {
			t.Fatal("search secret leaked")
		}
	}
}
func TestSearchShapeAndConfigValidation(t *testing.T) {
	for _, body := range []string{`{"code":0,"data":null}`, `{"code":0,"data":{}}`, `{"code":0,"data":[{}]}`, `{"code":0,"data":[{"type":"TV","title":"Synthetic","season_number":"unknown"}]}`} {
		s := syntheticServer(t, map[string]string{"/search/list": body})
		r := Probe(context.Background(), Config{Host: s.URL, Username: "user-secret", Password: "password-secret", SampleTitles: []string{"诛仙"}})
		s.Close()
		if r.Status == "passed" {
			t.Fatalf("bad search accepted %+v", r)
		}
	}
	for _, titles := range [][]string{{"1", "2", "3", "4"}, {""}, {strings.Repeat("夜", 257)}} {
		r := Probe(context.Background(), Config{Host: "https://example.invalid", SampleTitles: titles})
		if r.Status != "failed" || r.Requests != 0 {
			t.Fatal("invalid input contacted service")
		}
	}
}
