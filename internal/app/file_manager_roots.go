package app

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Publicly configured roots, the default library and current directory rules
// grant access. An auto-generated destination retained only for import history
// does not grant access on its own.
func fileManagerRootSettings(settings Settings) Settings {
	current := map[string]bool{}
	for _, rule := range settings.Directories {
		current[ruleLibraryPath(settings, rule)] = true
	}
	roots := Settings{AllowedRoots: settings.AllowedRoots}
	for _, root := range settings.Roots {
		if !current[root.Path] && root.ID != settings.DefaultRoot {
			generated, _, err := physicalTargetRoot(nil, root.Path, root.Kind)
			if err == nil && generated == root {
				continue
			}
		}
		roots.Roots = append(roots.Roots, root)
	}
	return roots
}

func (a *App) fileManagerRoots() []browserRoot {
	return browserRootsFromSettings(fileManagerRootSettings(a.Settings()))
}

func (a *App) fileManagerRoot(id, path string) (browserRoot, error) {
	return findBrowserRoot(a.fileManagerRoots(), id, path)
}

type fileManagerBoundaries struct {
	appDir    string
	libraries []string
	roots     []string
	identity  func(string) (mountedDirectoryID, error)
}

func (a *App) fileManagerProtection() *fileManagerBoundaries {
	settings := a.Settings()
	grants := fileManagerRootSettings(settings)
	roots := browserRootsFromSettings(grants)
	p := &fileManagerBoundaries{appDir: a.Dir, identity: cachedMountedDirectoryIdentity()}
	for _, root := range grants.Roots {
		p.libraries = append(p.libraries, root.Path)
	}
	for _, rule := range settings.Directories {
		// Native saves register the physical target. Older or partially migrated
		// rules can also name it directly.
		if path := ruleLibraryPath(settings, rule); path != "" && !slices.Contains(p.libraries, path) {
			p.libraries = append(p.libraries, path)
		}
		if path, err := directorySourceWithSettings(settings, rule); err == nil && !slices.Contains(p.roots, path) {
			p.roots = append(p.roots, path)
		}
	}
	for _, path := range grants.AllowedRoots {
		// Directory rules historically appended their sources to AllowedRoots.
		// A nested record adds no boundary once its parent grants access.
		if !p.hasAuthorizedParent(roots, path) && !slices.Contains(p.roots, path) {
			p.roots = append(p.roots, path)
		}
	}
	mountInfo, _ := os.ReadFile("/proc/self/mountinfo")
	mounts := append(filepath.SplitList(os.Getenv("LIGHT_MEDIA_MEDIA_ROOTS")), fileManagerMountPaths(string(mountInfo))...)
	for _, path := range mounts {
		if slices.ContainsFunc(roots, func(root browserRoot) bool { return within(root.Path, path) }) && !slices.Contains(p.roots, path) {
			p.roots = append(p.roots, path)
		}
	}
	return p
}

func (p *fileManagerBoundaries) hasAuthorizedParent(roots []browserRoot, path string) bool {
	return slices.ContainsFunc(roots, func(root browserRoot) bool {
		if root.Path == path {
			return false
		}
		mapped, overlap := sharedDirectoryPathWithIdentity(root.Path, path, p.identity)
		return overlap && mapped != root.Path && within(root.Path, mapped)
	})
}

// Field five is the mount point in this process's mount namespace. Kernel
// octal escapes keep spaces and backslashes inside a single field.
func fileManagerMountPaths(mountInfo string) []string {
	unescape := strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`)
	var paths []string
	for line := range strings.SplitSeq(mountInfo, "\n") {
		fields := strings.Fields(line)
		if len(fields) > 5 && strings.HasPrefix(fields[4], "/") {
			paths = append(paths, unescape.Replace(fields[4]))
		}
	}
	return paths
}

func (p *fileManagerBoundaries) reason(path string) string {
	if _, overlap := sharedDirectoryPathWithIdentity(p.appDir, path, p.identity); overlap {
		return "应用数据目录受保护"
	}
	containsRoot := func(root string) bool {
		mapped, overlap := sharedDirectoryPathWithIdentity(root, path, p.identity)
		return overlap && within(mapped, root)
	}
	if slices.ContainsFunc(p.libraries, containsRoot) {
		return "媒体库根目录不能改名或删除"
	}
	if slices.ContainsFunc(p.roots, containsRoot) {
		return "授权目录或挂载点不能改名或删除"
	}
	return ""
}
