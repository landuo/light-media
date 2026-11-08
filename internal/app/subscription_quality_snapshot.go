package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"sort"

	"light-media/internal/sites"
	"light-media/internal/subscriptionquality"
)

type qualityReader interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}
type qualityAssetSnapshot struct {
	Asset    Asset       `json:"asset"`
	Video    managedFile `json:"video"`
	RootPath string      `json:"root_path"`
}
type qualityDownloadSnapshot struct {
	AllowWholeExtra                                              bool `json:"allow_whole_extra,omitempty"`
	Version                                                      int  `json:"version"`
	Kind, ID, Hash, MediaKey, SubscriptionID, SiteID, ResourceID string
	Revision, Season                                             int
	Episodes                                                     []int
	BestVersion                                                  bool
	RulesSHA, SourceContextSHA                                   string
	Policy                                                       Policy
	Resource                                                     subscriptionquality.Resource
	Slots                                                        map[int][]string
	Assets                                                       []qualityAssetSnapshot
}

func qualityRecord(ctx context.Context, q qualityReader, kind, key string, value any) error {
	var raw []byte
	if e := q.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind=? AND id=?`, kind, key).Scan(&raw); e != nil {
		return e
	}
	return json.Unmarshal(raw, value)
}
func qualityRuleSHA(settings Settings) string {
	return tokenHash(string(mustCleanSeedJSON(settings.PriorityRules)))
}
func (a *App) subscriptionResourcePriority(ctx context.Context, s Subscription, resource sites.Resource, providerModel ...json.RawMessage) (int, error) {
	in := SearchRequest{MediaType: s.Media.MediaType, TMDBID: s.Media.ID, Season: s.Season, Policy: s.Policy, prioritySubscription: true, priorityMedia: &s.Media}
	if providerNativeMedia(s.Media) {
		if len(providerModel) != 1 || len(providerModel[0]) == 0 {
			return 0, acquisitionEvidence()
		}
		info := sourceMediaInformation{MediaInfo: append(json.RawMessage(nil), providerModel[0]...)}
		var err error
		info.Media, err = subscriptionProviderMedia(info, s.Media.ProviderSource, s.Media.ProviderID, s.Media.MediaType)
		if err != nil {
			return 0, err
		}
		in.originalSearchInfo = &info
	}
	filter, e := a.preparePriorityFilter(ctx, in, a.Settings())
	if e != nil {
		return 0, e
	}
	out, e := filter.apply(ctx, a, SearchOutput{Candidates: []Candidate{{Resource: resource, Accepted: true}}})
	if e != nil {
		return 0, e
	}
	if len(out.Candidates) != 1 {
		return 0, bad("QUALITY_RESOURCE_REJECTED", "资源不符合订阅优先级规则")
	}
	return out.Candidates[0].Resource.PriorityOrder, nil
}
func qualitySlots(ctx context.Context, q qualityReader, key string, season int, episodes []int) (map[int][]string, error) {
	out := map[int][]string{}
	for _, episode := range importEpisodes(episodes) {
		out[episode] = []string{}
		rows, e := q.QueryContext(ctx, `SELECT asset_id FROM asset_slots WHERE media_key=? AND season=? AND episode=? ORDER BY asset_id`, key, season, episode)
		if e != nil {
			return nil, e
		}
		for rows.Next() {
			var id string
			if e = rows.Scan(&id); e != nil {
				rows.Close()
				return nil, e
			}
			out[episode] = append(out[episode], id)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return nil, e
		}
	}
	return out, nil
}
func (a *App) freezeQualityAssets(ctx context.Context, p *qualityDownloadSnapshot) error {
	var e error
	p.Slots, e = qualitySlots(ctx, a.Store.DB, p.MediaKey, p.Season, p.Episodes)
	if e != nil {
		return e
	}
	seen := map[string]bool{}
	ids := []string{}
	for _, values := range p.Slots {
		for _, id := range values {
			if !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
	}
	sort.Strings(ids)
	for _, id := range ids {
		var frozen qualityAssetSnapshot
		if e = qualityRecord(ctx, a.Store.DB, "asset", id, &frozen.Asset); e != nil {
			return e
		}
		asset := frozen.Asset
		if asset.State != "present" || mediaKey(asset.Media) != p.MediaKey || asset.Season != p.Season {
			return bad("QUALITY_ASSET_CHANGED", "库内资源缺少可替换的归属记录")
		}
		root, e := a.importRoot(asset.RootID)
		if e != nil || !within(root.Path, asset.Path) {
			return bad("QUALITY_ASSET_CHANGED", "原资源不在受管媒体库目录中")
		}
		frozen.RootPath = root.Path
		if e = qualityRecord(ctx, a.Store.DB, "managed_file", tokenHash(asset.Path), &frozen.Video); e != nil {
			return e
		}
		if frozen.Video.AssetID != asset.ID || frozen.Video.Kind != "video" || frozen.Video.RootID != root.ID || frozen.Video.MediaKey != p.MediaKey {
			return bad("QUALITY_ASSET_CHANGED", "原视频文件归属无法核对")
		}
		stamp, e := snapshotPath(asset.Path)
		if e != nil || stamp != frozen.Video.Snapshot {
			return bad("QUALITY_ASSET_CHANGED", "原视频文件已被外部修改")
		}
		p.Assets = append(p.Assets, frozen)
	}
	return nil
}
func qualityWholeScope(state subscriptionquality.State, resource subscriptionquality.Resource, acquired []int, extra bool) bool {
	if !state.BestVersion || state.Type != subscriptionquality.TV {
		return !extra
	}
	selection := state.Select(resource)
	if !selection.Accepted {
		return false
	}
	if extra {
		if len(resource.SelectedEpisodes) > 0 || !resource.ConfirmedFullCoverage || !state.FullSeason(resource) || !state.Full() && !selection.PreferFullPack {
			return false
		}
		for _, ep := range state.Targets() {
			if !slices.Contains(acquired, ep) {
				return false
			}
		}
		return true
	}
	for _, ep := range acquired {
		if !slices.Contains(state.Targets(), ep) {
			return false
		}
	}
	return state.Full() || reflect.DeepEqual(selection.AllowedEpisodes, acquired)
}
func (a *App) prepareQualityPreview(ctx context.Context, preview DownloadPreview, resource sites.Resource, providerModel ...json.RawMessage) (*qualityDownloadSnapshot, error) {
	if preview.SubscriptionID == "" {
		return nil, nil
	}
	var s Subscription
	if e := qualityRecord(ctx, a.Store.DB, "subscription", preview.SubscriptionID, &s); e != nil {
		return nil, e
	}
	if s.Revision != preview.Revision || !a.subscriptionSearchState(ctx, s) || mediaKey(s.Media) != mediaKey(preview.Media) || s.Season != preview.Season {
		return nil, bad("SUBSCRIPTION_CHANGED", "订阅在资源预览前已变化")
	}
	state, e := a.subscriptionQualitySourceState(ctx, s)
	if e != nil {
		return nil, e
	}
	priority, e := a.subscriptionResourcePriority(ctx, s, resource, providerModel...)
	if e != nil {
		return nil, e
	}
	extra := false
	if grant, ok := ctx.Value(subscriptionAcquisitionKey{}).(*subscriptionAcquisitionGrant); ok && grant != nil {
		if grant.a != a || grant.root == nil || grant.ordinal < 0 || grant.ordinal >= len(grant.root.Steps) {
			return nil, acquisitionEvidence()
		}
		input := grant.root.Steps[grant.ordinal].Input
		if input.AllowWholeExtra {
			if !reflect.DeepEqual(input.Episodes, preview.Episodes) || input.Selected != nil || preview.SelectedEpisodes != nil {
				return nil, acquisitionEvidence()
			}
			extra = true
		}
	}
	full := s.Media.MediaType == "tv" && len(preview.Episodes) > 0 && reflect.DeepEqual(preview.Episodes, state.Targets())
	if extra {
		full = len(state.Targets()) > 0
		for _, ep := range state.Targets() {
			full = full && slices.Contains(preview.Episodes, ep)
		}
	}
	r := subscriptionquality.Resource{Type: state.Type, Priority: priority, Episodes: qualityCopyNote(preview.Episodes), SelectedEpisodes: qualityCopyNote(preview.SelectedEpisodes), ConfirmedFullCoverage: full}
	if s.Media.MediaType == "tv" {
		r.Seasons = []int{s.Season}
	}
	selection := state.Select(r)
	if !selection.Accepted {
		return nil, bad("QUALITY_NOT_HIGHER", "资源优先级未提升订阅目标")
	}
	if s.BestVersion && s.Media.MediaType == "tv" && !qualityWholeScope(state, r, preview.Episodes, extra) {
		return nil, bad("QUALITY_SCOPE_CHANGED", "部分目标集已达到更高优先级，请重新检查")
	}
	p := &qualityDownloadSnapshot{AllowWholeExtra: extra, Version: 1, Kind: "download_preview", ID: preview.ID, Hash: preview.Hash, MediaKey: mediaKey(preview.Media), Season: preview.Season, Episodes: qualityCopyNote(preview.Episodes), SubscriptionID: s.ID, Revision: s.Revision, BestVersion: s.BestVersion, RulesSHA: qualityRuleSHA(a.Settings()), SourceContextSHA: s.SourceContextSHA, SiteID: preview.SiteID, ResourceID: preview.ResourceID, Resource: r, Policy: preview.Policy, Assets: []qualityAssetSnapshot{}}
	if e = a.freezeQualityAssets(ctx, p); e != nil {
		return nil, e
	}
	return p, nil
}
func (a *App) saveQualitySnapshotTx(ctx context.Context, tx *sql.Tx, p *qualityDownloadSnapshot) error {
	if p == nil {
		return nil
	}
	key := p.Kind + ":" + p.ID
	sealed, e := a.pluginStore.seal("subscription_quality_download", "system", key, mustCleanSeedJSON(p))
	if e != nil {
		return e
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO records(kind,id,payload,updated_at) VALUES('subscription_quality_download',?,?,?)`, key, sealed, now())
	return e
}
func (a *App) qualitySnapshot(ctx context.Context, q qualityReader, kind, key, hash string) (*qualityDownloadSnapshot, error) {
	identity := kind + ":" + key
	var sealed []byte
	e := q.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind='subscription_quality_download' AND id=?`, identity).Scan(&sealed)
	if errors.Is(e, sql.ErrNoRows) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	raw, e := a.pluginStore.unseal("subscription_quality_download", "system", identity, sealed)
	if e != nil {
		return nil, e
	}
	var p qualityDownloadSnapshot
	if cleanupJournalStrict(raw, &p) != nil || p.Version != 1 || p.Kind != kind || p.ID != key || !sameHash(p.Hash, hash) || p.SubscriptionID == "" || p.Revision < 1 {
		return nil, bad("QUALITY_SNAPSHOT_CHANGED", "洗版下载快照身份无法核对")
	}
	return &p, nil
}
func (a *App) guardQualityAssets(ctx context.Context, q qualityReader, p *qualityDownloadSnapshot) error {
	slots, e := qualitySlots(ctx, q, p.MediaKey, p.Season, p.Episodes)
	if e != nil {
		return e
	}
	if !reflect.DeepEqual(slots, p.Slots) {
		return bad("QUALITY_ASSET_CHANGED", "原季集归属在预览后已变化")
	}
	for _, frozen := range p.Assets {
		var asset Asset
		if e = qualityRecord(ctx, q, "asset", frozen.Asset.ID, &asset); e != nil || !reorganizeSameAsset(asset, frozen.Asset) {
			return bad("QUALITY_ASSET_CHANGED", "原整理资产已变化")
		}
		root, e := a.importRoot(asset.RootID)
		if e != nil || root.Path != frozen.RootPath {
			return bad("QUALITY_ASSET_CHANGED", "原媒体库路径已变化")
		}
		var video managedFile
		if e = qualityRecord(ctx, q, "managed_file", tokenHash(asset.Path), &video); e != nil || video.AssetID != frozen.Video.AssetID || video.Snapshot != frozen.Video.Snapshot || video.RootID != frozen.Video.RootID {
			return bad("QUALITY_ASSET_CHANGED", "原视频归属已变化")
		}
		stamp, e := snapshotPath(asset.Path)
		if e != nil || stamp != frozen.Video.Snapshot {
			return bad("QUALITY_ASSET_CHANGED", "原视频文件在预览后已变化")
		}
	}
	return nil
}
func (a *App) guardQualityAcquisition(ctx context.Context, q qualityReader, p *qualityDownloadSnapshot) error {
	var s Subscription
	if e := qualityRecord(ctx, q, "subscription", p.SubscriptionID, &s); e != nil {
		return e
	}
	if !a.subscriptionSearchState(ctx, s) || s.Revision != p.Revision || s.BestVersion != p.BestVersion || mediaKey(s.Media) != p.MediaKey || s.Season != p.Season || s.SourceContextSHA != p.SourceContextSHA || qualityRuleSHA(a.Settings()) != p.RulesSHA {
		return bad("QUALITY_SCOPE_CHANGED", "订阅或优先级规则在预览后已变化")
	}
	state := subscriptionQualityState(s)
	selection := state.Select(p.Resource)
	if !selection.Accepted || s.BestVersion && s.Media.MediaType == "tv" && !qualityWholeScope(state, p.Resource, p.Episodes, p.AllowWholeExtra) {
		return bad("QUALITY_NOT_HIGHER", "本资源已不能提升所选目标集")
	}
	return a.guardQualityAssets(ctx, q, p)
}
func (a *App) reserveQualityDownloadTx(ctx context.Context, tx *sql.Tx, preview DownloadPreview, intent Intent) error {
	p, e := a.qualitySnapshot(ctx, tx, "download_preview", preview.ID, preview.Hash)
	if e != nil {
		return e
	}
	if p == nil {
		if preview.SubscriptionID != "" {
			var owner Subscription
			if e = qualityRecord(ctx, tx, "subscription", preview.SubscriptionID, &owner); e != nil {
				return e
			}
			if owner.BestVersion {
				return bad("QUALITY_SNAPSHOT_CHANGED", "洗版预览缺少原始授权快照")
			}
		}
		seasonProof, scopeErr := a.readDownloadSeasonProof(ctx, tx, "download_preview", preview.ID, preview.Hash, preview.SeasonScopeSHA)
		if scopeErr != nil {
			return scopeErr
		}
		if seasonProof != nil {
			return a.confirmDownloadSeasonProofTx(ctx, tx, preview, intent)
		}
		return a.Store.Reserve(ctx, tx, mediaKey(preview.Media), preview.Season, preview.Episodes, intent.ID)
	}
	if p.MediaKey != mediaKey(intent.Media) || p.Season != intent.Season || !reflect.DeepEqual(p.Episodes, intent.Episodes) || p.SubscriptionID != intent.SubscriptionID || p.Revision != intent.SubscriptionRevision || p.SiteID != intent.SiteID || p.ResourceID != intent.ResourceID {
		return bad("QUALITY_SNAPSHOT_CHANGED", "下载意图与原资源快照不符")
	}
	if e = a.guardQualityAcquisition(ctx, tx, p); e != nil {
		return e
	}
	if !p.BestVersion {
		if e = a.Store.Reserve(ctx, tx, p.MediaKey, p.Season, p.Episodes, intent.ID); e != nil {
			return e
		}
	} else {
		for _, episode := range importEpisodes(p.Episodes) {
			if _, e = tx.ExecContext(ctx, `INSERT INTO reservations VALUES(?,?,?,?)`, p.MediaKey, p.Season, episode, intent.ID); e != nil {
				return bad("ALREADY_ACQUIRED", "目标已有在途资源")
			}
		}
	}
	p.Kind, p.ID = "intent", intent.ID
	return a.saveQualitySnapshotTx(ctx, tx, p)
}
