package app

import "path/filepath"

// The directory list is an ordered rule list, as in MoviePilot. A file belongs
// to the first active rule accepting its path and media type, even when another
// watcher also sees that file through an overlapping bind mount.
func (a *App) directoryRuleOwnsFile(rule DirectoryRule, file importScanFile) bool {
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
	for _, other := range a.Settings().Directories {
		if !other.Enabled || other.MonitorType == "none" || other.RootID == "" {
			continue
		}
		if kind != "" && other.MediaType != "mixed" && other.MediaType != kind {
			continue
		}
		source, err := a.directorySource(other)
		if err != nil {
			continue
		}
		mapped, relevant := sharedDirectoryPath(source, filepath.Dir(file.Path))
		if relevant && within(source, filepath.Join(mapped, filepath.Base(file.Path))) {
			return other.ID == rule.ID
		}
	}
	return false
}
