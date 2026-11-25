package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"light-media/internal/download"
	"light-media/internal/plugins"
)

// Both Transmission dialects must select the whole batch on a single daemon.
// The only torrent-get is the exact ownership read; it never asks for files.
func TestBrushDeleteFilesTransmissionBatch(t *testing.T) {
	for _, dialect := range []string{"legacy", "jsonrpc"} {
		t.Run(dialect, func(t *testing.T) {
			hashes := []string{strings.Repeat("a", 40), strings.Repeat("b", 40)}
			var reads, mutations atomic.Int32
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var call map[string]json.RawMessage
				if err := json.NewDecoder(r.Body).Decode(&call); err != nil {
					t.Error(err)
					return
				}
				var method string
				_ = json.Unmarshal(call["method"], &method)
				var args struct {
					IDs          []string `json:"ids"`
					Fields       []string `json:"fields"`
					DeleteLegacy bool     `json:"delete-local-data"`
					DeleteModern bool     `json:"delete_local_data"`
				}
				key := "params"
				if dialect == "legacy" {
					key = "arguments"
				}
				_ = json.Unmarshal(call[key], &args)
				result := map[string]any{}
				switch strings.ReplaceAll(method, "-", "_") {
				case "session_get":
					if dialect == "legacy" {
						result["version"] = "4.0.6"
					} else {
						result["version"] = "4.1.0"
					}
				case "torrent_get":
					if !slices.Equal(args.IDs, hashes) || slices.Contains(args.Fields, "files") {
						t.Error("unexpected read scope", args)
					}
					reads.Add(1)
					var rows []any
					for _, hash := range hashes {
						rows = append(rows, map[string]any{"hashString": hash, "hash_string": hash, "status": 6})
					}
					result["torrents"] = rows
				case "torrent_remove":
					if !slices.Equal(args.IDs, hashes) || args.DeleteLegacy != (dialect == "legacy") || args.DeleteModern != (dialect == "jsonrpc") {
						t.Error("incorrect batch removal parameters", args)
					}
					mutations.Add(1)
				default:
					t.Error("unexpected method", method)
				}
				if dialect == "legacy" {
					workflowJSON(w, map[string]any{"result": "success", "tag": json.RawMessage(call["tag"]), "arguments": result})
				} else {
					workflowJSON(w, map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(call["id"]), "result": result})
				}
			}))
			t.Cleanup(peer.Close)

			a := newTestApp(t)
			ctx := t.Context()
			downloader := DownloaderConfig{Config: download.Config{ID: "tr", Kind: "transmission", BaseURL: peer.URL}, Name: "Owned TR"}
			a.config.Downloaders = []DownloaderConfig{downloader}
			a.invalidateSettings()
			meta := PluginInstallation{InstanceID: "BrushFlowLanduo", PluginID: "BrushFlowLanduo", Version: "5.2.18", ManifestSchemaVersion: 1, Manifest: json.RawMessage(`{}`)}
			if err := a.pluginStore.Install(ctx, meta); err != nil {
				t.Fatal(err)
			}
			meta, err := a.pluginStore.Installation(ctx, meta.InstanceID)
			if err != nil {
				t.Fatal(err)
			}
			object := &originalPluginInstance{app: a, installation: meta, hostContract: pluginResourcesSDKContract, manifest: plugins.Manifest{Capabilities: []string{plugins.DownloaderDeleteFilesCapability}}}
			lease, callCtx, err := object.reserveOriginalSubmission()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { object.releaseOriginalSubmission(lease); a.wg.Done() })
			scope := originalBaseCallScope{installation: meta}
			for _, hash := range hashes {
				if err := a.originalBrushRememberOwner(ctx, scope, downloader, "original-task", hash); err != nil {
					t.Fatal(err)
				}
			}
			host, revoke := object.originalBaseSessionHost(callCtx, "promotion_expiry", nil)
			defer revoke()
			identity := plugins.Identity{PluginID: meta.PluginID, InstanceID: meta.InstanceID, Version: meta.Version}
			raw, err := host(callCtx, identity, plugins.DownloaderDeleteFilesCapability, mustCleanSeedJSON(plugins.DownloaderDeleteFilesRequestV1{DownloaderID: downloader.ID, Hashes: hashes, BrushOwnership: true}))
			var result plugins.DownloaderDeleteFilesV1
			if err != nil || json.Unmarshal(raw, &result) != nil || !result.DaemonApplied || !result.BookkeepingCompleted || reads.Load() != 1 || mutations.Load() != 1 {
				t.Fatalf("receipt=%s err=%v reads=%d removes=%d", raw, err, reads.Load(), mutations.Load())
			}
		})
	}
}
