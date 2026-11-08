package browserrender

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
)

// Expectations are actual fixed private FS/RequestUtils calls, not mock outputs.
func TestSourceFSAuthenticatedSessionOriginal(t *testing.T) {
	data, err := os.ReadFile("testdata/source-page-auth-oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	type wire struct{ Method, Path, UA, Cookie, ContentType, Body string }
	var oracle struct {
		Rows []struct {
			Stage   string
			Timeout json.RawMessage
			Result  map[string]any
			Wire    []map[string]any
			Logs    []struct{ Level, Message string }
		}
	}
	if json.Unmarshal(data, &oracle) != nil || len(oracle.Rows) != 5 {
		t.Fatal("actual source5 required")
	}
	for _, k := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "all_proxy", "no_proxy"} {
		t.Setenv(k, "")
	}
	for _, row := range oracle.Rows {
		t.Run(row.Stage+"/"+string(row.Timeout), func(t *testing.T) {
			var mu sync.Mutex
			actual := []map[string]any{}
			levels := []string{}
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				b, _ := io.ReadAll(r.Body)
				var p map[string]any
				if json.Unmarshal(b, &p) != nil {
					t.Error("invalid body")
				}
				var cookie any
				if h := r.Header.Get("Cookie"); h != "" {
					cookie = h
				}
				mu.Lock()
				actual = append(actual, map[string]any{"method": r.Method, "path": r.URL.RequestURI(), "ua": r.Header.Get("User-Agent"), "cookie": cookie, "content_type": r.Header.Get("Content-Type"), "body": string(b)})
				mu.Unlock()
				status := "ok"
				if row.Stage == "create-failed" && p["cmd"] == "sessions.create" || row.Stage == "request-failed" && p["cmd"] == "request.get" {
					status = "error"
				}
				json.NewEncoder(w).Encode(map[string]any{"status": status, "message": "owned failure", "solution": map[string]any{"response": "<html>owned FS</html>"}})
			}))
			defer peer.Close()
			got, _ := (Config{FlareSolverrURL: peer.URL}).flareSolverr(context.Background(), Request{URL: "https://hdsky.me/attendance.php", Cookie: "a=owned", SourceProxy: &ProxySettings{Server: "http://owned.invalid:1234", Username: "owned%2Fuser", Password: "owned%3Apass"}, SourceTimeout: row.Timeout, SourceLog: func(level, code string) { levels = append(levels, strings.ToLower(level)) }})
			mu.Lock()
			defer mu.Unlock()
			// Only the generated UUID is normalized; exact insertion order, spacing,
			// literal auth escapes, cookie parse and cleanup10 request remain observable.
			normalize := func(in []map[string]any) []map[string]any {
				out := []map[string]any{}
				session := ""
				for _, v := range in {
					var p map[string]any
					if json.Unmarshal([]byte(v["body"].(string)), &p) != nil {
						t.Fatal("body")
					}
					s, _ := p["session"].(string)
					if len(s) != 36 {
						t.Fatal("uuid", s)
					}
					if session == "" {
						session = s
					}
					if session != s {
						t.Fatal("different cleanup session")
					}
					copy := map[string]any{}
					for k, x := range v {
						copy[k] = x
					}
					copy["body"] = strings.ReplaceAll(v["body"].(string), s, "<generated-uuid>")
					out = append(out, copy)
				}
				return out
			}
			wantLevels := []string{}
			for _, l := range row.Logs {
				wantLevels = append(wantLevels, strings.ToLower(l.Level))
			}
			if !reflect.DeepEqual(got, row.Result) || !reflect.DeepEqual(normalize(actual), normalize(row.Wire)) || !reflect.DeepEqual(levels, wantLevels) {
				t.Fatalf("original auth session differs result=%v/%v wire=%v/%v stages=%v/%v", got, row.Result, normalize(actual), normalize(row.Wire), levels, wantLevels)
			}
		})
	}
}
