package app

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"github.com/andybalholm/brotli"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"light-media/internal/plugins"
	"light-media/internal/sites"
)

func ownedOCRCleanProxy(t *testing.T) {
	t.Helper()
	for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "all_proxy", "NO_PROXY", "no_proxy"} {
		t.Setenv(key, "")
	}
}
func ownedOCRClose(h http.ResponseWriter) {
	if hijacker, ok := h.(http.Hijacker); ok {
		conn, _, err := hijacker.Hijack()
		if err == nil {
			_ = conn.Close()
		}
	}
}

func TestOriginalHostCaptchaOCRActualSDKFixedSource(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/plugin-host-business-20261031/ocr-source/oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	var corpus struct {
		SourceCommit string `json:"source_commit"`
		Cases        []struct {
			Case        string
			Request     map[string]string
			Images      map[string]string
			PostStatus  *int            `json:"post_status"`
			PostJSON    json.RawMessage `json:"post_json"`
			InvalidJSON bool            `json:"invalid_json"`
			ImageMode   *string         `json:"image_mode"`
			OCRHost     string          `json:"ocr_host"`
			Expected    json.RawMessage
			Error       string
			Trace       []struct {
				Method, URL string
				Body        map[string]string
			}
		}
	}
	if json.Unmarshal(raw, &corpus) != nil || corpus.SourceCommit != "8a83efeacd3e26b82be495e17d50146e31c96a60" || len(corpus.Cases) != 34 {
		t.Fatal("fixed complete OcrHelper corpus missing")
	}
	for _, row := range corpus.Cases {
		t.Run(row.Case, func(t *testing.T) {
			ownedOCRCleanProxy(t)
			png, err := base64.StdEncoding.DecodeString(row.Images["png_base64"])
			if err != nil {
				t.Fatal(err)
			}
			var imageCalls, postCalls atomic.Int32
			imageServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				imageCalls.Add(1)
				if r.Method != "GET" || r.URL.Path != "/image.png" {
					t.Error("unexpected image route")
				}
				expectedUA := row.Request["ua"]
				if expectedUA == "" {
					expectedUA = "python-urllib3/2.7.0"
				}
				if r.Header.Get("Cookie") != row.Request["cookie"] || r.Header.Get("User-Agent") != expectedUA {
					t.Error("host did not retain source site Cookie/UA")
				}
				if row.ImageMode == nil {
					ownedOCRClose(w)
					return
				}
				switch *row.ImageMode {
				case "empty":
					w.WriteHeader(200)
				case "503":
					w.WriteHeader(503)
					_, _ = w.Write([]byte("owned image error body"))
				default:
					_, _ = w.Write(png)
				}
			}))
			defer imageServer.Close()
			ocrServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				postCalls.Add(1)
				if r.Method != "POST" || r.URL.Path != "/captcha/base64" {
					t.Error("unexpected OCR route")
				}
				if r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" || r.Header.Get("Content-Type") != "application/json" || r.Header.Get("Accept") != "" || r.Header.Get("User-Agent") != "python-urllib3/2.7.0" {
					t.Error("site credentials leaked to OCR service or source JSON type changed")
				}
				body, _ := io.ReadAll(r.Body)
				var actual map[string]string
				if json.Unmarshal(body, &actual) != nil {
					t.Error("OCR body not JSON")
				}
				var wanted map[string]string
				for _, trace := range row.Trace {
					if trace.Method == "POST" {
						wanted = trace.Body
					}
				}
				if actual["base64_img"] != wanted["base64_img"] {
					t.Errorf("source base64 normalization/URL precedence differs got=%q want=%q", actual["base64_img"], wanted["base64_img"])
				}
				if row.PostStatus == nil {
					ownedOCRClose(w)
					return
				}
				w.WriteHeader(*row.PostStatus)
				if row.InvalidJSON {
					_, _ = w.Write([]byte("{owned invalid"))
				} else {
					_, _ = w.Write(row.PostJSON)
				}
			}))
			defer ocrServer.Close()
			if row.OCRHost == "" {
				t.Setenv("OCR_HOST", "")
			} else {
				t.Setenv("OCR_HOST", ocrServer.URL)
			}
			a := newTestApp(t)
			_, worker := hostBusinessFixture(t, a)
			config := a.Settings()
			config.Sites = []SiteConfig{{Config: sites.Config{ID: "owned-ocr-site", Kind: "nexusphp", Name: "Owned OCR source image", BaseURL: imageServer.URL, Cookie: row.Request["cookie"], UserAgent: row.Request["ua"]}, Enabled: false}}
			ownedDownloaderSave(t, a, config)
			request := plugins.CaptchaOCRRequestV1{ImageURL: row.Request["image_url"], ImageBase64: row.Request["image_b64"]}
			if strings.HasPrefix(request.ImageURL, "https://owned-site.invalid") {
				request.SiteID = "owned-ocr-site"
				request.ImageURL = strings.Replace(request.ImageURL, "https://owned-site.invalid", imageServer.URL, 1)
			}
			var result plugins.CaptchaOCRResultV1
			err = hostBusinessCall(t, worker, plugins.CaptchaOCRTextCapability, request, &result)
			if row.Error != "" {
				if err == nil {
					t.Fatal("fixed helper exception became successful result", string(result.Result))
				}
			} else if err != nil || !originalHostSubscriptionJSONEqual(result.Result, row.Expected) {
				t.Fatal("source OCR return differs", err, string(result.Result), string(row.Expected))
			}
			expectedImages, expectedPosts := 0, 0
			for _, trace := range row.Trace {
				if trace.Method == "GET" && strings.HasPrefix(trace.URL, "https://owned-site.invalid") {
					expectedImages++
				}
				if trace.Method == "POST" && row.OCRHost != "" {
					expectedPosts++
				}
			}
			if int(imageCalls.Load()) != expectedImages || int(postCalls.Load()) != expectedPosts {
				t.Fatal("actual source HTTP side effects differ", imageCalls.Load(), postCalls.Load(), expectedImages, expectedPosts)
			}
		})
	}
}

func TestOriginalHostCaptchaOCRActualSDKOriginAndConfigurationGuard(t *testing.T) {
	ownedOCRCleanProxy(t)
	var posts atomic.Int32
	ocr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		posts.Add(1)
		_, _ = w.Write([]byte(`{"result":"ABC123"}`))
	}))
	defer ocr.Close()
	t.Setenv("OCR_HOST", ocr.URL)
	a := newTestApp(t)
	_, worker := hostBusinessFixture(t, a)
	var image *httptest.Server
	image = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.cfgMu.Lock()
		a.config.Sites[0].Cookie = "owned changed after GET"
		a.cfgMu.Unlock()
		_, _ = w.Write([]byte("owned synthetic image"))
	}))
	defer image.Close()
	config := a.Settings()
	config.Sites = []SiteConfig{{Config: sites.Config{ID: "owned-ocr-site", Kind: "nexusphp", BaseURL: image.URL, Cookie: "owned old cookie"}}}
	ownedDownloaderSave(t, a, config)
	var result plugins.CaptchaOCRResultV1
	if err := hostBusinessCall(t, worker, plugins.CaptchaOCRTextCapability, plugins.CaptchaOCRRequestV1{SiteID: "owned-ocr-site", ImageURL: image.URL + "/image.png"}, &result); err == nil {
		t.Fatal("stale site configuration returned OCR success")
	}
	if posts.Load() != 0 {
		t.Fatal("stale site image was sent to OCR after config changed")
	}
	for _, request := range []plugins.CaptchaOCRRequestV1{{ImageURL: image.URL + "/image.png"}, {SiteID: "owned-ocr-site", ImageURL: ocr.URL + "/other-origin"}, {SiteID: "missing", ImageURL: image.URL + "/image.png"}} {
		if err := hostBusinessCall(t, worker, plugins.CaptchaOCRTextCapability, request, &result); err == nil {
			t.Fatal("unbound or foreign image URL was accepted")
		}
	}
	if posts.Load() != 0 {
		t.Fatal("denied image operation called OCR")
	}
	t.Setenv("OCR_HOST", "") // Does not rewrite the source class's startup URL.
	if err := hostBusinessCall(t, worker, plugins.CaptchaOCRTextCapability, plugins.CaptchaOCRRequestV1{ImageBase64: "YWJj"}, &result); err != nil || string(result.Result) != `"ABC123"` || posts.Load() != 1 {
		t.Fatal("ordinary OCR_HOST startup snapshot changed live", err, string(result.Result), posts.Load())
	}
}

func TestOriginalHostCaptchaOCRActualSDKLargeAndDecodedImage(t *testing.T) {
	ownedOCRCleanProxy(t)
	png, err := os.ReadFile("../../testdata/plugin-host-business-20261031/ocr-source/owned-large.png")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("../../testdata/plugin-host-business-20261031/ocr-source/large-oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	var oracle struct {
		PNGBytes int    `json:"png_bytes"`
		Expected string `json:"expected_result"`
		Post     struct {
			Bytes int    `json:"base64_length"`
			SHA   string `json:"base64_sha256"`
		}
	}
	if json.Unmarshal(raw, &oracle) != nil || len(png) != oracle.PNGBytes || len(png) < 128<<10 {
		t.Fatal("source owned large PNG missing")
	}
	var posts atomic.Int32
	ocr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		posts.Add(1)
		body, _ := io.ReadAll(r.Body)
		var decoded map[string]string
		if json.Unmarshal(body, &decoded) != nil {
			t.Error("invalid OCR body")
		}
		encoded := decoded["base64_img"]
		digest := sha256.Sum256([]byte(encoded))
		if len(encoded) != oracle.Post.Bytes || hex.EncodeToString(digest[:]) != oracle.Post.SHA {
			t.Error("large image was truncated or compressed image bytes were encoded instead of Response.content")
		}
		w.Header().Set("Content-Encoding", "br")
		compressed := brotli.NewWriter(w)
		_, _ = compressed.Write([]byte(`{"result":"OWNED_LARGE"}`))
		_ = compressed.Close()
	}))
	defer ocr.Close()
	t.Setenv("OCR_HOST", ocr.URL)
	image := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mode := strings.TrimPrefix(r.URL.Path, "/")
		w.Header().Set("Content-Encoding", mode)
		var writer io.WriteCloser
		switch mode {
		case "gzip":
			writer = gzip.NewWriter(w)
		case "deflate":
			writer = zlib.NewWriter(w)
		case "br":
			writer = brotli.NewWriter(w)
		default:
			t.Error("unexpected image encoding")
			return
		}
		_, _ = writer.Write(png)
		_ = writer.Close()
	}))
	defer image.Close()
	a := newTestApp(t)
	_, worker := hostBusinessFixture(t, a)
	config := a.Settings()
	config.Sites = []SiteConfig{{Config: sites.Config{ID: "owned-large-image", Kind: "nexusphp", BaseURL: image.URL}}}
	ownedDownloaderSave(t, a, config)
	inputs := []plugins.CaptchaOCRRequestV1{{ImageBase64: base64.StdEncoding.EncodeToString(png)}}
	for _, encoding := range []string{"gzip", "deflate", "br"} {
		inputs = append(inputs, plugins.CaptchaOCRRequestV1{SiteID: "owned-large-image", ImageURL: image.URL + "/" + encoding})
	}
	for _, input := range inputs {
		var result plugins.CaptchaOCRResultV1
		if err := hostBusinessCall(t, worker, plugins.CaptchaOCRTextCapability, input, &result); err != nil || !bytes.Equal(result.Result, mustCleanSeedJSON(oracle.Expected)) {
			t.Fatal("large/decoded source image failed actual SDK", err, string(result.Result))
		}
	}
	if posts.Load() != 4 {
		t.Fatal("large/decoded requests not actually sent", posts.Load())
	}
}

func TestOriginalHostCaptchaOCRActualSDKFixedRequestCookieHeaders(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/plugin-host-business-20261031/ocr-source/cookie-oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	var corpus struct {
		SourceCommit string `json:"source_commit"`
		Requests     string
		Cases        []struct {
			Case, Cookie, UA, Error string
			Trace                   []struct {
				CookieB64 string `json:"cookie_bytes_b64"`
				UAB64     string `json:"ua_bytes_b64"`
			}
		}
	}
	if json.Unmarshal(raw, &corpus) != nil || len(corpus.Cases) != 11 || corpus.SourceCommit != "8a83efeacd3e26b82be495e17d50146e31c96a60" || corpus.Requests != "2.34.2" {
		t.Fatal("fixed actual requests header source corpus missing")
	}
	for _, row := range corpus.Cases {
		t.Run(row.Case, func(t *testing.T) {
			ownedOCRCleanProxy(t)
			var imageCalls, postCalls atomic.Int32
			image := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				imageCalls.Add(1)
				if len(row.Trace) != 1 {
					t.Error("source exception unexpectedly reached image network")
					return
				}
				cookie, _ := base64.StdEncoding.DecodeString(row.Trace[0].CookieB64)
				ua, _ := base64.StdEncoding.DecodeString(row.Trace[0].UAB64)
				if r.Header.Get("Cookie") != string(cookie) || r.Header.Get("User-Agent") != string(ua) {
					t.Errorf("actual request headers differ from fixed cookie_parse+requests wire gotCookie=%q wantCookie=%q gotUA=%q wantUA=%q", r.Header.Get("Cookie"), cookie, r.Header.Get("User-Agent"), ua)
				}
				_, _ = w.Write([]byte("owned synthetic image"))
			}))
			defer image.Close()
			ocr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				postCalls.Add(1)
				if r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" {
					t.Error("source site credentials leaked to OCR")
				}
				_, _ = w.Write([]byte(`{"result":"HEADER_OK"}`))
			}))
			defer ocr.Close()
			t.Setenv("OCR_HOST", ocr.URL)
			a := newTestApp(t)
			_, worker := hostBusinessFixture(t, a)
			config := a.Settings()
			config.Sites = []SiteConfig{{Config: sites.Config{ID: "owned-cookie-image", Kind: "nexusphp", BaseURL: image.URL, Cookie: row.Cookie, UserAgent: row.UA}}}
			ownedDownloaderSave(t, a, config)
			var result plugins.CaptchaOCRResultV1
			err := hostBusinessCall(t, worker, plugins.CaptchaOCRTextCapability, plugins.CaptchaOCRRequestV1{SiteID: "owned-cookie-image", ImageURL: image.URL + "/image", ImageBase64: "YWJj"}, &result)
			if row.Error != "" {
				if err == nil || imageCalls.Load() != 0 || postCalls.Load() != 0 {
					t.Fatal("source header exception became network or OCR success", err, imageCalls.Load(), postCalls.Load())
				}
			} else if err != nil || string(result.Result) != `"HEADER_OK"` || imageCalls.Load() != 1 || postCalls.Load() != 1 {
				t.Fatal("ordinary source header request failed actual SDK", err, string(result.Result), imageCalls.Load(), postCalls.Load())
			}
		})
	}
}
