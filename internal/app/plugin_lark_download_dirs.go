package app

import (
	"context"
	"path/filepath"
	"sort"
	"strings"

	"light-media/internal/catalog"
)

const larkAutoDownloadDirectory = "自动匹配目录"

func larkDirectoryPath(d DirectoryRule) string {
	if d.DownloaderID == "" && d.SourcePath != "" {
		return d.SourcePath
	}
	return d.DownloadPath
}
func larkMediaTypeLabel(kind string) string {
	if kind == "movie" {
		return "电影"
	}
	if kind == "tv" {
		return "电视剧"
	}
	return kind
}
func larkUnrestrictedMediaType(kind string) bool { return kind == "" || kind == "mixed" }

// The interaction filters media category even on an unrestricted type. The
// automatic DirectoryHelper.get_dir deliberately has a different branch: an
// unrestricted type matches before testing category. Keep both source rules.
func larkDirectoryMatches(d DirectoryRule, m catalog.Media, category string, interaction bool) bool {
	if m.MediaType == "" {
		return true
	}
	if larkUnrestrictedMediaType(d.MediaType) {
		return !interaction || d.MediaCategory == "" || d.MediaCategory == category
	}
	if d.MediaType != m.MediaType && d.MediaType != larkMediaTypeLabel(m.MediaType) {
		return false
	}
	return d.MediaCategory == "" || d.MediaCategory == category
}
func larkDownloadDirectories(config []DirectoryRule, m catalog.Media, category string) []DirectoryRule {
	// get_download_dirs sorts by priority, retaining configuration order for ties.
	dirs := append([]DirectoryRule{}, config...)
	sort.SliceStable(dirs, func(i, j int) bool { return dirs[i].Priority < dirs[j].Priority })
	out := []DirectoryRule{}
	for _, d := range dirs {
		if larkDirectoryPath(d) != "" && larkDirectoryMatches(d, m, category, true) {
			out = append(out, d)
		}
	}
	if len(out) > 1 {
		out = append([]DirectoryRule{{Name: larkAutoDownloadDirectory, Priority: -1}}, out...)
	}
	return out
}
func larkIsAutoDirectory(d DirectoryRule) bool {
	return d.Name == larkAutoDownloadDirectory && larkDirectoryPath(d) == ""
}
func larkDownloadDirectoryLabel(d DirectoryRule) string {
	path := larkDirectoryPath(d)
	name := d.Name
	if name == "" {
		name = path
	}
	if name == "" {
		name = "下载目录"
	}
	if path != "" && path != name {
		return name + " (" + path + ")"
	}
	return name
}
func larkAppendDownloadClassification(path string, d DirectoryRule, m catalog.Media, category string) string {
	if larkUnrestrictedMediaType(d.MediaType) && d.DownloadTypeFolder {
		path = filepath.Join(path, larkMediaTypeLabel(m.MediaType))
	}
	if d.MediaCategory == "" && d.DownloadCategoryFolder && category != "" {
		path = filepath.Join(path, category)
	}
	return path
}

// nil means the source automatic DirectoryHelper.get_dir(include_unsorted=True),
// not the downloader default. An explicit exact configured root inherits its
// classification; an explicit custom subdirectory remains exactly as supplied.
func larkResolveDownloadPath(config []DirectoryRule, m catalog.Media, category string, explicit *string) (string, error) {
	if explicit == nil {
		for _, d := range config { // get_dir uses configuration order, not priority.
			if larkDirectoryMatches(d, m, category, false) {
				path := larkDirectoryPath(d)
				if path == "" {
					// pathlib.Path(None) fails in the original; do not silently pick
					// a later rule or ask the downloader for its default location.
					return "", bad("DOWNLOAD_DIRECTORY_INVALID", "下载目录配置无效")
				}
				return larkAppendDownloadClassification(path, d, m, category), nil
			}
		}
		return "", bad("DOWNLOAD_DIRECTORY_NOT_FOUND", "未找到下载目录")
	}
	path := strings.TrimPrefix(strings.TrimSpace(*explicit), "local:")
	if e := safeRoot(path); e != nil {
		return "", e
	}
	path = filepath.Clean(path)
	dirs := append([]DirectoryRule{}, config...)
	sort.SliceStable(dirs, func(i, j int) bool { return dirs[i].Priority < dirs[j].Priority })
	allowed := false
	for _, d := range dirs {
		root := larkDirectoryPath(d)
		if root != "" && safeRoot(root) == nil && within(filepath.Clean(root), path) {
			allowed = true
			break
		}
	}
	if !allowed {
		return "", bad("DOWNLOAD_DIRECTORY_NOT_ALLOWED", "保存路径不在允许的下载目录范围内")
	}
	for _, d := range dirs {
		root := larkDirectoryPath(d)
		if root == "" || filepath.Clean(root) != path || !larkDirectoryMatches(d, m, category, false) {
			continue
		}
		return larkAppendDownloadClassification(path, d, m, category), nil
	}
	return path, nil
}
func larkShouldAutoDownload(config, user string) bool {
	if config == "" {
		return false
	}
	if config == "all" {
		return true
	}
	for _, candidate := range strings.Split(config, ",") {
		if user == candidate {
			return true
		}
	}
	return false
}

type larkDownloadDirectorySnapshot struct {
	Media    catalog.Media
	Category string
	Rules    []DirectoryRule
	Choices  []DirectoryRule
}

func (a *App) larkDownloadDirectoryFacts(ctx context.Context, core *pluginLarkCoreExecution, media catalog.Media) (larkDownloadDirectorySnapshot, error) {
	return larkCoreRead(ctx, a, core, "download.directories", media, func(ctx context.Context) (larkDownloadDirectorySnapshot, error) {
		m, category, e := a.importMediaCategory(ctx, media)
		if e != nil {
			return larkDownloadDirectorySnapshot{}, e
		}
		rules := a.Settings().Directories
		return larkDownloadDirectorySnapshot{m, category, rules, larkDownloadDirectories(rules, m, category)}, nil
	})
}
