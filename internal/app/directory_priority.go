package app

import "path/filepath"

// The directory list is an ordered rule list, as in MoviePilot. A file belongs
// to the first active rule accepting its path and media type, even when another
// watcher also sees that file through an overlapping bind mount.
func directoryRuleOwnsFile(settings *Settings, rule DirectoryRule, file importScanFile, identity func(string) (mountedDirectoryID, error)) bool {
	kind := ""
	if file.InferredMedia != nil {
		kind = file.InferredMedia.Type
	}
	if kind == "" && file.Parsed.Title != "" {
		if file.Parsed.Type == "episode" {
			kind = "tv"
		} else {
			kind = "movie"
		}
	}
	// MoviePilot's pickTransferDir (transfer_service.go:1738) is a three-pass
	// choice: an exact media-type match first, then a type-agnostic rule
	// (media_type ""/None/全部), and finally any rule with a library path. Only the
	// first pass existed here, so a movie that landed under a TV rule reported
	// "此下载目录未配置启用的整理规则" instead of being organized at all.
	active := []DirectoryRule{}
	for _, other := range settings.Directories {
		if !other.Enabled || other.MonitorType == "none" || other.RootID == "" {
			continue
		}
		source, err := directorySourceWithSettings(*settings, other)
		if err != nil {
			continue
		}
		mapped, relevant := sharedDirectoryPathWithIdentity(source, filepath.Dir(file.Path), identity)
		if relevant && within(source, filepath.Join(mapped, filepath.Base(file.Path))) {
			active = append(active, other)
		}
	}
	if len(active) == 0 {
		return false
	}
	if kind != "" {
		for _, other := range active {
			if other.MediaType == kind {
				return other.ID == rule.ID
			}
		}
	}
	for _, other := range active {
		if other.MediaType == "" || other.MediaType == "mixed" || other.MediaType == "None" || other.MediaType == "全部" {
			return other.ID == rule.ID
		}
	}
	// Fall through to the first covering rule, like the original's last loop.
	return active[0].ID == rule.ID
}
