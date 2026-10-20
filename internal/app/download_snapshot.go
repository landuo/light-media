package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// This fingerprint contains destination semantics only. Credential rotation
// does not invalidate a reviewed workflow, and no password enters the digest.
func downloaderSnapshot(d DownloaderConfig) string {
	raw, _ := json.Marshal(struct {
		Kind, BaseURL, DownloadPath string
		Mappings                    []PathMapping
		AutoCategory                bool `json:"AutoCategory,omitempty"`
		ForceResume                 bool `json:"ForceResume,omitempty"`
	}{d.Kind, d.BaseURL, d.DownloadPath, d.Mappings, d.AutoCategory, d.ForceResume})
	h := sha256.Sum256(raw)
	return hex.EncodeToString(h[:])
}
func (a *App) guardDownloaderSnapshot(ctx context.Context, in Intent) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	d, err := a.downloader(in.DownloaderID)
	if err != nil {
		return err
	}
	if in.DownloaderSnapshot == "" || in.DownloaderSnapshot != downloaderSnapshot(d) {
		return bad("REVIEW_DOWNLOADER_CHANGED", "下载器地址、下载目录或路径映射与已确认预览不同，请在关联下载中重新核对并确认")
	}
	return nil
}
func (a *App) guardAcquisitionSlots(ctx context.Context, in Intent) error {
	proof, e := a.qualitySnapshot(ctx, a.Store.DB, "intent", in.ID, in.Hash)
	if e != nil {
		return e
	}
	if proof != nil && proof.BestVersion {
		return a.guardQualityAcquisition(ctx, a.Store.DB, proof)
	}
	seasonProof, e := a.intentSeasonProof(ctx, a.Store.DB, in)
	if e != nil {
		return e
	}
	scopes := []downloadSeasonScope{{in.Season, importEpisodes(in.Episodes)}}
	if seasonProof != nil {
		scopes = seasonProof.Scopes
	}
	for _, scope := range scopes {
		for _, ep := range scope.Episodes {
			var count int
			if err := a.Store.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM asset_slots WHERE media_key=? AND season=? AND episode=?`, mediaKey(in.Media), scope.Season, ep).Scan(&count); err != nil {
				return err
			}
			if count > 0 {
				return bad("REVIEW_ALREADY_IN_LIBRARY", "目标已登记为库内资产，未再次提交下载，请核对已有文件")
			}
		}
	}
	return nil
}
