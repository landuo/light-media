package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"

	"light-media/internal/plugins"
	"light-media/internal/sites"
)

// Both get_res/post_res use the fixed RequestUtils.request. Its actual paired
// requests/urllib3 oracle follows 30 redirects, rejects the 31st, and retains
// Set-Cookie only inside that request's temporary Session.
func TestOriginalHostCaptchaOCRActualSDKRedirectBoundary(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/plugin-host-services-20261031/shared-runtime-review/fs-http-source-oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	var oracle struct {
		SourceCommit string `json:"source_commit"`
		Redirects    []struct {
			Count    int             `json:"redirect_count"`
			Result   json.RawMessage `json:"result"`
			Requests int             `json:"actual_http_requests"`
			Cookie   string          `json:"second_request_cookie"`
		} `json:"redirects"`
	}
	if json.Unmarshal(raw, &oracle) != nil || oracle.SourceCommit != "8a83efeacd3e26b82be495e17d50146e31c96a60" || len(oracle.Redirects) != 2 {
		t.Fatal("fixed actual HTTP redirect oracle missing")
	}
	for _, sample := range oracle.Redirects {
		t.Run(strconv.Itoa(sample.Count), func(t *testing.T) {
			ownedOCRCleanProxy(t)
			var calls atomic.Int32
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				step, _ := strconv.Atoi(r.URL.Query().Get("step"))
				if step > 0 && r.Header.Get("Cookie") != sample.Cookie {
					t.Error("request Session lost redirect cookie")
				}
				if step == 0 && r.Header.Get("Cookie") != "" {
					t.Error("a prior OCR call's cookie escaped its temporary Session")
				}
				if r.Method != http.MethodPost {
					t.Error("307 changed OCR POST method")
				}
				if step < sample.Count {
					w.Header().Set("Location", fmt.Sprintf("/captcha/base64?step=%d", step+1))
					if step == 0 {
						w.Header().Set("Set-Cookie", sample.Cookie+"; Path=/")
					}
					w.WriteHeader(http.StatusTemporaryRedirect)
					return
				}
				_, _ = w.Write([]byte(`{"result":"ABC123"}`))
			}))
			defer peer.Close()
			t.Setenv("OCR_HOST", peer.URL)
			a := newTestApp(t)
			_, worker := hostBusinessFixture(t, a)
			var result plugins.CaptchaOCRResultV1
			for run := 0; run < 2; run++ {
				before := calls.Load()
				if err := hostBusinessCall(t, worker, plugins.CaptchaOCRTextCapability, plugins.CaptchaOCRRequestV1{ImageBase64: "YWJj"}, &result); err != nil {
					t.Fatal(err)
				}
				want := `"ABC123"`
				if string(sample.Result) == "null" {
					want = `""`
				}
				if string(result.Result) != want || int(calls.Load()-before) != sample.Requests {
					t.Fatalf("source redirect boundary changed: result=%s calls=%d want=%s/%d", result.Result, calls.Load()-before, want, sample.Requests)
				}
			}
		})
	}
}

func TestOriginalHostCaptchaOCRActualSDKImageRedirectCookieSource(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/plugin-platform-final-20261104/browser-ocr/ocr-redirect-source-oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	type trace struct {
		Method string            `json:"method"`
		Step   int               `json:"step"`
		Cookie *string           `json:"cookie"`
		Body   map[string]string `json:"body,omitempty"`
	}
	var corpus struct {
		Commit string `json:"source_commit"`
		Cases  []struct {
			Kind      string  `json:"kind"`
			Redirects int     `json:"redirects"`
			Result    string  `json:"result"`
			Trace     []trace `json:"trace"`
		} `json:"cases"`
	}
	if json.Unmarshal(raw, &corpus) != nil || corpus.Commit != "8a83efeacd3e26b82be495e17d50146e31c96a60" || len(corpus.Cases) != 5 {
		t.Fatal("complete fixed OCR/request redirect source corpus missing")
	}
	for _, sample := range corpus.Cases {
		t.Run(sample.Kind+strconv.Itoa(sample.Redirects), func(t *testing.T) {
			ownedOCRCleanProxy(t)
			actual := []trace{}
			var mu sync.Mutex
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				step, _ := strconv.Atoi(r.URL.Query().Get("step"))
				row := trace{Method: r.Method, Step: step}
				if value, present := r.Header["Cookie"]; present {
					joined := value[0]
					row.Cookie = &joined
				}
				if r.Method == "POST" {
					if err := json.NewDecoder(r.Body).Decode(&row.Body); err != nil {
						t.Error(err)
					}
				}
				mu.Lock()
				actual = append(actual, row)
				mu.Unlock()
				if (sample.Kind == "image" && r.Method == "GET" || sample.Kind == "post" && r.Method == "POST") && step < sample.Redirects {
					w.Header().Set("Location", fmt.Sprintf("%s?step=%d", r.URL.Path, step+1))
					w.Header().Set("Set-Cookie", "owned=redirect; Path=/")
					w.WriteHeader(http.StatusTemporaryRedirect)
					return
				}
				if r.Method == "GET" {
					_, _ = w.Write([]byte("owned-image"))
				} else {
					_, _ = w.Write([]byte(`{"result":"OWNED"}`))
				}
			}))
			defer peer.Close()
			t.Setenv("OCR_HOST", peer.URL)
			a := newTestApp(t)
			_, worker := hostBusinessFixture(t, a)
			settings := a.Settings()
			settings.Sites = []SiteConfig{{Config: sites.Config{ID: "owned-redirect-image", Kind: "nexusphp", BaseURL: peer.URL, Cookie: "owned=initial; other=yes", UserAgent: "Owned/OCR"}}}
			ownedDownloaderSave(t, a, settings)
			request := plugins.CaptchaOCRRequestV1{ImageBase64: "ZmFsbGJhY2s="}
			if sample.Kind == "image" {
				request.SiteID = "owned-redirect-image"
				request.ImageURL = peer.URL + "/image"
			}
			var result plugins.CaptchaOCRResultV1
			if err := hostBusinessCall(t, worker, plugins.CaptchaOCRTextCapability, request, &result); err != nil {
				t.Fatal(err)
			}
			want, _ := json.Marshal(sample.Result)
			mu.Lock()
			defer mu.Unlock()
			if string(result.Result) != string(want) || !reflect.DeepEqual(actual, sample.Trace) {
				t.Fatalf("source full OCR redirect trace changed: result=%s want=%s actual=%+v expected=%+v", result.Result, want, actual, sample.Trace)
			}
		})
	}
}
