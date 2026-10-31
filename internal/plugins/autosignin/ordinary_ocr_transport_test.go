package autosignin

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func bindOwnedOCR(t *testing.T, address, cookie string, guard func(context.Context) error) *BoundSiteHTTP {
	t.Helper()
	bound, err := BindSiteHTTP(SiteHTTPBinding{URL: address, Cookie: cookie, RequestsProxy: &RequestsProxyPolicy{Environment: os.Environ()}, BeforeRequest: guard})
	if err != nil {
		t.Fatal(err)
	}
	return bound
}
func TestOrdinaryOCRFullBodyNativeLimitRetained(t *testing.T) {
	body := bytes.Repeat([]byte("A"), 2<<20)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, present := r.Header["Accept"]; present {
			t.Error("RequestUtils accept_type=None must remove Accept")
		}
		if r.Method == "POST" {
			raw, _ := io.ReadAll(r.Body)
			if string(raw) != `{"base64_img": "QQ=="}` || r.Header.Get("Content-Type") != "application/json" || r.Header.Get("Cookie") != "" || r.Header.Get("User-Agent") != "python-urllib3/2.7.0" {
				t.Errorf("OCR POST request isolation differs: body=%s", raw)
			}
			_, _ = w.Write([]byte("owned-post"))
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		if r.URL.Path == "/gzip" {
			w.Header().Set("Content-Encoding", "gzip")
			g := gzip.NewWriter(w)
			_, _ = g.Write(body)
			_ = g.Close()
			return
		}
		_, _ = w.Write(body)
	}))
	defer server.Close()
	bound := bindOwnedOCR(t, server.URL, "", nil)
	for _, path := range []string{"/raw", "/gzip"} {
		t.Run(path, func(t *testing.T) {
			response, err := bound.DoOrdinaryOCR(context.Background(), OrdinaryOCRRequest{Method: "GET", URL: server.URL + path})
			if err != nil || response == nil || !bytes.Equal(response.Content, body) || response.ContentType != "application/octet-stream" {
				t.Fatalf("OCR full body: %v", err)
			}
			_, err = bound.Do(context.Background(), PageRequest{Method: "GET", URL: server.URL + path, Binary: true})
			if !errors.Is(err, ErrSiteResponseLimit) {
				t.Fatalf("native limit lost: %v", err)
			}
		})
	}
	postBound := bindOwnedOCR(t, server.URL, "owned=image-cookie", nil)
	postBound.binding.UserAgent = "image-UA"
	response, err := postBound.DoOrdinaryOCR(context.Background(), OrdinaryOCRRequest{Method: "POST", URL: server.URL, Base64Image: "QQ=="})
	if err != nil || response == nil || string(response.Content) != "owned-post" {
		t.Fatalf("owned POST failed: %v", err)
	}
}
func TestOrdinaryOCRCancellationAndDecodeErrors(t *testing.T) {
	var hits atomic.Int32
	started := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.URL.Path == "/slow" {
			started <- struct{}{}
			<-r.Context().Done()
			return
		}
		w.Header().Set("Content-Encoding", "gzip")
		_, _ = w.Write([]byte("not-gzip"))
	}))
	defer server.Close()
	bound := bindOwnedOCR(t, server.URL, "", nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := bound.DoOrdinaryOCR(ctx, OrdinaryOCRRequest{Method: "GET", URL: server.URL}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if hits.Load() != 0 {
		t.Fatal("canceled context requested peer")
	}
	ctx, cancel = context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := bound.DoOrdinaryOCR(ctx, OrdinaryOCRRequest{Method: "GET", URL: server.URL + "/slow"})
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		cancel()
		t.Fatal("owned request never reached peer")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancel did not stop owned request")
	}
	response, err := bound.DoOrdinaryOCR(context.Background(), OrdinaryOCRRequest{Method: "GET", URL: server.URL + "/broken"})
	if err != nil || response != nil {
		t.Fatalf("ordinary malformed gzip should return nil response, got %v %v", response, err)
	}
	sentinel := errors.New("owned revoked scope")
	guarded := bindOwnedOCR(t, server.URL, "", func(context.Context) error { return sentinel })
	if _, err = guarded.DoOrdinaryOCR(context.Background(), OrdinaryOCRRequest{Method: "GET", URL: server.URL}); !errors.Is(err, sentinel) {
		t.Fatalf("guard swallowed: %v", err)
	}
}
func TestOrdinaryOCRSourceTLSIsIsolated(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1); _, _ = w.Write([]byte("owned")) }))
	defer server.Close()
	bound := bindOwnedOCR(t, server.URL, "", nil)
	for i := 0; i < 2; i++ {
		response, _ := bound.Do(context.Background(), PageRequest{Method: "GET", URL: server.URL, Binary: true})
		if response != nil {
			t.Fatal("ordinary native Do accepted owned self-signed TLS")
		}
		if i == 0 {
			response, err := bound.DoOrdinaryOCR(context.Background(), OrdinaryOCRRequest{Method: "GET", URL: server.URL})
			if err != nil || response == nil || string(response.Content) != "owned" {
				t.Fatalf("private OCR TLS: %v", err)
			}
		}
	}
	if hits.Load() != 1 {
		t.Fatalf("private isolation hit count %d", hits.Load())
	}
}
func TestOrdinaryOCRCredentialRedirectAndNativeRemainDenied(t *testing.T) {
	var targetHits atomic.Int32
	var leaked atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetHits.Add(1)
		if r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" {
			leaked.Store(true)
		}
		_, _ = w.Write([]byte("target"))
	}))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("Cookie"), "owned=secret") {
			t.Error("initial credential missing")
		}
		http.SetCookie(w, &http.Cookie{Name: "fromSource", Value: "only-source", Path: "/"})
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer source.Close()
	bound := bindOwnedOCR(t, source.URL, "owned=secret", nil)
	response, err := bound.DoOrdinaryOCR(context.Background(), OrdinaryOCRRequest{Method: "GET", URL: source.URL})
	if err != nil || response == nil || string(response.Content) != "target" || leaked.Load() || targetHits.Load() != 1 {
		t.Fatalf("OCR redirect boundary err=%v hits=%d leaked=%v", err, targetHits.Load(), leaked.Load())
	}
	if _, err = bound.Do(context.Background(), PageRequest{Method: "GET", URL: source.URL, Binary: true}); !errors.Is(err, ErrSiteTargetDenied) {
		t.Fatalf("native cross-origin changed: %v", err)
	}
	if targetHits.Load() != 1 {
		t.Fatal("native request crossed origin")
	}
}
func TestOrdinaryOCRRedirectGuardsAndURLCredentials(t *testing.T) {
	var targetHits atomic.Int32
	var revoked atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetHits.Add(1)
		_, _ = w.Write([]byte("must not arrive"))
	}))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/userinfo" {
			http.Redirect(w, r, strings.Replace(target.URL, "http://", "http://owned:secret@", 1), http.StatusFound)
			return
		}
		revoked.Store(true)
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer source.Close()
	sentinel := errors.New("owned scope revoked on redirect")
	bound := bindOwnedOCR(t, source.URL, "", func(context.Context) error {
		if revoked.Load() {
			return sentinel
		}
		return nil
	})
	if _, err := bound.DoOrdinaryOCR(context.Background(), OrdinaryOCRRequest{Method: "GET", URL: strings.Replace(source.URL, "http://", "http://owned:secret@", 1)}); !errors.Is(err, ErrSiteTargetDenied) {
		t.Fatalf("URL credentials accepted: %v", err)
	}
	if _, err := bound.DoOrdinaryOCR(context.Background(), OrdinaryOCRRequest{Method: "GET", URL: source.URL + "/userinfo"}); !errors.Is(err, ErrSiteTargetDenied) {
		t.Fatalf("redirect userinfo accepted: %v", err)
	}
	if _, err := bound.DoOrdinaryOCR(context.Background(), OrdinaryOCRRequest{Method: "GET", URL: source.URL + "/revoke"}); !errors.Is(err, sentinel) {
		t.Fatalf("redirect guard swallowed: %v", err)
	}
	if targetHits.Load() != 0 {
		t.Fatal(fmt.Sprintf("target hits %d", targetHits.Load()))
	}
}
