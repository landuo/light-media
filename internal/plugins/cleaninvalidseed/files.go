package cleaninvalidseed

import (
	"context"
	"fmt"
	"math/big"
	"path"
	"strings"
	"time"

	py "light-media/internal/releasemeta/legacyanime"
)

func posixPath(s string) string {
	v := path.Clean(s)
	if strings.HasPrefix(s, "//") && !strings.HasPrefix(s, "///") {
		return "/" + v
	}
	return v
}
func (m *Machine) FileOldEnough(p string) bool {
	days, e := integer(m.Config["min_seeding_days"])
	if e != nil {
		return false
	}
	if days.Sign() <= 0 {
		return true
	}
	if !m.Host.Exists(p) {
		return false
	}
	stat, e := m.Host.Stat(p)
	if e != nil {
		return false
	}
	now := m.Now()
	age := (float64(now.Unix()) + float64(now.Nanosecond())/1e9 - stat.MTime) / 86400
	f := new(big.Rat).SetFloat64(age)
	return f != nil && f.Cmp(new(big.Rat).SetInt(days)) >= 0
}
func (m *Machine) Size(p string) (*big.Int, error) {
	if m.Host.IsFile(p) {
		s, e := m.Host.Stat(p)
		return big.NewInt(s.Size), e
	}
	v, e := m.Host.Descendants(p)
	if e != nil {
		return nil, e
	}
	total := new(big.Int)
	for _, p := range v {
		if m.Host.IsFile(p) {
			s, e := m.Host.Stat(p)
			if e != nil {
				return nil, e
			}
			total.Add(total, big.NewInt(s.Size))
		}
	}
	return total, nil
}
func (m *Machine) DetectFiles(ctx context.Context) error {
	m.log(ctx, "info", "开始检测未做种的无效源文件")
	services, e := m.services(ctx)
	if e != nil {
		return e
	}
	all := []*Object{}
	for _, s := range services {
		if s.FalseInstance {
			continue
		}
		v, e := m.getAll(ctx, s)
		if e != nil {
			return e
		}
		all = append(all, v...)
	}
	exclude, e := lines(m.Config["exclude_keywords"])
	if e != nil {
		return e
	}
	dirs := m.Config["download_dirs"]
	if !Truth(dirs) {
		txt := "未配置下载目录，无法检测未做种无效源文件"
		m.log(ctx, "error", txt)
		return m.notice(ctx, "⚠️ 【检测无效源文件】", &txt)
	}
	dirText, e := stringValue(dirs, "split")
	if e != nil {
		return e
	}
	mappings := map[string][]string{}
	roots := []string{}
	for _, line := range strings.Split(dirText, "\n") {
		before, after, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		src := py.Strip(before)
		if _, ok := mappings[src]; !ok {
			mappings[src] = []string{}
			roots = append(roots, src)
		}
		for _, p := range strings.Split(after, ",") {
			if p = py.Strip(p); p != "" {
				mappings[src] = append(mappings[src], p)
			}
		}
	}
	contents := []string{}
	seen := map[string]bool{}
	for _, t := range all {
		var content any
		if SafeHasTimeout(t, "content_path", 2*time.Second) {
			content = SafeGetTimeout(t, "content_path", nil, 3*time.Second)
		} else if SafeHasTimeout(t, "downloadDir", 2*time.Second) || SafeHasTimeout(t, "download_dir", 2*time.Second) || SafeHasTimeout(t, "hashString", 2*time.Second) || SafeHasTimeout(t, "trackerStats", 2*time.Second) {
			d := SafeGetTimeout(t, "downloadDir", nil, 3*time.Second)
			if d == nil {
				d = SafeGetTimeout(t, "download_dir", nil, 3*time.Second)
			}
			if d != nil {
				n := SafeGetTimeout(t, "name", nil, 2*time.Second)
				if n != nil {
					content = str(d) + "/" + str(n)
				}
			}
		}
		if Truth(content) {
			s, ok := content.(string)
			if !ok {
				return source("TypeError", "content path membership")
			}
			if !seen[s] {
				seen[s] = true
				contents = append(contents, s)
			}
		}
	}
	count, filtered := 0, 0
	total := new(big.Int)
	message := "检测未做种无效源文件：\n"
	for _, rawRoot := range roots {
		root := posixPath(rawRoot)
		if !m.Host.Exists(root) {
			m.log(ctx, "error", root+" 不存在，无法检测未做种无效源文件")
			txt := "路径 " + root + " 不存在，无法检测未做种无效源文件"
			if e = m.notice(ctx, "❌ 【检测无效源文件】", &txt); e != nil {
				return e
			}
			continue
		}
		children, e := m.Host.Children(root)
		if e != nil {
			continue
		}
		for _, p := range children {
			name := path.Base(p)
			skip := false
			for _, k := range exclude {
				if strings.Contains(name, k) {
					skip = true
					break
				}
			}
			if skip {
				continue
			}
			exists := false
			for _, d := range mappings[rawRoot] {
				mapped := strings.ReplaceAll(p, rawRoot, d)
				for _, c := range contents {
					if strings.Contains(c, mapped) {
						exists = true
						break
					}
				}
				if exists {
					break
				}
			}
			if exists {
				continue
			}
			if !m.FileOldEnough(p) {
				filtered++
				continue
			}
			count++
			message += fmt.Sprintf("%d. %s\n", count, p)
			size, e := m.Size(p)
			if e != nil {
				return e
			}
			total.Add(total, size)
			if Truth(m.Config["delete_invalid_files"]) {
				if m.Host.IsFile(p) {
					if e = m.Host.Unlink(p); e != nil {
						return e
					}
				} else if m.Host.IsDir(p) {
					if e = m.Host.RemoveTree(p); e != nil {
						return e
					}
				}
			}
		}
	}
	min, e := integer(m.Config["min_seeding_days"])
	if e != nil {
		return e
	}
	if min.Sign() > 0 && filtered > 0 {
		message += fmt.Sprintf("⏰ 时间筛选：过滤掉 %d 个存在时间不足 %s 天的文件\n", filtered, min.String())
	}
	if count > 0 {
		message += fmt.Sprintf("🔍 检测到 %d 个未做种的无效源文件，共占用 %s 空间\n", count, FileSize(total))
		if Truth(m.Config["delete_invalid_files"]) {
			message += "🗑️ ***已删除无效源文件，释放 " + FileSize(total) + " 空间！***\n"
		} else {
			message += "💡 提示：开启删除功能可自动清理这些文件\n"
		}
	} else {
		message += "✅ 未发现无效源文件，所有文件都在正常做种中\n"
	}
	m.log(ctx, "info", message)
	if Truth(m.Config["notify"]) {
		title := "✅ 【检测无效源文件 - 一切正常】"
		if count > 0 {
			if Truth(m.Config["delete_invalid_files"]) {
				title = "🧹 【清理无效源文件 - 已清理】"
			} else {
				title = "🔍 【检测无效源文件 - 发现问题】"
			}
		}
		message = strings.ReplaceAll(message, "_", "\\_")
		if e = m.notice(ctx, title, &message); e != nil {
			return e
		}
	}
	m.log(ctx, "info", "检测无效源文件任务结束")
	return nil
}
