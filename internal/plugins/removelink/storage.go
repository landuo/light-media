package removelink

import (
	"errors"
	"fmt"

	py "light-media/internal/releasemeta/legacyanime"
	"strings"
)

type Mapping struct{ Prefix, Storage, Target string }

func ParseMappings(raw string) []Mapping {
	out := []Mapping{}
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !strings.Contains(line, ":") {
			continue
		}
		parts := strings.SplitN(line, ":", 3)
		m := Mapping{Prefix: strings.TrimSpace(parts[0]), Storage: "local"}
		if len(parts) == 2 {
			m.Target = strings.TrimSpace(parts[1])
		} else {
			m.Storage = strings.TrimSpace(parts[1])
			m.Target = strings.TrimSpace(parts[2])
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
func (m *Machine) findStorage(storage, p string) (Item, bool) {
	parent := directoryItem(storage, parent(p))
	if !m.storageExists(parent) {
		return Item{}, false
	}
	for _, x := range m.storageList(parent) {
		if x.Type != "file" || !strings.HasPrefix(x.Name, base(p)) || x.Extension == "" {
			continue
		}
		for _, ext := range m.Ops.MediaExtensions() {
			if "."+py.LowerCase(x.Extension) == ext {
				return x, true
			}
		}
	}
	return Item{}, false
}
func (m *Machine) storageScrap(storage string, video Item) (n int) {
	defer func() {
		if x := recover(); x != nil {
			m.remember(x)
		}
	}()
	if !Truth(m.Config.Scrap) {
		return 0
	}
	parent := directoryItem(storage, parent(video.Path))
	if !m.storageExists(parent) {
		return 0
	}
	for _, x := range m.storageList(parent) {
		if x.Type != "file" {
			continue
		}
		matched := strings.HasPrefix(stem(x.Name), stem(video.Path)) && m.Config.IsScrap(x.Name)
		for _, name := range []string{"poster.jpg", "backdrop.jpg", "fanart.jpg", "banner.jpg", "logo.png"} {
			matched = matched || py.LowerCase(x.Name) == name
		}
		if matched && m.storageDelete(x) {
			n++
		}
	}
	return n
}
func (m *Machine) storageDir(storage, p string) (result Item, found bool) {
	defer func() {
		if x := recover(); x != nil {
			m.remember(x)
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
func (m *Machine) deleteStorageDir(storage string, item Item) bool {
	if s := py.LowerCase(storage); s == "alist" || s == "openlist" {
		item.Type = "file"
		if item.Name == "" {
			item.Name = base(item.Path)
		}
	}
	return m.storageDelete(item)
}
func (m *Machine) storageEmpty(storage string, video Item) (n int) {
	defer func() {
		if x := recover(); x != nil {
			m.remember(x)
		}
	}()
	current := parent(video.Path)
	for current != "" && current != "/" && current != "\\" {
		item, ok := m.storageDir(storage, current)
		if !ok {
			break
		}
		files := m.storageList(item)
		if len(files) > 0 {
			only := true
			for _, x := range files {
				if x.Type != "file" || !m.Config.IsScrap(x.Name) {
					only = false
					break
				}
			}
			if !only {
				break
			}
			for _, x := range files {
				m.storageDelete(x)
			}
			item, ok = m.storageDir(storage, current)
			if !ok || len(m.storageList(item)) > 0 {
				break
			}
		}
		if !m.deleteStorageDir(storage, item) {
			break
		}
		n++
		current = parent(current)
		if current == strings.TrimRight(strings.ReplaceAll(current, base(current), ""), "/\\") {
			break
		}
	}
	return n
}
func (m *Machine) strm(p string) {
	defer func() {
		if x := recover(); x != nil {
			m.remember(x)
		}
	}()
	if Trickplay(p) {
		return
	}
	m.Config.ensureString("strm_path_mappings")
	storage, target, ok := StoragePath(m.Config.Mappings, p)
	if !ok || storage == "" || target == "" {
		return
	}
	video, ok := m.findStorage(storage, target)
	if !ok {
		return
	}
	if !m.storageDelete(video) {
		m.Errors = append(m.Errors, errors.New("storage deletion not confirmed"))
		return
	}
	local, scrap, dirs := 0, 0, 0
	if Truth(m.Config.Scrap) {
		m.scrap(p)
		local = 1
		scrap = m.storageScrap(storage, video)
		dirs = m.storageEmpty(storage, video)
	}
	history := m.history("dest", video.Path)
	if Truth(m.Config.Notify) {
		parts := []string{"🗂️ STRM 文件：" + p, "🗑️ 已删除网盘文件：[" + storage + "] " + video.Path}
		if Truth(m.Config.History) {
			if history {
				parts = append(parts, "📝 已清理转移记录")
			} else {
				parts = append(parts, "📝 无转移记录")
			}
		}
		if Truth(m.Config.Scrap) {
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
		m.notice("🧹 媒体文件清理", "✅ 清理完成\n\n"+strings.Join(parts, "\n"))
	}
}
