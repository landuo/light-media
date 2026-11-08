// Owned acceptance driver, no SiteStatistic business implementation.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"light-media/internal/plugins"
	"os"
)

func main() {
	m := plugins.Manifest{SchemaVersion: 1, PluginID: "SourceSitesTest", Name: "Owned source sites acceptance", Version: "1.0", Runtime: "go-executable", Entry: "plugin-source-sites-fixture", HostAPIMin: 1, HostAPIMax: 1, Methods: []string{"init", "get_state", "get_service", "get_command", "get_api", "get_render_mode", "source_indexer", "source_indexers", "source_refresh", "source_refresh_all", "legacy_refresh", "accounts", "source_accounts", "resource", "stop"}, Capabilities: []string{plugins.SitesSourceIndexerCapability, plugins.SitesSourceIndexersCapability, plugins.SitesSourceRefreshCapability, plugins.SitesSourceRefreshAllCapability, plugins.SitesRefreshCapability, plugins.SitesAccountReadCapability, plugins.SitesSourceAccountsCapability}}
	if len(os.Args) == 2 && os.Args[1] == "--manifest" {
		json.NewEncoder(os.Stdout).Encode(m)
		return
	}
	h := func(ctx context.Context, method string, raw json.RawMessage, host plugins.HostCaller) (json.RawMessage, error) {
		s := plugins.Sites{Host: host}
		var out any
		var err error
		switch method {
		case "init", "stop":
			return json.RawMessage(`null`), nil
		case "get_state":
			return json.RawMessage(`false`), nil
		case "get_service", "get_command", "get_api":
			return json.RawMessage(`[]`), nil
		case "get_render_mode":
			return json.RawMessage(`["vuetify",null]`), nil
		case "source_indexer":
			var r plugins.SiteSourceIndexerRequestV1
			if err = json.Unmarshal(raw, &r); err == nil {
				out, err = s.SourceIndexer(ctx, r.Domain)
			}
		case "source_indexers":
			out, err = s.SourceIndexers(ctx)
		case "source_refresh_all":
			out, err = s.SourceRefreshAll(ctx)
		case "source_refresh", "legacy_refresh":
			var r plugins.SiteHostRefreshV1
			if err = json.Unmarshal(raw, &r); err == nil {
				if method == "source_refresh" {
					out, err = s.SourceRefresh(ctx, r.ID)
				} else {
					out, err = s.Refresh(ctx, r.ID)
				}
			}
		case "accounts", "source_accounts":
			var r plugins.SiteAccountHostRequestV1
			if err = json.Unmarshal(raw, &r); err == nil {
				if method == "source_accounts" {
					out, err = s.SourceAccounts(ctx, r)
				} else {
					out, err = s.Accounts(ctx, r)
				}
			}
		case "resource":
			var r struct {
				Method string          `json:"method"`
				Value  json.RawMessage `json:"value"`
			}
			if err = json.Unmarshal(raw, &r); err != nil {
				return nil, err
			}
			return host(ctx, r.Method, r.Value)
		default:
			return nil, errors.New("unsupported owned source sites method")
		}
		if err != nil {
			return nil, err
		}
		return json.Marshal(out)
	}
	if plugins.Serve(context.Background(), os.Stdin, os.Stdout, m, h) != nil {
		os.Exit(1)
	}
}
