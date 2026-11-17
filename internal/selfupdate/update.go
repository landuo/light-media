// Package selfupdate replaces the running binary with a newer published release
// before the server starts.
//
// It mirrors MoviePilot's startup update (app/cli.py `_best_effort_auto_update`):
// the check runs before the listener binds, stays inside the current major
// series, and never blocks startup. A failed check or a failed install leaves
// the current binary in place.
//
// The unit runs inside a container whose /app copy is a read-only bind mount, so
// the new artifact is installed under <data-dir>/bin and the process re-executes
// itself from there. The mounted path is only the bootstrap.
package selfupdate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// DefaultReleasesURL is the project's own release feed.
const DefaultReleasesURL = "https://api.github.com/repos/landuo/light-media/releases"

// UpdatedToEnv marks the tag this process was re-executed from, so a broken
// artifact cannot restart the update loop.
const UpdatedToEnv = "LIGHT_MEDIA_UPDATED_TO"

// maxAssetBytes bounds a downloaded artifact; the current build is ~60 MiB.
const maxAssetBytes = 256 << 20

type Options struct {
	Mode           string // "", "off", "release"
	CurrentVersion string // compiled version, e.g. "0.4.19"
	ReleasesURL    string
	DataDir        string
	GOOS, GOARCH   string
	Token          string
	Client         *http.Client
	Logf           func(format string, args ...any)
	// Exec replaces the current process. Tests inject a recorder.
	Exec func(path string, argv, env []string) error
}

type Asset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
}

type release struct {
	Tag        string  `json:"tag_name"`
	Draft      bool    `json:"draft"`
	Prerelease bool    `json:"prerelease"`
	Assets     []Asset `json:"assets"`
}

// Outcome describes what the check decided, for logs and tests.
type Outcome struct {
	Checked   bool
	Tag       string
	Installed string
	Execed    bool
	Reason    string
}

var versionPattern = regexp.MustCompile(`^[vV]?(\d+(?:\.\d+)*)$`)

// ModeEnabled reports whether a startup update should run. An empty mode falls
// back to the source default ("release"), matching MoviePilot's setting; "off",
// "false" and "0" disable it.
func ModeEnabled(mode string) bool {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", "true", "release", "dev", "1", "on":
		return true
	default:
		return false
	}
}

func digits(value string) []int {
	match := versionPattern.FindStringSubmatch(strings.TrimSpace(value))
	if match == nil {
		return nil
	}
	parts := strings.Split(match[1], ".")
	out := make([]int, len(parts))
	for i, part := range parts {
		n, err := strconv.Atoi(part)
		if err != nil {
			return nil
		}
		out[i] = n
	}
	return out
}

// Newer reports whether candidate is a strictly higher version than current.
func Newer(candidate, current string) bool {
	left, right := digits(candidate), digits(current)
	if left == nil || right == nil {
		return false
	}
	for i := 0; i < len(left) || i < len(right); i++ {
		var a, b int
		if i < len(left) {
			a = left[i]
		}
		if i < len(right) {
			b = right[i]
		}
		if a != b {
			return a > b
		}
	}
	return false
}

// SameSeries keeps the update on the current major line, the way the source
// update is pinned to its vN release prefix.
func SameSeries(candidate, current string) bool {
	left, right := digits(candidate), digits(current)
	return left != nil && right != nil && left[0] == right[0]
}

func assetName(goos, goarch string) []string {
	return []string{
		"light-media-server-" + goos + "-" + goarch,
		"light-media-linux-" + goarch,
	}
}

func checksumName(binary string) []string {
	return []string{binary + ".sha256", "SHA256SUMS", "sha256sums.txt", "checksums.txt"}
}

func findAsset(assets []Asset, names []string) (Asset, bool) {
	for _, want := range names {
		for _, asset := range assets {
			if strings.EqualFold(asset.Name, want) {
				return asset, true
			}
		}
	}
	return Asset{}, false
}

// selectRelease picks the highest non-prerelease tag inside the current series
// that carries a matching binary and a checksum asset.
func selectRelease(raw []byte, current, goos, goarch string) (release, Asset, Asset, string) {
	var releases []release
	if err := json.Unmarshal(raw, &releases); err != nil {
		return release{}, Asset{}, Asset{}, "release_feed_invalid"
	}
	sort.SliceStable(releases, func(i, j int) bool {
		left, right := digits(releases[i].Tag), digits(releases[j].Tag)
		for k := 0; k < len(left) || k < len(right); k++ {
			var a, b int
			if k < len(left) {
				a = left[k]
			}
			if k < len(right) {
				b = right[k]
			}
			if a != b {
				return a > b
			}
		}
		return false
	})
	for _, item := range releases {
		if item.Draft || item.Prerelease || !SameSeries(item.Tag, current) || !Newer(item.Tag, current) {
			continue
		}
		binary, ok := findAsset(item.Assets, assetName(goos, goarch))
		if !ok {
			continue
		}
		sums, ok := findAsset(item.Assets, checksumName(binary.Name))
		if !ok {
			continue
		}
		return item, binary, sums, ""
	}
	return release{}, Asset{}, Asset{}, "no_release_for_platform"
}

func (o Options) client() *http.Client {
	if o.Client != nil {
		return o.Client
	}
	// A startup check must never stall the listener; the source update is best
	// effort and the next start retries.
	return &http.Client{Timeout: 15 * time.Second}
}

func (o Options) fetch(ctx context.Context, target string) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	if o.Token != "" {
		request.Header.Set("Authorization", "Bearer "+o.Token)
	}
	response, err := o.client().Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("http %d", response.StatusCode)
	}
	return io.ReadAll(io.LimitReader(response.Body, maxAssetBytes))
}

// checksumFor reads "hash  name" / "hash *name" / bare-hash lines and returns
// the digest recorded for the binary.
func checksumFor(data []byte, binary string) (string, bool) {
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		digest := strings.ToLower(fields[0])
		if len(digest) != 64 || strings.Trim(digest, "0123456789abcdef") != "" {
			continue
		}
		if len(fields) == 1 || strings.TrimPrefix(fields[len(fields)-1], "*") == binary {
			return digest, true
		}
	}
	return "", false
}

func (o Options) logf(format string, args ...any) {
	if o.Logf != nil {
		o.Logf(format, args...)
	}
}

// Run performs one check. When it installs and re-executes, Run does not
// return: the process image is replaced.
func Run(ctx context.Context, opts Options) (Outcome, error) {
	if !ModeEnabled(opts.Mode) {
		return Outcome{Reason: "disabled"}, nil
	}
	if opts.DataDir == "" || opts.CurrentVersion == "" {
		return Outcome{Reason: "incomplete_configuration"}, nil
	}
	if opts.GOOS == "" || opts.GOARCH == "" {
		return Outcome{Reason: "unknown_platform"}, nil
	}
	if os.Getenv(UpdatedToEnv) != "" {
		// This process already replaced the mounted build; never loop.
		return Outcome{Reason: "already_updated_this_boot"}, nil
	}
	feed := opts.ReleasesURL
	if feed == "" {
		feed = DefaultReleasesURL
	}
	raw, err := opts.fetch(ctx, feed)
	if err != nil {
		return Outcome{Checked: true, Reason: "feed_unavailable"}, err
	}
	item, binary, sums, reason := selectRelease(raw, opts.CurrentVersion, opts.GOOS, opts.GOARCH)
	if reason != "" {
		return Outcome{Checked: true, Reason: reason}, nil
	}
	path, err := opts.install(ctx, item.Tag, binary, sums)
	if err != nil {
		return Outcome{Checked: true, Tag: item.Tag, Reason: "install_failed"}, err
	}
	outcome := Outcome{Checked: true, Tag: item.Tag, Installed: path}
	exec := opts.Exec
	if exec == nil {
		exec = Exec
	}
	if err = exec(path, os.Args, append(os.Environ(), UpdatedToEnv+"="+item.Tag)); err != nil {
		return outcome, err
	}
	outcome.Execed = true
	return outcome, nil
}

// install downloads, verifies and publishes the artifact under the data
// directory. It never touches the currently running file.
func (o Options) install(ctx context.Context, tag string, binary, sums Asset) (string, error) {
	payload, err := o.fetch(ctx, binary.URL)
	if err != nil {
		return "", err
	}
	expected, err := o.fetch(ctx, sums.URL)
	if err != nil {
		return "", err
	}
	want, ok := checksumFor(expected, binary.Name)
	if !ok {
		return "", errors.New("checksum entry missing")
	}
	actual := sha256.Sum256(payload)
	if hex.EncodeToString(actual[:]) != want {
		return "", errors.New("checksum mismatch")
	}
	dir := filepath.Join(o.DataDir, "bin")
	if err = os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	stage, err := os.CreateTemp(dir, ".incoming-*")
	if err != nil {
		return "", err
	}
	staged := stage.Name()
	cleanup := func() { _ = os.Remove(staged) }
	if _, err = stage.Write(payload); err != nil {
		stage.Close()
		cleanup()
		return "", err
	}
	if err = stage.Sync(); err != nil {
		stage.Close()
		cleanup()
		return "", err
	}
	if err = stage.Close(); err != nil {
		cleanup()
		return "", err
	}
	if err = os.Chmod(staged, 0755); err != nil {
		cleanup()
		return "", err
	}
	target := filepath.Join(dir, "light-media-server-"+strings.TrimPrefix(tag, "v"))
	if err = os.Rename(staged, target); err != nil {
		cleanup()
		return "", err
	}
	return target, nil
}
