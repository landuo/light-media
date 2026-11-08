package app

import (
	"path/filepath"
	"slices"
	"strings"
)

// Container mounts are deployment configuration, independent of downloaders or
// media-server libraries. Only concrete, existing directory capabilities enter
// the browser; the application data tree never becomes a media root.
func mountedMediaRoots(c *Settings, dataDir, value string) error {
	roots, err := mountedDirectoryRoots(dataDir, value, "LIGHT_MEDIA_MEDIA_ROOTS")
	if err != nil {
		return err
	}
	for _, root := range roots {
		if !slices.Contains(c.AllowedRoots, root) {
			c.AllowedRoots = append(c.AllowedRoots, root)
		}
	}
	return nil
}

func mountedDirectoryRoots(dataDir, value, setting string) ([]string, error) {
	var roots []string
	for _, raw := range filepath.SplitList(value) {
		root := cleanDirectoryInput(strings.TrimSpace(raw))
		if root == "" {
			continue
		}
		if err := safeRoot(root); err != nil {
			return nil, bad("INVALID_MEDIA_MOUNT", setting+" 必须使用已挂载的具体目录")
		}
		if root == "/config" || within(dataDir, root) || within(root, dataDir) {
			return nil, bad("INVALID_MEDIA_MOUNT", "应用配置目录不能作为资源挂载目录")
		}
		opened, err := importDir(root, false)
		if err != nil {
			return nil, bad("MEDIA_MOUNT_UNAVAILABLE", setting+" 目录不存在、不可读或包含符号链接，请检查容器挂载")
		}
		opened.Close()
		if !slices.Contains(roots, root) {
			roots = append(roots, root)
		}
	}
	return roots, nil
}
