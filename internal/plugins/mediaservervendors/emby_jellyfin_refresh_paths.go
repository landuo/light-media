package mediaservervendors

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"slices"

	"light-media/internal/plugins"
	"light-media/internal/plugins/mediaserverruntime"
	"light-media/internal/pyregex"
)

func ejString(s string) json.RawMessage { b, _ := json.Marshal(s); return b }

// Exact lexical PosixPath component comparison: discard '.'/empty separators,
// preserve '..', preserve the special exactly-two-leading-slashes anchor. This
// never calls filepath.Clean/Abs/EvalSymlinks/stat/resolve or reads a file.
func ejRefreshPath(raw json.RawMessage) ([][]rune, error) {
	points, err := plugins.DecodeSystemConfigJSONString(raw)
	if err != nil {
		return nil, &mediaserverruntime.SourceError{Class: "TypeError"}
	}
	result := [][]rune{}
	lead := 0
	for lead < len(points) && points[lead] == '/' {
		lead++
	}
	if lead > 0 {
		anchor := []rune{'/'}
		if lead == 2 {
			anchor = []rune{'/', '/'}
		}
		result = append(result, anchor)
	}
	start := lead
	for end := lead; end <= len(points); end++ {
		if end != len(points) && points[end] != '/' {
			continue
		}
		part := points[start:end]
		if len(part) > 0 && !(len(part) == 1 && part[0] == '.') {
			result = append(result, append([]rune(nil), part...))
		}
		start = end + 1
	}
	return result, nil
}
func ejRefreshRelative(path, parent [][]rune) bool {
	anchor := func(parts [][]rune) string {
		if len(parts) > 0 && (slices.Equal(parts[0], []rune{'/'}) || slices.Equal(parts[0], []rune{'/', '/'})) {
			return string(parts[0])
		}
		return ""
	}
	if anchor(path) != anchor(parent) {
		return false
	}
	if len(parent) > len(path) {
		return false
	}
	for i := range parent {
		if !slices.Equal(path[i], parent[i]) {
			return false
		}
	}
	return true
}
func ejRefreshRegexText(raw json.RawMessage) (string, error) {
	points, err := plugins.DecodeSystemConfigJSONString(raw)
	if err != nil {
		return "", &mediaserverruntime.SourceError{Class: "TypeError"}
	}
	for _, point := range points {
		if point >= 0xd800 && point <= 0xdfff {
			return "", errors.New("refresh regex codepoint requires a concrete compatible adapter")
		}
	}
	return string(points), nil
}

func (v *ejVendor) libraryIDByTypedItem(ctx context.Context, item mediaserverruntime.ValidatedRefreshItem) (json.RawMessage, error) {
	if !ejTruth(item.Title) || !ejTruth(item.Year) || item.Type == mediaserverruntime.RefreshMediaTypeNone {
		return ejCopy(json.RawMessage(`null`)), nil
	}
	// Every non-None actual MediaType Enum is unequal to MOVIE.value (str).
	// The original typed MOVIE therefore takes the Series branch too. There
	// is deliberately no invented string-type/duck-typed movie shortcut.
	id, err := v.seriesID(ctx, item.Title, item.Year)
	if err != nil {
		return nil, err
	}
	if ejTruth(id) {
		return id, nil
	}
	path, err := ejRefreshPath(item.TargetPath)
	if err != nil {
		return nil, err
	}
	v.mu.RLock()
	foldersRaw := ejCopy(v.state.Folders)
	present := v.state.FoldersPresent
	v.mu.RUnlock()
	if !present {
		return nil, &mediaserverruntime.SourceError{Class: "AttributeError"}
	}
	folders, err := ejIter(foldersRaw)
	if err != nil {
		return nil, err
	}
	for _, folderRaw := range folders {
		folder, e := ejObject(folderRaw)
		if e != nil {
			return nil, &mediaserverruntime.SourceError{Class: "AttributeError"}
		}
		subfoldersRaw := folder.get("SubFolders")
		subs, e := ejIter(subfoldersRaw)
		if e != nil {
			return nil, e
		}
		for _, subRaw := range subs {
			sub, e := ejObject(subRaw)
			if e != nil {
				v.report("match_subdirectory", "debug", "AttributeError")
				continue
			}
			subpath, e := ejRefreshPath(sub.get("Path"))
			if e != nil {
				if ordinary := v.refreshCaughtLevel(ctx, "match_subdirectory", "debug", e); ordinary != nil {
					return nil, ordinary
				}
				continue
			}
			if ejRefreshRelative(path, subpath) {
				return ejCopy(folder.get("Id")), nil
			}
		}
	}
	// The original category is interpolated unescaped into re.search. Reuse
	// the existing bounded Python-pattern engine; do not quote it as a literal
	// or introduce a new timeout policy. This does not claim all CPython regex.
	var category string
	categoryRaw := bytes.TrimSpace(item.Category)
	if len(categoryRaw) > 0 && categoryRaw[0] == '"' {
		category, err = ejRefreshRegexText(categoryRaw)
	} else {
		category, err = ejPythonText(categoryRaw, false)
	}
	if err != nil {
		return nil, err
	}
	for _, folderRaw := range folders {
		folder, e := ejObject(folderRaw)
		if e != nil {
			return nil, &mediaserverruntime.SourceError{Class: "AttributeError"}
		}
		subfoldersRaw := folder.get("SubFolders")
		subs, e := ejIter(subfoldersRaw)
		if e != nil {
			return nil, e
		}
		for _, subRaw := range subs {
			sub, e := ejObject(subRaw)
			if e != nil {
				return nil, &mediaserverruntime.SourceError{Class: "AttributeError"}
			}
			if !ejTruth(sub.get("Path")) {
				continue
			}
			text, e := ejRefreshRegexText(sub.get("Path"))
			if e != nil {
				return nil, e
			}
			pattern, e := pyregex.Compile(`[/\\]`+category, false)
			if e != nil {
				return nil, errors.New("refresh category pattern is invalid or unsupported")
			}
			matched, e := pattern.MatchString(text)
			if e != nil {
				return nil, errors.New("refresh category pattern execution failed")
			}
			if matched {
				return ejCopy(folder.get("Id")), nil
			}
		}
	}
	return ejString("/"), nil
}
