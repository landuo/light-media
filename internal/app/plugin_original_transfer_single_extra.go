package app

import (
	"context"
	"path/filepath"

	"light-media/internal/transferextensions"
)

func originalTransferExtraKind(path string, classifier transferextensions.Classifier) string {
	item := transferextensions.PathFile(path)
	if classifier.Subtitle(item) {
		return "subtitle"
	}
	if classifier.Audio(item) {
		return "audio"
	}
	return ""
}

// The pinned single-extra planner only borrows a unique sibling main's
// metadata. Neither that main nor any other sibling becomes an execution task.
func (a *App) originalSingleExtraMetadataPath(ctx context.Context, path string) (string, error) {
	classifier, err := a.transferMediaClassifier(ctx)
	if err != nil {
		return "", err
	}
	entries, err := a.originalTransferSiblingItems(ctx, path)
	if err != nil {
		return "", err
	}
	names := []string{}
	for _, entry := range entries {
		if video, _ := classifier.Media(originalTransferItemClass(entry)); video {
			names = append(names, originalTransferItemName(entry))
		}
	}
	name, matched := sourceRelatedMainName(filepath.Base(path), names, classifier)
	if !matched {
		return path, nil
	}
	for _, entry := range entries {
		if originalTransferItemName(entry) != name {
			continue
		}
		video, _ := classifier.Media(originalTransferItemClass(entry))
		if !video {
			continue
		}
		source := sourceText(entry["path"])
		storage := sourceText(entry["storage"])
		if storage != "" && storage != "local" {
			return "", bad("UNSUPPORTED_STORAGE", "当前整理只支持本地存储")
		}
		if err := a.originalMetadataAllowed(source); err != nil {
			return "", err
		}
		return source, nil
	}
	return path, nil
}

func (a *App) manualOrganizeScan(ctx context.Context, path string, supplied ...manualOrganizeRequest) (any, error) {
	classifier, err := a.transferMediaClassifier(ctx)
	if err != nil {
		return nil, err
	}
	kind := originalTransferExtraKind(path, classifier)
	if kind == "" {
		var in manualOrganizeRequest
		if len(supplied) > 0 {
			in = supplied[0]
		}
		return a.originalManualStorageScan(ctx, path, in)
	}
	if !a.importSourceAllowed(path, true) || browserInternal(path) || originalTransferBlockedExtra(path) {
		return nil, bad("INVALID_SOURCE", "选择的附加文件不在允许目录内")
	}
	stamp, err := snapshotPath(path)
	if err != nil {
		return nil, err
	}
	metadataPath, err := a.originalSingleExtraMetadataPath(ctx, path)
	if err != nil {
		return nil, err
	}
	file := importScanFile{Path: path, Size: stamp.Size, Parsed: a.parseMediaName(filepath.Base(metadataPath)), originalMetadataPath: metadataPath, materialKind: kind}
	file.InferredMedia, file.Numbering, file.IdentityReason = a.importNFOEvidence(metadataPath)
	return map[string]any{"files": []importScanFile{file}, "truncated": false}, nil
}
