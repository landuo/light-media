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
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"light-media/internal/networkproxy"
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
	// StatusPath receives one JSON record per startup check. Operators need the
	// reason on the device; the process is re-executed before any log sink opens.
	StatusPath string
	// StallTimeout aborts a transfer that stops producing bytes. It is the guard
	// that keeps a half-open connection from holding startup hostage.
	StallTimeout time.Duration
	// ProxySetting is the saved proxy from the instance settings. A nonblank
	// value wins over the environment, exactly like the rest of the app.
	ProxySetting string
	// TotalTimeout caps one transfer (default 20 minutes).
	TotalTimeout time.Duration
	// NoExec installs the artifact without replacing the running process. The
	// listener must never wait on a slow mirror, so the background path uses it
	// and the next start picks the build up from disk.
	NoExec bool
	// lastProxy records which hop actually served the run, for the status file.
	lastProxy string
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

// fetchSession carries the proxy that actually served a request. The configured
// proxy is tried first, then the environment: one flaky hop must not decide
// whether the update happens.
type fetchSession struct {
	opts  Options
	proxy string
}

func (o Options) newSession() *fetchSession {
	session := &fetchSession{opts: o}
	if o.Client == nil {
		session.proxy = o.proxySource()
	} else {
		session.proxy = "custom"
	}
	return session
}

func (o Options) transportFor(stage int) *http.Transport {
	transport := &http.Transport{
		ResponseHeaderTimeout: 20 * time.Second,
		IdleConnTimeout:       30 * time.Second,
		ForceAttemptHTTP2:     true,
	}
	switch stage {
	case 0:
		transport.Proxy = o.proxy()
	case 1:
		transport.Proxy = http.ProxyFromEnvironment
	}
	return transport
}

func (o Options) stall() time.Duration {
	if o.StallTimeout > 0 {
		return o.StallTimeout
	}
	return 60 * time.Second
}

// totalTimeout bounds one transfer. It is generous on purpose: a slow but
// progressing link must be able to finish, while the stall watchdog still
// aborts a dead one quickly.
func (o Options) totalTimeout() time.Duration {
	if o.TotalTimeout > 0 {
		return o.TotalTimeout
	}
	return 20 * time.Minute
}

// proxy resolves the instance proxy setting the same way the rest of the app
// does: a nonblank setting wins, otherwise the standard environment variables.
func (o Options) proxy() func(*http.Request) (*url.URL, error) {
	config, err := networkproxy.Resolve(o.ProxySetting, os.Getenv)
	if err != nil {
		return http.ProxyFromEnvironment
	}
	target := config.HTTPS
	if target == "" {
		target = config.HTTP
	}
	if target == "" {
		return nil
	}
	parsed, err := url.Parse(target)
	if err != nil || parsed.Host == "" {
		return http.ProxyFromEnvironment
	}
	return http.ProxyURL(parsed)
}

func (o Options) reportProxy() string {
	if o.lastProxy != "" {
		return o.lastProxy
	}
	return o.proxySource()
}

// proxySource labels the resolved path for the status record.
func (o Options) proxySource() string {
	config, err := networkproxy.Resolve(o.ProxySetting, os.Getenv)
	if err != nil {
		return "invalid"
	}
	if config.HTTPS != "" {
		if o.ProxySetting != "" {
			return "setting"
		}
		return "environment"
	}
	return "none"
}

func (o Options) fetch(ctx context.Context, target string) ([]byte, error) {
	return o.newSession().get(ctx, target)
}

func (s *fetchSession) client(stage int) *http.Client {
	if s.opts.Client != nil {
		return s.opts.Client
	}
	return &http.Client{Transport: s.opts.transportFor(stage)}
}

// stages: 0 = configured proxy, 1 = environment proxy. A blank stage is skipped.
func (s *fetchSession) stages() []int {
	if s.opts.Client != nil {
		return []int{0}
	}
	config, err := networkproxy.Resolve(s.opts.ProxySetting, os.Getenv)
	if err != nil {
		return []int{1}
	}
	if config.HTTPS == "" && config.HTTP == "" {
		return []int{0}
	}
	if s.opts.ProxySetting == "" {
		return []int{1}
	}
	return []int{0, 1}
}

func (s *fetchSession) get(ctx context.Context, target string) ([]byte, error) {
	var lastErr error
	for _, stage := range s.stages() {
		data, err := s.getWith(ctx, target, stage)
		if err == nil {
			switch stage {
			case 0:
				s.proxy = s.opts.proxySource()
			case 1:
				s.proxy = "environment"
			}
			return data, nil
		}
		lastErr = err
		if ctx.Err() != nil {
			break
		}
	}
	return nil, lastErr
}

func (s *fetchSession) getWith(ctx context.Context, target string, stage int) ([]byte, error) {
	o := s.opts
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	if o.Token != "" {
		request.Header.Set("Authorization", "Bearer "+o.Token)
	}
	response, err := s.client(stage).Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("http %d", response.StatusCode)
	}
	// Reset on progress, fire on silence: a stalled socket cancels the request
	// instead of burning the whole startup budget.
	watchdog := time.AfterFunc(o.stall(), cancel)
	defer watchdog.Stop()
	hardCap := time.AfterFunc(o.totalTimeout(), cancel)
	defer hardCap.Stop()
	var out bytes.Buffer
	buf := make([]byte, 32<<10)
	for {
		n, readErr := response.Body.Read(buf)
		if n > 0 {
			watchdog.Reset(o.stall())
			if out.Len()+n > maxAssetBytes {
				return nil, errors.New("asset too large")
			}
			out.Write(buf[:n])
		}
		if readErr == io.EOF {
			return out.Bytes(), nil
		}
		if readErr != nil {
			if ctx.Err() != nil {
				return nil, errors.New("transfer stalled")
			}
			return nil, readErr
		}
	}
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

type statusRecord struct {
	Time      string `json:"time"`
	Mode      string `json:"mode"`
	Current   string `json:"current_version"`
	Checked   bool   `json:"checked"`
	Tag       string `json:"tag,omitempty"`
	Installed string `json:"installed,omitempty"`
	Execed    bool   `json:"execed"`
	Reason    string `json:"reason,omitempty"`
	Error     string `json:"error,omitempty"`
	Feed      string `json:"feed"`
	Platform  string `json:"platform"`
	Proxy     string `json:"proxy"`
}

// writeStatus records the attempt so a container without log access can still
// explain what happened at startup.
// WriteStatus records an attempt. The background updater calls it directly
// because it is not inside Run.
func (o Options) WriteStatus(outcome Outcome, runErr error) { o.writeStatus(outcome, runErr) }

func (o Options) writeStatus(outcome Outcome, runErr error) {
	if o.StatusPath == "" {
		return
	}
	record := statusRecord{
		Time: nowUTC(), Mode: o.Mode, Current: o.CurrentVersion, Checked: outcome.Checked,
		Tag: outcome.Tag, Installed: outcome.Installed, Execed: outcome.Execed,
		Reason: outcome.Reason, Feed: o.ReleasesURL, Platform: o.GOOS + "/" + o.GOARCH,
		Proxy: o.reportProxy(),
	}
	if record.Feed == "" {
		record.Feed = DefaultReleasesURL
	}
	if runErr != nil {
		record.Error = runErr.Error()
	}
	payload, err := json.Marshal(record)
	if err != nil {
		return
	}
	dir := filepath.Dir(o.StatusPath)
	if err = os.MkdirAll(dir, 0755); err != nil {
		return
	}
	staged := o.StatusPath + ".tmp"
	if err = os.WriteFile(staged, append(payload, '\n'), 0600); err != nil {
		return
	}
	_ = os.Rename(staged, o.StatusPath)
}

// installedBuild returns the highest verified build already published under
// <data>/bin. It makes an update durable across restarts and keeps startup off
// the network when the artifact is already on disk.
func (o Options) installedBuild() (string, string) {
	entries, err := os.ReadDir(filepath.Join(o.DataDir, "bin"))
	if err != nil {
		return "", ""
	}
	bestPath, bestVersion := "", ""
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasPrefix(name, "light-media-server-") {
			continue
		}
		version := strings.TrimPrefix(name, "light-media-server-")
		if !Newer(version, o.CurrentVersion) || !SameSeries(version, o.CurrentVersion) {
			continue
		}
		if bestVersion == "" || Newer(version, bestVersion) {
			info, statErr := entry.Info()
			if statErr != nil || info.Mode().Perm()&0111 == 0 {
				continue
			}
			bestPath, bestVersion = filepath.Join(o.DataDir, "bin", name), version
		}
	}
	return bestPath, bestVersion
}

// recentInstallFailure reports whether the previous attempt failed while
// transferring. A broken mirror must not cost the whole budget on every start.
func (o Options) recentInstallFailure(now time.Time) bool {
	raw, err := os.ReadFile(o.StatusPath)
	if err != nil {
		return false
	}
	var record struct {
		Time   string `json:"time"`
		Reason string `json:"reason"`
	}
	if json.Unmarshal(raw, &record) != nil || record.Reason != "install_failed" {
		return false
	}
	at, err := time.Parse(time.RFC3339, record.Time)
	if err != nil || at.After(now) {
		return false
	}
	return now.Sub(at) < installFailureBackoff
}

const installFailureBackoff = 6 * time.Hour

func nowUTC() string { return time.Now().UTC().Format(time.RFC3339) }

// Run performs one check. When it installs and re-executes, Run does not
// return: the process image is replaced.
func Run(ctx context.Context, opts Options) (Outcome, error) {
	outcome, runErr := run(ctx, opts)
	opts.writeStatus(outcome, runErr)
	return outcome, runErr
}

// PreferInstalled is the only step allowed to block startup: it re-executes an
// artifact that is already verified on disk and needs no network.
func PreferInstalled(opts Options) (Outcome, error) {
	if !ModeEnabled(opts.Mode) {
		return Outcome{Reason: "disabled"}, nil
	}
	if os.Getenv(UpdatedToEnv) != "" {
		return Outcome{Reason: "already_updated_this_boot"}, nil
	}
	path, version := opts.installedBuild()
	if path == "" {
		return Outcome{Reason: "no_local_build"}, nil
	}
	outcome := Outcome{Tag: "v" + version, Installed: path, Reason: "local_build"}
	exec := opts.Exec
	if exec == nil {
		exec = Exec
	}
	if err := exec(path, os.Args, append(os.Environ(), UpdatedToEnv+"="+version)); err != nil {
		return outcome, err
	}
	outcome.Execed = true
	return outcome, nil
}

// UpdateFromFeed runs after the listener is up. A slow mirror costs the
// operator nothing but a completed download applied on the next restart.
func UpdateFromFeed(ctx context.Context, opts Options) (Outcome, error) {
	if !ModeEnabled(opts.Mode) {
		return Outcome{Reason: "disabled"}, nil
	}
	return run(ctx, opts)
}

func run(ctx context.Context, opts Options) (Outcome, error) {
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
	// A verified build already on disk wins: restarting must not depend on the
	// feed, and it must not re-download the same artifact.
	if path, version := opts.installedBuild(); path != "" {
		outcome := Outcome{Checked: false, Tag: "v" + version, Installed: path, Reason: "local_build"}
		exec := opts.Exec
		if exec == nil {
			exec = Exec
		}
		if err := exec(path, os.Args, append(os.Environ(), UpdatedToEnv+"="+version)); err != nil {
			return outcome, err
		}
		outcome.Execed = true
		return outcome, nil
	}
	if opts.StatusPath != "" && opts.recentInstallFailure(time.Now()) {
		return Outcome{Reason: "backoff_after_failure"}, nil
	}
	feed := opts.ReleasesURL
	if feed == "" {
		feed = DefaultReleasesURL
	}
	session := opts.newSession()
	raw, err := session.get(ctx, feed)
	if err != nil {
		return Outcome{Checked: true, Reason: "feed_unavailable"}, err
	}
	item, binary, sums, reason := selectRelease(raw, opts.CurrentVersion, opts.GOOS, opts.GOARCH)
	if reason != "" {
		return Outcome{Checked: true, Reason: reason}, nil
	}
	path, err := session.install(ctx, item.Tag, binary, sums)
	if err != nil {
		return Outcome{Checked: true, Tag: item.Tag, Reason: "install_failed"}, err
	}
	opts.lastProxy = session.proxy
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

// directory. It never touches the currently running file.
func (s *fetchSession) install(ctx context.Context, tag string, binary, sums Asset) (string, error) {
	o := s.opts
	payload, err := s.get(ctx, binary.URL)
	if err != nil {
		return "", err
	}
	expected, err := s.get(ctx, sums.URL)
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
