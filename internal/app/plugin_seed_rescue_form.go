package app

import (
	"context"
	"encoding/json"
	sr "light-media/internal/plugins/seedrescue"
)

const seedRescuePrivateToken = "[保留已保存的 IYUU Token]"
const seedRescuePrivateRSS = "[保留已保存的 RSS 地址]"

func projectSeedRescueSecrets(model map[string]json.RawMessage) {
	for key, marker := range map[string]string{"token": seedRescuePrivateToken, "rss_urls": seedRescuePrivateRSS} {
		var value string
		if json.Unmarshal(model[key], &value) == nil && value != "" {
			model[key] = mustCleanSeedJSON(marker)
			continue
		}
		// The original RSS parser also accepts a list in legacy configuration.
		// Form merging retains that raw shape; never disclose its private URLs.
		if key == "rss_urls" {
			var urls []json.RawMessage
			if json.Unmarshal(model[key], &urls) == nil && len(urls) > 0 {
				model[key] = mustCleanSeedJSON(marker)
			}
		}
	}
}
func (a *App) restoreSeedRescueSecrets(ctx context.Context, instance string, value sr.Object) error {
	previous, e := a.pluginStore.Config(ctx, instance)
	if e != nil {
		return e
	}
	old, e := seedRescueConfig(previous.Value)
	if e != nil {
		return e
	}
	for key, marker := range map[string]string{"token": seedRescuePrivateToken, "rss_urls": seedRescuePrivateRSS} {
		if value[key] == marker {
			if old[key] == nil {
				return bad("SEED_RESCUE_PRIVATE_CONFIG_MISSING", "没有可保留的稀缺保种凭据")
			}
			value[key] = old[key]
		}
	}
	return nil
}
