// Package artifacts acquires declared Go worker artifacts. Declarations are not
// host authority, and acquisition never executes the downloaded program.
package artifacts

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
	"strings"
	"sync"
	"time"

	"light-media/internal/plugins"
)

const (
	IndexName               = "go-plugins.v1.json"
	SourceIndexName         = "go-plugins.v2.json"
	MaxIndexBytes     int64 = 2 << 20
	MaxArtifactBytes  int64 = 64 << 20
	MaxExpandedBytes  int64 = 128 << 20
	MaxArchiveEntries       = 4096
)

// RepositoryURL names the index URL, not a Python package.json or a GitHub
// repository webpage. An empty ReleaseVersion selects the explicitly indexed
// CurrentVersion; it never falls back from a requested release or platform.
type Request struct {
	PreserveUnchangedRuntime                                bool `json:"-"` // Trusted local watcher: stage same-worker resource changes before in-place publication.
	DeveloperLocalWatch                                     bool // Trusted host DEV watcher only; never an HTTP/metadata authority. SDK/platform/hash checks still apply.
	Root, RepositoryURL, PluginID, ReleaseVersion, Platform string
	LocalRoots                                              []string // trusted configured directories, never supplied by repository metadata
	Force                                                   bool
	RefreshBackup                                           bool   // trusted App runtime mode, corresponding to original Docker-only refresh
	BackupRoot                                              string // trusted persistent code directory; defaults to Root/.backups
	// Validate is a required trusted-host gate, run before any installed tree
	// changes. A nil gate is rejected; the caller must supply an explicit policy.
	Validate func(plugins.Manifest, string) error
}
type Acquired struct {
	FrontendClass                        string // Actual class spelling in frontend content, independently of the compiled worker identity.
	RuntimePreserved                     bool   `json:"-"` // This acquisition retained the runtime directory and executable inode.
	Manifest                             plugins.Manifest
	ExecutablePath, SHA256, HostContract string
	PackageSHA256                        string // complete immutable package identity; empty for legacy worker-only packages
	Files                                []PackageFile
	RepositoryURL                        string // inert source provenance, never host authority
	Command                              plugins.Command
	BackupWarning                        string // post-success backup refresh failure is nonfatal
}
type Index struct {
	SystemVersion  json.RawMessage   `json:"system_version,omitempty"`
	Level          *int              `json:"level,omitempty"`
	PublicKey      string            `json:"key,omitempty"`
	SchemaVersion  int               `json:"schema_version"`
	PluginID       string            `json:"plugin_id"`
	CurrentVersion string            `json:"current_version"`
	Releases       []Release         `json:"releases"`
	History        map[string]string `json:"history,omitempty"` // author-written notes, not evidence of an installable release
	HistoryJSON    json.RawMessage   `json:"-"`                 // preserve the author's displayed key order
	Label          string            `json:"plugin_label,omitempty"`
	AddTime        int               `json:"add_time,omitempty"` // author-supplied listing order, not an invented installation timestamp
}
type Release struct {
	Version   string           `json:"version"`
	Manifest  plugins.Manifest `json:"manifest"`
	Artifacts []Artifact       `json:"artifacts,omitempty"`
	Source    *SourceBuild     `json:"source,omitempty"`
}
type Artifact struct {
	Platform         string        `json:"platform"`
	URL              string        `json:"url"`
	SHA256           string        `json:"sha256"`
	Size             int64         `json:"size"`
	Format           string        `json:"format"` // executable, zip, or tar.gz
	ExecutableSHA256 string        `json:"executable_sha256"`
	HostContract     string        `json:"host_contract"`
	Files            []PackageFile `json:"files,omitempty"`
}

// StageError distinguishes preflight rejection (old code untouched), failure
// after removal (possibly restored), and a failed restoration (backup retained).
type StageError struct {
	Stage        string
	Restored     bool
	RecoveryPath string // nonempty only when restoration/cleanup failed
	Err          error
}

func (e *StageError) Error() string { return fmt.Sprintf("plugin acquisition %s: %v", e.Stage, e.Err) }
func (e *StageError) Unwrap() error { return e.Err }

var operationMu sync.Mutex

type installedIdentifier struct{}

func (installedIdentifier) MatchString(value string) bool { return plugins.ValidInstanceID(value) }

var idPattern installedIdentifier
var platformPattern = regexp.MustCompile(`^[a-z0-9]+-[a-z0-9]+$`)

func Acquire(ctx context.Context, req Request, client *http.Client) (Acquired, error) {
	prepared, err := Prepare(ctx, req, client)
	if err != nil {
		return Acquired{}, err
	}
	defer prepared.Close()
	return prepared.Publish(ctx)
}

// Preparation binds selection to one metadata response. Source builds complete
// before publication locks are taken, so installed workers can keep serving.
type Preparation struct {
	request     Request
	client      *http.Client
	release     Release
	artifact    Artifact
	sourceStage string
	packageSHA  string
}

func Prepare(ctx context.Context, req Request, client *http.Client) (*Preparation, error) {
	pre := func(err error) (*Preparation, error) { return nil, &StageError{Stage: "preflight", Err: err} }
	if !idPattern.MatchString(req.PluginID) || req.Root == "" {
		return pre(errors.New("invalid root or plugin ID"))
	}
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	release, artifact, err := inspectMetadata(ctx, req, client)
	if err != nil {
		return pre(err)
	}
	p := &Preparation{request: req, client: client, release: release, artifact: artifact}
	if release.Source == nil {
		return p, nil
	}
	root, err := filepath.Abs(req.Root)
	if err != nil {
		return pre(err)
	}
	if err = ensureRoot(root); err != nil {
		return pre(err)
	}
	p.sourceStage, err = os.MkdirTemp(root, ".prepare-source-")
	if err != nil {
		return pre(err)
	}
	buildCtx, cancel := context.WithTimeout(ctx, 8*time.Minute)
	defer cancel()
	req.Platform = artifact.Platform
	p.artifact, err = prepareSourceBuild(buildCtx, req, release, *release.Source, p.sourceStage, client)
	if err == nil {
		p.packageSHA, err = preparePackage(buildCtx, req, release, p.artifact, p.sourceStage)
	}
	if err != nil {
		p.Close()
		return nil, &StageError{Stage: "source build", Err: err}
	}
	return p, nil
}

func (p *Preparation) Close() {
	if p.sourceStage != "" {
		_ = os.RemoveAll(p.sourceStage)
	}
}

func (p *Preparation) Publish(ctx context.Context) (Acquired, error) {
	operationMu.Lock()
	defer operationMu.Unlock()
	req, client, release, artifact := p.request, p.client, p.release, p.artifact
	pre := func(err error) (Acquired, error) { return Acquired{}, &StageError{Stage: "preflight", Err: err} }
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return pre(err)
	}
	root, err := filepath.Abs(req.Root)
	if err != nil {
		return pre(err)
	}
	if err = ensureRoot(root); err != nil {
		return pre(err)
	}
	target := filepath.Join(root, req.PluginID)
	if err = safeExistingTree(target); err != nil {
		return pre(err)
	}
	if release.Source != nil {
		if err = verifyPackageTree(p.sourceStage, artifact.Files, true); err != nil {
			return pre(err)
		}
	}
	preserveRuntime := false
	if req.PreserveUnchangedRuntime && IsLocalRepository(req.RepositoryURL) {
		if installed, readErr := readInstalled(root, req.PluginID, req.Validate); readErr == nil && installed.HostContract == artifact.HostContract && strings.EqualFold(installed.SHA256, artifact.ExecutableSHA256) {
			before, _ := json.Marshal(installed.Manifest)
			after, _ := json.Marshal(release.Manifest)
			preserveRuntime = bytes.Equal(before, after)
		}
	}
	// Legacy installs keep the original replacement order. The source stage
	// already exists and always gets a recovery backup, including force updates.
	backup := ""
	if (!req.Force || release.Source != nil) && !preserveRuntime {
		if _, statErr := os.Lstat(target); statErr == nil {
			backup, err = os.MkdirTemp(root, ".backup-")
			if err != nil {
				return pre(err)
			}
			if err = copyTree(target, filepath.Join(backup, "code")); err != nil {
				os.RemoveAll(backup)
				return pre(err)
			}
		} else if !os.IsNotExist(statErr) {
			return pre(statErr)
		}
	}
	fail := func(stage string, cause error) (Acquired, error) {
		if preserveRuntime {
			var resourceError *StageError
			if errors.As(cause, &resourceError) && resourceError.RecoveryPath != "" {
				return Acquired{}, resourceError
			}
			return Acquired{}, &StageError{Stage: stage, Err: cause}
		}
		cleanupErr := os.RemoveAll(target)
		restored := false
		if backup != "" && cleanupErr == nil {
			cleanupErr = os.Rename(filepath.Join(backup, "code"), target)
			restored = cleanupErr == nil
		}
		if cleanupErr == nil && backup != "" {
			os.RemoveAll(backup)
		}
		recoveryPath := ""
		if cleanupErr != nil {
			recoveryPath = backup
		}
		return Acquired{}, &StageError{Stage: stage, Restored: restored, RecoveryPath: recoveryPath, Err: errors.Join(cause, cleanupErr)}
	}
	if !preserveRuntime {
		if err = os.RemoveAll(target); err != nil {
			return fail("remove", err)
		}
	}
	stage, packageSHA := p.sourceStage, p.packageSHA
	if release.Source == nil {
		var payload []byte
		if IsLocalRepository(req.RepositoryURL) {
			localRoot, rootErr := trustedLocalRoot(req.RepositoryURL, req.LocalRoots)
			if rootErr != nil {
				return fail("prepare", rootErr)
			}
			payload, err = readLocalFile(ctx, localRoot, artifact.URL, artifact.Size)
		} else {
			payload, err = download(ctx, client, artifact.URL, artifact.Size)
		}
		if err != nil {
			return fail("prepare", err)
		}
		if int64(len(payload)) != artifact.Size || digest(payload) != strings.ToLower(artifact.SHA256) {
			return fail("prepare", errors.New("artifact size or SHA256 mismatch"))
		}
		stage, err = os.MkdirTemp(root, ".prepare-")
		if err != nil {
			return fail("prepare", err)
		}
		defer os.RemoveAll(stage)
		if err = unpack(payload, artifact.Format, stage, release.Manifest.Entry); err != nil {
			return fail("prepare", err)
		}
		packageSHA, err = preparePackage(ctx, req, release, artifact, stage)
		if err != nil {
			return fail("prepare", err)
		}
	}
	// Go workers are self-contained: there is no Python requirements step, shell,
	// package install, postinstall hook or execution of self-declared commands.
	if err = ctx.Err(); err != nil {
		return fail("prepare", err)
	}
	if preserveRuntime {
		err = publishResourcesInPlace(ctx, root, req.PluginID, filepath.Base(stage), release.Manifest.Entry, func() error { _, err := readInstalled(root, req.PluginID, req.Validate); return err }, nil)
	} else {
		err = os.Rename(stage, target)
	}
	if err != nil {
		return fail("prepare", err)
	}
	if backup != "" {
		os.RemoveAll(backup)
	}
	warning := ""
	if req.RefreshBackup {
		backupRoot := req.BackupRoot
		if backupRoot == "" {
			backupRoot = filepath.Join(root, ".backups")
		}
		if err := backupInstalled(root, backupRoot, req.PluginID, true, req.Validate); err != nil {
			warning = err.Error()
		}
	}
	executable := filepath.Join(target, filepath.FromSlash(release.Manifest.Entry))
	return Acquired{FrontendClass: release.Manifest.PluginID, RuntimePreserved: preserveRuntime, BackupWarning: warning, Manifest: release.Manifest, ExecutablePath: executable, SHA256: strings.ToLower(artifact.ExecutableSHA256), HostContract: artifact.HostContract, RepositoryURL: req.RepositoryURL, PackageSHA256: packageSHA, Files: artifact.Files, Command: plugins.Command{Path: executable, Dir: target}}, nil
}

func preparePackage(ctx context.Context, req Request, release Release, artifact Artifact, stage string) (string, error) {
	executable := filepath.Join(stage, filepath.FromSlash(release.Manifest.Entry))
	info, err := os.Lstat(executable)
	if err != nil || !info.Mode().IsRegular() || info.Size() > MaxExpandedBytes {
		return "", errors.New("entry is not a bounded regular file")
	}
	data, err := os.ReadFile(executable)
	if err != nil {
		return "", err
	}
	sum := digest(data)
	if sum != strings.ToLower(artifact.ExecutableSHA256) {
		return "", errors.New("executable SHA256 mismatch")
	}
	if err = verifyPackageTree(stage, artifact.Files, false); err != nil {
		return "", err
	}
	packageSHA, err := packageDigest(release.Manifest, artifact.HostContract, artifact.Files)
	if err != nil {
		return "", err
	}
	receipt, err := json.Marshal(installedReceipt{Manifest: release.Manifest, SHA256: sum, HostContract: artifact.HostContract, RepositoryURL: req.RepositoryURL, Files: artifact.Files, PackageSHA256: packageSHA})
	if err != nil {
		return "", err
	}
	if int64(len(receipt)) > MaxIndexBytes {
		return "", errors.New("package receipt exceeds size bound")
	}
	if err = ctx.Err(); err != nil {
		return "", err
	}
	receiptFile, err := os.OpenFile(filepath.Join(stage, ".acquired.json"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return "", err
	}
	_, err = receiptFile.Write(receipt)
	if err = errors.Join(err, receiptFile.Close()); err != nil {
		return "", err
	}
	if err = os.Chmod(executable, 0700); err != nil {
		return "", err
	}
	return packageSHA, nil
}

func selectArtifact(index Index, req Request) (Release, Artifact, error) {
	bad := func(s string) (Release, Artifact, error) { return Release{}, Artifact{}, errors.New(s) }
	if (index.SchemaVersion != 1 && index.SchemaVersion != 2) || index.PluginID != req.PluginID {
		return bad("index schema or plugin identity mismatch")
	}
	// Fixed install checks the latest declaration only for current installs;
	// an explicitly selected older Release bypasses that current-only range.
	if (!req.DeveloperLocalWatch || !IsLocalRepository(req.RepositoryURL)) && (req.ReleaseVersion == "" || req.ReleaseVersion == index.CurrentVersion || IsLocalRepository(req.RepositoryURL)) {
		if compatible, message := CheckSystemVersion(index.SystemVersion, MoviePilotCompatibilityVersion); !compatible {
			return bad(message)
		}
	}
	version := req.ReleaseVersion
	if version == "" {
		version = index.CurrentVersion
	}
	if version == "" {
		return bad("missing release version")
	}
	versions := map[string]bool{}
	var selected *Release
	for i := range index.Releases {
		r := &index.Releases[i]
		if versions[r.Version] {
			return bad("duplicate release")
		}
		versions[r.Version] = true
		if r.Version == version {
			selected = r
		}
	}
	if selected == nil {
		return bad("requested release unavailable")
	}
	r := *selected
	if r.Manifest.PluginID != req.PluginID || r.Manifest.Version != version || r.Version != version {
		return bad("release manifest identity/version mismatch")
	}
	if err := r.Manifest.Validate(); err != nil {
		return Release{}, Artifact{}, err
	}
	if !safeName(r.Manifest.Entry) {
		return bad("unsafe executable entry")
	}
	if index.SchemaVersion == 2 {
		if r.Source == nil || len(r.Artifacts) != 0 || (r.Manifest.Frontend != nil) != (r.Source.FrontendDir != "") {
			return bad("source release requires one Go source declaration and matching frontend resources")
		}
		if err := validateSourceBuild(*r.Source, req.RepositoryURL, req.Platform); err != nil {
			return Release{}, Artifact{}, err
		}
		return r, Artifact{Platform: req.Platform, HostContract: r.Source.HostContract}, nil
	}
	if r.Source != nil {
		return bad("source release requires repository schema 2")
	}
	platforms := map[string]bool{}
	var found *Artifact
	for i := range r.Artifacts {
		a := &r.Artifacts[i]
		if platforms[a.Platform] {
			return bad("duplicate platform")
		}
		platforms[a.Platform] = true
		if a.Platform == req.Platform {
			found = a
		}
	}
	if found == nil {
		return bad("requested platform unavailable")
	}
	a := *found
	if a.Size <= 0 || a.Size > MaxArtifactBytes || !validDigest(a.SHA256) || !validDigest(a.ExecutableSHA256) || len(a.HostContract) > 256 {
		return bad("invalid artifact limits or digests")
	}
	if a.Format != "executable" && a.Format != "zip" && a.Format != "tar.gz" {
		return bad("unsupported artifact format")
	}
	if r.Manifest.Frontend != nil && a.Format == "executable" {
		return bad("frontend assets require an archive artifact")
	}
	files, err := validatePackageFiles(r.Manifest, a.Files, a.ExecutableSHA256)
	if err != nil {
		return Release{}, Artifact{}, err
	}
	a.Files = files
	if IsLocalRepository(req.RepositoryURL) {
		if !safeName(a.URL) {
			return bad("local artifact requires a relative path within its repository")
		}
	} else if _, err := httpURL(a.URL); err != nil {
		return Release{}, Artifact{}, err
	}
	return r, a, nil
}
func httpURL(raw string) (*url.URL, error) {
	u, e := url.Parse(raw)
	if e != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.Fragment != "" {
		return nil, errors.New("artifact URL must be absolute HTTP(S), without credentials or fragment")
	}
	return u, nil
}

type downloadStatusError struct{ Code int }

func (e *downloadStatusError) Error() string { return fmt.Sprintf("HTTP status %d", e.Code) }
func download(ctx context.Context, client *http.Client, raw string, limit int64) ([]byte, error) {
	if _, err := httpURL(raw); err != nil {
		return nil, err
	}
	clone := *client
	previous := clone.CheckRedirect
	clone.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("too many redirects")
		}
		if _, err := httpURL(req.URL.String()); err != nil {
			return err
		}
		if via[0].URL.Scheme == "https" && req.URL.Scheme != "https" {
			return errors.New("HTTPS downgrade")
		}
		if previous != nil {
			return previous(req, via)
		}
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return nil, err
	}
	resp, err := clone.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		// net/http errors can include signed query URLs or remote parser text.
		// Installation failures are public API messages; do not echo either.
		return nil, errors.New("artifact request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, &downloadStatusError{Code: resp.StatusCode}
	}
	if resp.ContentLength > limit {
		return nil, errors.New("download exceeds size bound")
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("artifact response read failed")
	}
	if int64(len(data)) > limit {
		return nil, errors.New("download exceeds size bound")
	}
	return data, nil
}
func digest(b []byte) string    { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func validDigest(s string) bool { b, e := hex.DecodeString(s); return e == nil && len(b) == 32 }
