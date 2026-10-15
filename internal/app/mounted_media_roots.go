package app

import (
	"path/filepath"
	"strings"
)

// Container mounts are deployment configuration, independent of downloaders or
// media-server libraries. Only concrete, existing directory capabilities enter
// the browser; the application data tree never becomes a media root.
func mountedMediaRoots(c *Settings, dataDir, value string) error {
	for _, raw := range filepath.SplitList(value) {
		root := cleanDirectoryInput(strings.TrimSpace(raw))
		if root == "" {
			continue
		}
		if err := safeRoot(root); err != nil {
			return bad("INVALID_MEDIA_MOUNT", "LIGHT_MEDIA_MEDIA_ROOTS必须使用已挂载的具体媒体目录")
		}
		if root == "/config" || within(dataDir, root) || within(root, dataDir) {
			return bad("INVALID_MEDIA_MOUNT", "应用配置目录不能作为媒体挂载目录")
		}
		opened, err := importDir(root, false)
		if err != nil {
			return bad("MEDIA_MOUNT_UNAVAILABLE", "LIGHT_MEDIA_MEDIA_ROOTS目录不存在、不可读或包含符号链接，请检查容器挂载")
		}
		opened.Close()
		found := false
		for _, existing := range c.AllowedRoots {
			if existing == root {
				found = true
				break
			}
		}
		if !found {
			c.AllowedRoots = append(c.AllowedRoots, root)
		}
	}
	return nil
}
