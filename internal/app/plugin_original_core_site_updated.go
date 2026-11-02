package app

import (
	"context"
	"encoding/json"
	"strings"
)

// Reuse the configured local indexer and the existing observation writer. This
// is an ordinary EventManager callback, with no persisted job or replay lease.
func (a *App) consumeOriginalCoreSiteUpdated(ctx context.Context, raw json.RawMessage) error {
	var input map[string]json.RawMessage
	if json.Unmarshal(raw, &input) != nil || input == nil {
		return bad("PLUGIN_EVENT_INVALID", "站点更新事件数据不是对象")
	}
	if !originalBaseJSONTruthy(input["domain"]) {
		return nil
	}
	var domain string
	if json.Unmarshal(input["domain"], &domain) != nil {
		return nil // no configured string domain can identify this value
	}
	if strings.HasPrefix(domain, "http") {
		domain = standardSiteEventDomain(domain)
	}
	for _, site := range a.Settings().Sites {
		if standardSiteEventDomain(site.BaseURL) != domain {
			continue
		}
		ctx = originalPluginSiteContext(allowDisabledSiteRequests(ctx))
		snapshot, err := a.collectManagedSiteObserved(ctx, site.ID, "", &siteCollectionHooks{OrdinaryEvent: true})
		if err != nil {
			return err
		}
		// The observation commit and SiteRefreshed enqueue precede messages.
		// Preserve source order: earlier messages survive a later malformed row.
		for _, step := range siteMessageBusinesses(site.Name, site.BaseURL, snapshot) {
			raw, err := siteMessageBusinessAt(site.Name, site.BaseURL, snapshot, step)
			if err != nil {
				return err
			}
			var message sourceBusinessNotice
			if err = json.Unmarshal(raw, &message); err != nil {
				return err
			}
			if sourceSubscriptionTruth(message.Envelope["source"]) {
				tx, err := a.Store.DB.BeginTx(ctx, nil)
				if err != nil {
					return err
				}
				exists, err := a.siteMessageSourceExistsTx(ctx, tx, message.Envelope["source"])
				tx.Rollback()
				if err != nil {
					return err
				}
				if exists {
					continue
				}
			}
			if _, err = a.postOriginalPluginMessage(ctx, message.Envelope); err != nil {
				return err
			}
		}
		return nil
	}
	return nil
}
