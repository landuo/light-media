package app

import (
	"context"
	"slices"
	"time"

	"light-media/internal/releasemeta"
)

type subscriptionMaterialFailureRound struct {
	lookupAt     int64
	parser       *releasemeta.MetaInfoParser
	fingerprints map[int]string
	active       map[string]bool
}

func (r *subscriptionMaterialFailureRound) fingerprint(ctx context.Context, a *App, sub Subscription, site SiteConfig, candidate Candidate, index int) (string, error) {
	if key := r.fingerprints[index]; key != "" {
		return key, nil
	}
	var meta *releasemeta.MetaObject
	var err error
	if len(candidate.originalSelectionMeta) > 0 {
		meta, err = releasemeta.DecodeMetaObjectState(candidate.originalSelectionMeta)
	} else {
		if r.parser == nil {
			recognition, ok := ctx.Value(resourceRecognitionKey{}).(resourceRecognition)
			if !ok {
				return "", acquisitionEvidence()
			}
			r.parser, err = releasemeta.NewMetaInfoParser(releasemeta.MetaInfoEnvironment{Options: recognition.options})
			if err != nil {
				return "", err
			}
		}
		subtitle := candidate.Resource.SourceSubtitle
		if subtitle == nil && candidate.Resource.Subtitle != "" {
			subtitle = &candidate.Resource.Subtitle
		}
		meta, err = r.parser.ParseTitle(candidate.Resource.Title, subtitle, nil)
	}
	if err != nil {
		return "", err
	}
	resource := acquisitionResourceKey(candidate.Resource)
	if batch := originalChatLatestFromContext(ctx); batch != nil && batch.Mode == "rss" {
		proof, err := a.originalChatLatestSelect(batch, site, candidate.Resource.ID)
		if err != nil {
			return "", err
		}
		material, err := a.originalHostReadSiteMaterial(ctx, site, proof.Row.Resource.ID)
		if err != nil {
			return "", err
		}
		resource = material.FailureResourceKey
		if resource == "" {
			// Older sealed rows contain only the resolved enclosure.
			resource = acquisitionResourceURLKey(material.URL)
		}
	}
	key, err := a.subscriptionFailureFingerprint(ctx, sub, site.ID, resource, meta)
	if err == nil {
		r.fingerprints[index] = key
	}
	return key, err
}

func (a *App) recordSubscriptionMaterialFailure(ctx context.Context, j Job, sub Subscription, site SiteConfig, fingerprint string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if a.paused() {
		return bad("AUTOMATION_PAUSED", "自动化已暂停")
	}
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var state string
	var generation int
	if err := tx.QueryRowContext(ctx, `SELECT state,generation FROM jobs WHERE id=? AND kind='subscription'`, j.ID).Scan(&state, &generation); err != nil {
		return err
	}
	if state != "running" || generation != j.Generation {
		return bad("JOB_CANCELLED", "任务已取消")
	}
	var current Subscription
	if err := qualityRecord(ctx, tx, "subscription", sub.ID, &current); err != nil {
		return err
	}
	if !a.subscriptionSearchState(ctx, current) || current.Revision != sub.Revision || current.BestVersion {
		return bad("REVIEW_SUBSCRIPTION_CHANGED", "订阅已修改、暂停或取消，本轮检查已失效")
	}
	settings := a.Settings()
	selected := subscriptionSiteControls(settings.RSSSites, current.SiteIDs)
	if len(selected) != 0 && !slices.Contains(selected, site.ID) {
		return bad("REVIEW_SITE_CHANGED", "订阅取种站点范围已变化")
	}
	if !slices.ContainsFunc(settings.Sites, func(configured SiteConfig) bool {
		return configured.ID == site.ID && configured.Enabled && a.originalChatLatestSiteKey(configured, settings.ProxyHost) == a.originalChatLatestSiteKey(site, settings.ProxyHost)
	}) {
		return bad("REVIEW_SITE_CHANGED", "订阅取种站点设置已变化")
	}
	projection, err := a.subscriptionSourceProjectionFrom(ctx, tx, current)
	if err != nil {
		return err
	}
	source, err := subscriptionAcquisitionSource(projection)
	if err != nil {
		return err
	}
	if err := a.recordSubscriptionFailureTx(ctx, tx, source, fingerprint, "下载种子内容为空"); err != nil {
		return err
	}
	return tx.Commit()
}

func newSubscriptionMaterialFailureRound() subscriptionMaterialFailureRound {
	return subscriptionMaterialFailureRound{lookupAt: time.Now().Unix(), fingerprints: map[int]string{}, active: map[string]bool{}}
}
