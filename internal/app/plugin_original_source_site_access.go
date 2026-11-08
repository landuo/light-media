package app

import (
	"context"
	"crypto/hmac"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"light-media/internal/plugins"
)

func (a *App) originalHostSourceSiteAccess(ctx context.Context, scope originalBaseCallScope, raw json.RawMessage) (json.RawMessage, error) {
	var in plugins.SiteSourceAccessResultV1
	if err := originalResourceDecode(raw, &in); err != nil {
		return nil, err
	}
	if in.Seconds != nil && *in.Seconds < 0 {
		return nil, bad("INVALID_PLUGIN_VALUE", "站点耗时不能为负")
	}
	source, err := a.resolveOriginalSourceSite(ctx, scope, in.Reference, false)
	if err != nil {
		return nil, err
	}
	guard := source.guard
	if in.Reference.CustomOrdinal != nil {
		// Follow the existing Base lock order before the existing statistics mutex.
		a.pluginMu.Lock()
		defer a.pluginMu.Unlock()
		a.pluginOriginalBase.mu.Lock()
		defer a.pluginOriginalBase.mu.Unlock()
		guard = func(call context.Context) error {
			if err := call.Err(); err != nil {
				return err
			}
			if err := scope.validate(call); err != nil {
				return err
			}
			_, current, err := a.originalSourceCustomConfigLocked(call, scope)
			if err != nil {
				return err
			}
			if !hmac.Equal([]byte(current), []byte(in.Reference.Snapshot)) {
				return sourceSiteReferenceError()
			}
			return nil
		}
	}
	a.settingsMu.Lock()
	defer a.settingsMu.Unlock()
	if err := guard(ctx); err != nil {
		return nil, err
	}
	key := originalSourceSiteDomain(source.base)
	out := plugins.SiteHostAccessV1{SchemaVersion: 1}
	err = a.Store.Get(ctx, "site_access_statistics", key, &out)
	exists := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	out = originalHostSiteAccessUpdate(out, exists, plugins.SiteHostAccessResultV1{Success: in.Success, Seconds: in.Seconds}, time.Now().In(shanghai()).Format("2006-01-02 15:04:05"))
	if err := guard(ctx); err != nil {
		return nil, err
	}
	if err = a.Store.Put(ctx, "site_access_statistics", key, out); err != nil {
		return nil, err
	}
	return json.Marshal(out)
}
