package artifacts

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"light-media/internal/plugins"
)

// RepositoryIndex contains ordered, self-contained plugin declarations. The
// existing single Index document remains supported without migration.
type RepositoryIndex struct {
	SchemaVersion int     `json:"schema_version"`
	Plugins       []Index `json:"plugins"`
}

func decodeStrictIndex(body []byte, dst any) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return fmt.Errorf("invalid Go artifact index: %w", err)
	}
	if decoder.Decode(new(any)) != io.EOF {
		return errors.New("trailing index data")
	}
	return nil
}

func parseRepository(body []byte) ([]Index, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil || fields == nil {
		return nil, errors.New("invalid Go repository JSON")
	}
	if _, multi := fields["plugins"]; !multi {
		index, err := parseIndex(body)
		if err != nil {
			return nil, err
		}
		return []Index{index}, nil
	}
	var repository RepositoryIndex
	if err := decodeStrictIndex(body, &repository); err != nil {
		return nil, err
	}
	if repository.SchemaVersion != 1 || repository.Plugins == nil || len(repository.Plugins) > 1024 {
		return nil, errors.New("invalid Go repository schema or plugin list")
	}
	seen := map[string]bool{}
	for _, index := range repository.Plugins {
		key := strings.ToLower(index.PluginID)
		if !idPattern.MatchString(index.PluginID) || seen[key] {
			return nil, errors.New("invalid or duplicate repository plugin ID")
		}
		seen[key] = true
	}
	return repository.Plugins, nil
}

func selectRepositoryIndex(indexes []Index, id string) (Index, error) {
	for _, index := range indexes {
		if index.PluginID == id {
			return index, nil
		}
	}
	return Index{}, errors.New("repository does not declare the requested plugin ID")
}

func fetchPluginIndex(ctx context.Context, repository, id string, client *http.Client, roots []string) (Index, error) {
	if IsLocalRepository(repository) && id != mustLocalID(repository) {
		return Index{}, errors.New("local repository plugin identity mismatch")
	}
	body, err := fetchIndexBody(ctx, repository, client, roots)
	if err != nil {
		return Index{}, err
	}
	indexes, err := parseRepository(body)
	if err != nil {
		return Index{}, err
	}
	return selectRepositoryIndex(indexes, id)
}

// DiscoverRepository reads one document once and returns each candidate in
// author order. An incompatible candidate remains metadata only: it cannot
// grant installation authority or hide compatible siblings in the same source.
func DiscoverRepository(ctx context.Context, repository, platform string, validate func(plugins.Manifest, string) error, client *http.Client, roots ...string) ([]Discovery, error) {
	if validate == nil || (platform != "" && !platformPattern.MatchString(platform)) {
		return nil, errors.New("valid platform and trusted host validator required")
	}
	body, err := fetchIndexBody(ctx, repository, client, roots)
	if err != nil {
		var status *downloadStatusError
		if _, github := githubRepositoryBase(repository); github && errors.As(err, &status) && status.Code == http.StatusNotFound {
			return discoverGitHubPythonMetadata(ctx, repository, client)
		}
		return nil, err
	}
	indexes, err := parseRepository(body)
	if err != nil {
		if values, pythonErr := discoverPythonMetadata(body, repository); pythonErr == nil {
			return values, nil
		}
		return nil, err
	}
	return discoverRepositoryIndexes(indexes, repository, platform, validate)
}

func discoverRepositoryIndexes(indexes []Index, repository, platform string, validate func(plugins.Manifest, string) error) ([]Discovery, error) {
	if validate == nil || (platform != "" && !platformPattern.MatchString(platform)) {
		return nil, errors.New("valid platform and trusted host validator required")
	}
	out := make([]Discovery, 0, len(indexes))
	for _, index := range indexes {
		value, err := discoverIndex(index, repository, platform, validate)
		if err != nil {
			// Identity and author-supplied current manifest are required even for
			// a disabled card; corrupt identity must not invent a usable candidate.
			if !idPattern.MatchString(index.PluginID) {
				return nil, err
			}
			var manifest plugins.Manifest
			for _, release := range index.Releases {
				if release.Version == index.CurrentVersion && release.Manifest.PluginID == index.PluginID {
					manifest = release.Manifest
					break
				}
			}
			if manifest.PluginID == "" || manifest.Name == "" || manifest.Version != index.CurrentVersion {
				return nil, err
			}
			value = Discovery{RepositoryURL: repository, PluginID: index.PluginID, CurrentVersion: index.CurrentVersion, Platform: platform, Current: Compatibility{Manifest: manifest}, Releases: []Compatibility{}, History: index.History, HistoryJSON: index.HistoryJSON, Label: index.Label, AddTime: index.AddTime, UnavailableReason: "当前 Go 插件制品与平台或宿主契约不兼容：" + err.Error()}
			if index.Level != nil {
				value.Current.Manifest.AuthLevel = index.Level
			}
			if index.PublicKey != "" {
				value.Current.Manifest.PluginPublicKey = index.PublicKey
			}
		}
		annotateDiscoverySystemVersion(&value, index.SystemVersion)
		if value.AddTime == 0 {
			value.AddTime = len(indexes) - len(out)
		}
		out = append(out, value)
	}
	return out, nil
}

// DiscoverPlugin selects one ID from either supported index shape. Use this
// when the caller knows the exact installed/selected plugin identity.
func DiscoverPlugin(ctx context.Context, repository, id, platform string, validate func(plugins.Manifest, string) error, client *http.Client, roots ...string) (Discovery, error) {
	index, err := fetchPluginIndex(ctx, repository, id, client, roots)
	if err != nil {
		return Discovery{}, err
	}
	return discoverIndex(index, repository, platform, validate)
}
