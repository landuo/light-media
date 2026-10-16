// Package builtin contains the Go implementations of migrated MoviePilot
// plugins. A catalog entry is added only when executable behavior exists.
package builtin

import "light-media/internal/plugins"

func Lookup(pluginID string) (plugins.Manifest, plugins.Handler, bool) {
	switch pluginID {
	case "CategoryEditor":
		return CategoryEditorManifest(), HandleCategoryEditor, true
	default:
		return plugins.Manifest{}, nil, false
	}
}

func Catalog() []plugins.Manifest {
	return []plugins.Manifest{CategoryEditorManifest()}
}
