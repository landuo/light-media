package app

import (
	"context"
	"reflect"
	"sort"
	"time"
)

// A canceled request must not remain queued behind a long-running operation.
// TryLock is used only for admission; the established lock order is unchanged.
func lockSettingsContext(ctx context.Context, mu interface {
	TryLock() bool
	Unlock()
}) error {
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if mu.TryLock() {
			if err := ctx.Err(); err != nil {
				mu.Unlock()
				return err
			}
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// Only authentication changes at the identical site identity qualify. Enabling,
// disabling, origin changes, priorities and any other settings retain the full
// download/import exclusion boundary. settingsMu protects the latest snapshot.
func siteCredentialsOnly(previous, next Settings) bool {
	if len(previous.Sites) != len(next.Sites) {
		return false
	}
	next.Sites = append([]SiteConfig(nil), next.Sites...)
	for i, old := range previous.Sites {
		if next.Sites[i].ID != old.ID {
			return false
		}
		next.Sites[i].Cookie = old.Cookie
		next.Sites[i].APIKey = old.APIKey
		next.Sites[i].Token = old.Token
	}
	return reflect.DeepEqual(previous, next)
}

func (a *App) saveManagedSiteSettings(ctx context.Context, c Settings) error {
	if err := lockSettingsContext(ctx, &a.pluginMu); err != nil {
		return err
	}
	defer a.pluginMu.Unlock()
	return a.persistSettingsWithEffect(ctx, c, nil, siteCredentialsOnly(a.Settings(), c))
}

// Saving is acknowledged from the committed configuration, not from a second
// read of historical statistics that can fail after the write has succeeded.
// GET /sites/manage remains the authoritative statistics endpoint.
func (a *App) managedSiteConfiguration(c Settings) any {
	c.Sites = append([]SiteConfig(nil), c.Sites...)
	sort.SliceStable(c.Sites, func(i, j int) bool { return c.Sites[i].Priority < c.Sites[j].Priority })
	rows := []map[string]any{}
	for _, s := range c.Sites {
		configured, state := siteCredentialConfigured(s), siteSecretState(s)
		hideSiteSecrets(&s)
		rows = append(rows, map[string]any{"config": s, "credential_configured": configured, "credential_state": state, "connection": managedConnection{}, "stats": nil, "stale": false})
	}
	return map[string]any{"configuration_only": true, "sites": rows, "supported": nativeSiteTemplates, "capabilities": map[string]bool{"rss": false, "token": true, "proxy": false, "render": false, "downloader": false, "filter": false, "limit": true, "timeout": true, "user_agent": true}}
}
