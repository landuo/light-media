package fnos

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

func refreshUnauthenticated(err error) bool {
	var service *ServiceError
	return errors.As(err, &service) && (service.Code == "unauthorized" || service.Code == "not_configured")
}

// resolveRefreshSourcePath follows Path.resolve(strict=False) for local Linux
// paths, including symlink components before '..' and absent trailing files.
// It reads paths only; a match never grants arbitrary local filesystem writes.
func resolveRefreshSourcePath(raw string) (string, error) {
	if !filepath.IsAbs(raw) {
		cwd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		raw = cwd + "/" + raw
	}
	parts := strings.Split(raw, "/")
	resolved := "/"
	links := 0
	for len(parts) > 0 {
		part := parts[0]
		parts = parts[1:]
		if part == "" || part == "." {
			continue
		}
		if part == ".." {
			resolved = filepath.Dir(resolved)
			continue
		}
		next := filepath.Join(resolved, part)
		info, err := os.Lstat(next)
		if err == nil && info.Mode()&os.ModeSymlink != 0 {
			links++
			if links > 40 {
				return "", &ServiceError{Code: "invalid_input"}
			}
			target, err := os.Readlink(next)
			if err != nil {
				return "", err
			}
			if filepath.IsAbs(target) {
				resolved = "/"
			}
			parts = append(strings.Split(target, "/"), parts...)
			continue
		}
		resolved = next
	}
	return resolved, nil
}

// RefreshSourceItems is Trimemedia.refresh_library_by_items: None target
// paths trigger server-wide fallback, an empty item list still runs preflight,
// and failed partial scans fall back to the source root refresh. A nil source
// result means not authenticated; false means the authenticated user is not an
// administrator. Transport/protocol and current-invocation failures remain
// errors for the host to classify, never a fabricated successful refresh.
func (s *Service) RefreshSourceItems(ctx context.Context, items []*string, beforeWrite func() error) (RefreshResult, error) {
	if beforeWrite == nil {
		beforeWrite = func() error { return nil }
	}
	read := false
	return s.refreshLibraryBatches(ctx, func(context.Context, []ServiceLibrary) ([]string, error) {
		if read {
			return nil, nil
		}
		read = true
		paths := make([]string, len(items))
		for index, path := range items {
			if path != nil {
				if *path == "" {
					paths[index] = "."
				} else {
					paths[index] = *path
				}
			}
		}
		return paths, nil
	}, beforeWrite, beforeWrite, true)
}

func (s *Service) RefreshSourceRoot(ctx context.Context, beforeWrite func() error) (RefreshResult, error) {
	result := RefreshResult{LibraryGUIDs: []string{}}
	current, err := s.session(ctx)
	if err != nil {
		if refreshUnauthenticated(err) {
			return result, nil
		}
		return result, err
	}
	if !current.admin {
		value := false
		result.SourceResult = &value
		return result, nil
	}
	_ = current.call(ctx, "/task/running", nil, "", nil)
	if beforeWrite != nil {
		if err := beforeWrite(); err != nil {
			return result, err
		}
	}
	var raw json.RawMessage
	if err := current.call(ctx, "/mdb/scanall", serviceEmptyPost{}, "", &raw); err != nil {
		return result, err
	}
	if !serviceTruthyData(raw) {
		return result, &ServiceError{Code: "scan_rejected"}
	}
	value := true
	result.SourceResult = &value
	result.AllLibraries = true
	return result, nil
}

// RefreshExplicitLibrary is a bounded Go host extension used when the caller
// deliberately selects a configured GUID. It follows the real empty-POST
// protocol and preflight, but NEVER falls back to another library or scanall.
// It is intentionally separate from the two source-semantic entry points.
func (s *Service) RefreshExplicitLibrary(ctx context.Context, guid string, beforeWrite func() error) (RefreshResult, error) {
	result := RefreshResult{LibraryGUIDs: []string{}}
	if !detailGUID.MatchString(guid) {
		return result, &ServiceError{Code: "invalid_input"}
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
	if _, found := serviceLibrary(libraries, guid); !found {
		return result, &ServiceError{Code: "invalid_input"}
	}
	_ = current.call(ctx, "/task/running", nil, "", nil)
	if beforeWrite != nil {
		if err := beforeWrite(); err != nil {
			return result, err
		}
	}
	var raw json.RawMessage
	if err := current.call(ctx, "/mdb/scan/"+guid, serviceEmptyPost{}, "", &raw); err != nil {
		return result, err
	}
	if !serviceTruthyData(raw) {
		return result, &ServiceError{Code: "scan_rejected"}
	}
	value := true
	result.SourceResult = &value
	result.LibraryGUIDs = append(result.LibraryGUIDs, guid)
	return result, nil
}
