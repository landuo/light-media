package app

import (
	"context"
	"encoding/json"
)

var originalSeedRescueSecretPlaceholders = map[string]string{
	"token":    "[保留已保存的 IYUU Token]",
	"rss_urls": "[保留已保存的 RSS 地址]",
}

func maskOriginalSeedRescueSecrets(fields map[string]json.RawMessage) bool {
	masked := false
	for key, placeholder := range originalSeedRescueSecretPlaceholders {
		var text string
		secret := json.Unmarshal(fields[key], &text) == nil && text != ""
		if key == "rss_urls" && !secret {
			var urls []json.RawMessage
			secret = json.Unmarshal(fields[key], &urls) == nil && len(urls) > 0
		}
		if secret {
			fields[key], _ = json.Marshal(placeholder)
			masked = true
		}
	}
	return masked
}

func (a *App) restoreOriginalSeedRescueSecrets(ctx context.Context, instance string, fields map[string]json.RawMessage) error {
	var saved map[string]json.RawMessage
	for key, placeholder := range originalSeedRescueSecretPlaceholders {
		var text string
		if json.Unmarshal(fields[key], &text) != nil || text != placeholder {
			continue
		}
		if saved == nil {
			raw, _, err := a.readOriginalBaseConfigForInit(ctx, instance)
			if err != nil {
				return err
			}
			if json.Unmarshal(raw, &saved) != nil || saved == nil {
				return bad("SEED_RESCUE_PRIVATE_CONFIG_MISSING", "没有可保留的稀缺保种凭据")
			}
		}
		previous := map[string]json.RawMessage{key: saved[key]}
		if !maskOriginalSeedRescueSecrets(previous) {
			return bad("SEED_RESCUE_PRIVATE_CONFIG_MISSING", "没有可保留的稀缺保种凭据")
		}
		fields[key] = saved[key]
	}
	return nil
}
