package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"strings"
)

type nativeDirectoryAuthorization struct {
	PreviewOnly              bool `json:"preview_only,omitempty"`
	Version                  int
	Plan, InputSHA, RootPath string
	Policy                   importOverwritePolicy
	Sidecars                 map[string][]importSidecar
	Selection                *nativeDirectorySelection
	Extensions               *nativeTransferExtensionObservation `json:"extensions,omitempty"`
}

func nativeDirectoryAuthority(plan ImportPlan, meta importMetadata, selection *nativeDirectorySelection, extensions *nativeTransferExtensionObservation) (nativeDirectoryAuthorization, error) {
	if (plan.DirectorySourceVersion != 1 && plan.DirectorySourceVersion != 2) || meta.Overwrite == nil {
		return nativeDirectoryAuthorization{}, bad("REVIEW_DIRECTORY_SELECTION", "整理缺少实际目录选择来源")
	}
	if selection == nil || !reflect.DeepEqual(meta.Overwrite.Selection, selection.reference(plan.ID)) {
		return nativeDirectoryAuthorization{}, bad("REVIEW_DIRECTORY_SELECTION", "目录选择引用与实际封存输入不一致")
	}
	for _, f := range plan.Files {
		if f.Method != meta.Methods[f.ID] {
			return nativeDirectoryAuthorization{}, bad("REVIEW_DIRECTORY_SELECTION", "整理方式与实际计划不一致")
		}
	}
	if plan.DirectorySourceVersion == 2 {
		if !validTransferExtensionObservation(extensions) || meta.ExtensionInputSHA != extensions.InputSHA {
			return nativeDirectoryAuthorization{}, bad("REVIEW_TRANSFER_EXTENSIONS", "整理扩展名来源未封存")
		}
	} else if extensions != nil || meta.ExtensionInputSHA != "" {
		return nativeDirectoryAuthorization{}, bad("REVIEW_TRANSFER_EXTENSIONS", "旧整理计划不能借用新扩展名来源")
	}
	return nativeDirectoryAuthorization{PreviewOnly: meta.OriginalPreview, Version: plan.DirectorySourceVersion, Plan: plan.ID, InputSHA: qualityImportSHA(plan), RootPath: meta.RootPath, Policy: *meta.Overwrite, Sidecars: meta.Sidecars, Selection: selection, Extensions: extensions}, nil
}
func (a *App) saveNativeDirectoryAuthorizationTx(ctx context.Context, tx *sql.Tx, plan ImportPlan, meta importMetadata, selection *nativeDirectorySelection, supplied ...*nativeTransferExtensionObservation) error {
	extensions := transferExtensionObservation(ctx)
	if len(supplied) > 0 {
		extensions = supplied[0]
	}
	p, e := nativeDirectoryAuthority(plan, meta, selection, extensions)
	if e != nil {
		return e
	}
	if len(mustCleanSeedJSON(p)) > pluginDataValueLimit {
		return bad("REVIEW_DIRECTORY_SELECTION_LIMIT", "本次目录与文件数量超过当前原生持久边界，请拆分整理")
	}
	return a.writeQualitySealedTx(ctx, tx, "native_directory_selection", plan.ID, p)
}
func (a *App) requireNativeDirectoryAuthorizationTx(ctx context.Context, q qualityReader, plan ImportPlan, meta importMetadata) error {
	var actual nativeDirectoryAuthorization
	e := a.readQualitySealed(ctx, q, "native_directory_selection", plan.ID, &actual)
	if errors.Is(e, sql.ErrNoRows) && plan.DirectorySourceVersion == 0 {
		return nil
	}
	if e != nil {
		return bad("REVIEW_DIRECTORY_SELECTION", "整理原始目录选择收据缺失或不可认证")
	}
	want, we := nativeDirectoryAuthority(plan, meta, actual.Selection, actual.Extensions)
	if we != nil {
		return we
	}
	if !reflect.DeepEqual(actual, want) {
		return bad("REVIEW_DIRECTORY_SELECTION", "整理目录、文件、方式或旁车与原始封存选择不一致")
	}
	return nil
}
func (a *App) transitionNativeDirectoryAuthorizationTx(ctx context.Context, tx *sql.Tx, before ImportPlan, beforeMeta importMetadata, after ImportPlan, afterMeta importMetadata) error {
	if e := a.requireNativeDirectoryAuthorizationTx(ctx, tx, before, beforeMeta); e != nil {
		return e
	}
	if before.DirectorySourceVersion == 0 {
		return nil
	}
	var prior nativeDirectoryAuthorization
	if e := a.readQualitySealed(ctx, tx, "native_directory_selection", before.ID, &prior); e != nil {
		return e
	}
	return a.saveNativeDirectoryAuthorizationTx(ctx, tx, after, afterMeta, prior.Selection, prior.Extensions)
}

func (a *App) guardNativeDirectorySelectionReference(ctx context.Context, ref *nativeDirectorySelectionReference) error {
	if ref == nil {
		return nil
	}
	if ref.Version != 1 || ref.Plan == "" {
		return bad("REVIEW_DIRECTORY_SELECTION", "目录选择引用无效")
	}
	var auth nativeDirectoryAuthorization
	if e := a.readQualitySealed(ctx, a.Store.DB, "native_directory_selection", ref.Plan, &auth); e != nil {
		return bad("REVIEW_DIRECTORY_SELECTION", "目录选择原始输入缺失或不可认证")
	}
	if (auth.Version != 1 && auth.Version != 2) || auth.Plan != ref.Plan || auth.Selection == nil || !reflect.DeepEqual(ref, auth.Selection.reference(ref.Plan)) || !reflect.DeepEqual(auth.Policy.Selection, ref) {
		return bad("REVIEW_DIRECTORY_SELECTION", "目录选择引用不属于封存来源")
	}
	if auth.Version == 2 {
		if e := a.guardTransferExtensionObservation(ctx, auth.Extensions); e != nil {
			return e
		}
	}
	return a.guardNativeDirectorySelection(auth.Selection)
}
func (a *App) requireNativeDirectoryPublication(ctx context.Context, key, source, target, rootID, method string, locked importSnapshot, policy importOverwritePolicy) error {
	id, _, ok := strings.Cut(key, "/")
	if !ok || !strings.HasPrefix(id, "plan_") {
		return nil
	} // Immutable pre-producer direct host receipts.
	var plan ImportPlan
	var meta importMetadata
	if e := a.Store.Get(ctx, "import_plan", id, &plan); e != nil {
		return e
	}
	if e := a.Store.Get(ctx, "import_meta", id, &meta); e != nil {
		return e
	}
	if e := a.requireNativeDirectoryAuthorizationTx(ctx, a.Store.DB, plan, meta); e != nil {
		return e
	}
	if plan.DirectorySourceVersion == 0 {
		return nil
	}
	if plan.RootID != rootID || meta.Overwrite == nil || !sameOverwritePolicy(*meta.Overwrite, policy) {
		return bad("REVIEW_DIRECTORY_SELECTION", "实际发布目标或策略不属于原目录选择")
	}
	for _, f := range plan.Files {
		if f.Method != method {
			continue
		}
		if key == plan.ID+"/"+f.ID && source == f.Source && target == f.Target && locked == planSnapshot(f) {
			return nil
		}
		for i, s := range meta.Sidecars[f.ID] {
			if key == fmt.Sprintf("%s/%s/side-%d", plan.ID, f.ID, i) && source == s.Source && target == s.Target && locked == s.Snapshot {
				return nil
			}
		}
	}
	return bad("REVIEW_DIRECTORY_SELECTION", "实际发布文件不属于原目录选择范围")
}
