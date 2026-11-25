package removelink

import (
	"errors"
	"fmt"

	"strings"
)

type Mapping struct{ Prefix, Storage, Target string }

func ParseMappings(raw string) []Mapping {
	out := []Mapping{}
	for _, line := range strings.Split(raw, "\n") {
		line = sourceStrip(line)
		if line == "" || !strings.Contains(line, ":") {
			continue
		}
		parts := strings.SplitN(line, ":", 3)
		m := Mapping{Prefix: sourceStrip(parts[0]), Storage: "local"}
		if len(parts) == 2 {
			m.Target = sourceStrip(parts[1])
		} else {
			m.Storage = sourceStrip(parts[1])
			m.Target = sourceStrip(parts[2])
		}
		found := false
		for i, x := range out {
			if x.Prefix == m.Prefix {
				out[i] = m
				found = true
				break
			}
		}
		if !found {
			out = append(out, m)
		}
	}
	return out
}
func StoragePath(raw, p string) (string, string, bool) {
	p = cleanPath(p)
	for _, m := range ParseMappings(raw) {
		if strings.HasPrefix(p, m.Prefix) {
			rel := strings.TrimLeft(strings.TrimPrefix(p, m.Prefix), "/")
			target := strings.TrimRight(m.Target, "/") + "/" + rel
			target = strings.TrimSuffix(target, ".strm")
			return m.Storage, target, true
		}
	}
	return "", "", false
}
func directoryItem(storage, p string) Item {
	if !strings.HasSuffix(p, "/") {
		p += "/"
	}
	return Item{Storage: storage, Path: p, Type: "dir"}
}
func (m *operation) findStorage(storage, p string) (Item, bool) {
	parentPath := parent(p)
	parent := directoryItem(storage, parentPath)
	if !m.storageExists(parent) {
		m.log("DEBUG", "父目录不存在: ["+storage+"] "+parentPath)
		return Item{}, false
	}
	files := m.storageList(parent)
	if len(files) == 0 {
		m.log("DEBUG", "父目录为空: ["+storage+"] "+parentPath)
		return Item{}, false
	}
	for _, x := range files {
		if x.Type != "file" || !strings.HasPrefix(x.Name, base(p)) || x.Extension == "" {
			continue
		}
		for _, ext := range m.Ops.MediaExtensions() {
			if "."+sourceLower(x.Extension) == ext {
				m.log("INFO", "找到匹配的视频文件: ["+storage+"] "+x.Path)
				return x, true
			}
		}
	}
	m.log("DEBUG", "未找到匹配的视频文件: ["+storage+"] "+p)
	return Item{}, false
}
func (m *operation) storageScrap(storage string, video Item) (n int) {
	defer func() {
		if x := recover(); x != nil {
			m.remember(x)
			m.logRecovered("清理网盘刮削文件失败: ["+storage+"] "+video.Path+" - ", x)
		}
	}()
	if !Truth(m.currentConfig().Scrap) {
		return 0
	}
	parentPath := parent(video.Path)
	parent := directoryItem(storage, parentPath)
	if !m.storageExists(parent) {
		m.log("DEBUG", "网盘父目录不存在: ["+storage+"] "+parentPath)
		return 0
	}
	files := m.storageList(parent)
	if len(files) == 0 {
		m.log("DEBUG", "网盘父目录为空: ["+storage+"] "+parentPath)
		return 0
	}
	for _, x := range files {
		if x.Type != "file" {
			continue
		}
		matched := strings.HasPrefix(stem(x.Name), stem(video.Path)) && m.currentConfig().IsScrap(x.Name)
		for _, name := range []string{"poster.jpg", "backdrop.jpg", "fanart.jpg", "banner.jpg", "logo.png"} {
			matched = matched || sourceLower(x.Name) == name
		}
		if matched {
			if m.storageDelete(x) {
				m.log("INFO", "删除网盘刮削文件: ["+storage+"] "+x.Path)
				n++
			} else {
				m.log("WARNING", "删除网盘刮削文件失败: ["+storage+"] "+x.Path)
			}
		}
	}
	m.log("INFO", fmt.Sprintf("网盘刮削文件清理完成: [%s] %s，删除了 %d 个文件", storage, parentPath, n))
	return n
}
func (m *operation) storageDir(storage, p string) (result Item, found bool) {
	defer func() {
		if x := recover(); x != nil {
			m.remember(x)
			m.logRecoveredLevel("DEBUG", "获取网盘目录信息失败: ["+storage+"] "+p+" - ", x)
		}
	}()
	parent := parent(p)
	if parent == p {
		return Item{}, false
	}
	item := directoryItem(storage, parent)
	if !m.storageExists(item) {
		return Item{}, false
	}
	for _, x := range m.storageList(item) {
		if x.Type == "dir" && x.Name == base(p) {
			return x, true
		}
	}
	return Item{}, false
}
func (m *operation) deleteStorageDir(storage string, item Item) bool {
	return m.storageDelete(item)
}
func (m *operation) storageEmpty(storage string, video Item) (n int) {
	defer func() {
		if x := recover(); x != nil {
			m.remember(x)
			m.logRecovered("清理网盘空目录失败: ["+storage+"] "+video.Path+" - ", x)
		}
	}()
	current := parent(video.Path)
	for current != "" && current != "/" && current != "\\" {
		item, ok := m.storageDir(storage, current)
		if !ok {
			m.log("DEBUG", "网盘目录不存在: ["+storage+"] "+current)
			break
		}
		files := m.storageList(item)
		initiallyEmpty := len(files) == 0
		if !initiallyEmpty {
			only := true
			for _, x := range files {
				if x.Type != "file" || !m.currentConfig().IsScrap(x.Name) {
					only = false
					break
				}
			}
			if !only {
				break
			}
			for _, x := range files {
				if m.storageDelete(x) {
					m.log("INFO", "删除网盘刮削文件: ["+storage+"] "+x.Path)
				} else {
					m.log("WARNING", "删除网盘刮削文件失败: ["+storage+"] "+x.Path)
				}
			}
			item, ok = m.storageDir(storage, current)
			if !ok || len(m.storageList(item)) > 0 {
				break
			}
		}
		if !m.deleteStorageDir(storage, item) {
			if initiallyEmpty {
				m.log("WARNING", "删除网盘空目录失败: ["+storage+"] "+current)
			}
			break
		}
		n++
		m.log("INFO", "删除网盘空目录: ["+storage+"] "+current)
		current = parent(current)
		if current == strings.TrimRight(strings.ReplaceAll(current, base(current), ""), "/\\") {
			break
		}
	}
	if n > 0 {
		m.log("INFO", fmt.Sprintf("网盘空目录清理完成: [%s] 删除了 %d 个目录", storage, n))
	}
	return n
}
func (m *operation) strm(p string) {
	defer func() {
		if x := recover(); x != nil {
			m.remember(x)
			m.logRecoveredTrace("处理 strm 文件删除失败: "+p+" - ", x)
		}
	}()
	if Trickplay(p) {
		return
	}
	m.log("INFO", "处理 strm 文件删除: "+p)
	m.currentConfig().ensureString("strm_path_mappings")
	storage, target, ok := StoragePath(m.currentConfig().Mappings, p)
	if !ok || storage == "" || target == "" {
		m.log("WARNING", "无法找到 strm 文件 "+p+" 对应的网盘路径映射")
		return
	}
	video, ok := m.findStorage(storage, target)
	if !ok {
		m.log("INFO", "网盘中未找到对应的视频文件: ["+storage+"] "+target)
		return
	}
	m.log("INFO", "准备删除网盘文件: ["+storage+"] "+video.Path)
	if !m.storageDelete(video) {
		m.recordError(errors.New("storage deletion not confirmed"), false)
		m.log("ERROR", "删除网盘文件失败: ["+storage+"] "+video.Path)
		return
	}
	m.log("INFO", "成功删除网盘文件: ["+storage+"] "+video.Path)
	local, scrap, dirs := 0, 0, 0
	if Truth(m.currentConfig().Scrap) {
		m.scrap(p)
		local = 1
		scrap = m.storageScrap(storage, video)
		dirs = m.storageEmpty(storage, video)
	}
	history := m.history("dest", video.Path)
	if Truth(m.currentConfig().Notify) {
		parts := []string{"🗂️ STRM 文件：" + p, "🗑️ 已删除网盘文件：[" + storage + "] " + video.Path}
		if Truth(m.currentConfig().History) {
			if history {
				parts = append(parts, "📝 已清理转移记录")
			} else {
				parts = append(parts, "📝 无转移记录")
			}
		}
		if Truth(m.currentConfig().Scrap) {
			msg := "🖼️ 无刮削文件需要清理"
			if local > 0 && scrap > 0 {
				msg = fmt.Sprintf("🖼️ 已清理刮削文件（本地+网盘 %d 个）", scrap)
			} else if local > 0 {
				msg = "🖼️ 已清理本地刮削文件"
			} else if scrap > 0 {
				msg = fmt.Sprintf("🖼️ 已清理网盘刮削文件（%d 个）", scrap)
			}
			if dirs > 0 {
				msg += fmt.Sprintf("，清理空目录 %d 个", dirs)
			}
			parts = append(parts, msg)
		}
		m.notice("strm", "🧹 媒体文件清理", "✅ 清理完成\n\n"+strings.Join(parts, "\n"))
	}
}
