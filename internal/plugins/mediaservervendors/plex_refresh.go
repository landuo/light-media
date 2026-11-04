package mediaservervendors

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/big"
	"strings"

	"light-media/internal/plugins"
	"light-media/internal/plugins/mediaserverruntime"
)

// This is a per-call trusted host capability, not a worker-supplied path/RPC
// resolver. Resolve must physically resolve the source PosixPath (including
// symlinks/nonexistent tails) under the caller's existing filesystem authority.
// No lexical filepath.Clean substitute or new ambient path authority is supplied.
type PlexRefreshDependencies struct {
	Resolve func(context.Context, string) (string, error)
	Report  func(PlexRefreshDiagnostic)
}
type PlexRefreshDiagnostic struct{ Level, Message string }

// RefreshRootLibrary deliberately returns the actual retained Library object,
// not a fabricated true. Inactive retains the original boolean false return.
func (p *Plex) RefreshRootLibrary(ctx context.Context) (any, error) {
	if err := plexContext(ctx); err != nil {
		return nil, err
	}
	p.mu.RLock()
	server := p.server
	p.mu.RUnlock()
	if plexNil(server) {
		return false, nil
	}
	truthy, err := server.Truthy(ctx)
	if err != nil {
		return nil, err
	}
	if !truthy {
		return false, nil
	}
	sdk, ok := server.(*PlexSDKServer)
	if !ok {
		return nil, errors.New("Plex refresh requires its concrete retained SDK")
	}
	return sdk.updateLibrary(ctx)
}

// Items are the already accepted five-field post-validation carrier; this is
// not raw Pydantic admission. None/False/Library stay distinct until the parent
// host chooses a faithful reference carrier; no registry is invented here.
func (p *Plex) RefreshLibraryByItems(ctx context.Context, items []mediaserverruntime.ValidatedRefreshItem, dependencies PlexRefreshDependencies) (any, error) {
	if err := plexContext(ctx); err != nil {
		return nil, err
	}
	p.mu.RLock()
	server, libraries := p.server, p.libraries
	p.mu.RUnlock()
	if plexNil(server) {
		return false, nil
	}
	truthy, err := server.Truthy(ctx)
	if err != nil {
		return nil, err
	}
	if !truthy {
		return false, nil
	}
	sdk, ok := server.(*PlexSDKServer)
	if !ok {
		return nil, errors.New("Plex refresh requires its concrete retained SDK")
	}
	list, ok := libraries.(*plexSDKSectionList)
	if !ok {
		return nil, errors.New("Plex refresh requires its retained SDK section list")
	}
	type match struct {
		path    string
		section *plexSDKSection
	}
	matches := []match{}
	positions := map[string]int{}
	for _, item := range items {
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
			text := string(points)
			path = &text
		}
		section, found, err := plexFindRefreshLibrary(ctx, path, list, dependencies)
		if err != nil {
			return nil, err
		}
		name := ""
		if found {
			name = *path
		}
		if index, exists := positions[name]; exists {
			matches[index].section = section
		} else {
			positions[name] = len(matches)
			matches = append(matches, match{path: name, section: section})
		}
	}
	if _, failed := positions[""]; failed {
		_, err := sdk.updateLibrary(ctx)
		return nil, err
	}
	for _, matched := range matches {
		if err := sdk.current(ctx); err != nil {
			return nil, err
		}
		key := plexRefreshKey(matched.section.fields["key"])
		if dependencies.Report == nil {
			return nil, errors.New("Plex refresh requires diagnostic port")
		}
		dependencies.Report(PlexRefreshDiagnostic{Level: "INFO", Message: fmt.Sprintf("刷新媒体库：%s - %s", key, matched.path)})
		err := sdk.refreshLibraryPath(ctx, key, plexRefreshParent(matched.path))
		return nil, err // exact source first matched request, NOT a batch fix
	}
	return nil, nil
}
func plexFindRefreshLibrary(ctx context.Context, path *string, list *plexSDKSectionList, dependencies PlexRefreshDependencies) (*plexSDKSection, bool, error) {
	if path == nil {
		return nil, false, nil
	}
	if dependencies.Resolve == nil || dependencies.Report == nil {
		return nil, false, errors.New("Plex refresh requires current owned resolver and diagnostic port")
	}
	fail := func(err error) (*plexSDKSection, bool, error) {
		if source, ordinary := plexOrdinary(err); ordinary {
			message := ""
			if source.Cause != nil {
				message = source.Cause.Error()
			}
			dependencies.Report(PlexRefreshDiagnostic{Level: "ERROR", Message: "查找媒体库出错：" + message})
			return nil, false, nil
		}
		return nil, false, err // current/cancel/platform boundaries cannot become whole-library effects
	}
	for _, section := range list.values {
		locations, err := section.getLocations(ctx)
		if err != nil {
			return fail(err)
		}
		for _, location := range locations {
			if location == nil {
				return fail(plexSDKSourceError("TypeError", "argument should be a str or an os.PathLike object where __fspath__ returns a str, not 'NoneType'"))
			}
			resolved, err := dependencies.Resolve(ctx, *path)
			if err != nil {
				return fail(err)
			}
			parent, err := dependencies.Resolve(ctx, *location)
			if err != nil {
				return fail(err)
			}
			if err = plexContext(ctx); err != nil {
				return nil, false, err
			}
			parts, parents := plexRefreshParts(resolved), plexRefreshParts(parent)
			if len(parts) >= len(parents) {
				same := true
				for i := range parents {
					if parts[i] != parents[i] {
						same = false
						break
					}
				}
				if same {
					return section, true, nil
				}
			}
		}
	}
	return nil, false, nil
}
func plexRefreshParts(path string) []string {
	// Resolve's trusted result is the actual absolute PosixPath spelling. The
	// source compares components, not string-prefix (lib vs library).
	prefix := ""
	if strings.HasPrefix(path, "/") {
		prefix = "/"
	}
	if strings.HasPrefix(path, "//") && !strings.HasPrefix(path, "///") {
		prefix = "//"
	}
	parts := []string{}
	if prefix != "" {
		parts = append(parts, prefix)
	}
	for _, part := range strings.Split(path, "/") {
		if part != "" && part != "." {
			parts = append(parts, part)
		}
	}
	return parts
}
func plexRefreshParent(path string) string {
	// TargetPath is already a normalized source PosixPath carrier. Do not resolve
	// it here: source sends the lexical parent of the original path, not realpath.
	parts := plexRefreshParts(path)
	if len(parts) == 0 {
		return "."
	}
	if len(parts) == 1 {
		if parts[0] == "/" || parts[0] == "//" {
			return parts[0]
		}
		return "."
	}
	parts = parts[:len(parts)-1]
	if parts[0] == "/" || parts[0] == "//" {
		return parts[0] + strings.Join(parts[1:], "/")
	}
	return strings.Join(parts, "/")
}
func plexRefreshKey(value any) string {
	if value == nil {
		return "None"
	}
	if number, ok := value.(*big.Int); ok {
		return number.String()
	}
	if number, ok := value.(float64); ok && math.IsNaN(number) {
		return "nan"
	}
	return fmt.Sprint(value) // all current SDK keys are None/int/nan
}
