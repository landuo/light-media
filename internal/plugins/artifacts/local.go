package artifacts

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"

	"light-media/internal/plugins"
)

// Local sources use MoviePilot's local://ID?path=... provenance shape. The Go
// package index remains go-plugins.v1.json, with relative artifact paths. A URL
// is never authority: every acquisition must name an exactly configured root.
func IsLocalRepository(repository string) bool { return strings.HasPrefix(repository, "local://") }

func NormalizeLocalRoot(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "~") {
		head, tail, hasTail := strings.Cut(raw, "/")
		var directory string
		if head == "~" {
			var err error
			directory, err = os.UserHomeDir()
			if err != nil {
				return "", err
			}
		} else {
			account, err := user.Lookup(strings.TrimPrefix(head, "~"))
			if err != nil {
				return "", errors.New("local repository home is unavailable")
			}
			directory = account.HomeDir
		}
		if directory == "" {
			return "", errors.New("local repository home is unavailable")
		}
		raw = directory
		if hasTail {
			raw += string(filepath.Separator) + tail
		}
	}
	if raw == "" || strings.ContainsRune(raw, 0) {
		return "", errors.New("invalid local repository path")
	}
	absolute := raw
	if !filepath.IsAbs(absolute) {
		cwd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		absolute = cwd + string(filepath.Separator) + raw
	}
	// Path.resolve(strict=False) resolves existing links but permits future
	// directories. Configuration is not proof that a readable repository exists;
	// discovery/acquisition independently open and validate the configured root.
	return resolveLocalConfigurationPath(absolute, 0)
}
func resolveLocalConfigurationPath(absolute string, links int) (string, error) {
	if links > 255 {
		return "", errors.New("local repository symlink loop")
	}
	resolved := filepath.VolumeName(absolute) + string(filepath.Separator)
	parts := strings.Split(strings.TrimPrefix(absolute, resolved), string(filepath.Separator))
	for i, part := range parts {
		if part == "" || part == "." {
			continue
		}
		if part == ".." {
			resolved = filepath.Dir(resolved)
			continue
		}
		candidate := filepath.Join(resolved, part)
		info, err := os.Lstat(candidate)
		if err != nil {
			// Native pathlib resolves as far as possible with strict=False.
			// Readability and regular-directory checks belong to repository access.
			resolved = candidate
			continue
		}
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(candidate)
			if err != nil {
				resolved = candidate
				continue
			}
			if !filepath.IsAbs(target) {
				target = resolved + string(filepath.Separator) + target
			}
			next := target + string(filepath.Separator) + strings.Join(parts[i+1:], string(filepath.Separator))
			return resolveLocalConfigurationPath(next, links+1)
		}
		resolved = candidate
	}
	return resolved, nil
}

func LocalRepositoryURL(id, root string) (string, error) {
	if !idPattern.MatchString(id) || !filepath.IsAbs(root) || filepath.Clean(root) != root {
		return "", errors.New("invalid local repository identity")
	}
	u := url.URL{Scheme: "local", Host: id, RawQuery: url.Values{"path": {root}}.Encode()}
	return u.String(), nil
}

func parseLocalRepository(repository string) (string, string, error) {
	u, err := url.Parse(repository)
	if err != nil || u.Scheme != "local" || !idPattern.MatchString(u.Host) || u.User != nil || u.Path != "" || u.Fragment != "" {
		return "", "", errors.New("invalid local repository URL")
	}
	query, err := url.ParseQuery(u.RawQuery)
	if err != nil || len(query) != 1 || len(query["path"]) != 1 {
		return "", "", errors.New("local repository requires one path")
	}
	root := query.Get("path")
	if !filepath.IsAbs(root) || filepath.Clean(root) != root || strings.ContainsRune(root, 0) {
		return "", "", errors.New("invalid local repository path")
	}
	return u.Host, root, nil
}

func trustedLocalRoot(repository string, roots []string) (string, error) {
	_, root, err := parseLocalRepository(repository)
	if err != nil {
		return "", err
	}
	if !slices.Contains(roots, root) {
		return "", errors.New("local repository path is not configured")
	}
	return root, nil
}

func validateRepositoryURL(repository string) error {
	if IsLocalRepository(repository) {
		_, _, err := parseLocalRepository(repository)
		return err
	}
	_, err := httpURL(repository)
	return err
}

func openLocalRoot(root string) (*os.Root, error) {
	cur := filepath.VolumeName(root) + string(filepath.Separator)
	for _, part := range strings.Split(strings.TrimPrefix(root, cur), string(filepath.Separator)) {
		if part == "" {
			continue
		}
		cur = filepath.Join(cur, part)
		info, err := os.Lstat(cur)
		if err != nil {
			return nil, err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, errors.New("unsafe local repository ancestor")
		}
	}
	return os.OpenRoot(root)
}

func localFile(root, name string) (*os.File, error) {
	if !safeName(name) {
		return nil, errors.New("local artifact requires a relative path within its repository")
	}
	dir, err := openLocalRoot(root)
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	parts := strings.Split(name, "/")
	for i := range parts {
		info, err := dir.Lstat(strings.Join(parts[:i+1], "/"))
		if err != nil {
			return nil, err
		}
		if info.Mode()&os.ModeSymlink != 0 || (i < len(parts)-1 && !info.IsDir()) || (i == len(parts)-1 && !info.Mode().IsRegular()) {
			return nil, errors.New("local artifact contains a link or special file")
		}
	}
	// Root.Open also rejects traversal outside the anchored root if a path is
	// replaced between the checks above and opening it.
	return dir.Open(name)
}

func readLocalFile(ctx context.Context, root, name string, limit int64) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f, err := localFile(root, name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > limit {
		return nil, errors.New("local artifact exceeds size bound or is not regular")
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errors.New("local artifact exceeds size bound")
	}
	return data, ctx.Err()
}

// DiscoverLocal is a trusted-host entry point: callers supply a configured
// directory, never a directory taken directly from an install request.
func DiscoverLocal(ctx context.Context, root, platform string, validate func(plugins.Manifest, string) error) (Discovery, error) {
	values, err := DiscoverLocalRepository(ctx, root, platform, validate)
	if err != nil {
		return Discovery{}, err
	}
	if len(values) != 1 {
		return Discovery{}, errors.New("local repository contains multiple plugins; select an explicit plugin ID")
	}
	if values[0].UnavailableReason != "" {
		return Discovery{}, errors.New(values[0].UnavailableReason)
	}
	return values[0], nil
}

func DiscoverLocalRepository(ctx context.Context, root, platform string, validate func(plugins.Manifest, string) error) ([]Discovery, error) {
	return discoverLocalRepository(ctx, root, platform, validate, false)
}

// Fixed DEV watcher may synchronize local source despite a system_version
// mismatch. Ordinary market/manual installation remains strict in DEV too.
func DiscoverLocalRepositoryForWatch(ctx context.Context, root, platform string, validate func(plugins.Manifest, string) error, developer bool) ([]Discovery, error) {
	return discoverLocalRepository(ctx, root, platform, validate, developer)
}
func discoverLocalRepository(ctx context.Context, root, platform string, validate func(plugins.Manifest, string) error, developer bool) ([]Discovery, error) {
	body, err := readLocalFile(ctx, root, IndexName, MaxIndexBytes)
	if err != nil {
		return nil, err
	}
	indexes, err := parseRepository(body)
	if err != nil {
		return nil, err
	}
	out := make([]Discovery, 0, len(indexes))
	for _, index := range indexes {
		repository, err := LocalRepositoryURL(index.PluginID, root)
		if err != nil {
			return nil, err
		}
		systemRange := index.SystemVersion
		if developer {
			index.SystemVersion = nil
		}
		values, err := discoverRepositoryIndexes([]Index{index}, repository, platform, validate)
		if err != nil {
			return nil, err
		}
		if developer {
			reason := values[0].UnavailableReason
			annotateDiscoverySystemVersion(&values[0], systemRange)
			values[0].UnavailableReason = reason
		}
		values[0].AddTime = index.AddTime
		out = append(out, values[0])
	}
	return out, nil
}

// LocalFingerprint detects a published index or worker/package replacement
// without reading a 64 MiB executable every polling interval. Acquisition still
// checks all declared content digests before publishing or running the package.
func LocalFingerprint(ctx context.Context, repository, platform string, roots []string, validate func(plugins.Manifest, string) error) (string, error) {
	return LocalFingerprintForWatch(ctx, repository, platform, roots, validate, false)
}
func LocalFingerprintForWatch(ctx context.Context, repository, platform string, roots []string, validate func(plugins.Manifest, string) error, developer bool) (string, error) {
	root, err := trustedLocalRoot(repository, roots)
	if err != nil {
		return "", err
	}
	body, err := readLocalFile(ctx, root, IndexName, MaxIndexBytes)
	if err != nil {
		return "", err
	}
	indexes, err := parseRepository(body)
	if err != nil {
		return "", err
	}
	index, err := selectRepositoryIndex(indexes, mustLocalID(repository))
	if err != nil {
		return "", err
	}
	if platform == "" {
		platform = runtime.GOOS + "-" + runtime.GOARCH
	}
	release, artifact, err := selectArtifact(index, Request{DeveloperLocalWatch: developer, RepositoryURL: repository, PluginID: mustLocalID(repository), Platform: platform})
	if err != nil {
		return "", err
	}
	if validate == nil {
		return "", errors.New("trusted host validator required")
	}
	if err = validate(release.Manifest, artifact.HostContract); err != nil {
		return "", err
	}
	f, err := localFile(root, artifact.URL)
	if err != nil {
		return "", err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return "", err
	}
	// Only this plugin's declaration belongs to its fingerprint. Publishing a
	// sibling in the same repository must not reload this unchanged instance.
	identity, _ := json.Marshal(index)
	return digest(identity) + ":" + release.Version + ":" + info.ModTime().UTC().Format("2006-01-02T15:04:05.999999999Z07:00") + ":" + strconv.FormatInt(info.Size(), 10), nil
}

func mustLocalID(repository string) string { id, _, _ := parseLocalRepository(repository); return id }
