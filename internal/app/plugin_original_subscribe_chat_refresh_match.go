package app

import (
	"context"
	"encoding/json"
	"slices"
	"strconv"
	"strings"

	"light-media/internal/decision"
	"light-media/internal/releasemeta"
	"light-media/internal/rsscontext"
	"light-media/internal/sites"
)

func (a *App) originalChatLatestContext(ctx context.Context, j Job) (context.Context, error) {
	if len(j.Payload) == 0 {
		return ctx, nil
	}
	var input originalChatLatestJob
	if err := json.Unmarshal(j.Payload, &input); err != nil {
		return ctx, err
	}
	if input.LatestBatch == "" {
		return ctx, nil
	}
	var batch originalChatLatestBatch
	if err := a.originalChatLatestRead(ctx, a.Store.DB, originalChatLatestBatchKind, input.LatestBatch, &batch); err != nil {
		return ctx, err
	}
	if batch.Mode != "spider" && batch.Mode != "rss" {
		return ctx, bad("SUBSCRIPTION_LATEST_INVALID", "最新资源快照类型无效")
	}
	if input.RSSFallback && !batch.ownsRSSRoot(j) {
		return ctx, bad("SUBSCRIPTION_LATEST_INVALID", "RSS 匹配任务与已保存批次不符")
	}
	batch.id = input.LatestBatch
	return context.WithValue(ctx, originalChatLatestKey{}, &batch), nil
}

func originalChatLatestFromContext(ctx context.Context) *originalChatLatestBatch {
	batch, _ := ctx.Value(originalChatLatestKey{}).(*originalChatLatestBatch)
	return batch
}

func (a *App) originalSubscriptionCandidates(ctx context.Context, request SearchRequest) (SearchOutput, error) {
	batch := originalChatLatestFromContext(ctx)
	if batch == nil {
		return a.search(ctx, request, true)
	}
	var err error
	ctx, err = a.ensureResourceRecognition(ctx)
	if err != nil {
		return SearchOutput{}, err
	}
	var target decision.Target
	if request.originalSearchInfo == nil && request.originalSubscription != nil {
		information, err := a.sourceRecognizeSubscriptionMedia(ctx, *request.originalSubscription)
		if err != nil {
			return SearchOutput{}, err
		}
		request.originalSearchInfo = &information
	}
	if request.originalSubscription != nil && providerNativeMedia(request.originalSubscription.Media) && request.originalSearchInfo != nil {
		target, err = providerSubscriptionSearchTarget(request, *request.originalSearchInfo)
	} else if request.originalSearchInfo != nil {
		target, err = a.targetMedia(ctx, request, true, request.originalSearchInfo.Media)
	} else {
		target, err = a.target(ctx, request, true)
	}
	if err != nil {
		return SearchOutput{}, err
	}
	if batch.Mode == "rss" && request.originalSubscription != nil && request.originalSubscription.BestVersion {
		// Full-quality coverage can include known episodes outside the requested
		// range. The source batch decides whether a whole candidate is allowed.
		target = qualityEvaluationTarget(target)
	}
	request.priorityMedia, request.priorityTarget, request.prioritySubscription = &target.Media, &target, true
	additional := snapshotAdditionalFilter(request.Policy.FilterParams)
	request.Policy.FilterParams = additional.params
	settings := a.Settings()
	priority, err := a.preparePriorityFilter(ctx, request, settings)
	if err != nil {
		return SearchOutput{}, err
	}
	sorter, err := a.preparePrioritySorter(ctx, request, settings)
	if err != nil {
		return SearchOutput{}, err
	}
	configured := map[string]SiteConfig{}
	for _, site := range settings.Sites {
		configured[site.ID] = site
	}
	out := SearchOutput{Candidates: []Candidate{}, Sites: []SiteStatus{}, CachedAt: now()}
	policy := decision.NativePolicy(decisionPolicy(request.Policy))
	var targetMedia originalChatLatestMedia
	if request.originalSearchInfo != nil {
		targetMedia = originalChatLatestMediaFields(request.originalSearchInfo.MediaInfo)
	}
	recognition, _ := ctx.Value(resourceRecognitionKey{}).(resourceRecognition)
	var parser *releasemeta.MetaInfoParser
	var recheckWords bool
	if request.originalSubscription != nil {
		words, err := a.subscriptionWords(ctx, request.originalSubscription.ID)
		if err != nil {
			return SearchOutput{}, err
		}
		recheckWords = words != ""
	}
	matchSeason := target.Media.MediaType == "tv"
	if recheckWords || matchSeason {
		parser, err = originalChatLatestParser(recognition.options)
		if err != nil {
			return SearchOutput{}, err
		}
	}
	for _, entry := range batch.Sites {
		if err := a.guardSubscriptionContext(ctx); err != nil {
			return SearchOutput{}, err
		}
		site := configured[entry.SiteID]
		if !site.Enabled || entry.Key != a.originalChatLatestSiteKey(site, settings.ProxyHost) ||
			len(request.SiteIDs) != 0 && !slices.Contains(request.SiteIDs, entry.SiteID) {
			continue
		}
		if s := request.originalSubscription; s != nil && len(s.SiteIDs) != 0 && !slices.Contains(s.SiteIDs, entry.SiteID) {
			continue
		}
		candidates := make([]Candidate, 0, len(entry.Rows))
		for _, row := range entry.Rows {
			if err := ctx.Err(); err != nil {
				return SearchOutput{}, err
			}
			if recheckWords && len(recognition.matcher.Prepare(row.MetaName).Applied) != 0 {
				a.originalChatLatestRecognize(ctx, &row, parser)
			}
			resource := row.resource()
			if !originalChatLatestMediaMatches(resource.ModuleMediaFields, targetMedia, target) {
				continue
			}
			if matchSeason {
				meta, err := originalChatPackMeta(parser, resource)
				if err != nil {
					continue
				}
				// SubscribeChain.match excludes a multi-season listing before
				// rule filters or material lookup, for either latest-resource mode.
				first, last := meta.Number("begin_season"), meta.Number("end_season")
				if first != nil && last != nil && last.Cmp(first) > 0 {
					continue
				}
			}
			choice := a.evaluateResourceContext(ctx, resource, target, policy)
			candidates = append(candidates, Candidate{Resource: resource, Accepted: choice.Accepted, NeedsConfirmation: choice.NeedsConfirmation,
				Reasons: choice.Reasons, Season: choice.Season, Episodes: choice.Episodes})
		}
		candidates, err = additional.apply(ctx, candidates)
		if err != nil {
			return SearchOutput{}, err
		}
		out.Candidates = append(out.Candidates, candidates...)
		out.Sites = append(out.Sites, a.searchStatus(ctx, entry.SiteID, len(candidates), nil, 0))
	}
	out, err = priority.apply(ctx, a, out)
	if err != nil {
		return SearchOutput{}, err
	}
	sorter.sort(a, out.Candidates, request.Policy)
	return out, nil
}

func originalChatLatestMediaMatches(raw json.RawMessage, expected originalChatLatestMedia, target decision.Target) bool {
	media := originalChatLatestMediaFields(raw)
	if rsscontext.ResolveIdentity(media).ID == "" {
		return true // Native decision still requires title and numbering evidence.
	}
	kind := map[string]string{"movie": "电影", "tv": "电视剧"}[target.Media.MediaType]
	if media.Get("type") != kind {
		return false
	}
	if providerNativeMedia(target.Media) {
		identity := rsscontext.ResolveIdentity(media)
		return identity.Source == target.Media.ProviderSource && identity.ID == target.Media.ProviderID
	}
	if candidateID := sourceStringValue(media.Get("tmdb_id")); candidateID != "" && candidateID != "0" && candidateID != strconv.FormatInt(target.Media.ID, 10) {
		return false
	}
	if candidateID := sourceStringValue(media.Get("douban_id")); candidateID != "" && candidateID != "0" && candidateID != sourceStringValue(expected.Get("douban_id")) {
		return false
	}
	return true
}

// The resource was obtained from a configured tracker and sealed with the
// refresh batch. Resolve only that row under the current connection snapshot.
// The ordinary preview still verifies the actual torrent and local destination.
type originalChatLatestDownloadProof struct {
	Mode    string                `json:"mode"`
	SiteKey string                `json:"site_key"`
	Row     originalChatLatestRow `json:"row"`
}

func (a *App) originalChatLatestSelect(batch *originalChatLatestBatch, site SiteConfig, resourceID string) (*originalChatLatestDownloadProof, error) {
	key := a.originalChatLatestSiteKey(site, a.Settings().ProxyHost)
	for _, entry := range batch.Sites {
		if entry.SiteID != site.ID || entry.Key != key {
			continue
		}
		for _, row := range entry.Rows {
			if row.Resource.SiteID == site.ID && row.Resource.ID == resourceID {
				return &originalChatLatestDownloadProof{Mode: batch.Mode, SiteKey: key, Row: row}, nil
			}
		}
	}
	return nil, bad("REVIEW_RESOURCE_CHANGED", "最新资源已变化，请重新刷新订阅")
}

func (a *App) originalChatLatestProofResource(proof *originalChatLatestDownloadProof, site SiteConfig, resourceID string) (sites.Resource, error) {
	if proof == nil || proof.Row.Resource.SiteID != site.ID || proof.Row.Resource.ID != resourceID || !site.Enabled ||
		(proof.Mode != "spider" && proof.Mode != "rss") || (proof.Mode == "rss") != strings.HasPrefix(resourceID, "rss_") ||
		proof.SiteKey != a.originalChatLatestSiteKey(site, a.Settings().ProxyHost) {
		return sites.Resource{}, bad("REVIEW_RESOURCE_CHANGED", "订阅最新资源或站点设置已变化，请重新刷新")
	}
	return proof.Row.resource(), nil
}

func (a *App) originalChatLatestIntentResource(ctx context.Context, intent Intent, proof *originalChatLatestDownloadProof) (sites.Resource, error) {
	if proof == nil || !intent.Auto || intent.SubscriptionID == "" {
		return sites.Resource{}, bad("REVIEW_RESOURCE_CHANGED", "最新资源下载凭据无法核对，请重新刷新")
	}
	var owner Subscription
	if err := a.Store.Get(ctx, "subscription", intent.SubscriptionID, &owner); err != nil {
		return sites.Resource{}, err
	}
	site, err := a.originalChatLatestIntentSite(owner, intent)
	if err != nil {
		return sites.Resource{}, err
	}
	return a.originalChatLatestProofResource(proof, site, intent.ResourceID)
}

func (a *App) originalChatLatestIntentSite(owner Subscription, intent Intent) (SiteConfig, error) {
	settings := a.Settings()
	selected := subscriptionSiteControls(settings.RSSSites, owner.SiteIDs)
	if owner.ID != intent.SubscriptionID || !originalChatLatestActive(owner) || owner.Revision != intent.SubscriptionRevision ||
		(len(selected) != 0 && !slices.Contains(selected, intent.SiteID)) ||
		(len(owner.SiteIDs) != 0 && !slices.Contains(owner.SiteIDs, intent.SiteID)) {
		return SiteConfig{}, bad("REVIEW_RESOURCE_CHANGED", "订阅最新资源站点范围已变化，请重新刷新")
	}
	for _, site := range settings.Sites {
		if site.ID == intent.SiteID && site.Enabled {
			return site, nil
		}
	}
	return SiteConfig{}, bad("REVIEW_RESOURCE_CHANGED", "订阅最新资源站点已变化，请重新刷新")
}

func (a *App) originalChatLatestTorrent(ctx context.Context, proof *originalChatLatestDownloadProof, site SiteConfig, client *sites.Client) ([]byte, error) {
	material, err := a.originalChatLatestMaterial(ctx, proof, site, client)
	if err != nil {
		return nil, err
	}
	if material.Magnet != "" {
		return nil, bad("REVIEW_MAGNET_FILES_REQUIRED", "磁力链接缺少可核对的种子文件")
	}
	return material.Torrent, nil
}

func (a *App) originalChatLatestMaterial(ctx context.Context, proof *originalChatLatestDownloadProof, site SiteConfig, client *sites.Client) (sites.SubscriptionRSSContent, error) {
	if proof == nil {
		return sites.SubscriptionRSSContent{}, bad("REVIEW_RESOURCE_CHANGED", "订阅最新资源缺少有效快照")
	}
	if _, err := a.originalChatLatestProofResource(proof, site, proof.Row.Resource.ID); err != nil {
		return sites.SubscriptionRSSContent{}, err
	}
	if proof.Mode == "rss" {
		material, err := a.originalHostReadSiteMaterial(ctx, site, proof.Row.Resource.ID)
		if err != nil {
			return sites.SubscriptionRSSContent{}, err
		}
		guard := func(ctx context.Context) error {
			settings := a.Settings()
			for _, current := range settings.Sites {
				if current.ID == site.ID && current.Enabled && proof.SiteKey == a.originalChatLatestSiteKey(current, settings.ProxyHost) {
					return ctx.Err()
				}
			}
			return bad("REVIEW_RESOURCE_CHANGED", "订阅RSS站点设置已变化，请重新刷新")
		}
		if strings.HasPrefix(material.URL, "magnet:") {
			if err := a.guardSubscriptionContext(ctx); err != nil {
				return sites.SubscriptionRSSContent{}, err
			}
			// A direct magnet makes no HTTP request and must still satisfy the
			// current site binding without consuming a site's request budget.
			if err := guard(ctx); err != nil {
				return sites.SubscriptionRSSContent{}, err
			}
		}
		client.AddRequestGuard(guard)
		data, invalidEnclosure, err := client.SubscriptionRSSMaterial(ctx, material.URL)
		if invalidEnclosure != "" {
			a.rememberOriginalChatRSSInvalidEnclosure(invalidEnclosure)
		}
		return data, err
	}
	torrent, err := client.Torrent(ctx, proof.Row.Resource.ID)
	return sites.SubscriptionRSSContent{Torrent: torrent}, err
}
