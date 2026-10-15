package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"sort"
	"strings"
)

type directoryDestination struct {
	Rule                       DirectoryRule
	Path, Root, Mode, Snapshot string
}

func directoryFingerprint(rule DirectoryRule) string {
	rule.Enabled = false // Enabling an unchanged destination resumes its own pending downloads.
	b, _ := json.Marshal(rule)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
func (a *App) getDirectory(key string) (DirectoryRule, error) {
	for _, rule := range a.Settings().Directories {
		if rule.ID == key {
			return rule, nil
		}
	}
	return DirectoryRule{}, bad("DIRECTORY_NOT_FOUND", "目录规则不存在，请重新选择下载目录")
}
func (a *App) enabledDirectory(key string) (DirectoryRule, error) {
	rule, e := a.getDirectory(key)
	if e != nil {
		return rule, e
	}
	if !rule.Enabled || rule.MonitorType == "none" {
		return rule, bad("DIRECTORY_MONITOR_DISABLED", "此目录未启用自动整理，文件保留在下载目录")
	}
	return rule, nil
}
func directorySourceWithSettings(c Settings, rule DirectoryRule) (string, error) {
	if rule.DownloaderID == "" {
		if rule.SourcePath == "" {
			return "", bad("DIRECTORY_SOURCE_REQUIRED", "请输入应用可见的源目录")
		}
		return rule.SourcePath, nil
	}
	for _, d := range c.Downloaders {
		if d.ID == rule.DownloaderID {
			p, e := mapDownloaderPath(c, d, rule.DownloadPath)
			if e != nil {
				return "", e
			}
			if rule.SourcePath != "" && rule.SourcePath != p {
				return "", bad("DIRECTORY_MAPPING_MISMATCH", "应用源目录须与下载器路径映射一致")
			}
			return p, nil
		}
	}
	return "", bad("DOWNLOADER_NOT_CONFIGURED", "目录规则关联的下载器不存在")
}
func (a *App) directorySource(rule DirectoryRule) (string, error) {
	return directorySourceWithSettings(a.Settings(), rule)
}
func sourceAllowed(c Settings, path string) bool {
	for _, root := range c.AllowedRoots {
		if within(root, path) {
			return true
		}
	}
	return false
}
func validateDirectoryRules(c Settings) error {
	if len(c.Directories) > 64 {
		return bad("DIRECTORY_LIMIT", "目录规则最多64条")
	}
	seen := map[string]bool{}
	for _, r := range c.Directories {
		if r.ID == "" || seen[r.ID] {
			return bad("DUPLICATE_ID", "目录规则ID为空或重复")
		}
		seen[r.ID] = true
		if strings.TrimSpace(r.Name) == "" || len([]rune(r.Name)) > 80 {
			return bad("INVALID_DIRECTORY_NAME", "请输入80字以内的目录名称")
		}
		if r.MediaType != "movie" && r.MediaType != "tv" && r.MediaType != "mixed" {
			return bad("INVALID_DIRECTORY_TYPE", "请选择电影、剧集或全部类型")
		}
		if r.Mode != "hardlink" && r.Mode != "copy" && r.Mode != "auto" {
			return bad("INVALID_IMPORT_MODE", "整理方式仅支持硬链接、复制或自动选择，源文件始终保留")
		}
		if r.MonitorType != "monitor" && r.MonitorType != "downloader" && r.MonitorType != "none" {
			return bad("INVALID_MONITOR_TYPE", "请选择目录监听、下载器完成监听或不监控")
		}
		if r.DownloaderID == "" && (r.DownloadPath != "" || r.MonitorType == "downloader") {
			return bad("DOWNLOADER_NOT_CONFIGURED", "下载器完成监听必须选择下载器")
		}
		if r.DownloaderID != "" {
			if e := safeRoot(r.DownloadPath); e != nil {
				return e
			}
		}
		source, e := directorySourceWithSettings(c, r)
		if e != nil {
			return e
		}
		if e = safeRoot(source); e != nil {
			return e
		}
		if !sourceAllowed(c, source) {
			return bad("DIRECTORY_SOURCE_NOT_ALLOWED", "源目录须位于设置中已授权的文件访问目录内")
		}
		found := false
		for _, root := range c.Roots {
			if within(root.Path, source) || within(source, root.Path) {
				return bad("DIRECTORY_OVERLAP", "下载源目录与媒体库目录不能相同或互相包含")
			}
			if root.ID == r.RootID {
				found = true
				if r.MediaType != "mixed" && root.Kind != "mixed" && root.Kind != r.MediaType {
					return bad("INVALID_ROOT", "媒体库类型与目录规则不符")
				}
				if r.MediaType == "mixed" && root.Kind != "mixed" {
					return bad("INVALID_ROOT", "全部类型规则须使用混合媒体库，或分开创建电影与剧集规则")
				}
			}
		}
		if !found {
			return bad("ROOT_NOT_CONFIGURED", "请为整理规则选择媒体库目录")
		}
		for _, other := range c.Directories {
			if other.ID != r.ID && r.Enabled && other.Enabled && r.MonitorType == "monitor" && other.MonitorType == "monitor" && (r.MediaType == other.MediaType || r.MediaType == "mixed" || other.MediaType == "mixed") {
				otherSource, e := directorySourceWithSettings(c, other)
				if e != nil {
					return e
				}
				if within(source, otherSource) || within(otherSource, source) {
					return bad("DIRECTORY_MONITOR_OVERLAP", "相同影视类型的已启用目录监听不能重叠或嵌套，请拆分源目录")
				}
			}
			if other.ID != r.ID && other.DownloaderID == r.DownloaderID && other.DownloadPath == r.DownloadPath && (r.DownloaderID != "" || other.SourcePath == r.SourcePath) && (other.MediaType == r.MediaType || other.MediaType == "mixed" || r.MediaType == "mixed") {
				return bad("DIRECTORY_AMBIGUOUS", "同一源目录和媒体类型不能配置多个整理规则")
			}
		}
	}
	return nil
}
func (a *App) resolveDownloadDestination(d DownloaderConfig, key, path, legacyRoot, kind string) (directoryDestination, error) {
	c := a.Settings()
	out := directoryDestination{Path: cleanDirectoryInput(path)}
	if key != "" {
		r, e := a.getDirectory(key)
		if e != nil {
			return out, e
		}
		out.Rule = r
		if (r.DownloaderID != "" && r.DownloaderID != d.ID) || (r.MediaType != kind && r.MediaType != "mixed") {
			return out, bad("DIRECTORY_MISMATCH", "下载目录与所选下载器或影视类型不匹配")
		}
		if out.Path == "" {
			out.Path = r.DownloadPath
		}
		if r.DownloaderID != "" && !within(r.DownloadPath, out.Path) {
			return out, bad("DIRECTORY_MISMATCH", "下载保存目录必须位于所选目录规则内")
		}
	}
	if out.Path == "" {
		out.Path = d.DownloadPath
	}
	if e := safeRoot(out.Path); e != nil {
		return out, e
	}
	if key == "" && path == "" && (legacyRoot != "" || c.DefaultRoot != "") { // Existing pre-directory API records retain their reviewed behavior.
		root, e := a.root(legacyRoot, kind)
		if e != nil {
			return out, e
		}
		out.Root = root.ID
		out.Mode = c.AutoImportMode
		if out.Mode == "" {
			out.Mode = "hardlink"
		}
		return out, nil
	}
	source, e := mapDownloaderPath(c, d, out.Path)
	if e != nil {
		return out, e
	}
	if !sourceAllowed(c, source) {
		return out, bad("DIRECTORY_SOURCE_NOT_ALLOWED", "下载目录映射后须在已授权文件目录内")
	}
	for _, root := range c.Roots {
		if within(root.Path, source) || within(source, root.Path) {
			return out, bad("DIRECTORY_OVERLAP", "请使用独立下载目录，由整理规则写入媒体库")
		}
	}
	if out.Rule.ID != "" && out.Rule.DownloaderID == "" && !within(out.Rule.SourcePath, source) {
		return out, bad("DIRECTORY_MISMATCH", "下载路径映射后不在所选目录监听范围内")
	}
	if out.Rule.ID == "" {
		best := -1
		for _, r := range c.Directories {
			if (r.MediaType != kind && r.MediaType != "mixed") || (r.DownloaderID != "" && r.DownloaderID != d.ID) {
				continue
			}
			ruleSource, err := directorySourceWithSettings(c, r)
			if err != nil || !within(ruleSource, source) {
				continue
			}
			score := len(ruleSource) * 2
			if r.DownloaderID == d.ID {
				score++
			}
			if score > best {
				out.Rule = r
				best = score
			}
		}
	}
	if out.Rule.ID != "" {
		out.Snapshot = directoryFingerprint(out.Rule)
		out.Root = out.Rule.RootID
		out.Mode = out.Rule.Mode
	}
	return out, nil
}
func (a *App) managedDirectories(ctx context.Context) (any, error) {
	c := a.Settings()
	rows := []map[string]any{}
	for _, rule := range c.Directories {
		source, e := a.directorySource(rule)
		var state managedConnection
		_ = a.Store.Get(ctx, "directory_status", rule.ID, &state)
		if e != nil {
			state.Status = "error"
			state.Error = e.Error()
		}
		if !rule.Enabled || rule.MonitorType == "none" {
			state.Status = "disabled"
			state.Error = ""
		} else if state.Status == "" {
			state.Status = "pending"
		}
		var pending int
		_ = a.Store.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM records WHERE kind='intent' AND json_extract(payload,'$.directory_id')=? AND json_extract(payload,'$.state') NOT IN ('imported','cancelled')`, rule.ID).Scan(&pending)
		var filesPending int
		_ = a.Store.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM records WHERE kind='directory_file' AND json_extract(payload,'$.directory_id')=? AND json_extract(payload,'$.state') NOT IN ('complete','imported','baseline','tracking')`, rule.ID).Scan(&filesPending)
		pending += filesPending
		rows = append(rows, map[string]any{"config": rule, "application_path": source, "connection": state, "pending_count": pending})
	}
	for i := range c.Downloaders {
		c.Downloaders[i].Password = ""
	}
	return map[string]any{"directories": rows, "downloaders": c.Downloaders, "roots": c.Roots}, nil
}
func (a *App) saveManagedDirectory(ctx context.Context, in DirectoryRule) (any, error) {
	a.settingsMu.Lock()
	defer a.settingsMu.Unlock()
	c := a.Settings()
	in.Name = strings.TrimSpace(in.Name)
	in.DownloadPath = cleanDirectoryInput(in.DownloadPath)
	in.SourcePath = cleanDirectoryInput(in.SourcePath)
	if in.ID == "" {
		in.ID = id("directory_")
	}
	if in.MediaType == "" {
		in.MediaType = "mixed"
	}
	if in.Mode == "" {
		in.Mode = "hardlink"
	}
	if in.MonitorType == "" {
		in.MonitorType = "none"
	}
	index := -1
	for i, r := range c.Directories {
		if r.ID == in.ID {
			index = i
			break
		}
	}
	if index < 0 {
		c.Directories = append(c.Directories, in)
	} else {
		c.Directories[index] = in
	}
	if e := a.saveSettings(ctx, c); e != nil {
		return nil, e
	}
	return a.managedDirectories(ctx)
}
func (a *App) deleteManagedDirectory(ctx context.Context, key string) (any, error) {
	a.settingsMu.Lock()
	defer a.settingsMu.Unlock()
	c := a.Settings()
	filtered := []DirectoryRule{}
	found := false
	for _, r := range c.Directories {
		if r.ID == key {
			found = true
		} else {
			filtered = append(filtered, r)
		}
	}
	if !found {
		return nil, bad("DIRECTORY_NOT_FOUND", "目录规则不存在")
	}
	c.Directories = filtered
	if e := a.saveSettings(ctx, c); e != nil {
		return nil, e
	}
	return a.managedDirectories(ctx)
}
func (a *App) directoryOptions(downloader, kind string) (any, error) {
	d, e := a.downloader(downloader)
	if e != nil {
		return nil, e
	}
	rows := []map[string]any{}
	for _, r := range a.Settings().Directories {
		if r.DownloaderID == d.ID && (kind == "" || r.MediaType == kind || r.MediaType == "mixed") {
			source, _ := a.directorySource(r)
			rows = append(rows, map[string]any{"id": r.ID, "name": r.Name, "download_path": r.DownloadPath, "root_id": r.RootID, "enabled": r.Enabled, "monitor_type": r.MonitorType, "application_path": source})
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i]["name"].(string) < rows[j]["name"].(string) })
	return map[string]any{"directories": rows, "default_path": d.DownloadPath}, nil
}
func (a *App) directoryAPI(r *http.Request, path string) (any, error) {
	ctx := r.Context()
	if path == "options" && r.Method == "GET" {
		return a.directoryOptions(r.URL.Query().Get("downloader_id"), r.URL.Query().Get("media_type"))
	}
	if path == "manage" {
		if r.Method == "GET" {
			return a.managedDirectories(ctx)
		}
		if r.Method == "POST" {
			var in DirectoryRule
			if e := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20)).Decode(&in); e != nil {
				return nil, bad("INVALID_JSON", "目录配置格式无效")
			}
			return a.saveManagedDirectory(ctx, in)
		}
	}
	parts := strings.Split(strings.TrimPrefix(path, "manage/"), "/")
	if len(parts) == 2 && parts[1] == "files" && r.Method == "GET" {
		return a.listDirectoryFiles(ctx, parts[0])
	}
	if len(parts) == 1 && r.Method == "DELETE" {
		return a.deleteManagedDirectory(ctx, parts[0])
	}
	if len(parts) == 2 && parts[1] == "check" && r.Method == "POST" {
		rule, e := a.enabledDirectory(parts[0])
		if e != nil {
			return nil, e
		}
		if rule.MonitorType == "monitor" {
			if e = a.pollDirectoryFileRule(ctx, rule); e != nil {
				return nil, e
			}
			return a.managedDirectories(ctx)
		}
		if e := a.pollDownloads(ctx); e != nil {
			return nil, e
		}
		return a.managedDirectories(ctx)
	}
	return nil, &APIError{"NOT_FOUND", "接口不存在", 404}
}

func cleanDirectoryInput(path string) string {
	path = strings.TrimSpace(path)
	if path != "/" {
		path = strings.TrimRight(path, "/")
	}
	return path
}

// Empty mappings mean the downloader and application share the same path.
// This shortcut is only a capability within already authorized source roots.
func mapDownloaderPath(c Settings, d DownloaderConfig, path string) (string, error) {
	if len(d.Mappings) > 0 {
		return mapPath(path, d.Mappings)
	}
	if e := safeRoot(path); e != nil {
		return "", e
	}
	if !sourceAllowed(c, path) {
		return "", bad("PATH_MAPPING_REQUIRED", "此下载器路径不在应用授权目录内，请配置路径映射")
	}
	return path, nil
}
