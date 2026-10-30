package artifacts

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

var githubRepositoryPart = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

// MoviePilot's PluginHelper._base_url uses main, not GitHub's default-branch
// API. Go sources first look for their own index at that same repository root.
func githubRepositoryBase(repository string) (string, bool) {
	u, err := url.Parse(repository)
	if err != nil || u.Scheme != "https" || !strings.EqualFold(u.Host, "github.com") || u.User != nil || u.Fragment != "" {
		return "", false
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) != 2 {
		return "", false
	}
	parts[1] = strings.TrimSuffix(parts[1], ".git")
	for _, part := range parts {
		if part == "" || part == "." || part == ".." || !githubRepositoryPart.MatchString(part) {
			return "", false
		}
	}
	// Do not carry arbitrary query credentials from a GitHub webpage to raw.
	for key := range u.Query() {
		if key != "_refresh" {
			return "", false
		}
	}
	return "https://raw.githubusercontent.com/" + parts[0] + "/" + parts[1] + "/main/", true
}

func githubRepositoryFile(repository, name string) (string, bool) {
	base, ok := githubRepositoryBase(repository)
	if !ok {
		return "", false
	}
	result, _ := url.Parse(base + name)
	source, _ := url.Parse(repository)
	result.RawQuery = source.RawQuery
	return result.String(), true
}

// Source metadata also accepts the ordinary raw GitHub root supported by the
// original get_repo_info. This resolver is never used for native artifact
// inspection/acquisition, and preserves HTTPS/host/query/part validation.
func githubSourceRepositoryFile(repository, name string) (string, bool) {
	if address, ok := githubRepositoryFile(repository, name); ok {
		return address, true
	}
	u, err := url.Parse(repository)
	if err != nil || u.Scheme != "https" || !strings.EqualFold(u.Host, "raw.githubusercontent.com") || u.User != nil || u.Fragment != "" {
		return "", false
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) != 3 {
		return "", false
	}
	for _, part := range parts {
		if part == "" || part == "." || part == ".." || !githubRepositoryPart.MatchString(part) {
			return "", false
		}
	}
	for key := range u.Query() {
		if key != "_refresh" {
			return "", false
		}
	}
	u.Path = "/" + parts[0] + "/" + parts[1] + "/main/" + name
	u.RawPath = ""
	return u.String(), true
}

func discoverGitHubPythonMetadata(ctx context.Context, repository string, client *http.Client) ([]Discovery, error) {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	all := []Discovery{}
	for _, version := range []string{"v2", ""} {
		values, err := DiscoverGitHubSourcePackage(ctx, repository, version, client)
		if err != nil {
			return nil, err
		}
		all = append(all, values...)
	}
	return all, nil
}

// DiscoverGitHubSourcePackage reads exactly one fixed source package. The host
// can cache successful base/v2 results independently, including 404/empty JSON;
// transport or parse failures must not overwrite that package's prior value.
// Python declarations remain metadata-only and cannot acquire executable code.
func DiscoverGitHubSourcePackage(ctx context.Context, repository, version string, client *http.Client) ([]Discovery, error) {
	name := "package.json"
	if version == "v2" {
		name = "package.v2.json"
	} else if version != "" {
		return nil, errors.New("unsupported source package version")
	}
	address, ok := githubSourceRepositoryFile(repository, name)
	if !ok {
		return nil, errors.New("unsupported GitHub repository")
	}
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	body, err := download(ctx, client, address, MaxIndexBytes)
	if err != nil {
		var status *downloadStatusError
		if errors.As(err, &status) && status.Code == http.StatusNotFound {
			return []Discovery{}, nil
		}
		return nil, err
	}
	values, err := discoverPythonMetadataForVersion(body, repository, version == "")
	if err != nil {
		return nil, err
	}
	for i := range values {
		values[i].SourcePackageVersion = version
	}
	return values, nil
}
