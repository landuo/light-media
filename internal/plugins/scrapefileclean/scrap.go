package scrapefileclean

import (
	"errors"
	py "light-media/internal/releasemeta/legacyanime"
	"strings"
	"unicode/utf8"
)

var errSourceRootLoop = errors.New("source_non_terminating_root_cleanup")

func SameMediaScrapName(path, mediaStem string) bool {
	name := base(path)
	if !strings.HasPrefix(name, mediaStem) {
		return false
	}
	if len(name) == len(mediaStem) {
		return true
	}
	r, _ := utf8.DecodeRuneInString(name[len(mediaStem):])
	return !pythonAlnum(r)
}
func BelongsToOtherMedia(name, mediaStem string, other []string) bool {
	for _, stem := range other {
		if stem == mediaStem {
			return true
		}
		if utf8.RuneCountInString(stem) <= utf8.RuneCountInString(mediaStem) || !strings.HasPrefix(name, stem) || len(name) == len(stem) {
			continue
		}
		r, _ := utf8.DecodeRuneInString(name[len(stem):])
		if !pythonAlnum(r) {
			return true
		}
	}
	return false
}
func (m *Machine) Scrap(path string) (err error) {
	defer capture(&err)
	m.mu.Lock()
	defer m.mu.Unlock()
	m.scrap(cleanPath(path))
	return nil
}
func (m *Machine) scrap(path string) {
	if !m.Config.Scrap {
		return
	}
	if m.Config.KeywordExcluded(path) || m.Config.Excluded(path, m.Home, m.UserHome) || !m.Ops.Exists(parent(path)) {
		return
	}
	func() {
		defer func() {
			if p := recover(); p != nil {
				m.remember(p)
			}
		}()
		if !m.Config.IsScrap(path) {
			rows, e := m.Ops.Children(parent(path))
			checked(e)
			others := []string{}
			for _, file := range rows {
				if !file.Dir && base(file.Path) != base(path) && !m.Config.IsScrap(file.Path) {
					others = append(others, stem(file.Path))
				}
			}
			rows, e = m.Ops.Children(parent(path))
			checked(e)
			for _, file := range rows {
				if !SameMediaScrapName(file.Path, stem(path)) || m.Config.KeywordExcluded(file.Path) || m.Config.Excluded(file.Path, m.Home, m.UserHome) || BelongsToOtherMedia(base(file.Path), stem(path), others) {
					continue
				}
				if file.Dir && py.LowerCase(suffix(file.Path)) == ".trickplay" {
					checked(m.Ops.RemoveTree(file.Path))
				} else if m.Config.IsScrap(file.Path) {
					checked(m.Ops.Unlink(file.Path))
				}
			}
		}
	}()
	m.empty(path)
}
func (m *Machine) onlyScrap(path string) bool {
	rows, e := m.Ops.Children(path)
	if e != nil {
		return false
	}
	for _, file := range rows {
		if file.Dir {
			if py.LowerCase(suffix(file.Path)) != ".trickplay" {
				return false
			}
		} else if !m.Config.IsScrap(file.Path) {
			return false
		}
	}
	return true
}
func (m *Machine) empty(path string) {
	roots := []string{}
	for _, dir := range strings.Split(m.Config.text("monitor_dirs", "split"), "\n") {
		dir = py.Strip(dir)
		if dir != "" {
			roots = append(roots, normalized(dir, m.Home, m.UserHome))
		}
	}
	for {
		dir := parent(path)
		if m.Config.Excluded(dir, m.Home, m.UserHome) || m.Config.KeywordExcluded(dir) || !m.Ops.Exists(dir) || contains(roots, normalized(dir, m.Home, m.UserHome)) {
			return
		}
		func() {
			defer func() {
				if p := recover(); p != nil {
					m.remember(p)
				}
			}()
			if m.onlyScrap(dir) {
				rows, e := m.Ops.Children(dir)
				checked(e)
				for _, file := range rows {
					if m.Config.KeywordExcluded(file.Path) || m.Config.Excluded(file.Path, m.Home, m.UserHome) {
						continue
					}
					if file.Dir {
						checked(m.Ops.RemoveTree(file.Path))
					} else {
						checked(m.Ops.Unlink(file.Path))
					}
				}
			}
		}()
		nonempty := false
		func() {
			defer func() {
				if p := recover(); p != nil {
					m.remember(p)
				}
			}()
			rows, e := m.Ops.Children(dir)
			checked(e)
			if len(rows) > 0 {
				nonempty = true
				return
			}
			checked(m.Ops.RemoveDir(dir))
			if m.Config.Notify {
				checked(m.Ops.Notice("📁 目录清理", "🗑️ 清理空目录："+dir))
			}
		}()
		if nonempty {
			return
		}
		if dir == path {
			panic(effectFailure{errSourceRootLoop})
		}
		path = dir
	}
}
