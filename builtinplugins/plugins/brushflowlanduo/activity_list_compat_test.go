package brushflowlanduo

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"

	sdk "light-media/builtinplugins/internal/sdk"
)

func TestActivityListFallsBackToStrictLegacyShape(t *testing.T) {
	for _, test := range []struct{ kind, fields string }{
		{"qbittorrent", `{"progress":0.5,"time_active":15000,"seeding_time":0}`},
		{"transmission", `{"percentDone":0.5,"secondsDownloading":15000}`},
	} {
		t.Run(test.kind, func(t *testing.T) {
			var requests []string
			caller := func(_ context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
				switch method {
				case sdk.DownloaderConfigurationsReadCapability:
					return json.RawMessage(`{"schema_version":1,"downloaders":[{"id":"owned","name":"brush","kind":"` + test.kind + `","enabled":true}]}`), nil
				case sdk.DownloaderListCapability:
					requests = append(requests, string(raw))
					// The previous host uses this exact request type and rejects unknown fields.
					var request struct {
						DownloaderID string `json:"downloader_id"`
					}
					decoder := json.NewDecoder(bytes.NewReader(raw))
					decoder.DisallowUnknownFields()
					if err := decoder.Decode(&request); err != nil {
						return nil, &sdk.Error{Code: "host_operation_failed"}
					}
					if request.DownloaderID != "owned" {
						t.Fatalf("changed downloader ID: %q", request.DownloaderID)
					}
					// Even if a host emits counters on a legacy read, the caller did not negotiate them.
					return json.RawMessage(`{"schema_version":1,"downloader_id":"owned","kind":"` + test.kind + `","tasks":[{"hash":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","` + test.kind + `":` + test.fields + `}]}`), nil
				default:
					t.Fatalf("unexpected callback %s", method)
					return nil, nil
				}
			}
			ctx := withBrushHost(t.Context(), caller)
			rows, err := new(standardHost).Torrents(ctx, "brush")
			if err != nil || len(rows) != 1 || torrentHash(rows[0]) != "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" {
				t.Fatalf("could not read the legacy task: %v %v", rows, err)
			}
			if seconds := unfinishedDownloadSeconds(rows[0]); seconds != nil {
				t.Fatalf("legacy result enabled download timeout: %v seconds", seconds)
			}
			if !slices.Equal(requests, []string{`{"downloader_id":"owned","include_activity_time":true}`, `{"downloader_id":"owned"}`}) {
				t.Fatalf("unexpected list requests %v", requests)
			}
		})
	}
}

func TestActivityListHonorsCallbackFailures(t *testing.T) {
	for _, test := range []struct {
		name, code string
		cancel     bool
		maxReads   int
	}{
		{"denied on both reads", "host_operation_failed", false, 2},
		{"canceled before retry", "host_operation_failed", true, 1},
		{"revoked callback", "invalid_host_call", false, 1},
		{"canceled invocation", "execution_canceled", false, 1},
		{"identity mismatch", "identity_mismatch", false, 1},
		{"invalid response", "", false, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			reads := 0
			caller := func(_ context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
				switch method {
				case sdk.DownloaderConfigurationsReadCapability:
					return json.RawMessage(`{"schema_version":1,"downloaders":[{"id":"owned","name":"brush","kind":"qbittorrent","enabled":true}]}`), nil
				case sdk.DownloaderListCapability:
					reads++
					if test.cancel {
						cancel()
					}
					if test.code == "" {
						return json.RawMessage(`{}`), nil
					}
					code := test.code
					if reads == 2 {
						code = "invalid_host_call"
					}
					return nil, &sdk.Error{Code: code}
				default:
					t.Fatalf("unexpected callback %s", method)
					return nil, nil
				}
			}
			rows, err := new(standardHost).Torrents(withBrushHost(ctx, caller), "brush")
			if err == nil || rows != nil || reads != test.maxReads {
				t.Fatalf("rows=%v, err=%v, list reads=%d", rows, err, reads)
			}
			var resultError *sdk.Error
			if test.maxReads == 2 && (!errors.As(err, &resultError) || resultError.Code != "invalid_host_call") {
				t.Fatalf("legacy denial was lost: %v", err)
			}
		})
	}
}

type legacyActivityListHost struct {
	*promotionHost
	caller sdk.HostCaller
}

func (h *legacyActivityListHost) Torrents(ctx context.Context, name string) ([]DownloadState, error) {
	return new(standardHost).Torrents(withBrushHost(ctx, h.caller), name)
}

func TestLegacyActivityListKeepsVerifiedTorrentDuringCheck(t *testing.T) {
	task := promotionTask()
	task.Row["download_time"] = 4
	hash := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	now := promotionNow()
	host := &legacyActivityListHost{promotionHost: &promotionHost{now: now, data: map[string]json.RawMessage{}}}
	readModes := []bool{}
	host.caller = func(_ context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
		switch method {
		case sdk.DownloaderConfigurationsReadCapability:
			return json.RawMessage(`{"schema_version":1,"downloaders":[{"id":"owned","name":"qb","kind":"qbittorrent","enabled":true}]}`), nil
		case sdk.DownloaderListCapability:
			var legacy struct {
				ID string `json:"downloader_id"`
			}
			decoder := json.NewDecoder(bytes.NewReader(raw))
			decoder.DisallowUnknownFields()
			err := decoder.Decode(&legacy)
			readModes = append(readModes, err != nil)
			if err != nil {
				return nil, &sdk.Error{Code: "host_operation_failed"}
			}
			if legacy.ID != "owned" {
				t.Fatalf("wrong downloader: %q", legacy.ID)
			}
			return json.RawMessage(`{"schema_version":1,"downloader_id":"owned","kind":"qbittorrent","tasks":[{"hash":"` + hash +
				`","qbittorrent":{"name":"verified","progress":0.25,"added_on":1,"seeding_time":0,"tags":"` + GlobalTag + `, ` + task.Tag() + `"}}]}`), nil
		default:
			t.Fatalf("unexpected callback %s", method)
			return nil, nil
		}
	}
	if err := host.Put(t.Context(), taskKey(task.ID(), "torrents"), Rows{hash: promotionRecord(task, hash, now, .25)}); err != nil {
		t.Fatal(err)
	}
	core := newCore(host, Config{Settings: Row{"enabled": true}, Tasks: []Task{task}})
	if err := core.Check(t.Context(), task.ID(), true); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(readModes, []bool{true, false}) || len(host.pauses) != 0 || len(host.deletes) != 0 {
		t.Fatalf("legacy check: calls=%v paused=%v deleted=%v", readModes, host.pauses, host.deletes)
	}
}
