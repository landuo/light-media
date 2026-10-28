package app

import (
	"context"
	"encoding/json"
	"path/filepath"
	"regexp"
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
func (a *App) originalTransferCandidates(ctx context.Context, path string) ([]originalTransferCandidate, map[string][]map[string]any, error) {
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
		raw, err := a.originalMetadataStorage(ctx, "list_files", map[string]any{"fileitem": item, "recursion": false}, func() (any, error) { return a.originalMetadataNativeList(ctx, item) })
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

var originalTransferBlurayStream = regexp.MustCompile(`(?i)BDMV[/\\]STREAM`)

type originalTransferDirectoryListingsKey struct{}

func (a *App) originalManualStorageScan(ctx context.Context, path string, in manualOrganizeRequest) (any, error) {
	candidates, listings, err := a.originalTransferCandidates(ctx, path)
	if err != nil {
		return nil, err
	}
	classifier, err := a.transferMediaClassifier(ctx)
	if err != nil {
		return nil, err
	}
	files := []importScanFile{}
	inherited := map[string]string{}
	metas := map[string]*releasemeta.MetaObject{}
	directory := len(listings) > 0
	var buildMeta func(string) (*releasemeta.MetaObject, error)
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
			meta, e := buildMeta(path)
			metas[path] = meta
			return meta, e
		})
		if err != nil {
			return nil, err
		}
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
		// The publication layer still requires a real guarded source snapshot.
		// A directory candidate is retained as a unit by the collector; it must
		// never silently become a collection of unrelated STREAM video tasks.
		stamp, err := snapshotTransferPath(path)
		if err != nil {
			return nil, err
		}
		frozen, err := originalTransferFrozenItem(item)
		if err != nil {
			return nil, err
		}
		metadataPath := path
		if inherited[path] != "" {
			metadataPath = inherited[path]
		}
		file := importScanFile{Path: path, Size: stamp.Size, Parsed: a.parseMediaName(filepath.Base(metadataPath)), originalItem: frozen, originalDirectoryListings: listings, originalDirectoryPlanned: directory, originalMetadataPath: metadataPath}
		if directory {
			meta := metas[metadataPath]
			if meta == nil {
				meta, err = buildMeta(metadataPath)
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
		file.InferredMedia, file.Numbering, file.IdentityReason = a.importNFOEvidence(metadataPath)
		if file.InferredMedia != nil {
			file.IdentitySource = "nfo"
		}
		files = append(files, file)
	}
	return map[string]any{"files": files, "truncated": false}, nil
}
