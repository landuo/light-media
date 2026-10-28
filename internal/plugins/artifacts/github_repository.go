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

func discoverGitHubPythonMetadata(ctx context.Context, repository string, client *http.Client) ([]Discovery, error) {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	all := []Discovery{}
	for _, name := range []string{"package.v2.json", "package.json"} {
		address, ok := githubRepositoryFile(repository, name)
		if !ok {
			return nil, errors.New("unsupported GitHub repository")
		}
		body, err := download(ctx, client, address, MaxIndexBytes)
		if err != nil {
			var status *downloadStatusError
			if errors.As(err, &status) && status.Code == http.StatusNotFound {
				continue
			}
			return nil, err
		}
		values, err := discoverPythonMetadataForVersion(body, repository, name == "package.json")
		if err != nil {
			return nil, err
		}
		all = append(all, values...)
	}
	return all, nil
}
