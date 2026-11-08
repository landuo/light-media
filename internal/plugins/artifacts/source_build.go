package artifacts

import (
	"bytes"
	"context"
	"debug/elf"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"syscall"
	"time"

	"light-media/internal/plugins"
)

// SourceBuild identifies a Go package in a fixed commit of the index repository.
// License paths are also their installed paths. FrontendDir contains ready-to-serve
// assets and is copied into the manifest's frontend root without running a build script.
type SourceBuild struct {
	Commit       string   `json:"commit"`
	ModuleDir    string   `json:"module_dir,omitempty"`
	Package      string   `json:"package"`
	FrontendDir  string   `json:"frontend_dir,omitempty"`
	LicenseFiles []string `json:"license_files"`
	HostContract string   `json:"host_contract"`
}

var sourceCommitPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)
var sourceBuildSlot = make(chan struct{}, 1)

func sourceGitHubCoordinates(repository string) (owner, name string, ok bool) {
	if base, valid := githubRepositoryBase(repository); valid {
		repository = base + SourceIndexName
	}
	u, err := url.Parse(repository)
	if err != nil || u.Scheme != "https" || u.Host != "raw.githubusercontent.com" || u.User != nil || u.Fragment != "" {
		return "", "", false
	}
	parts := strings.Split(strings.TrimPrefix(u.Path, "/"), "/")
	if len(parts) != 4 || parts[3] != IndexName && parts[3] != SourceIndexName {
		return "", "", false
	}
	for _, part := range parts[:3] {
		if part == "" || part == "." || part == ".." || !githubRepositoryPart.MatchString(part) {
			return "", "", false
		}
	}
	return parts[0], parts[1], true
}

func validateSourceBuild(source SourceBuild, repository, platform string) error {
	if _, _, ok := sourceGitHubCoordinates(repository); !ok {
		return errors.New("source build requires a GitHub repository index")
	}
	if platform != "linux-amd64" && platform != "linux-arm64" {
		return errors.New("source build supports linux-amd64 and linux-arm64")
	}
	if !sourceCommitPattern.MatchString(source.Commit) {
		return errors.New("source build requires a full Git commit SHA")
	}
	if source.ModuleDir != "" && source.ModuleDir != "." && !plugins.ValidFrontendPath(source.ModuleDir) {
		return errors.New("invalid source Go module directory")
	}
	if source.Package != "." && (!strings.HasPrefix(source.Package, "./") ||
		!plugins.ValidFrontendPath(strings.TrimPrefix(source.Package, "./")) ||
		strings.Contains(source.Package, "...")) {
		return errors.New("source build requires one relative Go package")
	}
	if source.FrontendDir != "" && !plugins.ValidFrontendPath(source.FrontendDir) {
		return errors.New("invalid source frontend directory")
	}
	if len(source.LicenseFiles) == 0 || len(source.LicenseFiles) > MaxArchiveEntries ||
		strings.TrimSpace(source.HostContract) == "" || len(source.HostContract) > 256 {
		return errors.New("source build requires license files and a host contract")
	}
	licenses := make(map[string]bool, len(source.LicenseFiles))
	for _, name := range source.LicenseFiles {
		if !plugins.ValidFrontendPath(name) || licenses[name] || name == ".acquired.json" {
			return errors.New("invalid or duplicate source license path")
		}
		licenses[name] = true
	}
	return nil
}

func prepareSourceBuild(ctx context.Context, req Request, release Release, source SourceBuild, stage string, client *http.Client) (Artifact, error) {
	ctx, cancel := context.WithTimeout(ctx, 6*time.Minute)
	defer cancel()
	platform := req.Platform
	if platform == "" {
		platform = runtime.GOOS + "-" + runtime.GOARCH
	}
	if err := validateSourceBuild(source, req.RepositoryURL, platform); err != nil {
		return Artifact{}, err
	}
	if (source.FrontendDir != "") != (release.Manifest.Frontend != nil) {
		return Artifact{}, errors.New("source frontend directory does not match plugin manifest")
	}
	select {
	case sourceBuildSlot <- struct{}{}:
		defer func() { <-sourceBuildSlot }()
	case <-ctx.Done():
		return Artifact{}, ctx.Err()
	}
	goTool, err := exec.LookPath("go")
	if err != nil {
		return Artifact{}, errors.New("Go toolchain unavailable for plugin source build")
	}
	owner, repo, _ := sourceGitHubCoordinates(req.RepositoryURL)
	address := "https://codeload.github.com/" + owner + "/" + repo + "/zip/" + source.Commit
	if client == nil {
		client = &http.Client{Timeout: 45 * time.Second}
	}
	archiveClient := *client
	archiveClient.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}
	archive, err := download(ctx, &archiveClient, address, MaxArtifactBytes)
	if err != nil {
		return Artifact{}, err
	}
	// Docker's /tmp is bounded separately. Keep disposable files and reusable Go
	// caches beneath the app-owned package root instead.
	parent := filepath.Dir(stage)
	work, err := os.MkdirTemp(parent, ".build-")
	if err != nil {
		return Artifact{}, err
	}
	defer os.RemoveAll(work)
	checkout, err := unpackSourceBuild(archive, work, repo, source.Commit)
	if err != nil {
		return Artifact{}, err
	}
	module := filepath.Join(checkout, filepath.FromSlash(source.ModuleDir))
	info, err := os.Lstat(filepath.Join(module, "go.mod"))
	if err != nil || !info.Mode().IsRegular() || info.Size() < 1 || info.Size() > MaxIndexBytes {
		return Artifact{}, errors.New("source Go module is missing a bounded go.mod")
	}
	pkg, err := os.Stat(filepath.Join(module, filepath.FromSlash(source.Package)))
	if err != nil || !pkg.IsDir() {
		return Artifact{}, errors.New("source Go package directory missing")
	}
	cache := filepath.Join(parent, ".build-cache")
	buildCache, modCache := filepath.Join(cache, "objects"), filepath.Join(cache, "modules")
	temp := filepath.Join(work, "tmp")
	for _, dir := range []string{buildCache, modCache, temp} {
		if err = ensureRoot(dir); err != nil {
			return Artifact{}, err
		}
	}
	proxy, closeProxy, err := startSourceProxy(ctx, client)
	if err != nil {
		return Artifact{}, errors.New("cannot initialize Go module proxy")
	}
	defer closeProxy()
	arch := strings.TrimPrefix(platform, "linux-")
	env := []string{
		"HOME=" + work, "GOPATH=" + filepath.Join(work, "gopath"),
		"PATH=" + filepath.Dir(goTool), "TMPDIR=" + temp, "GOTMPDIR=" + temp,
		"GOCACHE=" + buildCache, "GOMODCACHE=" + modCache,
		"GOENV=off", "GOWORK=off", "GOTOOLCHAIN=local", "GOAUTH=off",
		"GOPROXY=" + proxy, "GOSUMDB=sum.golang.org", "GOVCS=*:off",
		"GOOS=linux", "GOARCH=" + arch, "CGO_ENABLED=0", "GOMAXPROCS=2", "GOMEMLIMIT=192MiB",
	}
	if _, err = runSourceGo(ctx, goTool, work, env, "telemetry", "off"); err != nil {
		if ctx.Err() != nil {
			return Artifact{}, ctx.Err()
		}
		return Artifact{}, errors.New("cannot initialize isolated Go toolchain")
	}
	data, err := runSourceGo(ctx, goTool, module, env, "mod", "edit", "-json")
	if err != nil {
		if ctx.Err() != nil {
			return Artifact{}, ctx.Err()
		}
		return Artifact{}, errors.New("cannot read plugin Go module")
	}
	var settings struct {
		Go      string
		Replace []json.RawMessage
	}
	if json.Unmarshal(data, &settings) != nil || len(settings.Replace) != 0 {
		return Artifact{}, errors.New("plugin Go module must not use replace directives")
	}
	entry := filepath.Join(stage, filepath.FromSlash(release.Manifest.Entry))
	if err = os.MkdirAll(filepath.Dir(entry), 0700); err != nil {
		return Artifact{}, err
	}
	data, err = runSourceGo(ctx, goTool, module, env, "build", "-mod=readonly", "-modcacherw", "-buildvcs=false",
		"-trimpath", "-p=1", "-ldflags=-linkmode=internal", "-o", entry, source.Package)
	if err != nil {
		if ctx.Err() != nil {
			return Artifact{}, ctx.Err()
		}
		if settings.Go != "" && strings.Contains(string(data), "requires go >=") {
			return Artifact{}, fmt.Errorf("plugin source requires Go %s or newer", settings.Go)
		}
		return Artifact{}, errors.New("Go plugin source compilation failed")
	}
	if err = verifySourceELF(entry, arch); err != nil {
		return Artifact{}, err
	}
	for _, name := range source.LicenseFiles {
		from := filepath.Join(checkout, filepath.FromSlash(name))
		info, err := os.Lstat(from)
		if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
			return Artifact{}, errors.New("declared source license file is missing or empty")
		}
		to := filepath.Join(stage, filepath.FromSlash(name))
		if err = os.MkdirAll(filepath.Dir(to), 0700); err != nil {
			return Artifact{}, err
		}
		if err = copyTree(from, to); err != nil {
			return Artifact{}, err
		}
	}
	if front := release.Manifest.Frontend; front != nil {
		from := filepath.Join(checkout, filepath.FromSlash(source.FrontendDir))
		info, err := os.Lstat(from)
		if err != nil || !info.IsDir() {
			return Artifact{}, errors.New("declared source frontend directory missing")
		}
		if err = copyTree(from, filepath.Join(stage, filepath.FromSlash(front.Root))); err != nil {
			return Artifact{}, err
		}
	}
	return sourceBuildInventory(stage, platform, release.Manifest, source.HostContract)
}

func unpackSourceBuild(archive []byte, work, repo, commit string) (string, error) {
	root := filepath.Join(work, "source")
	if err := os.Mkdir(root, 0700); err != nil {
		return "", err
	}
	if err := unpack(archive, "zip", root, ""); err != nil {
		return "", err
	}
	children, err := os.ReadDir(root)
	if err != nil {
		return "", err
	}
	if len(children) != 1 || !children[0].IsDir() || !strings.EqualFold(children[0].Name(), repo+"-"+commit) {
		return "", errors.New("GitHub source archive root does not match repository and commit")
	}
	return filepath.Join(root, children[0].Name()), nil
}

func verifySourceELF(name, arch string) error {
	info, err := os.Lstat(name)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > MaxArtifactBytes {
		return errors.New("compiled plugin worker is missing or exceeds size bound")
	}
	file, err := elf.Open(name)
	if err != nil {
		return errors.New("compiled plugin worker is not a Linux executable")
	}
	defer file.Close()
	want := elf.EM_X86_64
	if arch == "arm64" {
		want = elf.EM_AARCH64
	}
	if file.Class != elf.ELFCLASS64 || file.Data != elf.ELFDATA2LSB || file.Machine != want ||
		file.Type != elf.ET_EXEC && file.Type != elf.ET_DYN || file.Entry == 0 {
		return errors.New("compiled plugin worker does not match target platform")
	}
	return nil
}

func sourceBuildInventory(stage, platform string, manifest plugins.Manifest, contract string) (Artifact, error) {
	artifact := Artifact{Platform: platform, Format: "source", HostContract: contract}
	var total int64
	err := filepath.WalkDir(stage, func(name string, entry os.DirEntry, err error) error {
		if err != nil || name == stage {
			return err
		}
		rel := filepath.ToSlash(strings.TrimPrefix(name, stage+string(filepath.Separator)))
		if !plugins.ValidFrontendPath(rel) || rel == ".acquired.json" {
			return errors.New("invalid source package path")
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Size() > MaxExpandedBytes-total || len(artifact.Files) >= MaxArchiveEntries {
			return errors.New("invalid source package file or size")
		}
		total += info.Size()
		data, err := os.ReadFile(name)
		if err != nil {
			return err
		}
		sum := digest(data)
		artifact.Files = append(artifact.Files, PackageFile{Path: rel, Size: info.Size(), SHA256: sum})
		if rel == manifest.Entry {
			artifact.SHA256, artifact.ExecutableSHA256, artifact.Size = sum, sum, info.Size()
		}
		return nil
	})
	if err != nil {
		return Artifact{}, err
	}
	artifact.Files, err = validatePackageFiles(manifest, artifact.Files, artifact.ExecutableSHA256)
	return artifact, err
}

// Keep compiler output bounded and private. The compiler and its child
// processes share a process group so a canceled build does not leave children.
func runSourceGo(ctx context.Context, goTool, dir string, env []string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, goTool, args...)
	cmd.Dir, cmd.Env, cmd.WaitDelay = dir, env, time.Second
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	output := &sourceGoOutput{}
	cmd.Stdout, cmd.Stderr = output, output
	err := cmd.Run()
	return output.buffer.Bytes(), err
}

type sourceGoOutput struct{ buffer bytes.Buffer }

func (out *sourceGoOutput) Write(data []byte) (int, error) {
	if available := int(MaxIndexBytes) - out.buffer.Len(); available > 0 {
		_, _ = out.buffer.Write(data[:min(available, len(data))])
	}
	return len(data), nil
}
