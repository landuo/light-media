package app

import (
	"context"
	"encoding/json"
	"io"
	"light-media/internal/catalog"
	"net/http"
	"os"
	"strings"
	"testing"
)

func TestOriginalHostMediaBangumiNativeFixedSource(t *testing.T) {
	testOriginalHostMediaNativeFixedSource(t, "bangumi")
}
func TestOriginalHostMediaDoubanNativeFixedSource(t *testing.T) {
	testOriginalHostMediaNativeFixedSource(t, "douban")
}
func testOriginalHostMediaNativeFixedSource(t *testing.T, source string) {
	raw, err := os.ReadFile("../../testdata/plugin-host-integration-20261031/source-models/" + source + "-native-cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var oracle struct {
		Cases []struct {
			Case     string
			Request  map[string]any
			Expected json.RawMessage
			Error    *string
			Replies  []struct {
				Host, Method, Path string
				Query              map[string][]string
				Status             int
				Body               json.RawMessage
			}
		}
	}
	if err = json.Unmarshal(raw, &oracle); err != nil {
		t.Fatal(err)
	}
	for _, row := range oracle.Cases {
		t.Run(row.Case, func(t *testing.T) {
			a := newTestApp(t)
			offset := 0
			explorer := catalog.NewExplorer(nil, imageTransport(func(r *http.Request) (*http.Response, error) {
				if offset >= len(row.Replies) {
					t.Errorf("extra HTTP %s", r.URL.Path)
					return &http.Response{StatusCode: 404, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{}`))}, nil
				}
				reply := row.Replies[offset]
				offset++
				if r.URL.Host != reply.Host || r.Method != reply.Method || r.URL.Path != reply.Path {
					t.Errorf("request %s %s expected %s %s", r.Method, r.URL, reply.Method, reply.Path)
				}
				for key, want := range reply.Query {
					if !originalBaseJSONEqual(mustCleanSeedJSON(r.URL.Query()[key]), mustCleanSeedJSON(want)) {
						t.Errorf("query %s=%v want %v", key, r.URL.Query()[key], want)
					}
				}
				return &http.Response{StatusCode: reply.Status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(string(reply.Body)))}, nil
			}))
			defer explorer.CloseIdleConnections()
			explicit := row.Request
			kwargs, err := originalCatalogRecognitionCallKwargs(sourceMap(row.Request["meta"]), explicit)
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.WithValue(context.Background(), originalHostMediaFreshContextKey{}, true)
			var actual json.RawMessage
			if source == "bangumi" {
				actual, err = a.originalHostMediaRecognizeBangumi(ctx, explorer, kwargs)
			} else {
				actual, err = a.originalHostMediaRecognizeDouban(ctx, explorer, kwargs)
			}
			if (err != nil) != (row.Error != nil) {
				t.Fatal(err)
			}
			gotValue, _ := decodeSourceJSON(actual)
			want, _ := decodeSourceJSON(row.Expected)
			if got := sourceMap(gotValue); got != nil {
				delete(got, "recognize_cache_hit")
			}
			if !originalBaseValueEqual(gotValue, want) {
				t.Errorf("result got=%s want=%s", actual, row.Expected)
			}
			if offset != len(row.Replies) {
				t.Errorf("HTTP count %d want %d", offset, len(row.Replies))
			}
		})
	}
}
