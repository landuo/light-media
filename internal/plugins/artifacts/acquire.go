// Package artifacts acquires declared Go worker artifacts. Declarations are not
// host authority, and acquisition never executes the downloaded program.
package artifacts

import (
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
	MaxIndexBytes     int64 = 2 << 20
	MaxArtifactBytes  int64 = 64 << 20
	MaxExpandedBytes  int64 = 128 << 20
	MaxArchiveEntries       = 4096
)

// RepositoryURL names the index URL, not a Python package.json or a GitHub
// repository webpage. An empty ReleaseVersion selects the explicitly indexed
// CurrentVersion; it never falls back from a requested release or platform.
type Request struct {
	Root, RepositoryURL, PluginID, ReleaseVersion, Platform string
	Force                                                   bool
	RefreshBackup                                           bool // trusted App runtime mode, corresponding to original Docker-only refresh
	// Validate is a required trusted-host gate, run before any installed tree
	// changes. A nil gate is rejected; the caller must supply an explicit policy.
	Validate func(plugins.Manifest, string) error
}
type Acquired struct {
	Manifest                             plugins.Manifest
	ExecutablePath, SHA256, HostContract string
	RepositoryURL                        string // inert source provenance, never host authority
	Command                              plugins.Command
	BackupWarning                        string // post-success backup refresh failure is nonfatal
}
type Index struct {
	SchemaVersion  int       `json:"schema_version"`
	PluginID       string    `json:"plugin_id"`
	CurrentVersion string    `json:"current_version"`
	Releases       []Release `json:"releases"`
}
type Release struct {
	Version   string           `json:"version"`
	Manifest  plugins.Manifest `json:"manifest"`
	Artifacts []Artifact       `json:"artifacts"`
}
type Artifact struct {
	Platform         string `json:"platform"`
	URL              string `json:"url"`
	SHA256           string `json:"sha256"`
	Size             int64  `json:"size"`
	Format           string `json:"format"` // executable, zip, or tar.gz
	ExecutableSHA256 string `json:"executable_sha256"`
	HostContract     string `json:"host_contract"`
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
var idPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.-]{0,127}$`)
var platformPattern = regexp.MustCompile(`^[a-z0-9]+-[a-z0-9]+$`)

func Acquire(ctx context.Context, req Request, client *http.Client) (Acquired, error) {
	operationMu.Lock()
	defer operationMu.Unlock()
	var out Acquired
	pre := func(err error) (Acquired, error) { return out, &StageError{Stage: "preflight", Err: err} }
	if !idPattern.MatchString(req.PluginID) || req.Root == "" {
		return pre(errors.New("invalid root or plugin ID"))
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	release, artifact, err := inspectMetadata(ctx, req, client)
	if err != nil {
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
	// Original install flow: backup, remove old code, THEN download/prepare.
	backup := ""
	if !req.Force {
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
	if err = os.RemoveAll(target); err != nil {
		return fail("remove", err)
	}
	payload, err := download(ctx, client, artifact.URL, artifact.Size)
	if err != nil {
		return fail("prepare", err)
	}
	if int64(len(payload)) != artifact.Size || digest(payload) != strings.ToLower(artifact.SHA256) {
		return fail("prepare", errors.New("artifact size or SHA256 mismatch"))
	}
	stage, err := os.MkdirTemp(root, ".prepare-")
	if err != nil {
		return fail("prepare", err)
	}
	defer os.RemoveAll(stage)
	if err = unpack(payload, artifact.Format, stage, release.Manifest.Entry); err != nil {
		return fail("prepare", err)
	}
	executable := filepath.Join(stage, filepath.FromSlash(release.Manifest.Entry))
	data, err := os.ReadFile(executable)
	if err != nil {
		return fail("prepare", err)
	}
	info, err := os.Lstat(executable)
	if err != nil || !info.Mode().IsRegular() {
		return fail("prepare", errors.New("entry is not a regular file"))
	}
	if digest(data) != strings.ToLower(artifact.ExecutableSHA256) {
		return fail("prepare", errors.New("executable SHA256 mismatch"))
	}
	receipt, err := json.Marshal(struct {
		Manifest      plugins.Manifest `json:"manifest"`
		SHA256        string           `json:"sha256"`
		HostContract  string           `json:"host_contract"`
		RepositoryURL string           `json:"repository_url,omitempty"`
	}{release.Manifest, digest(data), artifact.HostContract, req.RepositoryURL})
	if err != nil {
		return fail("prepare", err)
	}
	receiptFile, err := os.OpenFile(filepath.Join(stage, ".acquired.json"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return fail("prepare", err)
	}
	_, err = receiptFile.Write(receipt)
	err = errors.Join(err, receiptFile.Close())
	if err != nil {
		return fail("prepare", err)
	}
	if err = os.Chmod(executable, 0700); err != nil {
		return fail("prepare", err)
	}
	// Go workers are self-contained: there is no Python requirements step, shell,
	// package install, postinstall hook or execution of self-declared commands.
	if err = ctx.Err(); err != nil {
		return fail("prepare", err)
	}
	if err = os.Rename(stage, target); err != nil {
		return fail("prepare", err)
	}
	if backup != "" {
		os.RemoveAll(backup)
	}
	warning := ""
	if req.RefreshBackup {
		if err := refreshBackup(root, req.PluginID, target); err != nil {
			warning = err.Error()
		}
	}
	executable = filepath.Join(target, filepath.FromSlash(release.Manifest.Entry))
	return Acquired{BackupWarning: warning, Manifest: release.Manifest, ExecutablePath: executable, SHA256: digest(data), HostContract: artifact.HostContract, RepositoryURL: req.RepositoryURL, Command: plugins.Command{Path: executable, Dir: target}}, nil
}

func selectArtifact(index Index, req Request) (Release, Artifact, error) {
	bad := func(s string) (Release, Artifact, error) { return Release{}, Artifact{}, errors.New(s) }
	if index.SchemaVersion != 1 || index.PluginID != req.PluginID {
		return bad("index schema or plugin identity mismatch")
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
	if _, err := httpURL(a.URL); err != nil {
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
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP status %d", resp.StatusCode)
	}
	if resp.ContentLength > limit {
		return nil, errors.New("download exceeds size bound")
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errors.New("download exceeds size bound")
	}
	return data, nil
}
func digest(b []byte) string    { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func validDigest(s string) bool { b, e := hex.DecodeString(s); return e == nil && len(b) == 32 }
