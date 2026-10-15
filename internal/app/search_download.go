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
	VerifiedSeasonPack bool                `json:"verified_season_pack,omitempty"`
	WorkflowID         string              `json:"workflow_id"`
	Query              string              `json:"query,omitempty"`
	MediaType          string              `json:"media_type"`
	TMDBID             int64               `json:"tmdb_id"`
	Season             int                 `json:"season"`
	Episodes           []int               `json:"episodes"`
	Policy             Policy              `json:"policy"`
	SiteIDs            []string            `json:"site_ids"`
	Page               int                 `json:"page"`
	Mappings           []media.MappingRule `json:"mappings"`
	AllowEarly         []int               `json:"allow_early"`
	EpisodeDates       map[int]string      `json:"episode_dates"`
}
type PreviewRequest struct {
	SearchRequest
	automaticSeasonPack bool
	SiteID              string `json:"site_id"`
	ResourceID          string `json:"resource_id"`
	DownloaderID        string `json:"downloader_id"`
	RootID              string `json:"root_id"`
	SubscriptionID      string `json:"subscription_id"`
	Revision            int    `json:"revision"`
}

func (a *App) site(key string) (SiteConfig, error) {
	for _, s := range a.Settings().Sites {
		if s.ID == key && s.Enabled {
			return s, nil
		}
	}
	return SiteConfig{}, bad("SITE_NOT_CONFIGURED", "站点尚未配置或未启用")
}
func (a *App) downloader(key string) (DownloaderConfig, error) {
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
	client, e := sites.New(s.Config)
	if e != nil {
		return bad("SITE_CONFIG", e.Error())
	}
	client.SetRequestGuard(func(requestCtx context.Context) error {
		if e := a.guardSubscriptionContext(requestCtx); e != nil {
			return e
		}
		if auto && a.paused() {
			return &APIError{"AUTOMATION_PAUSED", "自动化已暂停", 409}
		}
		current, e := a.site(s.ID)
		if e != nil {
			return e
		}
		if current.BaseURL != s.BaseURL || current.Cookie != s.Cookie || current.APIKey != s.APIKey {
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
	return decision.Policy{Source: p.Source, HDR: p.HDR, Languages: p.Languages, Subtitles: p.Subtitles, PreferredGroups: p.PreferredGroups, PreferredResolutions: p.PreferredResolutions, MinSeeders: p.MinSeeders, FreeOnly: p.FreeOnly, Resolution: p.Resolution, Codec: p.Codec, MinBytes: p.MinBytes, MaxBytes: p.MaxBytes, Include: p.Include, Exclude: p.Exclude}
}
func (a *App) search(ctx context.Context, in SearchRequest, auto bool) (SearchOutput, error) {
	t, e := a.target(ctx, in, auto)
	if e != nil {
		return SearchOutput{}, e
	}
	out := SearchOutput{Candidates: []Candidate{}, Sites: []SiteStatus{}, WorkflowID: a.workflow(ctx, in.WorkflowID, t.Media), CachedAt: now()}
	selected := map[string]bool{}
	for _, v := range in.SiteIDs {
		selected[v] = true
	}
	for _, s := range a.Settings().Sites {
		if ctx.Err() != nil {
			return out, ctx.Err()
		}
		if !s.Enabled || (len(selected) > 0 && !selected[s.ID]) {
			continue
		}
		status := SiteStatus{ID: s.ID, Status: "ok"}
		var result sites.SearchResult
		queries := map[string]string{}
		successfulQueries := 0
		start := time.Now()
		e = a.withSite(ctx, s, auto, func(client *sites.Client) error {
			seen := map[string]bool{}
			for _, query := range fallbackSearchQueries(t.Media, in.Query) {
				page, err := client.Search(ctx, query, in.Page)
				accepted := false
				for _, resource := range page.Resources {
					if !seen[resource.ID] {
						result.Resources = append(result.Resources, resource)
						queries[resource.ID] = query
						seen[resource.ID] = true
					}
					if decision.Evaluate(resource, t, decisionPolicy(in.Policy)).Accepted {
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
		if e != nil {
			status.Status = "failed"
			if successfulQueries > 0 || len(result.Resources) > 0 {
				status.Status = "partial"
			}
			status.Message = a.clean(e.Error())
			a.log("WARN", "sites", "search_failed", status.Message, out.WorkflowID, "")
		}
		if successfulQueries > 0 || len(result.Resources) > 0 {
			status.Count = len(result.Resources)
			if len(result.Resources) == 0 && e == nil {
				status.Status = "empty"
			}
			for _, r := range result.Resources {
				d := decision.Evaluate(r, t, decisionPolicy(in.Policy))
				out.Candidates = append(out.Candidates, Candidate{Resource: r, Accepted: d.Accepted, Reasons: d.Reasons, Season: d.Season, Episodes: d.Episodes, NeedsConfirmation: d.NeedsConfirmation})
				_ = a.Store.Put(ctx, "resource", r.SiteID+":"+r.ID, map[string]any{"resource": r, "query": queries[r.ID], "page": in.Page, "fetched_at": now(), "media_key": mediaKey(t.Media)})
			}
			a.log("INFO", "sites", "search_completed", fmt.Sprintf("站点 %s 返回 %d 个资源，耗时 %dms", s.Name, len(result.Resources), time.Since(start).Milliseconds()), out.WorkflowID, "")
		}
		out.Sites = append(out.Sites, status)
		a.logSite(status, out.WorkflowID)
		emitSearchProgress(ctx, out)
	}
	if len(out.Sites) == 0 {
		return out, bad("NO_SITES", "请在设置中启用至少一个站点")
	}
	sort.SliceStable(out.Candidates, func(i, j int) bool {
		if out.Candidates[i].Accepted != out.Candidates[j].Accepted {
			return out.Candidates[i].Accepted
		}
		leftRank, rightRank := decision.Rank(out.Candidates[i].Resource, decisionPolicy(in.Policy)), decision.Rank(out.Candidates[j].Resource, decisionPolicy(in.Policy))
		if leftRank != rightRank {
			return leftRank > rightRank
		}
		if out.Candidates[i].Resource.Free != out.Candidates[j].Resource.Free {
			return out.Candidates[i].Resource.Free
		}
		return out.Candidates[i].Resource.Seeders > out.Candidates[j].Resource.Seeders
	})
	a.saveDecision(ctx, out, t.Media)
	return out, nil
}
func (a *App) previewDownload(ctx context.Context, in PreviewRequest, auto bool) (DownloadPreview, error) {
	in.VerifiedSeasonPack = false // Never accept a client assertion of material validation.
	t, e := a.target(ctx, in.SearchRequest, auto)
	if e != nil {
		return DownloadPreview{}, e
	}
	if t.Media.MediaType == "tv" && len(in.Episodes) == 0 {
		return DownloadPreview{}, bad("REVIEW_EPISODES", "请选择此资源的目标集数；整季下载也须先明确确认本季集数")
	}
	in.Episodes = append([]int{}, t.Episodes...)
	site, e := a.site(in.SiteID)
	if e != nil {
		return DownloadPreview{}, e
	}
	d, e := a.downloader(in.DownloaderID)
	if e != nil {
		return DownloadPreview{}, e
	}
	root, e := a.root(in.RootID, t.Media.MediaType)
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
		torrent, err2 = client.Torrent(ctx, in.ResourceID)
		return err2
	})
	if e != nil {
		return DownloadPreview{}, e
	}
	choice := decision.Evaluate(fresh, t, decisionPolicy(in.Policy))
	verifiedPack := auto && in.automaticSeasonPack && eligibleAutomaticSeasonPack(fresh, t, in.Policy)
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
	p := DownloadPreview{WorkflowID: a.workflow(ctx, in.WorkflowID, t.Media), Request: in.SearchRequest, ID: id("preview_"), Media: t.Media, Season: in.Season, Episodes: in.Episodes, SiteID: site.ID, ResourceID: in.ResourceID, ResourceTitle: fresh.Title, DownloaderID: d.ID, RootID: root.ID, Hash: material.Hash, Files: []string{}, TotalBytes: material.TotalBytes, CreatedAt: now(), ExpiresAt: time.Now().Add(10 * time.Minute).UTC().Format(time.RFC3339Nano), Policy: in.Policy, SubscriptionID: in.SubscriptionID, Revision: in.Revision, Auto: auto, Warnings: choice.Reasons, NeedsConfirmation: choice.NeedsConfirmation}
	p.DownloaderSnapshot = downloaderSnapshot(d)
	coverage := map[int]bool{}
	p.ImportMode = a.Settings().AutoImportMode
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
		fileTarget := t
		if verifiedPack {
			fileTarget.AllowEarly = nil
		}
		fileChoice := decision.Evaluate(fileResource, fileTarget, decision.Policy{})
		if !fileChoice.Accepted || fileChoice.NeedsConfirmation {
			if auto {
				return p, bad("REVIEW_FILE_IDENTITY", "种子实际视频文件名不能确认目标身份，请手动核对")
			}
			p.Warnings = append(p.Warnings, "需要核对实际文件身份："+filepath.Base(f.Path))
			p.NeedsConfirmation = true
		}
		if t.Media.MediaType == "tv" {
			parsed := media.Parse(filepath.Base(f.Path))
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
					if verifiedPack && coverage[ep.Episode] {
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
	if verifiedPack {
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
	if p.ExtraFiles {
		p.Warnings = append(p.Warnings, "此资源还包含额外视频，确认后仅整理所选目标")
		p.NeedsConfirmation = true
	}
	if auto && p.NeedsConfirmation {
		return p, bad("REVIEW_RESOURCE", "自动获取需人工确认该资源包")
	}
	p.MaterialRef, e = a.persistMaterial(ctx, torrent, p.Hash)
	if e != nil {
		return p, e
	}
	p.MaterialState = "archived"
	if e = a.Store.Put(ctx, "download_preview", p.ID, p); e != nil {
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
func (a *App) confirmDownload(ctx context.Context, key string, acceptExtra bool) (Intent, error) {
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
	intent.ImportMode = p.ImportMode
	intent.DownloaderSnapshot = p.DownloaderSnapshot
	intent.MaterialRef, intent.MaterialState, intent.Purpose = p.MaterialRef, p.MaterialState, materialPurpose
	tx, e := a.Store.DB.BeginTx(ctx, nil)
	if e != nil {
		return intent, e
	}
	defer tx.Rollback()
	if e = pinMaterialTx(ctx, tx, p.MaterialRef, p.Hash); e != nil {
		return intent, e
	}
	if e = a.Store.Reserve(ctx, tx, mediaKey(p.Media), p.Season, p.Episodes, intent.ID); e != nil {
		return intent, bad("ALREADY_ACQUIRED", e.Error())
	}
	if e = txPut(ctx, tx, "intent", intent.ID, intent); e != nil {
		return intent, e
	}
	if _, e = enqueueTx(ctx, tx, "download", map[string]string{"id": intent.ID}, intent.WorkflowID); e != nil {
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
	if in.State == "imported" || in.State == "cancelled" {
		return nil
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
	if e := a.ensureNotExcluded(ctx, in.Media, in.Season, in.Episodes); e != nil {
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
	client, e := download.New(d.Config)
	if e != nil {
		return e
	}
	tasks, e := client.List(ctx)
	if e != nil {
		return jobErr("DOWNLOADER_UNAVAILABLE", e)
	}
	for _, task := range tasks {
		if sameHash(task.Hash, in.Hash) {
			in.Hash = task.Hash
			in.State = "accepted"
			in.UpdatedAt = now()
			a.event(ctx, j, "INFO", "downloads", "reconciled", "下载器已有对应种子，已关联而未重复添加")
			return a.acceptDownload(ctx, in, j)
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
	target, e := a.target(ctx, request, in.Auto)
	if e != nil {
		return e
	}
	site, e := a.site(in.SiteID)
	if e != nil {
		return e
	}
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
		if err != nil {
			a.log("WARN", "sites", "partial_recheck", "同页其他资源解析不完整；当前资源已独立重新确认", in.WorkflowID, j.ID)
		}
		err = nil
		choice := decision.Evaluate(*fresh, target, decisionPolicy(preview.Policy))
		if in.Auto && request.VerifiedSeasonPack && eligibleAutomaticSeasonPack(*fresh, target, preview.Policy) {
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
	material, e := metainfo.ParseMaterial(torrent)
	if e != nil || !sameHash(material.Hash, in.Hash) {
		return bad("REVIEW_MATERIAL_CHANGED", "下载材料与确认预览不一致")
	}
	if e = actualSizePolicy(material.TotalBytes, preview.Policy); e != nil {
		return e
	}
	if in.Auto && request.VerifiedSeasonPack {
		if e = validateAutomaticSeasonPackFiles(material, target, in.SiteID); e != nil {
			return e
		}
	}
	if e = a.jobCancelled(ctx, j); e != nil {
		return e
	}
	if e = a.guardSubscriptionContext(ctx); e != nil {
		return e
	}
	if e = a.ensureNotExcluded(ctx, in.Media, in.Season, in.Episodes); e != nil {
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
	release, e := a.beginWrite()
	if e != nil {
		return e
	}
	defer release()
	in.State = "submitting"
	in.UpdatedAt = now()
	if e = a.Store.Put(ctx, "intent", in.ID, in); e != nil {
		return e
	}
	result, e := client.Add(ctx, download.AddRequest{Torrent: torrent, SavePath: d.DownloadPath, Tags: []string{"light-media"}})
	if e != nil {
		in.State = "reconcile_pending"
		in.Error = a.clean(e.Error())
		_ = a.Store.Put(ctx, "intent", in.ID, in)
		return jobErr("DOWNLOAD_RECONCILE_PENDING", e)
	}
	in.State = "accepted"
	in.Hash = result.Hash
	in.Error = ""
	in.UpdatedAt = now()
	a.materialMu.Lock()
	delete(a.materials, in.PreviewID)
	a.materialMu.Unlock()
	a.event(ctx, j, "INFO", "downloads", "accepted", "下载器已接收，后续按稳定Hash跟踪")
	return a.acceptDownload(ctx, in, j)
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
func (a *App) downloadList(ctx context.Context) (any, error) {
	intents, e := a.Store.List(ctx, "intent", 200, 0)
	if e != nil {
		return nil, e
	}
	instances := []map[string]any{}
	for _, d := range a.Settings().Downloaders {
		c, e := download.New(d.Config)
		entry := map[string]any{"id": d.ID, "name": d.Name, "tasks": []download.Task{}}
		if e == nil {
			var tasks []download.Task
			tasks, e = c.List(ctx)
			entry["tasks"] = tasks
		}
		if e != nil {
			entry["error"] = a.clean(e.Error())
		}
		instances = append(instances, entry)
	}
	return map[string]any{"intents": intents, "instances": instances}, nil
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
			if e = c.Control(ctx, in.Hash, action); e != nil {
				return e
			}
		}
	} else {
		if e = c.Control(ctx, in.Hash, action); e != nil {
			return e
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
		if _, e = tx.ExecContext(ctx, `UPDATE jobs SET state='cancelled',generation=generation+1,updated_at=? WHERE kind='download' AND json_extract(payload,'$.id')=? AND state IN ('queued','retry_wait','waiting_paused')`, now(), in.ID); e != nil {
			return e
		}
		return tx.Commit()
	}
	a.log("INFO", "downloads", action, "管理员操作下载任务", in.WorkflowID, "")
	return nil
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
		case "accepted", "downloading", "downloaded", "reconcile_pending":
			byInstance[in.DownloaderID] = append(byInstance[in.DownloaderID], in)
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
		if e != nil {
			continue
		}
		for _, in := range intents {
			for _, t := range tasks {
				if !sameHash(t.Hash, in.Hash) {
					continue
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
						continue
					}
				}
				stepCtx := ctx
				if parent, err := scanJob(a.Store.DB.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM jobs WHERE kind='download' AND json_extract(payload,'$.id')=? ORDER BY created_at DESC LIMIT 1`, in.ID)); err == nil {
					stepCtx = a.traceContext(ctx, in.WorkflowID, parent.ID, parent.RequestID)
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
	if e := a.guardDownloaderSnapshot(ctx, *in); e != nil {
		return e
	}
	if in.DownloaderSnapshot != downloaderSnapshot(d) {
		return bad("REVIEW_DOWNLOADER_CHANGED", "下载器配置已变化，请重新核对预览")
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
	rules := in.Request.Mappings
	request.Mappings = rules
	for _, f := range files {
		if !isVideo(f.Path) || f.Progress < 1 {
			continue
		}
		full, e := mapPath(filepath.Join(task.DownloadDir, filepath.FromSlash(f.Path)), d.Mappings)
		if e != nil {
			return e
		}
		parsed := media.Parse(filepath.Base(f.Path))
		entry := ImportFileRequest{Path: full}
		if in.Media.MediaType == "tv" {
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
	covered := map[int]bool{}
	for _, f := range request.Files {
		for _, ep := range f.Episodes {
			covered[ep] = true
		}
	}
	if in.Media.MediaType == "tv" {
		for _, ep := range in.Episodes {
			if !covered[ep] {
				return bad("REVIEW_DOWNLOAD_COVERAGE", "下载完成文件未覆盖所有目标，请手动整理核对")
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
