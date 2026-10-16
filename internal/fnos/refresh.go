package fnos

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
)

type RefreshResult struct {
	AllLibraries bool     `json:"all_libraries"`
	LibraryGUIDs []string `json:"library_guids"`
}

func serviceTruthyData(raw json.RawMessage) bool {
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if decoder.Decode(&value) != nil {
		return false
	}
	switch value := value.(type) {
	case nil:
		return false
	case bool:
		return value
	case string:
		return value != ""
	case []any:
		return len(value) > 0
	case map[string]any:
		return len(value) > 0
	case json.Number:
		mantissa, _, _ := strings.Cut(strings.ToLower(string(value)), "e")
		return strings.ContainsAny(mantissa, "123456789")
	}
	return false
}

// RefreshLibrariesByPaths follows MediaServerRefresh/trimemedia: match in
// server order, deduplicate libraries, call task/running before scans, and
// fall back to scanall for unmatched paths or a failed scan. This differs from
// the P0 strict-directory Scan. Callers must authorize this server-wide scope.
func (s *Service) RefreshLibrariesByPaths(ctx context.Context, paths []string) (RefreshResult, error) {
	result := RefreshResult{LibraryGUIDs: []string{}}
	if ctx == nil || len(paths) == 0 {
		return result, &ServiceError{Code: "invalid_input"}
	}
	for _, target := range paths {
		if ctx.Err() != nil {
			return result, &ServiceError{Code: "canceled"}
		}
		if !servicePath(target, false) {
			return result, &ServiceError{Code: "invalid_input"}
		}
	}
	current, err := s.session(ctx)
	if err != nil {
		return result, err
	}
	if !current.admin {
		return result, &ServiceError{Code: "unauthorized"}
	}
	libraries, err := current.readLibraries(ctx, false)
	if err != nil {
		return result, err
	}
	selected := []string{}
	seen := map[string]bool{}
	all := false
	for _, target := range paths {
		if ctx.Err() != nil {
			return result, &ServiceError{Code: "canceled"}
		}
		matched := ""
		for _, library := range libraries {
			for _, directory := range library.Directories {
				if target == directory || strings.HasPrefix(target, directory+"/") {
					matched = library.GUID
					break
				}
			}
			if matched != "" {
				break
			}
		}
		if matched == "" {
			all = true
			break
		}
		if !seen[matched] {
			seen[matched] = true
			selected = append(selected, matched)
		}
	}
	refreshAll := func() (RefreshResult, error) {
		if ctx.Err() != nil {
			return result, &ServiceError{Code: "canceled"}
		}
		// Preflight workaround from the original. Its result alone never
		// proves a scan was accepted or any media is visible.
		_ = current.call(ctx, "/task/running", nil, "", nil)
		var raw json.RawMessage
		if err := current.call(ctx, "/mdb/scanall", serviceEmptyPost{}, "", &raw); err != nil {
			return result, err
		}
		if !serviceTruthyData(raw) {
			return result, &ServiceError{Code: "scan_rejected"}
		}
		result.AllLibraries = true
		result.LibraryGUIDs = []string{}
		return result, nil
	}
	if all {
		return refreshAll()
	}
	_ = current.call(ctx, "/task/running", nil, "", nil)
	for _, guid := range selected {
		if current.requests >= 16 {
			current, err = s.session(ctx)
			if err != nil {
				return result, err
			}
			if !current.admin {
				return result, &ServiceError{Code: "unauthorized"}
			}
			_ = current.call(ctx, "/task/running", nil, "", nil)
		}
		var raw json.RawMessage
		if err := current.call(ctx, "/mdb/scan/"+guid, serviceEmptyPost{}, "", &raw); err != nil || !serviceTruthyData(raw) {
			return refreshAll()
		}
		result.LibraryGUIDs = append(result.LibraryGUIDs, guid)
	}
	return result, nil
}
