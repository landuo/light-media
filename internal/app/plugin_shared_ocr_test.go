package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"light-media/internal/ocr"
	"light-media/internal/plugins"
	"light-media/internal/sites"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
)

func newOwnedOCRApp(t *testing.T, host string) *App {
	t.Helper()
	a, err := New(Options{DataDir: t.TempDir(), OCRHost: &host})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	return a
}
func ownedOCRScope() originalBaseCallScope {
	return originalBaseCallScope{installation: PluginInstallation{PluginID: "OwnedOCR", InstanceID: "OwnedOCR", Version: "1.0"}, validate: func(ctx context.Context) error { return ctx.Err() }}
}
func ownedOCRCall(t *testing.T, a *App, input plugins.OCRRequest) (plugins.OCRReply, error) {
	t.Helper()
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	out, err := a.pluginOriginalResourceHost(context.Background(), ownedOCRScope(), plugins.Identity{PluginID: "OwnedOCR", InstanceID: "OwnedOCR", Version: "1.0"}, plugins.SharedOCRGetCaptchaText, raw)
	var reply plugins.OCRReply
	if err == nil {
		if err = json.Unmarshal(out, &reply); err != nil {
			t.Fatal(err)
		}
	}
	return reply, err
}
func ownedOCRSite(a *App, address string) {
	a.cfgMu.Lock()
	defer a.cfgMu.Unlock()
	a.config.Sites = []SiteConfig{{Config: sites.Config{ID: "owned-ocr-site", Kind: "nexusphp", BaseURL: address}, Enabled: true}}
}
func ownedOCRString(t *testing.T, reply plugins.OCRReply, err error, wanted string) {
	t.Helper()
	if err != nil || reply.SourceError != "" || reply.Value == nil || reply.Value.Kind != "string" || string(reply.Value.Points) != wanted {
		t.Fatalf("OCR value %#v / %v want %q", reply, err, wanted)
	}
}
func TestSharedOCROwnedHTTPFixedSource(t *testing.T) {
	var imageCalls, postCalls atomic.Int32
	var latinUA atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, present := r.Header["Accept"]; present {
			t.Error("source accept_type=None must omit Accept")
		}
		if r.URL.Path == "/image" {
			imageCalls.Add(1)
			wantedUA := "owned-UA"
			if latinUA.Load() {
				wantedUA = string([]byte{0xe9})
			}
			if r.Header.Get("Cookie") != "owned=cookie" || r.Header.Get("User-Agent") != wantedUA {
				t.Errorf("image cookie/UA differ: UAbytes=%x", []byte(r.Header.Get("User-Agent")))
			}
			w.WriteHeader(404)
			_, _ = w.Write([]byte("B"))
			return
		}
		if r.URL.Path != "/captcha/base64" || r.Method != "POST" {
			t.Error("outside owned OCR routes")
			w.WriteHeader(502)
			return
		}
		postCalls.Add(1)
		raw, _ := io.ReadAll(r.Body)
		if string(raw) != `{"base64_img": "Qg=="}` || r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" || r.Header.Get("User-Agent") != "python-urllib3/2.7.0" {
			t.Errorf("POST body/credential/UA differs: %s", raw)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"result":"OWNED"}`))
	}))
	defer server.Close()
	a := newOwnedOCRApp(t, server.URL)
	ownedOCRSite(a, server.URL)
	imageURL, fallback, cookie, ua := server.URL+"/image", "QQ", "owned=cookie", "owned-UA"
	reply, err := ownedOCRCall(t, a, plugins.OCRRequest{ImageURL: &imageURL, ImageBase64: &fallback, Cookie: &cookie, UserAgent: &ua})
	ownedOCRString(t, reply, err, "OWNED")
	if imageCalls.Load() != 1 || postCalls.Load() != 1 {
		t.Fatal("image error HTTP body precedence changed")
	}
	latinUA.Store(true)
	ua = "é"
	reply, err = ownedOCRCall(t, a, plugins.OCRRequest{ImageURL: &imageURL, Cookie: &cookie, UserAgent: &ua})
	ownedOCRString(t, reply, err, "OWNED")
	if imageCalls.Load() != 2 || postCalls.Load() != 2 {
		t.Fatal("Latin1 request did not reach owned peer")
	}
	ua = "☃"
	reply, err = ownedOCRCall(t, a, plugins.OCRRequest{ImageURL: &imageURL, Cookie: &cookie, UserAgent: &ua})
	if err != nil || reply.SourceError != "UnicodeEncodeError" {
		t.Fatalf("nonLatin UA must preserve typed source error: %#v %v", reply, err)
	}
	ua = " leading"
	reply, err = ownedOCRCall(t, a, plugins.OCRRequest{ImageURL: &imageURL, Cookie: &cookie, UserAgent: &ua})
	ownedOCRString(t, reply, err, "")
	if imageCalls.Load() != 2 || postCalls.Load() != 2 {
		t.Fatal("invalid header dialed owned peer")
	}
	ua = "é"
	ignoredCookie := "ignored☃; owned=cookie"
	reply, err = ownedOCRCall(t, a, plugins.OCRRequest{ImageURL: &imageURL, Cookie: &ignoredCookie, UserAgent: &ua})
	ownedOCRString(t, reply, err, "OWNED")
	badCookie := "owned=☃"
	reply, err = ownedOCRCall(t, a, plugins.OCRRequest{ImageURL: &imageURL, Cookie: &badCookie, UserAgent: &ua})
	if err != nil || reply.SourceError != "UnicodeEncodeError" || imageCalls.Load() != 3 || postCalls.Load() != 3 {
		t.Fatalf("effective cookie Latin1 error or no-dial differs: %#v %v", reply, err)
	}
	if err := originalOCRCookieEncoding("ignored☃; duplicate=☃; duplicate=ok"); err != nil {
		t.Fatal("ignored/overwritten cookie value was incorrectly rejected")
	}
}
func TestSharedOCRHostBoundariesAndSnapshot(t *testing.T) {
	for _, row := range []struct {
		value   string
		present bool
		want    string
	}{{"", false, ocr.DefaultHost}, {"", true, ""}, {" \u001chttps://owned.invalid/\u3000", true, "https://owned.invalid/"}} {
		got := OCRHostEnvironment(func(string) (string, bool) { return row.value, row.present })
		if got != row.want {
			t.Fatalf("snapshot config %q want %q", got, row.want)
		}
	}
	before := originalStartupOCRHost
	// Only this owned endpoint selector changes; no proxy or TLS environment changes.
	t.Setenv("OCR_HOST", "http://changed.invalid")
	a, err := New(Options{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if a.originalOCRHost != before {
		t.Fatal("App.New reread process OCR_HOST")
	}
	if os.Getenv("OCR_HOST") != "http://changed.invalid" {
		t.Fatal("test selector not applied")
	}
	if originalStandardCapabilityAllowed(pluginBaseSDKContract, plugins.SharedOCRGetCaptchaText) || !originalStandardCapabilityAllowed(pluginResourcesSDKContract, plugins.SharedOCRGetCaptchaText) {
		t.Fatal("OCR capability contract leak")
	}
	scope := ownedOCRScope()
	_, err = a.pluginOriginalResourceHost(context.Background(), scope, plugins.Identity{PluginID: "Other", InstanceID: "OwnedOCR", Version: "1.0"}, plugins.SharedOCRGetCaptchaText, json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("identity bypass")
	}
	for _, raw := range []string{`{"endpoint":"http://owned.invalid"}`, `{"image_b64":1}`, `{"cookie":"\ud800"}`, `null`} {
		if _, err = a.originalSharedOCRResource(context.Background(), scope, plugins.SharedOCRGetCaptchaText, json.RawMessage(raw)); err == nil {
			t.Fatalf("invalid input accepted %s", raw)
		}
	}
	sentinel := errors.New("owned revoked")
	scope.validate = func(context.Context) error { return sentinel }
	if _, err = a.originalSharedOCRResource(context.Background(), scope, plugins.SharedOCRGetCaptchaText, json.RawMessage(`{}`)); !errors.Is(err, sentinel) {
		t.Fatal("scope guard lost")
	}
}
func TestSharedOCRAnonymousImageDoesNotRequireSite(t *testing.T) {
	sourceUA := originalPluginServerUserAgent()
	wantedUA := sourceUA + " Plugin/" + plugins.OriginalPluginLower("OwnedOCR")
	var imageCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/image" {
			imageCalls.Add(1)
			if r.Header.Get("Cookie") != "" {
				t.Error("anonymous image cookie")
			}
			if r.Header.Get("User-Agent") != wantedUA {
				t.Errorf("source caller UA suffix missing: %s", r.Header.Get("User-Agent"))
			}
			_, _ = w.Write([]byte("B"))
			return
		}
		_, _ = w.Write([]byte(`{"result":"ANON"}`))
	}))
	defer server.Close()
	a := newOwnedOCRApp(t, server.URL)
	imageURL := server.URL + "/image"
	reply, err := ownedOCRCall(t, a, plugins.OCRRequest{ImageURL: &imageURL, UserAgent: &sourceUA})
	ownedOCRString(t, reply, err, "ANON")
	cookie := "owned=cookie"
	if _, err = ownedOCRCall(t, a, plugins.OCRRequest{ImageURL: &imageURL, Cookie: &cookie}); err == nil {
		t.Fatal("unconfigured credentialed origin accepted")
	}
	if imageCalls.Load() != 1 {
		t.Fatal("denied credential image reached peer")
	}
}
