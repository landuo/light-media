package mediaservervendors

import (
	"context"
	"errors"
	"strings"

	"light-media/internal/plugins"
	"light-media/internal/plugins/mediaserverruntime"
)

// Physical resolution is an operation-scoped host port, not a plugin-defined
// callback. GUID ordering is the concrete source string-set algorithm below;
// callers cannot replace it with an arbitrary priority or sorted-list adapter.
type TrimeRefreshDependencies struct {
	Resolve func(context.Context, string) (string, error)
}

// Genuine nonempty implementation over the actual retained Api and MediaDb
// cache. This does not fabricate a media library from input configuration and
// does not replace the original Empty/None/authentication distinctions.
func (p *TrimeMedia) RefreshLibraryByItems(ctx context.Context, input []mediaserverruntime.ValidatedRefreshItem, deps TrimeRefreshDependencies) (*bool, error) {
	auth, err := p.IsAuthenticated(ctx)
	if err != nil || !auth {
		return nil, err
	}
	p.mu.RLock()
	user := p.user
	p.mu.RUnlock()
	if user == nil || user.IsAdmin != 1 {
		p.log("error", "refresh-non-admin")
		result := false
		return &result, nil
	}
	libraries, err := NewSourceStringSet()
	if err != nil {
		return nil, err
	}
	for _, item := range input {
		owned, err := item.OwnedCopy()
		if err != nil {
			return nil, err
		}
		var path *string
		if string(owned.TargetPath) != "null" {
			points, err := plugins.DecodeSystemConfigJSONString(owned.TargetPath)
			if err != nil {
				return nil, err
			}
			for _, point := range points {
				if point >= 0xd800 && point <= 0xdfff {
					return nil, &TrimeArgumentBoundary{Field: "physical path contains an unrepresentable source surrogate"}
				}
			}
			text := string(points)
			path = &text
		}
		library, err := p.matchRefreshLibrary(ctx, path, deps.Resolve)
		if err != nil {
			return nil, err
		}
		if library == nil {
			return p.RefreshRoot(ctx)
		}
		if err = libraries.Add(library.GUID); err != nil {
			return nil, err
		}
	}
	api, err := p.currentAPI(ctx, "task_running")
	if err != nil {
		return nil, err
	}
	if _, err = api.TaskRunning(ctx); err != nil {
		return nil, err
	}
	ordered, err := libraries.Values()
	if err != nil {
		return nil, err
	}
	for _, guid := range ordered {
		if err = trimeContext(ctx); err != nil {
			return nil, err
		}
		// Re-read the real cache at the original dictionary indexing point, after
		// task_running. Do not keep an earlier matching object through a reload.
		p.mu.RLock()
		var library *TrimeMediaDB
		for _, candidate := range p.libraries {
			if candidate != nil && candidate.GUID == guid {
				library = candidate
				break
			}
		}
		p.mu.RUnlock()
		if library == nil {
			return nil, &mediaserverruntime.SourceError{Class: "KeyError"}
		}
		p.log("info", "refresh-library")
		api, err = p.currentAPI(ctx, "mdb_scan")
		if err != nil {
			return nil, err
		}
		// The concrete API already implements Scan. The smaller old recording API
		// interface is not treated as a successful replacement for that operation.
		scanner, ok := api.(interface {
			Scan(context.Context, *TrimeMediaDB) (bool, error)
		})
		if !ok {
			return nil, errors.New("Trime refresh requires the actual library scan API")
		}
		success, err := scanner.Scan(ctx, library)
		if err != nil {
			return nil, err
		}
		if !success {
			return p.RefreshRoot(ctx)
		}
	}
	result := true
	return &result, nil
}

func (p *TrimeMedia) matchRefreshLibrary(ctx context.Context, path *string, resolve func(context.Context, string) (string, error)) (*TrimeMediaDB, error) {
	if path == nil {
		return nil, nil
	}
	p.mu.RLock()
	libraries := append([]*TrimeMediaDB(nil), p.libraries...)
	p.mu.RUnlock()
	for _, library := range libraries {
		if library == nil {
			return nil, trimeMissing("dir_list")
		}
		for _, directory := range library.Directories {
			if resolve == nil {
				return nil, &TrimeArgumentBoundary{Field: "refresh requires current owned physical Path.resolve"}
			}
			actual, err := resolve(ctx, *path)
			if err != nil {
				return nil, err
			}
			parent, err := resolve(ctx, directory)
			if err != nil {
				return nil, err
			}
			if err = trimeContext(ctx); err != nil {
				return nil, err
			}
			parts, parents := trimeRefreshParts(actual), trimeRefreshParts(parent)
			if len(parts) < len(parents) {
				continue
			}
			same := true
			for i := range parents {
				if parts[i] != parents[i] {
					same = false
					break
				}
			}
			if same {
				return library, nil
			}
		}
	}
	return nil, nil
}

func trimeRefreshParts(path string) []string {
	prefix := ""
	if strings.HasPrefix(path, "/") {
		prefix = "/"
	}
	if strings.HasPrefix(path, "//") && !strings.HasPrefix(path, "///") {
		prefix = "//"
	}
	result := []string{}
	if prefix != "" {
		result = append(result, prefix)
	}
	for _, part := range strings.Split(path, "/") {
		if part != "" && part != "." {
			result = append(result, part)
		}
	}
	return result
}
