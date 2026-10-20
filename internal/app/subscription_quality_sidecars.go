package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"reflect"
	"slices"
)

type qualitySidecarProof struct {
	Sidecar importSidecar
	Old     *managedFile
}

// Existing subtitles are eligible only through the same old, managed asset
// whose episode slots the actual accepted replacement owns.
func (a *App) qualitySidecarOwner(ctx context.Context, q qualityReader, p *qualityDownloadSnapshot, file PlanFile, target string) (*managedFile, error) {
	if p == nil || !p.BestVersion {
		return nil, bad("IMPORT_CONFLICT", "字幕目标已存在")
	}
	var owned managedFile
	if e := qualityRecord(ctx, q, "managed_file", tokenHash(target), &owned); e != nil {
		return nil, bad("QUALITY_SIDECAR_UNMANAGED", "已有字幕不属于本次可替换资产")
	}
	for _, old := range qualityFileOldAssets(p, file) {
		if owned.AssetID != old.Asset.ID {
			continue
		}
		if owned.Path != target || !auxiliaryMaterialKind(owned.Kind) || owned.RootID != old.Asset.RootID || owned.MediaKey != p.MediaKey || !within(old.RootPath, target) || target == old.Asset.SourcePath {
			return nil, bad("QUALITY_SIDECAR_UNMANAGED", "已有字幕归属或路径无法核对")
		}
		for _, episode := range importEpisodes(old.Asset.Episodes) {
			if !slices.Contains(importEpisodes(file.Episodes), episode) {
				return nil, bad("QUALITY_PARTIAL_PATH_CONFLICT", "原字幕还服务于未替换剧集，不能原位替换")
			}
		}
		stamp, e := snapshotPath(target)
		if e != nil || stamp != owned.Snapshot {
			return nil, bad("QUALITY_SIDECAR_CHANGED", "已有字幕在预览前发生变化")
		}
		return &owned, nil
	}
	return nil, bad("QUALITY_SIDECAR_UNMANAGED", "已有字幕属于其他资产，禁止覆盖")
}

func (a *App) qualityImportSidecars(ctx context.Context, file PlanFile, p *qualityDownloadSnapshot) ([]importSidecar, error) {
	if p == nil {
		return a.sourceImportSidecars(ctx, file, false)
	}
	return a.sourceImportSidecarsChecked(ctx, file, func(target string) error {
		_, e := a.qualitySidecarOwner(ctx, a.Store.DB, p, file, target)
		return e
	})
}

func (a *App) freezeQualitySidecars(ctx context.Context, q qualityReader, plan ImportPlan, meta importMetadata, p *qualityDownloadSnapshot) (map[string][]qualitySidecarProof, error) {
	out := map[string][]qualitySidecarProof{}
	paths := map[string]bool{}
	for _, file := range plan.Files {
		paths[file.Target] = true
	}
	for _, file := range plan.Files {
		if file.Status != "ready" && file.Status != "already_imported" {
			continue
		}
		for _, sidecar := range meta.Sidecars[file.ID] {
			if sidecar.Source == sidecar.Target || paths[sidecar.Target] || !within(meta.RootPath, sidecar.Target) || !a.importSourceAllowed(sidecar.Source, plan.Manual) {
				return nil, bad("QUALITY_SIDECAR_CHANGED", "洗版字幕路径冲突或不在允许目录")
			}
			paths[sidecar.Target] = true
			frozen := qualitySidecarProof{Sidecar: sidecar}
			if _, e := os.Lstat(sidecar.Target); e == nil && meta.Overwrite == nil {
				frozen.Old, e = a.qualitySidecarOwner(ctx, q, p, file, sidecar.Target)
				if e != nil {
					return nil, e
				}
			} else if e != nil && !errors.Is(e, os.ErrNotExist) {
				return nil, e
			}
			out[file.ID] = append(out[file.ID], frozen)
		}
	}
	return out, nil
}

func validateQualitySidecarPlan(plan ImportPlan, meta importMetadata, p *qualityImportProof) error {
	seen := map[string]bool{}
	for _, file := range plan.Files {
		seen[file.ID] = true
		frozen := p.Sidecars[file.ID]
		actual := meta.Sidecars[file.ID]
		if len(frozen) != len(actual) || (p.Version == 1 && len(actual) != 0) {
			return bad("QUALITY_SIDECAR_CHANGED", "洗版字幕计划缺少原始封存证明")
		}
		for i := range actual {
			if frozen[i].Sidecar != actual[i] {
				return bad("QUALITY_SIDECAR_CHANGED", "洗版字幕来源或目标已变化")
			}
		}
	}
	for fileID := range p.Sidecars {
		if !seen[fileID] {
			return bad("QUALITY_SIDECAR_CHANGED", "洗版字幕文件身份不属于计划")
		}
	}
	for fileID, sides := range meta.Sidecars {
		if !seen[fileID] && len(sides) > 0 {
			return bad("QUALITY_SIDECAR_CHANGED", "字幕计划出现未封存文件")
		}
	}
	return nil
}

func qualitySidecarKey(plan ImportPlan, file PlanFile, index int) string {
	return fmt.Sprintf("%s/%s/side-%d", plan.ID, file.ID, index)
}

func (a *App) guardQualitySidecars(ctx context.Context, q qualityReader, plan ImportPlan, file PlanFile, p *qualityImportProof) error {
	for index, frozen := range p.Sidecars[file.ID] {
		stamp, e := snapshotPath(frozen.Sidecar.Source)
		if e != nil || stamp != frozen.Sidecar.Snapshot {
			return bad("QUALITY_SIDECAR_CHANGED", "洗版字幕源文件已变化，请重新预览")
		}
		if frozen.Old == nil {
			continue
		}
		var current managedFile
		if e := qualityRecord(ctx, q, "managed_file", tokenHash(frozen.Old.Path), &current); e != nil || !reflect.DeepEqual(current, *frozen.Old) {
			return bad("QUALITY_SIDECAR_CHANGED", "洗版原字幕的受管归属已变化")
		}
		sidecar := frozen.Sidecar
		path, e := a.qualitySwapOldPath(ctx, q, qualitySidecarKey(plan, file, index), plan, file, p, *frozen.Old, sidecar.Source, sidecar.Target, sidecar.Snapshot)
		if e != nil {
			return e
		}
		stamp, e = snapshotPath(path)
		if e != nil || stamp != frozen.Old.Snapshot {
			return bad("QUALITY_SIDECAR_CHANGED", "洗版原字幕已被外部修改")
		}
	}
	return nil
}

func (a *App) publishQualitySidecar(ctx context.Context, plan ImportPlan, file PlanFile, p *qualityImportProof, index int, sidecar importSidecar, check func() error) (importReceipt, error) {
	key := qualitySidecarKey(plan, file, index)
	if p == nil {
		return a.publishImport(ctx, key, sidecar.Source, sidecar.Target, sidecar.Snapshot, "copy", check)
	}
	if p.Version == 3 && p.Overwrite != nil {
		return a.publishDirectoryOverwrite(ctx, key, sidecar.Source, sidecar.Target, plan.RootID, file.Method, sidecar.Snapshot, *p.Overwrite, check)
	}
	if index < 0 || index >= len(p.Sidecars[file.ID]) || p.Sidecars[file.ID][index].Sidecar != sidecar {
		return importReceipt{}, bad("QUALITY_SIDECAR_CHANGED", "洗版字幕封存身份不符")
	}
	frozen := p.Sidecars[file.ID][index]
	if frozen.Old == nil {
		return a.publishImport(ctx, key, sidecar.Source, sidecar.Target, sidecar.Snapshot, "copy", check)
	}
	return a.publishQualitySwap(ctx, key, plan, file, p, *frozen.Old, sidecar.Source, sidecar.Target, sidecar.Snapshot, "copy", check)
}

// Called before new managed ownership is written. Each superseded path is
// relocated only using its exact, authenticated per-file exchange receipt.
func (a *App) qualityCleanupRelocatedPath(ctx context.Context, tx *sql.Tx, plan ImportPlan, file PlanFile, p *qualityImportProof, owned managedFile) (string, error) {
	if owned.Kind == "video" && owned.Path == file.Target {
		return a.qualitySwapOldPath(ctx, tx, plan.ID+"/"+file.ID, plan, file, p, owned, file.Source, file.Target, planSnapshot(file))
	}
	for index, frozen := range p.Sidecars[file.ID] {
		if frozen.Old != nil && reflect.DeepEqual(*frozen.Old, owned) {
			sidecar := frozen.Sidecar
			return a.qualitySwapOldPath(ctx, tx, qualitySidecarKey(plan, file, index), plan, file, p, owned, sidecar.Source, sidecar.Target, sidecar.Snapshot)
		}
	}
	return owned.Path, nil
}
