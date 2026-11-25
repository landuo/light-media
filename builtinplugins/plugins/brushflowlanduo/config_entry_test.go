package brushflowlanduo

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	sdk "light-media/builtinplugins/internal/sdk"
)

type brushConfigEntryHost struct {
	promotionHost
	saved  Row
	queued []string
}

func (h *brushConfigEntryHost) SaveConfig(_ context.Context, cfg Row) error {
	h.saved = cfg
	return nil
}

func (h *brushConfigEntryHost) Queue(_ context.Context, _ Task, operation string) error {
	h.queued = append(h.queued, operation)
	return nil
}

func (*brushConfigEntryHost) RefreshSchedules(context.Context, []Service) error { return nil }

func TestLegacyMigrationDoesNotDispatchSavedOneShot(t *testing.T) {
	for _, operation := range []string{"brush", "check"} {
		t.Run(operation, func(t *testing.T) {
			h := &brushConfigEntryHost{promotionHost: promotionHost{data: map[string]json.RawMessage{}}}
			raw := Row{"enabled": true, "brushsites": []any{3}, "downloader": "qb", "onlyonce": true}
			p, err := New(t.Context(), h, raw)
			if err != nil {
				t.Fatal(err)
			}
			if _, exists := h.saved["onlyonce"]; exists || len(p.Config().Tasks) != 1 {
				t.Fatalf("migration did not produce a normalized task: %v", h.saved)
			}
			if !slices.Equal(h.queued, []string{"cleanup_tags"}) {
				t.Fatalf("migrating old config dispatched %v", h.queued)
			}
			task := p.Config().Tasks[0]
			if !task.B("enabled") {
				t.Fatal("migrating old config disabled the task")
			}
			res, err := p.Submit(t.Context(), task.ID(), operation)
			if err != nil || !res.Success || !slices.Equal(h.queued, []string{"cleanup_tags", operation}) {
				t.Fatalf("explicit %s was not submitted: queue=%v success=%v err=%v", operation, h.queued, res.Success, err)
			}
		})
	}
}

func TestSettingsAcceptsNumericZeroFromHTTPRequest(t *testing.T) {
	h := &brushConfigEntryHost{}
	p := &Standard{core: NewUninitialized(h)}
	req := sdk.HTTPRequest{Key: "settings", Method: "POST", Path: "/settings",
		Body: json.RawMessage(`{"global_proxy_delete":0,"global_delete_size_range":"invalid hidden draft"}`)}
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, err = p.http(t.Context(), raw)
	if err != nil {
		t.Fatal(err)
	}
	var res sdk.HTTPResponse
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatal(err)
	}
	if res.Status != 200 || h.saved["global_proxy_delete"] != false || h.saved["global_delete_size_range"] != nil {
		t.Fatalf("status=%d saved=%v", res.Status, h.saved)
	}
}

func TestTaskRequestRejectsNonStringIDBeforeMutation(t *testing.T) {
	for _, method := range []string{"POST", "PUT"} {
		t.Run(method, func(t *testing.T) {
			h := &brushConfigEntryHost{promotionHost: promotionHost{data: map[string]json.RawMessage{}}}
			p := &Standard{core: NewUninitialized(h)}
			req := sdk.HTTPRequest{Key: "create", Method: method, Path: "/tasks", Body: json.RawMessage(`{"id":17,"name":"draft","site_id":3,"downloader":"qb"}`)}
			if method == "PUT" {
				req.Key, req.Path = "update", "/tasks/missing"
			}
			raw, err := json.Marshal(req)
			if err != nil {
				t.Fatal(err)
			}
			raw, err = p.http(t.Context(), raw)
			if err != nil {
				t.Fatal(err)
			}
			var res sdk.HTTPResponse
			if err := json.Unmarshal(raw, &res); err != nil {
				t.Fatal(err)
			}
			if res.Status != 422 || h.saved != nil {
				t.Fatalf("status=%d saved=%v", res.Status, h.saved)
			}
		})
	}
}

func TestAddUsesDownloaderSpecificFractionalSpeed(t *testing.T) {
	for _, kind := range []string{"qbittorrent", "transmission"} {
		t.Run(kind, func(t *testing.T) {
			var request *sdk.BrushAddRequestV1
			caller := func(_ context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
				switch method {
				case sdk.SitesReadCapability:
					return json.RawMessage(`{"schema_version":1,"items":[{"id":"owned-site","source_id":3}]}`), nil
				case sdk.DownloaderConfigurationsReadCapability:
					return json.Marshal(sdk.DownloaderConfigurationsV1{SchemaVersion: 1,
						Downloaders: []sdk.BrushDownloaderReferenceV1{{ID: "owned", Name: "qb", Kind: kind, Enabled: true}}})
				case sdk.BrushSourceAddCapability:
					request = new(sdk.BrushAddRequestV1)
					if err := json.Unmarshal(raw, request); err != nil {
						t.Fatal(err)
					}
					return json.RawMessage(`{"schema_version":1,"downloader_id":"owned","accepted":false,"outcome":"rejected","native_error":false}`), nil
				default:
					t.Fatalf("unexpected callback %s", method)
					return nil, nil
				}
			}
			task := promotionTask()
			task.Row["up_speed"], task.Row["dl_speed"] = .5, 2.75
			_, err := new(standardHost).Download(withBrushHost(t.Context(), caller), task, Row{"brush_material_id": "owned-material"}, nil, "owned-tag")
			if err != nil || request == nil {
				t.Fatalf("add request missing: %v", err)
			}
			if request.DownloadLimitBytes == nil || *request.DownloadLimitBytes != 2*1024 {
				t.Fatalf("invalid download limit %v", request.DownloadLimitBytes)
			}
			if kind == "qbittorrent" && request.UploadLimitBytes != nil {
				t.Fatal("qBittorrent must omit a sub-KiB limit")
			}
			if kind == "transmission" && (request.UploadLimitBytes == nil || *request.UploadLimitBytes != 0) {
				t.Fatal("Transmission must receive the explicitly truncated zero")
			}
		})
	}
}
