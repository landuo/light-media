package mediaservervendors

import (
	"bytes"
	"encoding/json"
	"slices"

	"light-media/internal/plugins"
)

// Complete source __normalize_dir_path / __is_subpath semantics. The input
// from a validated RefreshMediaItem is a lexical Path spelling, not permission
// to resolve/read that filesystem path. A root slash becomes empty in source;
// do not replace this with filepath.Clean, Abs, EvalSymlinks or path containment.
func ugreenRefreshNormalizePath(raw json.RawMessage) ([]rune, error) {
	if ejNull(raw) {
		return []rune{}, nil
	}
	var points []rune
	var err error
	if bytes.TrimSpace(raw)[0] == '"' {
		points, err = plugins.DecodeSystemConfigJSONString(raw)
	} else {
		var text string
		text, err = ejPythonText(raw, false)
		points = []rune(text)
	}
	if err != nil {
		return nil, err
	}
	for i := range points {
		if points[i] == '\\' {
			points[i] = '/'
		}
	}
	for len(points) > 0 && points[len(points)-1] == '/' {
		points = points[:len(points)-1]
	}
	return points, nil
}
func ugreenRefreshIsSubpath(path, parent json.RawMessage) (bool, error) {
	pathPoints, err := ugreenRefreshNormalizePath(path)
	if err != nil {
		return false, err
	}
	parentPoints, err := ugreenRefreshNormalizePath(parent)
	if err != nil {
		return false, err
	}
	if len(pathPoints) == 0 || len(parentPoints) == 0 {
		return false, nil
	}
	if slices.Equal(pathPoints, parentPoints) {
		return true, nil
	}
	if len(pathPoints) <= len(parentPoints) || pathPoints[len(parentPoints)] != '/' {
		return false, nil
	}
	return slices.Equal(pathPoints[:len(parentPoints)], parentPoints), nil
}
