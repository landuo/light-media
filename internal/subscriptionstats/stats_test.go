package subscriptionstats

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
)

type oracle struct {
	Helper []struct {
		Share         bool              `json:"share"`
		Status        *int              `json:"status"`
		UID           *string           `json:"uid"`
		Kind          string            `json:"kind"`
		Returned      bool              `json:"returned"`
		AsyncReturned bool              `json:"async_returned"`
		Requests      []json.RawMessage `json:"requests"`
	} `json:"helper_cases"`
	Payload []struct {
		Name             string `json:"name"`
		Subscribe, Media json.RawMessage
		Body             string `json:"body"`
	} `json:"payload_cases"`
	Registration []struct {
		Name   string          `json:"name"`
		Fields json.RawMessage `json:"fields"`
		Body   string          `json:"body"`
	} `json:"registration_cases"`
}

func fixture(t *testing.T) oracle {
	t.Helper()
	b, e := os.ReadFile("testdata/original.json")
	if e != nil {
		t.Fatal(e)
	}
	var out oracle
	if e = json.Unmarshal(b, &out); e != nil {
		t.Fatal(e)
	}
	return out
}
func boolPtr(v bool) *bool { return &v }
func ownedSettings(host string) SettingsSnapshot {
	return SettingsSnapshot{Share: boolPtr(true), Known: true, ServerHost: host, UserAgent: "Owned-UA", Environment: []string{}}
}
func ownedGuard(context.Context, *url.URL) error { return nil }

func TestOriginalCompletionPayloads(t *testing.T) {
	for _, tc := range fixture(t).Payload {
		t.Run(tc.Name, func(t *testing.T) {
			body, e := BuildCompletion(tc.Subscribe, tc.Media)
			if e != nil || string(body) != tc.Body {
				t.Fatalf("source payload mismatch: %v\nactual %s\nsource %s", e, body, tc.Body)
			}
		})
	}
}
func TestOriginalRegistrationPayloads(t *testing.T) {
	for _, tc := range fixture(t).Registration {
		t.Run(tc.Name, func(t *testing.T) {
			body, e := BuildRegistration(tc.Fields)
			if e != nil || string(body) != tc.Body {
				t.Fatalf("source payload mismatch: %v\nactual %s\nsource %s", e, body, tc.Body)
			}
		})
	}
}

func TestOriginalHelperResultsThroughActualHTTP(t *testing.T) {
	for i, tc := range fixture(t).Helper {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				body, _ := io.ReadAll(r.Body)
				if r.Method != "POST" || r.URL.Path != "/base/subscribe/"+tc.Kind || string(body) != "{\"owned\": \"fixture\"}" || r.Header.Get("Content-Type") != "application/json" || r.UserAgent() != "Owned-UA" {
					t.Errorf("unexpected owned request")
				}
				uid := ""
				if tc.UID != nil {
					uid = *tc.UID
				}
				if r.Header.Get("X-MoviePilot-User-Uid") != uid {
					t.Errorf("source UID mismatch")
				}
				if tc.Status == nil {
					conn, _, e := w.(http.Hijacker).Hijack()
					if e != nil {
						t.Error(e)
						return
					}
					conn.Close()
					return
				}
				w.WriteHeader(*tc.Status)
				_, _ = w.Write([]byte("{}"))
			}))
			defer srv.Close()
			setting := ownedSettings(srv.URL + "/base///")
			setting.Share = boolPtr(tc.Share)
			setting.UserUID = tc.UID
			c := Client{Guard: ownedGuard}
			var got Result
			var e error
			if tc.Kind == "done" {
				got, e = c.SubDone(context.Background(), setting, []byte("{\"owned\": \"fixture\"}"))
			} else {
				got, e = c.SubReg(context.Background(), setting, []byte("{\"owned\": \"fixture\"}"))
			}
			if e != nil || !got.SourceReturned || got.SourceValue != tc.Returned || got.SourceError != "" {
				t.Fatalf("source result mismatch: %#v %v", got, e)
			}
			if int(calls.Load()) != len(tc.Requests) {
				t.Fatalf("source lazy call/no POST retry mismatch: %d vs %d", calls.Load(), len(tc.Requests))
			}
			if tc.Share && tc.Status == nil && got.Outcome != "unconfirmed" {
				t.Fatalf("missing response must not be ACK: %#v", got)
			}
		})
	}
}

func TestActualExplicitProxyOverridesEnvironmentBypass(t *testing.T) {
	var proxyCalls atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxyCalls.Add(1)
		if r.URL.Host != "owned.invalid" || r.URL.Path != "/subscribe/done" {
			t.Error("proxy target differs")
		}
		w.WriteHeader(200)
	}))
	defer proxy.Close()
	s := ownedSettings("http://owned.invalid")
	s.Environment = []string{"no_proxy=*", "HTTP_PROXY=http://unused.invalid:1"}
	s.Proxies = map[string]string{"http": proxy.URL, "https": "http://unused-https.invalid:2"}
	r, e := (Client{Guard: ownedGuard}).SubDone(context.Background(), s, []byte("{}"))
	if e != nil || !r.SourceValue || proxyCalls.Load() != 1 {
		t.Fatalf("explicit proxy not used: %#v %v calls=%d", r, e, proxyCalls.Load())
	}
}

func TestActualTLSAndSameOriginRedirect(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path == "/subscribe/done" {
			http.SetCookie(w, &http.Cookie{Name: "owned", Value: "yes", Path: "/"})
			w.Header().Set("Location", "/complete")
			w.WriteHeader(307)
			return
		}
		c, e := r.Cookie("owned")
		if e != nil || c.Value != "yes" {
			t.Error("source session cookie was lost")
		}
		if r.Method != "POST" {
			t.Error("307 changed method")
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()
	r, e := (Client{Guard: ownedGuard}).SubDone(context.Background(), ownedSettings(srv.URL), []byte("{}"))
	if e != nil || !r.SourceValue || calls.Load() != 2 {
		t.Fatalf("scoped source TLS/redirect failed: %#v %v", r, e)
	}
}

func TestForeignRedirectAndLostReplyAreNotNativeSuccess(t *testing.T) {
	var foreignCalls atomic.Int32
	foreign := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { foreignCalls.Add(1) }))
	defer foreign.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", foreign.URL)
		w.WriteHeader(307)
	}))
	defer srv.Close()
	r, e := (Client{Guard: ownedGuard}).SubDone(context.Background(), ownedSettings(srv.URL), []byte("{}"))
	if !errors.Is(e, ErrRedirect) || r.SourceReturned || foreignCalls.Load() != 0 {
		t.Fatalf("unbound redirect escaped: %#v %v", r, e)
	}
}

func TestNativeDependencyAndCancellationDoNotBecomeSourceFalse(t *testing.T) {
	for _, s := range []SettingsSnapshot{{}, {Share: boolPtr(true)}} {
		r, e := (Client{Guard: ownedGuard}).SubDone(context.Background(), s, nil)
		if !errors.Is(e, ErrDependency) || r.SourceReturned {
			t.Fatalf("unknown source settings hidden: %#v %v", r, e)
		}
	}
	r, e := (Client{}).SubDone(context.Background(), SettingsSnapshot{Share: boolPtr(false)}, nil)
	if e != nil || !r.SourceReturned || r.SourceValue || r.Outcome != "disabled" {
		t.Fatal(r, e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r, e = (Client{Guard: ownedGuard}).SubDone(ctx, ownedSettings("http://owned.invalid"), nil)
	if !errors.Is(e, context.Canceled) || r.SourceReturned {
		t.Fatal(r, e)
	}
	s := ownedSettings("http://owned.invalid")
	s.UserAgent = "不支持的HTTP头"
	r, e = (Client{Guard: ownedGuard}).SubDone(context.Background(), s, nil)
	if e != nil || r.SourceReturned || r.SourceError != "UnicodeEncodeError" {
		t.Fatal(r, e)
	}
}

func TestPayloadMissingAttributesDoNotInventNone(t *testing.T) {
	base := fixture(t).Payload[0]
	for _, key := range []string{"tmdb_id", "douban_id", "bangumi_id", "anilist_id"} {
		var m map[string]json.RawMessage
		_ = json.Unmarshal(base.Media, &m)
		delete(m, key)
		b, _ := json.Marshal(m)
		if _, e := BuildCompletion(base.Subscribe, b); !errors.Is(e, ErrDependency) {
			t.Fatal("missing source attribute accepted", key, e)
		}
	}
	for _, raw := range []string{"null", "[]", "{} {}", strings.Repeat("{", 3)} {
		if _, e := BuildRegistration(json.RawMessage(raw)); !errors.Is(e, ErrDependency) {
			t.Fatal("invalid source fields accepted")
		}
	}
}

func TestSourceJSONInvalidIsCaughtOnlyInTheStatisticsChild(t *testing.T) {
	for _, literal := range []string{"1e999", "-1e999"} {
		media := json.RawMessage(`{"tmdb_id":` + literal + `,"douban_id":null,"bangumi_id":null,"anilist_id":null}`)
		_, err := BuildCompletion(json.RawMessage(`{"media_source":"themoviedb","media_id":"owned","season":1}`), media)
		result, caught := CaughtJSONResult(err)
		if !caught || !result.SourceReturned || result.SourceValue || result.Outcome != "not_requested" {
			t.Fatal("requests InvalidJSONError was not caught", result, err)
		}
	}
	if _, caught := CaughtJSONResult(ErrDependency); caught {
		t.Fatal("native missing provenance masqueraded as source false")
	}
}
