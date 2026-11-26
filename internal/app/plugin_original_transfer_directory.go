package app

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"light-media/internal/plugins"
	"light-media/internal/releasemeta"
)

type originalTransferCandidate struct {
	Item   map[string]any
	Bluray bool
}

// The pinned collector refreshes the selected item once, lists each directory
// through StorageChain, and passes child DTOs directly into recursion. It does
// not refresh every child or sort the provider's order.
func (a *App) originalTransferCandidates(ctx context.Context, path string, stamps map[string]importSnapshot) ([]originalTransferCandidate, map[string][]map[string]any, error) {
	if !a.importSourceAllowed(path, true) || browserInternal(path) {
		return nil, nil, bad("INVALID_SOURCE", "选择的文件不在允许目录内")
	}
	item, err := a.originalMetadataGetFileItem(ctx, "local", path)
	if err != nil || item == nil {
		return nil, nil, err
	}
	listings := map[string][]map[string]any{}
	result := []originalTransferCandidate{}
	visited := 0
	var collect func(map[string]any, int) error
	collect = func(item map[string]any, depth int) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		visited++
		if depth > 16 || visited > 20000 {
			return bad("FILE_SELECTION_LIMIT", "目录扫描超过上限，请缩小选择范围")
		}
		current := strings.TrimSuffix(sourceText(item["path"]), "/")
		if sourceText(item["storage"]) != "local" {
			return bad("UNSUPPORTED_STORAGE", "当前整理只支持本地存储")
		}
		if !a.importSourceAllowed(current, true) || browserInternal(current) {
			return bad("INVALID_SOURCE", "存储模块文件项不在允许目录内")
		}
		if originalTransferBlurayStream.MatchString(current) {
			for parent := filepath.Dir(current); parent != filepath.Dir(parent); parent = filepath.Dir(parent) {
				if filepath.Base(parent) != "BDMV" {
					continue
				}
				root, err := a.originalMetadataGetFileItem(ctx, "local", filepath.Dir(parent))
				if err != nil {
					return err
				}
				if root != nil {
					result = append(result, originalTransferCandidate{root, true})
					return nil
				}
				break
			}
		}
		if sourceText(item["type"]) == "file" {
			result = append(result, originalTransferCandidate{item, false})
			return nil
		}
		groups, err := a.originalPluginModulesForMethod(ctx, "", "list_files")
		if err != nil {
			return err
		}
		if stamps != nil && !originalModuleGroupsHaveMethod(groups, "list_files") {
			if rows, ok := a.localVideoDirectory(ctx, current, stamps); ok {
				listings[current] = rows
				for _, row := range rows {
					result = append(result, originalTransferCandidate{Item: row})
				}
				return nil
			}
		}
		raw, err := a.originalMetadataStorageGroups(ctx, "list_files", map[string]any{"fileitem": item, "recursion": false}, func() (any, error) { return a.originalMetadataNativeList(ctx, item) }, groups)
		if err != nil {
			return err
		}
		children := []map[string]any{}
		if originalModuleResultTruthy(raw) {
			rows, tuple := plugins.DecodeModuleResultTuple(raw)
			if !tuple && json.Unmarshal(raw, &rows) != nil {
				return bad("PLUGIN_MODULE_RESULT_INVALID", "整理存储模块没有返回可迭代的文件项")
			}
			for _, row := range rows {
				child, err := originalMetadataStorageItem(row)
				if err != nil {
					return err
				}
				if child == nil {
					return bad("PLUGIN_MODULE_RESULT_INVALID", "整理目录包含无效文件项")
				}
				children = append(children, child)
			}
		}
		listings[current] = children
		for _, child := range children {
			if sourceText(child["type"]) == "dir" && (sourceText(child["name"]) == "BDMV" || sourceText(child["name"]) == "CERTIFICATE") {
				result = append(result, originalTransferCandidate{item, true})
				return nil
			}
		}
		for _, child := range children {
			// A local module may provide metadata, but cannot turn a selected
			// directory into authority over an unrelated path or a cyclic tree.
			childPath := strings.TrimSuffix(sourceText(child["path"]), "/")
			if filepath.Dir(childPath) != current {
				return bad("INVALID_SOURCE", "存储模块返回了当前目录之外的文件项")
			}
			if sourceText(child["type"]) == "dir" {
				if err := collect(child, depth+1); err != nil {
					return err
				}
			} else {
				result = append(result, originalTransferCandidate{child, false})
			}
		}
		return nil
	}
	if err := collect(item, 0); err != nil {
		return nil, nil, err
	}
	return result, listings, nil
}

// Common flat video folders need neither recursive storage DTO serialization
// nor a second file open. Keep discs, links, extras, hidden entries and errors
// on the regular provider path. No partially read directory is accepted.
func (a *App) localVideoDirectory(ctx context.Context, path string, stamps map[string]importSnapshot) ([]map[string]any, bool) {
	if a.originalMetadataAllowed(path) != nil {
		return nil, false
	}
	dir, err := importDir(path, false)
	if err != nil {
		return nil, false
	}
	defer dir.Close()
	entries, err := dir.ReadDir(129)
	if err != nil || len(entries) == 0 || len(entries) > 128 {
		return nil, false
	}
	if _, err = dir.ReadDir(1); err != io.EOF {
		return nil, false
	}
	for _, entry := range entries {
		if !entry.Type().IsRegular() || !isVideo(entry.Name()) || strings.HasPrefix(entry.Name(), ".") {
			return nil, false
		}
	}
	rows := make([]map[string]any, 0, len(entries))
	observed := make([]importSnapshot, 0, len(entries))
	for _, entry := range entries {
		child := filepath.Join(path, entry.Name())
		if ctx.Err() != nil || !importPath(child) {
			return nil, false
		}
		f, info, err := importRegularAt(dir, child)
		if err != nil {
			return nil, false
		}
		f.Close()
		stamp, err := snapshot(info)
		if err != nil {
			return nil, false
		}
		fields := originalLocalFileItemFields(child, info)
		// Exactly the values yielded by the normal JSON storage chain.
		fields["size"] = json.Number(strconv.FormatInt(info.Size(), 10))
		mtime, _ := json.Marshal(fields["modify_time"])
		fields["modify_time"] = json.Number(mtime)
		rows = append(rows, fields)
		observed = append(observed, stamp)
	}
	if inputs, _ := ctx.Value(manualPreviewInputsKey{}).(*manualPreviewInputs); inputs != nil {
		if inputs.nfoDirectories == nil || len(inputs.nfoDirectories) >= 8 {
			inputs.nfoDirectories = make(map[string]map[string]bool)
		}
		inputs.nfoDirectories[path] = map[string]bool{} // Completely enumerated, all entries are videos.
	}
	for i, row := range rows {
		stamps[sourceText(row["path"])] = observed[i]
	}
	return rows, true
}

var originalTransferBlurayStream = regexp.MustCompile(`(?i)BDMV[/\\]STREAM`)

type originalTransferDirectoryListingsKey struct{}

func (a *App) originalManualStorageScan(ctx context.Context, path string, in manualOrganizeRequest) (any, error) {
	phase := a.performanceStages(ctx, "organize_scan", "storage_candidates")
	defer phase("")
	stamps := make(map[string]importSnapshot)
	candidates, listings, err := a.originalTransferCandidates(ctx, path, stamps)
	if err != nil {
		return nil, err
	}
	phase("inspect_sources")
	if len(candidates) > 1 || len(in.Paths) > 1 {
		if inputs, _ := ctx.Value(manualPreviewInputsKey{}).(*manualPreviewInputs); inputs != nil && inputs.nfoDirectories == nil {
			inputs.nfoDirectories = make(map[string]map[string]bool)
		}
	}
	classifier, err := a.transferMediaClassifier(ctx)
	if err != nil {
		return nil, err
	}
	files := []importScanFile{}
	inherited := map[string]string{}
	metas := map[string]*releasemeta.MetaObject{}
	directory := len(listings) > 0
	var buildMeta func(string, bool) (*releasemeta.MetaObject, error)
	if directory {
		format, e := organizeEpisodeTemplate(in.EpisodeFormat)
		if e != nil {
			return nil, e
		}
		allowed := []originalTransferCandidate{}
		for _, candidate := range candidates {
			if originalDirectoryAllowed(candidate, classifier, in.MinFilesize, format) {
				allowed = append(allowed, candidate)
			}
		}
		buildMeta, e = a.originalDirectoryMetaBuilder(ctx, in)
		if e != nil {
			return nil, e
		}
		candidates, inherited, err = originalPlanTransferDirectory(allowed, classifier, true, func(path string) (*releasemeta.MetaObject, error) {
			meta, e := buildMeta(path, false)
			metas[path] = meta
			return meta, e
		})
		if err != nil {
			return nil, err
		}
	}
	// The collector already groups siblings by directory. Validate every
	// regular file against the same guarded parent, opening at most one
	// directory at a time; the executable plan revalidates the full paths.
	var parent *os.File
	defer func() {
		if parent != nil {
			parent.Close()
		}
	}()
	sourceSnapshot := func(path string, ordinary bool) (importSnapshot, error) {
		if stamp, ok := stamps[path]; ordinary && ok {
			return stamp, nil
		}
		if !directory || !ordinary {
			return snapshotTransferPath(path)
		}
		if !importPath(path) {
			return importSnapshot{}, bad("INVALID_PATH", "文件路径无效")
		}
		dir := filepath.Dir(path)
		if parent == nil || parent.Name() != dir {
			if parent != nil {
				parent.Close()
			}
			var err error
			parent, err = importDir(dir, false)
			if err != nil {
				return importSnapshot{}, err
			}
		}
		file, info, err := importRegularAt(parent, path)
		if err != nil {
			// A storage module may have described a physical disc as a file.
			return snapshotTransferPath(path)
		}
		file.Close()
		return snapshot(info)
	}
	for _, candidate := range candidates {
		item := candidate.Item
		path := strings.TrimSuffix(sourceText(item["path"]), "/")
		if originalTransferBlockedExtra(path) {
			continue
		}
		media, _ := classifier.Media(originalTransferItemClass(item))
		class := originalTransferItemClass(item)
		if !media && !candidate.Bluray && !(directory && classifier.Extra(class)) {
			continue
		}
		frozen, err := originalTransferFrozenItem(item)
		if err != nil {
			return nil, err
		}
		metadataPath := path
		if inherited[path] != "" {
			metadataPath = inherited[path]
		}
		file := importScanFile{Path: path, Parsed: a.parseMediaNameContext(ctx, filepath.Base(metadataPath)), originalItem: frozen, originalDirectoryListings: listings, originalDirectoryPlanned: directory, originalMetadataPath: metadataPath}
		if in.Interactive && in.Preview && directory && candidate.Bluray && class.Type == "dir" {
			// The display plan measures and validates every member before rendering
			// the target. Guard the selected root here without walking the disc twice.
			dir, err := importDir(path, false)
			if err != nil {
				return nil, err
			}
			dir.Close()
		} else {
			stamp, err := sourceSnapshot(path, !candidate.Bluray && class.Type == "file")
			if err != nil {
				return nil, err
			}
			file.Size, file.Snapshot = stamp.Size, &stamp
		}
		if directory {
			meta := metas[metadataPath]
			if meta == nil {
				meta, err = buildMeta(metadataPath, candidate.Bluray)
				if err != nil {
					return nil, err
				}
			}
			file.originalResolvedMeta = meta.DeepCopy()
		}
		if classifier.Subtitle(class) {
			file.materialKind = "subtitle"
		} else if classifier.Audio(class) {
			file.materialKind = "audio"
		}
		file.InferredMedia, file.Numbering, file.IdentityReason = a.transferNFOEvidence(ctx, importNFOAnchor(metadataPath, candidate.Bluray))
		if file.InferredMedia != nil {
			file.IdentitySource = "nfo"
		}
		files = append(files, file)
	}
	return map[string]any{"files": files, "truncated": false}, nil
}
