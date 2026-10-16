package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"light-media/internal/catalog"
	"light-media/internal/media"
)

type SubscriptionRequest struct {
	Keyword       string              `json:"keyword"`
	SiteIDs       []string            `json:"site_ids"`
	TotalEpisodes int                 `json:"total_episodes"`
	DirectoryID   string              `json:"directory_id"`
	DownloadPath  string              `json:"download_path"`
	TargetScope   string              `json:"target_scope"`
	StartEpisode  int                 `json:"start_episode"`
	MediaType     string              `json:"media_type"`
	TMDBID        int64               `json:"tmdb_id"`
	Media         catalog.Media       `json:"media"`
	Season        int                 `json:"season"`
	Episodes      []int               `json:"episodes"`
	Excluded      []int               `json:"excluded"`
	Policy        Policy              `json:"policy"`
	Schedule      Schedule            `json:"schedule"`
	Mappings      []media.MappingRule `json:"mappings"`
	DownloaderID  string              `json:"downloader_id"`
	RootID        string              `json:"root_id"`
	State         string              `json:"state"`
	EpisodeDates  map[int]string      `json:"episode_dates"`
	AllowEarly    []int               `json:"allow_early"`
	Revision      int                 `json:"revision"`
}
type subscriptionGuardKey struct{}
type subscriptionIdentity struct {
	ID       string
	Revision int
}

func subscriptionContext(ctx context.Context, key string, revision int) context.Context {
	return context.WithValue(ctx, subscriptionGuardKey{}, subscriptionIdentity{key, revision})
}
func (a *App) guardSubscriptionContext(ctx context.Context) error {
	if identity, ok := ctx.Value(subscriptionGuardKey{}).(subscriptionIdentity); ok {
		return a.guardSubscription(ctx, identity.ID, identity.Revision)
	}
	return nil
}
func (a *App) guardSubscription(ctx context.Context, key string, revision int) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if a.paused() {
		return &APIError{"AUTOMATION_PAUSED", "自动化已暂停", 409}
	}
	var s Subscription
	if e := a.Store.Get(ctx, "subscription", key, &s); e != nil {
		return e
	}
	if s.State != "active" || s.Revision != revision {
		return bad("REVIEW_SUBSCRIPTION_CHANGED", "订阅已修改、暂停或取消，本轮检查已失效")
	}
	return nil
}
func subscriptionNumbers(values []int) ([]int, error) {
	if len(values) > 10000 {
		return nil, bad("INVALID_EPISODES", "集号数量过多")
	}
	out := append([]int{}, values...)
	sort.Ints(out)
	for i, n := range out {
		if n < 1 || n > 9999 || (i > 0 && out[i-1] == n) {
			return nil, bad("INVALID_EPISODES", "集号须为1至9999且不重复")
		}
	}
	return out, nil
}
func validateSubscriptionPolicy(p Policy) error {
	if p.MinSeeders < 0 || p.MinBytes < 0 || p.MaxBytes < 0 || (p.MaxBytes > 0 && p.MinBytes > p.MaxBytes) {
		return bad("INVALID_POLICY", "做种人数或体积范围无效")
	}
	for _, v := range []string{p.Include, p.Exclude} {
		if len(v) > 512 {
			return bad("INVALID_POLICY", "筛选正则过长")
		}
		if _, e := regexp.Compile(v); e != nil {
			return bad("INVALID_POLICY", "筛选正则无效")
		}
	}
	return nil
}
func (a *App) saveSubscription(ctx context.Context, key string, in SubscriptionRequest) (Subscription, error) {
	var snapshot Subscription
	if key != "" {
		if e := a.Store.Get(ctx, "subscription", key, &snapshot); e != nil {
			return Subscription{}, e
		}
	}
	if in.MediaType == "" {
		in.MediaType = in.Media.MediaType
	}
	if in.TMDBID == 0 {
		in.TMDBID = in.Media.ID
	}
	if snapshot.ID != "" {
		if in.MediaType == "" {
			in.MediaType = snapshot.Media.MediaType
		}
		if in.TMDBID == 0 {
			in.TMDBID = snapshot.Media.ID
		}
		if in.MediaType != snapshot.Media.MediaType || in.TMDBID != snapshot.Media.ID {
			return Subscription{}, bad("SUBSCRIPTION_IDENTITY_CHANGED", "请为另一部作品创建新订阅")
		}
		if in.State == "" {
			in.State = snapshot.State
		}
	}
	var m catalog.Media
	var e error
	if in.State == "paused" && snapshot.ID != "" {
		m = snapshot.Media
	} else {
		m, e = a.media(ctx, in.MediaType, in.TMDBID)
		if e != nil {
			return Subscription{}, e
		}
	}
	if e = normalizeSubscriptionScope(&in, snapshot, m.MediaType); e != nil {
		return Subscription{}, e
	}
	if in.TotalEpisodes < 0 || in.TotalEpisodes > 9999 || in.TotalEpisodes > 0 && in.TotalEpisodes < in.StartEpisode {
		return Subscription{}, bad("INVALID_TOTAL_EPISODES", "总集数须大于等于开始集数，且不超过9999")
	}
	if len(in.SiteIDs) > 128 {
		return Subscription{}, bad("INVALID_SITES", "订阅站点数量超限")
	}
	in.Keyword = strings.TrimSpace(in.Keyword)
	if len([]rune(in.Keyword)) > 200 || strings.ContainsAny(in.Keyword, "\x00\r\n") {
		return Subscription{}, bad("INVALID_KEYWORD", "搜索关键词须在200字以内")
	}
	seenSites := map[string]bool{}
	for _, key := range in.SiteIDs {
		if seenSites[key] {
			return Subscription{}, bad("INVALID_SITES", "订阅站点不能重复")
		}
		seenSites[key] = true
		found := false
		for _, site := range a.Settings().Sites {
			if site.ID == key {
				found = true
				break
			}
		}
		if !found && !(snapshot.ID != "" && in.State == "paused" && slices.Contains(snapshot.SiteIDs, key)) {
			return Subscription{}, bad("INVALID_SITES", "订阅选择的站点不存在")
		}
	}
	schedule, e := normalizeSchedule(in.Schedule)
	if e != nil {
		return Subscription{}, e
	}
	if e = validateSubscriptionPolicy(in.Policy); e != nil {
		return Subscription{}, e
	}
	episodes, e := subscriptionNumbers(in.Episodes)
	if e != nil {
		return Subscription{}, e
	}
	excluded, e := subscriptionNumbers(in.Excluded)
	if e != nil {
		return Subscription{}, e
	}
	early, e := subscriptionNumbers(in.AllowEarly)
	if e != nil {
		return Subscription{}, e
	}
	known := map[int]bool{}
	if m.MediaType == "movie" {
		if in.Season != 0 || len(episodes)+len(excluded)+len(early) > 0 || len(in.Mappings) > 0 || len(in.EpisodeDates) > 0 {
			return Subscription{}, bad("INVALID_MOVIE_SUBSCRIPTION", "电影订阅不能设置季集或剧集映射")
		}
	} else {
		if in.Season < 1 || in.Season > 99 {
			return Subscription{}, bad("INVALID_SEASON", "请选择标准季号")
		}
		if in.State == "paused" && snapshot.ID != "" && snapshot.Season == in.Season {
			for _, ep := range a.cachedSeasonEpisodes(ctx, snapshot) {
				known[ep] = true
			}
			// Pausing remains local even when the provider is unavailable. Only
			// numbers already confirmed in the stored subscription are added.
			for _, values := range [][]int{snapshot.Episodes, snapshot.Excluded, snapshot.AllowEarly} {
				for _, ep := range values {
					known[ep] = true
				}
			}
			for ep := range snapshot.EpisodeDates {
				known[ep] = true
			}
		} else {
			season, e := a.season(ctx, m.ID, in.Season)
			if e != nil {
				return Subscription{}, e
			}
			for _, ep := range season.Episodes {
				known[ep.EpisodeNumber] = true
			}
		}
		for _, values := range [][]int{episodes, excluded, early} {
			for _, ep := range values {
				if !known[ep] {
					return Subscription{}, bad("UNKNOWN_EPISODE", "订阅集号必须已被元数据确认")
				}
			}
		}
		for ep, date := range in.EpisodeDates {
			if !known[ep] {
				return Subscription{}, bad("UNKNOWN_EPISODE", "自定义播出日期不能创造元数据之外的集")
			}
			if _, e := parseDay(date, shanghai()); e != nil {
				return Subscription{}, e
			}
		}
		for _, x := range schedule.Exceptions {
			if x.Episode > 0 && !known[x.Episode] {
				return Subscription{}, bad("UNKNOWN_EPISODE", "例外集号尚未被元数据确认")
			}
		}
	}
	if len(in.Mappings) > 128 {
		return Subscription{}, bad("INVALID_MAPPING", "映射规则过多")
	}
	for _, r := range in.Mappings {
		if e := r.Validate(); e != nil || r.MediaID != mediaKey(m) || r.TargetSeason != in.Season {
			return Subscription{}, bad("INVALID_MAPPING", "映射规则须匹配此作品及标准季")
		}
	}
	destination := directoryDestination{}
	preservePausedDestination := snapshot.ID != "" && in.State == "paused" && in.DownloaderID == snapshot.DownloaderID && in.DownloadPath == snapshot.DownloadPath && in.DirectoryID == snapshot.DirectoryID && in.RootID == snapshot.RootID
	if preservePausedDestination {
		// Pausing an existing subscription is local even after a referenced
		// connection/rule has been disabled or removed. Activation revalidates it.
		destination = directoryDestination{Path: snapshot.DownloadPath, Root: snapshot.RootID, Rule: DirectoryRule{ID: snapshot.DirectoryID}}
	} else {
		d, connectionErr := a.downloader(in.DownloaderID)
		if connectionErr != nil && (in.DownloaderID != "" || len(a.Settings().Downloaders) > 0) {
			return Subscription{}, connectionErr
		}
		if in.DownloadPath != "" || in.DirectoryID != "" || in.RootID != "" {
			// Subscription defaults are preferences, not a current daemon snapshot.
			// Resolve/freeze the actual default only when a download is submitted.
			if in.RootID == "" {
				d.SharedPaths = true
			}
			destination, e = a.resolveDownloadDestinationContext(ctx, d, in.DirectoryID, in.DownloadPath, in.RootID, m.MediaType)
			if e != nil {
				return Subscription{}, e
			}
		}
	}
	state := in.State
	if state == "" {
		state = "active"
	}
	if state != "active" && state != "paused" && !(state == "completed" && snapshot.State == "completed" && m.MediaType == "movie") {
		return Subscription{}, bad("INVALID_SUBSCRIPTION_STATE", "订阅状态须为active或paused")
	}
	a.subMu.Lock()
	defer a.subMu.Unlock()
	tx, e := a.Store.DB.BeginTx(ctx, nil)
	if e != nil {
		return Subscription{}, e
	}
	defer tx.Rollback()
	var old Subscription
	if key != "" {
		var b []byte
		if e = tx.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind='subscription' AND id=?`, key).Scan(&b); e != nil {
			return old, e
		}
		if e = json.Unmarshal(b, &old); e != nil {
			return old, e
		}
		if in.Revision > 0 && in.Revision != old.Revision {
			return old, bad("SUBSCRIPTION_CONFLICT", "订阅已被其他操作修改，请刷新后重试")
		}
	} else {
		key = id("sub_")
	}
	rows, e := tx.QueryContext(ctx, `SELECT payload FROM records WHERE kind='subscription'`)
	if e != nil {
		return old, e
	}
	count := 0
	duplicate := false
	for rows.Next() {
		count++
		var b []byte
		if e = rows.Scan(&b); e != nil {
			rows.Close()
			return old, e
		}
		var other Subscription
		if json.Unmarshal(b, &other) == nil && other.ID != key && other.State != "cancelled" && mediaKey(other.Media) == mediaKey(m) && other.Season == in.Season {
			duplicate = true
		}
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return old, e
	}
	if duplicate {
		return old, bad("DUPLICATE_SUBSCRIPTION", "该作品及季已有订阅，请编辑现有订阅")
	}
	if old.ID == "" && count >= 500 {
		return old, bad("SUBSCRIPTION_LIMIT", "订阅数量已达500个上限")
	}
	s := Subscription{ID: key, TargetScope: in.TargetScope, StartEpisode: in.StartEpisode, Media: m, Season: in.Season, Episodes: episodes, Excluded: excluded, EpisodeDates: in.EpisodeDates, AllowEarly: early, Policy: in.Policy, Schedule: schedule, Mappings: in.Mappings, DownloaderID: in.DownloaderID, RootID: destination.Root, DirectoryID: destination.Rule.ID, DownloadPath: destination.Path, State: state, Revision: old.Revision + 1, CreatedAt: old.CreatedAt}
	s.SiteIDs, s.TotalEpisodes = append([]string{}, in.SiteIDs...), in.TotalEpisodes
	s.Keyword = in.Keyword
	if in.DownloadPath == "" && in.DirectoryID == "" && destination.Rule.ID == "" && destination.Root != "" {
		s.DownloadPath = ""
	}
	if s.CreatedAt == "" {
		s.CreatedAt = now()
	}
	if s.EpisodeDates == nil {
		s.EpisodeDates = map[int]string{}
	}
	if s.Mappings == nil {
		s.Mappings = []media.MappingRule{}
	}
	s.LastCheck = old.LastCheck
	if state == "active" && schedule.Mode != "manual" {
		s.NextCheck = now()
		s.LastReason = "已安排检查；窗口外仍会补漏旧缺集"
	} else {
		s.LastReason = "已暂停自动检查"
		if state == "completed" {
			s.LastReason = "电影已入库，订阅完成"
		}
		if schedule.Mode == "manual" {
			s.LastReason = "仅手动检查"
		}
	}
	if e = txPut(ctx, tx, "subscription", key, s); e != nil {
		return s, e
	}
	if _, e = tx.ExecContext(ctx, `UPDATE jobs SET state='cancelled',generation=generation+1,lease_until=0,updated_at=? WHERE kind='subscription' AND json_extract(payload,'$.id')=? AND state IN ('queued','running','retry_wait','waiting_paused')`, now(), key); e != nil {
		return s, e
	}
	if e = tx.Commit(); e != nil {
		return s, e
	}
	a.log("INFO", "subscriptions", "saved", "订阅与更新周期已保存", "", "")
	return s, nil
}
func (a *App) cancelSubscription(ctx context.Context, key string) error {
	a.subMu.Lock()
	defer a.subMu.Unlock()
	tx, e := a.Store.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var b []byte
	if e = tx.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind='subscription' AND id=?`, key).Scan(&b); e != nil {
		return e
	}
	var s Subscription
	if e = json.Unmarshal(b, &s); e != nil {
		return e
	}
	s.State = "cancelled"
	s.Revision++
	s.NextCheck = ""
	s.LastReason = "订阅已取消；已在途下载与做种源保留"
	if e = txPut(ctx, tx, "subscription", key, s); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, `UPDATE jobs SET state='cancelled',generation=generation+1,lease_until=0,updated_at=? WHERE kind='subscription' AND json_extract(payload,'$.id')=? AND state IN ('queued','running','retry_wait','waiting_paused')`, now(), key); e != nil {
		return e
	}
	return tx.Commit()
}

type subscriptionCoverage struct {
	Known         []int `json:"known_episodes"`
	Missing       []int `json:"missing_episodes"`
	Library       []int `json:"in_library_episodes"`
	Reserved      []int `json:"reserved_episodes"`
	Excluded      []int `json:"excluded_episodes"`
	Unreleased    []int `json:"unreleased_episodes"`
	UnknownDate   []int `json:"unknown_airdate_episodes"`
	PendingImport []int `json:"pending_import_episodes"`
}

func (a *App) subscriptionCoverage(ctx context.Context, s Subscription, known []int) (subscriptionCoverage, error) {
	known = subscriptionTargetEpisodes(s, known)
	out := subscriptionCoverage{Known: append([]int{}, known...), Missing: []int{}, Library: []int{}, Reserved: []int{}, Excluded: append([]int{}, s.Excluded...), Unreleased: []int{}, UnknownDate: []int{}, PendingImport: []int{}}
	wanted := subscriptionTargetEpisodes(s, s.Episodes)
	if len(s.Episodes) == 0 {
		wanted = known
	}
	if s.Media.MediaType == "movie" {
		wanted = []int{0}
		out.Known = []int{}
	}
	occupied := map[int]bool{}
	reservedSet := map[int]bool{}
	pendingImport := map[int]bool{}
	excludedSet := map[int]bool{}
	for _, query := range []struct {
		sql  string
		dest map[int]bool
	}{{`SELECT episode FROM asset_slots WHERE media_key=? AND season=?`, occupied}} {
		rows, e := a.Store.DB.QueryContext(ctx, query.sql, mediaKey(s.Media), s.Season)
		if e != nil {
			return out, e
		}
		for rows.Next() {
			var ep int
			if e = rows.Scan(&ep); e != nil {
				rows.Close()
				return out, e
			}
			query.dest[ep] = true
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return out, e
		}
	}
	// Reservations are also the ownership boundary for in-flight downloads. A
	// completed task remains reserved until its import commits an asset slot.
	// Read its persisted intent in the same query; viewing subscriptions never
	// polls downloaders or makes one metadata request per episode.
	rows, e := a.Store.DB.QueryContext(ctx, `SELECT r.episode,COALESCE(json_extract(i.payload,'$.state'),'') FROM reservations r LEFT JOIN records i ON i.kind='intent' AND i.id=r.intent_id WHERE r.media_key=? AND r.season=?`, mediaKey(s.Media), s.Season)
	if e != nil {
		return out, e
	}
	for rows.Next() {
		var ep int
		var state string
		if e = rows.Scan(&ep, &state); e != nil {
			rows.Close()
			return out, e
		}
		reservedSet[ep] = true
		pendingImport[ep] = state == "downloaded"
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return out, e
	}
	prefix := fmt.Sprintf("%s:%d:", mediaKey(s.Media), s.Season)
	rows, e = a.Store.DB.QueryContext(ctx, `SELECT id,payload FROM records WHERE kind='exclusion' AND id LIKE ?`, prefix+"%")
	if e != nil {
		return out, e
	}
	for rows.Next() {
		var key string
		var b []byte
		if e = rows.Scan(&key, &b); e != nil {
			rows.Close()
			return out, e
		}
		var rule struct {
			Blocked bool `json:"blocked"`
		}
		if e = json.Unmarshal(b, &rule); e != nil {
			rows.Close()
			return out, e
		}
		ep, parseError := strconv.Atoi(strings.TrimPrefix(key, prefix))
		if parseError == nil && rule.Blocked {
			excludedSet[ep] = true
		}
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return out, e
	}
	dateByEpisode := map[int]string{}
	if s.Media.MediaType == "tv" {
		var cache struct {
			Data catalog.Season `json:"data"`
		}
		_ = a.Store.Get(ctx, "cache", fmt.Sprintf("season:%d:%d", s.Media.ID, s.Season), &cache)
		for _, ep := range cache.Data.Episodes {
			dateByEpisode[ep.EpisodeNumber] = ep.AirDate
		}
	}
	today := time.Now().In(shanghai()).Format("2006-01-02")
	for _, ep := range wanted {
		if containsInt(s.Excluded, ep) {
			continue
		}
		if occupied[ep] {
			out.Library = append(out.Library, ep)
		} else if reservedSet[ep] {
			out.Reserved = append(out.Reserved, ep)
			if pendingImport[ep] {
				out.PendingImport = append(out.PendingImport, ep)
			}
		} else {
			if excludedSet[ep] {
				out.Excluded = append(out.Excluded, ep)
				continue
			}
			out.Missing = append(out.Missing, ep)
			// Missing retains its physical-absence meaning for callers. These
			// subsets explain why an absent known episode is not yet available;
			// AllowEarly affects acquisition permission, not its actual air date.
			if s.Media.MediaType == "tv" {
				date, _ := effectiveEpisodeDate(s, ep, dateByEpisode[ep])
				if _, e := parseDay(date, shanghai()); e != nil {
					out.UnknownDate = append(out.UnknownDate, ep)
				} else if date > today {
					out.Unreleased = append(out.Unreleased, ep)
				}
			}
		}
	}
	return out, nil
}
func (a *App) cachedSeasonEpisodes(ctx context.Context, s Subscription) []int {
	var cache struct {
		Data catalog.Season `json:"data"`
	}
	if a.Store.Get(ctx, "cache", fmt.Sprintf("season:%d:%d", s.Media.ID, s.Season), &cache) != nil {
		return []int{}
	}
	out := []int{}
	for _, ep := range cache.Data.Episodes {
		out = append(out, ep.EpisodeNumber)
	}
	sort.Ints(out)
	return out
}
func (a *App) subscriptionList(ctx context.Context) (any, error) {
	raw, e := a.Store.List(ctx, "subscription", 500, 0)
	if e != nil {
		return nil, e
	}
	items := []map[string]any{}
	for _, b := range raw {
		var s Subscription
		if e = json.Unmarshal(b, &s); e != nil {
			return nil, e
		}
		coverage, e := a.subscriptionCoverage(ctx, s, a.cachedSeasonEpisodes(ctx, s))
		if e != nil {
			return nil, e
		}
		entry := map[string]any{}
		_ = json.Unmarshal(b, &entry)
		cb, _ := json.Marshal(coverage)
		_ = json.Unmarshal(cb, &entry)
		items = append(items, entry)
	}
	return map[string]any{"items": items, "total": len(items)}, nil
}
func (a *App) recordSubscriptionCheck(ctx context.Context, s Subscription, reason string, metadataDates []string, deferUntil time.Time) error {
	a.subMu.Lock()
	defer a.subMu.Unlock()
	var current Subscription
	if e := a.Store.Get(ctx, "subscription", s.ID, &current); e != nil {
		return e
	}
	if current.Revision != s.Revision || current.State != "active" {
		return nil
	}
	current.LastCheck = now()
	current.LastReason = reason
	next := nextScheduleCheck(current.Schedule, time.Now(), metadataDates)
	if !deferUntil.IsZero() && (next.IsZero() || deferUntil.After(next)) {
		next = deferUntil
	}
	current.NextCheck = ""
	if !next.IsZero() {
		current.NextCheck = next.UTC().Format(time.RFC3339Nano)
	}
	return a.Store.Put(ctx, "subscription", s.ID, current)
}

func (a *App) completeMovieSubscription(ctx context.Context, key string, revision int) error {
	a.subMu.Lock()
	defer a.subMu.Unlock()
	var current Subscription
	if e := a.Store.Get(ctx, "subscription", key, &current); e != nil {
		return e
	}
	if current.Revision != revision || current.State != "active" || current.Media.MediaType != "movie" {
		return nil
	}
	current.State = "completed"
	current.LastCheck = now()
	current.NextCheck = ""
	current.LastReason = "电影已入库，订阅完成"
	return a.Store.Put(ctx, "subscription", key, current)
}
func (a *App) quotaReset(ctx context.Context, siteIDs []string) time.Time {
	var earliest int64
	for _, site := range siteIDs {
		var oldest sql.NullInt64
		if a.Store.DB.QueryRowContext(ctx, `SELECT MIN(ts) FROM site_usage WHERE site_id=? AND ts>?`, site, time.Now().Add(-24*time.Hour).Unix()).Scan(&oldest) == nil && oldest.Valid {
			value := oldest.Int64 + 24*3600 + 2
			if earliest == 0 || value < earliest {
				earliest = value
			}
		}
	}
	if earliest == 0 {
		return time.Now().Add(time.Hour)
	}
	return time.Unix(earliest, 0)
}

func (a *App) checkSubscription(ctx context.Context, j Job, key string, revision int) error {
	if e := a.guardSubscription(ctx, key, revision); e != nil {
		return e
	}
	if e := a.jobCancelled(ctx, j); e != nil {
		return e
	}
	ctx = subscriptionContext(ctx, key, revision)
	var s Subscription
	if e := a.Store.Get(ctx, "subscription", key, &s); e != nil {
		return e
	}
	known := []int{}
	available := map[int]bool{}
	dates := map[int]string{}
	metadataDates := []string{}
	if s.Media.MediaType == "tv" {
		if e := a.guardSubscriptionContext(ctx); e != nil {
			return e
		}
		season, e := a.season(ctx, s.Media.ID, s.Season)
		if e != nil {
			_ = a.recordSubscriptionCheck(ctx, s, "元数据暂不可用，下次继续检查", nil, time.Time{})
			return e
		}
		today := time.Now().In(shanghai()).Format("2006-01-02")
		for _, ep := range season.Episodes {
			known = append(known, ep.EpisodeNumber)
			if ep.EpisodeNumber < subscriptionStartEpisode(s) || s.TotalEpisodes > 0 && ep.EpisodeNumber > s.TotalEpisodes {
				continue
			}
			date, skip := effectiveEpisodeDate(s, ep.EpisodeNumber, ep.AirDate)
			if skip {
				continue
			}
			dates[ep.EpisodeNumber] = date
			if date != "" {
				metadataDates = append(metadataDates, date)
			}
			if (date != "" && date <= today) || containsInt(s.AllowEarly, ep.EpisodeNumber) {
				available[ep.EpisodeNumber] = true
			}
		}
	}
	coverage, e := a.subscriptionCoverage(ctx, s, known)
	if e != nil {
		return e
	}
	missing := []int{}
	for _, ep := range coverage.Missing {
		if s.Media.MediaType == "movie" || available[ep] {
			missing = append(missing, ep)
		}
	}
	if len(missing) == 0 {
		if s.Media.MediaType == "movie" && len(coverage.Library) > 0 {
			return a.completeMovieSubscription(ctx, s.ID, s.Revision)
		}
		reason := "当前没有已播缺集；继续等待元数据和更新窗口"
		if len(coverage.Library) > 0 && len(coverage.Missing) == 0 {
			reason = "目标已入库，不会重复获取"
		}
		if len(coverage.Reserved) > 0 {
			reason = "缺集已有在途下载，保留占位并等待完成"
		}
		return a.recordSubscriptionCheck(ctx, s, reason, metadataDates, time.Time{})
	}
	if s.Media.MediaType == "movie" && s.Media.ReleaseDate != "" && s.Media.ReleaseDate > time.Now().In(shanghai()).Format("2006-01-02") {
		return a.recordSubscriptionCheck(ctx, s, "电影尚未到上映日期，保持订阅", metadataDates, time.Time{})
	}
	if _, e := a.downloader(s.DownloaderID); e != nil {
		return e
	}
	settings := a.Settings()
	selectedSiteIDs := subscriptionSiteControls(settings.RSSSites, s.SiteIDs)
	enabledSites := 0
	for _, site := range settings.Sites {
		if site.Enabled && (len(selectedSiteIDs) == 0 || slices.Contains(selectedSiteIDs, site.ID)) {
			enabledSites++
		}
	}
	if enabledSites == 0 {
		return a.recordSubscriptionCheck(ctx, s, "订阅选择的站点尚未启用，请在站点管理中启用站点", metadataDates, time.Time{})
	}
	quotaSites := []string{}
	created := 0
	scheduled := map[int]bool{}
	checkedPacks := map[string]bool{}
	reason := "暂未找到符合筛选条件的资源，下次继续补漏"
	for _, ep := range missing {
		if scheduled[ep] {
			continue
		}
		if created >= 4 {
			reason = "本轮已安排4个下载，剩余缺集下轮继续"
			break
		}
		episodes := []int{}
		if s.Media.MediaType == "tv" {
			episodes = []int{ep}
		}
		for _, site := range a.Settings().Sites {
			if created >= 4 {
				break
			}
			if !site.Enabled || len(selectedSiteIDs) > 0 && !slices.Contains(selectedSiteIDs, site.ID) {
				continue
			}
			if e := a.guardSubscriptionContext(ctx); e != nil {
				return e
			}
			if e := a.jobCancelled(ctx, j); e != nil {
				return e
			}
			request := SearchRequest{Query: s.Keyword, MediaType: s.Media.MediaType, TMDBID: s.Media.ID, Season: s.Season, Episodes: episodes, Policy: s.Policy, SiteIDs: []string{site.ID}, Mappings: s.Mappings, AllowEarly: s.AllowEarly, EpisodeDates: dates}
			result, e := a.search(ctx, request, true)
			if e != nil {
				reason = "站点检查失败，详见任务日志"
				a.event(ctx, j, "WARN", "subscriptions", "search_failed", e.Error())
				continue
			}
			for _, status := range result.Sites {
				if status.Status == "failed" {
					reason = "站点访问失败，详见任务日志，下次继续补漏"
				}
				if strings.Contains(status.Message, "预算") {
					quotaSites = append(quotaSites, status.ID)
					reason = "站点调用预算不足，已按最早恢复时间延期检查"
				}
			}
			acquired := false
			for _, candidate := range result.Candidates {
				pack := s.Media.MediaType == "tv" && automaticPackCandidate(candidate, s.Season)
				if (!candidate.Accepted || candidate.NeedsConfirmation) && !pack {
					continue
				}
				if created >= 4 {
					break
				}
				previewRequest := request
				if pack {
					packKey := site.ID + ":" + candidate.Resource.ID
					if checkedPacks[packKey] {
						continue
					}
					checkedPacks[packKey] = true
					previewRequest.Episodes = []int{}
					for _, wanted := range missing {
						if !scheduled[wanted] {
							previewRequest.Episodes = append(previewRequest.Episodes, wanted)
						}
					}
				}
				if e := a.guardSubscriptionContext(ctx); e != nil {
					return e
				}
				if e := a.jobCancelled(ctx, j); e != nil {
					return e
				}
				preview, e := a.previewDownload(ctx, PreviewRequest{SearchRequest: previewRequest, automaticSeasonPack: pack, SiteID: site.ID, ResourceID: candidate.Resource.ID, DownloaderID: s.DownloaderID, RootID: subscriptionLegacyRoot(s), DirectoryID: s.DirectoryID, DownloadPath: s.DownloadPath, SubscriptionID: s.ID, Revision: s.Revision}, true)
				if e != nil {
					var api *APIError
					if errors.As(e, &api) && api.Code == "SITE_QUOTA" {
						quotaSites = append(quotaSites, site.ID)
					}
					a.event(ctx, j, "WARN", "subscriptions", "candidate_rejected", e.Error())
					if pack {
						reason = "整季包包含起始集、在库或未知范围，需要人工核对；继续寻找符合条件的资源"
					}
					continue
				}
				if e := a.guardSubscriptionContext(ctx); e != nil {
					return e
				}
				if e := a.jobCancelled(ctx, j); e != nil {
					return e
				}
				if _, e = a.confirmDownload(ctx, preview.ID, false); e != nil {
					var api *APIError
					if errors.As(e, &api) && api.Code == "ALREADY_ACQUIRED" {
						acquired = true
						break
					}
					a.event(ctx, j, "WARN", "subscriptions", "confirm_failed", e.Error())
					continue
				}
				created++
				for _, covered := range preview.Episodes {
					scheduled[covered] = true
				}
				acquired = s.Media.MediaType == "movie" || scheduled[ep]
				reason = "已安排 " + strconv.Itoa(created) + " 个下载；在途目标不会重复获取"
				if acquired {
					break
				}
			}
			if acquired {
				break
			}
		}
	}
	deferUntil := time.Time{}
	if len(quotaSites) > 0 {
		deferUntil = a.quotaReset(ctx, quotaSites)
		if !strings.Contains(reason, "预算") {
			reason += "；部分站点预算不足，检查将按恢复时间延期"
		}
	}
	return a.recordSubscriptionCheck(ctx, s, reason, metadataDates, deferUntil)
}
