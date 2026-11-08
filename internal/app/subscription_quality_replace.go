package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"

	"golang.org/x/sys/unix"
)

type qualityImportProof struct {
	Overwrite                                     *importOverwritePolicy `json:"overwrite,omitempty"`
	Version                                       int
	Plan, Intent, InputSHA, DownloadSHA, RootPath string
	Download                                      qualityDownloadSnapshot
	Sidecars                                      map[string][]qualitySidecarProof
}
type qualitySwapReceipt struct {
	Version                                        int
	Plan, File, InputSHA, Target, Backup, OldAsset string
	Old                                            importSnapshot
	New                                            importReceipt
}
type qualityCleanup struct {
	Version                               int
	ID, NewAsset, NewPath, NewRoot, State string
	NewSnapshot                           importSnapshot
	RootPaths                             map[string]string
	OldAssets                             []string
	Files                                 []managedFile
	NewFiles                              []managedFile
}

func qualityImportSHA(plan ImportPlan) string {
	type identityFile struct {
		ID, Source, Target, Method string
		Season                     int
		Episodes                   []int
		SourceSnapshot             importSnapshot
	}
	files := []identityFile{}
	for _, f := range plan.Files {
		files = append(files, identityFile{f.ID, f.Source, f.Target, f.Method, f.Season, f.Episodes, planSnapshot(f)})
	}
	base := tokenHash(string(mustCleanSeedJSON(struct {
		Plan, Intent, Media, Root, Mode, Directory string
		Files                                      []identityFile
	}{plan.ID, plan.IntentID, mediaKey(plan.Media), plan.RootID, plan.Mode, "", files})))
	if plan.DirectorySourceVersion != 0 {
		return tokenHash(base + "\x00directory_source=" + jsonNumber(int64(plan.DirectorySourceVersion)))
	}
	return base
}
func (a *App) readQualitySealed(ctx context.Context, q qualityReader, kind, id string, out any) error {
	var sealed []byte
	if e := q.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind=? AND id=?`, kind, id).Scan(&sealed); e != nil {
		return e
	}
	raw, e := a.pluginStore.unseal(kind, "system", id, sealed)
	if e != nil {
		return e
	}
	return cleanupJournalStrict(raw, out)
}
func (a *App) writeQualitySealedTx(ctx context.Context, tx *sql.Tx, kind, id string, value any) error {
	sealed, e := a.pluginStore.seal(kind, "system", id, mustCleanSeedJSON(value))
	if e != nil {
		return e
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO records(kind,id,payload,updated_at) VALUES(?,?,?,?) ON CONFLICT(kind,id) DO UPDATE SET payload=excluded.payload,updated_at=excluded.updated_at`, kind, id, sealed, now())
	return e
}
func (a *App) qualityImportDownload(ctx context.Context, request ImportRequest) (*qualityDownloadSnapshot, error) {
	if !request.qualityReplacement {
		return nil, nil
	}
	if request.IntentID == "" || request.Manual || request.ReorganizeAssetID != "" {
		return nil, bad("QUALITY_IMPORT_CONTEXT_MISSING", "洗版整理缺少真实下载来源")
	}
	var intent Intent
	if e := a.Store.Get(ctx, "intent", request.IntentID, &intent); e != nil {
		return nil, e
	}
	if intent.State != "accepted" && intent.State != "downloading" && intent.State != "downloaded" {
		return nil, bad("QUALITY_IMPORT_UNCONFIRMED", "洗版下载尚未实际接收或已取消")
	}
	if e := a.guardSourceEpisodeReceipt(ctx, intent); e != nil {
		return nil, e
	}
	p, e := a.qualitySnapshot(ctx, a.Store.DB, "intent", intent.ID, intent.Hash)
	if e != nil || p == nil || !p.BestVersion {
		return p, e
	}
	var fact subscriptionQualityAccepted
	if e = a.readQualitySealed(ctx, a.Store.DB, "subscription_quality_accepted", intent.ID, &fact); e != nil {
		return nil, e
	}
	if fact.Version != 1 || fact.Intent != intent.ID || fact.Hash != intent.Hash || fact.SnapshotSHA != tokenHash(string(mustCleanSeedJSON(p))) {
		return nil, bad("QUALITY_IMPORT_UNCONFIRMED", "洗版整理缺少实际新下载接受事实")
	}
	if fact.Disposition != "accepted" && fact.Disposition != "accepted_duplicate" {
		if fact.Disposition != "subscription_changed" && fact.Disposition != "subscription_removed" {
			return nil, bad("QUALITY_IMPORT_UNCONFIRMED", "洗版下载尚未实际接收")
		}
		// A changed/deleted subscription does not undo its already accepted
		// download. Only the immutable native one-use receipt can bridge that
		// historical scope; mutable priority or an isolated task hash cannot.
		if intent.SubscriptionAcquisition == "" {
			return nil, bad("QUALITY_IMPORT_UNCONFIRMED", "旧规则下载缺少原生实际接受收据")
		}
		tx, te := a.Store.DB.BeginTx(ctx, nil)
		if te != nil {
			return nil, te
		}
		defer tx.Rollback()
		receipt, te := a.readAcquisitionReceiptTx(ctx, tx, intent)
		if te != nil {
			return nil, te
		}
		if receipt.Phase != "accepted" || receipt.Disposition != fact.Disposition || receipt.RootID != intent.SubscriptionAcquisition {
			return nil, bad("QUALITY_IMPORT_UNCONFIRMED", "旧规则下载实际接受事实不完整")
		}
		if te = a.guardAcceptedDownloadProgressTx(ctx, tx, intent); te != nil {
			return nil, te
		}
		if te = tx.Rollback(); te != nil {
			return nil, te
		}
	}
	identity := request.MediaType + ":" + jsonNumber(request.TMDBID)
	if request.providerImport() {
		identity = mediaKey(request.providerIdentity)
		if identity != mediaKey(intent.Media) {
			return nil, bad("QUALITY_IMPORT_CONTEXT_CHANGED", "洗版下载与整理影视来源不符")
		}
	}
	if p.MediaKey != identity || intent.RootID != request.RootID {
		return nil, bad("QUALITY_IMPORT_CONTEXT_CHANGED", "洗版下载与整理身份或媒体库不符")
	}
	if e = a.guardQualityAssets(ctx, a.Store.DB, p); e != nil {
		return nil, e
	}
	return p, nil
}
func jsonNumber(n int64) string { b, _ := json.Marshal(n); return string(b) }
func qualityFileOldAssets(p *qualityDownloadSnapshot, file PlanFile) []qualityAssetSnapshot {
	if p == nil {
		return nil
	}
	ids := map[string]bool{}
	for _, ep := range importEpisodes(file.Episodes) {
		for _, id := range p.Slots[ep] {
			ids[id] = true
		}
	}
	out := []qualityAssetSnapshot{}
	for _, frozen := range p.Assets {
		if ids[frozen.Asset.ID] {
			out = append(out, frozen)
		}
	}
	return out
}
func qualityTargetOwner(p *qualityDownloadSnapshot, file PlanFile) (*qualityAssetSnapshot, error) {
	for _, frozen := range qualityFileOldAssets(p, file) {
		if frozen.Asset.Path == file.Target {
			for _, ep := range importEpisodes(frozen.Asset.Episodes) {
				if !slices.Contains(importEpisodes(file.Episodes), ep) {
					return nil, bad("QUALITY_PARTIAL_PATH_CONFLICT", "原视频还承载未替换剧集，不能原位替换")
				}
			}
			f := frozen
			return &f, nil
		}
	}
	return nil, nil
}
func (a *App) saveQualityImportProofTx(ctx context.Context, tx *sql.Tx, plan ImportPlan, meta importMetadata, p *qualityDownloadSnapshot) error {
	if p == nil || !p.BestVersion {
		return nil
	}
	for _, f := range plan.Files {
		if f.Status != "ready" && f.Status != "already_imported" {
			continue
		}
		if f.Season != p.Season {
			return bad("QUALITY_IMPORT_CONTEXT_CHANGED", "洗版整理季号不符")
		}
		for _, ep := range importEpisodes(f.Episodes) {
			if !slices.Contains(importEpisodes(p.Episodes), ep) {
				return bad("QUALITY_IMPORT_CONTEXT_CHANGED", "洗版整理超出实际下载选集")
			}
		}
	}
	root, e := a.importRoot(plan.RootID)
	if e != nil {
		return e
	}
	sidecars, e := a.freezeQualitySidecars(ctx, tx, plan, meta, p)
	if e != nil {
		return e
	}
	proof := qualityImportProof{Version: 3, Overwrite: meta.Overwrite, RootPath: root.Path, Plan: plan.ID, Intent: plan.IntentID, InputSHA: qualityImportSHA(plan), DownloadSHA: tokenHash(string(mustCleanSeedJSON(p))), Download: *p, Sidecars: sidecars}
	return a.writeQualitySealedTx(ctx, tx, "subscription_quality_import", plan.ID, proof)
}
func (a *App) qualityPlanProof(ctx context.Context, q qualityReader, plan ImportPlan) (*qualityImportProof, error) {
	var proof qualityImportProof
	e := a.readQualitySealed(ctx, q, "subscription_quality_import", plan.ID, &proof)
	if errors.Is(e, sql.ErrNoRows) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	if (proof.Version != 1 && proof.Version != 2 && proof.Version != 3) || proof.Plan != plan.ID || proof.Intent != plan.IntentID || proof.InputSHA != qualityImportSHA(plan) || proof.DownloadSHA != tokenHash(string(mustCleanSeedJSON(proof.Download))) || !proof.Download.BestVersion || proof.Download.MediaKey != mediaKey(plan.Media) {
		return nil, bad("QUALITY_IMPORT_CONTEXT_CHANGED", "洗版整理证明与实际计划不符")
	}
	var meta importMetadata
	if e = qualityRecord(ctx, q, "import_meta", plan.ID, &meta); e != nil {
		return nil, e
	}
	if proof.Version == 3 {
		if proof.Overwrite == nil || meta.Overwrite == nil || !sameOverwritePolicy(*proof.Overwrite, *meta.Overwrite) {
			return nil, bad("QUALITY_IMPORT_CONTEXT_CHANGED", "洗版目录覆盖策略与封存计划不符")
		}
		for _, f := range plan.Files {
			if f.Method != meta.Methods[f.ID] {
				return nil, bad("QUALITY_IMPORT_CONTEXT_CHANGED", "洗版实际整理方式与封存计划不符")
			}
		}
	} else if proof.Overwrite != nil || meta.Overwrite != nil {
		return nil, bad("QUALITY_IMPORT_CONTEXT_CHANGED", "旧洗版计划不能追加未封存的目录覆盖权限")
	}
	if e = validateQualitySidecarPlan(plan, meta, &proof); e != nil {
		return nil, e
	}
	root, e := a.importRoot(plan.RootID)
	if e != nil || root.Path != proof.RootPath {
		return nil, bad("QUALITY_IMPORT_CONTEXT_CHANGED", "洗版媒体库目录已变化")
	}
	var current Intent
	if e = qualityRecord(ctx, q, "intent", plan.IntentID, &current); e != nil {
		return nil, e
	}
	p, e := a.qualitySnapshot(ctx, q, "intent", current.ID, current.Hash)
	if e != nil || p == nil {
		return nil, bad("QUALITY_IMPORT_UNCONFIRMED", "洗版下载原始证明缺失")
	}
	if proof.DownloadSHA != tokenHash(string(mustCleanSeedJSON(p))) || current.State == "cancelled" {
		return nil, bad("QUALITY_IMPORT_CONTEXT_CHANGED", "洗版实际下载已变化或取消")
	}
	return &proof, nil
}
func (a *App) guardQualityImportFile(ctx context.Context, q qualityReader, plan ImportPlan, file PlanFile, p *qualityImportProof, _ bool) error {
	if p == nil {
		return nil
	}
	if p.InputSHA != qualityImportSHA(plan) {
		return bad("QUALITY_IMPORT_CONTEXT_CHANGED", "洗版计划已变化")
	}
	if p.Version == 3 {
		// The accepted material proves what may be organized. Inventory slots
		// are presence facts, never permission to remove all previous versions.
		if file.Season != p.Download.Season || p.Overwrite == nil {
			return bad("QUALITY_IMPORT_CONTEXT_CHANGED", "洗版实际下载季号或目录覆盖证明缺失")
		}
		for _, ep := range importEpisodes(file.Episodes) {
			if !slices.Contains(importEpisodes(p.Download.Episodes), ep) {
				return bad("QUALITY_IMPORT_CONTEXT_CHANGED", "洗版整理超出实际接收范围")
			}
		}
		return a.guardQualitySidecars(ctx, q, plan, file, p)
	}
	for _, ep := range importEpisodes(file.Episodes) {
		actual, e := qualitySlots(ctx, q, mediaKey(plan.Media), file.Season, []int{ep})
		if e != nil {
			return e
		}
		if !reflect.DeepEqual(actual[ep], p.Download.Slots[ep]) {
			return bad("QUALITY_IMPORT_ASSET_CHANGED", "洗版原季集归属已变化")
		}
	}
	for _, frozen := range qualityFileOldAssets(&p.Download, file) {
		var current Asset
		if e := qualityRecord(ctx, q, "asset", frozen.Asset.ID, &current); e != nil {
			return e
		}
		check, original := current, frozen.Asset
		check.Episodes = original.Episodes
		if !reorganizeSameAsset(check, original) {
			return bad("QUALITY_IMPORT_ASSET_CHANGED", "洗版原资产身份已变化")
		}
		var video managedFile
		if e := qualityRecord(ctx, q, "managed_file", tokenHash(frozen.Video.Path), &video); e != nil || !reflect.DeepEqual(video, frozen.Video) {
			return bad("QUALITY_IMPORT_ASSET_CHANGED", "洗版原视频归属已变化")
		}
		path := frozen.Video.Path
		if path == file.Target {
			var e error
			path, e = a.qualitySwapOldPath(ctx, q, plan.ID+"/"+file.ID, plan, file, p, frozen.Video, file.Source, file.Target, planSnapshot(file))
			if e != nil {
				return e
			}
		}
		stamp, e := snapshotPath(path)
		if e != nil || stamp != frozen.Video.Snapshot {
			return bad("QUALITY_IMPORT_ASSET_CHANGED", "洗版原视频已被外部修改")
		}
		root, e := a.importRoot(frozen.Asset.RootID)
		if e != nil || root.Path != frozen.RootPath {
			return bad("QUALITY_IMPORT_ASSET_CHANGED", "洗版原媒体库已变化")
		}
	}
	return a.guardQualitySidecars(ctx, q, plan, file, p)
}
func (a *App) publishQualityImport(ctx context.Context, plan ImportPlan, file PlanFile, p *qualityImportProof, method string, check func() error) (importReceipt, error) {
	if p == nil {
		return a.publishImport(ctx, plan.ID+"/"+file.ID, file.Source, file.Target, planSnapshot(file), method, check)
	}
	if p.Version == 3 && p.Overwrite != nil {
		return a.publishDirectoryOverwrite(ctx, plan.ID+"/"+file.ID, file.Source, file.Target, plan.RootID, method, planSnapshot(file), *p.Overwrite, check)
	}
	owner, e := qualityTargetOwner(&p.Download, file)
	if e != nil {
		return importReceipt{}, e
	}
	if owner == nil {
		if e = a.guardQualityImportFile(ctx, a.Store.DB, plan, file, p, false); e != nil {
			return importReceipt{}, e
		}
		return a.publishImport(ctx, plan.ID+"/"+file.ID, file.Source, file.Target, planSnapshot(file), method, check)
	}
	return a.publishQualitySwap(ctx, plan.ID+"/"+file.ID, plan, file, p, owner.Video, file.Source, file.Target, planSnapshot(file), method, check)
}

func qualitySwapBackup(key, target string) string {
	return filepath.Join(filepath.Dir(target), ".light-media-quality-"+tokenHash(key)[:32]+".previous")
}

func validateQualitySwap(swap qualitySwapReceipt, key string, plan ImportPlan, file PlanFile, p *qualityImportProof, old managedFile, source, target string, locked importSnapshot) error {
	if swap.Version != 1 || swap.Plan != plan.ID || swap.File != file.ID || swap.InputSHA != p.InputSHA || swap.Target != target || swap.Backup != qualitySwapBackup(key, target) || swap.OldAsset != old.AssetID || swap.Old != old.Snapshot || swap.New.ID != key+"/quality-stage" || swap.New.Target != swap.Backup || swap.New.Source != source || swap.New.SourceSnapshot != locked || swap.New.State != "published" {
		return bad("QUALITY_SWAP_CHANGED", "洗版原位替换收据无法核对")
	}
	return nil
}

// Both pre-publication and cold recovery check the same sealed orientation.
// A durable, completed exchange is not mistaken for an external overwrite.
func (a *App) qualitySwapOldPath(ctx context.Context, q qualityReader, key string, plan ImportPlan, file PlanFile, p *qualityImportProof, old managedFile, source, target string, locked importSnapshot) (string, error) {
	var swap qualitySwapReceipt
	e := a.readQualitySealed(ctx, q, "subscription_quality_swap", key, &swap)
	if errors.Is(e, sql.ErrNoRows) {
		return target, nil
	}
	if e != nil {
		return "", e
	}
	if e = validateQualitySwap(swap, key, plan, file, p, old, source, target, locked); e != nil {
		return "", e
	}
	targetStamp, targetErr := snapshotPath(target)
	backupStamp, backupErr := snapshotPath(swap.Backup)
	if targetErr == nil && backupErr == nil {
		if targetStamp == swap.Old && backupStamp == swap.New.StageSnapshot {
			return target, nil
		}
		if targetStamp == swap.New.StageSnapshot && backupStamp == swap.Old {
			return swap.Backup, nil
		}
	}
	return "", bad("QUALITY_SWAP_UNCONFIRMED", "洗版新旧文件身份无法核对，文件均保留")
}

func (a *App) publishQualitySwap(ctx context.Context, key string, plan ImportPlan, file PlanFile, p *qualityImportProof, old managedFile, source, target string, locked importSnapshot, method string, check func() error) (importReceipt, error) {
	backup := qualitySwapBackup(key, target)
	var swap qualitySwapReceipt
	e := a.readQualitySealed(ctx, a.Store.DB, "subscription_quality_swap", key, &swap)
	if errors.Is(e, sql.ErrNoRows) {
		if e = a.guardQualityImportFile(ctx, a.Store.DB, plan, file, p, false); e != nil {
			return importReceipt{}, e
		}
		newReceipt, e := a.publishImport(ctx, key+"/quality-stage", source, backup, locked, method, check)
		if e != nil {
			return newReceipt, e
		}
		swap = qualitySwapReceipt{Version: 1, Plan: plan.ID, File: file.ID, InputSHA: p.InputSHA, Target: target, Backup: backup, OldAsset: old.AssetID, Old: old.Snapshot, New: newReceipt}
		tx, e := a.Store.DB.BeginTx(ctx, nil)
		if e != nil {
			return importReceipt{}, e
		}
		defer tx.Rollback()
		if e = a.writeQualitySealedTx(ctx, tx, "subscription_quality_swap", key, swap); e != nil {
			return importReceipt{}, e
		}
		if e = tx.Commit(); e != nil {
			return importReceipt{}, e
		}
	} else if e != nil {
		return importReceipt{}, e
	}
	if e = validateQualitySwap(swap, key, plan, file, p, old, source, target, locked); e != nil {
		return importReceipt{}, e
	}
	targetStamp, targetErr := snapshotPath(target)
	backupStamp, backupErr := snapshotPath(backup)
	if targetErr != nil || backupErr != nil {
		return importReceipt{}, bad("QUALITY_SWAP_UNCONFIRMED", "洗版新旧文件缺失，未重新发布或删除")
	}
	if targetStamp == swap.Old && backupStamp == swap.New.StageSnapshot {
		if e = check(); e != nil {
			return importReceipt{}, e
		}
		if e = a.guardQualityImportFile(ctx, a.Store.DB, plan, file, p, false); e != nil {
			return importReceipt{}, e
		}
		parent, e := importDir(filepath.Dir(target), false)
		if e != nil {
			return importReceipt{}, e
		}
		defer parent.Close()
		if e = qualityRenameExchange(int(parent.Fd()), filepath.Base(backup), filepath.Base(target)); e != nil {
			return importReceipt{}, e
		}
		if e = parent.Sync(); e != nil {
			return importReceipt{}, e
		}
		targetStamp, targetErr = snapshotPath(target)
		backupStamp, backupErr = snapshotPath(backup)
	}
	if targetErr != nil || backupErr != nil || targetStamp != swap.New.StageSnapshot || backupStamp != swap.Old {
		return importReceipt{}, bad("QUALITY_SWAP_UNCONFIRMED", "洗版替换后的新旧文件身份无法核对，文件均保留")
	}
	receipt := swap.New
	receipt.ID, receipt.Target, receipt.Stage, receipt.State = key, target, "", "published"
	return receipt, nil
}

func (a *App) commitQualityReplacementTx(ctx context.Context, tx *sql.Tx, plan ImportPlan, file PlanFile, asset Asset, newFiles []managedFile) error {
	p, e := a.qualityPlanProof(ctx, tx, plan)
	if e != nil || p == nil {
		return e
	}
	if e = a.guardQualityImportFile(ctx, tx, plan, file, p, true); e != nil {
		return e
	}
	if p.Version == 3 {
		// The shared directory host registers only owners of actually affected
		// paths in this transaction. Old slot cleanup must not run alongside it.
		return a.requireQualityDirectoryOverwriteTx(ctx, tx, plan, file, p)
	}
	if len(newFiles) == 0 || newFiles[0].Kind != "video" || newFiles[0].AssetID != asset.ID {
		return bad("QUALITY_IMPORT_CONTEXT_CHANGED", "洗版新文件归属缺失")
	}
	cleanup := qualityCleanup{Version: 2, ID: plan.ID + "/" + file.ID, NewAsset: asset.ID, NewPath: asset.Path, NewRoot: asset.RootID, NewSnapshot: newFiles[0].Snapshot, NewFiles: append([]managedFile(nil), newFiles...), State: "pending", Files: []managedFile{}, OldAssets: []string{}, RootPaths: map[string]string{plan.RootID: p.RootPath}}
	for _, old := range qualityFileOldAssets(&p.Download, file) {
		cleanup.RootPaths[old.Asset.RootID] = old.RootPath
		for _, ep := range importEpisodes(file.Episodes) {
			if _, e = tx.ExecContext(ctx, `DELETE FROM asset_slots WHERE media_key=? AND season=? AND episode=? AND asset_id=?`, mediaKey(plan.Media), file.Season, ep, old.Asset.ID); e != nil {
				return e
			}
		}
		var remaining int
		if e = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM asset_slots WHERE asset_id=?`, old.Asset.ID).Scan(&remaining); e != nil {
			return e
		}
		if remaining > 0 {
			continue
		}
		var current Asset
		if e = qualityRecord(ctx, tx, "asset", old.Asset.ID, &current); e != nil {
			return e
		}
		current.State, current.Error = "superseded", "已由更高优先级的实际下载替换；旧受管文件待核验清理"
		if e = txPut(ctx, tx, "asset", current.ID, current); e != nil {
			return e
		}
		cleanup.OldAssets = append(cleanup.OldAssets, current.ID)
		rows, e := tx.QueryContext(ctx, `SELECT payload FROM records WHERE kind='managed_file' AND json_extract(payload,'$.asset_id')=?`, current.ID)
		if e != nil {
			return e
		}
		for rows.Next() {
			var raw []byte
			var owned managedFile
			if e = rows.Scan(&raw); e != nil {
				rows.Close()
				return e
			}
			if json.Unmarshal(raw, &owned) != nil {
				rows.Close()
				return bad("QUALITY_IMPORT_ASSET_CHANGED", "旧整理文件归属损坏")
			}
			if owned.Path == current.SourcePath || owned.RootID != old.Asset.RootID || !within(old.RootPath, owned.Path) {
				rows.Close()
				return bad("QUALITY_IMPORT_ASSET_CHANGED", "旧整理文件与做种源或媒体库归属冲突")
			}
			cleanup.Files = append(cleanup.Files, owned)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
	}
	for i, owned := range cleanup.Files {
		path, e := a.qualityCleanupRelocatedPath(ctx, tx, plan, file, p, owned)
		if e != nil {
			return e
		}
		if path != owned.Path {
			owned.Path = path
			cleanup.Files[i] = owned
			if e = txPut(ctx, tx, "managed_file", tokenHash(owned.Path), owned); e != nil {
				return e
			}
		}
	}
	if len(cleanup.Files) == 0 {
		return nil
	}
	if e = a.writeQualitySealedTx(ctx, tx, "subscription_quality_cleanup", cleanup.ID, cleanup); e != nil {
		return e
	}
	_, e = enqueueTx(ctx, tx, "quality_cleanup", map[string]string{"id": cleanup.ID}, plan.WorkflowID)
	return e
}

func (a *App) runQualityCleanup(ctx context.Context, j Job, id string) error {
	a.importMu.Lock()
	defer a.importMu.Unlock()
	if e := a.jobCancelled(ctx, j); e != nil {
		return e
	}
	var p qualityCleanup
	if e := a.readQualitySealed(ctx, a.Store.DB, "subscription_quality_cleanup", id, &p); e != nil {
		return e
	}
	if (p.Version != 1 && p.Version != 2) || p.ID != id {
		return bad("QUALITY_CLEANUP_CHANGED", "洗版清理收据无效")
	}
	if p.State == "complete" {
		return nil
	}
	release, e := a.beginWrite()
	if e != nil {
		return e
	}
	defer release()
	root, e := a.importRoot(p.NewRoot)
	if e != nil || root.Path != p.RootPaths[p.NewRoot] || !within(root.Path, p.NewPath) {
		return bad("QUALITY_CLEANUP_CHANGED", "洗版媒体库目录已变化")
	}
	var replacement Asset
	if e := a.Store.Get(ctx, "asset", p.NewAsset, &replacement); e != nil || replacement.State != "present" || replacement.Path != p.NewPath || replacement.RootID != p.NewRoot {
		return bad("QUALITY_CLEANUP_CHANGED", "新洗版资产无法核验，保留旧文件")
	}
	stamp, e := snapshotPath(p.NewPath)
	if e != nil || stamp != p.NewSnapshot {
		return bad("QUALITY_CLEANUP_CHANGED", "新洗版视频已变化，保留旧文件")
	}
	var newVideo managedFile
	if e = a.Store.Get(ctx, "managed_file", tokenHash(p.NewPath), &newVideo); e != nil || newVideo.AssetID != p.NewAsset || newVideo.Snapshot != p.NewSnapshot {
		return bad("QUALITY_CLEANUP_CHANGED", "新洗版视频归属已变化")
	}
	for _, frozen := range p.NewFiles {
		var current managedFile
		if frozen.AssetID != p.NewAsset || frozen.RootID != p.NewRoot || !within(root.Path, frozen.Path) {
			return bad("QUALITY_CLEANUP_CHANGED", "新洗版文件归属无法核对，保留旧文件")
		}
		if e = a.Store.Get(ctx, "managed_file", tokenHash(frozen.Path), &current); e != nil || !reflect.DeepEqual(current, frozen) {
			return bad("QUALITY_CLEANUP_CHANGED", "新洗版文件受管归属已变化，保留旧文件")
		}
		stamp, e := snapshotPath(frozen.Path)
		if e != nil || stamp != frozen.Snapshot {
			return bad("QUALITY_CLEANUP_CHANGED", "新洗版文件已被外部修改，保留旧文件")
		}
	}
	for _, owned := range p.Files {
		var count int
		if e = a.Store.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM asset_slots WHERE asset_id=?`, owned.AssetID).Scan(&count); e != nil {
			return e
		}
		if count != 0 {
			return bad("QUALITY_CLEANUP_CHANGED", "旧视频仍有季集归属，保留旧文件")
		}
		root, e := a.importRoot(owned.RootID)
		if e != nil || root.Path != p.RootPaths[owned.RootID] || !within(root.Path, owned.Path) || owned.Path == p.NewPath {
			return bad("QUALITY_CLEANUP_CHANGED", "旧文件目录或新旧归属冲突")
		}
		var current managedFile
		if e = a.Store.Get(ctx, "managed_file", tokenHash(owned.Path), &current); e != nil || !reflect.DeepEqual(current, owned) {
			return bad("QUALITY_CLEANUP_CHANGED", "旧文件已被其他资产接管，保留文件")
		}
		parent, e := importDir(filepath.Dir(owned.Path), false)
		if e != nil {
			return e
		}
		quarantine := ".light-media-quality-remove-" + tokenHash(id + owned.Path)[:32] + ".part"
		quarantinePath := filepath.Join(filepath.Dir(owned.Path), quarantine)
		oldStamp, oldErr := snapshotPath(owned.Path)
		quarantineStamp, quarantineErr := snapshotPath(quarantinePath)
		if oldErr == nil && oldStamp == owned.Snapshot && errors.Is(quarantineErr, os.ErrNotExist) {
			e = importRenameExclusive(int(parent.Fd()), filepath.Base(owned.Path), quarantine)
			if e == nil {
				e = parent.Sync()
			}
			if e != nil {
				parent.Close()
				return e
			}
			quarantineStamp, quarantineErr = snapshotPath(quarantinePath)
		} else if !errors.Is(oldErr, os.ErrNotExist) {
			parent.Close()
			return bad("QUALITY_CLEANUP_CHANGED", "旧文件已被外部修改，保留文件")
		}
		if errors.Is(quarantineErr, os.ErrNotExist) {
			parent.Close()
			continue
		}
		if quarantineErr != nil || quarantineStamp != owned.Snapshot {
			parent.Close()
			return bad("QUALITY_CLEANUP_CHANGED", "隔离旧文件的身份无法核对，文件保留")
		}
		e = unix.Unlinkat(int(parent.Fd()), quarantine, 0)
		if e == nil {
			e = parent.Sync()
		}
		parent.Close()
		if e != nil {
			return e
		}
	}
	tx, e := a.Store.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	for _, owned := range p.Files {
		if _, e = tx.ExecContext(ctx, `DELETE FROM records WHERE kind='managed_file' AND id=? AND json_extract(payload,'$.asset_id')=?`, tokenHash(owned.Path), owned.AssetID); e != nil {
			return e
		}
	}
	deferred, e := completeOriginalDeferredScrapeTx(ctx, tx, j, replacement.ID)
	if e != nil {
		return e
	}
	if replacement.ScrapeState == "pending" && !deferred {
		if _, e = enqueueTx(ctx, tx, "scrape", map[string]string{"id": replacement.ID}, j.WorkflowID); e != nil {
			return e
		}
	}
	p.State = "complete"
	if e = a.writeQualitySealedTx(ctx, tx, "subscription_quality_cleanup", id, p); e != nil {
		return e
	}
	return tx.Commit()
}
