package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"slices"
	"sort"

	"light-media/internal/catalog"
	"light-media/internal/media"
	"light-media/internal/metainfo"
	"light-media/internal/sites"
)

// A multi-season source action still creates one torrent and one Intent. The
// private proof keeps its actual per-file acquisition separate from the legacy
// primary-season fields; callers cannot enlarge it through HTTP JSON.
type downloadSeasonScope struct {
	Season   int
	Episodes []int
}
type downloadScopedFile struct {
	Path     string
	Size     int64
	Season   int
	Episodes []int
}
type downloadSeasonProof struct {
	SourceBasis                                             json.RawMessage `json:",omitempty"`
	Version                                                 int
	Kind, ID, MediaKey, Hash, SiteID, ResourceID, LarkInbox string
	Requested                                               []SearchRequest
	Scopes                                                  []downloadSeasonScope
	Files                                                   []downloadScopedFile
}

func seasonScopeError() error {
	return bad("REVIEW_SEASON_SCOPE", "多季下载的实际文件范围无法核对，请重新选择资源")
}
func seasonScopeSHA(p downloadSeasonProof) string {
	p.Kind, p.ID = "", ""
	return tokenHash(string(mustCleanSeedJSON(p)))
}
func (a *App) saveDownloadSeasonProofTx(ctx context.Context, tx *sql.Tx, p downloadSeasonProof) error {
	key := p.Kind + ":" + p.ID
	raw := mustCleanSeedJSON(p)
	if len(raw) > pluginDataValueLimit-(8<<10) {
		return bad("DOWNLOAD_CONTEXT_TOO_LARGE", "多季下载文件范围超过允许范围，尚未提交下载")
	}
	sealed, e := a.pluginStore.seal("download_season_scope", "system", key, raw)
	if e != nil {
		return e
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO records(kind,id,payload,updated_at) VALUES('download_season_scope',?,?,?)`, key, sealed, now())
	return e
}
func (a *App) readDownloadSeasonProof(ctx context.Context, q qualityReader, kind, key, hash, sha string) (*downloadSeasonProof, error) {
	var sealed []byte
	e := q.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind='download_season_scope' AND id=?`, kind+":"+key).Scan(&sealed)
	if errors.Is(e, sql.ErrNoRows) && sha == "" {
		return nil, nil
	}
	if e != nil {
		return nil, seasonScopeError()
	}
	raw, e := a.pluginStore.unseal("download_season_scope", "system", kind+":"+key, sealed)
	if e != nil {
		return nil, e
	}
	var p downloadSeasonProof
	if cleanupJournalStrict(raw, &p) != nil || p.Version != 1 || p.Kind != kind || p.ID != key || !sameHash(p.Hash, hash) || sha == "" || seasonScopeSHA(p) != sha || len(p.Scopes) < 2 || len(p.Files) == 0 {
		return nil, seasonScopeError()
	}
	if len(p.SourceBasis) > 0 {
		if e = validateDownloadSeasonBasis(p.SourceBasis, p.Requested, p.MediaKey, p.SiteID, p.ResourceID); e != nil {
			return nil, e
		}
	}
	return &p, nil
}
func (a *App) intentSeasonProof(ctx context.Context, q qualityReader, in Intent) (*downloadSeasonProof, error) {
	p, e := a.readDownloadSeasonProof(ctx, q, "intent", in.ID, in.Hash, in.SeasonScopeSHA)
	if e != nil || p == nil {
		return p, e
	}
	if p.LarkInbox == "" || p.LarkInbox != in.LarkInbox || in.Media.MediaType != "tv" || in.SubscriptionID != "" || len(in.SelectedEpisodes) > 0 || p.MediaKey != mediaKey(in.Media) || p.SiteID != in.SiteID || p.ResourceID != in.ResourceID || p.Scopes[0].Season != in.Season || !reflect.DeepEqual(p.Scopes[0].Episodes, in.Episodes) {
		return nil, seasonScopeError()
	}
	return p, nil
}
func (a *App) prepareDownloadSeasonProof(ctx context.Context, p *DownloadPreview, wanted []SearchRequest, m metainfo.Material, r sites.Resource, basis json.RawMessage) (*downloadSeasonProof, error) {
	if len(wanted) == 0 {
		return nil, nil
	}
	if p.Media.MediaType != "tv" || p.SubscriptionID != "" || len(wanted) < 2 || len(p.SelectedEpisodes) > 0 {
		return nil, seasonScopeError()
	}
	core, ok := ctx.Value(pluginLarkCoreKey{}).(*pluginLarkCoreExecution)
	if !ok || core == nil || core.inbox.ID == "" {
		return nil, seasonScopeError()
	}
	if e := validateDownloadSeasonBasis(basis, wanted, mediaKey(p.Media), p.SiteID, p.ResourceID); e != nil {
		return nil, e
	}
	expected := map[int]map[int]bool{}
	known := []media.Episode{}
	for _, request := range wanted {
		if request.MediaType != "tv" || request.TMDBID != p.Media.ID || request.Season < 1 || expected[request.Season] != nil || len(request.Episodes) == 0 {
			return nil, seasonScopeError()
		}
		expected[request.Season] = map[int]bool{}
		season, e := a.season(ctx, p.Media.ID, request.Season)
		if e != nil {
			return nil, e
		}
		for _, ep := range season.Episodes {
			known = append(known, media.Episode{Season: request.Season, Episode: ep.EpisodeNumber})
		}
		for _, ep := range request.Episodes {
			if ep < 1 || expected[request.Season][ep] || !slices.ContainsFunc(season.Episodes, func(e catalog.Episode) bool { return e.EpisodeNumber == ep }) {
				return nil, seasonScopeError()
			}
			expected[request.Season][ep] = true
		}
		if e := a.ensureNotExcluded(ctx, p.Media, request.Season, request.Episodes); e != nil {
			return nil, e
		}
	}
	result := &downloadSeasonProof{SourceBasis: append(json.RawMessage(nil), basis...), Version: 1, Kind: "download_preview", ID: p.ID, MediaKey: mediaKey(p.Media), Hash: m.Hash, SiteID: p.SiteID, ResourceID: p.ResourceID, LarkInbox: core.inbox.ID, Requested: wanted, Scopes: []downloadSeasonScope{}, Files: []downloadScopedFile{}}
	acquired := map[int]map[int]bool{}
	for _, file := range m.Files {
		if !isVideo(file.Path) {
			continue
		}
		parsed := a.parseMediaNameContext(ctx, filepath.Base(file.Path))
		if parsed.Status != media.Parsed || parsed.Original == nil {
			return nil, seasonScopeError()
		}
		resolved := media.Resolve(mediaKey(p.Media), *parsed.Original, media.Source{Site: p.SiteID}, p.Request.Mappings, known, nil)
		if resolved.Status != media.Parsed || len(resolved.Standard) == 0 {
			return nil, seasonScopeError()
		}
		f := downloadScopedFile{Path: file.Path, Size: file.Size, Episodes: []int{}}
		for _, ep := range resolved.Standard {
			if expected[ep.Season] == nil || !expected[ep.Season][ep.Episode] || f.Season != 0 && f.Season != ep.Season {
				return nil, seasonScopeError()
			}
			f.Season = ep.Season
			if acquired[ep.Season] == nil {
				acquired[ep.Season] = map[int]bool{}
			}
			if acquired[ep.Season][ep.Episode] {
				return nil, bad("REVIEW_SEASON_PACK", "多季包同一集包含多个视频版本，请人工核对")
			}
			acquired[ep.Season][ep.Episode] = true
			f.Episodes = append(f.Episodes, ep.Episode)
		}
		target, e := a.target(ctx, SearchRequest{MediaType: "tv", TMDBID: p.Media.ID, Season: f.Season, Episodes: f.Episodes, Mappings: p.Request.Mappings}, false)
		if e != nil {
			return nil, e
		}
		actual := r
		actual.Title = filepath.Base(file.Path)
		actual.Subtitle = ""
		actual.Size = file.Size
		c := a.evaluateResourceContext(ctx, actual, target, decisionPolicy(Policy{}))
		if !c.Accepted || c.NeedsConfirmation {
			return nil, bad("REVIEW_FILE_IDENTITY", "多季包实际视频文件名不能确认目标身份，请手动核对")
		}
		sort.Ints(f.Episodes)
		result.Files = append(result.Files, f)
	}
	seasons := []int{}
	for season := range acquired {
		seasons = append(seasons, season)
	}
	sort.Ints(seasons)
	if len(seasons) < 2 || len(seasons) != len(expected) {
		return nil, seasonScopeError()
	}
	for _, season := range seasons {
		episodes := []int{}
		for ep := range acquired[season] {
			episodes = append(episodes, ep)
		}
		sort.Ints(episodes)
		result.Scopes = append(result.Scopes, downloadSeasonScope{season, episodes})
	}
	p.Season = result.Scopes[0].Season
	p.Episodes = qualityCopyNote(result.Scopes[0].Episodes)
	p.Request.Season, p.Request.Episodes = p.Season, qualityCopyNote(p.Episodes)
	p.SeasonScopeSHA = seasonScopeSHA(*result)
	return result, nil
}
func (a *App) confirmDownloadSeasonProofTx(ctx context.Context, tx *sql.Tx, p DownloadPreview, in Intent) error {
	proof, e := a.readDownloadSeasonProof(ctx, tx, "download_preview", p.ID, p.Hash, p.SeasonScopeSHA)
	if e != nil || proof == nil {
		return e
	}
	if in.LarkInbox == "" || proof.LarkInbox != in.LarkInbox || in.SeasonScopeSHA != p.SeasonScopeSHA || proof.MediaKey != mediaKey(in.Media) || proof.Scopes[0].Season != in.Season || !reflect.DeepEqual(proof.Scopes[0].Episodes, in.Episodes) {
		return seasonScopeError()
	}
	for _, scope := range proof.Scopes {
		if e = a.Store.Reserve(ctx, tx, proof.MediaKey, scope.Season, scope.Episodes, in.ID); e != nil {
			return e
		}
	}
	proof.Kind, proof.ID = "intent", in.ID
	return a.saveDownloadSeasonProofTx(ctx, tx, *proof)
}
func (a *App) guardIntentSeasonExclusions(ctx context.Context, in Intent) error {
	proof, e := a.intentSeasonProof(ctx, a.Store.DB, in)
	if e != nil {
		return e
	}
	if proof == nil {
		return a.ensureNotExcluded(ctx, in.Media, in.Season, in.Episodes)
	}
	for _, scope := range proof.Scopes {
		if e = a.ensureNotExcluded(ctx, in.Media, scope.Season, scope.Episodes); e != nil {
			return e
		}
	}
	return nil
}
func (a *App) guardSeasonMaterial(ctx context.Context, in Intent, m metainfo.Material) error {
	p, e := a.intentSeasonProof(ctx, a.Store.DB, in)
	if e != nil || p == nil {
		return e
	}
	actual := []struct {
		Path string
		Size int64
	}{}
	expected := []struct {
		Path string
		Size int64
	}{}
	for _, f := range m.Files {
		if isVideo(f.Path) {
			actual = append(actual, struct {
				Path string
				Size int64
			}{f.Path, f.Size})
		}
	}
	for _, f := range p.Files {
		expected = append(expected, struct {
			Path string
			Size int64
		}{f.Path, f.Size})
	}
	if !reflect.DeepEqual(actual, expected) {
		return seasonScopeError()
	}
	return nil
}
func seasonScopeForFile(p *downloadSeasonProof, path string) (downloadScopedFile, bool) {
	for _, f := range p.Files {
		if f.Path == path {
			return f, true
		}
	}
	return downloadScopedFile{}, false
}

func (a *App) guardSeasonImportPlan(ctx context.Context, in Intent, plan ImportPlan) error {
	p, e := a.intentSeasonProof(ctx, a.Store.DB, in)
	if e != nil || p == nil {
		return e
	}
	if e = a.guardSourcePostAddReceipt(ctx, in); e != nil {
		return e
	}
	d, e := a.downloader(in.DownloaderID)
	if e != nil {
		return e
	}
	for _, file := range plan.Files {
		matched := false
		for _, scoped := range p.Files {
			full, e := mapDownloaderPath(a.Settings(), d, filepath.Join(in.DownloadPath, filepath.FromSlash(scoped.Path)))
			if e != nil {
				return e
			}
			if file.Source == full && file.Season == scoped.Season && reflect.DeepEqual(file.Episodes, scoped.Episodes) && file.Size == scoped.Size {
				matched = true
				break
			}
		}
		if !matched {
			return seasonScopeError()
		}
	}
	return nil
}
