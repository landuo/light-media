package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"light-media/internal/transferextensions"
	"path/filepath"
	"strings"
)

// Roots remain an internal identity for existing plans and assets. A physical
// target does not require a media-server library GUID or a separate setup step.
func physicalTarget(c *Settings, path, kind string) (string, error) {
	path = cleanDirectoryInput(path)
	if err := safeRoot(path); err != nil {
		return "", err
	}
	for _, root := range c.Roots {
		if root.Path == path && (root.Kind == kind || root.Kind == "mixed") {
			return root.ID, nil
		}
	}
	sum := sha256.Sum256([]byte(path + "\x00" + kind))
	key := "path_" + hex.EncodeToString(sum[:12])
	c.Roots = append(c.Roots, Root{ID: key, Name: filepath.Base(path), Path: path, Kind: kind})
	return key, nil
}

func (a *App) ensureImportTarget(ctx context.Context, path, kind string, files []ImportFileRequest, manual ...bool) (string, error) {
	path = cleanDirectoryInput(path)
	if err := safeRoot(path); err != nil {
		return "", err
	}
	for _, file := range files {
		if len(manual) > 0 && manual[0] {
			continue // Explicit manual operations check the final file target.
		}
		_, physicalOverlap := sharedDirectoryPath(filepath.Dir(file.Path), path)
		if within(path, file.Path) || physicalOverlap {
			return "", bad("DIRECTORY_OVERLAP", "源目录与整理目标目录不能相同或互相包含")
		}
	}
	opened, err := importDir(path, false)
	if err != nil {
		return "", err
	}
	opened.Close()
	a.settingsMu.Lock()
	defer a.settingsMu.Unlock()
	c := a.Settings()
	before := len(c.Roots)
	key, err := physicalTarget(&c, path, kind)
	if err != nil {
		return "", err
	}
	if len(c.Roots) != before {
		if err = a.saveSettings(ctx, c); err != nil {
			return "", err
		}
	}
	return key, nil
}

func ruleLibraryPath(c Settings, rule DirectoryRule) string {
	if rule.LibraryPath != "" {
		return rule.LibraryPath
	}
	for _, root := range c.Roots {
		if root.ID == rule.RootID {
			return root.Path
		}
	}
	return ""
}

func publicDirectory(c Settings, rule DirectoryRule) map[string]any {
	source, _ := directorySourceWithSettings(c, rule)
	return map[string]any{"id": rule.ID, "name": rule.Name, "source_path": source, "library_path": ruleLibraryPath(c, rule), "media_type": rule.MediaType, "mode": rule.Mode, "overwrite_mode": rule.OverwriteMode, "overwrite_mode_known": rule.OverwriteModeKnown, "monitor_type": rule.MonitorType, "enabled": rule.Enabled, "include_existing": rule.IncludeExisting, "renaming": importBool(rule.Renaming, true), "scraping": importBool(rule.Scraping, true), "library_type_folder": importBool(rule.LibraryTypeFolder, false)}
}

func preparePhysicalDirectory(c *Settings, rule *DirectoryRule) error {
	rule.Name = strings.TrimSpace(rule.Name)
	rule.SourcePath = cleanDirectoryInput(rule.SourcePath)
	rule.LibraryPath = cleanDirectoryInput(rule.LibraryPath)
	if rule.ID == "" {
		// New native directory: source TransferDirectoryConf defaults to None.
		// Existing IDs lacking the marker remain explicitly unmigrated.
		if rule.OverwriteMode == nil {
			rule.OverwriteModeKnown = true
		}
		rule.ID = id("directory_")
	}
	if rule.MediaType == "" {
		rule.MediaType = "mixed"
	}
	if rule.Mode == "" {
		rule.Mode = "hardlink"
	}
	if rule.MonitorType == "" {
		rule.MonitorType = "none"
	}
	if rule.LibraryPath == "" && rule.RootID != "" {
		return nil
	} // Unedited encrypted legacy record.
	if err := safeRoot(rule.SourcePath); err != nil {
		return err
	}
	if !sourceAllowed(*c, rule.SourcePath) {
		c.AllowedRoots = append(c.AllowedRoots, rule.SourcePath)
	}
	if rule.LibraryPath == "" {
		if rule.MonitorType != "none" {
			return bad("DIRECTORY_TARGET_REQUIRED", "启用自动整理前请填写媒体库目录")
		}
		rule.DownloaderID, rule.DownloadPath, rule.RootID = "", "", ""
		return nil
	}
	if err := safeRoot(rule.LibraryPath); err != nil {
		return err
	}
	if _, overlap := sharedDirectoryPath(rule.SourcePath, rule.LibraryPath); overlap {
		return bad("DIRECTORY_OVERLAP", "源目录与整理目标目录不能相同或互相包含")
	}
	root, err := physicalTarget(c, rule.LibraryPath, rule.MediaType)
	if err != nil {
		return err
	}
	rule.RootID = root
	rule.DownloaderID, rule.DownloadPath = "", ""
	if !sourceAllowed(*c, rule.SourcePath) {
		c.AllowedRoots = append(c.AllowedRoots, rule.SourcePath)
	}
	return nil
}

func (a *App) prepareManualImport(ctx context.Context, request ImportRequest) (ImportRequest, error) {
	if len(request.Files) == 0 || len(request.Files) > 2000 {
		return request, bad("INVALID_IMPORT", "请选择1至2000个视频文件")
	}
	classifier, err := a.transferMediaClassifier(ctx)
	if err != nil {
		return request, err
	}
	if !validMedia(request.MediaType, request.TMDBID) {
		for i, file := range request.Files {
			video, _ := classifier.Media(transferextensions.PathFile(file.Path))
			if !a.importSourceAllowed(file.Path, request.Manual) || !video {
				return request, bad("INVALID_SOURCE", "请选择容器媒体目录内的视频文件")
			}
			hint, numbering, reason := a.importNFOEvidence(file.Path)
			scanned := importScanFile{Path: file.Path, Parsed: a.parseManualImportFileContext(ctx, file), InferredMedia: hint, Numbering: numbering, IdentityReason: reason}
			inferred, err := a.directoryFileIdentity(ctx, DirectoryRule{MediaType: "mixed", Mode: request.Mode}, scanned)
			if err != nil {
				return request, err
			}
			if request.MediaType != "" && request.MediaType != "auto" && request.MediaType != inferred.MediaType {
				return request, bad("IMPORT_IDENTITY_MISMATCH", "自动识别结果与指定媒体类型不符")
			}
			if request.TMDBID != 0 && request.TMDBID != inferred.TMDBID {
				return request, bad("IMPORT_IDENTITY_MISMATCH", "自动识别结果与指定TMDB ID不符")
			}
			request.MediaType, request.TMDBID = inferred.MediaType, inferred.TMDBID
			if file.Season == 0 && !file.SeasonExplicit {
				request.Files[i].Season = inferred.Files[0].Season
				request.Files[i].SeasonExplicit = inferred.Files[0].SeasonExplicit
			}
			if len(file.Episodes) == 0 {
				request.Files[i].Episodes = inferred.Files[0].Episodes
				if !file.SeasonExplicit || file.Season == inferred.Files[0].Season {
					request.Files[i].SourceEpisodeID = inferred.Files[0].SourceEpisodeID
				}
			}
		}
	}
	selection, matchedRule, err := a.selectNativeImportDirectory(ctx, request)
	if err != nil {
		return request, err
	}
	request.directorySelection = selection
	if request.IntentID != "" && request.TargetPath == "" && request.RootID != "" && matchedRule != nil {
		root, e := a.importRoot(request.RootID)
		if e != nil {
			return request, e
		}
		if ruleLibraryPath(a.Settings(), *matchedRule) != root.Path {
			return request, bad("REVIEW_DIRECTORY_SELECTION", "实际目录规则选择了不同媒体库，不能借用旧下载的目标权限")
		}
	}
	if request.Mode == "" {
		request.Mode = "hardlink"
		if matchedRule != nil && matchedRule.Mode != "" {
			request.Mode = matchedRule.Mode
		}
	}
	optionsRule := matchedRule
	if optionsRule != nil {
		if request.Renaming == nil {
			request.Renaming = optionsRule.Renaming
		}
		if request.Scraping == nil {
			request.Scraping = optionsRule.Scraping
		}
		if request.LibraryTypeFolder == nil {
			request.LibraryTypeFolder = optionsRule.LibraryTypeFolder
		}
	}
	if request.TargetPath == "" && request.RootID == "" {
		if matchedRule != nil {
			request.RootID = matchedRule.RootID
		}
		if request.RootID == "" {
			return request, bad("DIRECTORY_TARGET_REQUIRED", "源目录未匹配整理规则，请指定整理目标目录")
		}
	}
	return request, nil
}
