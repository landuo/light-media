package app

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Library candidates are observations, never ownership receipts. Confirming one
// only indexes the existing video; it never gives this application delete rights.
type LibraryCandidate struct {
	ID          string         `json:"id"`
	RootID      string         `json:"root_id"`
	RootPath    string         `json:"root_path"`
	Path        string         `json:"path"`
	Snapshot    importSnapshot `json:"snapshot"`
	ReasonCode  string         `json:"reason_code"`
	Reason      string         `json:"reason"`
	State       string         `json:"state"`
	DetectedAt  string         `json:"detected_at"`
	UpdatedAt   string         `json:"updated_at"`
	AssetID     string         `json:"asset_id,omitempty"`
	ConfirmedAt string         `json:"confirmed_at,omitempty"`
}
type LibraryCandidatePage struct {
	Items      []LibraryCandidate `json:"items"`
	NextCursor string             `json:"next_cursor,omitempty"`
	Complete   bool               `json:"complete"`
}
type LibraryRegisterRequest struct {
	CandidateID string `json:"candidate_id"`
	MediaType   string `json:"media_type"`
	TMDBID      int64  `json:"tmdb_id"`
	Season      int    `json:"season"`
	Episodes    []int  `json:"episodes"`
	Confirm     bool   `json:"confirm"`
}
type libraryIdentity struct {
	AssetID     string         `json:"asset_id"`
	CandidateID string         `json:"candidate_id"`
	RootID      string         `json:"root_id"`
	RootPath    string         `json:"root_path"`
	Path        string         `json:"path"`
	Snapshot    importSnapshot `json:"snapshot"`
	MediaType   string         `json:"media_type"`
	TMDBID      int64          `json:"tmdb_id"`
	Season      int            `json:"season"`
	Episodes    []int          `json:"episodes"`
	Source      string         `json:"source"`
	ConfirmedAt string         `json:"confirmed_at"`
	Actor       string         `json:"actor"`
}

func libraryCandidateID(root Root, path string, locked importSnapshot) string {
	return "lib_candidate_" + tokenHash(fmt.Sprintf("%s\x00%s\x00%s\x00%d:%d:%d:%d", root.ID, root.Path, path, locked.Device, locked.Inode, locked.Size, locked.ModNS))
}
func (a *App) pendingLibraryCandidates(ctx context.Context, rootID, path string) ([]LibraryCandidate, error) {
	rows, err := a.Store.DB.QueryContext(ctx, `SELECT payload FROM records WHERE kind='library_candidate' AND json_extract(payload,'$.state')='pending' AND json_extract(payload,'$.root_id')=? AND (?='' OR json_extract(payload,'$.path')=?) ORDER BY id LIMIT 10001`, rootID, path, path)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LibraryCandidate{}
	for rows.Next() {
		var raw []byte
		var candidate LibraryCandidate
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(raw, &candidate); err != nil {
			return nil, err
		}
		out = append(out, candidate)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if len(out) > 10000 {
		return nil, bad("LIBRARY_CANDIDATE_LIMIT", "未确认候选过多，请先处理现有候选")
	}
	return out, nil
}

type libraryCandidateCurrent struct {
	ID string `json:"id"`
}

func libraryCandidateCurrentKey(rootID, path string) string { return tokenHash(rootID + "\x00" + path) }
func (a *App) observeLibraryCandidate(ctx context.Context, root Root, path, code, reason string) error {
	locked, err := snapshotPath(path)
	if err != nil {
		return bad("REVIEW_LIBRARY_CHANGED", "未识别视频已变化或不可安全访问，请重新扫描")
	}
	candidate := LibraryCandidate{ID: libraryCandidateID(root, path, locked), RootID: root.ID, RootPath: root.Path, Path: path, Snapshot: locked, ReasonCode: code, Reason: reason, State: "pending", DetectedAt: now(), UpdatedAt: now()}
	var previous LibraryCandidate
	err = a.Store.Get(ctx, "library_candidate", candidate.ID, &previous)
	if err == nil {
		if previous.State == "registered" {
			return nil
		}
		candidate.DetectedAt = previous.DetectedAt
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	indexKey := libraryCandidateCurrentKey(root.ID, path)
	var current libraryCandidateCurrent
	err = a.Store.Get(ctx, "library_candidate_current", indexKey, &current)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	var prior LibraryCandidate
	if current.ID != "" && current.ID != candidate.ID {
		if err = a.Store.Get(ctx, "library_candidate", current.ID, &prior); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
	}
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if prior.ID != "" && prior.State == "pending" {
		prior.State = "changed"
		prior.UpdatedAt = now()
		if err = txPut(ctx, tx, "library_candidate", prior.ID, prior); err != nil {
			return err
		}
	}
	if err = txPut(ctx, tx, "library_candidate", candidate.ID, candidate); err != nil {
		return err
	}
	if err = txPut(ctx, tx, "library_candidate_current", indexKey, libraryCandidateCurrent{ID: candidate.ID}); err != nil {
		return err
	}
	return tx.Commit()
}
func (a *App) resolveLibraryCandidate(ctx context.Context, rootID, path, assetID string) error {
	var current libraryCandidateCurrent
	err := a.Store.Get(ctx, "library_candidate_current", libraryCandidateCurrentKey(rootID, path), &current)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	var candidate LibraryCandidate
	if err = a.Store.Get(ctx, "library_candidate", current.ID, &candidate); err != nil {
		return err
	}
	if candidate.State != "pending" {
		return nil
	}
	candidate.State = "resolved"
	candidate.AssetID = assetID
	candidate.UpdatedAt = now()
	return a.Store.Put(ctx, "library_candidate", candidate.ID, candidate)
}
func (a *App) finalizeLibraryCandidates(ctx context.Context, root Root, paths []string) error {
	seen := map[string]bool{}
	for _, path := range paths {
		seen[path] = true
	}
	items, err := a.pendingLibraryCandidates(ctx, root.ID, "")
	if err != nil {
		return err
	}
	for _, candidate := range items {
		var current libraryCandidateCurrent
		indexErr := a.Store.Get(ctx, "library_candidate_current", libraryCandidateCurrentKey(root.ID, candidate.Path), &current)
		if indexErr != nil && !errors.Is(indexErr, sql.ErrNoRows) {
			return indexErr
		}
		if current.ID != "" && current.ID != candidate.ID {
			candidate.State = "changed"
			candidate.UpdatedAt = now()
			if err = a.Store.Put(ctx, "library_candidate", candidate.ID, candidate); err != nil {
				return err
			}
			continue
		}
		if candidate.RootPath != root.Path || !seen[candidate.Path] {
			candidate.State = "unavailable"
			candidate.UpdatedAt = now()
			if err = a.Store.Put(ctx, "library_candidate", candidate.ID, candidate); err != nil {
				return err
			}
		}
	}
	return nil
}
func (a *App) libraryCandidates(ctx context.Context, rootID string, limit int, cursor string) (LibraryCandidatePage, error) {
	out := LibraryCandidatePage{Items: []LibraryCandidate{}}
	if limit == 0 {
		limit = 50
	}
	if limit < 1 || limit > 200 {
		return out, bad("INVALID_CANDIDATE_LIMIT", "候选每页数量必须为1至200")
	}
	if rootID != "" {
		if _, e := a.importRoot(rootID); e != nil {
			return out, e
		}
	}
	after := ""
	if cursor != "" {
		if len(cursor) > 1024 {
			return out, bad("INVALID_CANDIDATE_CURSOR", "候选游标无效，请重新查询")
		}
		parts := strings.Split(cursor, ".")
		if len(parts) != 2 {
			return out, bad("INVALID_CANDIDATE_CURSOR", "候选游标无效，请重新查询")
		}
		body, e := base64.RawURLEncoding.DecodeString(parts[0])
		if e != nil {
			return out, bad("INVALID_CANDIDATE_CURSOR", "候选游标无效")
		}
		sig, e := base64.RawURLEncoding.DecodeString(parts[1])
		mac := hmac.New(sha256.New, a.Store.key)
		mac.Write(append([]byte("library-candidate:"), body...))
		var state struct{ Root, After string }
		if e != nil || !hmac.Equal(sig, mac.Sum(nil)) || json.Unmarshal(body, &state) != nil || state.Root != rootID || !strings.HasPrefix(state.After, "lib_candidate_") {
			return out, bad("INVALID_CANDIDATE_CURSOR", "更改库筛选后请重新查询")
		}
		after = state.After
	}
	rows, err := a.Store.DB.QueryContext(ctx, `SELECT payload FROM records WHERE kind='library_candidate' AND id>? AND json_extract(payload,'$.state')='pending' AND (?='' OR json_extract(payload,'$.root_id')=?) ORDER BY id LIMIT ?`, after, rootID, rootID, limit+1)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var raw []byte
		if err = rows.Scan(&raw); err != nil {
			return out, err
		}
		var candidate LibraryCandidate
		if err = json.Unmarshal(raw, &candidate); err != nil {
			return out, err
		}
		out.Items = append(out.Items, candidate)
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	out.Complete = len(out.Items) <= limit
	if !out.Complete {
		out.Items = out.Items[:limit]
		body, _ := json.Marshal(struct{ Root, After string }{rootID, out.Items[len(out.Items)-1].ID})
		mac := hmac.New(sha256.New, a.Store.key)
		mac.Write(append([]byte("library-candidate:"), body...))
		out.NextCursor = base64.RawURLEncoding.EncodeToString(body) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	}
	return out, nil
}
func (a *App) registerLibraryAsset(ctx context.Context, in LibraryRegisterRequest) (Asset, error) {
	a.importMu.Lock()
	defer a.importMu.Unlock()
	var empty Asset
	if !in.Confirm {
		return empty, bad("LIBRARY_CONFIRM_REQUIRED", "请明确确认影视身份，只登记现有文件索引")
	}
	if len(in.CandidateID) > 100 || !strings.HasPrefix(in.CandidateID, "lib_candidate_") || !validMedia(in.MediaType, in.TMDBID) {
		return empty, bad("INVALID_LIBRARY_IDENTITY", "请选择候选和明确的影视身份")
	}
	var candidate LibraryCandidate
	if err := a.Store.Get(ctx, "library_candidate", in.CandidateID, &candidate); err != nil {
		return empty, err
	}
	root, err := a.importRoot(candidate.RootID)
	if err != nil {
		return empty, err
	}
	if root.Path != candidate.RootPath || !importPath(candidate.Path) || !within(root.Path, candidate.Path) || candidate.Path == root.Path || !isVideo(candidate.Path) {
		return empty, bad("REVIEW_LIBRARY_CHANGED", "媒体库配置已变化，请重新扫描")
	}
	if (root.Kind == "movie" && in.MediaType != "movie") || (root.Kind == "tv" && in.MediaType != "tv") {
		return empty, bad("INVALID_LIBRARY_IDENTITY", "影视类型与所选媒体库不一致")
	}
	current, err := snapshotPath(candidate.Path)
	if err != nil || current != candidate.Snapshot {
		return empty, bad("REVIEW_LIBRARY_CHANGED", "候选文件指纹已变化，请重新扫描后确认")
	}
	if in.MediaType == "movie" {
		if in.Season != 0 || len(in.Episodes) != 0 {
			return empty, bad("INVALID_LIBRARY_EPISODE", "电影不应指定季集")
		}
	} else if in.Season < 1 || in.Season > 9999 || len(in.Episodes) != 1 || in.Episodes[0] < 1 || in.Episodes[0] > 9999 {
		return empty, bad("INVALID_LIBRARY_EPISODE", "请选择一个明确的标准季和单集；多集文件需先人工核对")
	}
	if candidate.State == "registered" {
		var prior Asset
		if err = a.Store.Get(ctx, "asset", candidate.AssetID, &prior); err != nil {
			return empty, err
		}
		sameEpisodes := len(prior.Episodes) == len(in.Episodes)
		if sameEpisodes && len(in.Episodes) == 1 {
			sameEpisodes = prior.Episodes[0] == in.Episodes[0]
		}
		if prior.Media.MediaType != in.MediaType || prior.Media.ID != in.TMDBID || prior.Season != in.Season || !sameEpisodes || prior.State == "removed" || prior.State == "missing_confirmed" {
			return empty, bad("LIBRARY_ALREADY_REGISTERED", "此候选已登记，不能通过旧确认覆盖既有身份")
		}
		return prior, nil
	}
	if candidate.State != "pending" {
		return empty, bad("LIBRARY_CANDIDATE_RESOLVED", "此候选已处理，请刷新候选列表")
	}
	identity, err := a.media(ctx, in.MediaType, in.TMDBID)
	if err != nil {
		return empty, err
	}
	if identity.ID != in.TMDBID || identity.MediaType != in.MediaType || identity.Title == "" {
		return empty, bad("REVIEW_LIBRARY_METADATA", "影视元数据身份不一致，请刷新后重试")
	}
	if in.MediaType == "tv" {
		season, e := a.season(ctx, in.TMDBID, in.Season)
		if e != nil {
			return empty, e
		}
		if season.SeriesID != in.TMDBID || season.SeasonNumber != in.Season {
			return empty, bad("REVIEW_LIBRARY_METADATA", "单季元数据身份不一致，请刷新后重试")
		}
		found := false
		for _, episode := range season.Episodes {
			if episode.EpisodeNumber == in.Episodes[0] && episode.SeasonNumber == in.Season && episode.ID > 0 {
				found = true
				break
			}
		}
		if !found {
			return empty, bad("INVALID_LIBRARY_EPISODE", "所选季集缺少官方单集元数据，请重新选择")
		}
	}
	current, err = snapshotPath(candidate.Path)
	if err != nil || current != candidate.Snapshot {
		return empty, bad("REVIEW_LIBRARY_CHANGED", "确认期间文件指纹已变化，请重新扫描")
	}
	latestRoot, err := a.importRoot(candidate.RootID)
	if err != nil || latestRoot.Path != candidate.RootPath {
		return empty, bad("REVIEW_LIBRARY_CHANGED", "确认期间媒体库配置已变化，请重新扫描")
	}
	asset := Asset{ID: id("asset_"), Media: identity, Season: in.Season, Episodes: append([]int{}, in.Episodes...), RootID: root.ID, Path: candidate.Path, Size: current.Size, State: "external", ScrapeState: "external", FNOSState: "unverified", CreatedAt: now(), IdentitySource: "manual", IdentityConfirmedAt: now()}
	proof := libraryIdentity{AssetID: asset.ID, CandidateID: candidate.ID, RootID: root.ID, RootPath: root.Path, Path: asset.Path, Snapshot: current, MediaType: in.MediaType, TMDBID: in.TMDBID, Season: in.Season, Episodes: asset.Episodes, Source: "manual", ConfirmedAt: asset.IdentityConfirmedAt, Actor: "admin"}
	candidate.State = "registered"
	candidate.AssetID = asset.ID
	candidate.ConfirmedAt = proof.ConfirmedAt
	candidate.UpdatedAt = now()
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return empty, err
	}
	defer tx.Rollback()
	var count int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM records WHERE kind='asset' AND json_extract(payload,'$.path')=?`, asset.Path).Scan(&count); err != nil {
		return empty, err
	}
	if count != 0 {
		return empty, bad("LIBRARY_ALREADY_REGISTERED", "此视频已有资产索引，请在现有资产中核对")
	}
	for _, record := range []struct {
		kind, id string
		value    any
	}{{"asset", asset.ID, asset}, {"asset_identity", asset.ID, proof}, {"library_candidate", candidate.ID, candidate}} {
		if err = txPut(ctx, tx, record.kind, record.id, record.value); err != nil {
			return empty, err
		}
	}
	for _, episode := range importEpisodes(asset.Episodes) {
		if _, err = tx.ExecContext(ctx, `INSERT INTO asset_slots VALUES(?,?,?,?)`, mediaKey(asset.Media), asset.Season, episode, asset.ID); err != nil {
			return empty, err
		}
	}
	auditID := id("audit_")
	if err = txPut(ctx, tx, "audit", auditID, map[string]any{"time": now(), "actor": "admin", "operation": "library.register", "asset_id": asset.ID, "candidate_id": candidate.ID, "media_id": mediaKey(identity), "season": asset.Season, "episodes": asset.Episodes, "file_unchanged": true}); err != nil {
		return empty, err
	}
	final, finalErr := snapshotPath(candidate.Path)
	if finalErr != nil || final != candidate.Snapshot {
		return empty, bad("REVIEW_LIBRARY_CHANGED", "提交登记时视频指纹已变化，请重新扫描")
	}
	if err = tx.Commit(); err != nil {
		return empty, err
	}
	a.log("INFO", "assets", "library_registered", fmt.Sprintf("现有视频已人工登记为外部资产：%s；未修改视频或旁车", mediaKey(identity)), "", "")
	return asset, nil
}
