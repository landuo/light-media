package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"

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
	return nil, bad("PLUGIN_ADAPTER_UNAVAILABLE", "消息插件多季下载入口尚未适配")
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
