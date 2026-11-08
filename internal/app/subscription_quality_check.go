package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"time"

	"light-media/internal/subscriptionquality"
)

// A library slot is evidence of a file, not of a source priority. Quality
// subscriptions keep searching that slot until the accepted source reaches 100.
func (a *App) checkQualitySubscription(ctx context.Context, j Job, s Subscription) error {
	resumed, err := a.resumeKnownSubscriptionAcquisition(ctx, j)
	if err != nil {
		return err
	}
	if resumed {
		if err = a.Store.Get(ctx, "subscription", s.ID, &s); err != nil {
			return err
		}
		return a.recordQualitySubscriptionCheck(ctx, j, s, "已根据实际下载接受收据恢复质量记录，未重复提交下载", nil)
	}
	state, err := a.subscriptionQualitySourceState(ctx, s)
	if err != nil {
		return err
	}
	prior, priorErr := a.readAcquisition(ctx, a.Store.DB, j.ID)
	if priorErr != nil && !errors.Is(priorErr, sql.ErrNoRows) {
		return priorErr
	}
	provider := providerNativeMedia(s.Media)
	var info *sourceMediaInformation
	if provider {
		if priorErr == nil {
			info = prior.Request.originalSearchInfo
			if info == nil || mediaKey(info.Media) != mediaKey(s.Media) {
				return acquisitionEvidence()
			}
		} else {
			recognized, err := a.sourceRecognizeSubscriptionMedia(ctx, s)
			if err != nil {
				return err
			}
			info = &recognized
		}
	}
	dates := map[int]string{}
	metadataDates := []string{}
	available := []int{}
	if s.Media.MediaType == "tv" {
		var providerModel []byte
		if info != nil {
			providerModel = info.MediaInfo
		}
		season, err := a.importSeason(ctx, s.Media, providerModel, s.Season)
		if err != nil {
			return err
		}
		total := 0
		today := time.Now().In(shanghai()).Format("2006-01-02")
		for _, ep := range season.Episodes {
			total = max(total, ep.EpisodeNumber)
			date, skip := effectiveEpisodeDate(s, ep.EpisodeNumber, ep.AirDate)
			if skip {
				continue
			}
			dates[ep.EpisodeNumber] = date
			if date != "" {
				metadataDates = append(metadataDates, date)
			}
			aired := date != "" && date <= today
			if provider {
				aired = providerEpisodeAired(date, info.Media.ReleaseDate, today)
			}
			if ep.EpisodeNumber >= subscriptionStartEpisode(s) && !slices.Contains(s.Excluded, ep.EpisodeNumber) && (aired || slices.Contains(s.AllowEarly, ep.EpisodeNumber)) {
				available = append(available, ep.EpisodeNumber)
			}
		}
		if !s.ManualTotalEpisode && total > state.TotalEpisode {
			state = state.ExpandTotal(total)
			if err = a.storeQualityProgress(ctx, s, state); err != nil {
				return err
			}
			setSubscriptionQualityState(&s, state)
		}
		available = slices.DeleteFunc(available, func(ep int) bool { return ep > state.TotalEpisode })
		if len(available) == 0 {
			return a.recordQualitySubscriptionCheck(ctx, j, s, "等待已播剧集及元数据", metadataDates)
		}
	}
	if state.Complete() {
		// Completion is not an ordinary save or a fabricated library match.
		// The synchronous source check, history and notification bridge own it.
		return a.recordQualitySubscriptionCheck(ctx, j, s, "已达到最高优先级，等待原订阅完成流程", metadataDates)
	}
	releaseDate := s.Media.ReleaseDate
	if provider {
		releaseDate = info.Media.ReleaseDate
	}
	if s.Media.MediaType == "movie" && releaseDate > time.Now().In(shanghai()).Format("2006-01-02") {
		return a.recordSubscriptionCheck(ctx, s, "电影尚未到上映日期，保持洗版订阅", metadataDates, time.Time{})
	}
	settings := a.Settings()
	request := SearchRequest{originalSubscription: &s, originalSearchInfo: info, Query: s.Keyword, MediaType: s.Media.MediaType, TMDBID: s.Media.ID, Season: s.Season, Episodes: available, Policy: s.Policy, SiteIDs: subscriptionSiteControls(settings.RSSSites, s.SiteIDs), Mappings: s.Mappings, AllowEarly: s.AllowEarly, EpisodeDates: dates}
	var result SearchOutput
	if priorErr == nil {
		// The existing acquisition owns this already selected batch. Searching
		// again would add provider effects whose results beginAcquisition then
		// discards, and could require a provider that selected Context replaced.
		request, result.Candidates = prior.Request, prior.Candidates
	} else {
		result, err = a.originalSubscriptionCandidates(ctx, request)
		if err != nil {
			return err
		}
		if len(result.Candidates) > 0 {
			result.Candidates, err = a.originalSubscriptionResourceSelection(ctx, s, request, result.Candidates)
			if err != nil {
				return err
			}
		}
	}
	created, err := a.runSubscriptionAcquisition(ctx, j, s, request, result.Candidates, state, available)
	if err != nil {
		return err
	}
	reason := "暂未找到更高优先级资源，继续洗版检查"
	if created > 0 {
		reason = fmt.Sprintf("下载器已实际接收 %d 个洗版下载", created)
	}
	return a.recordQualitySubscriptionCheck(ctx, j, s, reason, metadataDates)
}

func (a *App) recordQualitySubscriptionCheck(ctx context.Context, job Job, s Subscription, reason string, dates []string) error {
	if err := a.recordSubscriptionCheck(ctx, s, reason, dates, time.Time{}); err != nil {
		return err
	}
	return a.requestPausedQualityCompletion(ctx, job, s)
}

func (a *App) storeQualityProgress(ctx context.Context, old Subscription, state subscriptionquality.State, sourceMetadataUpdatedAt ...string) error {
	a.subMu.Lock()
	defer a.subMu.Unlock()
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var current Subscription
	if err = qualityRecord(ctx, tx, "subscription", old.ID, &current); err != nil {
		return err
	}
	if current.Revision != old.Revision || !a.subscriptionSearchState(ctx, current) {
		return bad("SUBSCRIPTION_CHANGED", "订阅在元数据更新期间已变化")
	}
	setSubscriptionQualityState(&current, state)
	if len(sourceMetadataUpdatedAt) > 0 && sourceMetadataUpdatedAt[0] != "" {
		current.LastCheck = sourceMetadataUpdatedAt[0]
	}
	if current.State == "active" && current.SourceState == "N" {
		current.SourceState = "R"
	}
	if err = txPut(ctx, tx, "subscription", current.ID, current); err != nil {
		return err
	}
	return tx.Commit()
}

var _ qualityReader = (*sql.Tx)(nil)
