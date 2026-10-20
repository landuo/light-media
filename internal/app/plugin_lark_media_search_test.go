package app

import (
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"light-media/internal/catalog"
	"light-media/internal/networkproxy"
)

func TestPluginLarkActualP0MediaQueryFrozenBeforeReplies(t *testing.T) {
	a := newTestApp(t)
	installUnregisteredLarkConfig(t, a, map[string]any{"enabled": true})
	core, ctx := larkStartedCoreFixture(t, a)
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Query().Get("api_key") != "owned_tmdb" || r.URL.Query().Get("query") != "测试作品" {
			t.Error("wrong actual TMDB query", r.URL)
		}
		switch r.URL.Path {
		case "/3/search/movie":
			fmt.Fprint(w, `{"page":1,"total_pages":1,"total_results":1,"results":[{"id":42,"title":"测试作品","release_date":"2026-01-01"}]}`)
		case "/3/search/tv":
			fmt.Fprint(w, `{"page":1,"total_pages":1,"total_results":1,"results":[{"id":43,"name":"测试作品","first_air_date":"2026-02-01"}]}`)
		default:
			t.Error("unexpected actual P0 endpoint", r.URL.Path)
			w.WriteHeader(400)
		}
	}))
	defer server.Close()
	target, _ := url.Parse(server.URL)
	before := http.DefaultTransport
	http.DefaultTransport = brushFlowMetadataRoundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.Scheme != "https" || r.URL.Host != "api.themoviedb.org" {
			t.Error("escaped fixed TMDB authority", r.URL)
		}
		clone := r.Clone(r.Context())
		u := *r.URL
		clone.URL = &u
		clone.URL.Scheme, clone.URL.Host = target.Scheme, target.Host
		return server.Client().Transport.RoundTrip(clone)
	})
	client := catalog.New(catalog.Credentials{APIKey: "owned_tmdb"})
	http.DefaultTransport = before
	a.cfgMu.Lock()
	a.config.TMDB = TMDBConfig{APIKey: "owned_tmdb"}
	a.cfgMu.Unlock()
	a.catMu.Lock()
	a.catClient = client
	a.catCreds = a.Settings().TMDB
	a.catProxy, _ = networkproxy.Resolve(a.Settings().ProxyHost, os.Getenv)
	a.catMu.Unlock()
	result, e := a.larkSearchMedia(ctx, core, "测试作品 (2026)")
	if e != nil || len(result.Medias) != 2 || result.Medias[0].ID != 43 || calls.Load() != 2 {
		t.Fatal(result, e, calls.Load())
	}
	core.ordinal = 0
	if _, e = a.larkSearchMedia(ctx, core, "测试作品 (2026)"); e != nil || calls.Load() != 2 {
		t.Fatal("actual P0 query replayed", e, calls.Load())
	}
	core.ordinal = 0
	if _, e = a.larkSearchMedia(ctx, core, "不同的作品"); pluginErrorCodeInternal(e) != "LARK_EVIDENCE_MISSING" {
		t.Fatal("source content substituted", e)
	}
}
func TestOriginalCn2anSeasonIntegerBranch(t *testing.T) {
	raw, e := os.ReadFile(filepath.Join("../../testdata/plugins/lark-message-chain-reference", "season-number-oracle.json"))
	if e != nil {
		t.Fatal(e)
	}
	var corpus struct {
		Cases []struct{ Input, Output, Error string }
	}
	if json.Unmarshal(raw, &corpus) != nil || len(corpus.Cases) < 20 {
		t.Fatal("incomplete actual cn2an corpus")
	}
	for _, c := range corpus.Cases {
		t.Run(c.Input, func(t *testing.T) {
			n, ok := new(big.Int).SetString(c.Input, 10)
			if !ok {
				t.Fatal("oracle input")
			}
			got, e := larkSourceSeasonName(n)
			if c.Error != "" {
				if e == nil {
					t.Fatal("source range exception became success", got)
				}
				return
			}
			if e != nil || got != c.Output {
				t.Fatal(got, c.Output, e)
			}
		})
	}
}
func TestPluginLarkNativeModuleDTORoundTripPreservesSourceNone(t *testing.T) {
	for _, name := range []string{"message_parser", "post_message", "post_medias_message", "post_torrents_message", "edit_message", "send_direct_message", "finalize_message", "mark_message_processing_started", "mark_message_processing_finished", "download_feishu_image_to_data_url", "download_feishu_file_bytes", "add_feishu_message_reaction", "delete_feishu_message_reaction"} {
		t.Run(name, func(t *testing.T) {
			in := pluginLarkModuleInput{Name: name}
			raw := mustCleanSeedJSON(in)
			var out pluginLarkModuleInput
			if e := cleanupJournalStrict(raw, &out); e != nil || !larkSameRaw(raw, mustCleanSeedJSON(out)) {
				t.Fatal("native durable DTO rejected source None", e)
			}
		})
	}
}
