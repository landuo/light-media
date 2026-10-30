package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"light-media/internal/download"
	"light-media/internal/plugins"
)

func TestSharedTransmissionQueryWorkerPermissionAndWire(t *testing.T) {
	a, _, object := ownedTransmissionQuerySDKFixture(t, true)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.Error(w, "owned", http.StatusUnauthorized)
	}))
	defer server.Close()
	config := a.Settings()
	config.Downloaders = []DownloaderConfig{{Config: download.Config{ID: "owned", Kind: "transmission", BaseURL: server.URL}}}
	ownedDownloaderSave(t, a, config)
	invalid := []string{
		`null`, `[]`, `{}`, `{"downloader_id":""}`, `{"downloader_id":"owned","Downloader_id":"other"}`, `{"downloader_id":"owned","downloader_id":"other"}`,
		`{"downloader_id":"owned","\u0069ds":[],"ids":[]}`, `{"downloader_id":"owned","rpc":"session-set"}`, `{"downloader_id":"owned","status":true}`, `{"downloader_id":"owned","tags":[1]}`,
		`{"downloader_id":"owned","tags":"\ud800"}`, `{"downloader_id":"owned","status":"\udc00"}`,
	}
	for _, payload := range invalid {
		raw := json.RawMessage(`{"method":"` + plugins.TransmissionTorrentsQueryCapability + `","payload":` + payload + `}`)
		result, err := object.callSession(context.Background(), plugins.Runner{Timeout: 10 * time.Second}, "resource", raw, nil)
		if err == nil || len(result.Result) != 0 {
			t.Fatal("malformed resource request accepted")
		}
	}
	if requests.Load() != 0 {
		t.Fatal("malformed request reached daemon")
	}
	valid := json.RawMessage(`{"downloader_id":"owned"}`)
	callback, closeCall := object.originalBaseSessionHost(context.Background(), "snapshot", nil)
	identity := plugins.Identity{PluginID: object.installation.PluginID, InstanceID: object.installation.InstanceID, Version: object.installation.Version}
	wrong := identity
	wrong.InstanceID = "another-owned-test-instance"
	if raw, err := callback(context.Background(), wrong, plugins.TransmissionTorrentsQueryCapability, valid); err == nil || len(raw) != 0 {
		t.Fatal("different invocation identity accepted")
	}
	for _, method := range []string{"downloader.rpc", "http.request", "downloader.transmission.torrents.delete"} {
		if raw, err := callback(context.Background(), identity, method, valid); err == nil || len(raw) != 0 {
			t.Fatal("ungranted method accepted")
		}
	}
	closeCall()
	if raw, err := callback(context.Background(), identity, plugins.TransmissionTorrentsQueryCapability, valid); err == nil || len(raw) != 0 {
		t.Fatal("closed callback accepted")
	}
	_, _, denied := ownedTransmissionQuerySDKFixture(t, false)
	raw := json.RawMessage(`{"method":"` + plugins.TransmissionTorrentsQueryCapability + `","payload":` + string(valid) + `}`)
	if result, err := denied.callSession(context.Background(), plugins.Runner{Timeout: 10 * time.Second}, "resource", raw, nil); err == nil || len(result.Result) != 0 {
		t.Fatal("undeclared actual worker capability accepted")
	}
	if requests.Load() != 0 {
		t.Fatal("permission denial reached daemon")
	}
	for _, contract := range []string{pluginPureSDKContract, pluginBaseSDKContract} {
		if validatePluginArtifactContract(ownedTransmissionQueryManifest(true), contract) == nil {
			t.Fatal("non-resource artifact accepted query grant")
		}
	}
	t.Log("actual compiled standard worker: malformed/Unicode/duplicate-key/unknown capability rejected; explicit source identity and closed invocation checked; daemon requests=0")
}

func TestSharedTransmissionQueryDispatchScopeBoundaries(t *testing.T) {
	// Owned protocol barrier, NOT a real-daemon equivalence claim. It controls
	// exactly when a session response arrives to exercise the current host scope.
	for _, mode := range []string{"cancel", "revoke", "reconfigure", "disable"} {
		t.Run(mode, func(t *testing.T) {
			a, _, object := ownedTransmissionQuerySDKFixture(t, true)
			entered, release := make(chan struct{}), make(chan struct{})
			var selected atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request struct {
					Method string `json:"method"`
					Tag    uint64 `json:"tag"`
				}
				_ = json.NewDecoder(r.Body).Decode(&request)
				if request.Method != "session-get" {
					selected.Add(1)
					t.Error("read passed revoked/reconfigured binding")
				}
				close(entered)
				<-release
				_ = json.NewEncoder(w).Encode(map[string]any{"result": "success", "tag": request.Tag, "arguments": map[string]any{"version": "owned-protocol-fixture"}})
			}))
			defer server.Close()
			config := a.Settings()
			config.Downloaders = []DownloaderConfig{{Config: download.Config{ID: "owned", Kind: "transmission", BaseURL: server.URL}}}
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
				raw, err := callback(ctx, identity, plugins.TransmissionTorrentsQueryCapability, json.RawMessage(`{"downloader_id":"owned"}`))
				done <- outcome{raw, err}
			}()
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				close(release)
				t.Fatal("owned barrier not entered")
			}
			switch mode {
			case "cancel":
				cancel()
			case "revoke":
				revoke()
			case "reconfigure":
				config.Downloaders[0].Password = "changed-owned-test"
				ownedDownloaderSave(t, a, config)
			case "disable":
				no := false
				config.Downloaders[0].Enabled = &no
				ownedDownloaderSave(t, a, config)
			}
			close(release)
			select {
			case out := <-done:
				if out.err == nil || len(out.raw) != 0 {
					t.Fatal("invalidated invocation returned data")
				}
				if mode == "cancel" && !errors.Is(out.err, context.Canceled) {
					t.Fatal("cancellation semantics changed")
				}
			case <-time.After(5 * time.Second):
				t.Fatal("cancelled operation failed to finish")
			}
			if selected.Load() != 0 {
				t.Fatal("guard not applied before selected read")
			}
		})
	}
}

func TestSharedTransmissionQueryWireLimitExplicitFailure(t *testing.T) {
	a, _, object := ownedTransmissionQuerySDKFixture(t, true)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Method string `json:"method"`
			Tag    uint64 `json:"tag"`
		}
		_ = json.NewDecoder(r.Body).Decode(&request)
		args := map[string]any{"version": "owned"}
		if request.Method == "torrent-get" {
			args = map[string]any{"torrents": []any{map[string]any{"labels": []string{}, "name": strings.Repeat("x", plugins.MaxPayloadBytes)}}}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"result": "success", "tag": request.Tag, "arguments": args})
	}))
	defer server.Close()
	config := a.Settings()
	config.Downloaders = []DownloaderConfig{{Config: download.Config{ID: "owned", Kind: "transmission", BaseURL: server.URL}}}
	ownedDownloaderSave(t, a, config)
	result, err := object.callSession(context.Background(), plugins.Runner{Timeout: 20 * time.Second}, "snapshot", json.RawMessage(`{"downloader_id":"owned"}`), nil)
	if err == nil || len(result.Result) != 0 {
		t.Fatal("over-limit response was truncated or became empty source success")
	}
	t.Log("owned synthetic protocol response >16 MiB rejected explicitly; source Python has no matching limit; not real-daemon large-list acceptance")
}
