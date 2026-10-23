package artifacts

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"runtime"

	"light-media/internal/plugins"
)

// Discovery is metadata-only: no artifact is downloaded or executed, and no
// permission or installed state is established. One URL describes one plugin.
type Discovery struct {
	RepositoryURL  string              `json:"repository_url"`
	PluginID       string              `json:"plugin_id"`
	CurrentVersion string              `json:"current_version"`
	Platform       string              `json:"platform"`
	Current        Compatibility       `json:"current"`
	Releases       []Compatibility     `json:"releases"`
	Rejected       []ReleaseDiagnostic `json:"rejected,omitempty"`
}

// ReleaseDiagnostic is noninstallable source metadata, not a fallback release.
type ReleaseDiagnostic struct {
	Version string `json:"version"`
	Reason  string `json:"reason"`
}

// Discover returns the actual index identity and validated releases for the
// requested GOOS-GOARCH (or this host if empty). Current must be compatible and
// authorized by validate or discovery fails. Incompatible historical releases
// are excluded from Releases and explicitly reported in Rejected, in index order.
// Artifact declarations never grant host authority; validate is a trusted policy.
func Discover(ctx context.Context, repositoryURL, platform string, validate func(plugins.Manifest, string) error, client *http.Client) (Discovery, error) {
	bad := func(err error) (Discovery, error) { return Discovery{}, &StageError{Stage: "preflight", Err: err} }
	if platform == "" {
		platform = runtime.GOOS + "-" + runtime.GOARCH
	}
	if !platformPattern.MatchString(platform) {
		return bad(errors.New("invalid platform"))
	}
	if validate == nil {
		return bad(errors.New("trusted host validator required"))
	}
	index, err := fetchIndex(ctx, repositoryURL, client)
	if err != nil {
		return bad(err)
	}
	if !idPattern.MatchString(index.PluginID) {
		return bad(errors.New("invalid index plugin ID"))
	}
	req := Request{PluginID: index.PluginID, Platform: platform, Validate: validate}
	current, artifact, err := selectArtifact(index, req)
	if err != nil {
		return bad(err)
	}
	if err = validate(current.Manifest, artifact.HostContract); err != nil {
		return bad(fmt.Errorf("current release: %w", err))
	}
	currentMeta := Compatibility{Manifest: current.Manifest, HostContract: artifact.HostContract, Platform: artifact.Platform}
	result := Discovery{RepositoryURL: repositoryURL, PluginID: index.PluginID, CurrentVersion: index.CurrentVersion, Platform: platform, Current: currentMeta, Releases: []Compatibility{}}
	for _, listed := range index.Releases {
		if listed.Version == "" {
			result.Rejected = append(result.Rejected, ReleaseDiagnostic{Reason: "missing historical release version"})
			continue
		}
		if listed.Version == index.CurrentVersion {
			result.Releases = append(result.Releases, currentMeta)
			continue
		}
		req.ReleaseVersion = listed.Version
		release, selected, selectErr := selectArtifact(index, req)
		if selectErr == nil {
			selectErr = validate(release.Manifest, selected.HostContract)
		}
		if selectErr != nil {
			result.Rejected = append(result.Rejected, ReleaseDiagnostic{Version: listed.Version, Reason: selectErr.Error()})
			continue
		}
		result.Releases = append(result.Releases, Compatibility{Manifest: release.Manifest, HostContract: selected.HostContract, Platform: selected.Platform})
	}
	return result, nil
}
