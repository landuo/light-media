package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"light-media/internal/plugins"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestSharedOCRRedirectSourceAndCookieIsolation(t *testing.T) {
	var revoke, leak atomic.Bool
	var images, posts atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		images.Add(1)
		if r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" {
			leak.Store(true)
		}
		_, _ = io.WriteString(w, "B")
	}))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("Cookie"), "owned=cookie") {
			t.Error("initial cookie missing")
		}
		http.SetCookie(w, &http.Cookie{Name: "sourceOnly", Value: "owned", Path: "/"})
		if r.URL.Path == "/revoke" {
			revoke.Store(true)
		}
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer source.Close()
	post := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		posts.Add(1)
		if r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" {
			leak.Store(true)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"result":"ISOLATED"}`)
	}))
	defer post.Close()
	a := newOwnedOCRApp(t, post.URL)
	ownedOCRSite(a, source.URL)
	address, cookie := source.URL+"/image", "owned=cookie"
	reply, err := ownedOCRCall(t, a, plugins.OCRRequest{ImageURL: &address, Cookie: &cookie})
	ownedOCRString(t, reply, err, "ISOLATED")
	if images.Load() != 1 || posts.Load() != 1 || leak.Load() {
		t.Fatal("cross-origin credential isolation differs")
	}
	sentinel := errors.New("owned invocation revoked")
	scope := ownedOCRScope()
	scope.validate = func(ctx context.Context) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if revoke.Load() {
			return sentinel
		}
		return nil
	}
	address = source.URL + "/revoke"
	raw, _ := json.Marshal(plugins.OCRRequest{ImageURL: &address, Cookie: &cookie})
	if _, err = a.originalSharedOCRResource(context.Background(), scope, plugins.SharedOCRGetCaptchaText, raw); !errors.Is(err, sentinel) {
		t.Fatalf("revoked redirect became ordinary empty: %v", err)
	}
	if images.Load() != 1 || posts.Load() != 1 {
		t.Fatal("revoked scope contacted next request")
	}
}
