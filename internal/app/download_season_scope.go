package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"slices"
	"strings"

	"light-media/internal/catalog"
	"light-media/internal/decision"
	"light-media/internal/download"
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
	Magnet                                                  bool                        `json:",omitempty"`
	SourceBasis                                             json.RawMessage             `json:",omitempty"`
	ChatOrigin                                              *originalChatDownloadOrigin `json:",omitempty"`
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
	if cleanupJournalStrict(raw, &p) != nil || p.Version != 1 || p.Kind != kind || p.ID != key || !sameHash(p.Hash, hash) || sha == "" || seasonScopeSHA(p) != sha || len(p.Scopes) < 2 || p.Magnet != (len(p.Files) == 0) {
		return nil, seasonScopeError()
	}
	if len(p.SourceBasis) > 0 {
		if e = validateDownloadSeasonBasis(p.SourceBasis, p.Requested, p.MediaKey, p.SiteID, p.ResourceID); e != nil {
			return nil, e
		}
	}
	if p.Magnet && (p.ChatOrigin == nil || len(p.SourceBasis) == 0 || len(p.Requested) != len(p.Scopes)) {
		return nil, seasonScopeError()
	}
	return &p, nil
}
func (a *App) intentSeasonProof(ctx context.Context, q qualityReader, in Intent) (*downloadSeasonProof, error) {
	p, e := a.readDownloadSeasonProof(ctx, q, "intent", in.ID, in.Hash, in.SeasonScopeSHA)
	if e != nil || p == nil {
		return p, e
	}
	if !p.matchesIntent(in) {
		return nil, seasonScopeError()
	}
	return p, nil
}

func (p *downloadSeasonProof) matchesIntent(in Intent) bool {
	if p.Magnet != in.ChatMagnet || in.Media.MediaType != "tv" || in.SubscriptionID != "" || len(in.SelectedEpisodes) != 0 || p.MediaKey != mediaKey(in.Media) || p.SiteID != in.SiteID || p.ResourceID != in.ResourceID || p.Scopes[0].Season != in.Season || !slices.Equal(p.Scopes[0].Episodes, in.Episodes) {
		return false
	}
	if p.ChatOrigin == nil {
		// Previously accepted legacy scopes remain readable for existing files.
		return !in.ChatDownload && p.LarkInbox != "" && p.LarkInbox == in.LarkInbox
	}
	return in.ChatDownload && p.ChatOrigin.valid() && p.LarkInbox == "" && in.LarkInbox == ""
}

func validateOriginalChatSeasonListing(ctx context.Context, r sites.Resource, wanted []SearchRequest) error {
	parser, err := originalChatPackParser(ctx)
	if err != nil {
		return err
	}
	meta, err := originalChatPackMeta(parser, r)
	if err != nil {
		return err
	}
	seasons, episodes, err := larkMetaNumbers(meta)
	if err != nil || len(episodes) != 0 || len(seasons) != len(wanted) {
		return seasonScopeError()
	}
	for i, request := range wanted {
		if seasons[i] != request.Season {
			return seasonScopeError()
		}
	}
	return nil
}

func (a *App) prepareDownloadSeasonProof(ctx context.Context, p *DownloadPreview, wanted []SearchRequest, m metainfo.Material, r sites.Resource, basis, providerModel json.RawMessage) (*downloadSeasonProof, error) {
	if len(wanted) == 0 {
		return nil, nil
	}
	origin, err := a.originalChatDownloadOrigin(ctx)
	if err != nil || !p.ChatDownload || p.Media.MediaType != "tv" || p.SubscriptionID != "" || len(wanted) < 2 || len(p.SelectedEpisodes) != 0 || p.Auto {
		return nil, seasonScopeError()
	}
	if err = validateDownloadSeasonBasis(basis, wanted, mediaKey(p.Media), p.SiteID, p.ResourceID); err != nil {
		return nil, err
	}
	if err = validateOriginalChatSeasonListing(ctx, r, wanted); err != nil {
		return nil, err
	}
	result := &downloadSeasonProof{SourceBasis: append(json.RawMessage(nil), basis...), ChatOrigin: &origin, Version: 1, Kind: "download_preview", ID: p.ID, MediaKey: mediaKey(p.Media), Hash: p.Hash, SiteID: p.SiteID, ResourceID: p.ResourceID, Requested: wanted, Magnet: p.ChatMagnet}
	for _, request := range wanted {
		if request.MediaType != "tv" || request.TMDBID != p.Media.ID || request.Season < 1 || len(request.Episodes) == 0 {
			return nil, seasonScopeError()
		}
		if err = a.ensureNotExcluded(ctx, p.Media, request.Season, request.Episodes); err != nil {
			return nil, err
		}
		if result.Magnet {
			result.Scopes = append(result.Scopes, downloadSeasonScope{Season: request.Season, Episodes: slices.Clone(request.Episodes)})
		}
	}
	if !result.Magnet {
		result.Files, result.Scopes, err = a.measureDownloadSeasonFiles(ctx, result, p.Media, providerModel, p.Request.Mappings, m.Files, r)
		if err != nil {
			return nil, err
		}
	}
	p.Season, p.Episodes = result.Scopes[0].Season, slices.Clone(result.Scopes[0].Episodes)
	p.Request.Season, p.Request.Episodes = p.Season, slices.Clone(p.Episodes)
	p.SeasonScopeSHA = seasonScopeSHA(*result)
	return result, nil
}

func (a *App) measureDownloadSeasonFiles(ctx context.Context, proof *downloadSeasonProof, selected catalog.Media, providerModel json.RawMessage, mappings []media.MappingRule, files []metainfo.File, r sites.Resource) ([]downloadScopedFile, []downloadSeasonScope, error) {
	targets := make(map[int]decision.Target, len(proof.Requested))
	acquired := make(map[int][]int, len(proof.Requested))
	var known []media.Episode
	for _, request := range proof.Requested {
		if targets[request.Season].Season != 0 {
			return nil, nil, seasonScopeError()
		}
		request.Mappings = mappings
		var target decision.Target
		var err error
		if providerNativeMedia(selected) {
			target, err = providerDownloadTarget(request, selected, providerModel, false)
		} else {
			target, err = a.targetMedia(ctx, request, false, selected)
		}
		if err != nil {
			return nil, nil, err
		}
		targets[request.Season] = target
		for _, ep := range target.KnownEpisodes {
			known = append(known, media.Episode{Season: request.Season, Episode: ep})
		}
	}
	var scopedFiles []downloadScopedFile
	for _, file := range files {
		if !isVideo(file.Path) {
			continue
		}
		name := filepath.Base(file.Path)
		parsed := a.parseMediaNameContext(ctx, name)
		if parsed.Status != media.Parsed || parsed.Original == nil {
			return nil, nil, seasonScopeError()
		}
		resolved := media.Resolve(proof.MediaKey, *parsed.Original, media.Source{Site: proof.SiteID}, mappings, known, nil)
		if resolved.Status != media.Parsed || len(resolved.Standard) == 0 {
			return nil, nil, seasonScopeError()
		}
		scoped := downloadScopedFile{Path: file.Path, Size: file.Size, Season: resolved.Standard[0].Season}
		target, ok := targets[scoped.Season]
		if !ok {
			return nil, nil, seasonScopeError()
		}
		for _, ep := range resolved.Standard {
			if ep.Season != scoped.Season || !slices.Contains(target.Episodes, ep.Episode) || slices.Contains(acquired[ep.Season], ep.Episode) {
				return nil, nil, seasonScopeError()
			}
			scoped.Episodes = append(scoped.Episodes, ep.Episode)
			acquired[ep.Season] = append(acquired[ep.Season], ep.Episode)
		}
		actual := r
		actual.Title, actual.Subtitle, actual.Size = name, "", file.Size
		actual.SourceSubtitle = nil
		choice := a.evaluateResourceContext(ctx, actual, target, decision.Policy{})
		if !choice.Accepted || choice.NeedsConfirmation {
			return nil, nil, bad("REVIEW_FILE_IDENTITY", "多季包实际视频文件名不能确认目标身份，请手动核对")
		}
		slices.Sort(scoped.Episodes)
		scopedFiles = append(scopedFiles, scoped)
	}
	var scopes []downloadSeasonScope
	for _, request := range proof.Requested {
		episodes := acquired[request.Season]
		if len(episodes) == 0 {
			return nil, nil, seasonScopeError()
		}
		slices.Sort(episodes)
		scopes = append(scopes, downloadSeasonScope{Season: request.Season, Episodes: episodes})
	}
	return scopedFiles, scopes, nil
}
func (a *App) confirmDownloadSeasonProofTx(ctx context.Context, tx *sql.Tx, p DownloadPreview, in Intent) error {
	proof, e := a.readDownloadSeasonProof(ctx, tx, "download_preview", p.ID, p.Hash, p.SeasonScopeSHA)
	if e != nil || proof == nil {
		return e
	}
	origin, err := a.originalChatDownloadOrigin(ctx)
	if err != nil || proof.ChatOrigin == nil || origin != *proof.ChatOrigin || in.SeasonScopeSHA != p.SeasonScopeSHA || !proof.matchesIntent(in) {
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
	if in.ChatWholeUnscoped {
		return nil // Actual file slots are checked if the download is later reviewed for import.
	}
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

const completedSeasonMagnetKind = "download_magnet_files"

func completedSeasonMagnetKey(in Intent) string { return in.ID + ":" + in.SeasonScopeSHA }

func (a *App) measureCompletedSeasonMagnet(ctx context.Context, in Intent, proof *downloadSeasonProof, files []download.File) ([]downloadScopedFile, error) {
	material := make([]metainfo.File, 0, len(files))
	for _, file := range files {
		if isVideo(file.Path) {
			material = append(material, metainfo.File{Path: file.Path, Size: file.Size})
		}
	}
	var providerModel json.RawMessage
	if providerNativeMedia(in.Media) {
		snapshot, err := a.loadDownloadContext(ctx, "intent", in.ID, in.Hash)
		if err != nil {
			return nil, err
		}
		if err = verifyChatDownloadContext(in, snapshot); err != nil {
			return nil, err
		}
		providerModel = snapshot.Original.Media
	}
	measured, scopes, err := a.measureDownloadSeasonFiles(ctx, proof, in.Media, providerModel, in.Request.Mappings, material, sites.Resource{SiteID: in.SiteID})
	if err != nil {
		return nil, err
	}
	if !reflect.DeepEqual(scopes, proof.Scopes) {
		return nil, seasonScopeError()
	}
	slices.SortFunc(measured, func(a, b downloadScopedFile) int { return strings.Compare(a.Path, b.Path) })
	return measured, nil
}

func (a *App) saveCompletedSeasonMagnet(ctx context.Context, in Intent, files []downloadScopedFile) error {
	if len(files) == 0 || len(mustCleanSeedJSON(files)) > pluginDataValueLimit-(8<<10) {
		return seasonScopeError()
	}
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	key := completedSeasonMagnetKey(in)
	var previous []downloadScopedFile
	err = a.readQualitySealed(ctx, tx, completedSeasonMagnetKind, key, &previous)
	if errors.Is(err, sql.ErrNoRows) {
		if err = a.writeQualitySealedTx(ctx, tx, completedSeasonMagnetKind, key, files); err != nil {
			return err
		}
	} else if err != nil || !reflect.DeepEqual(previous, files) {
		return seasonScopeError()
	}
	return tx.Commit()
}

func (a *App) guardSeasonImportPlan(ctx context.Context, in Intent, plan ImportPlan) error {
	p, e := a.intentSeasonProof(ctx, a.Store.DB, in)
	if e != nil || p == nil {
		return e
	}
	if e = a.guardSourcePostAddReceipt(ctx, in); e != nil {
		return e
	}
	if p.Magnet && len(plan.Files) != 0 {
		if e = a.readQualitySealed(ctx, a.Store.DB, completedSeasonMagnetKind, completedSeasonMagnetKey(in), &p.Files); e != nil || len(p.Files) != len(plan.Files) {
			return seasonScopeError()
		}
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
