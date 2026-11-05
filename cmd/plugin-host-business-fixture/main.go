// Owned acceptance worker for ordinary host business resources. This is not an
// installed production business plugin and contains no service credentials.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"light-media/internal/plugins"
	"os"
)

// Distinct bound test releases exercise real host updates without changing
// the resource behavior being checked.
var fixtureVersion = "1.0"

func manifest() plugins.Manifest {
	caps := append(plugins.BaseMethods(), plugins.SitesReadCapability, plugins.SitesAccountReadCapability, plugins.SitesRefreshCapability, plugins.SitesResultWriteCapability, plugins.SitesBrowseCapability, plugins.SitesRSSCapability, plugins.SitesTorrentDownloadCapability, plugins.SubscriptionsReadCapability, plugins.SubscriptionsUpdateCapability, plugins.DownloadHistoryReadCapability, plugins.TransferHistoryReadCapability, plugins.TransferHistoryDeleteCapability, plugins.SettingsReadCapability, plugins.SettingsWriteCapability, plugins.CaptchaOCRTextCapability)
	return plugins.Manifest{SchemaVersion: 1, PluginID: "OwnedHostBusiness", Name: "普通宿主资源验收", Version: fixtureVersion, Runtime: "go-executable", Entry: "plugin-host-business-fixture", HostAPIMin: 1, HostAPIMax: 1, Methods: []string{"init", "get_state", "get_service", "get_command", "get_api", "get_render_mode", "get_form", "get_page", "resource", "http", "stop"}, Capabilities: caps, APIs: []plugins.HTTPAPI{{Key: "run", Path: "/run", Methods: []string{"POST"}, Auth: "bear", Body: "json", BodyRequired: true}}}
}
func main() {
	m := manifest()
	if len(os.Args) == 2 && os.Args[1] == "--manifest" {
		_ = json.NewEncoder(os.Stdout).Encode(m)
		return
	}
	var handle plugins.Handler
	handle = func(ctx context.Context, method string, raw json.RawMessage, host plugins.HostCaller) (json.RawMessage, error) {
		base := plugins.Base{Host: host}
		switch method {
		case "init":
			var config map[string]json.RawMessage
			if err := json.Unmarshal(raw, &config); err != nil {
				return nil, err
			}
			if string(config["run_once"]) == "true" {
				config["run_once"] = json.RawMessage(`false`)
				if _, err := base.UpdateConfig(ctx, config); err != nil {
					return nil, err
				}
				var operation, input string
				if json.Unmarshal(config["method"], &operation) != nil || json.Unmarshal(config["input"], &input) != nil || !json.Valid([]byte(input)) {
					return nil, errors.New("invalid configured acceptance request")
				}
				request, _ := json.Marshal(map[string]any{"method": operation, "input": json.RawMessage(input)})
				if _, err := handle(ctx, "resource", request, host); err != nil {
					return nil, err
				}
			}
			return json.RawMessage(`null`), nil
		case "stop":
			return json.RawMessage(`null`), nil
		case "get_state":
			return json.RawMessage(`true`), nil
		case "get_service", "get_command":
			return json.RawMessage(`[]`), nil
		case "get_api":
			return json.Marshal(m.APIs)
		case "get_render_mode":
			return json.RawMessage(`["vuetify",""]`), nil
		case "get_form":
			return json.RawMessage(`[[{"component":"VTextField","props":{"model":"marker","label":"验收标记"}},{"component":"VTextField","props":{"model":"method","label":"资源方法"}},{"component":"VTextarea","props":{"model":"input","label":"资源参数"}},{"component":"VSwitch","props":{"model":"run_once","label":"立即执行一次"}}],{"marker":"owned","method":"sites.read","input":"{}","run_once":false}]`), nil
		case "get_page":
			key := "last"
			value, err := base.GetData(ctx, &key)
			if err != nil {
				return nil, err
			}
			return json.Marshal([]any{map[string]any{"component": "VAlert", "props": map[string]any{"type": "info"}, "text": string(value)}})
		case "http":
			var r plugins.HTTPRequest
			if err := json.Unmarshal(raw, &r); err != nil {
				return nil, err
			}
			if r.Key != "run" || r.Method != "POST" {
				return nil, errors.New("invalid fixture API")
			}
			raw = r.Body
			fallthrough
		case "resource":
			var in struct {
				Method string          `json:"method"`
				Input  json.RawMessage `json:"input"`
			}
			if err := json.Unmarshal(raw, &in); err != nil {
				return nil, err
			}
			var value any
			var err error
			switch in.Method {
			case plugins.SitesReadCapability:
				var r plugins.SiteHostRequestV1
				err = json.Unmarshal(in.Input, &r)
				if err == nil {
					value, err = (plugins.Sites{Host: host}).Read(ctx, r)
				}
			case plugins.SitesAccountReadCapability:
				var r plugins.SiteAccountHostRequestV1
				err = json.Unmarshal(in.Input, &r)
				if err == nil {
					value, err = (plugins.Sites{Host: host}).Accounts(ctx, r)
				}
			case plugins.SitesRefreshCapability:
				var r plugins.SiteHostRefreshV1
				err = json.Unmarshal(in.Input, &r)
				if err == nil {
					value, err = (plugins.Sites{Host: host}).Refresh(ctx, r.ID)
				}
			case plugins.SitesResultWriteCapability:
				var r plugins.SiteHostAccessResultV1
				err = json.Unmarshal(in.Input, &r)
				if err == nil {
					value, err = (plugins.Sites{Host: host}).RecordAccess(ctx, r)
				}
			case plugins.SitesBrowseCapability:
				var r plugins.SiteBrowseHostRequestV1
				err = json.Unmarshal(in.Input, &r)
				if err == nil {
					value, err = (plugins.Sites{Host: host}).Browse(ctx, r)
				}
			case plugins.SitesTorrentDownloadCapability:
				var r plugins.SiteTorrentHostRequestV1
				err = json.Unmarshal(in.Input, &r)
				if err == nil {
					value, err = (plugins.Sites{Host: host}).Torrent(ctx, r)
				}
			case plugins.SitesRSSCapability:
				var r plugins.SiteHostRefreshV1
				err = json.Unmarshal(in.Input, &r)
				if err == nil {
					value, err = (plugins.Sites{Host: host}).RSS(ctx, r.ID)
				}
			case plugins.SubscriptionsReadCapability:
				var r plugins.SubscriptionHostRequestV1
				err = json.Unmarshal(in.Input, &r)
				if err == nil {
					value, err = (plugins.Subscriptions{Host: host}).Read(ctx, r)
				}
			case plugins.SubscriptionsUpdateCapability:
				var r plugins.SubscriptionHostUpdateV1
				err = json.Unmarshal(in.Input, &r)
				if err == nil {
					value, err = (plugins.Subscriptions{Host: host}).Update(ctx, r)
				}
			case plugins.DownloadHistoryReadCapability:
				var r plugins.DownloadHistoryRequestV1
				err = json.Unmarshal(in.Input, &r)
				if err == nil {
					value, err = (plugins.History{Host: host}).Download(ctx, r)
				}
			case plugins.TransferHistoryReadCapability:
				var r plugins.TransferHistoryRequestV1
				err = json.Unmarshal(in.Input, &r)
				if err == nil {
					value, err = (plugins.History{Host: host}).Transfer(ctx, r)
				}
			case plugins.TransferHistoryDeleteCapability:
				var r plugins.TransferHistoryDeleteV1
				err = json.Unmarshal(in.Input, &r)
				if err == nil {
					value, err = (plugins.History{Host: host}).DeleteTransfer(ctx, r.ID)
				}
			case plugins.CaptchaOCRTextCapability:
				var r plugins.CaptchaOCRRequestV1
				err = json.Unmarshal(in.Input, &r)
				if err == nil {
					value, err = (plugins.CaptchaOCR{Host: host}).Text(ctx, r)
				}
			case plugins.SettingsReadCapability:
				var r plugins.SettingsHostRequestV1
				err = json.Unmarshal(in.Input, &r)
				if err == nil {
					value, err = (plugins.SystemSettings{Host: host}).Get(ctx, r.Key)
				}
			case plugins.SettingsWriteCapability:
				var r plugins.SettingsHostWriteV1
				err = json.Unmarshal(in.Input, &r)
				if err == nil {
					value, err = (plugins.SystemSettings{Host: host}).Set(ctx, r.Key, r.Value)
				}
			default:
				return nil, errors.New("unsupported acceptance resource")
			}
			if err != nil {
				return nil, err
			}
			result, err := json.Marshal(map[string]any{"pid": os.Getpid(), "value": value})
			if err != nil {
				return nil, err
			}
			// Keep private materials private: do not expose torrent bytes in the
			// fixture data-page rendering used by browser acceptance.
			if in.Method != plugins.SitesTorrentDownloadCapability {
				if err = base.SaveData(ctx, "last", json.RawMessage(result)); err != nil {
					return nil, err
				}
			}
			if method == "http" {
				return json.Marshal(plugins.HTTPResponse{Status: 200, Body: result})
			}
			return result, nil
		}
		return nil, errors.New("unknown fixture method")
	}
	if plugins.Serve(context.Background(), os.Stdin, os.Stdout, m, handle) != nil {
		os.Exit(1)
	}
}
