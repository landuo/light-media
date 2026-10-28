package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"light-media/internal/catalog"
	"light-media/internal/decision"
	"light-media/internal/download"
	"light-media/internal/media"
	"light-media/internal/metainfo"
	"light-media/internal/sites"
)

type SearchRequest struct {
	resourceBatch        *originalResourceBatchSearch
	mediaModuleSearch    *originalMediaResourceSearch
	originalSearchInfo   *sourceMediaInformation
	originalSubscription *Subscription
	priorityMedia        *catalog.Media
	priorityTarget       *decision.Target
	prioritySubscription bool
	VerifiedSeasonPack   bool                `json:"verified_season_pack,omitempty"`
	WorkflowID           string              `json:"workflow_id"`
	Query                string              `json:"query,omitempty"`
	MediaType            string              `json:"media_type"`
	TMDBID               int64               `json:"tmdb_id"`
	Season               int                 `json:"season"`
	Episodes             []int               `json:"episodes"`
	Policy               Policy              `json:"policy"`
	SiteIDs              []string            `json:"site_ids"`
	Page                 int                 `json:"page"`
	Mappings             []media.MappingRule `json:"mappings"`
	AllowEarly           []int               `json:"allow_early"`
	EpisodeDates         map[int]string      `json:"episode_dates"`
}
type PreviewRequest struct {
	sourceContext     json.RawMessage
	sourceSeasons     []SearchRequest
	sourceSeasonBasis json.RawMessage
	sourceMaterial    string
	sourceMeta        json.RawMessage
	selectedEpisodes  []int  // Native batch-only selection, never a public JSON claim.
	DirectoryID       string `json:"directory_id"`
	DownloadPath      string `json:"download_path"`
	SearchRequest
	qualitySelection    bool
	automaticSeasonPack bool
	SiteID              string `json:"site_id"`
	ResourceID          string `json:"resource_id"`
	DownloaderID        string `json:"downloader_id"`
	RootID              string `json:"root_id"`
	SubscriptionID      string `json:"subscription_id"`
	Revision            int    `json:"revision"`
}

func (a *App) site(key string) (SiteConfig, error) {
	return a.siteForRequest(context.Background(), key)
}

type manualSiteRequestKey struct{}

func allowDisabledSiteRequests(ctx context.Context) context.Context {
	return context.WithValue(ctx, manualSiteRequestKey{}, true)
}

func (a *App) siteForRequest(ctx context.Context, key string) (SiteConfig, error) {
	manual, _ := ctx.Value(manualSiteRequestKey{}).(bool)
	for _, s := range a.Settings().Sites {
		if s.ID == key && (s.Enabled || manual) {
			return s, nil
		}
	}
	return SiteConfig{}, bad("SITE_NOT_CONFIGURED", "站点尚未配置或未启用")
}
func (a *App) downloader(key string) (DownloaderConfig, error) {
	if key == "" {
		c := a.Settings()
		for _, d := range c.Downloaders {
			if d.ID == c.DefaultDownloader && downloaderEnabled(d) {
				return d, nil
			}
		}
		for _, d := range c.Downloaders {
			if downloaderEnabled(d) {
				return d, nil
			}
		}
	}
	d, err := a.configuredDownloader(key)
	if err != nil {
		return d, err
	}
	if !downloaderEnabled(d) {
		return d, bad("DOWNLOADER_DISABLED", "此下载器已停用，启用后继续处理")
	}
	return d, nil
}
func (a *App) configuredDownloader(key string) (DownloaderConfig, error) {
	c := a.Settings()
	if key == "" {
		key = c.DefaultDownloader
	}
	for _, d := range c.Downloaders {
		if d.ID == key {
			return d, nil
		}
	}
	return DownloaderConfig{}, bad("DOWNLOADER_NOT_CONFIGURED", "请选择已配置的下载器实例")
}
func (a *App) root(key, kind string) (Root, error) {
	c := a.Settings()
	if key == "" {
		key = c.DefaultRoot
	}
	for _, r := range c.Roots {
		if r.ID == key && (r.Kind == kind || r.Kind == "mixed") {
			return r, nil
		}
	}
	return Root{}, bad("ROOT_NOT_CONFIGURED", "请选择匹配类型的媒体目录")
}
func (a *App) withSite(ctx context.Context, s SiteConfig, auto bool, fn func(*sites.Client) error) error {
	if e := a.guardSubscriptionContext(ctx); e != nil {
		return e
	}
	val, _ := a.siteLocks.LoadOrStore(s.ID, make(chan struct{}, 1))
	lock := val.(chan struct{})
	select {
	case lock <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-lock }()
	if e := a.guardSubscriptionContext(ctx); e != nil {
		return e
	}
	if auto && a.paused() {
		return &APIError{"AUTOMATION_PAUSED", "自动化已暂停", 409}
	}
	var cooldown struct {
		Until int64 `json:"until"`
	}
	if a.Store.Get(ctx, "site_cooldown", s.ID, &cooldown) == nil && cooldown.Until > time.Now().Unix() {
		return bad("SITE_COOLDOWN", "站点因限流或认证异常正在冷却，请稍后重试")
	}
	proxySnapshot := a.Settings().ProxyHost
	siteConfig := s.Config
	siteConfig.MessageToken = s.Token
	client, e := sites.New(siteConfig)
	if e == nil && s.Proxy {
		proxy, proxyErr := resolveProxyConfig(proxySnapshot)
		if proxyErr != nil {
			return bad("SITE_PROXY", "全局代理配置无效")
		}
		client, e = sites.NewWithProxy(siteConfig, proxy)
	}
	if e != nil {
		return bad("SITE_CONFIG", e.Error())
	}
	defer client.CloseIdleConnections()
	client.SetRequestGuard(func(requestCtx context.Context) error {
		if e := a.guardSubscriptionContext(requestCtx); e != nil {
			return e
		}
		if auto && a.paused() {
			return &APIError{"AUTOMATION_PAUSED", "自动化已暂停", 409}
		}
		current, e := a.siteForRequest(requestCtx, s.ID)
		if e != nil {
			return e
		}
		if a.managedSiteKey(current) != a.managedSiteKey(s) {
			return bad("SITE_CHANGED", "站点设置已变化，请重新发起请求")
		}
		if s.Proxy && a.Settings().ProxyHost != proxySnapshot {
			return bad("SITE_CHANGED", "代理设置已变化，请重新发起请求")
		}
		if e := a.waitSiteRequestLimits(requestCtx, current); e != nil {
			return e
		}
		if e := a.guardSubscriptionContext(requestCtx); e != nil {
			return e
		}
		if auto && a.paused() {
			return &APIError{"AUTOMATION_PAUSED", "自动化已暂停", 409}
		}
		current, e = a.siteForRequest(requestCtx, s.ID)
		if e != nil {
			return e
		}
		if a.managedSiteKey(current) != a.managedSiteKey(s) {
			return bad("SITE_CHANGED", "站点设置已变化，请重新发起请求")
		}
		budget := current.DailyBudget
		if budget == 0 {
			budget = 600
		}
		limit := budget
		if auto {
			limit = budget * 8 / 10
		}
		tx, e := a.Store.DB.BeginTx(requestCtx, nil)
		if e != nil {
			return e
		}
		defer tx.Rollback()
		var used int
		if e = tx.QueryRowContext(requestCtx, `SELECT COUNT(*) FROM site_usage WHERE site_id=? AND ts>?`, s.ID, time.Now().Add(-24*time.Hour).Unix()).Scan(&used); e != nil {
			return e
		}
		if used >= limit {
			return bad("SITE_QUOTA", "站点近24小时调用预算已用尽，已保留手动额度")
		}
		if _, e = tx.ExecContext(requestCtx, `INSERT INTO site_usage VALUES(?,?)`, s.ID, time.Now().Unix()); e != nil {
			return e
		}
		if _, e = tx.ExecContext(requestCtx, `DELETE FROM site_usage WHERE ts<?`, time.Now().Add(-48*time.Hour).Unix()); e != nil {
			return e
		}
		return tx.Commit()
	})
	e = fn(client)
	if e != nil && (strings.Contains(e.Error(), "site_rate_limited") || strings.Contains(e.Error(), "site_authentication_failed") || strings.Contains(e.Error(), "site_forbidden_or_challenge")) {
		_ = a.Store.Put(context.Background(), "site_cooldown", s.ID, map[string]int64{"until": time.Now().Add(15 * time.Minute).Unix()})
	}
	return e
}

func (a *App) target(ctx context.Context, in SearchRequest, auto bool) (decision.Target, error) {
	m, e := a.media(ctx, in.MediaType, in.TMDBID)
	if e != nil {
		return decision.Target{}, e
	}
	return a.targetMedia(ctx, in, auto, m)
}

func (a *App) targetMedia(ctx context.Context, in SearchRequest, auto bool, m catalog.Media) (decision.Target, error) {
	t := decision.Target{Media: m, Season: in.Season, Episodes: in.Episodes, Mappings: in.Mappings, Auto: auto, AllowEarly: in.AllowEarly}
	if m.MediaType == "tv" {
		if in.Season < 1 {
			return t, bad("INVALID_SEASON", "请先选择标准季号")
		}
		season, e := a.season(ctx, m.ID, in.Season)
		if e != nil {
			return t, e
		}
		seasonStart := season.AirDate
		if seasonStart == "" {
			for _, episode := range season.Episodes {
				if episode.AirDate != "" && (seasonStart == "" || episode.AirDate < seasonStart) {
					seasonStart = episode.AirDate
				}
			}
		}
		if date, err := time.Parse("2006-01-02", seasonStart); err == nil {
			t.SeasonYear = date.Year()
		}
		today := time.Now().In(shanghai()).Format("2006-01-02")
		for _, ep := range season.Episodes {
			t.KnownEpisodes = append(t.KnownEpisodes, ep.EpisodeNumber)
			date := ep.AirDate
			if in.EpisodeDates[ep.EpisodeNumber] != "" {
				date = in.EpisodeDates[ep.EpisodeNumber]
			}
			if date != "" && date <= today {
				t.AiredEpisodes = append(t.AiredEpisodes, ep.EpisodeNumber)
			}
		}
		if len(t.Episodes) == 0 {
			t.Episodes = append([]int{}, t.KnownEpisodes...)
		}
		known := map[int]bool{}
		for _, n := range t.KnownEpisodes {
			known[n] = true
		}
		for _, n := range in.Episodes {
			if !known[n] {
				return t, bad("UNKNOWN_EPISODE", "选择的剧集尚未被元数据确认")
			}
		}
	}
	return t, nil
}
func decisionPolicy(p Policy) decision.Policy {
	return decision.Policy{FilterParams: p.FilterParams, Source: p.Source, HDR: p.HDR, Languages: p.Languages, Subtitles: p.Subtitles, PreferredGroups: p.PreferredGroups, PreferredResolutions: p.PreferredResolutions, MinSeeders: p.MinSeeders, FreeOnly: p.FreeOnly, Resolution: p.Resolution, Codec: p.Codec, MinBytes: p.MinBytes, MaxBytes: p.MaxBytes, Include: p.Include, Exclude: p.Exclude}
}
func (a *App) search(ctx context.Context, in SearchRequest, auto bool) (SearchOutput, error) {
	ctx, e := a.ensureResourceRecognition(ctx)
	if e != nil {
		return SearchOutput{}, e
	}
	in.Policy.FilterParams = snapshotAdditionalFilter(in.Policy.FilterParams).params
	moduleSearch, e := a.prepareOriginalMediaResourceSearch(ctx, in, auto)
	if e != nil {
		return SearchOutput{}, e
	}
	if moduleSearch != nil && moduleSearch.originalInformation != nil {
		defer func() { *moduleSearch.originalInformation = moduleSearch.information }()
	}
	var t decision.Target
	if moduleSearch == nil {
		t, e = a.target(ctx, in, auto)
	} else {
		t, e = a.targetMedia(ctx, in, auto, moduleSearch.information.Media)
		in.mediaModuleSearch = moduleSearch
		in.originalSearchInfo = &moduleSearch.information
		in.Page = 1 // Source media process uses the ordinary default page0.
	}
	if e != nil {
		return SearchOutput{}, e
	}
	in.priorityMedia = &t.Media
	in.priorityTarget = &t
	in.prioritySubscription = auto
	out := SearchOutput{Candidates: []Candidate{}, Sites: []SiteStatus{}, WorkflowID: a.workflow(ctx, in.WorkflowID, t.Media), CachedAt: now()}
	out, e = a.searchSites(ctx, in, out, func(siteCtx context.Context, s SiteConfig) siteSearchOutput {
		if moduleSearch != nil {
			return a.originalMediaResourceSiteOutput(siteCtx, s, in, t, out.WorkflowID, moduleSearch)
		}
		var result sites.SearchResult
		queries := map[string]string{}
		successfulQueries := 0
		start := time.Now()
		err := a.withSite(siteCtx, s, auto, func(client *sites.Client) error {
			seen := map[string]bool{}
			for _, query := range fallbackSearchQueries(t.Media, in.Query) {
				page, err := client.Search(siteCtx, query, in.Page)
				accepted := false
				for _, resource := range page.Resources {
					if !seen[resource.ID] {
						result.Resources = append(result.Resources, resource)
						queries[resource.ID] = query
						seen[resource.ID] = true
					}
					if a.evaluateResourceContext(siteCtx, resource, t, decision.NativePolicy(decisionPolicy(in.Policy))).Accepted {
						accepted = true
					}
				}
				if err != nil {
					return err
				}
				successfulQueries++
				if accepted {
					break
				}
			}
			return nil
		})
		status := a.searchStatus(siteCtx, s.ID, len(result.Resources), err, successfulQueries)
		found := siteSearchOutput{status: status, candidates: []Candidate{}}
		if err != nil {
			a.log("WARN", "sites", "search_failed", status.Message, out.WorkflowID, "")
		}
		if successfulQueries > 0 || len(result.Resources) > 0 {
			for _, r := range result.Resources {
				d := a.evaluateResourceContext(siteCtx, r, t, decision.NativePolicy(decisionPolicy(in.Policy)))
				found.candidates = append(found.candidates, Candidate{Resource: r, Accepted: d.Accepted, Reasons: d.Reasons, Season: d.Season, Episodes: d.Episodes, NeedsConfirmation: d.NeedsConfirmation})
			}
			if err := a.cacheSearchResources(ctx, result.Resources, queries, in.Page, mediaKey(t.Media)); err != nil && ctx.Err() == nil {
				a.log("WARN", "sites", "search_cache_failed", "搜索结果已返回，但预览检索记录保存失败", out.WorkflowID, "")
			}
			a.log("INFO", "sites", "search_completed", fmt.Sprintf("站点 %s 返回 %d 个资源，耗时 %dms", s.Name, len(result.Resources), time.Since(start).Milliseconds()), out.WorkflowID, "")
		}
		return found
	})
	if e != nil {
		return out, e
	}
	if moduleSearch != nil && len(out.Candidates) > 0 {
		moduleSearch.information.MediaInfo, e = originalClearMediaSearchInformation(moduleSearch.information.MediaInfo)
		if e != nil {
			return out, e
		}
	}
	a.saveDecision(ctx, out, t.Media)
	return out, nil
}
func (a *App) previewDownload(ctx context.Context, in PreviewRequest, auto bool) (DownloadPreview, error) {
	var e error
	if _, scoped := ctx.Value(resourceRecognitionKey{}).(resourceRecognition); !scoped && auto && in.SubscriptionID != "" {
		ctx, e = a.subscriptionRecognitionContext(ctx, in.SubscriptionID, in.Revision)
	} else {
		ctx, e = a.ensureResourceRecognition(ctx)
	}
	if e != nil {
		return DownloadPreview{}, e
	}
	in.VerifiedSeasonPack = false // Never accept a client assertion of material validation.
	if in.qualitySelection {
		var owner Subscription
		if !auto || in.SubscriptionID == "" {
			return DownloadPreview{}, bad("QUALITY_CONTEXT_MISSING", "洗版选集缺少实际订阅")
		}
		if e = a.Store.Get(ctx, "subscription", in.SubscriptionID, &owner); e != nil {
			return DownloadPreview{}, e
		}
		if !owner.BestVersion || owner.Revision != in.Revision || owner.State != "active" {
			return DownloadPreview{}, bad("QUALITY_CONTEXT_CHANGED", "洗版订阅已变化")
		}
	}
	var selectedContext *originalDownloadContext
	var t decision.Target
	if len(in.sourceContext) > 0 {
		selectedContext = &originalDownloadContext{}
		if e = json.Unmarshal(in.sourceContext, selectedContext); e != nil {
			return DownloadPreview{}, e
		}
		// batch_download already resolved this private, identity-checked source
		// Context. Reusing its media avoids a second provider call before the
		// actual material/path checks, including after durable batch recovery.
		info, err := a.originalRecognitionProjection(catalog.Media{ID: in.TMDBID, MediaType: in.MediaType}, selectedContext.Media)
		if err != nil {
			return DownloadPreview{}, err
		}
		t, e = a.targetMedia(ctx, in.SearchRequest, auto, info.Media)
	} else {
		t, e = a.target(ctx, in.SearchRequest, auto)
	}
	if e != nil {
		return DownloadPreview{}, e
	}
	if t.Media.MediaType == "tv" && len(in.Episodes) == 0 {
		return DownloadPreview{}, bad("REVIEW_EPISODES", "请选择此资源的目标集数；整季下载也须先明确确认本季集数")
	}
	in.Episodes = append([]int{}, t.Episodes...)
	if len(in.selectedEpisodes) > 0 {
		if t.Media.MediaType != "tv" {
			return DownloadPreview{}, bad("INVALID_EPISODE_SELECTION", "仅电视剧支持按集选择种子文件")
		}
		for _, episode := range in.selectedEpisodes {
			if !containsInt(t.Episodes, episode) {
				return DownloadPreview{}, bad("INVALID_EPISODE_SELECTION", "种子文件选择超出目标集数范围")
			}
		}
	}
	site, e := a.site(in.SiteID)
	if e != nil {
		return DownloadPreview{}, e
	}
	d, sourceChoice, e := a.originalDownloadSelection(in.DownloaderID, site)
	if e != nil {
		return DownloadPreview{}, e
	}
	destination, e := a.resolveDownloadDestinationContext(ctx, d, in.DirectoryID, in.DownloadPath, in.RootID, t.Media.MediaType)
	if e != nil {
		return DownloadPreview{}, e
	}
	var fresh sites.Resource
	var torrent []byte
	if in.Query == "" {
		var prior struct {
			Query    string `json:"query"`
			Page     int    `json:"page"`
			MediaKey string `json:"media_key"`
		}
		if a.Store.Get(ctx, "resource", in.SiteID+":"+in.ResourceID, &prior) == nil && prior.MediaKey == mediaKey(t.Media) {
			in.Query = prior.Query
			in.Page = prior.Page
		}
	}
	if in.Query == "" {
		in.Query = t.Media.Title
	}
	e = a.withSite(ctx, site, auto, func(client *sites.Client) error {
		resources, err := client.Search(ctx, in.Query, in.Page)
		for _, r := range resources.Resources {
			if r.ID == in.ResourceID {
				fresh = r
				break
			}
		}
		if fresh.ID == "" {
			if err != nil {
				return err
			}
			return bad("RESOURCE_CHANGED", "当前搜索未能重新确认该资源，请刷新结果")
		}
		if err != nil {
			a.log("WARN", "sites", "partial_preview", "同页其他资源解析不完整；当前所选资源已逐项重新确认", in.WorkflowID, "")
		}
		var err2 error
		if in.sourceMaterial != "" {
			torrent, err2 = a.resolveMaterial(ctx, in.sourceMaterial, materialPurpose)
		} else {
			torrent, err2 = client.Torrent(ctx, in.ResourceID)
		}
		return err2
	})
	if e != nil {
		return DownloadPreview{}, e
	}
	evaluationTarget := t
	if in.qualitySelection {
		evaluationTarget = qualityEvaluationTarget(t)
	}
	choice := a.evaluateResourceContext(ctx, fresh, evaluationTarget, decisionPolicy(in.Policy))
	verifiedPack := auto && in.automaticSeasonPack && a.eligibleAutomaticSeasonPackContext(ctx, fresh, evaluationTarget, in.Policy)
	if !choice.Accepted && !verifiedPack {
		return DownloadPreview{}, bad("RESOURCE_REJECTED", strings.Join(choice.Reasons, "；"))
	}
	material, e := metainfo.ParseMaterial(torrent)
	if e != nil {
		return DownloadPreview{}, bad("INVALID_TORRENT", e.Error())
	}
	if e = actualSizePolicy(material.TotalBytes, in.Policy); e != nil {
		return DownloadPreview{}, e
	}
	if t.Media.MediaType == "tv" && len(in.Episodes) == 0 {
		return DownloadPreview{}, bad("REVIEW_EPISODES", "请先选择明确的目标集，再确认资源覆盖")
	}
	if t.Media.MediaType == "movie" && t.Media.ReleaseDate != "" && t.Media.ReleaseDate > time.Now().In(shanghai()).Format("2006-01-02") {
		return DownloadPreview{}, bad("REVIEW_UNRELEASED", "该电影尚未到元数据上映日期，请核实后再获取")
	}
	p := DownloadPreview{WorkflowID: a.workflow(ctx, in.WorkflowID, t.Media), Request: in.SearchRequest, ID: id("preview_"), Media: t.Media, Season: in.Season, Episodes: in.Episodes, SiteID: site.ID, ResourceID: in.ResourceID, ResourceTitle: fresh.Title, DownloaderID: d.ID, RootID: destination.Root, DirectoryID: destination.Rule.ID, DownloadPath: destination.Path, DirectorySnapshot: destination.Snapshot, Hash: material.Hash, Files: []string{}, TotalBytes: material.TotalBytes, CreatedAt: now(), ExpiresAt: time.Now().Add(10 * time.Minute).UTC().Format(time.RFC3339Nano), Policy: in.Policy, SubscriptionID: in.SubscriptionID, Revision: in.Revision, Auto: auto, Warnings: choice.Reasons, NeedsConfirmation: choice.NeedsConfirmation}
	p.DownloaderSnapshot = downloaderSnapshot(d)
	p.SourceDownloader = sourceChoice
	p.SelectedEpisodes = append([]int(nil), in.selectedEpisodes...)
	seasonProof, e := a.prepareDownloadSeasonProof(ctx, &p, in.sourceSeasons, material, fresh, in.sourceSeasonBasis)
	if e != nil {
		return p, e
	}
	if seasonProof != nil {
		in.Season, in.Episodes = p.Season, qualityCopyNote(p.Episodes)
		evaluationTarget.Season, evaluationTarget.Episodes = p.Season, qualityCopyNote(p.Episodes)
	}
	coverage := map[int]bool{}
	p.ImportMode = destination.Mode
	if p.ImportMode == "" {
		p.ImportMode = "hardlink"
	}
	videos := 0
	for _, f := range material.Files {
		if !isVideo(f.Path) {
			continue
		}
		videos++
		p.Files = append(p.Files, f.Path)
		fileResource := fresh
		fileResource.Title = filepath.Base(f.Path)
		fileResource.Subtitle = ""
		fileResource.Size = f.Size
		fileTarget := evaluationTarget
		if verifiedPack && !in.qualitySelection {
			fileTarget.AllowEarly = nil
		}
		fileChoice := a.evaluateResourceContext(ctx, fileResource, fileTarget, decision.Policy{})
		if !fileChoice.Accepted || fileChoice.NeedsConfirmation {
			if auto {
				return p, bad("REVIEW_FILE_IDENTITY", "种子实际视频文件名不能确认目标身份，请手动核对")
			}
			p.Warnings = append(p.Warnings, "需要核对实际文件身份："+filepath.Base(f.Path))
			p.NeedsConfirmation = true
		}
		if t.Media.MediaType == "tv" {
			parsed := a.parseMediaNameContext(ctx, filepath.Base(f.Path))
			if parsed.Status != media.Parsed || parsed.Original == nil {
				p.ExtraFiles = true
				continue
			}
			known := []media.Episode{}
			for _, ep := range t.KnownEpisodes {
				known = append(known, media.Episode{Season: in.Season, Episode: ep})
			}
			resolved := media.Resolve(mediaKey(t.Media), *parsed.Original, media.Source{Site: site.ID}, in.Mappings, known, nil)
			if resolved.Status != media.Parsed {
				p.ExtraFiles = true
				continue
			}
			for _, ep := range resolved.Standard {
				if ep.Season != in.Season || !containsInt(in.Episodes, ep.Episode) {
					p.ExtraFiles = true
				} else {
					if (verifiedPack || in.qualitySelection) && coverage[ep.Episode] {
						return p, bad("REVIEW_SEASON_PACK", "整季包同一集包含多个视频版本，请人工核对")
					}
					coverage[ep.Episode] = true
				}
			}
		}
	}
	if videos == 0 {
		return p, bad("NO_MEDIA_FILES", "种子未包含支持的视频文件")
	}
	if verifiedPack && !in.qualitySelection {
		if p.ExtraFiles || len(coverage) == 0 {
			return p, bad("REVIEW_SEASON_PACK", "整季包包含目标缺集之外或无法确认的文件，请人工核对")
		}
		// Every actual video already passed the normal strict automatic identity,
		// season, known/aired and target checks above. Freeze exactly that subset.
		p.Episodes = []int{}
		for ep := range coverage {
			p.Episodes = append(p.Episodes, ep)
		}
		sort.Ints(p.Episodes)
		in.Episodes = append([]int{}, p.Episodes...)
		p.Request.Episodes = append([]int{}, p.Episodes...)
		p.Request.VerifiedSeasonPack = true
		p.Warnings, p.NeedsConfirmation = []string{}, false
	}
	if t.Media.MediaType == "tv" {
		for _, ep := range in.Episodes {
			if !coverage[ep] {
				return p, bad("REVIEW_TORRENT_COVERAGE", "种子文件无法证明覆盖所选目标集，请修正集号映射或更换资源")
			}
		}
	}
	if videos > 1 && t.Media.MediaType == "movie" {
		p.ExtraFiles = true
	}
	if in.qualitySelection && t.Media.MediaType == "tv" {
		p.SelectedEpisodes = qualityCopyNote(p.Episodes)
		if grant, ok := ctx.Value(subscriptionAcquisitionKey{}).(*subscriptionAcquisitionGrant); ok && grant != nil {
			if grant.a != a || grant.ordinal < 0 || grant.ordinal >= len(grant.root.Steps) {
				return p, acquisitionEvidence()
			}
			p.SelectedEpisodes = qualityCopyNote(grant.root.Steps[grant.ordinal].Input.Selected)
		}
		p.Warnings, p.NeedsConfirmation = []string{}, false
	}
	if p.ExtraFiles && !in.qualitySelection {
		p.Warnings = append(p.Warnings, "此资源还包含额外视频，确认后仅整理所选目标")
		p.NeedsConfirmation = true
	}
	if auto && p.NeedsConfirmation {
		return p, bad("REVIEW_RESOURCE", "自动获取需人工确认该资源包")
	}
	qualitySnapshot, e := a.prepareQualityPreview(ctx, p, fresh)
	if e != nil {
		return p, e
	}
	p.MaterialRef, e = a.persistMaterial(ctx, torrent, p.Hash)
	if e != nil {
		return p, e
	}
	p.MaterialState = "archived"
	sourceContext, e := a.prepareDownloadContext(ctx, "download_preview", p.ID, p.Hash, fresh)
	if e != nil {
		return p, e
	}
	if len(in.sourceMeta) > 0 {
		if !json.Valid(in.sourceMeta) {
			return p, bad("INVALID_SOURCE_METADATA", "原资源识别上下文无法核对")
		}
		sourceContext.SourceMeta = append(json.RawMessage(nil), in.sourceMeta...)
	}
	if selectedContext != nil {
		sourceContext.Original = selectedContext
	} else {
		contextResource, originalMedia, contextErr := a.originalCachedDownloadInputs(ctx, fresh, p.Media)
		if contextErr != nil {
			return p, contextErr
		}
		if in.originalSearchInfo != nil {
			originalMedia = in.originalSearchInfo.MediaInfo
		}
		if e = a.completeOriginalDownloadContext(ctx, &sourceContext, Candidate{Resource: contextResource}, p.Media, originalMedia); e != nil {
			return p, e
		}
	}
	previewTx, e := a.Store.DB.BeginTx(ctx, nil)
	if e != nil {
		return p, e
	}
	defer previewTx.Rollback()
	if e = txPut(ctx, previewTx, "download_preview", p.ID, p); e != nil {
		return p, e
	}
	if e = a.saveDownloadContextTx(ctx, previewTx, sourceContext); e != nil {
		return p, e
	}
	if seasonProof != nil {
		if e = a.saveDownloadSeasonProofTx(ctx, previewTx, *seasonProof); e != nil {
			return p, e
		}
	}
	if e = a.saveQualitySnapshotTx(ctx, previewTx, qualitySnapshot); e != nil {
		return p, e
	}
	if e = previewTx.Commit(); e != nil {
		return p, e
	}
	a.materialMu.Lock()
	if len(torrent) <= 16<<20 {
		total := 0
		for _, b := range a.materials {
			total += len(b)
		}
		for k, b := range a.materials {
			if total+len(torrent) <= 16<<20 && len(a.materials) < 8 {
				break
			}
			total -= len(b)
			delete(a.materials, k)
		}
		a.materials[p.ID] = torrent
	}
	a.materialMu.Unlock()
	return p, nil
}
func (a *App) confirmDownload(ctx context.Context, key string, acceptExtra bool) (confirmed Intent, resultErr error) {
	release, e := a.beginWrite()
	if e != nil {
		return Intent{}, e
	}
	defer release()
	var p DownloadPreview
	if e = a.Store.Get(ctx, "download_preview", key, &p); e != nil {
		return Intent{}, e
	}
	expires, e := time.Parse(time.RFC3339Nano, p.ExpiresAt)
	if e != nil || time.Now().After(expires) {
		return Intent{}, bad("PREVIEW_EXPIRED", "下载预览已过期，请重新预览")
	}
	if p.NeedsConfirmation && !acceptExtra {
		return Intent{}, bad("CONFIRM_REQUIRED", "请确认预览中的额外内容和复核提示")
	}
	if p.SubscriptionID != "" {
		var s Subscription
		if e = a.Store.Get(ctx, "subscription", p.SubscriptionID, &s); e != nil {
			return Intent{}, e
		}
		if s.State != "active" || s.Revision != p.Revision {
			return Intent{}, bad("SUBSCRIPTION_CHANGED", "订阅已修改或暂停，请重新检查")
		}
		for _, ep := range p.Episodes {
			if containsInt(s.Excluded, ep) {
				return Intent{}, bad("EXCLUDED_EPISODE", "目标集已被排除")
			}
		}
	}
	if e = a.ensureNotExcluded(ctx, p.Media, p.Season, p.Episodes); e != nil {
		return Intent{}, e
	}
	intent := Intent{Request: p.Request, ID: id("dl_"), WorkflowID: p.WorkflowID, Media: p.Media, Season: p.Season, Episodes: p.Episodes, SiteID: p.SiteID, ResourceID: p.ResourceID, ResourceTitle: p.ResourceTitle, DownloaderID: p.DownloaderID, RootID: p.RootID, Hash: p.Hash, State: "planned", SubscriptionID: p.SubscriptionID, SubscriptionRevision: p.Revision, Auto: p.Auto, CreatedAt: now(), UpdatedAt: now(), PreviewID: p.ID}
	intent.SeasonScopeSHA = p.SeasonScopeSHA
	intent.ImportMode = p.ImportMode
	intent.SourcePostAddVersion = 1
	intent.SourceDownloader = append(json.RawMessage(nil), p.SourceDownloader...)
	intent.SelectedEpisodes = append([]int(nil), p.SelectedEpisodes...)
	intent.DirectoryID, intent.DownloadPath, intent.DirectorySnapshot = p.DirectoryID, p.DownloadPath, p.DirectorySnapshot
	intent.DownloaderSnapshot = p.DownloaderSnapshot
	intent.MaterialRef, intent.MaterialState, intent.Purpose = p.MaterialRef, p.MaterialState, materialPurpose
	jobKind := "download"
	if grant, ok := ctx.Value(subscriptionAcquisitionKey{}).(*subscriptionAcquisitionGrant); ok {
		if grant == nil || grant.a != a || grant.root == nil || intent.LarkInbox != "" {
			return intent, acquisitionEvidence()
		}
		intent.SubscriptionAcquisition, jobKind = grant.root.ID, "subscription_download"
	}
	sourceContext, e := a.loadDownloadContext(ctx, "download_preview", p.ID, p.Hash)
	if e != nil {
		return intent, e
	}
	tx, e := a.Store.DB.BeginTx(ctx, nil)
	if e != nil {
		return intent, e
	}
	defer tx.Rollback()
	if p.SubscriptionID != "" {
		// A plugin fill or user edit can commit after the preliminary read but
		// before this transaction. Never create an obsolete selected intent or
		// leave its reservation outside the subscription revision boundary.
		var raw []byte
		if e = tx.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind='subscription' AND id=?`, p.SubscriptionID).Scan(&raw); e != nil {
			return intent, e
		}
		var current Subscription
		if json.Unmarshal(raw, &current) != nil || current.ID != p.SubscriptionID || current.State != "active" || current.Revision != p.Revision {
			return intent, bad("SUBSCRIPTION_CHANGED", "订阅已修改或暂停，请重新检查")
		}
	}
	if e = pinMaterialTx(ctx, tx, p.MaterialRef, p.Hash); e != nil {
		return intent, e
	}
	if e = a.reserveQualityDownloadTx(ctx, tx, p, intent); e != nil {
		return intent, bad("ALREADY_ACQUIRED", e.Error())
	}
	if e = txPut(ctx, tx, "intent", intent.ID, intent); e != nil {
		return intent, e
	}
	if sourceContext != nil {
		sourceContext.DownloadKind, sourceContext.DownloadID = "intent", intent.ID
		if e = a.saveDownloadContextTx(ctx, tx, *sourceContext); e != nil {
			return intent, e
		}
	}
	child, e := enqueueTx(ctx, tx, jobKind, map[string]string{"id": intent.ID}, intent.WorkflowID)
	if e != nil {
		return intent, e
	}
	if e = a.acquisitionConfirmTx(ctx, tx, p, intent, child); e != nil {
		return intent, e
	}
	return intent, tx.Commit()
}
func (a *App) runDownload(ctx context.Context, j Job, key string) error {
	a.downloadMu.Lock()
	defer a.downloadMu.Unlock()
	var in Intent
	if e := a.Store.Get(ctx, "intent", key, &in); e != nil {
		return e
	}
	if e := a.guardAcquisitionChild(ctx, j, in); e != nil {
		return e
	}
	if j.Kind == "lark_download" || in.LarkInbox != "" {
		return bad("PLUGIN_ADAPTER_UNAVAILABLE", "旧插件下载任务尚未适配标准模块")
	}
	if in.State == "accepted" || in.State == "downloading" || in.State == "downloaded" || in.State == "imported" || in.State == "cancelled" {
		// Acceptance and its event commit with the intent. A retried submission
		// job must not recheck a later subscription revision or replay the event.
		if in.State == "cancelled" {
			return nil
		}
		return a.guardSourceEpisodeReceipt(ctx, in)
	}
	if in.SourceModuleStarted {
		return bad("REVIEW_PLUGIN_DOWNLOAD_UNCONFIRMED", "下载插件已调用，结果尚未提交，请核对原任务后重新添加")
	}
	if in.SourcePostAddVersion == 1 && in.State == "post_add" {
		return a.resumeSourcePostAdd(ctx, in, j)
	}
	if len(in.SelectedEpisodes) > 0 && in.State != "planned" {
		// A prior Add/selection may have crossed an external write boundary.
		// Reading that hash back is not evidence that file priorities and Start
		// completed. Preserve the original receipt instead of silently skipping
		// or repeating its post-Add operations after a cold restart.
		return bad("REVIEW_FILE_SELECTION_UNCONFIRMED", "按集下载的添加或文件选择结果尚未确认，请核对原任务记录")
	}
	if in.SourcePostAddVersion != 0 && (in.SourcePostAddVersion != 1 || in.State != "planned") {
		return bad("REVIEW_POST_ADD_UNCONFIRMED", "下载后的原操作结果尚未确认，请核对原任务记录")
	}
	ctx, recognitionErr := a.intentRecognitionContext(ctx, in)
	if recognitionErr != nil {
		return recognitionErr
	}
	if in.SubscriptionID != "" {
		ctx = subscriptionContext(ctx, in.SubscriptionID, in.SubscriptionRevision)
	}
	if e := a.guardSubscriptionContext(ctx); e != nil {
		return e
	}
	if e := a.jobCancelled(ctx, j); e != nil {
		return e
	}
	if in.SubscriptionID != "" {
		var s Subscription
		if e := a.Store.Get(ctx, "subscription", in.SubscriptionID, &s); e != nil {
			return e
		}
		if s.State != "active" || s.Revision != in.SubscriptionRevision {
			return bad("REVIEW_SUBSCRIPTION_CHANGED", "订阅状态或规则变化，请人工核对未提交的下载意图")
		}
	}
	if e := a.guardIntentSeasonExclusions(ctx, in); e != nil {
		return e
	}
	d, e := a.downloader(in.DownloaderID)
	if e != nil {
		return e
	}
	if e = a.guardDownloaderSnapshot(ctx, in); e != nil {
		return e
	}
	if in.DownloaderSnapshot != downloaderSnapshot(d) {
		return bad("REVIEW_DOWNLOADER_CHANGED", "下载器配置已变化，请重新核对预览")
	}
	if e = a.guardIntentDirectory(ctx, in, false); e != nil {
		return e
	}
	client, e := download.New(d.Config)
	if e != nil {
		return e
	}
	qualityIntentProof, e := a.qualitySnapshot(ctx, a.Store.DB, "intent", in.ID, in.Hash)
	if e != nil {
		return e
	}
	if qualityIntentProof == nil && in.SubscriptionID != "" {
		var owner Subscription
		if e = a.Store.Get(ctx, "subscription", in.SubscriptionID, &owner); e != nil {
			return e
		}
		if owner.BestVersion {
			return bad("QUALITY_SNAPSHOT_CHANGED", "洗版下载缺少原始授权快照")
		}
	}
	tasks, e := client.List(ctx)
	if e != nil {
		return jobErr("DOWNLOADER_UNAVAILABLE", e)
	}
	for _, task := range tasks {
		if sameHash(task.Hash, in.Hash) && (qualityIntentProof == nil || !qualityIntentProof.BestVersion) {
			in.Hash = task.Hash
			in.State = "accepted"
			in.UpdatedAt = now()
			a.event(ctx, j, "INFO", "downloads", "reconciled", "下载器已有对应种子，已关联而未重复添加")
			return a.acceptDownload(withSourcePostAddExistingHash(ctx, in), in, j)
		}
	}
	var torrent []byte
	a.materialMu.Lock()
	torrent = a.materials[in.PreviewID]
	a.materialMu.Unlock()
	if torrent == nil && in.MaterialRef != "" {
		torrent, e = a.resolveMaterial(ctx, in.MaterialRef, materialPurpose)
		if e != nil {
			return e
		}
	}
	var preview DownloadPreview
	if e = a.Store.Get(ctx, "download_preview", in.PreviewID, &preview); e != nil {
		return e
	}
	request := in.Request
	if request.MediaType == "" {
		request = preview.Request
	}
	request.MediaType = in.Media.MediaType
	request.TMDBID = in.Media.ID
	request.Season = in.Season
	request.Episodes = in.Episodes
	if in.Auto && in.Media.MediaType == "tv" {
		// The check's effective dates were a snapshot. Reapply the unchanged
		// subscription's explicit overrides to current provider data before Add.
		var current Subscription
		if in.SubscriptionID == "" {
			return bad("REVIEW_EPISODE_DATE", "自动剧集下载缺少订阅来源，请人工核对")
		}
		if e = a.Store.Get(ctx, "subscription", in.SubscriptionID, &current); e != nil {
			return e
		}
		season, err := a.season(ctx, in.Media.ID, in.Season)
		if err != nil {
			return err
		}
		request.EpisodeDates = map[int]string{}
		for _, episode := range season.Episodes {
			date, skip := effectiveEpisodeDate(current, episode.EpisodeNumber, episode.AirDate)
			if skip && containsInt(in.Episodes, episode.EpisodeNumber) {
				return bad("REVIEW_EPISODE_DATE", "剧集目标已被更新例外跳过，请人工核对")
			}
			request.EpisodeDates[episode.EpisodeNumber] = date
		}
	}
	frozen, e := a.loadDownloadContext(ctx, "intent", in.ID, in.Hash)
	if e != nil {
		return e
	}
	var target decision.Target
	if frozen != nil && frozen.Original != nil && len(frozen.Original.Media) > 0 && string(frozen.Original.Media) != "null" {
		information, err := a.originalRecognitionProjection(in.Media, frozen.Original.Media)
		if err != nil {
			return err
		}
		target, e = a.targetMedia(ctx, request, in.Auto, information.Media)
	} else {
		target, e = a.target(ctx, request, in.Auto)
	}
	if e != nil {
		return e
	}
	site, e := a.site(in.SiteID)
	if e != nil {
		return e
	}
	qualityProof, e := a.qualitySnapshot(ctx, a.Store.DB, "intent", in.ID, in.Hash)
	if e != nil {
		return e
	}
	if qualityProof != nil && qualityProof.BestVersion {
		target = qualityEvaluationTarget(target)
	}
	var sourceResource sites.Resource
	e = a.withSite(ctx, site, in.Auto, func(c *sites.Client) error {
		query := request.Query
		if query == "" {
			query = in.Media.Title
		}
		result, err := c.Search(ctx, query, request.Page)
		var fresh *sites.Resource
		for i := range result.Resources {
			if result.Resources[i].ID == in.ResourceID {
				fresh = &result.Resources[i]
				break
			}
		}
		if fresh == nil {
			if err != nil {
				return err
			}
			return bad("REVIEW_RESOURCE_CHANGED", "提交前未能重新确认资源，请重新预览")
		}
		sourceResource = *fresh
		if err != nil {
			a.log("WARN", "sites", "partial_recheck", "同页其他资源解析不完整；当前资源已独立重新确认", in.WorkflowID, j.ID)
		}
		err = nil
		choice := a.evaluateResourceContext(ctx, *fresh, target, decisionPolicy(preview.Policy))
		if in.Auto && (request.VerifiedSeasonPack || qualityProof != nil && qualityProof.BestVersion) && a.eligibleAutomaticSeasonPackContext(ctx, *fresh, target, preview.Policy) {
			choice.Accepted, choice.NeedsConfirmation, choice.Reasons = true, false, []string{}
		}
		newReason := false
		for _, reason := range choice.Reasons {
			found := false
			for _, old := range preview.Warnings {
				if old == reason {
					found = true
					break
				}
			}
			if !found {
				newReason = true
			}
		}
		if !choice.Accepted || (choice.NeedsConfirmation && (!preview.NeedsConfirmation || newReason)) {
			return bad("REVIEW_POLICY_CHANGED", "资源促销或筛选条件已变化，请重新预览确认")
		}
		if torrent == nil {
			torrent, err = c.Torrent(ctx, in.ResourceID)
		}
		return err
	})
	if e != nil {
		return e
	}
	if qualityProof != nil && qualityProof.BestVersion {
		var owner Subscription
		if e = a.Store.Get(ctx, "subscription", qualityProof.SubscriptionID, &owner); e != nil {
			return e
		}
		owner.Policy = qualityProof.Policy
		priority, err := a.subscriptionResourcePriority(ctx, owner, sourceResource)
		if err != nil {
			return err
		}
		if priority != qualityProof.Resource.Priority {
			return bad("QUALITY_PRIORITY_CHANGED", "实际资源优先级在确认后已变化")
		}
	}
	material, e := metainfo.ParseMaterial(torrent)
	if e != nil || !sameHash(material.Hash, in.Hash) {
		return bad("REVIEW_MATERIAL_CHANGED", "下载材料与确认预览不一致")
	}
	if e = a.guardSeasonMaterial(ctx, in, material); e != nil {
		return e
	}
	if e = actualSizePolicy(material.TotalBytes, preview.Policy); e != nil {
		return e
	}
	if in.Auto && (request.VerifiedSeasonPack || qualityProof != nil && qualityProof.BestVersion && in.Media.MediaType == "tv") {
		if qualityProof != nil && qualityProof.BestVersion {
			target.Episodes = in.Episodes
		}
		if e = a.validateQualityOrAutomaticFiles(ctx, qualityProof, material, target, in.SiteID); e != nil {
			return e
		}
	}
	if e = a.jobCancelled(ctx, j); e != nil {
		return e
	}
	if e = a.guardSubscriptionContext(ctx); e != nil {
		return e
	}
	if e = a.guardIntentSeasonExclusions(ctx, in); e != nil {
		return e
	}
	a.importMu.Lock()
	defer a.importMu.Unlock()
	if e = a.guardAcquisitionSlots(ctx, in); e != nil {
		return e
	}
	if e = a.guardDownloaderSnapshot(ctx, in); e != nil {
		return e
	}
	sourceContext, e := a.prepareDownloadContext(ctx, "intent", in.ID, in.Hash, sourceResource)
	if e != nil {
		return e
	}
	if frozen != nil {
		sourceContext.SourceMeta = append(json.RawMessage(nil), frozen.SourceMeta...)
		sourceContext.Original = frozen.Original
	}
	if sourceContext.Original == nil {
		if e = a.completeOriginalDownloadContext(ctx, &sourceContext, Candidate{Resource: sourceResource}, in.Media, nil); e != nil {
			return e
		}
	}
	origin := "Manual"
	if in.SubscriptionAcquisition != "" {
		acquisition, err := a.readAcquisition(ctx, a.Store.DB, in.SubscriptionAcquisition)
		if err != nil {
			return err
		}
		origin = acquisition.Source
	} else if in.SubscriptionID != "" {
		var subscription Subscription
		if e = a.Store.Get(ctx, "subscription", in.SubscriptionID, &subscription); e != nil {
			return e
		}
		raw, err := a.subscriptionSourceProjection(ctx, subscription)
		if err != nil {
			return err
		}
		origin, e = subscriptionAcquisitionSource(raw)
		if e != nil {
			return e
		}
	}
	var eventSelection []int
	if len(in.SelectedEpisodes) > 0 {
		eventSelection = in.SelectedEpisodes
	}
	in.DownloadPath, e = a.originalResourceDownload(ctx, &sourceContext, effectiveIntentDownloadPath(in, d), eventSelection, origin, in.SourceDownloader, client)
	if e != nil {
		return e
	}
	if e = a.guardIntentDirectory(ctx, in, false); e != nil {
		return e
	}
	release, e := a.beginWrite()
	if e != nil {
		return e
	}
	defer release()
	if in.LarkInbox != "" {
		return bad("PLUGIN_ADAPTER_UNAVAILABLE", "旧插件下载任务尚未适配标准模块")
	}
	in.State = "submitting"
	in.UpdatedAt = now()
	sourceContext.OriginalDownloadDir, sourceContext.OriginalMaterialRef = effectiveIntentDownloadPath(in, d), in.MaterialRef
	if e = a.stageIntentDownload(ctx, in, sourceContext); e != nil {
		return e
	}
	if e := a.guardAcquisitionChild(ctx, j, in); e != nil {
		return e
	}
	requestAdd := download.AddRequest{Torrent: torrent, SavePath: effectiveIntentDownloadPath(in, d), Paused: len(in.SelectedEpisodes) > 0, Tags: []string{"light-media"}}
	var episodes any
	if len(in.SelectedEpisodes) > 0 {
		episodes = in.SelectedEpisodes
	}
	var postAddErr error
	category, categorySet := originalDownloadContextCategory(&sourceContext)
	module, e := a.originalDownload(ctx, d, requestAdd, originalDownloadOptions{Category: category, CategorySet: categorySet, Cookie: site.Cookie, Episodes: episodes, Downloader: in.SourceDownloader, Media: &in.Media, BeforeInvoke: func() error {
		in.SourceModuleStarted = true
		return a.Store.Put(ctx, "intent", in.ID, in)
	}}, func(options originalDownloadOptions) (download.AddResult, error) {
		if in.SourceModuleStarted || options.CategorySet {
			requestAdd.SourceCategorySet = true
			if category, ok := options.Category.(string); ok {
				requestAdd.SourceCategory = &category
			}
		}
		result, addErr := client.Add(ctx, requestAdd)
		if addErr != nil {
			return result, addErr
		}
		in.Hash = result.Hash
		in.State, in.UpdatedAt = "post_add", now()
		if postAddErr = a.Store.Put(ctx, "intent", in.ID, in); postAddErr == nil {
			ctx, postAddErr = a.applySourceIntentPostAdd(ctx, client, result, in)
		}
		return result, postAddErr
	})
	if postAddErr != nil {
		return postAddErr
	}
	if e != nil {
		if module.Declared {
			in.State, in.Error, in.UpdatedAt = "failed", a.clean(e.Error()), now()
			if saveErr := a.Store.Put(ctx, "intent", in.ID, in); saveErr != nil {
				return saveErr
			}
			return jobErr("REVIEW_PLUGIN_DOWNLOAD_FAILED", e)
		}
		if download.MutationNotStarted(e) || download.MutationRejected(e) {
			in.State, in.Error, in.UpdatedAt = "planned", a.clean(e.Error()), now()
			if saveErr := a.Store.Put(ctx, "intent", in.ID, in); saveErr != nil {
				return saveErr
			}
			return jobErr("REVIEW_DOWNLOAD_REJECTED", e)
		}
		in.State = "reconcile_pending"
		in.Error = a.clean(e.Error())
		_ = a.Store.Put(ctx, "intent", in.ID, in)
		return jobErr("DOWNLOAD_RECONCILE_PENDING", e)
	}
	result := module.Added
	if module.Override {
		if e = verifyOriginalDownloadTask(ctx, client, in.Hash, result.Hash, requestAdd.SavePath); e != nil {
			return e
		}
		if !module.CoreAccepted {
			// The module owns its post-Add work. Observe its real task just as
			// the pre-existing-hash branch does; never synthesize an Add receipt.
			ctx = withSourcePostAddExistingHash(ctx, in)
		}
	}
	in.State = "accepted"
	in.Hash = result.Hash
	in.Error = ""
	in.UpdatedAt = now()
	a.materialMu.Lock()
	delete(a.materials, in.PreviewID)
	a.materialMu.Unlock()
	a.event(ctx, j, "INFO", "downloads", "accepted", "下载器已接收，后续按稳定Hash跟踪")
	sourceContext.OriginalDownloader = module.Downloader
	sourceContext.OriginalLayout = module.Layout
	return a.acceptDownloadWithContext(ctx, in, j, &sourceContext)
}
func sameHash(a, b string) bool {
	a = strings.ToLower(a)
	b = strings.ToLower(b)
	return a == b || (len(a) == 40 && len(b) == 64 && a == b[:40]) || (len(b) == 40 && len(a) == 64 && b == a[:40])
}

func actualSizePolicy(size int64, p Policy) error {
	if (p.MinBytes > 0 && size < p.MinBytes) || (p.MaxBytes > 0 && size > p.MaxBytes) {
		return bad("REVIEW_ACTUAL_SIZE", "种子实际文件总大小不符合大小筛选规则，请重新选择资源")
	}
	return nil
}
func (a *App) downloadList(ctx context.Context, selected ...string) (any, error) {
	selector := ""
	if len(selected) > 0 {
		selector = selected[0]
	}
	moduleRows, useCore, e := a.originalDownloadingTorrents(ctx, selector)
	if e != nil {
		return nil, e
	}
	intents, e := a.Store.List(ctx, "intent", 200, 0)
	if e != nil {
		return nil, e
	}
	instances := []map[string]any{}
	for _, d := range a.Settings().Downloaders {
		if len(selected) > 0 && selected[0] != "" && d.ID != selected[0] {
			continue
		}
		if !downloaderEnabled(d) {
			continue
		}
		owned, e := a.downloaderTaskOwnership(ctx, d.ID)
		entry := map[string]any{"id": d.ID, "name": d.Name, "tasks": []download.Task{}}
		if e == nil {
			visible := []download.Task{}
			for _, row := range moduleRows {
				task, match, err := originalDownloadingTask(row, d, selector != "")
				if err != nil {
					return nil, err
				}
				if match && owned.contains(task) {
					visible = append(visible, task)
				}
			}
			if useCore {
				var c *download.Client
				c, e = download.New(d.Config)
				var tasks []download.Task
				if e == nil {
					tasks, e = c.ListDownloading(ctx)
				}
				if e == nil {
					for _, task := range tasks {
						if owned.contains(task) {
							visible = append(visible, task)
						}
					}
				}
			}
			entry["tasks"] = visible
		}
		if e != nil {
			entry["error"] = a.clean(e.Error())
		}
		instances = append(instances, entry)
	}
	native, e := a.Store.List(ctx, "native_download", 200, 0)
	if e != nil {
		return nil, e
	}
	return map[string]any{"intents": intents, "instances": instances, "native_downloads": native}, nil
}
func (a *App) downloadAction(ctx context.Context, key, action string) error {
	a.downloadMu.Lock()
	defer a.downloadMu.Unlock()
	var in Intent
	if e := a.Store.Get(ctx, "intent", key, &in); e != nil {
		return e
	}
	d, e := a.downloader(in.DownloaderID)
	if e != nil {
		return e
	}
	c, e := download.New(d.Config)
	if e != nil {
		return e
	}
	switch action {
	case "start", "pause", "verify", "remove_keep_data":
	default:
		return bad("INVALID_ACTION", "未知下载操作")
	}
	var moduleErr error
	if action == "remove_keep_data" {
		tasks, err := c.List(ctx)
		if err != nil {
			return err
		}
		found := false
		for _, t := range tasks {
			if sameHash(t.Hash, in.Hash) {
				found = true
				in.Hash = t.Hash
				break
			}
		}
		if found {
			var applied bool
			applied, moduleErr = a.originalDownloaderControl(ctx, d, in.Hash, action, func() error {
				return c.Control(ctx, in.Hash, action)
			})
			if moduleErr != nil && !applied {
				return moduleErr
			}
		}
	} else {
		_, moduleErr = a.originalDownloaderControl(ctx, d, in.Hash, action, func() error {
			return c.Control(ctx, in.Hash, action)
		})
		if moduleErr != nil {
			return moduleErr
		}
	}
	if action == "remove_keep_data" {
		in.State = "cancelled"
		in.UpdatedAt = now()
		tx, e := a.Store.DB.BeginTx(ctx, nil)
		if e != nil {
			return e
		}
		defer tx.Rollback()
		if e = txPut(ctx, tx, "intent", in.ID, in); e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, `DELETE FROM reservations WHERE intent_id=?`, in.ID); e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, `UPDATE jobs SET state='cancelled',generation=generation+1,updated_at=? WHERE kind IN ('download','lark_download') AND json_extract(payload,'$.id')=? AND state IN ('queued','retry_wait','waiting_paused')`, now(), in.ID); e != nil {
			return e
		}
		if e = tx.Commit(); e != nil {
			return e
		}
		return moduleErr
	}
	a.log("INFO", "downloads", action, "管理员操作下载任务", in.WorkflowID, "")
	return moduleErr
}
func (a *App) pollDownloads(ctx context.Context) error {
	a.downloadMu.Lock()
	defer a.downloadMu.Unlock()
	raw, e := a.Store.List(ctx, "intent", 500, 0)
	if e != nil {
		return e
	}
	byInstance := map[string][]Intent{}
	for _, b := range raw {
		var in Intent
		if json.Unmarshal(b, &in) != nil {
			continue
		}
		switch in.State {
		case "accepted", "downloading", "downloaded", "reconcile_pending", "imported":
			byInstance[in.DownloaderID] = append(byInstance[in.DownloaderID], in)
		}
	}
	for _, rule := range a.Settings().Directories {
		if rule.Enabled && rule.MonitorType == "downloader" && rule.DownloaderID != "" {
			if _, ok := byInstance[rule.DownloaderID]; !ok {
				byInstance[rule.DownloaderID] = nil
			}
		}
	}
	for instance, intents := range byInstance {
		d, e := a.downloader(instance)
		if e != nil {
			continue
		}
		client, e := download.New(d.Config)
		if e != nil {
			continue
		}
		tasks, e := client.List(ctx)
		a.recordDirectoryDownloaderStatus(ctx, instance, e)
		if e != nil {
			for _, in := range intents {
				_ = a.saveIntentError(ctx, in.ID, a.clean(e.Error()))
			}
			continue
		}
		for _, in := range intents {
			for _, t := range tasks {
				if !sameHash(t.Hash, in.Hash) {
					continue
				}
				if in.State == "imported" {
					// Pinned TransferChain's all-history-hit fallback retries an
					// untagged completed torrent without republishing its files.
					marked := false
					for _, tag := range t.Tags {
						marked = marked || strings.TrimSpace(tag) == "已整理"
					}
					if !marked && t.Progress >= 1 && !a.paused() {
						if err := a.markOriginalTorrentCompleted(ctx, in); err != nil {
							a.log("WARN", "imports", "torrent_completion_check_failed", a.clean(err.Error()), in.WorkflowID, "")
						}
					}
					break
				}
				latest, terminal, err := a.saveIntentProgress(ctx, in.ID, t)
				if err != nil {
					return err
				}
				if terminal {
					break
				}
				in = latest
				if in.State == "downloaded" && in.DownloaderID != "" {
					if err = a.guardDownloaderSnapshot(ctx, in); err != nil {
						_ = a.saveIntentError(ctx, in.ID, a.clean(err.Error()))
						a.recordDirectoryIntentError(ctx, in, err)
						continue
					}
				}
				stepCtx := ctx
				if parent, err := scanJob(a.Store.DB.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM jobs WHERE kind IN ('download','lark_download') AND json_extract(payload,'$.id')=? ORDER BY created_at DESC LIMIT 1`, in.ID)); err == nil {
					stepCtx = a.traceContext(ctx, in.WorkflowID, parent.ID, parent.RequestID)
				}
				if in.State == "downloaded" {
					if err = a.bindCompletedDirectory(stepCtx, &in, t, d); err != nil {
						a.recordDirectoryIntentError(ctx, in, err)
						_ = a.saveIntentError(ctx, in.ID, a.clean(err.Error()))
						continue
					}
				}
				if in.State == "downloaded" && in.ImportPlanID != "" && !a.paused() {
					if e = a.resumeCompletedPlan(stepCtx, &in); e != nil {
						a.log("WARN", "imports", "plan_waiting", e.Error(), in.WorkflowID, "")
					}
				}
				if in.State == "downloaded" && in.ImportPlanID == "" && !a.paused() {
					if e = a.planCompletedDownload(stepCtx, client, d, &in, t); e != nil {
						in.Error = a.clean(e.Error())
						_ = a.saveIntentError(ctx, in.ID, in.Error)
						a.recordDirectoryIntentError(ctx, in, e)
						a.log("WARN", "imports", "download_needs_review", in.Error, in.WorkflowID, "")
					}
				}
				break
			}
		}
	}
	return nil
}
func (a *App) planCompletedDownload(ctx context.Context, c *download.Client, d DownloaderConfig, in *Intent, task download.Task) error {
	ctx, recognitionErr := a.intentRecognitionContext(ctx, *in)
	if recognitionErr != nil {
		return recognitionErr
	}
	if e := a.guardDownloaderSnapshot(ctx, *in); e != nil {
		return e
	}
	if in.DownloaderSnapshot != downloaderSnapshot(d) {
		return bad("REVIEW_DOWNLOADER_CHANGED", "下载器配置已变化，请重新核对预览")
	}
	if e := a.bindCompletedDirectory(ctx, in, task, d); e != nil {
		return e
	}
	files, e := c.Files(ctx, in.Hash)
	if e != nil {
		return e
	}
	mode := in.ImportMode
	if mode == "" {
		mode = "hardlink"
	}
	request := ImportRequest{MediaType: in.Media.MediaType, TMDBID: in.Media.ID, RootID: in.RootID, Mode: mode, IntentID: in.ID, WorkflowID: in.WorkflowID}
	qualityProof, e := a.qualitySnapshot(ctx, a.Store.DB, "intent", in.ID, in.Hash)
	if e != nil {
		return e
	}
	request.qualityReplacement = qualityProof != nil && qualityProof.BestVersion
	seasonProof, e := a.intentSeasonProof(ctx, a.Store.DB, *in)
	if e != nil {
		return e
	}
	if in.DirectoryID != "" {
		rule, ruleErr := a.getDirectory(in.DirectoryID)
		if ruleErr != nil {
			return ruleErr
		}
		request.Renaming, request.Scraping, request.LibraryTypeFolder = rule.Renaming, rule.Scraping, rule.LibraryTypeFolder
	}
	rules := in.Request.Mappings
	request.Mappings = rules
	for _, f := range files {
		if !isVideo(f.Path) {
			continue
		}
		if qualityProof != nil && in.Media.MediaType == "tv" {
			selected, err := a.qualityCompletedFileSelected(ctx, *in, f.Path)
			if err != nil {
				return err
			}
			if !selected {
				continue
			}
		}
		if f.Progress < 1 {
			return bad("DOWNLOAD_NOT_COMPLETE", "视频文件尚未下载完成，等待下一轮")
		}
		if filepath.IsAbs(f.Path) || strings.ContainsAny(f.Path, "\x00\r\n") || filepath.Clean(f.Path) == ".." || strings.HasPrefix(filepath.Clean(f.Path), ".."+string(filepath.Separator)) {
			return bad("INVALID_DOWNLOAD_PATH", "下载器返回的视频文件路径越界")
		}
		full, e := mapDownloaderPath(a.Settings(), d, filepath.Join(task.DownloadDir, filepath.FromSlash(f.Path)))
		if e != nil {
			return e
		}
		stamp, e := snapshotPath(full)
		if e != nil {
			return e
		}
		if f.Size <= 0 || stamp.Size != f.Size {
			return bad("DOWNLOAD_FILE_NOT_READY", "下载文件大小与下载器记录不一致，等待重新检查")
		}
		parsed := a.parseMediaNameContext(ctx, filepath.Base(f.Path))
		entry := ImportFileRequest{Path: full}
		if seasonProof != nil {
			scoped, ok := seasonScopeForFile(seasonProof, f.Path)
			if !ok || scoped.Size != f.Size {
				return seasonScopeError()
			}
			entry.Season, entry.Episodes = scoped.Season, qualityCopyNote(scoped.Episodes)
		} else if in.Media.MediaType == "tv" {
			if parsed.Original == nil {
				continue
			}
			season, e := a.season(ctx, in.Media.ID, in.Season)
			if e != nil {
				return e
			}
			known := []media.Episode{}
			for _, ep := range season.Episodes {
				known = append(known, media.Episode{Season: in.Season, Episode: ep.EpisodeNumber})
			}
			resolved := media.Resolve(mediaKey(in.Media), *parsed.Original, media.Source{Site: in.SiteID}, rules, known, nil)
			if resolved.Status != media.Parsed {
				continue
			}
			valid := true
			for _, ep := range resolved.Standard {
				if ep.Season != in.Season || !containsInt(in.Episodes, ep.Episode) {
					valid = false
				}
				entry.Episodes = append(entry.Episodes, ep.Episode)
			}
			if !valid {
				continue
			}
			entry.Season = in.Season
		}
		request.Files = append(request.Files, entry)
	}
	covered := map[media.Episode]bool{}
	for _, f := range request.Files {
		for _, ep := range f.Episodes {
			covered[media.Episode{Season: f.Season, Episode: ep}] = true
		}
	}
	scopes := []downloadSeasonScope{{in.Season, in.Episodes}}
	if seasonProof != nil {
		scopes = seasonProof.Scopes
	}
	if in.Media.MediaType == "tv" {
		for _, scope := range scopes {
			for _, ep := range scope.Episodes {
				if !covered[media.Episode{Season: scope.Season, Episode: ep}] {
					return bad("REVIEW_DOWNLOAD_COVERAGE", "下载完成文件未覆盖所有目标，请手动整理核对")
				}
			}
		}
	}
	if len(request.Files) == 0 {
		return bad("REVIEW_DOWNLOAD_FILES", "下载文件需要手动识别或集号修正")
	}
	plan, e := a.previewImport(ctx, request)
	if e != nil {
		return e
	}
	for _, f := range plan.Files {
		if f.Status != "ready" && f.Status != "already_imported" {
			plan.State = "needs_review"
			_ = a.Store.Put(ctx, "import_plan", plan.ID, plan)
			in.ImportPlanID = plan.ID
			_ = a.Store.Put(ctx, "intent", in.ID, *in)
			return bad("REVIEW_IMPORT_PLAN", "整理计划存在冲突，请在手动整理页核对")
		}
	}
	in.ImportPlanID = plan.ID
	if e = a.Store.Put(ctx, "intent", in.ID, *in); e != nil {
		return e
	}
	_, e = a.executeImport(ctx, plan.ID, nil)
	return e
}
func mapPath(p string, mappings []PathMapping) (string, error) {
	p = filepath.Clean(p)
	best := -1
	dest := ""
	for _, m := range mappings {
		if within(m.From, p) && len(m.From) > best {
			rel, e := filepath.Rel(m.From, p)
			if e != nil {
				return "", e
			}
			dest = filepath.Join(m.To, rel)
			best = len(m.From)
		}
	}
	if best < 0 {
		return "", bad("PATH_MAPPING_REQUIRED", "下载器目录尚未映射到应用可见目录")
	}
	return dest, nil
}
func containsInt(xs []int, n int) bool {
	for _, x := range xs {
		if x == n {
			return true
		}
	}
	return false
}
func (a *App) ensureNotExcluded(ctx context.Context, m catalog.Media, season int, episodes []int) error {
	keys := episodes
	if len(keys) == 0 {
		keys = []int{0}
	}
	for _, n := range keys {
		var rule struct {
			Blocked bool `json:"blocked"`
		}
		e := a.Store.Get(ctx, "exclusion", fmt.Sprintf("%s:%d:%d", mediaKey(m), season, n), &rule)
		if e == nil && rule.Blocked {
			return bad("EXCLUDED_CONTENT", "该内容已被排除自动获取和整理，请先显式解除")
		}
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return e
		}
	}
	return nil
}

func (a *App) resumeCompletedPlan(ctx context.Context, in *Intent) error {
	var plan ImportPlan
	if e := a.Store.Get(ctx, "import_plan", in.ImportPlanID, &plan); e != nil {
		return e
	}
	if plan.State != "preview" {
		return nil
	}
	for _, f := range plan.Files {
		if f.Status != "ready" && f.Status != "already_imported" && f.Status != "imported" {
			return nil
		}
	}
	var n int
	if e := a.Store.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM jobs WHERE kind='import' AND json_extract(payload,'$.id')=? AND state IN ('queued','running','retry_wait','waiting_paused')`, plan.ID).Scan(&n); e != nil {
		return e
	}
	if n > 0 {
		return nil
	}
	_, e := a.executeImport(ctx, plan.ID, nil)
	return e
}

func (a *App) saveIntentProgress(ctx context.Context, key string, t download.Task) (Intent, bool, error) {
	tx, e := a.Store.DB.BeginTx(ctx, nil)
	if e != nil {
		return Intent{}, false, e
	}
	defer tx.Rollback()
	var b []byte
	if e = tx.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind='intent' AND id=?`, key).Scan(&b); e != nil {
		return Intent{}, false, e
	}
	var in Intent
	if e = json.Unmarshal(b, &in); e != nil {
		return in, false, e
	}
	if in.State == "imported" || in.State == "cancelled" {
		return in, true, nil
	}
	if e = a.guardAcceptedDownloadProgressTx(ctx, tx, in); e != nil {
		return in, false, e
	}
	previousState := in.State
	in.Hash = t.Hash
	in.Progress = t.Progress
	in.State = "downloading"
	if t.Progress >= 1 && t.TotalSize > 0 && t.Completed >= t.TotalSize {
		in.State = "downloaded"
	}
	in.UpdatedAt = now()
	if e = txPut(ctx, tx, "intent", key, in); e != nil {
		return in, false, e
	}
	if in.State == "downloaded" && previousState != "downloaded" {
		if e = recordDomainEventTx(ctx, tx, DomainEvent{SchemaVersion: 1, EventID: domainEventID("download.completed", in.ID), Type: "download.completed", OccurredAt: now(), WorkflowID: in.WorkflowID, MediaID: mediaKey(in.Media), IntentID: in.ID, Purpose: in.Purpose}); e != nil {
			return in, false, e
		}
	}
	return in, false, tx.Commit()
}

func (a *App) saveIntentError(ctx context.Context, key, message string) error {
	_, e := a.Store.DB.ExecContext(ctx, `UPDATE records SET payload=json_set(payload,'$.error',?,'$.updated_at',?),updated_at=? WHERE kind='intent' AND id=? AND json_extract(payload,'$.state') NOT IN ('imported','cancelled')`, message, now(), now(), key)
	return e
}
