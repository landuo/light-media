package sitestatistic

import (
	"context"
	"encoding/json"
	"errors"

	plugins "light-media/builtinplugins/internal/sdk"
)

type sdkHost struct {
	caller     plugins.HostCaller
	initConfig json.RawMessage
}

func (h sdkHost) accounts(ctx context.Context, request plugins.SiteAccountHostRequestV1) ([]Data, error) {
	out, e := (plugins.Sites{Host: h.caller}).SourceAccounts(ctx, request)
	if e != nil {
		return nil, e
	}
	rows := []Data{}
	for _, item := range out.Items {
		var row Data
		if e := json.Unmarshal(item.Fields, &row); e != nil {
			return nil, e
		}
		rows = append(rows, row)
	}
	return rows, nil
}
func (h sdkHost) Latest(ctx context.Context) ([]Data, error) {
	return h.accounts(ctx, plugins.SiteAccountHostRequestV1{Latest: true})
}
func (h sdkHost) ByDate(ctx context.Context, date string) ([]Data, error) {
	return h.accounts(ctx, plugins.SiteAccountHostRequestV1{Date: date})
}
func (h sdkHost) ActiveDomains(ctx context.Context) ([]string, error) {
	out, e := (plugins.Sites{Host: h.caller}).Read(ctx, plugins.SiteHostRequestV1{ActiveOnly: true})
	if e != nil {
		return nil, e
	}
	domains := []string{}
	for _, r := range out.Items {
		domains = append(domains, r.Domain)
	}
	return domains, nil
}
func (h sdkHost) GetData(ctx context.Context, key string) (any, error) {
	raw, e := (plugins.Base{Host: h.caller}).GetData(ctx, &key)
	if e != nil {
		return nil, e
	}
	return decode(raw)
}
func (h sdkHost) SaveData(ctx context.Context, key string, value any) error {
	return (plugins.Base{Host: h.caller}).SaveData(ctx, key, value)
}
func (h sdkHost) SaveConfig(ctx context.Context, value map[string]any) error {
	var config any = value
	if len(h.initConfig) > 0 {
		var e error
		config, e = onlyOnceConfig(h.initConfig)
		if e != nil {
			return e
		}
	}
	_, e := (plugins.Base{Host: h.caller}).UpdateConfig(ctx, config)
	return e
}
func (h sdkHost) Post(ctx context.Context, n Notification) error {
	_, e := (plugins.Base{Host: h.caller}).PostMessage(ctx, map[string]any{"mtype": "站点", "title": n.Title, "text": n.Text})
	return e
}
func (h sdkHost) Log(ctx context.Context, level, message string) error {
	return (plugins.Logger{Host: h.caller}).Log(ctx, plugins.LogLevel(level), message)
}

// Source operations expose the common host chain's current DTO/None and the
// actual compiled helper's lookup, not a guessed domain or old account row.
func (h sdkHost) Indexer(ctx context.Context, domain string) (Indexer, error) {
	out, e := (plugins.Sites{Host: h.caller}).SourceIndexer(ctx, domain)
	if e != nil || out.Indexer == nil {
		return nil, e
	}
	value, e := decode(out.Indexer.Fields)
	if e != nil {
		return nil, e
	}
	fields, ok := value.(map[string]any)
	if !ok {
		return nil, errors.New("invalid source indexer metadata")
	}
	if out.Indexer.SiteID != nil {
		fields["_host_site_id"] = *out.Indexer.SiteID
	} else {
		fields["_host_site_id"] = nil
	}
	return Indexer(fields), nil
}
func (h sdkHost) Refresh(ctx context.Context, site Indexer) (json.RawMessage, error) {
	id, ok := site["_host_site_id"].(string)
	if !ok || id == "" {
		return nil, errors.New("source template has no current canonical site identity")
	}
	out, e := (plugins.Sites{Host: h.caller}).SourceRefresh(ctx, id)
	return out.Data, e
}
func (h sdkHost) RefreshAll(ctx context.Context) error {
	_, e := (plugins.Sites{Host: h.caller}).SourceRefreshAll(ctx)
	return e
}
