package app

import (
	"os"
	"path/filepath"
	"strings"

	"light-media/internal/fnos"
)

// The media server owns its library configuration. Match its real directory
// paths (or the same mounted directory) rather than requiring a user GUID map.
func matchFNOSDirectory(source, actual string, libraries []fnos.ServiceLibrary) (string, string, error) {
	type match struct {
		guid, path string
		depth      int
	}
	best := match{depth: -10000}
	ambiguous := false
	for _, library := range libraries {
		for _, directory := range library.Directories {
			if e := safeRoot(directory); e != nil {
				continue
			}
			candidate, depth := "", -1
			if within(directory, actual) {
				candidate = actual
				depth = len(directory)
			}
			// Docker bind aliases can expose the same NAS directory under both
			// container and host paths; compare inode identities before translating.
			info, e := os.Stat(directory)
			if e == nil && info.IsDir() {
				for ancestor := filepath.Dir(source); ancestor != "/" && ancestor != "."; ancestor = filepath.Dir(ancestor) {
					parent, e := os.Stat(ancestor)
					if e == nil && os.SameFile(info, parent) {
						rel, e := filepath.Rel(ancestor, source)
						if e == nil && filepath.IsLocal(rel) {
							candidate = filepath.Join(directory, rel)
							depth = len(ancestor)
						}
						break
					}
				}
			}
			if candidate == "" {
				continue
			}
			// Compare distance to the file, not the length of two unrelated
			// container/host prefixes. The nearest enclosing library wins.
			rel, e := filepath.Rel(directory, candidate)
			if e != nil || !filepath.IsLocal(rel) {
				continue
			}
			depth = -len(strings.Split(rel, string(filepath.Separator)))
			if depth > best.depth {
				best = match{library.GUID, candidate, depth}
				ambiguous = false
			} else if depth == best.depth && (best.guid != library.GUID || best.path != candidate) {
				ambiguous = true
			}
		}
	}
	if best.guid == "" {
		return "", "", bad("FNOS_LIBRARY_NOT_FOUND", "整理目录未包含在飞牛影视已有媒体库中，请检查飞牛库目录与容器挂载")
	}
	if ambiguous {
		return "", "", bad("FNOS_LIBRARY_AMBIGUOUS", "同一整理目录对应多个飞牛媒体库，请在飞牛影视中检查重叠目录")
	}
	return best.guid, best.path, nil
}
