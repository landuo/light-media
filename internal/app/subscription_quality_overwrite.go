package app

import (
	"context"
	"database/sql"
	"errors"
)

// Validate the entire video/sidecar set before its first mutation. Publication
// still rechecks each real file. A previous known publication is read through
// its immutable host receipt instead of treating the new file as the old one.
func (a *App) preflightQualityDirectoryOverwrite(ctx context.Context, plan ImportPlan, file PlanFile, p *qualityImportProof, meta importMetadata) error {
	if p == nil || p.Version != 3 {
		return nil
	}
	if p.Overwrite == nil || meta.Overwrite == nil || !sameOverwritePolicy(*p.Overwrite, *meta.Overwrite) {
		return bad("QUALITY_IMPORT_CONTEXT_CHANGED", "洗版目录覆盖策略与封存计划不符")
	}
	type item struct {
		key, source, target string
		locked              importSnapshot
	}
	items := []item{}
	for i, s := range meta.Sidecars[file.ID] {
		items = append(items, item{qualitySidecarKey(plan, file, i), s.Source, s.Target, s.Snapshot})
	}
	items = append(items, item{plan.ID + "/" + file.ID, file.Source, file.Target, planSnapshot(file)})
	for _, v := range items {
		op, e := a.readDirectoryOverwriteOperation(ctx, v.key)
		if e == nil {
			if op.Source != v.source || op.Target != v.target || op.RootID != plan.RootID || op.Method != meta.Methods[file.ID] || op.SourceSnapshot != v.locked || !sameOverwritePolicy(op.Policy, *p.Overwrite) {
				return bad("REVIEW_OVERWRITE_RECEIPT", "覆盖重试与封存输入不一致")
			}
			known, er := a.knownDirectoryOverwritePublication(ctx, &op)
			if er != nil {
				return er
			}
			if known {
				continue
			}
		} else if !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		op, e = a.readDirectoryOverwrite(ctx, v.key, v.source, v.target, plan.RootID, meta.Methods[file.ID], v.locked, *p.Overwrite)
		if e != nil {
			return e
		}
		for _, old := range op.Targets {
			if old.Owner == nil {
				continue
			}
			var asset Asset
			if er := a.Store.Get(ctx, "asset", old.Owner.AssetID, &asset); er != nil || asset.RootID != old.Owner.RootID || old.Owner.Kind == "video" && asset.Path != old.Path {
				return bad("REVIEW_OVERWRITE_OWNER", "原文件的受管资产无法核对，未开始替换")
			}
		}
	}
	return nil
}

// New quality plans cannot use the host's legacy no-op for an absent operation.
// The host will verify the actual publication and register its precise owners
// later in this same transaction; first bind every required operation to this
// sealed quality plan, including its actual source and selected method.
func (a *App) requireQualityDirectoryOverwriteTx(ctx context.Context, tx *sql.Tx, plan ImportPlan, file PlanFile, p *qualityImportProof) error {
	type item struct {
		key, source, target string
		locked              importSnapshot
	}
	items := []item{{plan.ID + "/" + file.ID, file.Source, file.Target, planSnapshot(file)}}
	for i, s := range p.Sidecars[file.ID] {
		items = append(items, item{qualitySidecarKey(plan, file, i), s.Sidecar.Source, s.Sidecar.Target, s.Sidecar.Snapshot})
	}
	for _, v := range items {
		var cipher directoryOverwriteCipher
		if e := qualityRecord(ctx, tx, "directory_overwrite", v.key, &cipher); e != nil {
			return bad("REVIEW_OVERWRITE_RECEIPT", "新洗版计划缺少实际目录覆盖操作收据")
		}
		raw, e := a.pluginStore.unseal("directory_overwrite", "system", v.key, cipher.Sealed)
		if e != nil {
			return e
		}
		var op directoryOverwriteOperation
		if cleanupJournalStrict(raw, &op) != nil || op.Version != 1 || op.ID != v.key || op.Source != v.source || op.Target != v.target || op.RootID != plan.RootID || op.RootPath != p.RootPath || op.SourceSnapshot != v.locked || op.Method != file.Method || p.Overwrite == nil || !sameOverwritePolicy(op.Policy, *p.Overwrite) {
			return bad("REVIEW_OVERWRITE_RECEIPT", "实际覆盖操作与洗版封存范围不符")
		}
	}
	return nil
}
