package app

import (
	"context"
	"database/sql"
	"errors"
)

// App-owned checks add the live API grant and site settings to the existing
// authenticated route/ordinal owner. They run inside the caller's transaction;
// do not call the non-transactional guardSiteCollection from a Router step.
func (a *App) requireNoticeV2ExecutionTx(ctx context.Context, tx *sql.Tx, route pluginNoticeRoute) error {
	if err := a.pluginStore.requireNoticeV2ExecutionTx(ctx, tx, route); err != nil {
		return err
	}
	return a.requireSiteMessageAuthorityTx(ctx, tx, route)
}
func (a *App) requireNoticeV2Execution(ctx context.Context, route pluginNoticeRoute) error {
	tx, err := a.Store.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	return a.requireNoticeV2ExecutionTx(ctx, tx, route)
}
func (a *App) requireSiteMessageAuthorityTx(ctx context.Context, tx *sql.Tx, route pluginNoticeRoute) error {
	return a.requireSiteMessageAuthorityModeTx(ctx, tx, route, false)
}
func (a *App) requireSiteMessageAuthorityModeTx(ctx context.Context, tx *sql.Tx, route pluginNoticeRoute, accepted bool) error {
	if route.Version != 2 || route.BusinessV2 == nil || route.BusinessV2.Binding.Kind != "site_message" {
		return nil
	}
	identity := route.BusinessV2.Binding.SiteMessage
	if identity == nil {
		return siteMessageNoticeEvidenceError()
	}
	receipt, err := a.pluginStore.readSiteMessageCollection(ctx, tx, identity.Collection)
	if err != nil {
		return err
	}
	return a.requireSiteCollectionAuthorityModeTx(ctx, tx, receipt, accepted)
}

// Fresh source error publication must never use the accepted-ordinal exception.
func (a *App) requireSiteCollectionAuthorityTx(ctx context.Context, tx *sql.Tx, receipt siteMessageCollection) error {
	return a.requireSiteCollectionAuthorityModeTx(ctx, tx, receipt, false)
}

// The source producer and message effects share the same persisted grant/site
// validation. accepted is reserved for already-completed ordinal delivery.
func (a *App) requireSiteCollectionAuthorityModeTx(ctx context.Context, tx *sql.Tx, receipt siteMessageCollection, accepted bool) error {
	var err error
	var sealed []byte
	if err = tx.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind='config' AND id='main'`).Scan(&sealed); err != nil {
		return err
	}
	var settings Settings
	if err = a.Store.Unseal(sealed, &settings); err != nil {
		return err
	}
	matched := false
	for _, site := range settings.Sites {
		if site.ID != receipt.Snapshot.SiteID {
			continue
		}
		// Manual API refresh of an initially disabled site remains allowed. A
		// subsequent enable/disable transition revokes this frozen collection.
		if site.Enabled != receipt.SiteEnabled || (receipt.Origin.Kind == "job" && !site.Enabled) || a.managedSiteKey(site) != receipt.Snapshot.ConfigKey {
			return siteMessageAuthorityRevoked{}
		}
		matched = true
		break
	}
	if !matched {
		return siteMessageAuthorityRevoked{}
	}
	if receipt.Origin.Kind != "api" {
		return nil
	}
	return bad("PLUGIN_ADAPTER_UNAVAILABLE", "旧插件API消息来源尚未适配")
}

// A delayed queue entry may outlive normal source completion. Its dispatcher
// rechecks current authority/site, not the source's already-finished ordinal.
func (a *App) requireSiteMessageQueueAuthorityTx(ctx context.Context, tx *sql.Tx, entry pluginNoticeQueueEntry) error {
	route, _, err := a.pluginStore.readNoticeRoute(ctx, tx, entry.RouteID)
	if err != nil {
		return err
	}
	if route.Version == 2 && route.BusinessV2 != nil && route.BusinessV2.Binding.Kind == "site_message" {
		identity := route.BusinessV2.Binding.SiteMessage
		if identity == nil {
			return siteMessageNoticeEvidenceError()
		}
		receipt, err := a.pluginStore.readSiteMessageCollection(ctx, tx, identity.Collection)
		if err != nil {
			return err
		}
		if receipt.Origin.Kind == "job" {
			var kind, state string
			if err = tx.QueryRowContext(ctx, `SELECT kind,state FROM jobs WHERE id=?`, receipt.Origin.ID).Scan(&kind, &state); err != nil {
				return err
			}
			if kind != "site_refresh" {
				return siteMessageNoticeEvidenceError()
			}
			if state == "cancelled" || state == "canceled" {
				return siteMessageAuthorityRevoked{}
			}
		}
		if entry.Direct {
			if err = a.requireSiteMessageAuthorityModeTx(ctx, tx, route, false); err != nil {
				return err
			}
			if err = a.pluginStore.requireSiteMessageExecutionTx(ctx, tx, *identity); err != nil {
				return err
			}
		} else {
			// An earlier normal post remains accepted when a later source row fails.
			// Verify that ordinal's completion, never infer it from collection status.
			if identity.Ordinal < 0 || identity.Ordinal >= len(receipt.Steps) {
				return siteMessageNoticeEvidenceError()
			}
			step := receipt.Steps[identity.Ordinal]
			if step.State != "complete" {
				return siteMessageNoticeEvidenceError()
			}
			if err = a.requireSiteMessageNoticeTx(ctx, tx, *identity, step.Business, step.Result); err != nil {
				return err
			}
			if receipt.Origin.Kind == "job" {
				var kind, state string
				var generation int64
				if err = tx.QueryRowContext(ctx, `SELECT kind,state,generation FROM jobs WHERE id=?`, receipt.Origin.ID).Scan(&kind, &state, &generation); err != nil {
					return err
				}
				if kind != "site_refresh" || generation < receipt.Origin.Generation {
					return siteMessageNoticeEvidenceError()
				}
				if state == "canceled" || state == "cancelled" {
					return siteMessageAuthorityRevoked{}
				}
				switch state {
				case "queued", "running", "retry_wait", "waiting_paused", "succeeded", "failed":
					// These states describe the collection worker, not this
					// already-completed ordinal. A later local unknown effect
					// may leave the collection incomplete across retries or
					// final failure without revoking the earlier queue item.
					// Only its immutable completion proof above grants send;
					// unfinished ordinals and unknown sends remain blocked.
				default:
					return siteMessageNoticeEvidenceError()
				}
			}

		}
	}
	return a.requireSiteMessageAuthorityModeTx(ctx, tx, route, !entry.Direct)
}
func (a *App) requireSiteMessageQueueAuthority(ctx context.Context, entry pluginNoticeQueueEntry) error {
	tx, err := a.Store.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	return a.requireSiteMessageQueueAuthorityTx(ctx, tx, entry)
}

// Only this typed result certifies that no new delivery is authorized. SQL,
// missing receipt, malformed proof, or unknown execution results never do.
type siteMessageAuthorityRevoked struct{}

func (siteMessageAuthorityRevoked) Error() string { return "site message authority revoked" }
func siteMessageIsAuthorityRevoked(err error) bool {
	var revoked siteMessageAuthorityRevoked
	return errors.As(err, &revoked)
}
