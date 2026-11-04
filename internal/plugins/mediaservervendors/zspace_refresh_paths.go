package mediaservervendors

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
	"math/big"
	"slices"
	"strconv"

	"light-media/internal/plugins"
	"light-media/internal/plugins/mediaserverruntime"
	"light-media/internal/pyregex"
	"light-media/internal/sourcejson"
)

// JSON-domain iteration; arrays keep unvalidated later elements so a first
// match can return before an otherwise erroneous subsequent element is read.
func zspaceRefreshIterable(raw json.RawMessage) ([]json.RawMessage, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || !json.Valid(raw) {
		return nil, errors.New("invalid owned refresh response JSON")
	}
	switch raw[0] {
	case '[':
		var values []json.RawMessage
		err := json.Unmarshal(raw, &values)
		return values, err
	case '{':
		decoder := json.NewDecoder(bytes.NewReader(raw))
		if _, err := decoder.Token(); err != nil {
			return nil, err
		}
		result := []json.RawMessage{}
		seen := map[string]bool{}
		for decoder.More() {
			token, err := decoder.Token()
			if err != nil {
				return nil, err
			}
			key := token.(string)
			var discard json.RawMessage
			if err := decoder.Decode(&discard); err != nil {
				return nil, err
			}
			if !seen[key] {
				result = append(result, zspaceString(key))
				seen[key] = true
			}
		}
		return result, nil
	case '"':
		points, err := plugins.DecodeSystemConfigJSONString(raw)
		if err != nil {
			return nil, err
		}
		out := make([]json.RawMessage, 0, len(points))
		for _, point := range points {
			encoded, e := plugins.EncodeSystemConfigJSONString([]rune{point})
			if e != nil {
				return nil, e
			}
			out = append(out, encoded)
		}
		return out, nil
	default:
		return nil, &mediaserverruntime.SourceError{Class: "TypeError"}
	}
}

// Python JSON equality for the finite IDs/field values used here. Integer
// precision is retained; decimal/exponent values have original binary64 value.
func zspaceRefreshNumber(raw json.RawMessage) (*big.Rat, int, bool) {
	if bytes.Equal(raw, []byte("true")) {
		return new(big.Rat).SetInt64(1), 0, true
	}
	if bytes.Equal(raw, []byte("false")) {
		return new(big.Rat), 0, true
	}
	if len(raw) == 0 || !((raw[0] >= '0' && raw[0] <= '9') || raw[0] == '-') {
		return nil, 0, false
	}
	if bytes.ContainsAny(raw, ".eE") {
		value, _ := strconv.ParseFloat(string(raw), 64)
		if math.IsInf(value, 1) {
			return nil, 1, true
		}
		if math.IsInf(value, -1) {
			return nil, -1, true
		}
		return new(big.Rat).SetFloat64(value), 0, true
	}
	value, ok := new(big.Int).SetString(string(raw), 10)
	if !ok {
		return nil, 0, false
	}
	return new(big.Rat).SetInt(value), 0, true
}
func zspaceRefreshEqual(a, b json.RawMessage) bool {
	a, b = bytes.TrimSpace(a), bytes.TrimSpace(b)
	if len(a) == 0 {
		a = zspaceNull
	}
	if len(b) == 0 {
		b = zspaceNull
	}
	if !json.Valid(a) || !json.Valid(b) {
		return false
	}
	an, ai, aok := zspaceRefreshNumber(a)
	bn, bi, bok := zspaceRefreshNumber(b)
	if aok || bok {
		return aok && bok && ai == bi && (ai != 0 || (an != nil && bn != nil && an.Cmp(bn) == 0))
	}
	if a[0] != b[0] {
		return false
	}
	switch a[0] {
	case 'n':
		return true
	case '"':
		aa, e := plugins.DecodeSystemConfigJSONString(a)
		if e != nil {
			return false
		}
		bb, e := plugins.DecodeSystemConfigJSONString(b)
		return e == nil && slices.Equal(aa, bb)
	case '[':
		var aa, bb []json.RawMessage
		if json.Unmarshal(a, &aa) != nil || json.Unmarshal(b, &bb) != nil || len(aa) != len(bb) {
			return false
		}
		for i := range aa {
			if !zspaceRefreshEqual(aa[i], bb[i]) {
				return false
			}
		}
		return true
	case '{':
		aa, e := zspaceObject(a)
		if e != nil {
			return false
		}
		bb, e := zspaceObject(b)
		if e != nil || len(aa) != len(bb) {
			return false
		}
		for key, value := range aa {
			other, ok := bb[key]
			if !ok || !zspaceRefreshEqual(value, other) {
				return false
			}
		}
		return true
	}
	return false
}

// Exact lexical PosixPath component comparison: discard '.'/empty separators,
// preserve '..', preserve the special exactly-two-leading-slashes anchor. This
// never calls filepath.Clean/Abs/EvalSymlinks/stat/resolve or reads a file.
func zspaceRefreshPath(raw json.RawMessage) ([][]rune, error) {
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
func zspaceRefreshRelative(path, parent [][]rune) bool {
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
func zspaceRefreshRegexText(raw json.RawMessage) (string, error) {
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

func (v *ZSpace) libraryIDByTypedItem(ctx context.Context, item mediaserverruntime.ValidatedRefreshItem) (json.RawMessage, error) {
	if !zspaceTruth(item.Title) || !zspaceTruth(item.Year) || item.Type == mediaserverruntime.RefreshMediaTypeNone {
		return zspaceCopy(zspaceNull), nil
	}
	// Every non-None actual MediaType Enum is unequal to MOVIE.value (str).
	// The original typed MOVIE therefore takes the Series branch too. There
	// is deliberately no invented string-type/duck-typed movie shortcut.
	id, err := v.seriesID(ctx, item.Title, item.Year)
	if err != nil {
		return nil, err
	}
	if zspaceTruth(id) {
		return id, nil
	}
	path, err := zspaceRefreshPath(item.TargetPath)
	if err != nil {
		return nil, err
	}
	v.mu.RLock()
	foldersRaw := zspaceCopy(v.state.Folders)
	present := v.state.FoldersPresent
	v.mu.RUnlock()
	if !present {
		return nil, &mediaserverruntime.SourceError{Class: "AttributeError"}
	}
	folders, err := zspaceRefreshIterable(foldersRaw)
	if err != nil {
		return nil, err
	}
	for _, folderRaw := range folders {
		folder, e := zspaceObject(folderRaw)
		if e != nil {
			return nil, &mediaserverruntime.SourceError{Class: "AttributeError"}
		}
		subfoldersRaw := folder.get("SubFolders")
		if !zspaceTruth(subfoldersRaw) {
			subfoldersRaw = json.RawMessage("[]")
		}
		subs, e := zspaceRefreshIterable(subfoldersRaw)
		if e != nil {
			return nil, e
		}
		for _, subRaw := range subs {
			sub, e := zspaceObject(subRaw)
			if e != nil {
				v.report("match_subdirectory", "debug", "AttributeError")
				continue
			}
			subpath, e := zspaceRefreshPath(sub.get("Path"))
			if e != nil {
				if ordinary := v.caught("match_subdirectory", "debug", e); ordinary != nil {
					return nil, ordinary
				}
				continue
			}
			if zspaceRefreshRelative(path, subpath) {
				return zspaceCopy(folder.get("Id")), nil
			}
		}
	}
	// The original category is interpolated unescaped into re.search. Reuse
	// the existing bounded Python-pattern engine; do not quote it as a literal
	// or introduce a new timeout policy. This does not claim all CPython regex.
	var category string
	categoryRaw := bytes.TrimSpace(item.Category)
	if len(categoryRaw) > 0 && categoryRaw[0] == '"' {
		category, err = zspaceRefreshRegexText(categoryRaw)
	} else {
		category, err = sourcejson.String(categoryRaw)
	}
	if err != nil {
		return nil, err
	}
	for _, folderRaw := range folders {
		folder, e := zspaceObject(folderRaw)
		if e != nil {
			return nil, &mediaserverruntime.SourceError{Class: "AttributeError"}
		}
		subfoldersRaw := folder.get("SubFolders")
		if !zspaceTruth(subfoldersRaw) {
			subfoldersRaw = json.RawMessage("[]")
		}
		subs, e := zspaceRefreshIterable(subfoldersRaw)
		if e != nil {
			return nil, e
		}
		for _, subRaw := range subs {
			sub, e := zspaceObject(subRaw)
			if e != nil {
				return nil, &mediaserverruntime.SourceError{Class: "AttributeError"}
			}
			if !zspaceTruth(sub.get("Path")) {
				continue
			}
			text, e := zspaceRefreshRegexText(sub.get("Path"))
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
				return zspaceCopy(folder.get("Id")), nil
			}
		}
	}
	return zspaceString("/"), nil
}
