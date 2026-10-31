package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"light-media/internal/download"
	"light-media/internal/plugins"
)

func TestSharedQBittorrentCompletedWorkerPermissionAndWire(t *testing.T) {
	a, _, object := ownedQBittorrentCompletedSDKFixture(t, true)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); io.WriteString(w, `[]`) }))
	defer server.Close()
	config := a.Settings()
	config.Downloaders = []DownloaderConfig{{Config: download.Config{ID: "owned", Kind: "qbittorrent", BaseURL: server.URL}}}
	ownedDownloaderSave(t, a, config)
	for _, payload := range []string{`null`, `[]`, `{}`, `{"downloader_id":"owned","ids":true}`, `{"downloader_id":"owned","tags":1}`, `{"downloader_id":"owned","downloader_id":"other"}`, `{"downloader_id":"owned","\u0069ds":[],"ids":[]}`, `{"downloader_id":"owned","tags":"\ud800"}`, `{"downloader_id":"owned","status":"all"}`, `{"downloader_id":"owned","url":"http://other.invalid"}`} {
		raw := json.RawMessage(`{"method":"` + plugins.QBittorrentCompletedQueryCapability + `","payload":` + payload + `}`)
		got, err := object.callSession(context.Background(), plugins.Runner{Timeout: 10 * time.Second}, "resource", raw, nil)
		if err == nil || len(got.Result) != 0 {
			t.Fatal("invalid finite request accepted")
		}
	}
	valid := json.RawMessage(`{"downloader_id":"owned"}`)
	callback, closeCall := object.originalBaseSessionHost(context.Background(), "snapshot", nil)
	identity := plugins.Identity{PluginID: object.installation.PluginID, InstanceID: object.installation.InstanceID, Version: object.installation.Version}
	wrong := identity
	wrong.InstanceID = "other"
	if raw, err := callback(context.Background(), wrong, plugins.QBittorrentCompletedQueryCapability, valid); err == nil || len(raw) != 0 {
		t.Fatal("wrong identity accepted")
	}
	for _, method := range []string{"downloader.rpc", "downloader.qbittorrent.torrents.query", "downloader.qbittorrent.delete"} {
		if raw, err := callback(context.Background(), identity, method, valid); err == nil || len(raw) != 0 {
			t.Fatal("expanded method grant")
		}
	}
	closeCall()
	if raw, err := callback(context.Background(), identity, plugins.QBittorrentCompletedQueryCapability, valid); err == nil || len(raw) != 0 {
		t.Fatal("closed caller accepted")
	}
	_, _, denied := ownedQBittorrentCompletedSDKFixture(t, false)
	got, err := denied.callSession(context.Background(), plugins.Runner{Timeout: 10 * time.Second}, "resource", json.RawMessage(`{"method":"`+plugins.QBittorrentCompletedQueryCapability+`","payload":`+string(valid)+`}`), nil)
	if err == nil || len(got.Result) != 0 || calls.Load() != 0 {
		t.Fatal("undeclared query or invalid wire reached endpoint")
	}
	for _, contract := range []string{pluginPureSDKContract, pluginBaseSDKContract} {
		if validatePluginArtifactContract(ownedQBittorrentCompletedManifest(true), contract) == nil {
			t.Fatal("wrong contract granted query")
		}
	}
}

func TestSharedQBittorrentCompletedDispatchScope(t *testing.T) {
	for _, mode := range []string{"cancel", "revoke", "reconfigure", "disable"} {
		t.Run(mode, func(t *testing.T) {
			a, _, object := ownedQBittorrentCompletedSDKFixture(t, true)
			entered, release := make(chan struct{}), make(chan struct{})
			var selected atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/v2/auth/login" {
					selected.Add(1)
					io.WriteString(w, `[]`)
					return
				}
				close(entered)
				<-release
				http.SetCookie(w, &http.Cookie{Name: "SID", Value: "owned", Path: "/"})
				io.WriteString(w, "Ok.")
			}))
			defer server.Close()
			config := a.Settings()
			config.Downloaders = []DownloaderConfig{{Config: download.Config{ID: "owned", Kind: "qbittorrent", BaseURL: server.URL, Username: "owned", Password: "owned"}}}
			ownedDownloaderSave(t, a, config)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			callback, revoke := object.originalBaseSessionHost(ctx, "snapshot", nil)
			defer revoke()
			identity := plugins.Identity{PluginID: object.installation.PluginID, InstanceID: object.installation.InstanceID, Version: object.installation.Version}
			type outcome struct {
				raw json.RawMessage
				err error
			}
			done := make(chan outcome, 1)
			go func() {
				raw, err := callback(ctx, identity, plugins.QBittorrentCompletedQueryCapability, json.RawMessage(`{"downloader_id":"owned"}`))
				done <- outcome{raw, err}
			}()
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				close(release)
				t.Fatal("owned login barrier not entered")
			}
			switch mode {
			case "cancel":
				cancel()
			case "revoke":
				revoke()
			case "reconfigure":
				config.Downloaders[0].Password = "changed"
				ownedDownloaderSave(t, a, config)
			case "disable":
				no := false
				config.Downloaders[0].Enabled = &no
				ownedDownloaderSave(t, a, config)
			}
			close(release)
			select {
			case got := <-done:
				if got.err == nil || len(got.raw) != 0 {
					t.Fatal("stale invocation returned tasks")
				}
				if mode == "cancel" && !errors.Is(got.err, context.Canceled) {
					t.Fatal("cancel lost")
				}
			case <-time.After(5 * time.Second):
				t.Fatal("scope shutdown blocked")
			}
			if selected.Load() != 0 {
				t.Fatal("selected query passed revoked login guard")
			}
		})
	}
}

func TestSharedQBittorrentCompletedResponseLimit(t *testing.T) {
	a, _, object := ownedQBittorrentCompletedSDKFixture(t, true)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `[{"progress":1,"extra":"`+strings.Repeat("x", plugins.MaxPayloadBytes)+`"}]`)
	}))
	defer server.Close()
	config := a.Settings()
	config.Downloaders = []DownloaderConfig{{Config: download.Config{ID: "owned", Kind: "qbittorrent", BaseURL: server.URL}}}
	ownedDownloaderSave(t, a, config)
	got, err := object.callSession(context.Background(), plugins.Runner{Timeout: 20 * time.Second}, "snapshot", json.RawMessage(`{"downloader_id":"owned"}`), nil)
	if err == nil || len(got.Result) != 0 {
		t.Fatal("overlimit result became source None/partial success")
	}
}
