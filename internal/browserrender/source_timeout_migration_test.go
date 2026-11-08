package browserrender

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
)

func TestSourceFSOriginalRawTimeoutHTTP(t *testing.T) {
	b, err := os.ReadFile("testdata/source-page-render-oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	type fsWire struct{ Method, Body string }
	var oracle struct {
		Rows []struct {
			Kind, Name, Mode string
			OriginalURL      string `json:"original_url"`
			Timeout          json.RawMessage
			Wire             []fsWire
		}
	}
	if json.Unmarshal(b, &oracle) != nil {
		t.Fatal("original source oracle")
	}
	for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "all_proxy", "no_proxy"} {
		t.Setenv(key, "")
	}
	count := 0
	for _, r := range oracle.Rows {
		if r.Kind != "browser" || r.Mode != "flaresolverr" {
			continue
		}
		count++
		sourcePOST := []fsWire{}
		for _, v := range r.Wire {
			if v.Method == "POST" {
				sourcePOST = append(sourcePOST, v)
			}
		}
		r.Wire = sourcePOST
		t.Run(r.Name, func(t *testing.T) {
			bodies := []string{}
			var mu sync.Mutex
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
				b, _ := io.ReadAll(q.Body)
				mu.Lock()
				bodies = append(bodies, string(b))
				mu.Unlock()
				if q.Method != "POST" || q.URL.Path != "/v1" || q.Header.Get("User-Agent") != "python-urllib3/2.7.0" || q.Header.Get("Cookie") != "" {
					t.Error("wrong FS service request", q.Method, q.URL, q.Header)
				}
				w.Header().Set("Content-Type", "application/json")
				io.WriteString(w, `{"status":"ok","solution":{"response":"<html>owned FS</html>"}}`)
			}))
			defer peer.Close()
			// The source SiteBase transforms falsey values before calling the helper;
			// direct helper calls keep the original value for the Cloak fallback.
			raw := r.Timeout
			if strings.Contains(r.Name, "/site-base/") {
				v, e := sourceBrowserTimeout(raw)
				if e != nil {
					t.Fatal(e)
				}
				if !sourceTimeoutTruthy(v) {
					raw = json.RawMessage(`60`)
				}
			}
			cfg := Config{FlareSolverrURL: peer.URL}
			solution, e := cfg.flareSolverr(context.Background(), Request{URL: "https://hdsky.me/attendance.php", Cookie: "a=owned", UserAgent: "owned-UA", SourceTimeout: raw})
			mu.Lock()
			defer mu.Unlock()
			if len(bodies) != len(r.Wire) {
				t.Fatal("original physical FS call count", len(bodies), len(r.Wire), e)
			}
			for i, b := range bodies {
				want := r.Wire[i].Body
				if r.OriginalURL != "" {
					if strings.Count(want, r.OriginalURL) != 1 {
						t.Fatal("original URL cardinality")
					}
					want = strings.Replace(want, r.OriginalURL, "https://hdsky.me/attendance.php", 1)
				}
				if b != want {
					t.Fatalf("original Requests JSON differs\n got %s\nwant %s", b, want)
				}
			}
			if len(r.Wire) > 0 {
				if e != nil || solution["response"] != "<html>owned FS</html>" {
					t.Fatal(solution, e)
				}
			} else if solution != nil {
				t.Fatal("failed FS became successful solution", solution, e)
			}
		})
	}
	if count != 22 {
		t.Fatal(count)
	}
}
