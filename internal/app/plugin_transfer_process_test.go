package app

import (
	"context"
	"crypto/sha256"
	"debug/buildinfo"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
	"testing"
	"time"

	"light-media/internal/plugins"
)

// An isolated source validation must pass the same overlay to nested builds.
// In normal go test runs the empty default builds the ordinary working tree.
var transferWorkerSourceOverlay = flag.String("transfer-worker-source-overlay", "", "Go source overlay for Transfer fixture builds")

var transferFixtureDirectory struct {
	once sync.Once
	path string
	err  error
}

var transferSDKBinary, transferServerBinary struct {
	once sync.Once
	path string
	err  error
}

// There was no pre-existing TestMain in this package. Only our own unique
// MkdirTemp directory is removed; shared Go caches and source trees are not.
func TestMain(m *testing.M) {
	code := m.Run()
	if transferFixtureDirectory.path != "" {
		if err := os.RemoveAll(transferFixtureDirectory.path); err != nil {
			fmt.Fprintln(os.Stderr, "Transfer fixture cache cleanup failed:", err)
			code = 1
		}
	}
	os.Exit(code)
}

func useTransferSDKProcess(t *testing.T, a *App) {
	t.Helper()
	transferSDKBinary.once.Do(func() {
		transferSDKBinary.path, transferSDKBinary.err = buildTransferFixture(false)
	})
	if transferSDKBinary.err != nil {
		t.Fatal(transferSDKBinary.err)
	}
	useTransferExecutable(t, a, transferSDKBinary.path)
}

// Unlike the SDK layer this compiles the actual normal server entry point,
// including the built-in registry. --plugin-worker prevents an HTTP listener.
func useTransferServerProcess(t *testing.T, a *App) {
	t.Helper()
	transferServerBinary.once.Do(func() {
		transferServerBinary.path, transferServerBinary.err = buildTransferFixture(true)
	})
	if transferServerBinary.err != nil {
		t.Fatal(transferServerBinary.err)
	}
	useTransferExecutable(t, a, transferServerBinary.path)
}

func useTransferExecutable(t *testing.T, a *App, executable string) {
	t.Helper()
	a.pluginCommand = func(id string) (plugins.Command, error) {
		if id != "TorrentTransfer" && id != "TorrentTransfermusic" {
			return plugins.Command{}, fmt.Errorf("Transfer fixture rejects plugin %q", id)
		}
		return plugins.Command{Path: executable, Args: []string{"--plugin-worker", id}}, nil
	}
}

func transferBuildSettings(info *debug.BuildInfo) map[string]string {
	settings := make(map[string]string, len(info.Settings))
	for _, setting := range info.Settings {
		settings[setting.Key] = setting.Value
	}
	return settings
}

func buildTransferFixture(server bool) (string, error) {
	parentInfo, ok := debug.ReadBuildInfo()
	if !ok {
		return "", fmt.Errorf("Transfer fixture: parent Go build information unavailable")
	}
	parent := transferBuildSettings(parentInfo)
	for _, key := range []string{"CGO_ENABLED", "GOOS", "GOARCH"} {
		if parent[key] == "" {
			return "", fmt.Errorf("Transfer fixture: parent build setting %s unavailable", key)
		}
	}
	if parent["GOOS"] != runtime.GOOS || parent["GOARCH"] != runtime.GOARCH {
		return "", fmt.Errorf("Transfer fixture: parent platform settings mismatch")
	}
	transferFixtureDirectory.once.Do(func() {
		transferFixtureDirectory.path, transferFixtureDirectory.err = os.MkdirTemp("", "light-media-transfer-worker-build-")
	})
	if transferFixtureDirectory.err != nil {
		return "", fmt.Errorf("Transfer fixture directory: %w", transferFixtureDirectory.err)
	}
	root, err := os.Getwd()
	if err != nil {
		return "", err
	}
	// Go runs this package's tests in internal/app; walk upward to allow a
	// caller to run the compiled test binary from the repository root too.
	for {
		contents, readErr := os.ReadFile(filepath.Join(root, "go.mod"))
		if readErr == nil && strings.HasPrefix(string(contents), "module light-media\n") {
			break
		}
		next := filepath.Dir(root)
		if next == root {
			return "", fmt.Errorf("Transfer fixture: cannot locate light-media module")
		}
		root = next
	}
	name, target := "sdk", "./internal/app/testdata/transfer-worker"
	race := parent["-race"] == "true"
	if server {
		name, target, race = "server", "./cmd/server", false
	}
	work := filepath.Join(transferFixtureDirectory.path, name)
	for _, dir := range []string{work, filepath.Join(work, "home"), filepath.Join(work, "tmp")} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return "", err
		}
	}
	executable := filepath.Join(work, name)
	args := []string{"build", "-mod=readonly", "-buildvcs=false", "-o", executable}
	if race {
		args = append(args, "-race")
	}
	if tags := parent["-tags"]; tags != "" {
		args = append(args, "-tags", tags)
	}
	if *transferWorkerSourceOverlay != "" {
		overlay, err := filepath.Abs(*transferWorkerSourceOverlay)
		if err != nil {
			return "", err
		}
		if err := validateTransferSourceOverlay(root, overlay); err != nil {
			return "", err
		}
		args = append(args, "-overlay", overlay)
	}
	args = append(args, target)
	env := []string{
		"GOROOT=" + runtime.GOROOT(), "GOENV=off", "GOTOOLCHAIN=local",
		"GOPROXY=off", "GOSUMDB=off", "GOFLAGS=", "GOWORK=off",
		"HOME=" + filepath.Join(work, "home"), "TMPDIR=" + filepath.Join(work, "tmp"),
		"TMP=" + filepath.Join(work, "tmp"), "TEMP=" + filepath.Join(work, "tmp"),
	}
	// No credentials, proxy settings or arbitrary inherited Go flags enter
	// nested builds. Existing dependency/build caches remain reusable.
	for _, key := range []string{"PATH", "GOMODCACHE", "CC", "CXX"} {
		if value := os.Getenv(key); value != "" {
			env = append(env, key+"="+value)
		}
	}
	// Capture defaults before replacing HOME, so ordinary go test can reuse
	// dependencies even when GOPATH/GOCACHE were not explicitly exported.
	gopath := os.Getenv("GOPATH")
	if gopath == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("Transfer fixture GOPATH default: %w", err)
		}
		gopath = filepath.Join(home, "go")
	}
	gocache := os.Getenv("GOCACHE")
	if gocache == "" {
		cache, err := os.UserCacheDir()
		if err != nil {
			return "", fmt.Errorf("Transfer fixture GOCACHE default: %w", err)
		}
		gocache = filepath.Join(cache, "go-build")
	}
	env = append(env, "GOPATH="+gopath, "GOCACHE="+gocache)
	for _, key := range []string{"CGO_ENABLED", "CGO_CFLAGS", "CGO_CPPFLAGS", "CGO_CXXFLAGS", "CGO_LDFLAGS", "GOOS", "GOARCH", "GOAMD64", "GOARM", "GOARM64", "GO386", "GOMIPS", "GOMIPS64", "GOPPC64", "GORISCV64", "GOEXPERIMENT"} {
		if value, exists := parent[key]; exists {
			env = append(env, key+"="+value)
		}
	}
	// Compilation happens synchronously in setup, before any Runner call.
	// This deadline is unrelated to and does not extend the product's 10s.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, filepath.Join(runtime.GOROOT(), "bin", "go"), args...)
	cmd.Dir, cmd.Env = root, env
	cmd.WaitDelay = 5 * time.Second
	output, err := cmd.CombinedOutput() // Run/Wait reaps the build subprocess.
	if err != nil {
		return "", fmt.Errorf("Transfer %s fixture build failed (deadline=%v): %w\n%s", name, ctx.Err(), err, output)
	}
	builtInfo, err := buildinfo.ReadFile(executable)
	if err != nil {
		return "", fmt.Errorf("Transfer fixture binary build information: %w", err)
	}
	built := transferBuildSettings(builtInfo)
	if (built["-race"] == "true") != race {
		return "", fmt.Errorf("Transfer %s fixture race mode does not match requested mode", name)
	}
	for _, key := range []string{"CGO_ENABLED", "GOOS", "GOARCH", "-tags"} {
		if built[key] != parent[key] {
			return "", fmt.Errorf("Transfer %s fixture build setting %s does not match parent", name, key)
		}
	}
	if builtInfo.GoVersion != parentInfo.GoVersion {
		return "", fmt.Errorf("Transfer fixture toolchain does not match parent")
	}
	file, err := os.Open(executable)
	if err != nil {
		return "", err
	}
	digest := sha256.New()
	_, hashErr := io.Copy(digest, file)
	closeErr := file.Close()
	if hashErr != nil {
		return "", fmt.Errorf("Transfer fixture binary digest: %w", hashErr)
	}
	if closeErr != nil {
		return "", closeErr
	}
	// Retain verifiable artifact evidence in go test output before TestMain
	// removes this process's disposable binary cache. No environment is logged.
	fmt.Printf("transfer_fixture_verified kind=%s entry=%s race=%t sha256=%x\n", name, target, race, digest.Sum(nil))
	return executable, nil
}

func validateTransferSourceOverlay(root, overlay string) error {
	raw, err := os.ReadFile(overlay)
	if err != nil {
		return fmt.Errorf("Transfer fixture overlay read: %w", err)
	}
	var config struct {
		Replace map[string]string
	}
	if err := json.Unmarshal(raw, &config); err != nil {
		return fmt.Errorf("Transfer fixture overlay parse: %w", err)
	}
	if len(config.Replace) == 0 {
		return fmt.Errorf("Transfer fixture overlay must contain nonempty Replace")
	}
	for target, replacement := range config.Replace {
		if !filepath.IsAbs(target) || !filepath.IsAbs(replacement) {
			return fmt.Errorf("Transfer fixture overlay requires absolute targets and replacements")
		}
		target = filepath.Clean(target)
		relative, err := filepath.Rel(root, target)
		if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return fmt.Errorf("Transfer fixture overlay target is outside selected module")
		}
		// Merely checking containment is insufficient: a frozen checkout may
		// itself be nested under the ordinary tree's work/ directory. Reject
		// a target belonging to that other module instead of silently building
		// the wrong tree. Virtual new files need no physical parent directory.
		for dir := filepath.Dir(target); dir != root; dir = filepath.Dir(dir) {
			_, err := os.Stat(filepath.Join(dir, "go.mod"))
			if err == nil {
				return fmt.Errorf("Transfer fixture overlay target belongs to a different nested module")
			}
			if !os.IsNotExist(err) {
				return fmt.Errorf("Transfer fixture overlay module check: %w", err)
			}
		}
	}
	return nil
}
