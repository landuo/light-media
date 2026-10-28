package app

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
)

// The native indexer has no generic site.* token/config namespace. Its existing
// credential-failure cooldown is the temporary cache invalidated by this source
// consumer. Account observations/history and configured credentials are not
// temporary module caches and must survive the event.
func (a *App) consumeOriginalCoreSiteDataClear(ctx context.Context, raw json.RawMessage) error {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil {
		return bad("PLUGIN_EVENT_INVALID", "站点更新事件数据不是对象")
	}
	if !originalBaseJSONTruthy(fields["domain"]) {
		return nil
	}
	var domain string
	if json.Unmarshal(fields["domain"], &domain) != nil {
		return bad("PLUGIN_EVENT_INVALID", "站点缓存域名不是字符串")
	}
	host, err := standardSiteEventHost(domain)
	if err != nil {
		return err
	}
	prefix := "site." + host
	rows, err := a.Store.DB.QueryContext(ctx, `SELECT id FROM records WHERE kind='site_cooldown' ORDER BY rowid`)
	if err != nil {
		return err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			break
		}
		ids = append(ids, id)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return err
	}
	for _, id := range ids {
		identity, err := a.Store.nativeSiteByAppID(ctx, id)
		if err != nil {
			return err
		}
		candidate, err := standardSiteEventHost(identity.CanonicalOrigin)
		if err != nil || !strings.HasPrefix("site."+candidate+".cooldown", prefix) {
			continue
		}
		// Original SystemConfig.delete commits each key separately. A later SQL
		// failure must not restore earlier successfully cleared cache entries.
		if _, err = a.Store.DB.ExecContext(ctx, `DELETE FROM records WHERE kind='site_cooldown' AND id=?`, id); err != nil {
			return err
		}
	}
	return nil
}

// Keep the fixed split[-2] behavior, including ports, dotless failure and no
// public-suffix interpretation. This differs from get_url_domain on purpose.
func standardSiteEventHost(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	netloc := value
	if strings.HasPrefix(value, "http") {
		parsed, err := url.Parse(value)
		if err != nil {
			return "", err
		}
		netloc = parsed.Host
	}
	if netloc == "" {
		return "", nil
	}
	parts := strings.Split(netloc, ".")
	if len(parts) < 2 {
		return "", bad("PLUGIN_EVENT_INVALID", "站点缓存域名没有原版所需的点分段")
	}
	return parts[len(parts)-2], nil
}
