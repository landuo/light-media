package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"time"

	"light-media/internal/fileextensions"
	"light-media/internal/transferextensions"
	"light-media/internal/transferoverwrite"
)

// A missing native policy is distinct from a migrated source None. Neither
// silently becomes always; the marker is only changed by an explicit choice.
type importOverwritePolicy struct {
	Selection    *nativeDirectorySelectionReference `json:"selection,omitempty"`
	Policy       transferoverwrite.Policy           `json:"policy"`
	DirectoryID  string                             `json:"directory_id,omitempty"`
	DirectorySHA string                             `json:"directory_sha,omitempty"`
}

func directoryOverwritePolicy(rule DirectoryRule) importOverwritePolicy {
	return importOverwritePolicy{Policy: transferoverwrite.Policy{Known: rule.OverwriteModeKnown, Mode: rule.OverwriteMode}, DirectoryID: rule.ID, DirectorySHA: directoryFingerprint(rule)}
}
func (a *App) resolveImportOverwrite(ctx context.Context, request ImportRequest) (importOverwritePolicy, error) {
	selection := request.directorySelection
	if selection == nil {
		var e error
		selection, _, e = a.selectNativeImportDirectory(ctx, request)
		if e != nil {
			return importOverwritePolicy{}, e
		}
	}
	if selection != nil {
		if e := a.guardNativeDirectorySelection(selection); e != nil {
			return importOverwritePolicy{}, e
		}
		if selection.DirectoryID == "" {
			mode := "never"
			return importOverwritePolicy{Selection: selection.reference(""), Policy: transferoverwrite.Policy{Known: true, Mode: &mode}}, nil
		}
		r, e := a.getDirectory(selection.DirectoryID)
		if e != nil {
			return importOverwritePolicy{}, e
		}
		policy := directoryOverwritePolicy(r)
		policy.Selection = selection.reference("")
		return policy, nil
	}
	// The original explicit custom target_path branch uses never.
	mode := "never"
	return importOverwritePolicy{Policy: transferoverwrite.Policy{Known: true, Mode: &mode}}, nil
}
func (a *App) guardDirectoryOverwritePolicy(ctx context.Context, p importOverwritePolicy) error {
	if e := a.guardNativeDirectorySelectionReference(ctx, p.Selection); e != nil {
		return e
	}
	if p.DirectoryID == "" {
		return nil
	}
	r, e := a.getDirectory(p.DirectoryID)
	if e != nil || directoryFingerprint(r) != p.DirectorySHA {
		return bad("REVIEW_DIRECTORY_CHANGED", "目录覆盖策略已变化，请重新整理")
	}
	return nil
}
func (a *App) overwriteDispatcher(ctx context.Context) (transferoverwrite.Dispatch, error) {
	rows, e := a.Store.DB.QueryContext(ctx, `SELECT plugin_id,version FROM plugin_installations WHERE installed=1 ORDER BY instance_id`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	installed := []transferoverwrite.Installation{}
	for rows.Next() {
		var i transferoverwrite.Installation
		if e = rows.Scan(&i.ID, &i.Version); e != nil {
			return nil, e
		}
		installed = append(installed, i)
	}
	if e = rows.Err(); e != nil {
		return nil, e
	}
	return transferoverwrite.AuditedDispatcher(installed)
}
func overwriteFileItem(path string, stamp importSnapshot) transferoverwrite.FileItem {
	when := time.Unix(0, stamp.ModNS)
	mod := float64(when.Unix()) + float64(when.Nanosecond())/1e9
	return transferoverwrite.FileItem{Path: path, Storage: "local", Type: "file", Name: filepath.Base(path), Basename: strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)), Extension: strings.TrimPrefix(filepath.Ext(path), "."), Size: big.NewInt(stamp.Size), ModifyTime: &mod, Children: []any{}}
}

func (a *App) overwritePathIdentity(ctx context.Context, path string, supplied ...fileextensions.Snapshot) (transferoverwrite.Identity, error) {
	options, e := a.globalRecognitionOptions(ctx)
	if e != nil {
		return transferoverwrite.Identity{}, e
	}
	snapshot, e := a.sourceFileExtensions(ctx)
	if len(supplied) > 0 {
		snapshot = supplied[0]
		e = nil
	}
	if e != nil {
		return transferoverwrite.Identity{}, e
	}
	if !snapshot.Valid() {
		return transferoverwrite.Identity{}, transferextensions.ErrDependency
	}
	options.MediaExts, e = fileextensions.ParserMediaExtensions(snapshot)
	if e != nil {
		return transferoverwrite.Identity{}, e
	}
	return transferoverwrite.PathIdentity(path, options)
}

type directoryOverwriteTarget struct {
	Path     string         `json:"path"`
	Snapshot importSnapshot `json:"snapshot"`
	Backup   string         `json:"backup"`
	Owner    *managedFile   `json:"owner,omitempty"`
}
type directoryOverwriteOperation struct {
	Version           int                        `json:"version"`
	ExtensionInputSHA string                     `json:"extension_input_sha,omitempty"`
	ID                string                     `json:"id"`
	Source            string                     `json:"source"`
	Target            string                     `json:"target"`
	RootID            string                     `json:"root_id"`
	RootPath          string                     `json:"root_path"`
	Method            string                     `json:"method"`
	Policy            importOverwritePolicy      `json:"policy"`
	SourceSnapshot    importSnapshot             `json:"source_snapshot"`
	Targets           []directoryOverwriteTarget `json:"targets"`
	Decision          transferoverwrite.Decision `json:"decision"`
	State             string                     `json:"state"`
	Published         importReceipt              `json:"published"`
}

// readDirectoryOverwrite is a real per-material-file host read, independent of
// subscription target episode filtering. Preview never calls this synchronous
// event or enumerates latest-version deletion candidates.
func (a *App) readDirectoryOverwrite(ctx context.Context, key, source, target, rootID, method string, locked importSnapshot, policy importOverwritePolicy) (directoryOverwriteOperation, error) {
	op := directoryOverwriteOperation{Version: 1, ID: key, Source: source, Target: target, RootID: rootID, Method: method, Policy: policy, SourceSnapshot: locked, Targets: []directoryOverwriteTarget{}, State: "planned"}
	if e := a.guardDirectoryOverwritePolicy(ctx, policy); e != nil {
		return op, e
	}
	root, e := a.importRoot(rootID)
	if e != nil {
		return op, e
	}
	op.RootPath = root.Path
	if !within(root.Path, target) || source == target || !importPath(source) || !importPath(target) {
		return op, bad("INVALID_TARGET", "覆盖目标不在明确的媒体库目录内")
	}
	stamp, e := snapshotPath(source)
	if e != nil || stamp != locked {
		return op, bad("REVIEW_SOURCE_CHANGED", "覆盖检查时源文件已变化")
	}
	observed, e := a.importExtensionObservation(ctx, policy)
	if e != nil {
		return op, e
	}
	handler, e := transferextensions.New(observed.Handler)
	if e != nil {
		return op, e
	}
	op.ExtensionInputSHA = observed.Handler.InputSHA
	input := transferoverwrite.Input{Policy: policy.Policy, Source: overwriteFileItem(source, stamp), TargetKnown: true, TargetPath: target, TargetStorage: "local", TransferType: map[string]string{"hardlink": "link", "copy": "copy"}[method], Extra: handler.Extra(transferextensions.PathFile(source))}
	targetStamp, e := snapshotPath(target)
	if e == nil {
		item := overwriteFileItem(target, targetStamp)
		input.Target = &item
	} else if !errors.Is(e, os.ErrNotExist) {
		// Symlinks and directories have distinct source behavior. The native file
		// publication capability deliberately refuses them instead of following them.
		return op, bad("REVIEW_OVERWRITE_TARGET", "无法确认目标为普通文件，未覆盖："+target)
	}
	if e != nil {
		if _, le := os.Lstat(target); le == nil {
			return op, bad("REVIEW_OVERWRITE_TARGET", "目标是悬空符号链接，需人工核对")
		}
	}
	dispatch, e := a.overwriteDispatcher(ctx)
	if e != nil {
		return op, e
	}
	op.Decision, e = transferoverwrite.Decide(ctx, input, dispatch)
	if e != nil {
		return op, e
	}
	if !op.Decision.Allowed {
		return op, bad("TRANSFER_OVERWRITE_DENIED", op.Decision.Reason)
	}
	paths := []string{}
	if input.Target != nil && op.Decision.Overwrite {
		paths = append(paths, target)
	}
	if op.Decision.DeleteVersions || !policy.Policy.Known && !input.Extra && input.Target == nil {
		candidates, e := a.directoryOverwriteLatestCandidates(ctx, target, observed.Handler)
		if e != nil {
			return op, e
		}
		if !policy.Policy.Known && len(candidates) > 0 {
			return op, transferoverwrite.ErrDependency
		}
		if op.Decision.DeleteVersions {
			paths = append(paths, candidates...)
		}
	}

	for _, path := range paths {
		if path == source {
			return op, bad("REVIEW_OVERWRITE_SOURCE", "原版覆盖会移除当前源文件，已保留文件等待人工处理")
		}
		old, e := snapshotPath(path)
		if e != nil {
			return op, bad("REVIEW_OVERWRITE_TARGET", "待覆盖版本不可确认，未移除任何文件")
		}
		target := directoryOverwriteTarget{Path: path, Snapshot: old, Backup: ".light-media-overwrite-" + tokenHash(key + "\x00" + path)[:32] + ".part"}
		var owner managedFile
		e = a.Store.Get(ctx, "managed_file", tokenHash(path), &owner)
		if e == nil {
			if owner.Path != path || owner.RootID != rootID || owner.Snapshot != old {
				return op, bad("REVIEW_OVERWRITE_OWNER", "目标归属或文件身份已经变化")
			}
			target.Owner = &owner
		} else if !errors.Is(e, sql.ErrNoRows) {
			return op, e
		}
		op.Targets = append(op.Targets, target)
	}
	return op, nil
}
func sameOverwriteOperation(a, b directoryOverwriteOperation) bool {
	a.State, b.State = "", ""
	a.Published, b.Published = importReceipt{}, importReceipt{}
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}

// Unknown policy may authorize a new target only after proving latest would
// remove no existing version. Unknown is not the source's explicit None.
func (a *App) directoryOverwriteLatestCandidates(ctx context.Context, target string, supplied ...fileextensions.Snapshot) ([]string, error) {
	snapshot, e := a.sourceFileExtensions(ctx)
	if len(supplied) > 0 {
		snapshot = supplied[0]
		e = nil
	}
	if e != nil {
		return nil, e
	}
	classifier, e := transferextensions.New(snapshot)
	if e != nil {
		return nil, e
	}
	want, e := a.overwritePathIdentity(ctx, target, snapshot)
	if e != nil {
		return nil, e
	}
	dir, e := importDir(filepath.Dir(target), false)
	if errors.Is(e, os.ErrNotExist) {
		return []string{}, nil
	}
	if e != nil {
		return nil, e
	}
	defer dir.Close()
	entries, e := dir.ReadDir(2001)
	if e != nil && !errors.Is(e, io.EOF) {
		return nil, e
	}
	if len(entries) > 2000 {
		return nil, bad("REVIEW_OVERWRITE_LIMIT", "覆盖版本目录超过原生读取边界")
	}
	paths := []string{}
	for _, entry := range entries {
		path := filepath.Join(filepath.Dir(target), entry.Name())
		media, _ := classifier.Media(transferextensions.PathFile(path))
		if path == target || entry.IsDir() || !media {
			continue
		}
		old, e := a.overwritePathIdentity(ctx, path, snapshot)
		if e != nil {
			return nil, e
		}
		if transferoverwrite.VersionMatches(want, old) {
			paths = append(paths, path)
		}
	}
	return paths, nil
}
