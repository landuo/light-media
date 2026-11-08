package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"slices"
	"strconv"
	"strings"

	"light-media/internal/sites"
)

const (
	originalChatLatestCacheKind = "subscription_latest_cache"
	originalChatLatestBatchKind = "subscription_latest_batch"
)

type originalChatLatestRow struct {
	Resource sites.Resource  `json:"resource"`
	Source   json.RawMessage `json:"source,omitempty"`
	Media    json.RawMessage `json:"media,omitempty"`
	MetaName string          `json:"meta_name,omitempty"`
	Failures int             `json:"recognize_failures,omitempty"`
}

func (row originalChatLatestRow) resource() sites.Resource {
	resource := row.Resource
	resource.ModuleSourceFields = row.Source
	resource.ModuleMediaFields = row.Media
	return resource
}

type originalChatLatestSite struct {
	rechecked map[string]originalChatLatestRow
	SiteID    string                  `json:"site_id"`
	Key       string                  `json:"key"`
	Rows      []originalChatLatestRow `json:"rows"`
}

type originalChatLatestBatch struct {
	id       string
	Mode     string                   `json:"mode"`
	Sites    []originalChatLatestSite `json:"sites"`
	RSSRoots map[string]string        `json:"rss_roots,omitempty"`
}

type originalChatLatestJob struct {
	ID          string `json:"id"`
	Revision    int    `json:"revision"`
	LatestBatch string `json:"latest_batch"`
	RSSFallback bool   `json:"_chat_rss_fallback,omitempty"`
}

type originalChatLatestKey struct{}

func originalChatLatestRootDigest(job Job) string {
	return tokenHash(job.WorkflowID + "\x00" + string(job.Payload))
}

func (batch *originalChatLatestBatch) ownsRSSRoot(job Job) bool {
	return batch.Mode == "rss" && batch.RSSRoots[job.ID] == originalChatLatestRootDigest(job)
}

// Only an exact root published with a sealed RSS batch owns synchronous
// download attempts. An older latest-resource job remains a normal job.
func (a *App) originalChatRSSOwnsSearch(ctx context.Context, q qualityReader, parent Job, owner Subscription) (bool, error) {
	if len(parent.Payload) == 0 {
		return false, nil
	}
	var input originalChatLatestJob
	if err := json.Unmarshal(parent.Payload, &input); err != nil {
		return false, err
	}
	if !input.RSSFallback {
		return false, nil
	}
	if parent.Kind != "subscription" || parent.ID == "" || input.LatestBatch == "" ||
		input.ID != owner.ID || input.Revision != owner.Revision || !originalChatLatestActive(owner) {
		return false, bad("SUBSCRIPTION_LATEST_INVALID", "RSS 匹配任务与订阅不符")
	}
	batch := originalChatLatestFromContext(ctx)
	if batch == nil || batch.id != input.LatestBatch {
		batch = new(originalChatLatestBatch)
		if err := a.originalChatLatestRead(ctx, q, originalChatLatestBatchKind, input.LatestBatch, batch); err != nil {
			return false, err
		}
	}
	if !batch.ownsRSSRoot(parent) {
		return false, bad("SUBSCRIPTION_LATEST_INVALID", "RSS 匹配任务与已保存批次不符")
	}
	return true, nil
}

func (a *App) originalChatRefreshSubscriptions(ctx context.Context) error {
	subs, err := a.originalChatLatestSubscriptions(ctx)
	if err != nil || len(subs) == 0 {
		return err
	}
	settings := a.Settings()
	selected := map[string]bool{}
	for _, sub := range subs {
		if originalChatLatestActive(sub) {
			for _, id := range subscriptionSiteControls(settings.RSSSites, sub.SiteIDs) {
				selected[id] = true
			}
		}
	}
	if len(selected) == 0 {
		for _, id := range settings.RSSSites {
			selected[id] = true
		}
	}

	mode, ok := os.LookupEnv("SUBSCRIBE_MODE")
	if !ok || mode == "spider" {
		mode = "spider"
	} else {
		mode = "rss"
	}
	var invalidRSS map[string]struct{}
	if mode == "rss" {
		invalidRSS = a.originalChatRSSInvalidSnapshot()
	}
	fetchLimit, keepLimit := 50, 100
	switch strings.ToLower(os.Getenv("BIG_MEMORY_MODE")) {
	case "1", "true", "yes", "on", "t", "y":
		fetchLimit, keepLimit = 100, 200
	}
	noCache, present := os.LookupEnv("NO_CACHE_SITE_KEY")
	if !present {
		noCache = "m-team"
	}
	options, err := a.globalRecognitionOptions(ctx)
	if err != nil {
		return err
	}
	parser, err := originalChatLatestParser(options)
	if err != nil {
		return err
	}
	var snapshots []originalChatLatestSite
	for _, site := range settings.Sites {
		if !site.Enabled || len(selected) != 0 && !selected[site.ID] {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		rows, err := a.originalChatLatestFetch(ctx, site, mode)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			a.log("WARN", "subscriptions", "latest_site_failed", "订阅刷新未能读取一个站点的最新资源", "", "")
		}
		slices.SortStableFunc(rows, func(a, b sites.Resource) int { return strings.Compare(b.PublishedAt, a.PublishedAt) })
		if len(rows) > fetchLimit {
			rows = rows[:fetchLimit]
		}
		snapshot := originalChatLatestSite{SiteID: site.ID, Key: a.originalChatLatestSiteKey(site, settings.ProxyHost)}
		for _, resource := range rows {
			if resource.ID != "" && resource.SiteID == site.ID && resource.Title != "" {
				snapshot.Rows = append(snapshot.Rows, originalChatLatestRow{Resource: resource, Source: resource.ModuleSourceFields, Media: resource.ModuleMediaFields})
			}
		}
		if err := a.originalChatLatestRecognizeSite(ctx, &snapshot, mode, originalChatLatestUncached(site, noCache), parser, invalidRSS); err != nil {
			return err
		}
		snapshots = append(snapshots, snapshot)
	}
	return a.originalChatLatestPublish(ctx, subs, mode, noCache, snapshots, keepLimit, invalidRSS)
}

func (a *App) originalChatLatestSubscriptions(ctx context.Context) ([]Subscription, error) {
	rows, err := a.Store.DB.QueryContext(ctx, `SELECT payload FROM records WHERE kind='subscription'
		AND json_extract(payload,'$.state') IN ('active','paused') ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var subs []Subscription
	for rows.Next() {
		var raw []byte
		var sub Subscription
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(raw, &sub); err != nil {
			return nil, err
		}
		subs = append(subs, sub)
	}
	return subs, rows.Err()
}

func originalChatLatestActive(sub Subscription) bool {
	state := originalChatSubscriptionState(sub)
	return sub.State == "active" && (state == "R" || state == "P")
}

func (a *App) originalChatLatestSiteKey(site SiteConfig, proxy string) string {
	if !site.Proxy {
		proxy = ""
	}
	return a.managedConnectionKey("subscription-latest", a.managedSiteKey(site), site.RSS, proxy, strconv.Itoa(site.Timeout), strconv.FormatBool(site.Render))
}

func (a *App) originalChatLatestFetch(ctx context.Context, site SiteConfig, mode string) ([]sites.Resource, error) {
	if mode == "rss" {
		result, err := a.originalConfiguredSiteRSS(ctx, site, true, func(ctx context.Context) error { return ctx.Err() })
		if err != nil {
			return nil, err
		}
		rows := make([]sites.Resource, 0, len(result.Items))
		for index, row := range result.Items {
			size, _ := row.Size.Int64()
			rows = append(rows, sites.Resource{ID: row.ID, SiteID: row.SiteID, Title: row.Title, Subtitle: row.Subtitle, PublishedAt: row.PublishedAt, Size: size, DownloadFactor: row.DownloadFactor, UploadFactor: row.UploadFactor, DetailsURL: result.detailURLs[index]})
		}
		return rows, nil
	}
	var rows []sites.Resource
	for page := 0; page < 2; page++ {
		result, err := a.originalSiteTorrentModule(ctx, site, "refresh_torrents", map[string]any{"keyword": nil, "cat": nil, "page": page}, page+1, func() (sites.SearchResult, error) {
			var result sites.SearchResult
			err := a.withSite(ctx, site, true, func(client *sites.Client) error {
				var err error
				result, err = client.Search(ctx, "", page+1)
				return err
			})
			return result, err
		})
		if err != nil || len(result.Resources) == 0 {
			return rows, err
		}
		rows = append(rows, result.Resources...)
	}
	return rows, nil
}

func originalChatLatestUncached(site SiteConfig, noCache string) bool {
	domain := siteStatisticDomain(site)
	return slices.ContainsFunc(strings.Split(noCache, ","), func(key string) bool { return strings.Contains(domain, key) })
}

func (a *App) originalChatLatestPublish(ctx context.Context, subs []Subscription, mode, noCache string, snapshots []originalChatLatestSite, keep int, invalidRSS map[string]struct{}) error {
	if len(snapshots) == 0 && len(invalidRSS) == 0 {
		return nil
	}
	settings := a.Settings()
	siteByID := map[string]SiteConfig{}
	for _, site := range settings.Sites {
		siteByID[site.ID] = site
	}
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	batch := originalChatLatestBatch{Mode: mode}
	updated := map[string]bool{}
	for _, site := range snapshots {
		current := siteByID[site.SiteID]
		if !current.Enabled || a.originalChatLatestSiteKey(current, settings.ProxyHost) != site.Key {
			continue
		}
		var previous originalChatLatestSite
		cacheID := mode + ":" + site.SiteID
		if err := a.originalChatLatestRead(ctx, tx, originalChatLatestCacheKind, cacheID, &previous); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if previous.Key != site.Key || previous.SiteID != site.SiteID {
			previous.Rows = nil
		}
		previous.Rows = originalChatLatestPruneInvalidRSS(previous.Rows, invalidRSS)
		if len(site.Rows) > 0 && originalChatLatestUncached(current, noCache) {
			previous.Rows = nil
		}
		oldSignatures := map[string]bool{}
		for i, row := range previous.Rows {
			signature := originalChatLatestSignature(row.Resource)
			oldSignatures[signature] = true
			if upgraded, ok := site.rechecked[signature]; ok && upgraded.Resource.ID == row.Resource.ID &&
				len(row.Media) == 0 && (len(upgraded.Media) != 0 || upgraded.Failures > row.Failures) {
				previous.Rows[i] = upgraded
			}
		}
		for _, row := range site.Rows {
			if !oldSignatures[originalChatLatestSignature(row.Resource)] {
				previous.Rows = append(previous.Rows, row)
			}
		}
		if len(previous.Rows) > keep {
			previous.Rows = previous.Rows[len(previous.Rows)-keep:]
		}
		// One native material ID can acquire only one current torrent. Retain
		// the most recent listing when a tracker changes its title/description.
		seenIDs := map[string]bool{}
		for i := len(previous.Rows) - 1; i >= 0; i-- {
			resourceID := previous.Rows[i].Resource.ID
			if seenIDs[resourceID] {
				previous.Rows = slices.Delete(previous.Rows, i, i+1)
			}
			seenIDs[resourceID] = true
		}
		site.Rows = previous.Rows
		if err := a.originalChatLatestWrite(ctx, tx, originalChatLatestCacheKind, cacheID, site); err != nil {
			return err
		}
		updated[cacheID] = true
		batch.Sites = append(batch.Sites, site)
	}
	if err := a.originalChatLatestPruneOtherRSS(ctx, tx, updated, invalidRSS); err != nil {
		return err
	}
	batchID := id("latest_")
	if mode == "rss" {
		if err := pruneOriginalChatLatestBatches(ctx, tx); err != nil {
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		return a.originalChatRSSMatchSubscriptions(ctx, subs, batchID, batch)
	}
	var queued []string
	var conflict error
	for _, sub := range subs {
		if !originalChatLatestActive(sub) || len(batch.Sites) == 0 {
			continue
		}
		job, err := enqueueTx(ctx, tx, "subscription", originalChatLatestJob{ID: sub.ID, Revision: sub.Revision, LatestBatch: batchID}, "")
		if err != nil {
			var api *APIError
			if errors.As(err, &api) && api.Code == "JOB_EXISTS" {
				conflict = err
				continue
			}
			return err
		}
		queued = append(queued, job.ID)
	}
	if len(queued) != 0 {
		if err := a.originalChatLatestWrite(ctx, tx, originalChatLatestBatchKind, batchID, batch); err != nil {
			return err
		}
	}
	if err := pruneOriginalChatLatestBatches(ctx, tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if len(queued) != 0 {
		select {
		case a.jobWake <- struct{}{}:
		default:
		}
	}
	for _, id := range queued {
		state, err := a.awaitOriginalChatSubscriptionJob(ctx, id)
		if err != nil {
			return err
		}
		if state != "succeeded" {
			return bad("SUBSCRIPTION_LATEST_FAILED", "订阅最新资源匹配未完成")
		}
		if err := a.originalChatLatestAwaitDownloads(ctx, id); err != nil {
			return err
		}
	}
	return conflict
}

func pruneOriginalChatLatestBatches(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `DELETE FROM records WHERE kind=? AND NOT EXISTS(
		SELECT 1 FROM jobs WHERE jobs.kind='subscription' AND state IN ('queued','running','retry_wait','waiting_paused')
		AND json_extract(jobs.payload,'$.latest_batch')=records.id)`, originalChatLatestBatchKind)
	return err
}

func (a *App) originalChatRSSMatchSubscriptions(ctx context.Context, subs []Subscription, batchID string, batch originalChatLatestBatch) error {
	if len(batch.Sites) == 0 {
		return nil
	}
	for _, sub := range subs {
		if !originalChatLatestActive(sub) {
			continue
		}
		job, err := a.enqueueOriginalChatRSSMatch(ctx, sub, batchID, batch)
		if err != nil {
			return err
		}
		job, err = a.awaitOriginalChatSearchChild(ctx, job)
		if err != nil {
			return err
		}
		if job.State != "succeeded" {
			return bad("SUBSCRIPTION_LATEST_FAILED", "订阅最新资源匹配未完成")
		}
		if err := a.settleOriginalChatSubscriptionWork(ctx, sub, job.ID, true); err != nil {
			return err
		}
	}
	return nil
}

func (a *App) enqueueOriginalChatRSSMatch(ctx context.Context, sub Subscription, batchID string, batch originalChatLatestBatch) (Job, error) {
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return Job{}, err
	}
	defer tx.Rollback()
	job, err := enqueueTx(ctx, tx, "subscription", originalChatLatestJob{ID: sub.ID, Revision: sub.Revision, LatestBatch: batchID, RSSFallback: true}, id("wf_"))
	if err != nil {
		return Job{}, err
	}
	// A completed root is no longer eligible. Re-publish the same feed
	// snapshot atomically with the next root, even if another refresh pruned it.
	batch.RSSRoots = map[string]string{job.ID: originalChatLatestRootDigest(job)}
	if err := a.originalChatLatestWrite(ctx, tx, originalChatLatestBatchKind, batchID, batch); err != nil {
		return Job{}, err
	}
	if err := tx.Commit(); err != nil {
		return Job{}, err
	}
	select {
	case a.jobWake <- struct{}{}:
	default:
	}
	return job, nil
}

func originalChatLatestPruneInvalidRSS(rows []originalChatLatestRow, invalid map[string]struct{}) []originalChatLatestRow {
	if len(invalid) == 0 {
		return rows
	}
	return slices.DeleteFunc(rows, func(row originalChatLatestRow) bool {
		_, marked := invalid[row.Resource.ID]
		return marked
	})
}

// Source prunes its whole cache before selecting refresh sites. Selected rows
// were handled before deduplication, and only those rows enter the job batch.
func (a *App) originalChatLatestPruneOtherRSS(ctx context.Context, tx *sql.Tx, updated map[string]bool, invalid map[string]struct{}) error {
	if len(invalid) == 0 {
		return nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT id FROM records WHERE kind=? AND id GLOB 'rss:*'`, originalChatLatestCacheKind)
	if err != nil {
		return err
	}
	var keys []string
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			rows.Close()
			return err
		}
		if !updated[key] {
			keys = append(keys, key)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, key := range keys {
		var cached originalChatLatestSite
		if err := a.originalChatLatestRead(ctx, tx, originalChatLatestCacheKind, key, &cached); err != nil {
			return err
		}
		count := len(cached.Rows)
		cached.Rows = originalChatLatestPruneInvalidRSS(cached.Rows, invalid)
		if len(cached.Rows) != count {
			if err := a.originalChatLatestWrite(ctx, tx, originalChatLatestCacheKind, key, cached); err != nil {
				return err
			}
		}
	}
	return nil
}

func (a *App) originalChatLatestAwaitDownloads(ctx context.Context, parentID string) error {
	rows, err := a.Store.DB.QueryContext(ctx, `SELECT id,json_extract(payload,'$.id') FROM jobs
		WHERE kind IN ('download','subscription_download') AND json_extract(payload,'$._parent_job_id')=? ORDER BY created_at,id`, parentID)
	if err != nil {
		return err
	}
	type child struct{ job, intent string }
	var children []child
	for rows.Next() {
		var c child
		if err := rows.Scan(&c.job, &c.intent); err != nil {
			rows.Close()
			return err
		}
		children = append(children, c)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, c := range children {
		state, err := a.awaitOriginalChatSubscriptionJob(ctx, c.job)
		if err != nil {
			return err
		}
		if state != "succeeded" {
			return bad("SUBSCRIPTION_LATEST_FAILED", "订阅最新资源下载未完成")
		}
		var intent Intent
		if err := a.Store.Get(ctx, "intent", c.intent, &intent); err != nil {
			return err
		}
		switch intent.State {
		case "accepted", "downloading", "downloaded", "imported":
		default:
			return bad("SUBSCRIPTION_LATEST_FAILED", "下载器尚未确认接收订阅最新资源")
		}
	}
	return nil
}

func originalChatLatestSignature(resource sites.Resource) string {
	if resource.SourceSubtitle != nil {
		return resource.Title + *resource.SourceSubtitle
	}
	if resource.Subtitle != "" {
		return resource.Title + resource.Subtitle
	}
	return resource.Title + "None"
}

func (a *App) originalChatLatestWrite(ctx context.Context, tx *sql.Tx, kind, key string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	sealed, err := a.pluginStore.seal(kind, "system", key, raw)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO records(kind,id,payload,updated_at) VALUES(?,?,?,?)
		ON CONFLICT(kind,id) DO UPDATE SET payload=excluded.payload,updated_at=excluded.updated_at`, kind, key, sealed, now())
	return err
}

func (a *App) originalChatLatestRead(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, kind, key string, output any) error {
	var sealed []byte
	if err := q.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind=? AND id=?`, kind, key).Scan(&sealed); err != nil {
		return err
	}
	raw, err := a.pluginStore.unseal(kind, "system", key, sealed)
	if err != nil || json.Unmarshal(raw, output) != nil {
		return bad("SUBSCRIPTION_LATEST_INVALID", "最新资源快照无法读取")
	}
	return nil
}
