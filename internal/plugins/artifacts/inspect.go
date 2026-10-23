package artifacts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"strings"
	"time"

	"light-media/internal/plugins"
)

// Compatibility is repository metadata, NOT installed state or an executable
// command. Its version is the currently indexed version, not a requested old
// release and not necessarily the existing class's installed version.
type Compatibility struct {
	Manifest     plugins.Manifest
	HostContract string
	Platform     string
}

// InspectCompatible implements the original existing-class, non-force branch:
// validate the repository's CURRENT declaration without replacing any code.
// ReleaseVersion is intentionally ignored in this branch. Root, Force and
// RefreshBackup cause no filesystem or lifecycle operations. App is responsible
// for selecting this branch only when it already has the class and is not forcing.
func InspectCompatible(ctx context.Context, req Request, client *http.Client) (Compatibility, error) {
	req.ReleaseVersion = ""
	release, artifact, err := inspectMetadata(ctx, req, client)
	if err != nil {
		return Compatibility{}, &StageError{Stage: "preflight", Err: err}
	}
	return Compatibility{Manifest: release.Manifest, HostContract: artifact.HostContract, Platform: artifact.Platform}, nil
}

// Shared metadata parser and trusted gate for acquisition and compatibility-only
// inspection. It never downloads artifact bytes or inspects/modifies Root.
func inspectMetadata(ctx context.Context, req Request, client *http.Client) (Release, Artifact, error) {
	bad := func(err error) (Release, Artifact, error) { return Release{}, Artifact{}, err }
	if !idPattern.MatchString(req.PluginID) {
		return bad(errors.New("invalid plugin ID"))
	}
	if req.Platform == "" {
		req.Platform = runtime.GOOS + "-" + runtime.GOARCH
	}
	if !platformPattern.MatchString(req.Platform) {
		return bad(errors.New("invalid platform"))
	}
	index, err := fetchIndex(ctx, req.RepositoryURL, client)
	if err != nil {
		return bad(err)
	}
	release, artifact, err := selectArtifact(index, req)
	if err != nil {
		return bad(err)
	}
	if req.Validate == nil {
		return bad(errors.New("trusted host validator required"))
	}
	if err = req.Validate(release.Manifest, artifact.HostContract); err != nil {
		return bad(err)
	}
	return release, artifact, nil
}

// fetchIndex is the single bounded strict JSON parser for every metadata path.
func fetchIndex(ctx context.Context, repositoryURL string, client *http.Client) (Index, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	body, err := download(ctx, client, repositoryURL, MaxIndexBytes)
	if err != nil {
		return Index{}, err
	}
	var index Index
	dec := json.NewDecoder(strings.NewReader(string(body)))
	dec.DisallowUnknownFields()
	if err = dec.Decode(&index); err != nil {
		return Index{}, fmt.Errorf("invalid Go artifact index: %w", err)
	}
	if dec.Decode(new(any)) != io.EOF {
		return Index{}, errors.New("trailing index data")
	}

	return index, nil
}
