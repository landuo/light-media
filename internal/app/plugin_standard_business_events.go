package app

import (
	"context"
	"database/sql"
	"encoding/json"
)

// emitStandardBusinessEvent is the non-durable original EventManager bridge.
// Only the successful owning core commit may call it. It does not read/replay
// the core outbox, persist plugin jobs/receipts, retry handlers, or roll back a
// committed business result after a plugin error. A crash between commit and
// dispatch can lose a notification, exactly as with the original best-effort
// in-memory broadcast. Each currently loaded enabled subscriber is attempted
// once by broadcastOriginalPluginEvent; this is NOT exactly-once delivery.
func (a *App) emitStandardBusinessEvent(ctx context.Context, event string, data any) {
	raw, err := json.Marshal(data)
	if err != nil {
		a.log("ERROR", "plugins", "business_event_invalid", "插件业务事件数据编码失败", "", "")
		return
	}
	// An HTTP client disconnect after successful commit cannot cancel a
	// detached original EventManager callback. The existing manager owns its
	// lifetime and closes all loaded objects during shutdown.
	a.broadcastOriginalPluginEvent(context.WithoutCancel(ctx), event, raw)
}

func (a *App) emitStandardSiteRefreshed(ctx context.Context, siteID string) {
	if siteID == "*" {
		a.emitStandardBusinessEvent(ctx, "site.refreshed", map[string]any{"site_id": "*"})
		return
	}
	identity, err := a.Store.nativeSiteByAppID(context.WithoutCancel(ctx), siteID)
	if err != nil || identity.State != "active" {
		a.log("ERROR", "plugins", "business_event_identity", "插件站点事件身份无法核对", "", "")
		return
	}
	a.emitStandardBusinessEvent(ctx, "site.refreshed", map[string]any{"site_id": identity.publicSourceID()})
}

// Called after config commit and publication, never during reconciliation or
// cold bootstrap. Deleted identities are permanent, so re-adding the same
// hostname cannot retarget the event to a new site or expose credentials.
func (a *App) emitStandardSiteDeletions(ctx context.Context, previous, next Settings) {
	current := make(map[string]bool, len(next.Sites))
	for _, site := range next.Sites {
		current[site.ID] = true
	}
	for _, site := range previous.Sites {
		if current[site.ID] {
			continue
		}
		identity, err := a.Store.nativeSiteByAppID(context.WithoutCancel(ctx), site.ID)
		if err != nil || identity.State != "deleted" {
			a.log("ERROR", "plugins", "business_event_identity", "插件站点删除事件身份无法核对", "", "")
			continue
		}
		a.emitStandardBusinessEvent(ctx, "site.deleted", map[string]any{"site_id": identity.publicSourceID()})
	}
}

func (a *App) emitStandardDownloadDeleted(ctx context.Context, key string) {
	// The source event is emitted after remove_torrents returns, including
	// false/None, not only after a successful reconciliation. Reuse the core's
	// authenticated source projection; do not reinterpret the daemon result.
	data, err := a.downloadDeletedSourceData(context.WithoutCancel(ctx), a.Store.DB, key)
	if err != nil {
		a.log("ERROR", "plugins", "business_event_identity", "插件下载删除事件事实无法核对", "", "")
		return
	}
	a.emitStandardBusinessEvent(ctx, "download.deleted", data)
}

// Reuse the core fact's existing identity to distinguish a newly accepted
// observation from an idempotent core retry. This is not a delivery ledger:
// no extra row, acknowledgement, cursor, startup scan, or replay is created.
func standardBusinessFactIsNewTx(ctx context.Context, tx *sql.Tx, eventID string) (bool, error) {
	var count int
	err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM domain_events WHERE event_id=?`, eventID).Scan(&count)
	return count == 0, err
}

func (a *App) emitStandardSubscriptionAdded(ctx context.Context, subscriptionID string, username, mediaInfo json.RawMessage) {
	if len(username) == 0 {
		username = json.RawMessage("null")
	}
	// Use the exact already-recognized/frozen MediaInfo, not catalog.Media,
	// and never fetch/re-recognize after successful creation.
	a.emitStandardBusinessEvent(ctx, "subscribe.added", struct {
		SubscribeID string          `json:"subscribe_id"`
		Username    json.RawMessage `json:"username"`
		MediaInfo   json.RawMessage `json:"mediainfo"`
	}{subscriptionID, username, mediaInfo})
}
