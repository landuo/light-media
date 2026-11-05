package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"light-media/internal/download"
	"light-media/internal/plugins"
)

func TestHostDownloaderConnectionActualSDKSourceEligibility(t *testing.T) {
	for _, mode := range []string{"qb_active", "qb_denied", "qb_bad_body", "qb_unreachable", "qb_disabled", "qb_changed_during_read", "tr_legacy_active", "tr_modern_active", "tr_denied"} {
		t.Run(mode, func(t *testing.T) {
			a, _, object := hostDownloaderFixture(t)
			var hits atomic.Int32
			kind := "qbittorrent"
			if strings.HasPrefix(mode, "tr_") {
				kind = "transmission"
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits.Add(1)
				if strings.HasSuffix(mode, "denied") {
					http.Error(w, "OWNED_PRIVATE_AUTH_BODY", 403)
					return
				}
				if mode == "qb_changed_during_read" {
					a.cfgMu.Lock()
					no := false
					a.config.Downloaders[0].Enabled = &no
					a.cfgMu.Unlock()
				}
				if kind == "qbittorrent" {
					if r.URL.Path != "/api/v2/app/webapiVersion" || r.Method != "GET" {
						t.Error("probe issued non-read-only operation", r.URL.Path)
					}
					body := "2.11.4"
					if mode == "qb_bad_body" {
						body = "OWNED_PRIVATE_INVALID_VERSION"
					}
					_, _ = io.WriteString(w, body)
					return
				}
				var call struct {
					Method string          `json:"method"`
					ID     json.RawMessage `json:"id"`
					Tag    json.RawMessage `json:"tag"`
				}
				if json.NewDecoder(r.Body).Decode(&call) != nil {
					t.Error("invalid RPC")
					return
				}
				if mode == "tr_legacy_active" {
					if call.Method == "session_get" {
						_, _ = io.WriteString(w, `{"result":"method name not recognized","arguments":{}}`)
						return
					}
					if call.Method != "session-get" {
						t.Error("probe used mutating RPC", call.Method)
					}
					_, _ = fmt.Fprintf(w, `{"tag":%s,"result":"success","arguments":{"version":"4.0.6"}}`, call.Tag)
				} else {
					if call.Method != "session_get" {
						t.Error("probe used mutating RPC", call.Method)
					}
					_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":{"version":"4.1.0"}}`, call.ID)
				}
			}))
			defer server.Close()
			if mode == "qb_unreachable" {
				server.Close()
			}
			yes := mode != "qb_disabled"
			a.config.Downloaders = []DownloaderConfig{{Config: download.Config{ID: "owned", Kind: kind, BaseURL: server.URL}, Enabled: &yes}}
			var out plugins.DownloaderConnectionV1
			err := hostDownloaderCall(t, object, "connection", plugins.DownloaderConnectionRequestV1{DownloaderID: "owned"}, &out)
			if mode == "qb_disabled" || mode == "qb_changed_during_read" {
				if err == nil || out.SchemaVersion != 0 {
					t.Fatal("changed/disabled scope became connection status", out, err)
				}
				if mode == "qb_disabled" && hits.Load() != 0 {
					t.Fatal("disabled probe dispatched")
				}
				return
			}
			wantInactive := strings.HasSuffix(mode, "denied") || mode == "qb_bad_body" || mode == "qb_unreachable"
			if err != nil || out.SchemaVersion != 1 || out.DownloaderID != "owned" || out.Kind != kind || out.Inactive != wantInactive {
				t.Fatal("source eligibility differs", out, err, wantInactive)
			}
			encoded, _ := json.Marshal(out)
			if strings.Contains(string(encoded), "OWNED_PRIVATE") || strings.Contains(string(encoded), server.URL) {
				t.Fatal("probe returned private error/endpoint")
			}
		})
	}
}

func TestHostDownloaderConnectionCancellationIsNotStatus(t *testing.T) {
	a, _, object := hostDownloaderFixture(t)
	var hits atomic.Int32
	callCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		cancel()
		_, _ = io.WriteString(w, "2.11.4")
	}))
	defer server.Close()
	a.config.Downloaders = []DownloaderConfig{{Config: download.Config{ID: "owned", Kind: "qbittorrent", BaseURL: server.URL}}}
	identity := plugins.Identity{PluginID: object.installation.PluginID, InstanceID: object.installation.InstanceID, Version: object.installation.Version}
	callback, revoke := object.originalBaseSessionHost(callCtx, "connection", nil)
	defer revoke()
	if body, err := callback(callCtx, identity, plugins.DownloaderConnectionCapability, json.RawMessage(`{"downloader_id":"owned"}`)); err == nil || len(body) != 0 || hits.Load() != 1 {
		t.Fatal("mid-read cancellation became inactive or active", string(body), err, hits.Load())
	}
	if body, err := callback(callCtx, identity, plugins.DownloaderConnectionCapability, json.RawMessage(`{"downloader_id":"owned"}`)); err == nil || len(body) != 0 || hits.Load() != 1 {
		t.Fatal("cancelled callback dispatched again", string(body), err, hits.Load())
	}
}
