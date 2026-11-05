package browserrender

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/andybalholm/brotli"
)

func browserOracleFile(t *testing.T, name string) []byte {
	t.Helper()
	directory := os.Getenv("BROWSER_SOURCE_ORACLE_DIR")
	if directory == "" {
		directory = "../../testdata/plugin-host-services-20261031/shared-runtime-review"
	}
	raw, err := os.ReadFile(filepath.Join(directory, name))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestFlareSolverrActualVendorHTTPOracle(t *testing.T) {
	for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "all_proxy", "NO_PROXY", "no_proxy"} {
		t.Setenv(key, "")
	}
	var corpus struct {
		SourceCommit string `json:"source_commit"`
		Cases        []struct {
			Case   string
			Result json.RawMessage
		}
		Traces []struct {
			Headers map[string]string
			JSON    map[string]any
		}
	}
	if json.Unmarshal(browserOracleFile(t, "fs-http-source-oracle.json"), &corpus) != nil || corpus.SourceCommit != "8a83efeacd3e26b82be495e17d50146e31c96a60" || len(corpus.Cases) != 6 {
		t.Fatal("fixed actual HTTP oracle missing")
	}
	for index, row := range corpus.Cases {
		t.Run(row.Case, func(t *testing.T) {
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				for name, wanted := range corpus.Traces[index].Headers {
					if name != "Content-Length" && r.Header.Get(name) != wanted {
						t.Errorf("source wire header %s differs", name)
					}
				}
				if r.Header.Get("Accept") != "" {
					t.Error("source Accept=None was restored")
				}
				var payload map[string]any
				if json.NewDecoder(r.Body).Decode(&payload) != nil || !reflect.DeepEqual(payload, corpus.Traces[index].JSON) {
					t.Error("source JSON payload changed")
				}
				body := []byte(`{"status": "ok", "solution": {"response": "owned"}}`)
				if row.Case == "gzip" {
					var compressed bytes.Buffer
					zipper := gzip.NewWriter(&compressed)
					_, _ = zipper.Write(body)
					_ = zipper.Close()
					body = compressed.Bytes()
					w.Header().Set("Content-Encoding", "gzip")
				}
				if row.Case == "br" {
					var compressed bytes.Buffer
					zipper := brotli.NewWriter(&compressed)
					_, _ = zipper.Write(body)
					_ = zipper.Close()
					body = compressed.Bytes()
					w.Header().Set("Content-Encoding", "br")
				}
				if row.Case == "500" {
					w.WriteHeader(500)
				}
				if row.Case == "invalid" {
					body = []byte("owned invalid JSON")
				}
				if row.Case == "progress" {
					for len(body) > 0 {
						n := min(10, len(body))
						_, _ = w.Write(body[:n])
						w.(http.Flusher).Flush()
						body = body[n:]
						time.Sleep(50 * time.Millisecond)
					}
				} else {
					_, _ = w.Write(body)
				}
			}))
			defer peer.Close()
			timeout := 3 * time.Second
			if row.Case == "progress" {
				timeout = 150 * time.Millisecond
			}
			start := time.Now()
			got, err := (Config{}).fsPost(context.Background(), peer.URL, corpus.Traces[index].JSON, timeout, nil)
			if string(row.Result) == "null" {
				if got != nil {
					t.Fatal("source None became JSON")
				}
				return
			}
			if err != nil {
				t.Fatal("source post_json success failed", err)
			}
			var want map[string]any
			if json.Unmarshal(row.Result, &want) != nil || !reflect.DeepEqual(got, want) {
				t.Fatal("source post_json result differs")
			}
			if row.Case == "progress" && time.Since(start) < timeout {
				t.Fatal("slow complete response was not exercised")
			}
		})
	}
}

func TestCookieParseFixedOracle(t *testing.T) {
	var corpus struct {
		CookieCases []struct {
			Input    string
			Expected json.RawMessage
		} `json:"cookie_cases"`
	}
	if json.Unmarshal(browserOracleFile(t, "browser-source-oracle.json"), &corpus) != nil || len(corpus.CookieCases) != 11 {
		t.Fatal("fixed cookie oracle missing")
	}
	for _, row := range corpus.CookieCases {
		var want any
		if json.Unmarshal(row.Expected, &want) != nil {
			t.Fatal("invalid source oracle")
		}
		raw, _ := json.Marshal(parseCookies(row.Input))
		var got any
		_ = json.Unmarshal(raw, &got)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("source cookie parse differs for %q: %s", row.Input, strings.TrimSpace(string(raw)))
		}
	}
}
