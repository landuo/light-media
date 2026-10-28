package artifacts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"runtime"

	"light-media/internal/plugins"
)

// Discovery is metadata-only: no artifact is downloaded or executed, and no
// permission or installed state is established. One URL may describe many IDs.
type Discovery struct {
	SystemVersion           json.RawMessage     `json:"system_version,omitempty"`
	SystemVersionCompatible *bool               `json:"system_version_compatible,omitempty"`
	SystemVersionMessage    string              `json:"system_version_message,omitempty"`
	RepositoryURL           string              `json:"repository_url"`
	PluginID                string              `json:"plugin_id"`
	CurrentVersion          string              `json:"current_version"`
	Platform                string              `json:"platform"`
	Current                 Compatibility       `json:"current"`
	Releases                []Compatibility     `json:"releases"`
	Rejected                []ReleaseDiagnostic `json:"rejected,omitempty"`
	History                 map[string]string   `json:"history,omitempty"`
	HistoryJSON             json.RawMessage     `json:"-"`
	Label                   string              `json:"plugin_label,omitempty"`
	AddTime                 int                 `json:"add_time,omitempty"`
	UnavailableReason       string              `json:"unavailable_reason,omitempty"`
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
func Discover(ctx context.Context, repositoryURL, platform string, validate func(plugins.Manifest, string) error, client *http.Client, localRoots ...string) (Discovery, error) {
	if validate == nil || (platform != "" && !platformPattern.MatchString(platform)) {
		return Discovery{}, &StageError{Stage: "preflight", Err: errors.New("valid platform and trusted host validator required")}
	}
	index, err := fetchIndex(ctx, repositoryURL, client, localRoots)
	if err != nil {
		return Discovery{}, &StageError{Stage: "preflight", Err: err}
	}
	return discoverIndex(index, repositoryURL, platform, validate)
}

func discoverIndex(index Index, repositoryURL, platform string, validate func(plugins.Manifest, string) error) (Discovery, error) {
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
	if !idPattern.MatchString(index.PluginID) {
		return bad(errors.New("invalid index plugin ID"))
	}
	req := Request{RepositoryURL: repositoryURL, PluginID: index.PluginID, Platform: platform, Validate: validate}
	current, artifact, err := selectArtifact(index, req)
	if err != nil {
		return bad(err)
	}
	if err = validate(current.Manifest, artifact.HostContract); err != nil {
		return bad(fmt.Errorf("current release: %w", err))
	}
	currentMeta := Compatibility{Manifest: current.Manifest, HostContract: artifact.HostContract, Platform: artifact.Platform}
	result := Discovery{RepositoryURL: repositoryURL, PluginID: index.PluginID, CurrentVersion: index.CurrentVersion, Platform: platform, Current: currentMeta, Releases: []Compatibility{}, History: index.History, HistoryJSON: index.HistoryJSON, Label: index.Label, AddTime: index.AddTime}
	// Market source.level/key affect the visible card, not the executable's
	// independently verified class declaration or its installed manifest.
	if index.Level != nil {
		result.Current.Manifest.AuthLevel = index.Level
	}
	if index.PublicKey != "" {
		result.Current.Manifest.PluginPublicKey = index.PublicKey
	}
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
	annotateDiscoverySystemVersion(&result, index.SystemVersion)
	return result, nil
}
